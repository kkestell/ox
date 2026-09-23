//! Runs one ACP prompt request. It saves the user message or skill invocation,
//! makes model requests, runs tools in call order, saves each complete
//! assistant batch, runs the invoked skill's `before_stop` hook on each
//! finished answer, and returns the final stop reason and answer.

use std::{fmt, future::Future, io};

use agent_client_protocol::{
    Client, ConnectionTo, Error, Result,
    schema::v1::{
        ConfigOptionUpdate, RequestPermissionOutcome, SessionId, SessionInfoUpdate, SessionUpdate,
        StopReason,
    },
};

use super::{convert, operations::PromptCancellation};
use crate::{
    compaction, hooks, openrouter,
    sessions::{
        AssistantBatch, AssistantMessage, HookDecision, HookFeedback, SessionMode, SessionSettings,
        SessionSettingsChange, SessionStore, SessionSummary, SkillInvocation, ToolCall,
        ToolOutcome, ToolResult, TranscriptEntry,
    },
    tools,
};

/// The most `continue` decisions one prompt run accepts from its hook.
const MAX_HOOK_CONTINUATIONS: usize = 50;

/// Transport for Ask mode's ACP permission request. The captured session mode,
/// not this transport, decides whether shell approval is required.
pub enum PermissionTransport {
    None,
    Acp(ConnectionTo<Client>),
}

pub(super) struct PromptInput {
    pub session_id: SessionId,
    /// The user message or skill invocation that starts the turn.
    pub turn_start: TranscriptEntry,
    /// The invoked skill's `before_stop` hook.
    pub hook: Option<hooks::Hook>,
    pub selected_settings: Option<SessionSettings>,
    /// The complete system prompt captured when the session became active.
    pub system_prompt: String,
}

/// How a prompt run ended.
#[derive(Debug)]
pub struct PromptOutput {
    pub stop_reason: StopReason,
    /// The text of the assistant message committed with the finished stop
    /// that ended the run. `None` unless the stop reason is `EndTurn`.
    pub answer: Option<String>,
}

pub fn run<F>(
    store: SessionStore,
    openrouter: openrouter::Client,
    input: PromptInput,
    cancellation: PromptCancellation,
    send_update: F,
    permission_transport: PermissionTransport,
) -> Result<impl Future<Output = Result<PromptOutput>>>
where
    F: FnMut(SessionUpdate) -> Result<()>,
{
    let mut run = PromptRun::open(
        store,
        openrouter,
        &input,
        cancellation,
        send_update,
        permission_transport,
    )?;
    let saved = run.save_turn_start(input.turn_start)?;
    Ok(async move {
        if !saved {
            return Ok(PromptOutput {
                stop_reason: StopReason::Cancelled,
                answer: None,
            });
        }
        let outcome = run.run_model_loop().await;
        run.finish(outcome)
    })
}

/// Why this prompt run stopped. Each variant requires a different final response.
enum PromptOutcome {
    Finished,
    Cancelled,
    TokenLimit,
    Refused,
    OpenRouter(io::Error),
    AcpUpdate(Error),
    Permission(Error),
    Storage(io::Error),
    Hook(io::Error),
}

impl fmt::Display for PromptOutcome {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Finished => write!(f, "the answer finished"),
            Self::Cancelled => write!(f, "the prompt was cancelled"),
            Self::TokenLimit => write!(f, "the model reached its token limit"),
            Self::Refused => write!(f, "the model refused"),
            Self::OpenRouter(error) => write!(f, "the model request failed: {error}"),
            Self::AcpUpdate(error) => write!(f, "sending an ACP update failed: {error}"),
            Self::Permission(error) => write!(f, "requesting shell permission failed: {error}"),
            Self::Storage(error) => write!(f, "saving the transcript failed: {error}"),
            Self::Hook(error) => write!(f, "{error}"),
        }
    }
}

struct PromptRun<F> {
    store: SessionStore,
    openrouter: openrouter::Client,
    settings: SessionSettings,
    settings_change: SessionSettingsChange,
    /// Sent before the transcript on every model request of this run.
    system_prompt: String,
    summary: SessionSummary,
    cancellation: PromptCancellation,
    send_update: F,
    permission_transport: PermissionTransport,
    /// Saved transcript, extended only after a database transaction succeeds.
    transcript: Vec<TranscriptEntry>,
    uncommitted_batch: Option<UncommittedAssistantBatch>,
    hook: Option<hooks::Hook>,
    hook_continuations: usize,
    /// The text of the latest assistant message committed with a finished stop.
    answer: Option<String>,
}

/// A validated assistant message whose tool calls do not all have results yet.
struct UncommittedAssistantBatch {
    message: AssistantMessage,
    /// Sequential execution makes observed results a prefix of the tool calls.
    results: Vec<ToolResult>,
}

impl UncommittedAssistantBatch {
    fn new(message: AssistantMessage) -> Self {
        Self {
            message,
            results: Vec::new(),
        }
    }

    fn append_remaining_results(&mut self, outcome: &ToolOutcome) -> Vec<ToolResult> {
        let results = self.message.tool_calls[self.results.len()..]
            .iter()
            .map(|call| tool_result(call, outcome.clone()))
            .collect::<Vec<_>>();
        self.results.extend(results.iter().cloned());
        results
    }

    fn complete(&self) -> AssistantBatch {
        AssistantBatch::new(self.message.clone(), self.results.clone())
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
        input: &PromptInput,
        cancellation: PromptCancellation,
        send_update: F,
        permission_transport: PermissionTransport,
    ) -> Result<Self> {
        let session_id = &input.session_id;
        let stored = store
            .read(session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| Error::resource_not_found(Some(session_id.to_string())))?;
        let saved_settings = stored.saved_settings(&super::default_settings());
        let mut settings = input
            .selected_settings
            .clone()
            .unwrap_or_else(|| saved_settings.clone());
        if !stored.transcript.is_empty() {
            settings.model.clone_from(&saved_settings.model);
        }
        if openrouter::catalog_model(&settings.model).is_none() {
            return Err(Error::into_internal_error(io::Error::new(
                io::ErrorKind::InvalidData,
                format!(
                    "session model {} is not in the model catalog",
                    settings.model
                ),
            )));
        }
        let settings_change = SessionSettingsChange {
            model: stored.transcript.is_empty().then(|| settings.model.clone()),
            effort: (saved_settings.effort != settings.effort).then_some(settings.effort),
            mode: (saved_settings.mode != settings.mode).then_some(settings.mode),
        };
        Ok(Self {
            store,
            openrouter,
            settings,
            settings_change,
            system_prompt: input.system_prompt.clone(),
            summary: stored.summary,
            cancellation,
            send_update,
            permission_transport,
            transcript: stored.transcript,
            uncommitted_batch: None,
            hook: input.hook.clone(),
            hook_continuations: 0,
            answer: None,
        })
    }

