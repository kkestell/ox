//! The agent loop: one model request at a time, tools run in call order, and
//! every exit after the user message is saved settles through one path that
//! commits the accepted batch and shapes the response.

use std::{fmt, io};

use agent_client_protocol::{
    Error, Result,
    schema::v1::{PromptResponse, SessionId, SessionInfoUpdate, SessionUpdate, StopReason},
};

use super::{convert, operations::PromptCancellation};
use crate::{
    model::{ModelClient, ModelCompletion, ModelEvent, ModelStop},
    sessions::{
        AssistantBatch, AssistantMessage, SessionStore, SessionSummary, ToolCall, ToolOutcome,
        ToolResult, TranscriptEvent,
    },
    tools,
};

/// Model requests one prompt may make, including the first. Tools requested
/// by the final allowed request are not run, because no request remains to
/// read their results.
pub const MAX_MODEL_CALLS: usize = 8;

pub async fn run<F>(
    store: SessionStore,
    client: ModelClient,
    session_id: SessionId,
    input: String,
    cancellation: PromptCancellation,
    deliver: F,
) -> Result<PromptResponse>
where
    F: FnMut(SessionUpdate) -> Result<()>,
{
    let mut prompt = Prompt::open(store, client, session_id, cancellation, deliver)?;
    if !prompt.accept(input)? {
        return Ok(PromptResponse::new(StopReason::Cancelled));
    }
    let exit = prompt.converse().await;
    prompt.settle(exit)
}

/// Why the loop stopped. Each variant settles differently.
enum Exit {
    Finished,
    Cancelled,
    ModelCallLimit,
    TokenLimit,
    Refused,
    Provider(io::Error),
    Delivery(Error),
    Storage(io::Error),
}

impl fmt::Display for Exit {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Finished => write!(f, "the answer finished"),
            Self::Cancelled => write!(f, "the prompt was cancelled"),
            Self::ModelCallLimit => write!(f, "the model request limit was reached"),
            Self::TokenLimit => write!(f, "the model reached its token limit"),
            Self::Refused => write!(f, "the model refused"),
            Self::Provider(error) => write!(f, "the model request failed: {error}"),
            Self::Delivery(error) => write!(f, "delivery to the client failed: {error}"),
            Self::Storage(error) => write!(f, "storing the conversation failed: {error}"),
        }
    }
}

struct Prompt<F> {
    store: SessionStore,
    client: ModelClient,
    model: String,
    summary: SessionSummary,
    cancellation: PromptCancellation,
    deliver: F,
    /// Committed history plus the accepted user message; extended only
    /// after a batch commits.
    history: Vec<TranscriptEvent>,
    pending: Option<PendingBatch>,
    model_calls: usize,
}

/// An accepted assistant message whose tool calls have not all reached a
/// terminal result. Each slot fills once; the closed batch lists results in
/// call order.
struct PendingBatch {
    message: AssistantMessage,
    outcomes: Vec<Option<ToolOutcome>>,
}

impl PendingBatch {
    fn new(message: AssistantMessage) -> Self {
        let outcomes = vec![None; message.tool_calls.len()];
        Self { message, outcomes }
    }

    fn record(&mut self, index: usize, outcome: ToolOutcome) -> ToolResult {
        let call = &self.message.tool_calls[index];
        let slot = &mut self.outcomes[index];
        assert!(
            slot.is_none(),
            "tool call {} already has a result",
            call.call_id
        );
        *slot = Some(outcome.clone());
        result(call, outcome)
    }

    /// Fills every empty slot with `outcome` and returns the results filled.
    fn fill(&mut self, outcome: &ToolOutcome) -> Vec<ToolResult> {
        let mut filled = Vec::new();
        for (call, slot) in self.message.tool_calls.iter().zip(&mut self.outcomes) {
            if slot.is_none() {
                *slot = Some(outcome.clone());
                filled.push(result(call, outcome.clone()));
            }
        }
        filled
    }

    fn closed(&self) -> AssistantBatch {
        let results = self
            .message
            .tool_calls
            .iter()
            .zip(&self.outcomes)
            .map(|(call, outcome)| {
                let outcome = outcome
                    .clone()
                    .expect("every tool call has a result before the batch closes");
                result(call, outcome)
            })
            .collect();
        AssistantBatch::new(self.message.clone(), results)
            .expect("a pending batch pairs one result with each call")
    }
}

fn result(call: &ToolCall, outcome: ToolOutcome) -> ToolResult {
    ToolResult {
        call_id: call.call_id.clone(),
        name: call.name.clone(),
        outcome,
    }
}

