//! Runs one ACP prompt request. It saves the user message, makes model requests,
//! runs tools in call order, saves each complete assistant batch, and returns
//! one final ACP response.

use std::{fmt, io};

use agent_client_protocol::{
    Client, ConnectionTo, Error, Result,
    schema::v1::{
        PromptResponse, RequestPermissionOutcome, SessionId, SessionInfoUpdate, SessionUpdate,
        StopReason,
    },
};

use super::{convert, operations::PromptCancellation};
use crate::{
    openrouter,
    sessions::{
        AssistantBatch, AssistantMessage, SessionStore, SessionSummary, ToolCall, ToolOutcome,
        ToolResult, TranscriptEntry,
    },
    tools,
};

/// Model requests one prompt may make, including the first. Tools requested
/// by the final allowed request are not run, because no request remains to
/// read their results.
pub const MAX_MODEL_REQUESTS: usize = 8;

pub enum ToolPermissions {
    AutoApprove,
    Acp(ConnectionTo<Client>),
}

pub async fn run<F>(
    store: SessionStore,
    openrouter: openrouter::Client,
    session_id: SessionId,
    user_message: String,
    cancellation: PromptCancellation,
    send_update: F,
    permissions: ToolPermissions,
) -> Result<PromptResponse>
where
    F: FnMut(SessionUpdate) -> Result<()>,
{
    let mut run = PromptRun::open(
        store,
        openrouter,
        session_id,
        cancellation,
        send_update,
        permissions,
    )?;
    if !run.save_user_message(user_message)? {
        return Ok(PromptResponse::new(StopReason::Cancelled));
    }
    let outcome = run.run_model_loop().await;
    run.finish(outcome)
}

/// Why this prompt run stopped. Each variant requires a different final response.
enum PromptOutcome {
    Finished,
    Cancelled,
    ModelRequestLimit,
    TokenLimit,
    Refused,
    OpenRouter(io::Error),
    AcpUpdate(Error),
    Permission(Error),
    Storage(io::Error),
}

impl fmt::Display for PromptOutcome {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Finished => write!(f, "the answer finished"),
            Self::Cancelled => write!(f, "the prompt was cancelled"),
            Self::ModelRequestLimit => write!(f, "the model request limit was reached"),
            Self::TokenLimit => write!(f, "the model reached its token limit"),
            Self::Refused => write!(f, "the model refused"),
            Self::OpenRouter(error) => write!(f, "the model request failed: {error}"),
            Self::AcpUpdate(error) => write!(f, "sending an ACP update failed: {error}"),
            Self::Permission(error) => write!(f, "requesting shell permission failed: {error}"),
            Self::Storage(error) => write!(f, "saving the transcript failed: {error}"),
        }
    }
}

struct PromptRun<F> {
    store: SessionStore,
    openrouter: openrouter::Client,
    model: String,
    summary: SessionSummary,
    cancellation: PromptCancellation,
    send_update: F,
    permissions: ToolPermissions,
    /// Saved transcript, extended only after a database transaction succeeds.
    transcript: Vec<TranscriptEntry>,
    uncommitted_batch: Option<UncommittedAssistantBatch>,
    model_requests: usize,
}

/// A validated assistant message whose tool calls do not all have outcomes yet.
struct UncommittedAssistantBatch {
    message: AssistantMessage,
    outcomes: Vec<Option<ToolOutcome>>,
}

impl UncommittedAssistantBatch {
    fn new(message: AssistantMessage) -> Self {
        let outcomes = vec![None; message.tool_calls.len()];
        Self { message, outcomes }
    }

    fn record_outcome(&mut self, index: usize, outcome: ToolOutcome) -> ToolResult {
        let call = &self.message.tool_calls[index];
        let slot = &mut self.outcomes[index];
        assert!(
            slot.is_none(),
            "tool call {} already has a result",
            call.call_id
        );
        *slot = Some(outcome.clone());
        tool_result(call, outcome)
    }

    /// Fills every empty slot with `outcome` and returns the results filled.
    fn fill_empty_outcomes(&mut self, outcome: &ToolOutcome) -> Vec<ToolResult> {
        let mut filled = Vec::new();
        for (call, slot) in self.message.tool_calls.iter().zip(&mut self.outcomes) {
            if slot.is_none() {
                *slot = Some(outcome.clone());
                filled.push(tool_result(call, outcome.clone()));
            }
        }
        filled
    }

    fn complete(&self) -> AssistantBatch {
        let results = self
            .message
            .tool_calls
            .iter()
            .zip(&self.outcomes)
            .map(|(call, outcome)| {
                let outcome = outcome
                    .clone()
                    .expect("every tool call has an outcome before the batch is complete");
                tool_result(call, outcome)
            })
            .collect();
        AssistantBatch::new(self.message.clone(), results)
            .expect("a complete assistant batch pairs one result with each call")
    }
}