    /// Saves the user message or skill invocation before any model request and
    /// announces the session update. `false` when cancellation was already
    /// observed, in which case nothing is written.
    fn save_turn_start(&mut self, turn_start: TranscriptEntry) -> Result<bool> {
        if self.cancellation.is_cancelled() {
            return Ok(false);
        }
        let mut prospective = self.transcript.clone();
        if let Some(model) = &self.settings_change.model {
            prospective.push(TranscriptEntry::Model(model.clone()));
        }
        if let Some(effort) = self.settings_change.effort {
            prospective.push(TranscriptEntry::Effort(effort));
        }
        if let Some(mode) = self.settings_change.mode {
            prospective.push(TranscriptEntry::Mode(mode));
        }
        prospective.push(turn_start.clone());
        if !compaction::input_fits(
            &self.settings.model,
            self.settings.effort,
            &self.system_prompt,
            &prospective,
        )
        .map_err(Error::into_internal_error)?
        {
            return Err(Error::invalid_params().data("prompt exceeds the model context limit"));
        }
        let updated = self
            .store
            .append_user(&self.summary.id, &self.settings_change, &turn_start)
            .map_err(Error::into_internal_error)?;
        let locks_model = self.settings_change.model.is_some();
        if let Some(model) = self.settings_change.model.take() {
            self.transcript.push(TranscriptEntry::Model(model));
        }
        if let Some(effort) = self.settings_change.effort.take() {
            self.transcript.push(TranscriptEntry::Effort(effort));
        }
        if let Some(mode) = self.settings_change.mode.take() {
            self.transcript.push(TranscriptEntry::Mode(mode));
        }
        if locks_model {
            (self.send_update)(SessionUpdate::ConfigOptionUpdate(ConfigOptionUpdate::new(
                super::config_options(&self.settings, true),
            )))?;
        }
        self.transcript.push(turn_start);
        let mut info = SessionInfoUpdate::new().updated_at(updated.updated_at);
        if self.summary.session_title.is_none()
            && let Some(session_title) = updated.session_title
        {
            info = info.title(session_title);
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
                Err(outcome) => return outcome,
            };
            let calls = message.tool_calls.clone();
            let text = message.text.clone();
            self.uncommitted_batch = Some(UncommittedAssistantBatch::new(message));
            for call in &calls {
                if let Err(error) = (self.send_update)(convert::pending_tool_call(call)) {
                    return PromptOutcome::AcpUpdate(error);
                }
            }
            let outcome = match stop {
                openrouter::Stop::Finished => Some(PromptOutcome::Finished),
                openrouter::Stop::TokenLimit => Some(PromptOutcome::TokenLimit),
                openrouter::Stop::Refused => Some(PromptOutcome::Refused),
                openrouter::Stop::ToolCalls => {
                    if let Err(outcome) = self.execute(&calls).await {
                        return outcome;
                    }
                    None
                }
            };
            if let Err(error) = self.commit() {
                return PromptOutcome::Storage(error);
            }
            match outcome {
                Some(PromptOutcome::Finished) => {
                    self.answer = Some(text);
                    if self.hook.is_some() {
                        match self.run_hook().await {
                            Ok(HookDecision::Continue) => continue,
                            Ok(HookDecision::Stop) => {}
                            Err(outcome) => return outcome,
                        }
                    }
                    return PromptOutcome::Finished;
                }
                Some(outcome) => return outcome,
                None => {}
            }
        }
    }

    /// Runs the invoked skill's hook on the answer just committed and saves
    /// its feedback. The hook is shown to the ACP client as an execute tool
    /// call; it is not a model tool call.
    async fn run_hook(&mut self) -> std::result::Result<HookDecision, PromptOutcome> {
        if self.cancellation.is_cancelled() {
            return Err(PromptOutcome::Cancelled);
        }
        let hook = self
            .hook
            .clone()
            .expect("a hook runs only for a skill that declares one");
        let call_id = convert::hook_call_id();
        let pending = convert::pending_hook_call(&call_id, &self.hook_invocation().name);
        (self.send_update)(pending).map_err(PromptOutcome::AcpUpdate)?;
        (self.send_update)(convert::in_progress_tool_call_update(&call_id))
            .map_err(PromptOutcome::AcpUpdate)?;
        let result = self.run_and_save_hook(&hook).await;
        let update = match &result {
            Ok(feedback) => convert::finished_hook_call_update(&call_id, Ok(&feedback.message)),
            Err(outcome) => convert::finished_hook_call_update(&call_id, Err(&outcome.to_string())),
        };
        (self.send_update)(update).map_err(PromptOutcome::AcpUpdate)?;
        result.map(|feedback| feedback.decision)
    }

    fn hook_invocation(&self) -> &SkillInvocation {
        match self.transcript.iter().rev().find(|entry| {
            matches!(
                entry,
                TranscriptEntry::UserMessage(_) | TranscriptEntry::SkillInvocation(_)
            )
        }) {
            Some(TranscriptEntry::SkillInvocation(invocation)) => invocation,
            _ => panic!("a hook runs only in a turn started by a skill invocation"),
        }
    }

    /// Runs the hook and saves its feedback. Nothing is saved for a failed hook.
    async fn run_and_save_hook(
        &mut self,
        hook: &hooks::Hook,
    ) -> std::result::Result<HookFeedback, PromptOutcome> {
        let invocation = self.hook_invocation();
        let feedback = hooks::run(
            hook,
            invocation,
            &self.summary.workspace_path,
            &self.settings.model,
            self.settings.effort,
            self.answer
                .as_deref()
                .expect("a before_stop hook runs on a committed answer"),
            self.cancellation.cancelled(),
        )
        .await
        .map_err(|error| {
            if error.kind() == io::ErrorKind::Interrupted {
                PromptOutcome::Cancelled
            } else {
                PromptOutcome::Hook(error)
            }
        })?;
        if feedback.decision == HookDecision::Continue
            && self.hook_continuations == MAX_HOOK_CONTINUATIONS
        {
            return Err(PromptOutcome::Hook(io::Error::other(format!(
                "{} before_stop hook asked to continue more than {MAX_HOOK_CONTINUATIONS} times",
                invocation.name
            ))));
        }
        let mut prospective = self.transcript.clone();
        prospective.push(TranscriptEntry::HookFeedback(feedback.clone()));
        if !compaction::input_fits(
            &self.settings.model,
            self.settings.effort,
            &self.system_prompt,
            &prospective,
        )
        .map_err(PromptOutcome::OpenRouter)?
        {
            return Err(PromptOutcome::Hook(io::Error::other(format!(
                "{} before_stop hook feedback exceeds the model context limit",
                invocation.name
            ))));
        }
        self.store
            .append_hook_feedback(&self.summary.id, &feedback)
            .map_err(PromptOutcome::Storage)?;
        self.transcript
            .push(TranscriptEntry::HookFeedback(feedback.clone()));
        if feedback.decision == HookDecision::Continue {
            self.hook_continuations += 1;
        }
        Ok(feedback)
    }

    /// Makes one model request, forwards provisional output, and returns its
    /// validated completion.
    async fn request_completion(
        &mut self,
    ) -> std::result::Result<openrouter::Completion, PromptOutcome> {
        let (admission, trigger, _) =
            compaction::budget(&self.settings.model).map_err(PromptOutcome::OpenRouter)?;
        let estimate = compaction::request_estimate(
            &self.settings.model,
            self.settings.effort,
            &self.system_prompt,
            &self.transcript,
        )
        .map_err(PromptOutcome::OpenRouter)?;
        if estimate >= trigger && compaction::has_candidate(&self.transcript) {
            self.compact().await?;
        }
        if compaction::request_estimate(
            &self.settings.model,
            self.settings.effort,
            &self.system_prompt,
            &self.transcript,
        )
        .map_err(PromptOutcome::OpenRouter)?
            > admission
        {
            return Err(PromptOutcome::OpenRouter(compaction::context_error()));
        }
        let mut retried = false;
        loop {
            let projected = compaction::projection(&self.transcript);
            let started = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
                started = self.openrouter.stream_completion(
                    &self.settings.model,
                    self.settings.effort,
                    &self.system_prompt,
                    &projected,
                ) => started,
            };
            let mut stream = match started {
                Ok(stream) => stream,
                Err(error) if openrouter::is_input_context_overflow(&error) && !retried => {
                    retried = true;
                    if self.compact().await? {
                        continue;
                    }
                    return Err(PromptOutcome::OpenRouter(compaction::context_error()));
                }
                Err(error) => return Err(PromptOutcome::OpenRouter(error)),
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
    }

    async fn compact(&mut self) -> std::result::Result<bool, PromptOutcome> {
        compaction::compact(
            &self.store,
            &self.openrouter,
            &self.cancellation,
            &self.summary.id,
            &self.settings.model,
            self.settings.effort,
            &self.system_prompt,
            &mut self.transcript,
        )
        .await
        .map_err(|error| {
            if error.kind() == io::ErrorKind::Interrupted {
                PromptOutcome::Cancelled
            } else {
                PromptOutcome::OpenRouter(error)
            }
        })
    }

    /// Runs validated calls in order. Each outcome enters the uncommitted batch
    /// before its ACP update is sent, so an update failure does not erase
    /// completed work.
    async fn execute(&mut self, calls: &[ToolCall]) -> std::result::Result<(), PromptOutcome> {
        for call in calls {
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
            let result = tool_result(call, outcome);
            self.uncommitted_batch
                .as_mut()
                .expect("tools run against an uncommitted assistant batch")
                .results
                .push(result.clone());
            (self.send_update)(convert::finished_tool_call_update(&result))
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(())
    }

    async fn approve(&self, call: &ToolCall) -> std::result::Result<bool, PromptOutcome> {
        if call.name != tools::SHELL {
            return Ok(true);
        }
        if self.settings.mode == SessionMode::Auto {
            return Ok(true);
        }
        let PermissionTransport::Acp(connection) = &self.permission_transport else {
            panic!("Ask mode requires an ACP permission-request connection");
        };
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
    /// the remaining updates, and returns the final stop reason.
    fn finish(&mut self, outcome: PromptOutcome) -> Result<PromptOutput> {
        let mut outcome = outcome;
        if let Some(batch) = self.uncommitted_batch.as_mut() {
            let placeholder = match &outcome {
                PromptOutcome::Cancelled => {
                    ToolOutcome::Cancelled("Cancelled before this tool was started.".to_owned())
                }
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
                | PromptOutcome::OpenRouter(_)
                | PromptOutcome::Hook(_) => {
                    unreachable!("{outcome} leaves no uncommitted batch")
                }
            };
            let unexecuted = batch.append_remaining_results(&placeholder);
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
            PromptOutcome::TokenLimit => StopReason::MaxTokens,
            PromptOutcome::Refused => StopReason::Refusal,
            PromptOutcome::OpenRouter(error)
            | PromptOutcome::Storage(error)
            | PromptOutcome::Hook(error) => {
                return Err(Error::into_internal_error(error));
            }
            PromptOutcome::AcpUpdate(error) | PromptOutcome::Permission(error) => {
                return Err(error);
            }
        };
        Ok(PromptOutput {
            answer: self
                .answer
                .take()
                .filter(|_| stop_reason == StopReason::EndTurn),
            stop_reason,
        })
    }
}

#[cfg(test)]
mod tests {
    use std::{cell::RefCell, fs, rc::Rc};

    use agent_client_protocol::schema::v1::ToolCallStatus;

    use super::*;
    use serde_json::json;

    use crate::{
        openrouter::{
            DEFAULT_MODEL, MODEL_CATALOG,
            fixture::{Reply, Server, delta, sse, text_reply, tool_reply},
        },
        sessions::{EffortLevel, SkillInvocation},
        system_prompt,
        tools::fixture::Workspace,
    };

    type Updates = Rc<RefCell<Vec<SessionUpdate>>>;

    struct Harness {
        server: Server,
        store: SessionStore,
        session_id: SessionId,
        cancellation: PromptCancellation,
        updates: Updates,
        workspace: Workspace,
    }

    impl Harness {
        async fn new(replies: Vec<Reply>) -> Self {
            let workspace = Workspace::new();
            let store = SessionStore::in_memory();
            let session_id = store.create(&workspace.0).unwrap().id;
            Self {
                server: Server::start(replies).await,
                store,
                session_id,
                cancellation: PromptCancellation::new(),
                updates: Rc::default(),
                workspace,
            }
        }

        /// Runs the prompt with an update closure that records every update
        /// and calls `on_update` before returning its result.
        async fn run(
            &self,
            input: &str,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            self.run_with_settings(
                input,
                SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default)
                    .with_mode(SessionMode::Auto),
                on_update,
            )
            .await
        }

        async fn run_with_settings(
            &self,
            input: &str,
            settings: SessionSettings,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            self.run_with_selection(input, Some(settings), on_update)
                .await
        }

        async fn run_with_selection(
            &self,
            input: &str,
            selected_settings: Option<SessionSettings>,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            self.run_turn(user(input), None, selected_settings, on_update)
                .await
        }

        async fn run_turn(
            &self,
            turn_start: TranscriptEntry,
            hook: Option<hooks::Hook>,
            selected_settings: Option<SessionSettings>,
            mut on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            let updates = self.updates.clone();
            let send_update = move |update: SessionUpdate| {
                updates.borrow_mut().push(update.clone());
                on_update(&update)
            };
            let prompt = run(
                self.store.clone(),
                self.server.client(),
                PromptInput {
                    session_id: self.session_id.clone(),
                    turn_start,
                    hook,
                    selected_settings,
                    system_prompt: system_prompt::for_workspace(&self.workspace.0).unwrap(),
                },
                self.cancellation.clone(),
                send_update,
                PermissionTransport::None,
            )
            .unwrap();
            let response = prompt.await;
            (response, self.stored())
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

    fn auto() -> TranscriptEntry {
        TranscriptEntry::Mode(SessionMode::Auto)
    }

    fn user(text: &str) -> TranscriptEntry {
        TranscriptEntry::UserMessage(text.to_owned())
    }

    fn invocation(arguments: &str) -> TranscriptEntry {
        TranscriptEntry::SkillInvocation(SkillInvocation {
            name: "goal".to_owned(),
            arguments: arguments.to_owned(),
            instructions: "Work until the hook stops you.".to_owned(),
        })
    }

    fn feedback(decision: HookDecision, message: &str) -> TranscriptEntry {
        TranscriptEntry::HookFeedback(HookFeedback {
            skill: "goal".to_owned(),
            decision,
            message: message.to_owned(),
        })
    }

    /// A hook command that appends each input line to `inputs`, continues
    /// once, and then stops.
    const CONTINUE_THEN_STOP: &str = r#"cat >> inputs; echo >> inputs; if [ -e continued ]; then echo '{"decision":"stop","message":"Objective met."}'; else touch continued; echo '{"decision":"continue","message":"Two tests still fail."}'; fi"#;

    impl Harness {
        /// Runs `/goal <arguments>` with a hook running `command` in the
        /// workspace.
        async fn run_goal(
            &self,
            command: &str,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            let hook = hooks::Hook {
                command: command.to_owned(),
                directory: self.workspace.0.clone(),
            };
            self.run_turn(
                invocation("Pass the tests."),
                Some(hook),
                Some(
                    SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default)
                        .with_mode(SessionMode::Auto),
                ),
                on_update,
            )
            .await
        }
    }

    /// Update descriptions with each generated hook call ID shortened to `hook`.
    fn described_updates(harness: &Harness) -> Vec<String> {
        harness
            .updates()
            .iter()
            .map(|update| {
                let description = describe(update);
                match description.split_once(' ') {
                    Some((id, status)) if id.starts_with("hook-") => format!("hook {status}"),
                    _ => description,
                }
            })
            .collect()
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
                .map(|(id, command)| ToolCall {
                    call_id: (*id).to_owned(),
                    name: tools::SHELL.to_owned(),
                    arguments: serde_json::json!({ "command": command }).to_string(),
                })
                .collect(),
            continuation_metadata: vec![],
        })
    }

    /// The completed result of a shell call that printed `text`.
    fn printed(id: &str, text: &str) -> TranscriptEntry {
        TranscriptEntry::ToolResult(ToolResult {
            call_id: id.to_owned(),
            name: tools::SHELL.to_owned(),
            outcome: ToolOutcome::Completed(format!(
                "Exit code: 0\n\nstdout:\n{text}\n\nstderr:\n(empty)"
            )),
        })
    }

    fn finished_tool_call_update_for(update: &SessionUpdate, call_id: &str) -> bool {
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
            SessionUpdate::ConfigOptionUpdate(_) => "config".to_owned(),
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

    const PATCH: &str =
        "*** Begin Patch\n*** Add File: first\n+one\n*** Add File: second\n+two\n*** End Patch";

    const APPLIED: &str = "Applied patch.\nAdded first\nAdded second";

    /// A prompt whose first reply is one `apply_patch` call adding `first` and
    /// `second`, followed by a plain answer.
    async fn patch_harness(finish_reason: &str) -> Harness {
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
        Harness::new(vec![call, text_reply("Done.")]).await
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
            |update| matches!(update, SessionUpdate::ToolCall(call) if call.title == "Apply patch to 2 files"),
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
            SessionUpdate::ToolCall(call) if call.title == "Apply patch to 2 files"
                && call.raw_output == Some(json!(outcome.text()))
                && call.status == status
        )));
    }

    #[tokio::test]
    async fn a_patch_call_writes_its_files_and_saves_its_summary() {
        let harness = patch_harness("tool_calls").await;
        let (response, transcript) = harness.run("Apply the patch", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            fs::read_to_string(harness.workspace.0.join("first")).unwrap(),
            "one\n"
        );
        assert_eq!(
            fs::read_to_string(harness.workspace.0.join("second")).unwrap(),
            "two\n"
        );

        let outcome = &patch_result(&transcript).outcome;
        assert_eq!(*outcome, ToolOutcome::Completed(APPLIED.to_owned()));
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn cancelling_before_execution_leaves_the_workspace_untouched() {
        let harness = patch_harness("tool_calls").await;
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
        assert_eq!(fs::read_dir(&harness.workspace.0).unwrap().count(), 0);
        let outcome = &patch_result(&transcript).outcome;
        assert!(matches!(outcome, ToolOutcome::Cancelled(_)));
        assert_eq!(harness.stored(), transcript);
        assert!(sent_patch_call(&harness));
        assert_replays_patch(&harness, outcome, ToolCallStatus::Failed);
    }

    #[tokio::test]
    async fn a_completed_patch_survives_a_later_interruption() {
        for fail_update in [false, true] {
            let harness = patch_harness("tool_calls").await;
            let cancel = harness.cancellation.clone();
            let (response, transcript) = harness
                .run("Apply the patch", |update| {
                    if finished_tool_call_update_for(update, "patch-1") {
                        if fail_update {
                            return Err(Error::internal_error().data("connection closed"));
                        }
                        cancel.cancel();
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
                fs::read_to_string(harness.workspace.0.join("first")).unwrap(),
                "one\n"
            );
            assert_eq!(
                patch_result(&transcript).outcome,
                ToolOutcome::Completed(APPLIED.to_owned())
            );
            assert_eq!(harness.stored(), transcript);
        }
    }

    #[tokio::test]
    async fn an_invalid_completion_runs_no_patch() {
        let harness = patch_harness("unknown").await;
        let (response, transcript) = harness.run("Apply the patch", |_| Ok(())).await;

        assert!(response.is_err());
        assert_eq!(transcript, vec![model(), auto(), user("Apply the patch")]);
        assert_eq!(harness.stored(), transcript);
        assert_eq!(fs::read_dir(&harness.workspace.0).unwrap().count(), 0);
    }

    #[tokio::test]
    async fn a_text_answer_is_saved_in_the_transcript() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;

        let (response, transcript) = harness.run_with_selection("Hi", None, |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        let request = &harness.server.requests()[0];
        assert_eq!(request["model"], DEFAULT_MODEL);
        assert!(request.get("reasoning").is_none());
        assert_eq!(
            transcript,
            vec![model(), user("Hi"), answer("Hello there.")]
        );
        assert_eq!(harness.stored(), transcript);
        let updates = harness.updates();
        let SessionUpdate::ConfigOptionUpdate(config) = &updates[0] else {
            panic!("the first prompt locks the model selector");
        };
        let config = serde_json::to_value(config).unwrap();
        assert_eq!(
            config["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
        assert!(matches!(
            &updates[1],
            SessionUpdate::SessionInfoUpdate(info) if info.title.contains_value(&"Hi".to_owned())
        ));
        assert_eq!(
            updates.iter().map(describe).collect::<Vec<_>>(),
            vec!["config", "info", "text"]
        );
    }

    #[tokio::test]
    async fn a_before_stop_hook_continues_stops_and_fails_without_saving() {
        let harness = Harness::new(vec![text_reply("First try."), text_reply("Second try.")]).await;
        let (response, transcript) = harness.run_goal(CONTINUE_THEN_STOP, |_| Ok(())).await;
        let output = response.unwrap();
        assert_eq!(output.stop_reason, StopReason::EndTurn);
        assert_eq!(output.answer.as_deref(), Some("Second try."));
        assert_eq!(
            transcript,
            vec![
                model(),
                auto(),
                invocation("Pass the tests."),
                answer("First try."),
                feedback(HookDecision::Continue, "Two tests still fail."),
                answer("Second try."),
                feedback(HookDecision::Stop, "Objective met."),
            ]
        );
        assert_eq!(harness.stored(), transcript);
        let requests = harness.server.requests();
        assert_eq!(
            requests[0]["messages"][1]["content"],
            "Skill /goal invoked.\n\nInstructions:\nWork until the hook stops you.\n\nArguments:\nPass the tests."
        );
        assert_eq!(
            requests[1]["messages"][3]["content"],
            "Feedback from the goal before_stop hook:\nTwo tests still fail."
        );
        let inputs = fs::read_to_string(harness.workspace.0.join("inputs")).unwrap();
        let inputs: Vec<serde_json::Value> = inputs
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        assert_eq!(inputs.len(), 2);
        assert_eq!(inputs[0]["skill"], "goal");
        assert_eq!(inputs[0]["arguments"], "Pass the tests.");
        assert_eq!(
            inputs[0]["workspace"],
            harness.workspace.0.to_str().unwrap()
        );
        assert_eq!(inputs[0]["model"], DEFAULT_MODEL);
        assert_eq!(inputs[0]["effort"], "default");
        assert_eq!(inputs[0]["answer"], "First try.");
        assert_eq!(inputs[1]["answer"], "Second try.");
        assert_eq!(
            described_updates(&harness),
            [
                "config",
                "info",
                "text",
                "hook pending",
                "hook running",
                "hook completed",
                "text",
                "hook pending",
                "hook running",
                "hook completed",
            ]
        );

        for (command, expected) in [
            (
                "echo broken >&2; exit 3",
                "goal before_stop hook exited with code 3\nstderr:\nbroken",
            ),
            (
                "echo 'not json'",
                "goal before_stop hook output is not one decision object",
            ),
            (
                r#"echo '{"decision":"maybe","message":"Unsure."}'"#,
                "unknown variant `maybe`",
            ),
            (
                r#"echo '{"decision":"stop","message":" "}'"#,
                "goal before_stop hook message is blank",
            ),
            (
                "head -c 20000 /dev/zero | tr '\\0' x",
                "goal before_stop hook output exceeds 16 KiB",
            ),
            (
                "printf '\\377'",
                "goal before_stop hook output is not UTF-8",
            ),
        ] {
            let harness = Harness::new(vec![text_reply("Done.")]).await;
            let (response, transcript) = harness.run_goal(command, |_| Ok(())).await;
            let error = response.unwrap_err();
            let data = error
                .data
                .as_ref()
                .and_then(serde_json::Value::as_str)
                .unwrap();
            assert!(data.contains(expected), "{command}: {data}");
            assert_eq!(
                transcript,
                vec![
                    model(),
                    auto(),
                    invocation("Pass the tests."),
                    answer("Done.")
                ],
                "{command}: nothing is saved for a failed hook"
            );
            assert_eq!(described_updates(&harness).last().unwrap(), "hook failed");
        }

        let harness = Harness::new(
            (0..=MAX_HOOK_CONTINUATIONS)
                .map(|index| text_reply(&format!("Try {index}.")))
                .collect(),
        )
        .await;
        let (response, transcript) = harness
            .run_goal(
                r#"echo '{"decision":"continue","message":"Again."}'"#,
                |_| Ok(()),
            )
            .await;
        let error = response.unwrap_err();
        assert!(format!("{error:?}").contains("asked to continue more than 50 times"));
        assert_eq!(harness.server.requests().len(), MAX_HOOK_CONTINUATIONS + 1);
        assert_eq!(
            transcript
                .iter()
                .filter(|entry| matches!(entry, TranscriptEntry::HookFeedback(_)))
                .count(),
            MAX_HOOK_CONTINUATIONS
        );
        assert_eq!(transcript.last(), Some(&answer("Try 50.")));

        let hang = format!(
            "data: {}\n\n",
            delta(json!({ "role": "assistant", "content": "Hel" }), None)
        );
        for (reply, stop_reason, hook_runs) in [
            (
                Reply::Stream(sse(&[delta(json!({}), Some("content_filter"))])),
                StopReason::Refusal,
                false,
            ),
            (
                Reply::Stream(sse(&[delta(json!({ "content": "Cut" }), Some("length"))])),
                StopReason::MaxTokens,
                false,
            ),
            (Reply::Hang(hang), StopReason::Cancelled, false),
            (text_reply("Done."), StopReason::Cancelled, true),
        ] {
            let harness = Harness::new(vec![reply]).await;
            let cancel = harness.cancellation.clone();
            let ran = harness.workspace.0.join("ran");
            // Cancels once the hook has started.
            let watcher = tokio::spawn({
                let cancel = cancel.clone();
                let ran = ran.clone();
                async move {
                    while !ran.exists() {
                        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
                    }
                    cancel.cancel();
                }
            });
            let (response, transcript) = harness
                .run_goal("touch ran; sleep 30", |update| {
                    if !hook_runs
                        && stop_reason == StopReason::Cancelled
                        && matches!(update, SessionUpdate::AgentMessageChunk(_))
                    {
                        cancel.cancel();
                    }
                    Ok(())
                })
                .await;
            watcher.abort();
            assert_eq!(response.unwrap().stop_reason, stop_reason);
            assert_eq!(ran.exists(), hook_runs, "{stop_reason:?}");
            assert!(
                transcript
                    .iter()
                    .all(|entry| !matches!(entry, TranscriptEntry::HookFeedback(_)))
            );
        }
    }

    #[tokio::test]
    async fn oversized_input_is_rejected_before_save_and_a_valid_prompt_can_follow() {
        let harness = Harness::new(vec![text_reply("Accepted")]).await;
        let oversized = "x".repeat(3_000_000);
        let rejected = run(
            harness.store.clone(),
            harness.server.client(),
            PromptInput {
                session_id: harness.session_id.clone(),
                turn_start: user(&oversized),
                hook: None,
                selected_settings: None,
                system_prompt: "system".to_owned(),
            },
            PromptCancellation::new(),
            |_| Ok(()),
            PermissionTransport::None,
        );
        assert!(rejected.is_err());
        assert!(harness.stored().is_empty());
        let valid = "v".repeat(2_300_000);
        let (response, transcript) = harness.run(&valid, |_| Ok(())).await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(transcript.len(), 4);
        assert_eq!(
            harness.server.requests().len(),
            1,
            "a valid request above the trigger runs when no prefix can be cut"
        );

        let directory =
            std::env::temp_dir().join(format!("ox-compaction-{}", uuid::Uuid::new_v4()));
        let database = directory.join("ox.db");
        let store = SessionStore::open(&database).unwrap();
        let id = store.create(&harness.workspace.0).unwrap().id;
        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: Some(DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("unanswered ".repeat(180_000)),
            )
            .unwrap();
        drop(store);
        let reopened = SessionStore::open(&database).unwrap();
        let before = reopened.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![text_reply("Accepted after reload")]).await;
        let input = |user_message: String| PromptInput {
            session_id: id.clone(),
            turn_start: user(&user_message),
            hook: None,
            selected_settings: None,
            system_prompt: "system".to_owned(),
        };
        assert!(
            run(
                reopened.clone(),
                server.client(),
                input("b".repeat(1_100_000)),
                PromptCancellation::new(),
                |_| Ok(()),
                PermissionTransport::None
            )
            .is_err()
        );
        assert_eq!(reopened.read(&id).unwrap().unwrap().transcript, before);
        let accepted = run(
            reopened.clone(),
            server.client(),
            input("small".to_owned()),
            PromptCancellation::new(),
            |_| Ok(()),
            PermissionTransport::None,
        )
        .unwrap();
        assert_eq!(accepted.await.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(server.requests().len(), 1);
        drop(reopened);
        fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn automatic_compaction_precedes_the_next_model_request() {
        let harness = Harness::new(vec![
            text_reply("Older work summarized."),
            text_reply("Done"),
        ])
        .await;
        let old = "x".repeat(2_300_000);
        harness
            .store
            .append_user(
                &harness.session_id,
                &SessionSettingsChange {
                    model: Some(DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage(old.clone()),
            )
            .unwrap();
        let TranscriptEntry::AssistantMessage(message) = answer("Earlier answer") else {
            unreachable!()
        };
        harness
            .store
            .append_batch(
                &harness.session_id,
                &AssistantBatch::new(message, vec![]).unwrap(),
            )
            .unwrap();
        let (response, transcript) = harness
            .run_with_selection("next request", None, |_| Ok(()))
            .await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        let requests = harness.server.requests();
        assert_eq!(requests.len(), 2);
        assert!(requests[0].get("tools").is_none());
        assert_eq!(
            requests[1]["messages"][1]["content"],
            "Compaction summary of earlier conversation:\nOlder work summarized."
        );
        assert_eq!(requests[1]["messages"][2]["content"], "next request");
        assert!(matches!(
            &transcript[4],
            TranscriptEntry::CompactionCheckpoint(_)
        ));
        assert_eq!(harness.stored(), transcript);

        let command = format!("printf %s {}", "z".repeat(1000));
        let between = Harness::new(vec![
            tool_reply(&[("call-1", &command)]),
            text_reply("The active request and tool result were summarized."),
            text_reply("Done"),
        ])
        .await;
        let system = system_prompt::for_workspace(&between.workspace.0).unwrap();
        let (_, trigger, _) = compaction::budget(DEFAULT_MODEL).unwrap();
        let base = "x".repeat(2_000_000);
        let prospective = vec![
            model(),
            auto(),
            user(&base),
            answer("Earlier answer"),
            user("next request"),
        ];
        let base_estimate = compaction::request_estimate(
            DEFAULT_MODEL,
            EffortLevel::Default,
            &system,
            &prospective,
        )
        .unwrap();
        let old = "x".repeat(2_000_000 + (trigger - base_estimate - 50) * 3);
        between
            .store
            .append_user(
                &between.session_id,
                &SessionSettingsChange {
                    model: Some(DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: Some(SessionMode::Auto),
                },
                &TranscriptEntry::UserMessage(old.clone()),
            )
            .unwrap();
        let TranscriptEntry::AssistantMessage(message) = answer("Earlier answer") else {
            unreachable!()
        };
        between
            .store
            .append_batch(
                &between.session_id,
                &AssistantBatch::new(message, vec![]).unwrap(),
            )
            .unwrap();
        let (response, transcript) = between
            .run_with_selection("next request", None, |_| Ok(()))
            .await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        let requests = between.server.requests();
        assert_eq!(
            requests.len(),
            3,
            "compaction occurs between the two ordinary requests"
        );
        assert!(requests[0].get("tools").is_some());
        assert!(requests[1].get("tools").is_none());
        assert!(
            requests[2]["messages"][1]["content"]
                .as_str()
                .unwrap()
                .contains("The active request and tool result were summarized.")
        );
        assert!(
            transcript
                .iter()
                .any(|entry| matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
        );

        // A hook continuation after a large answer compacts past the skill
        // invocation, which is repeated after the summary.
        let during_hook = Harness::new(vec![
            text_reply(&"y".repeat(2_300_000)),
            text_reply("The invocation and first try were summarized."),
            text_reply("Second try."),
        ])
        .await;
        let (response, transcript) = during_hook.run_goal(CONTINUE_THEN_STOP, |_| Ok(())).await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        let requests = during_hook.server.requests();
        assert_eq!(requests.len(), 3);
        assert!(requests[1].get("tools").is_none());
        let messages = requests[2]["messages"].as_array().unwrap();
        assert_eq!(messages.len(), 4);
        assert_eq!(
            messages[1]["content"],
            "Compaction summary of earlier conversation:\nThe invocation and first try were summarized."
        );
        assert_eq!(
            messages[2]["content"],
            requests[0]["messages"][1]["content"]
        );
        assert_eq!(
            messages[3]["content"],
            "Feedback from the goal before_stop hook:\nTwo tests still fail."
        );
        assert_eq!(
            transcript.last(),
            Some(&feedback(HookDecision::Stop, "Objective met."))
        );
    }

    #[tokio::test]
    async fn explicit_input_overflow_retries_once_only_after_a_smaller_checkpoint() {
        let harness = Harness::new(vec![
            Reply::Status(
                400,
                r#"{"error":{"message":"maximum context length exceeded"}}"#.to_owned(),
            ),
            text_reply("Older work summarized."),
            text_reply("Done"),
        ])
        .await;
        harness
            .store
            .append_user(
                &harness.session_id,
                &SessionSettingsChange {
                    model: Some(DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("history ".repeat(4000)),
            )
            .unwrap();
        let TranscriptEntry::AssistantMessage(message) = answer("Earlier answer") else {
            unreachable!()
        };
        harness
            .store
            .append_batch(
                &harness.session_id,
                &AssistantBatch::new(message, vec![]).unwrap(),
            )
            .unwrap();
        let (response, transcript) = harness
            .run_with_selection("next request", None, |_| Ok(()))
            .await;
        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(harness.server.requests().len(), 3);
        assert!(matches!(
            &transcript[4],
            TranscriptEntry::CompactionCheckpoint(_)
        ));

        let no_reduction = Harness::new(vec![
            Reply::Status(
                400,
                r#"{"error":{"message":"maximum context length exceeded"}}"#.to_owned(),
            ),
            text_reply(&"summary ".repeat(5000)),
        ])
        .await;
        no_reduction
            .store
            .append_user(
                &no_reduction.session_id,
                &SessionSettingsChange {
                    model: Some(DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("old".to_owned()),
            )
            .unwrap();
        let TranscriptEntry::AssistantMessage(message) = answer("done") else {
            unreachable!()
        };
        no_reduction
            .store
            .append_batch(
                &no_reduction.session_id,
                &AssistantBatch::new(message, vec![]).unwrap(),
            )
            .unwrap();
        let (response, transcript) = no_reduction
            .run_with_selection("next", None, |_| Ok(()))
            .await;
        assert!(response.is_err());
        assert_eq!(no_reduction.server.requests().len(), 2);
        assert_eq!(transcript.len(), 4, "the new user message remains saved");

        for reply in [
            Reply::Status(500, "server unavailable".to_owned()),
            Reply::Stream(sse(&[delta(
                json!({"role":"assistant", "content":"partial"}),
                Some("length"),
            )])),
        ] {
            let harness = Harness::new(vec![reply]).await;
            let (response, transcript) = harness.run("small", |_| Ok(())).await;
            assert!(response.is_err() || response.unwrap().stop_reason == StopReason::MaxTokens);
            assert_eq!(harness.server.requests().len(), 1);
            assert!(
                transcript
                    .iter()
                    .all(|entry| !matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
            );
        }
    }

    #[tokio::test]
    async fn different_efforts_are_saved_and_sent_for_sequential_turns() {
        let harness = Harness::new(vec![text_reply("First"), text_reply("Second")]).await;
        let selected = Rc::new(RefCell::new(EffortLevel::Low));
        let changed = selected.clone();
        let saved_model = MODEL_CATALOG[1].id;
        let first_settings = SessionSettings::new(saved_model, *selected.borrow());

        let (first, _) = harness
            .run_with_settings("one", first_settings, move |update| {
                if matches!(update, SessionUpdate::SessionInfoUpdate(_)) {
                    *changed.borrow_mut() = EffortLevel::High;
                }
                Ok(())
            })
            .await;
        assert_eq!(first.unwrap().stop_reason, StopReason::EndTurn);
        let second_settings = SessionSettings::new(MODEL_CATALOG[2].id, *selected.borrow());
        let (second, transcript) = harness
            .run_with_settings("two", second_settings, |_| Ok(()))
            .await;
        assert_eq!(second.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            transcript,
            vec![
                TranscriptEntry::Model(saved_model.to_owned()),
                TranscriptEntry::Effort(EffortLevel::Low),
                user("one"),
                answer("First"),
                TranscriptEntry::Effort(EffortLevel::High),
                user("two"),
                answer("Second"),
            ]
        );
        let requests = harness.server.requests();
        assert_eq!(requests[0]["model"], saved_model);
        assert_eq!(requests[1]["model"], saved_model);
        assert_eq!(requests[0]["reasoning"]["effort"], "low");
        assert_eq!(requests[1]["reasoning"]["effort"], "max");
    }

    #[tokio::test]
    async fn absent_selection_uses_saved_settings_without_duplicate_entries() {
        let harness = Harness::new(vec![text_reply("Done")]).await;
        let saved = SessionSettings::new(MODEL_CATALOG[1].id, EffortLevel::High);
        harness
            .store
            .append_user(
                &harness.session_id,
                &SessionSettingsChange {
                    model: Some(saved.model.clone()),
                    effort: Some(saved.effort),
                    mode: None,
                },
                &TranscriptEntry::UserMessage("saved turn".to_owned()),
            )
            .unwrap();

        let (response, transcript) = harness
            .run_with_selection("next turn", None, |_| Ok(()))
            .await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(harness.server.requests()[0]["model"], saved.model);
        assert_eq!(harness.server.requests()[0]["reasoning"]["effort"], "max");
        assert_eq!(
            transcript,
            vec![
                TranscriptEntry::Model(saved.model),
                TranscriptEntry::Effort(EffortLevel::High),
                user("saved turn"),
                user("next turn"),
                answer("Done"),
            ]
        );
    }

    #[tokio::test]
    async fn invalid_prompt_startup_sends_no_request_or_user_message() {
        let workspace = Workspace::new();
        let store = SessionStore::in_memory();
        let server = Server::start(vec![]).await;
        let missing = SessionId::new("missing");
        let missing_run = run(
            store.clone(),
            server.client(),
            PromptInput {
                session_id: missing,
                turn_start: user("not saved"),
                hook: None,
                selected_settings: None,
                system_prompt: system_prompt::for_workspace(&workspace.0).unwrap(),
            },
            PromptCancellation::new(),
            |_| Ok(()),
            PermissionTransport::None,
        );
        assert!(missing_run.is_err());

        let unknown = store.create(&workspace.0).unwrap().id;
        store
            .append_user(
                &unknown,
                &SessionSettingsChange {
                    model: Some("retired/model".to_owned()),
                    effort: Some(EffortLevel::High),
                    mode: None,
                },
                &TranscriptEntry::UserMessage("saved turn".to_owned()),
            )
            .unwrap();
        let before = store.read(&unknown).unwrap().unwrap().transcript;
        let unknown_run = run(
            store.clone(),
            server.client(),
            PromptInput {
                session_id: unknown.clone(),
                turn_start: user("not saved"),
                hook: None,
                selected_settings: None,
                system_prompt: system_prompt::for_workspace(&workspace.0).unwrap(),
            },
            PromptCancellation::new(),
            |_| Ok(()),
            PermissionTransport::None,
        );
        assert!(unknown_run.is_err());
        assert_eq!(store.read(&unknown).unwrap().unwrap().transcript, before);
        assert!(server.requests().is_empty());
    }

    #[tokio::test]
    async fn several_tool_calls_in_one_message_get_ordered_results() {
        let harness = Harness::new(vec![
            tool_reply(&[("call-1", "printf Chicago"), ("call-2", "printf Denver")]),
            text_reply("Both sunny."),
        ])
        .await;

        let (response, transcript) = harness.run("Weather?", |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        assert_eq!(
            transcript,
            vec![
                model(),
                auto(),
                user("Weather?"),
                calls(&[("call-1", "printf Chicago"), ("call-2", "printf Denver")]),
                printed("call-1", "Chicago"),
                printed("call-2", "Denver"),
                answer("Both sunny."),
            ]
        );
        assert_eq!(harness.stored(), transcript);
        assert_eq!(
            harness.updates().iter().map(describe).collect::<Vec<_>>(),
            vec![
                "config",
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
        assert_eq!(second_request["messages"].as_array().unwrap().len(), 5);
        assert_eq!(second_request["messages"][4]["tool_call_id"], "call-2");
    }

    #[tokio::test]
    async fn cancellation_during_tools_keeps_completed_results_and_cancels_the_rest() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "printf Chicago"),
            ("call-2", "printf Denver"),
        ])])
        .await;
        let cancel = harness.cancellation.clone();

        let (response, transcript) = harness
            .run("Weather?", |update| {
                if finished_tool_call_update_for(update, "call-1") {
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
                auto(),
                user("Weather?"),
                calls(&[("call-1", "printf Chicago"), ("call-2", "printf Denver")]),
                printed("call-1", "Chicago"),
                TranscriptEntry::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: tools::SHELL.to_owned(),
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
                "config",
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
        assert_eq!(transcript, vec![model(), auto(), user("Hi")]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn a_failed_batch_append_leaves_the_transcript_unchanged() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;
        harness.store.with_connection(|connection| {
            connection
                .execute_batch(
                    "CREATE TRIGGER refuse BEFORE INSERT ON transcript_entries
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
        assert_eq!(transcript, vec![model(), auto(), user("Hi")]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn update_failure_after_an_observed_result_still_commits_the_batch() {
        let harness = Harness::new(vec![tool_reply(&[
            ("call-1", "printf Chicago"),
            ("call-2", "printf Denver"),
        ])])
        .await;

        let (response, transcript) = harness
            .run("Weather?", |update| {
                if finished_tool_call_update_for(update, "call-1") {
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
                auto(),
                user("Weather?"),
                calls(&[("call-1", "printf Chicago"), ("call-2", "printf Denver")]),
                printed("call-1", "Chicago"),
                TranscriptEntry::ToolResult(ToolResult {
                    call_id: "call-2".to_owned(),
                    name: tools::SHELL.to_owned(),
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
                "config",
                "info",
                "call-1 pending",
                "call-2 pending",
                "call-1 running",
                "call-1 completed",
            ],
            "nothing more is attempted after sending an update fails"
        );
    }
}