impl<F: FnMut(SessionUpdate) -> Result<()>> Prompt<F> {
    fn open(
        store: SessionStore,
        client: ModelClient,
        session_id: SessionId,
        cancellation: PromptCancellation,
        deliver: F,
    ) -> Result<Self> {
        let stored = store
            .read(&session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| Error::resource_not_found(Some(session_id.to_string())))?;
        Ok(Self {
            store,
            client,
            model: stored.model().to_owned(),
            summary: stored.summary,
            cancellation,
            deliver,
            history: stored.transcript,
            pending: None,
            model_calls: 0,
        })
    }

    /// Saves the user message before any model request and announces the
    /// session update. `false` when cancellation was already observed, in
    /// which case nothing is written.
    fn accept(&mut self, input: String) -> Result<bool> {
        if self.cancellation.is_cancelled() {
            return Ok(false);
        }
        let updated = self
            .store
            .append_user(&self.summary.id, &input)
            .map_err(Error::into_internal_error)?;
        self.history.push(TranscriptEvent::UserMessage(input));
        let mut info = SessionInfoUpdate::new().updated_at(updated.updated_at);
        if self.summary.title.is_none()
            && let Some(title) = updated.title
        {
            info = info.title(title);
        }
        (self.deliver)(SessionUpdate::SessionInfoUpdate(info))?;
        Ok(true)
    }

    async fn converse(&mut self) -> Exit {
        loop {
            if self.cancellation.is_cancelled() {
                return Exit::Cancelled;
            }
            let ModelCompletion { message, stop } = match self.request().await {
                Ok(completion) => completion,
                Err(exit) => return exit,
            };
            let calls = message.tool_calls.clone();
            self.pending = Some(PendingBatch::new(message));
            for call in &calls {
                if let Err(error) = (self.deliver)(convert::tool_call_pending(call)) {
                    return Exit::Delivery(error);
                }
            }
            let ends_with = match stop {
                ModelStop::Finished => Some(Exit::Finished),
                ModelStop::TokenLimit => Some(Exit::TokenLimit),
                ModelStop::Refused => Some(Exit::Refused),
                ModelStop::ToolCalls => {
                    if self.model_calls >= MAX_MODEL_CALLS {
                        return Exit::ModelCallLimit;
                    }
                    if let Err(exit) = self.execute(&calls).await {
                        return exit;
                    }
                    None
                }
            };
            if let Err(error) = self.commit() {
                return Exit::Storage(error);
            }
            if let Some(exit) = ends_with {
                return exit;
            }
        }
    }