fn tool_result(call: &ToolCall, outcome: ToolOutcome) -> ToolResult {
    ToolResult {
        call_id: call.call_id.clone(),
        name: call.name.clone(),
        outcome,
    }
}

impl<F: FnMut(SessionUpdate) -> Result<()>> PromptRun<F> {
    fn open(
        store: SessionStore,
        openrouter: openrouter::Client,
        session_id: SessionId,
        cancellation: PromptCancellation,
        send_update: F,
        permissions: ToolPermissions,
    ) -> Result<Self> {
        let stored = store
            .read(&session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| Error::resource_not_found(Some(session_id.to_string())))?;
        Ok(Self {
            store,
            openrouter,
            model: stored.model().to_owned(),
            summary: stored.summary,
            cancellation,
            send_update,
            permissions,
            transcript: stored.transcript,
            uncommitted_batch: None,
            model_requests: 0,
        })
    }

    /// Saves the user message before any model request and announces the
    /// session update. `false` when cancellation was already observed, in
    /// which case nothing is written.
    fn save_user_message(&mut self, user_message: String) -> Result<bool> {
        if self.cancellation.is_cancelled() {
            return Ok(false);
        }
        let updated = self
            .store
            .append_user(&self.summary.id, &user_message)
            .map_err(Error::into_internal_error)?;
        self.transcript
            .push(TranscriptEntry::UserMessage(user_message));
        let mut info = SessionInfoUpdate::new().updated_at(updated.updated_at);
        if self.summary.title.is_none()
            && let Some(title) = updated.title
        {
            info = info.title(title);
        }
        (self.send_update)(SessionUpdate::SessionInfoUpdate(info))?;
        Ok(true)
    }

    async fn run_model_loop(&mut self) -> PromptOutcome {
        loop {
            if self.cancellation.is_cancelled() {
                return PromptOutcome::Cancelled;
            }
            let openrouter::Completion { message, stop } = match self.request_completion().await {
                Ok(completion) => completion,
                Err(exit) => return exit,
            };
            let calls = message.tool_calls.clone();
            self.uncommitted_batch = Some(UncommittedAssistantBatch::new(message));
            for call in &calls {
                if let Err(error) = (self.send_update)(convert::pending_tool_call(call)) {
                    return PromptOutcome::AcpUpdate(error);
                }
            }
            let ends_with = match stop {
                openrouter::Stop::Finished => Some(PromptOutcome::Finished),
                openrouter::Stop::TokenLimit => Some(PromptOutcome::TokenLimit),
                openrouter::Stop::Refused => Some(PromptOutcome::Refused),
                openrouter::Stop::ToolCalls => {
                    if self.model_requests >= MAX_MODEL_REQUESTS {
                        return PromptOutcome::ModelRequestLimit;
                    }
                    if let Err(exit) = self.execute(&calls).await {
                        return exit;
                    }
                    None
                }
            };
            if let Err(error) = self.commit() {
                return PromptOutcome::Storage(error);
            }
            if let Some(exit) = ends_with {
                return exit;
            }
        }
    }

    /// Makes one model request, forwards live text, and returns its validated
    /// completion.
    async fn request_completion(
        &mut self,
    ) -> std::result::Result<openrouter::Completion, PromptOutcome> {
        self.model_requests += 1;
        let mut stream = tokio::select! {
            biased;
            () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
            started = self.openrouter.stream_completion(&self.model, &self.transcript) => {
                started.map_err(PromptOutcome::OpenRouter)?
            }
        };
        loop {
            let item = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
                item = stream.next() => item.map_err(PromptOutcome::OpenRouter)?,
            };
            match item {
                Some(openrouter::StreamItem::TextDelta(text)) => {
                    (self.send_update)(convert::agent_message_chunk(&text))
                        .map_err(PromptOutcome::AcpUpdate)?;
                }
                Some(openrouter::StreamItem::ReasoningDelta(text)) => {
                    (self.send_update)(convert::agent_thought_chunk(&text))
                        .map_err(PromptOutcome::AcpUpdate)?;
                }
                Some(openrouter::StreamItem::Completion(completion)) => return Ok(completion),
                None => {
                    return Err(PromptOutcome::OpenRouter(io::Error::new(
                        io::ErrorKind::UnexpectedEof,
                        "OpenRouter stream ended without a completion",
                    )));
                }
            }
        }
    }

    /// Runs validated calls in order. Each outcome is recorded before its ACP
    /// update is sent, so an update failure does not erase completed work.
    async fn execute(&mut self, calls: &[ToolCall]) -> std::result::Result<(), PromptOutcome> {
        for (index, call) in calls.iter().enumerate() {
            if self.cancellation.is_cancelled() {
                return Err(PromptOutcome::Cancelled);
            }
            let approved = self.approve(call).await?;
            let outcome = if approved {
                (self.send_update)(convert::in_progress_tool_call_update(&call.call_id))
                    .map_err(PromptOutcome::AcpUpdate)?;
                // The dispatcher polls tools first, so a synchronous patch can finish
                // before it observes cancellation that arrived while sending the update.
                if self.cancellation.is_cancelled() {
                    return Err(PromptOutcome::Cancelled);
                }
                tools::execute(
                    &self.summary.workspace_path,
                    call,
                    self.cancellation.cancelled(),
                )
                .await
            } else {
                ToolOutcome::Failed("User denied permission to run this command.".to_owned())
            };
            let result = self
                .uncommitted_batch
                .as_mut()
                .expect("tools run against an uncommitted assistant batch")
                .record_outcome(index, outcome);
            (self.send_update)(convert::finished_tool_call_update(&result))
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(())
    }

    async fn approve(&self, call: &ToolCall) -> std::result::Result<bool, PromptOutcome> {
        let connection = match &self.permissions {
            ToolPermissions::AutoApprove => return Ok(true),
            ToolPermissions::Acp(connection) => connection,
        };
        if call.name != tools::SHELL {
            return Ok(true);
        }
        let response = tokio::select! {
            biased;
            () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
            response = connection.send_request(convert::shell_permission_request(
                self.summary.id.clone(), call, &self.summary.workspace_path,
            )).block_task() => response.map_err(PromptOutcome::Permission)?,
        };
        match response.outcome {
            RequestPermissionOutcome::Selected(selected) => match selected.option_id.0.as_ref() {
                "approve" => Ok(true),
                "deny" => Ok(false),
                id => Err(PromptOutcome::Permission(
                    Error::internal_error().data(format!("Unknown shell permission option: {id}")),
                )),
            },
            RequestPermissionOutcome::Cancelled => Err(PromptOutcome::Cancelled),
            _ => Err(PromptOutcome::Permission(
                Error::internal_error().data("Unsupported shell permission outcome"),
            )),
        }
    }

    /// Saves a complete assistant batch. The transcript changes only after the
    /// database transaction succeeds.
    fn commit(&mut self) -> io::Result<()> {
        let batch = self
            .uncommitted_batch
            .as_ref()
            .expect("commit needs an uncommitted assistant batch")
            .complete();
        self.store.append_batch(&self.summary.id, &batch)?;
        self.transcript
            .push(TranscriptEntry::AssistantMessage(batch.message));
        self.transcript
            .extend(batch.results.into_iter().map(TranscriptEntry::ToolResult));
        self.uncommitted_batch = None;
        Ok(())
    }

    /// Gives every unstarted call an explicit outcome, saves the batch, sends
    /// the remaining updates, and constructs the final ACP response.
    fn finish(&mut self, outcome: PromptOutcome) -> Result<PromptResponse> {
        let mut outcome = outcome;
        if let Some(batch) = self.uncommitted_batch.as_mut() {
            let placeholder = match &outcome {
                PromptOutcome::Cancelled => {
                    ToolOutcome::Cancelled("Cancelled before this tool was started.".to_owned())
                }
                PromptOutcome::ModelRequestLimit => ToolOutcome::Failed(format!(
                    "Not started: this prompt reached its limit of {MAX_MODEL_REQUESTS} model \
                     requests, so no request remained to read the result."
                )),
                PromptOutcome::AcpUpdate(_) => ToolOutcome::Failed(
                    "Not started: the client connection failed before this tool ran.".to_owned(),
                ),
                PromptOutcome::Permission(error) => ToolOutcome::Failed(format!(
                    "Not started: requesting shell permission failed: {error}"
                )),
                PromptOutcome::Storage(_) => ToolOutcome::Failed(
                    "Not started: the conversation could not be stored.".to_owned(),
                ),
                PromptOutcome::Finished
                | PromptOutcome::TokenLimit
                | PromptOutcome::Refused
                | PromptOutcome::OpenRouter(_) => {
                    unreachable!("{outcome} leaves no uncommitted batch")
                }
            };
            let unexecuted = batch.fill_empty_outcomes(&placeholder);
            if !matches!(outcome, PromptOutcome::Storage(_))
                && let Err(error) = self.commit()
            {
                outcome = PromptOutcome::Storage(io::Error::other(format!(
                    "{error}; the batch was being completed because {outcome}"
                )));
            }
            if !matches!(outcome, PromptOutcome::AcpUpdate(_)) {
                for result in &unexecuted {
                    if let Err(error) =
                        (self.send_update)(convert::finished_tool_call_update(result))
                    {
                        outcome = PromptOutcome::AcpUpdate(error);
                        break;
                    }
                }
            }
        }
        let stop_reason = match outcome {
            PromptOutcome::Finished => StopReason::EndTurn,
            PromptOutcome::Cancelled => StopReason::Cancelled,
            PromptOutcome::ModelRequestLimit => StopReason::MaxTurnRequests,
            PromptOutcome::TokenLimit => StopReason::MaxTokens,
            PromptOutcome::Refused => StopReason::Refusal,
            PromptOutcome::OpenRouter(error) | PromptOutcome::Storage(error) => {
                return Err(Error::into_internal_error(error));
            }
            PromptOutcome::AcpUpdate(error) | PromptOutcome::Permission(error) => {
                return Err(error);
            }
        };
        Ok(PromptResponse::new(stop_reason))
    }
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, fs, path::Path, rc::Rc};

    use agent_client_protocol::schema::v1::ToolCallStatus;

    use super::*;
    use serde_json::json;

    use crate::{
        openrouter::{
            DEFAULT_MODEL,
            fixture::{Reply, Server, delta, sse, text_reply, tool_reply},
        },
        tools::fixture::Workspace,
    };

    type Updates = Rc<RefCell<Vec<SessionUpdate>>>;

    struct Harness {
        server: Server,
        store: SessionStore,
        session_id: SessionId,
        cancellation: PromptCancellation,
        updates: Updates,
    }

    impl Harness {
        async fn new(replies: Vec<Reply>) -> Self {
            Self::in_workspace(replies, Path::new("/Users/kyle/projects/ox")).await
        }

        async fn in_workspace(replies: Vec<Reply>, workspace: &Path) -> Self {
            let store = SessionStore::in_memory();
            let session_id = store.create(workspace, DEFAULT_MODEL).unwrap().id;
            Self {
                server: Server::start(replies).await,
                store,
                session_id,
                cancellation: PromptCancellation::new(),
                updates: Rc::default(),
            }
        }

        /// Runs the prompt with an update closure that records every update
        /// and calls `on_update` before accepting it.
        async fn run(
            &self,
            input: &str,
            mut on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptResponse>, Vec<TranscriptEntry>) {
            let updates = self.updates.clone();
            let send_update = move |update: SessionUpdate| {
                updates.borrow_mut().push(update.clone());
                on_update(&update)
            };
            let mut prompt = PromptRun::open(
                self.store.clone(),
                self.server.client(),
                self.session_id.clone(),
                self.cancellation.clone(),
                send_update,
                ToolPermissions::AutoApprove,
            )
            .unwrap();
            assert!(prompt.save_user_message(input.to_owned()).unwrap());
            let exit = prompt.run_model_loop().await;
            (prompt.finish(exit), prompt.transcript)
        }

        fn stored(&self) -> Vec<TranscriptEntry> {
            self.store
                .read(&self.session_id)
                .unwrap()
                .unwrap()
                .transcript
        }

        fn updates(&self) -> Vec<SessionUpdate> {
            self.updates.borrow().clone()
        }
    }

    fn model() -> TranscriptEntry {
        TranscriptEntry::Model(DEFAULT_MODEL.to_owned())
    }

    fn user(text: &str) -> TranscriptEntry {
        TranscriptEntry::UserMessage(text.to_owned())
    }

    fn answer(text: &str) -> TranscriptEntry {
        TranscriptEntry::AssistantMessage(AssistantMessage {
            text: text.to_owned(),
            reasoning: String::new(),
            tool_calls: vec![],
            continuation_metadata: vec![],
        })
    }

    fn calls(calls: &[(&str, &str)]) -> TranscriptEntry {
        TranscriptEntry::AssistantMessage(AssistantMessage {
            text: String::new(),
            reasoning: String::new(),
            tool_calls: calls
                .iter()
                .map(|(id, location)| ToolCall {
                    call_id: (*id).to_owned(),
                    name: "get_weather".to_owned(),
                    arguments: serde_json::json!({ "location": location }).to_string(),
                })
                .collect(),
            continuation_metadata: vec![],
        })
    }

    fn weather(id: &str, location: &str) -> TranscriptEntry {
        TranscriptEntry::ToolResult(ToolResult {
            call_id: id.to_owned(),
            name: "get_weather".to_owned(),
            outcome: ToolOutcome::Completed(format!(
                "The weather in {location} is warm and sunny."
            )),
        })
    }

    fn terminal_update_for(update: &SessionUpdate, call_id: &str) -> bool {
        matches!(
            update,
            SessionUpdate::ToolCallUpdate(update)
                if update.tool_call_id.to_string() == call_id
                    && update.fields.status != Some(ToolCallStatus::InProgress)
        )
    }

    fn describe(update: &SessionUpdate) -> String {
        match update {
            SessionUpdate::SessionInfoUpdate(_) => "info".to_owned(),
            SessionUpdate::AgentMessageChunk(_) => "text".to_owned(),
            SessionUpdate::AgentThoughtChunk(_) => "reasoning".to_owned(),
            SessionUpdate::ToolCall(call) => format!("{} pending", call.tool_call_id),
            SessionUpdate::ToolCallUpdate(update) => format!(
                "{} {}",
                update.tool_call_id,
                match update.fields.status {
                    Some(ToolCallStatus::InProgress) => "running",
                    Some(ToolCallStatus::Completed) => "completed",
                    Some(ToolCallStatus::Failed) => "failed",
                    _ => "other",
                }
            ),
            _ => "other".to_owned(),
        }
    }

    #[tokio::test]
    async fn shell_failures_reach_the_next_model_request_and_replay() {
        let workspace = Workspace::new();
        let harness = Harness::in_workspace(
            vec![
                crate::openrouter::fixture::shell_reply(&[
                    ("printf problem >&2; exit 7", 5),
                    ("printf started; sleep 30", 1),
                ]),
                text_reply("Handled both failures."),
            ],
            &workspace.0,
        )
        .await;
        let (response, transcript) = harness.run("Run commands", |_| Ok(())).await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(transcript, harness.stored());
        let requests = harness.server.requests();
        let results: Vec<_> = transcript
            .iter()
            .filter_map(|entry| match entry {
                TranscriptEntry::ToolResult(result) => Some(result),
                _ => None,
            })
            .collect();
        assert_eq!(results.len(), 2);
        assert!(results[0].outcome.text().contains("Exit code: 7"));
        assert!(results[1].outcome.text().contains("Timed out"));
        let mut replay = Vec::new();
        convert::replay_transcript(&transcript, |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        for result in results {
            assert!(matches!(result.outcome, ToolOutcome::Failed(_)));
            assert!(
                requests[1]["messages"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|message| message["tool_call_id"] == result.call_id
                        && message["content"] == result.outcome.text())
            );
            assert!(replay.iter().any(|update| matches!(update, SessionUpdate::ToolCall(call)
                if call.title == "Run shell command" && call.raw_output == Some(json!(result.outcome.text())) && call.status == ToolCallStatus::Failed)));
        }
    }

    #[tokio::test]
    async fn running_shell_cancellation_is_saved_and_skips_later_calls() {
        let workspace = Workspace::new();
        let harness = Harness::in_workspace(
            vec![crate::openrouter::fixture::shell_reply(&[
                ("printf started; touch ready; sleep 30 & wait", 30),
                ("touch wrong", 5),
            ])],
            &workspace.0,
        )
        .await;
        let cancel = async {
            tokio::time::timeout(std::time::Duration::from_secs(5), async {
                while !workspace.0.join("ready").exists() {
                    tokio::time::sleep(std::time::Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            harness.cancellation.cancel();
        };
        let ((response, transcript), ()) =
            tokio::join!(harness.run("Run commands", |_| Ok(())), cancel);
        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(transcript, harness.stored());
        assert!(!workspace.0.join("wrong").exists());
        let results: Vec<_> = transcript
            .iter()
            .filter_map(|entry| match entry {
                TranscriptEntry::ToolResult(result) => Some(result),
                _ => None,
            })
            .collect();
        assert_eq!(results.len(), 2);
        assert!(
            matches!(&results[0].outcome, ToolOutcome::Cancelled(text) if text.contains("started") && text.contains("partial changes"))
        );
        assert!(
            matches!(&results[1].outcome, ToolOutcome::Cancelled(text) if text.contains("before this tool was started"))
        );
        assert_eq!(harness.server.requests().len(), 1);
        let mut replay = Vec::new();
        convert::replay_transcript(&transcript, |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        assert_eq!(replay.iter().filter(|update| matches!(update, SessionUpdate::ToolCall(call) if call.status == ToolCallStatus::Failed)).count(), 2);
    }

    #[tokio::test]
    async fn shell_result_survives_update_failure_or_late_cancellation() {
        for fail_update in [false, true] {
            let workspace = Workspace::new();
            let harness = Harness::in_workspace(
                vec![crate::openrouter::fixture::shell_reply(&[(
                    "printf saved > file",
                    5,
                )])],
                &workspace.0,
            )
            .await;
            let (response, transcript) = harness
                .run("Run command", |update| {
                    if terminal_update_for(update, "shell-0") {
                        if fail_update {
                            return Err(Error::internal_error().data("connection closed"));
                        }
                        harness.cancellation.cancel();
                    }
                    Ok(())
                })
                .await;
            if fail_update {
                assert!(response.is_err());
            } else {
                assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
            }
            assert_eq!(
                fs::read_to_string(workspace.0.join("file")).unwrap(),
                "saved"
            );
            assert_eq!(transcript, harness.stored());
            assert!(matches!(
                patch_result(&transcript).outcome,
                ToolOutcome::Completed(_)
            ));
        }
    }

    const PATCH: &str =
        "*** Begin Patch\n*** Add File: first\n+one\n*** Add File: second\n+two\n*** End Patch";

    #[tokio::test]
    async fn file_and_search_results_are_saved_and_sent_to_the_next_model_request() {
        let workspace = Workspace::new();
        fs::write(workspace.0.join("note.txt"), "first\nneedle\nlast\n").unwrap();
        let calls: Vec<_> = [
            ("read_file", json!({"path":"note.txt", "limit":1})),
            ("glob", json!({"pattern":"*.txt"})),
            ("grep", json!({"pattern":"needle"})),
        ]
        .into_iter()
        .enumerate()
        .map(|(index, (name, args))| {
            json!({
                "index":index, "id":format!("read-{index}"), "type":"function",
                "function":{"name":name,"arguments":args.to_string()}
            })
        })
        .collect();
        let reply = Reply::Stream(sse(&[delta(
            json!({"role":"assistant","tool_calls":calls}),
            Some("tool_calls"),
        )]));
        let harness = Harness::in_workspace(vec![reply, text_reply("Done.")], &workspace.0).await;
        let (response, transcript) = harness.run("Inspect the files", |_| Ok(())).await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(harness.stored(), transcript);
        let results: Vec<_> = transcript
            .iter()
            .filter_map(|entry| match entry {
                TranscriptEntry::ToolResult(result) => Some(result),
                _ => None,
            })
            .collect();
        assert_eq!(results.len(), 3);
        assert!(results[0].outcome.text().contains("offset=2"));
        assert!(results[1].outcome.text().contains("note.txt"));
        assert!(results[2].outcome.text().contains("note.txt:2:needle"));
        let requests = harness.server.requests();
        for result in &results {
            assert!(matches!(result.outcome, ToolOutcome::Completed(_)));
            assert!(
                requests[0]["tools"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|tool| tool["function"]["name"] == result.name)
            );
            assert!(
                requests[1]["messages"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .any(|message| message["tool_call_id"] == result.call_id
                        && message["content"] == result.outcome.text())
            );
        }
        let mut replay = Vec::new();
        convert::replay_transcript(&harness.stored(), |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        for result in results {
            assert!(replay.iter().any(|update| matches!(update, SessionUpdate::ToolCall(call) if call.raw_output == Some(json!(result.outcome.text())) && call.status == ToolCallStatus::Completed)));
        }
    }

    const APPLIED: &str = "Applied patch.\nA first\nA second";

    /// A prompt whose first reply is one `apply_patch` call adding `first` and
    /// `second`, followed by a plain answer.
    async fn patch_harness(workspace: &Path, finish_reason: &str) -> Harness {
        let call = Reply::Stream(sse(&[delta(
            json!({
                "role": "assistant",
                "tool_calls": [{
                    "index": 0,
                    "id": "patch-1",
                    "type": "function",
                    "function": {
                        "name": "apply_patch",
                        "arguments": json!({ "patch": PATCH }).to_string()
                    }
                }]
            }),
            Some(finish_reason),
        )]));
        Harness::in_workspace(vec![call, text_reply("Done.")], workspace).await
    }

    fn patch_result(transcript: &[TranscriptEntry]) -> &ToolResult {
        transcript
            .iter()
            .find_map(|entry| match entry {
                TranscriptEntry::ToolResult(result) => Some(result),
                _ => None,
            })
            .expect("the patch call left one tool result")
    }

    fn sent_patch_call(harness: &Harness) -> bool {
        harness.updates().iter().any(
            |update| matches!(update, SessionUpdate::ToolCall(call) if call.title == "Apply patch"),
        )
    }

    fn assert_replays_patch(harness: &Harness, outcome: &ToolOutcome, status: ToolCallStatus) {
        let mut replay = Vec::new();
        convert::replay_transcript(&harness.stored(), |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        assert!(replay.iter().any(|update| matches!(update,
            SessionUpdate::ToolCall(call) if call.title == "Apply patch"
                && call.raw_output == Some(json!(outcome.text()))
                && call.status == status
        )));
    }

    #[tokio::test]
    async fn a_patch_call_writes_its_files_and_saves_its_summary() {
        let workspace = Workspace::new();
        let harness = patch_harness(&workspace.0, "tool_calls").await;
        let (response, transcript) = harness.run("Apply the patch", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            fs::read_to_string(workspace.0.join("first")).unwrap(),
            "one\n"
        );
        assert_eq!(
            fs::read_to_string(workspace.0.join("second")).unwrap(),
            "two\n"
        );

        let outcome = &patch_result(&transcript).outcome;
        assert_eq!(*outcome, ToolOutcome::Completed(APPLIED.to_owned()));
        assert_eq!(harness.stored(), transcript);
        assert!(sent_patch_call(&harness));
        assert_replays_patch(&harness, outcome, ToolCallStatus::Completed);

        let requests = harness.server.requests();
        assert!(
            requests[0]["tools"]
                .as_array()
                .unwrap()
                .iter()
                .any(|tool| tool["function"]["name"] == "apply_patch")
        );
        assert_eq!(requests[1]["messages"][2]["content"], outcome.text());
    }

    #[tokio::test]
    async fn cancelling_before_execution_leaves_the_workspace_untouched() {
        let workspace = Workspace::new();
        let harness = patch_harness(&workspace.0, "tool_calls").await;
        let cancel = harness.cancellation.clone();
        let (response, transcript) = harness
            .run("Apply the patch", |update| {
                if matches!(update, SessionUpdate::ToolCallUpdate(update)
                    if update.fields.status == Some(ToolCallStatus::InProgress))
                {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
        let outcome = &patch_result(&transcript).outcome;
        assert!(matches!(outcome, ToolOutcome::Cancelled(_)));
        assert_eq!(harness.stored(), transcript);
        assert!(sent_patch_call(&harness));
        assert_replays_patch(&harness, outcome, ToolCallStatus::Failed);
    }

    #[tokio::test]
    async fn cancelling_after_the_result_keeps_the_applied_patch() {
        let workspace = Workspace::new();
        let harness = patch_harness(&workspace.0, "tool_calls").await;
        let cancel = harness.cancellation.clone();
        let (response, transcript) = harness
            .run("Apply the patch", |update| {
                if terminal_update_for(update, "patch-1") {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(
            fs::read_to_string(workspace.0.join("first")).unwrap(),
            "one\n"
        );
        let outcome = &patch_result(&transcript).outcome;
        assert_eq!(*outcome, ToolOutcome::Completed(APPLIED.to_owned()));
        assert_eq!(harness.stored(), transcript);
        assert_replays_patch(&harness, outcome, ToolCallStatus::Completed);
    }

    #[tokio::test]
    async fn an_update_failure_after_a_patch_still_saves_its_result() {
        let workspace = Workspace::new();
        let harness = patch_harness(&workspace.0, "tool_calls").await;
        let (response, transcript) = harness
            .run("Apply the patch", |update| {
                if terminal_update_for(update, "patch-1") {
                    return Err(Error::internal_error().data("connection closed"));
                }
                Ok(())
            })
            .await;

        assert!(response.is_err());
        assert_eq!(
            fs::read_to_string(workspace.0.join("second")).unwrap(),
            "two\n"
        );
        assert_eq!(
            patch_result(&transcript).outcome,
            ToolOutcome::Completed(APPLIED.to_owned())
        );
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn an_invalid_completion_runs_no_patch() {
        let workspace = Workspace::new();
        let harness = patch_harness(&workspace.0, "unknown").await;
        let (response, transcript) = harness.run("Apply the patch", |_| Ok(())).await;

        assert!(response.is_err());
        assert_eq!(transcript, vec![model(), user("Apply the patch")]);
        assert_eq!(harness.stored(), transcript);
        assert_eq!(fs::read_dir(&workspace.0).unwrap().count(), 0);
    }

    #[tokio::test]
    async fn a_text_answer_is_saved_in_the_transcript() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;

        let (response, transcript) = harness.run("Hi", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            transcript,
            vec![model(), user("Hi"), answer("Hello there.")]
        );
        assert_eq!(harness.stored(), transcript);
        let updates = harness.updates();
        assert!(matches!(
            &updates[0],
            SessionUpdate::SessionInfoUpdate(info) if info.title.contains_value(&"Hi".to_owned())
        ));
        assert_eq!(
            updates.iter().map(describe).collect::<Vec<_>>(),
            vec!["info", "text"]
        );
    }

    #[tokio::test]
    async fn several_tool_calls_in_one_message_get_ordered_results() {
        let harness = Harness::new(vec![
            tool_reply(&[("call-1", "Chicago"), ("call-2", "Denver")]),
            text_reply("Both sunny."),
        ])
        .await;

        let (response, transcript) = harness.run("Weather?", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            transcript,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                weather("call-2", "Denver"),
                answer("Both sunny."),
            ]
        );
        assert_eq!(harness.stored(), transcript);
        assert_eq!(
            harness.updates().iter().map(describe).collect::<Vec<_>>(),
            vec![
                "info",
                "call-1 pending",
                "call-2 pending",
                "call-1 running",
                "call-1 completed",
                "call-2 running",
                "call-2 completed",
                "text",
            ]
        );
        let second_request = &harness.server.requests()[1];
        assert_eq!(second_request["messages"].as_array().unwrap().len(), 4);
        assert_eq!(second_request["messages"][3]["tool_call_id"], "call-2");
    }

    #[tokio::test]
    async fn cancellation_during_tools_keeps_completed_results_and_cancels_the_rest() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "Chicago"),
            ("call-2", "Denver"),
        ])])
        .await;
        let cancel = harness.cancellation.clone();

        let (response, transcript) = harness
            .run("Weather?", |update| {
                if terminal_update_for(update, "call-1") {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(
            transcript,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                TranscriptEntry::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: "get_weather".to_owned(),
                    outcome: ToolOutcome::Cancelled(
                        "Cancelled before this tool was started.".to_owned()
                    ),
                }),
            ]
        );
        assert_eq!(harness.stored(), transcript);
        assert_eq!(
            harness.updates().iter().map(describe).collect::<Vec<_>>(),
            vec![
                "info",
                "call-1 pending",
                "call-2 pending",
                "call-1 running",
                "call-1 completed",
                "call-2 failed",
            ]
        );
    }

    #[tokio::test]
    async fn cancellation_during_the_openrouter_stream_discards_provisional_output() {
        let partial = format!(
            "data: {}\n\n",
            delta(
                serde_json::json!({ "role": "assistant", "content": "Hel" }),
                None
            )
        );
        let harness = Harness::new(vec![Reply::Hang(partial)]).await;
        let cancel = harness.cancellation.clone();

        let (response, transcript) = harness
            .run("Hi", |update| {
                if matches!(update, SessionUpdate::AgentMessageChunk(_)) {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(transcript, vec![model(), user("Hi")]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn a_failed_batch_append_leaves_the_transcript_unchanged() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;
        harness.store.with_connection(|connection| {
            connection
                .execute_batch(
                    "CREATE TRIGGER refuse BEFORE INSERT ON events
                     WHEN NEW.kind = 'assistant_message'
                     BEGIN SELECT RAISE(ABORT, 'disk full'); END;",
                )
                .unwrap()
        });

        let (response, transcript) = harness.run("Hi", |_| Ok(())).await;

        let error = response.unwrap_err();
        assert!(
            error.to_string().contains("disk full") || format!("{error:?}").contains("disk full")
        );
        assert_eq!(transcript, vec![model(), user("Hi")]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn update_failure_after_an_observed_result_still_commits_the_batch() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "Chicago"),
            ("call-2", "Denver"),
        ])])
        .await;

        let (response, transcript) = harness
            .run("Weather?", |update| {
                if terminal_update_for(update, "call-1") {
                    return Err(Error::internal_error().data("connection closed"));
                }
                Ok(())
            })
            .await;

        assert!(response.is_err());
        assert_eq!(
            transcript,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                TranscriptEntry::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: "get_weather".to_owned(),
                    outcome: ToolOutcome::Failed(
                        "Not started: the client connection failed before this tool ran."
                            .to_owned()
                    ),
                }),
            ]
        );
        assert_eq!(harness.stored(), transcript);
        assert_eq!(
            harness.updates().iter().map(describe).collect::<Vec<_>>(),
            vec![
                "info",
                "call-1 pending",
                "call-2 pending",
                "call-1 running",
                "call-1 completed",
            ],
            "nothing more is attempted after sending an update fails"
        );
    }

    #[tokio::test]
    async fn tools_requested_by_the_final_allowed_request_are_not_run() {
        let replies = (0..MAX_MODEL_REQUESTS)
            .map(|index| tool_reply(&[(&format!("call-{index}"), "Chicago")]))
            .collect();
        let harness = Harness::new(replies).await;

        let (response, transcript) = harness.run("Weather?", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::MaxTurnRequests);
        assert_eq!(harness.server.requests().len(), MAX_MODEL_REQUESTS);
        assert_eq!(transcript.len(), 2 + 2 * MAX_MODEL_REQUESTS);
        let completed = transcript
            .iter()
            .filter(|entry| {
                matches!(
                    entry,
                    TranscriptEntry::ToolResult(ToolResult {
                        outcome: ToolOutcome::Completed(_),
                        ..
                    })
                )
            })
            .count();
        assert_eq!(completed, MAX_MODEL_REQUESTS - 1);
        assert!(matches!(
            transcript.last(),
            Some(TranscriptEntry::ToolResult(ToolResult { outcome: ToolOutcome::Failed(reason), .. }))
                if reason.starts_with("Not started: this prompt reached its limit")
        ));
        assert_eq!(harness.stored(), transcript);
        let last = format!("call-{} failed", MAX_MODEL_REQUESTS - 1);
        assert_eq!(
            harness.updates().last().map(describe).as_deref(),
            Some(last.as_str())
        );
    }
}