    /// One model request: forwards provisional deltas and returns the single
    /// accepted completion.
    async fn request(&mut self) -> std::result::Result<ModelCompletion, Exit> {
        self.model_calls += 1;
        let mut request = tokio::select! {
            biased;
            () = self.cancellation.cancelled() => return Err(Exit::Cancelled),
            started = self.client.complete(&self.model, &self.history) => {
                started.map_err(Exit::Provider)?
            }
        };
        loop {
            let event = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return Err(Exit::Cancelled),
                event = request.next() => event.map_err(Exit::Provider)?,
            };
            match event {
                Some(ModelEvent::TextDelta(text)) => {
                    (self.deliver)(convert::agent_text(&text)).map_err(Exit::Delivery)?;
                }
                Some(ModelEvent::ReasoningDelta(text)) => {
                    (self.deliver)(convert::agent_reasoning(&text)).map_err(Exit::Delivery)?;
                }
                Some(ModelEvent::Completed(completion)) => return Ok(completion),
                None => {
                    return Err(Exit::Provider(io::Error::new(
                        io::ErrorKind::UnexpectedEof,
                        "model stream ended without a completion",
                    )));
                }
            }
        }
    }

    /// Runs the accepted calls in order. Each result enters the pending batch
    /// before its update is attempted, so a failed update loses nothing.
    async fn execute(&mut self, calls: &[ToolCall]) -> std::result::Result<(), Exit> {
        for (index, call) in calls.iter().enumerate() {
            if self.cancellation.is_cancelled() {
                return Err(Exit::Cancelled);
            }
            (self.deliver)(convert::tool_call_in_progress(&call.call_id))
                .map_err(Exit::Delivery)?;
            let outcome = tokio::select! {
                biased;
                outcome = tools::execute(call) => outcome,
                () = self.cancellation.cancelled() => ToolOutcome::Cancelled(
                    "Cancelled while this tool was running; no result was observed.".to_owned(),
                ),
            };
            let result = self
                .pending
                .as_mut()
                .expect("tools run against a pending batch")
                .record(index, outcome);
            (self.deliver)(convert::tool_result(&result)).map_err(Exit::Delivery)?;
        }
        Ok(())
    }

    /// Commits the pending batch. History advances and the pending state
    /// clears only after the transaction succeeds.
    fn commit(&mut self) -> io::Result<()> {
        let batch = self
            .pending
            .as_ref()
            .expect("commit needs a pending batch")
            .closed();
        self.store.append_batch(&self.summary.id, &batch)?;
        self.history
            .push(TranscriptEvent::AssistantMessage(batch.message));
        self.history
            .extend(batch.results.into_iter().map(TranscriptEvent::ToolResult));
        self.pending = None;
        Ok(())
    }

    /// Closes any accepted batch with explicit outcomes for calls that never
    /// ran, commits it, sends their terminal updates, and shapes the response.
    fn settle(&mut self, exit: Exit) -> Result<PromptResponse> {
        let mut exit = exit;
        if let Some(pending) = self.pending.as_mut() {
            let placeholder = match &exit {
                Exit::Cancelled => {
                    ToolOutcome::Cancelled("Cancelled before this tool was started.".to_owned())
                }
                Exit::ModelCallLimit => ToolOutcome::Failed(format!(
                    "Not started: this prompt reached its limit of {MAX_MODEL_CALLS} model \
                     requests, so no request remained to read the result."
                )),
                Exit::Delivery(_) => ToolOutcome::Failed(
                    "Not started: the client connection failed before this tool ran.".to_owned(),
                ),
                Exit::Storage(_) => ToolOutcome::Failed(
                    "Not started: the conversation could not be stored.".to_owned(),
                ),
                Exit::Finished | Exit::TokenLimit | Exit::Refused | Exit::Provider(_) => {
                    unreachable!("{exit} leaves no pending batch")
                }
            };
            let unexecuted = pending.fill(&placeholder);
            if !matches!(exit, Exit::Storage(_))
                && let Err(error) = self.commit()
            {
                exit = Exit::Storage(io::Error::other(format!(
                    "{error}; the batch was being settled because {exit}"
                )));
            }
            if !matches!(exit, Exit::Delivery(_)) {
                for result in &unexecuted {
                    if let Err(error) = (self.deliver)(convert::tool_result(result)) {
                        exit = Exit::Delivery(error);
                        break;
                    }
                }
            }
        }
        let stop_reason = match exit {
            Exit::Finished => StopReason::EndTurn,
            Exit::Cancelled => StopReason::Cancelled,
            Exit::ModelCallLimit => StopReason::MaxTurnRequests,
            Exit::TokenLimit => StopReason::MaxTokens,
            Exit::Refused => StopReason::Refusal,
            Exit::Provider(error) | Exit::Storage(error) => {
                return Err(Error::into_internal_error(error));
            }
            Exit::Delivery(error) => return Err(error),
        };
        Ok(PromptResponse::new(stop_reason))
    }
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, path::Path, rc::Rc};

    use agent_client_protocol::schema::v1::ToolCallStatus;

    use super::*;
    use crate::model::{
        MODEL,
        fixture::{Reply, Server, delta, text_reply, tool_reply},
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
            let store = SessionStore::in_memory();
            let session_id = store
                .create(Path::new("/Users/kyle/projects/ox"), MODEL)
                .unwrap()
                .id;
            Self {
                server: Server::start(replies).await,
                store,
                session_id,
                cancellation: PromptCancellation::new(),
                updates: Rc::default(),
            }
        }

        /// Runs the prompt with a delivery closure that records every update
        /// and calls `on_update` before accepting it.
        async fn run(
            &self,
            input: &str,
            mut on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptResponse>, Vec<TranscriptEvent>) {
            let updates = self.updates.clone();
            let deliver = move |update: SessionUpdate| {
                updates.borrow_mut().push(update.clone());
                on_update(&update)
            };
            let mut prompt = Prompt::open(
                self.store.clone(),
                self.server.client(),
                self.session_id.clone(),
                self.cancellation.clone(),
                deliver,
            )
            .unwrap();
            assert!(prompt.accept(input.to_owned()).unwrap());
            let exit = prompt.converse().await;
            (prompt.settle(exit), prompt.history)
        }

        fn stored(&self) -> Vec<TranscriptEvent> {
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

    fn model() -> TranscriptEvent {
        TranscriptEvent::Model(MODEL.to_owned())
    }

    fn user(text: &str) -> TranscriptEvent {
        TranscriptEvent::UserMessage(text.to_owned())
    }

    fn answer(text: &str) -> TranscriptEvent {
        TranscriptEvent::AssistantMessage(AssistantMessage {
            text: text.to_owned(),
            reasoning: String::new(),
            tool_calls: vec![],
            reasoning_details: vec![],
        })
    }

    fn calls(calls: &[(&str, &str)]) -> TranscriptEvent {
        TranscriptEvent::AssistantMessage(AssistantMessage {
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
            reasoning_details: vec![],
        })
    }

    fn weather(id: &str, location: &str) -> TranscriptEvent {
        TranscriptEvent::ToolResult(ToolResult {
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
    async fn a_text_answer_is_committed_and_advances_history() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;

        let (response, history) = harness.run("Hi", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(history, vec![model(), user("Hi"), answer("Hello there.")]);
        assert_eq!(harness.stored(), history);
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

        let (response, history) = harness.run("Weather?", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            history,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                weather("call-2", "Denver"),
                answer("Both sunny."),
            ]
        );
        assert_eq!(harness.stored(), history);
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
    async fn cancellation_during_tools_keeps_observed_results_and_closes_the_rest() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "Chicago"),
            ("call-2", "Denver"),
        ])])
        .await;
        let cancel = harness.cancellation.clone();

        let (response, history) = harness
            .run("Weather?", |update| {
                if terminal_update_for(update, "call-1") {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(
            history,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                TranscriptEvent::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: "get_weather".to_owned(),
                    outcome: ToolOutcome::Cancelled(
                        "Cancelled before this tool was started.".to_owned()
                    ),
                }),
            ]
        );
        assert_eq!(harness.stored(), history);
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
    async fn cancellation_during_the_model_stream_discards_provisional_output() {
        let partial = format!(
            "data: {}\n\n",
            delta(
                serde_json::json!({ "role": "assistant", "content": "Hel" }),
                None
            )
        );
        let harness = Harness::new(vec![Reply::Hang(partial)]).await;
        let cancel = harness.cancellation.clone();

        let (response, history) = harness
            .run("Hi", |update| {
                if matches!(update, SessionUpdate::AgentMessageChunk(_)) {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        assert_eq!(history, vec![model(), user("Hi")]);
        assert_eq!(harness.stored(), history);
    }

    #[tokio::test]
    async fn a_failed_batch_append_leaves_accepted_history_unchanged() {
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

        let (response, history) = harness.run("Hi", |_| Ok(())).await;

        let error = response.unwrap_err();
        assert!(
            error.to_string().contains("disk full") || format!("{error:?}").contains("disk full")
        );
        assert_eq!(history, vec![model(), user("Hi")]);
        assert_eq!(harness.stored(), history);
    }

    #[tokio::test]
    async fn delivery_failure_after_an_observed_result_still_commits_the_batch() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "Chicago"),
            ("call-2", "Denver"),
        ])])
        .await;

        let (response, history) = harness
            .run("Weather?", |update| {
                if terminal_update_for(update, "call-1") {
                    return Err(Error::internal_error().data("connection closed"));
                }
                Ok(())
            })
            .await;

        assert!(response.is_err());
        assert_eq!(
            history,
            vec![
                model(),
                user("Weather?"),
                calls(&[("call-1", "Chicago"), ("call-2", "Denver")]),
                weather("call-1", "Chicago"),
                TranscriptEvent::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: "get_weather".to_owned(),
                    outcome: ToolOutcome::Failed(
                        "Not started: the client connection failed before this tool ran."
                            .to_owned()
                    ),
                }),
            ]
        );
        assert_eq!(harness.stored(), history);
        assert_eq!(
            harness.updates().iter().map(describe).collect::<Vec<_>>(),
            vec![
                "info",
                "call-1 pending",
                "call-2 pending",
                "call-1 running",
                "call-1 completed",
            ],
            "nothing more is attempted after delivery fails"
        );
    }

    #[tokio::test]
    async fn tools_requested_by_the_final_allowed_request_are_not_run() {
        let replies = (0..MAX_MODEL_CALLS)
            .map(|index| tool_reply(&[(&format!("call-{index}"), "Chicago")]))
            .collect();
        let harness = Harness::new(replies).await;

        let (response, history) = harness.run("Weather?", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::MaxTurnRequests);
        assert_eq!(harness.server.requests().len(), MAX_MODEL_CALLS);
        assert_eq!(history.len(), 2 + 2 * MAX_MODEL_CALLS);
        let completed = history
            .iter()
            .filter(|event| {
                matches!(
                    event,
                    TranscriptEvent::ToolResult(ToolResult {
                        outcome: ToolOutcome::Completed(_),
                        ..
                    })
                )
            })
            .count();
        assert_eq!(completed, MAX_MODEL_CALLS - 1);
        assert!(matches!(
            history.last(),
            Some(TranscriptEvent::ToolResult(ToolResult { outcome: ToolOutcome::Failed(reason), .. }))
                if reason.starts_with("Not started: this prompt reached its limit")
        ));
        assert_eq!(harness.stored(), history);
        let last = format!("call-{} failed", MAX_MODEL_CALLS - 1);
        assert_eq!(
            harness.updates().last().map(describe).as_deref(),
            Some(last.as_str())
        );
    }
}
