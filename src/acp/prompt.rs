//! Runs one ACP prompt request. It saves the user message or skill invocation,
//! makes model requests, runs tools in call order, saves each complete
//! assistant batch, runs global and invoked skill hooks at their points in the
//! run, and returns the final stop reason and answer.

use std::{fmt, future::Future, io};

use agent_client_protocol::{
    Client, ConnectionTo, Error, Result,
    schema::v1::{
        ConfigOptionUpdate, RequestPermissionOutcome, SessionId, SessionInfoUpdate, SessionUpdate,
        StopReason,
    },
};

use super::convert;
use crate::cancellation::PromptCancellation;
use crate::{
    compaction,
    hooks::{self, RunHooks},
    openrouter,
    sessions::{
        AssistantBatch, AssistantMessage, HookDecision, HookFeedback, HookFeedbackContent,
        HookKind, SessionMode, SessionSettings, SessionSettingsChange, SessionStore,
        SessionSummary, SkillInvocation, ToolCall, ToolOutcome, ToolResult, TranscriptEntry,
    },
    tools,
};

/// The most hook continuations one prompt run accepts. Each is one model
/// request, however many hooks requested it.
const MAX_HOOK_CONTINUATIONS: usize = 50;

/// How a hook call finishes when its command saved nothing.
const NO_FEEDBACK: &str = "No feedback.";

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
    /// Global hooks followed by the invoked skill's hooks.
    pub hooks: Vec<RunHooks>,
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
    let updates = run.save_turn_start(input.turn_start)?;
    Ok(async move {
        let Some(updates) = updates else {
            return Ok(PromptOutput {
                stop_reason: StopReason::Cancelled,
                answer: None,
            });
        };
        let outcome = match run.start(updates).await {
            Ok(()) => run.run_model_loop().await,
            Err(outcome) => outcome,
        };
        let result = run.finish(outcome);
        run.run_after_run(&result).await;
        result
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
    hooks: Vec<RunHooks>,
    /// Identifies this run in the input of each of its hook commands.
    run_id: String,
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
            hooks: input.hooks.clone(),
            run_id: uuid::Uuid::new_v4().to_string(),
            hook_continuations: 0,
            answer: None,
        })
    }

    /// Saves the user message or skill invocation before any model request and
    /// returns the session updates that announce it. `None` when cancellation
    /// was already observed, in which case nothing is written.
    fn save_turn_start(
        &mut self,
        turn_start: TranscriptEntry,
    ) -> Result<Option<Vec<SessionUpdate>>> {
        if self.cancellation.is_cancelled() {
            return Ok(None);
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
        let mut updates = Vec::new();
        if locks_model {
            updates.push(SessionUpdate::ConfigOptionUpdate(ConfigOptionUpdate::new(
                super::config_options(&self.settings, true),
            )));
        }
        self.transcript.push(turn_start);
        let mut info = SessionInfoUpdate::new().updated_at(updated.updated_at);
        if self.summary.session_title.is_none()
            && let Some(session_title) = updated.session_title
        {
            info = info.title(session_title);
        }
        updates.push(SessionUpdate::SessionInfoUpdate(info));
        Ok(Some(updates))
    }

    /// Announces the saved turn start and runs the `before_run` hooks.
    async fn start(
        &mut self,
        updates: Vec<SessionUpdate>,
    ) -> std::result::Result<(), PromptOutcome> {
        for update in updates {
            (self.send_update)(update).map_err(PromptOutcome::AcpUpdate)?;
        }
        for hooks in self.hooks_for(HookKind::BeforeRun) {
            self.run_hook(
                &hooks,
                &hooks::Event::BeforeRun,
                |run, hooks, output: hooks::Feedback| {
                    run.save_optional_feedback(
                        hooks,
                        output
                            .message
                            .map(|message| HookFeedbackContent::BeforeRun { message }),
                    )
                },
            )
            .await?;
        }
        Ok(())
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
            if let Err(outcome) = self.send_usage() {
                return outcome;
            }
            match outcome {
                Some(PromptOutcome::Finished) => {
                    self.answer = Some(text.clone());
                    let event = hooks::Event::BeforeStop { answer: text };
                    let mut decision = HookDecision::Stop;
                    for hooks in self.hooks_for(HookKind::BeforeStop) {
                        match self
                            .run_hook(&hooks, &event, Self::save_stop_feedback)
                            .await
                        {
                            Ok(HookDecision::Continue) => decision = HookDecision::Continue,
                            Ok(HookDecision::Stop) => {}
                            Err(outcome) => return outcome,
                        }
                    }
                    if decision == HookDecision::Continue {
                        self.hook_continuations += 1;
                        continue;
                    }
                    return PromptOutcome::Finished;
                }
                Some(outcome) => return outcome,
                None => {
                    if let Err(outcome) = self.run_after_tools(&calls).await {
                        return outcome;
                    }
                }
            }
        }
    }

    fn hooks_for(&self, kind: HookKind) -> Vec<RunHooks> {
        self.hooks
            .iter()
            .filter(|hooks| hooks.hooks.command(kind).is_some())
            .cloned()
            .collect()
    }

    /// Runs one command for `event`, shown to the ACP client
    /// as an execute tool call; it is not a model tool call. `apply` turns the
    /// command's output into the result and the text the call finishes with.
    async fn run_hook<T: hooks::Output, R>(
        &mut self,
        hooks: &RunHooks,
        event: &hooks::Event,
        apply: impl FnOnce(&mut Self, &RunHooks, T) -> std::result::Result<(R, String), PromptOutcome>,
    ) -> std::result::Result<R, PromptOutcome> {
        if self.cancellation.is_cancelled() {
            return Err(PromptOutcome::Cancelled);
        }
        let context = self.hook_context(hooks);
        let call_id = convert::hook_call_id();
        let pending = convert::pending_hook_call(&call_id, context.skill.as_deref(), event.kind());
        (self.send_update)(pending).map_err(PromptOutcome::AcpUpdate)?;
        (self.send_update)(convert::in_progress_tool_call_update(&call_id))
            .map_err(PromptOutcome::AcpUpdate)?;
        let output = hooks::run(hooks, &context, event, self.cancellation.cancelled()).await;
        let result = match output {
            Ok(output) => apply(self, hooks, output),
            Err(error) if error.kind() == io::ErrorKind::Interrupted => {
                Err(PromptOutcome::Cancelled)
            }
            Err(error) => Err(PromptOutcome::Hook(error)),
        };
        let update = match &result {
            Ok((_, text)) => convert::finished_hook_call_update(&call_id, Ok(text)),
            Err(outcome) => convert::finished_hook_call_update(&call_id, Err(&outcome.to_string())),
        };
        (self.send_update)(update).map_err(PromptOutcome::AcpUpdate)?;
        result.map(|(value, _)| value)
    }

    fn hook_context(&self, hooks: &RunHooks) -> hooks::Context {
        hooks::Context {
            skill: hooks.skill.clone(),
            arguments: match &hooks.skill {
                Some(name) => {
                    let invocation = self.hook_invocation();
                    assert_eq!(
                        name, &invocation.name,
                        "skill hooks belong to the current invocation"
                    );
                    invocation.arguments.clone()
                }
                None => String::new(),
            },
            session_id: self.summary.id.to_string(),
            mode: self.settings.mode,
            run_id: self.run_id.clone(),
            workspace: self.summary.workspace_path.clone(),
            model: self.settings.model.clone(),
            effort: self.settings.effort,
        }
    }

    fn hook_invocation(&self) -> &SkillInvocation {
        match self.transcript.iter().rev().find(|entry| {
            matches!(
                entry,
                TranscriptEntry::UserMessage(_) | TranscriptEntry::SkillInvocation(_)
            )
        }) {
            Some(TranscriptEntry::SkillInvocation(invocation)) => invocation,
            _ => panic!("a skill hook runs only in a turn started by its invocation"),
        }
    }

    /// Runs the `before_tool` hooks on a call. `Some` holds the messages of any
    /// denials, one per line.
    async fn run_before_tool(
        &mut self,
        call: &ToolCall,
    ) -> std::result::Result<Option<String>, PromptOutcome> {
        let event = hooks::Event::BeforeTool { tool: call.clone() };
        let mut denials = Vec::new();
        for hooks in self.hooks_for(HookKind::BeforeTool) {
            let denial = self
                .run_hook(&hooks, &event, |_, hooks, decision: hooks::ToolDecision| {
                    Ok(match decision {
                        hooks::ToolDecision::Allow {} => (None, "Allowed.".to_owned()),
                        hooks::ToolDecision::Deny { message } => {
                            let label = crate::sessions::hook_label(
                                hooks.skill.as_deref(),
                                HookKind::BeforeTool,
                            );
                            (
                                Some(format!("{label} denied this call: {message}")),
                                format!("Denied: {message}"),
                            )
                        }
                    })
                })
                .await?;
            denials.extend(denial);
        }
        Ok((!denials.is_empty()).then(|| denials.join("\n")))
    }

    /// Runs the `after_tools` hooks on the batch just committed.
    async fn run_after_tools(
        &mut self,
        calls: &[ToolCall],
    ) -> std::result::Result<(), PromptOutcome> {
        let results = &self.transcript[self.transcript.len() - calls.len()..];
        let tools: Vec<_> = calls
            .iter()
            .zip(results)
            .map(|(call, entry)| match entry {
                TranscriptEntry::ToolResult(result) => hooks::ToolReport::new(call, result),
                _ => unreachable!("a committed batch ends with its tool results"),
            })
            .collect();
        let event = hooks::Event::AfterTools { tools };
        for hooks in self.hooks_for(HookKind::AfterTools) {
            self.run_hook(&hooks, &event, |run, hooks, output: hooks::Feedback| {
                run.save_optional_feedback(
                    hooks,
                    output
                        .message
                        .map(|message| HookFeedbackContent::AfterTools { message }),
                )
            })
            .await?;
        }
        Ok(())
    }

    /// Runs the `after_run` hooks on the prompt run's result. They ignore
    /// cancellation, which they may be reporting, and their deadlines bound
    /// them. Their ACP updates are best effort, and each failure is written to
    /// stderr without changing the result or stopping the next hook.
    async fn run_after_run(&mut self, result: &Result<PromptOutput>) {
        let (outcome, answer, error) = match result {
            Ok(output) => {
                let outcome = match output.stop_reason {
                    StopReason::EndTurn => hooks::RunOutcome::Finished,
                    StopReason::Cancelled => hooks::RunOutcome::Cancelled,
                    StopReason::MaxTokens => hooks::RunOutcome::TokenLimit,
                    StopReason::Refusal => hooks::RunOutcome::Refused,
                    other => unreachable!("a prompt run never stops with {other:?}"),
                };
                (outcome, output.answer.clone(), None)
            }
            // The ACP error's Display quotes string data as JSON.
            Err(error) => {
                let text = match error.data.as_ref().and_then(serde_json::Value::as_str) {
                    Some(data) => format!("{}: {data}", error.message),
                    None => error.to_string(),
                };
                (hooks::RunOutcome::Failed, None, Some(text))
            }
        };
        let event = hooks::Event::AfterRun {
            outcome,
            answer,
            error,
        };
        for hooks in self.hooks_for(HookKind::AfterRun) {
            let context = self.hook_context(&hooks);
            let call_id = convert::hook_call_id();
            let _ = (self.send_update)(convert::pending_hook_call(
                &call_id,
                context.skill.as_deref(),
                HookKind::AfterRun,
            ));
            let _ = (self.send_update)(convert::in_progress_tool_call_update(&call_id));
            let reported: io::Result<hooks::Report> =
                hooks::run(&hooks, &context, &event, std::future::pending()).await;
            let update = match reported {
                Ok(_) => convert::finished_hook_call_update(&call_id, Ok(NO_FEEDBACK)),
                Err(error) => {
                    eprintln!("{error}");
                    convert::finished_hook_call_update(&call_id, Err(&error.to_string()))
                }
            };
            let _ = (self.send_update)(update);
        }
    }

    fn save_optional_feedback(
        &mut self,
        hooks: &RunHooks,
        content: Option<HookFeedbackContent>,
    ) -> std::result::Result<((), String), PromptOutcome> {
        match content {
            Some(content) => self
                .save_feedback(hooks, content)
                .map(|message| ((), message)),
            None => Ok(((), NO_FEEDBACK.to_owned())),
        }
    }

    fn save_stop_feedback(
        &mut self,
        hooks: &RunHooks,
        output: hooks::StopDecision,
    ) -> std::result::Result<(HookDecision, String), PromptOutcome> {
        if output.decision == HookDecision::Continue
            && self.hook_continuations == MAX_HOOK_CONTINUATIONS
        {
            return Err(PromptOutcome::Hook(io::Error::other(format!(
                "{} asked to continue more than {MAX_HOOK_CONTINUATIONS} times",
                crate::sessions::hook_label(hooks.skill.as_deref(), HookKind::BeforeStop)
            ))));
        }
        let message = self.save_feedback(
            hooks,
            HookFeedbackContent::BeforeStop {
                decision: output.decision,
                message: output.message,
            },
        )?;
        Ok((output.decision, message))
    }

    /// Saves hook feedback after it passes input admission, and returns its
    /// message.
    fn save_feedback(
        &mut self,
        hooks: &RunHooks,
        content: HookFeedbackContent,
    ) -> std::result::Result<String, PromptOutcome> {
        let feedback = HookFeedback {
            skill: hooks.skill.clone(),
            content,
        };
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
                "{} feedback exceeds the model context limit",
                feedback.label()
            ))));
        }
        self.store
            .append_hook_feedback(&self.summary.id, &feedback)
            .map_err(PromptOutcome::Storage)?;
        let message = feedback.message().to_owned();
        self.transcript
            .push(TranscriptEntry::HookFeedback(feedback));
        Ok(message)
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
        let compacted = compaction::compact(
            &self.store,
            &self.openrouter,
            &self.cancellation,
            &self.summary.id,
            &self.settings,
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
        })?;
        if compacted {
            self.send_usage()?;
        }
        Ok(compacted)
    }

    /// Reports the context tokens and session cost of the saved transcript.
    fn send_usage(&mut self) -> std::result::Result<(), PromptOutcome> {
        let update = convert::usage_update(&self.transcript, &self.settings, &self.system_prompt)
            .map_err(PromptOutcome::OpenRouter)?;
        if let Some(update) = update {
            (self.send_update)(update).map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(())
    }

    /// Runs validated calls in order. Each outcome enters the uncommitted batch
    /// before its ACP update is sent, so an update failure does not erase
    /// completed work.
    async fn execute(&mut self, calls: &[ToolCall]) -> std::result::Result<(), PromptOutcome> {
        for call in calls {
            if self.cancellation.is_cancelled() {
                return Err(PromptOutcome::Cancelled);
            }
            let denial = self.run_before_tool(call).await?;
            let outcome = if let Some(message) = denial {
                ToolOutcome::Failed(message)
            } else if self.approve(call).await? {
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
                PromptOutcome::Hook(error) => ToolOutcome::Failed(format!("Not started: {error}")),
                PromptOutcome::Finished
                | PromptOutcome::TokenLimit
                | PromptOutcome::Refused
                | PromptOutcome::OpenRouter(_) => {
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
            fixture::{Reply, Server, delta, sse, text_reply, tool_reply, usage},
        },
        sessions::{EffortLevel, ModelUsage, SkillInvocation},
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
        global_hooks: Option<RunHooks>,
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
                global_hooks: None,
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
            self.run_turn(user(input), Vec::new(), selected_settings, on_update)
                .await
        }

        async fn run_turn(
            &self,
            turn_start: TranscriptEntry,
            hooks: Vec<RunHooks>,
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
                    hooks: self.global_hooks.clone().into_iter().chain(hooks).collect(),
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
            skill: Some("goal".to_owned()),
            content: HookFeedbackContent::BeforeStop {
                decision,
                message: message.to_owned(),
            },
        })
    }

    fn command(command: &str) -> Option<hooks::HookCommand> {
        Some(hooks::HookCommand {
            command: command.to_owned(),
        })
    }

    /// The JSON objects a hook command wrote to `file` in the workspace, one
    /// per line.
    fn hook_inputs(harness: &Harness, file: &str) -> Vec<serde_json::Value> {
        fs::read_to_string(harness.workspace.0.join(file))
            .unwrap()
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect()
    }

    /// A hook command that appends each input line to `inputs`, continues
    /// once, and then stops.
    const CONTINUE_THEN_STOP: &str = r#"cat >> inputs; echo >> inputs; if [ -e continued ]; then echo '{"decision":"stop","message":"Objective met."}'; else touch continued; echo '{"decision":"continue","message":"Two tests still fail."}'; fi"#;

    impl Harness {
        /// Runs `/goal Pass the tests.` with a `before_stop` hook running
        /// `before_stop` in the workspace.
        async fn run_goal(
            &self,
            before_stop: &str,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            let hooks = hooks::Hooks {
                before_stop: command(before_stop),
                ..hooks::Hooks::default()
            };
            self.run_skill(hooks, on_update).await
        }

        /// Runs `/goal Pass the tests.` with `hooks` running in the workspace.
        async fn run_skill(
            &self,
            hooks: hooks::Hooks,
            on_update: impl FnMut(&SessionUpdate) -> Result<()>,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            let hooks = RunHooks {
                skill: Some("goal".to_owned()),
                hooks,
                directory: self.workspace.0.clone(),
            };
            self.run_turn(
                invocation("Pass the tests."),
                vec![hooks],
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
            usage: None,
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
            usage: None,
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
            SessionUpdate::UsageUpdate(_) => "usage".to_owned(),
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
        let harness = Harness::new(vec![Reply::Stream(sse(&[
            delta(
                json!({ "role": "assistant", "content": "Hello there." }),
                None,
            ),
            delta(json!({}), Some("stop")),
            usage(120, 30, 0.25),
        ]))])
        .await;

        let (response, transcript) = harness.run_with_selection("Hi", None, |_| Ok(())).await;

        assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
        let request = &harness.server.requests()[0];
        assert_eq!(request["model"], DEFAULT_MODEL);
        assert!(request.get("reasoning").is_none());
        let TranscriptEntry::AssistantMessage(mut answered) = answer("Hello there.") else {
            unreachable!()
        };
        answered.usage = Some(ModelUsage {
            input_tokens: 120,
            output_tokens: 30,
            cost: 0.25,
        });
        assert_eq!(
            transcript,
            vec![
                model(),
                user("Hi"),
                TranscriptEntry::AssistantMessage(answered)
            ]
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
            vec!["config", "info", "text", "usage"]
        );
        let SessionUpdate::UsageUpdate(reported) = updates.last().unwrap() else {
            unreachable!()
        };
        assert_eq!(
            (reported.used, reported.size),
            (150, MODEL_CATALOG[0].context_limit as u64)
        );
        assert_eq!(
            reported.cost,
            Some(agent_client_protocol::schema::v1::Cost::new(0.25, "USD"))
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
            "Feedback from the skill /goal before_stop hook:\nTwo tests still fail."
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
                "usage",
                "hook pending",
                "hook running",
                "hook completed",
                "text",
                "usage",
                "hook pending",
                "hook running",
                "hook completed",
            ]
        );

        for (command, expected) in [
            (
                "echo broken >&2; exit 3",
                "skill /goal before_stop hook exited with code 3\nstderr:\nbroken",
            ),
            (
                "echo 'not json'",
                "skill /goal before_stop hook output is not one valid before_stop response object",
            ),
            (
                r#"echo '{"decision":"stop","message":" "}'"#,
                "skill /goal before_stop hook message is blank",
            ),
            (
                "head -c 20000 /dev/zero | tr '\\0' x",
                "skill /goal before_stop hook output exceeds 16 KiB",
            ),
            (
                "printf '\\377'",
                "skill /goal before_stop hook output is not UTF-8",
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

        let mut harness = Harness::new(
            (0..=MAX_HOOK_CONTINUATIONS)
                .map(|index| text_reply(&format!("Try {index}.")))
                .collect(),
        )
        .await;
        let again = r#"echo '{"decision":"continue","message":"Again."}'"#;
        harness.global_hooks = Some(RunHooks {
            skill: None,
            directory: harness.workspace.0.clone(),
            hooks: hooks::Hooks {
                before_stop: command(again),
                ..hooks::Hooks::default()
            },
        });
        let (response, transcript) = harness.run_goal(again, |_| Ok(())).await;
        let error = response.unwrap_err();
        assert!(format!("{error:?}").contains("asked to continue more than 50 times"));
        assert_eq!(harness.server.requests().len(), MAX_HOOK_CONTINUATIONS + 1);
        assert_eq!(
            transcript
                .iter()
                .filter(|entry| matches!(entry, TranscriptEntry::HookFeedback(_)))
                .count(),
            2 * MAX_HOOK_CONTINUATIONS
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

    /// One assistant message with one call per `(call_id, tool name,
    /// arguments)`.
    fn calls_reply(calls: &[(&str, &str, serde_json::Value)]) -> Reply {
        let tool_calls: Vec<_> = calls
            .iter()
            .enumerate()
            .map(|(index, (id, name, arguments))| {
                json!({
                    "index": index,
                    "id": id,
                    "type": "function",
                    "function": { "name": name, "arguments": arguments.to_string() },
                })
            })
            .collect();
        Reply::Stream(sse(&[delta(
            json!({ "role": "assistant", "tool_calls": tool_calls }),
            Some("tool_calls"),
        )]))
    }

    fn hook_titles(harness: &Harness) -> Vec<String> {
        harness
            .updates()
            .iter()
            .filter_map(|update| match update {
                SessionUpdate::ToolCall(call)
                    if call.tool_call_id.to_string().starts_with("hook-") =>
                {
                    Some(call.title.clone())
                }
                _ => None,
            })
            .collect()
    }

    const DENY_REMOVAL: &str = r#"input=$(cat); printf '%s\n' "$input" >> before_tool.json; case "$input" in *'rm -rf'*) echo '{"decision":"deny","message":"Removing files is not allowed."}';; *) echo '{"decision":"allow"}';; esac"#;

    #[tokio::test]
    async fn lifecycle_hooks_run_at_their_points_and_their_feedback_reaches_the_model() {
        for (global, skill, deciding) in [
            (true, false, 0),
            (false, true, 0),
            (true, true, 0),
            (true, true, 1),
            (true, true, 2),
        ] {
            let patch = |id: &'static str, file: &str| {
                (
                    id,
                    tools::APPLY_PATCH,
                    json!({ "patch": format!("*** Begin Patch\n*** Add File: {file}\n+{file}\n*** End Patch") }),
                )
            };
            let harness = Harness::new(vec![
                calls_reply(&[
                    patch("patch-1", "first"),
                    ("shell-1", tools::SHELL, json!({ "command": "exit 3" })),
                    patch("patch-2", "second"),
                    (
                        "shell-2",
                        tools::SHELL,
                        json!({ "command": "rm -rf first" }),
                    ),
                ]),
                text_reply("First try."),
                text_reply("Second try."),
            ])
            .await;
            let global_dir = Workspace::new();
            let decides = |index: usize| index == deciding || deciding == 2;
            let before_run =
                r#"cat > before_run.json; echo '{"message":"The tests live in tests/."}'"#;
            let allow =
                r#"cat >> before_tool.json; echo >> before_tool.json; echo '{"decision":"allow"}'"#;
            let after_tools = format!(
                r#"cat > after_tools.json; test -e '{0}/first' && test -e '{0}/second' && echo '{{"message":"Both files exist."}}'"#,
                harness.workspace.0.display()
            );
            let stop =
                r#"cat >> inputs; echo >> inputs; echo '{"decision":"stop","message":"Ready."}'"#;
            let definitions: Vec<_> = [
                global.then_some((None, global_dir.0.clone())),
                skill.then_some((Some("goal".to_owned()), harness.workspace.0.clone())),
            ]
            .into_iter()
            .flatten()
            .enumerate()
            .map(|(index, (skill, directory))| RunHooks {
                skill,
                directory,
                hooks: hooks::Hooks {
                    before_run: command(before_run),
                    before_tool: command(if decides(index) { DENY_REMOVAL } else { allow }),
                    after_tools: command(&after_tools),
                    before_stop: command(if decides(index) {
                        CONTINUE_THEN_STOP
                    } else {
                        stop
                    }),
                    after_run: command(r#"cat > after_run.json; echo '{}'"#),
                },
            })
            .collect();
            let turn_start = if skill {
                invocation("Pass the tests.")
            } else {
                user("Pass the tests.")
            };
            let (response, transcript) = harness
                .run_turn(
                    turn_start,
                    definitions.clone(),
                    Some(
                        SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default)
                            .with_mode(SessionMode::Auto),
                    ),
                    |_| Ok(()),
                )
                .await;
            assert_eq!(response.unwrap().answer.as_deref(), Some("Second try."));
            assert_eq!(
                fs::read_to_string(harness.workspace.0.join("first")).unwrap(),
                "first\n"
            );
            let denied = definitions
                .iter()
                .enumerate()
                .filter(|(index, _)| decides(*index))
                .map(|(_, hooks)| {
                    format!(
                        "{} denied this call: Removing files is not allowed.",
                        crate::sessions::hook_label(hooks.skill.as_deref(), HookKind::BeforeTool)
                    )
                })
                .collect::<Vec<_>>()
                .join("\n");
            let requests = harness.server.requests();
            assert_eq!(requests.len(), 3);
            assert_eq!(harness.stored(), transcript);
            let feedback: Vec<_> = transcript
                .iter()
                .filter_map(|entry| match entry {
                    TranscriptEntry::HookFeedback(feedback) => Some(feedback),
                    _ => None,
                })
                .collect();
            assert_eq!(feedback.len(), 4 * definitions.len());
            for (point, kind) in [
                HookKind::BeforeRun,
                HookKind::AfterTools,
                HookKind::BeforeStop,
                HookKind::BeforeStop,
            ]
            .into_iter()
            .enumerate()
            {
                for (entry, hooks) in feedback
                    [point * definitions.len()..(point + 1) * definitions.len()]
                    .iter()
                    .zip(&definitions)
                {
                    assert_eq!(entry.kind(), kind);
                    assert_eq!(entry.skill, hooks.skill);
                    let text = openrouter::hook_feedback_text(entry);
                    // The last stop feedback is saved after the final model request.
                    if point < 3 {
                        assert_eq!(
                            requests[point]["messages"]
                                .as_array()
                                .unwrap()
                                .iter()
                                .filter(|message| message["content"] == text)
                                .count(),
                            1
                        );
                    }
                }
            }
            let mut shared_run_id = None;
            let mut batch_input = None;
            for hooks in &definitions {
                let read = |file: &str| -> Vec<serde_json::Value> {
                    fs::read_to_string(hooks.directory.join(file))
                        .unwrap()
                        .lines()
                        .map(|line| serde_json::from_str(line).unwrap())
                        .collect()
                };
                for (file, kind, count) in [
                    ("before_run.json", "before_run", 1),
                    ("before_tool.json", "before_tool", 4),
                    ("after_tools.json", "after_tools", 1),
                    ("inputs", "before_stop", 2),
                    ("after_run.json", "after_run", 1),
                ] {
                    let inputs = read(file);
                    assert_eq!(inputs.len(), count);
                    for input in &inputs {
                        assert_eq!(input["kind"], kind);
                        assert_eq!(input["skill"], json!(hooks.skill));
                        assert_eq!(
                            input["arguments"],
                            if hooks.skill.is_some() {
                                "Pass the tests."
                            } else {
                                ""
                            }
                        );
                        assert_eq!(input["session_id"], harness.session_id.to_string());
                        assert_eq!(input["workspace"], harness.workspace.0.to_str().unwrap());
                        assert_eq!(input["mode"], "auto");
                        assert_eq!(
                            shared_run_id.get_or_insert_with(|| input["run_id"].clone()),
                            &input["run_id"]
                        );
                    }
                }
                let tools = read("before_tool.json");
                assert_eq!(
                    tools[1]["tool"],
                    json!({ "call_id": "shell-1", "name": "shell", "arguments": r#"{"command":"exit 3"}"# })
                );
                let input = read("after_tools.json").remove(0);
                assert_eq!(
                    batch_input.get_or_insert_with(|| input["tools"].clone()),
                    &input["tools"]
                );
                assert_eq!(
                    input["tools"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .map(|tool| tool["outcome"].as_str().unwrap())
                        .collect::<Vec<_>>(),
                    ["completed", "failed", "completed", "failed"]
                );
                assert_eq!(input["tools"][3]["text"], denied);
                let report = read("after_run.json").remove(0);
                assert_eq!(report["outcome"], "finished");
                assert_eq!(report["answer"], "Second try.");
                assert!(report["error"].is_null());
            }
            let expected_titles: Vec<_> = [
                HookKind::BeforeRun,
                HookKind::BeforeTool,
                HookKind::BeforeTool,
                HookKind::BeforeTool,
                HookKind::BeforeTool,
                HookKind::AfterTools,
                HookKind::BeforeStop,
                HookKind::BeforeStop,
                HookKind::AfterRun,
            ]
            .into_iter()
            .flat_map(|kind| {
                definitions
                    .iter()
                    .map(move |hooks| crate::sessions::hook_label(hooks.skill.as_deref(), kind))
            })
            .collect();
            assert_eq!(hook_titles(&harness), expected_titles);
        }
    }

    #[tokio::test]
    async fn hook_errors_keep_saved_work_and_after_run_reports_every_outcome() {
        const REPORT: &str = r#"cat > after_run.json; echo '{}'"#;
        let reported = |harness: &Harness| hook_inputs(harness, "after_run.json").remove(0);
        let error_text = |response: Result<PromptOutput>| {
            let error = response.unwrap_err();
            error.data.unwrap().as_str().unwrap().to_owned()
        };

        // Input rejected before saving runs no hook.
        let harness = Harness::new(vec![]).await;
        let rejected = run(
            harness.store.clone(),
            harness.server.client(),
            PromptInput {
                session_id: harness.session_id.clone(),
                turn_start: invocation(&"x".repeat(3_000_000)),
                hooks: vec![RunHooks {
                    skill: Some("goal".to_owned()),
                    hooks: hooks::Hooks {
                        before_run: command("touch ran; echo '{}'"),
                        after_run: command("touch ran; echo '{}'"),
                        ..hooks::Hooks::default()
                    },
                    directory: harness.workspace.0.clone(),
                }],
                selected_settings: None,
                system_prompt: "system".to_owned(),
            },
            PromptCancellation::new(),
            |_| Ok(()),
            PermissionTransport::None,
        );
        assert!(rejected.is_err());
        assert!(harness.stored().is_empty());
        assert!(!harness.workspace.0.join("ran").exists());

        // `before_run` saves nothing for `{}`; an error or oversized feedback
        // ends the run before any model request.
        let system = system_prompt::for_workspace(&Workspace::new().0).unwrap();
        let (admission, _, _) = compaction::budget(DEFAULT_MODEL).unwrap();
        let base = compaction::request_estimate(
            DEFAULT_MODEL,
            EffortLevel::Default,
            &system,
            &[model(), auto(), invocation("")],
        )
        .unwrap();
        let near_limit = "x".repeat((admission - base - 100) * 3);
        let large = r#"printf '{"message":"%s"}' $(head -c 1000 /dev/zero | tr '\0' y)"#;
        for (arguments, before_run, expected) in [
            ("Pass the tests.", "echo '{}'", None),
            (
                "Pass the tests.",
                "echo broken >&2; exit 4",
                Some("skill /goal before_run hook exited with code 4\nstderr:\nbroken"),
            ),
            (
                near_limit.as_str(),
                large,
                Some("skill /goal before_run hook feedback exceeds the model context limit"),
            ),
        ] {
            let harness = Harness::new(vec![text_reply("Done.")]).await;
            let (response, transcript) = harness
                .run_turn(
                    invocation(arguments),
                    vec![RunHooks {
                        skill: Some("goal".to_owned()),
                        hooks: hooks::Hooks {
                            before_run: command(before_run),
                            after_run: command(REPORT),
                            ..hooks::Hooks::default()
                        },
                        directory: harness.workspace.0.clone(),
                    }],
                    Some(
                        SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default)
                            .with_mode(SessionMode::Auto),
                    ),
                    |_| Ok(()),
                )
                .await;
            let report = reported(&harness);
            match expected {
                None => {
                    assert_eq!(response.unwrap().stop_reason, StopReason::EndTurn);
                    assert_eq!(
                        transcript,
                        [model(), auto(), invocation(arguments), answer("Done.")]
                    );
                    assert_eq!(report["outcome"], "finished");
                }
                Some(expected) => {
                    let error = error_text(response);
                    assert!(error.contains(expected), "{error}");
                    assert!(harness.server.requests().is_empty());
                    assert_eq!(transcript, [model(), auto(), invocation(arguments)]);
                    assert_eq!(report["outcome"], "failed");
                    assert!(report["error"].as_str().unwrap().contains(expected));
                }
            }
        }

        // A `before_tool` error completes and saves the batch; an
        // `after_tools` error leaves the committed batch saved.
        let not_started = |id: &str| {
            TranscriptEntry::ToolResult(ToolResult {
                call_id: id.to_owned(),
                name: tools::SHELL.to_owned(),
                outcome: ToolOutcome::Failed(
                    "Not started: skill /goal before_tool hook exited with code 5".to_owned(),
                ),
            })
        };
        let batch = [("call-1", "printf one"), ("call-2", "printf two")];
        for (hooks, results, expected) in [
            (
                hooks::Hooks {
                    before_tool: command("exit 5"),
                    ..hooks::Hooks::default()
                },
                vec![not_started("call-1"), not_started("call-2")],
                "skill /goal before_tool hook exited with code 5",
            ),
            (
                hooks::Hooks {
                    after_tools: command("exit 6"),
                    ..hooks::Hooks::default()
                },
                vec![printed("call-1", "one"), printed("call-2", "two")],
                "skill /goal after_tools hook exited with code 6",
            ),
        ] {
            let harness = Harness::new(vec![tool_reply(&batch), text_reply("Done.")]).await;
            let (response, transcript) = harness.run_skill(hooks, |_| Ok(())).await;
            let error = error_text(response);
            assert!(error.contains(expected), "{error}");
            assert_eq!(
                transcript,
                [
                    vec![
                        model(),
                        auto(),
                        invocation("Pass the tests."),
                        calls(&batch)
                    ],
                    results
                ]
                .concat()
            );
            assert_eq!(harness.stored(), transcript);
            assert_eq!(harness.server.requests().len(), 1);
        }

        // `after_run` reports cancellation during a tool and a failed
        // turn-start update, and its own failure or timeout changes nothing.
        let mut harness = Harness::new(vec![tool_reply(&[("call-1", "sleep 30")])]).await;
        harness.global_hooks = Some(RunHooks {
            skill: None,
            directory: harness.workspace.0.clone(),
            hooks: hooks::Hooks {
                after_run: command("exit 7"),
                ..hooks::Hooks::default()
            },
        });
        let cancel = harness.cancellation.clone();
        let hooks = hooks::Hooks {
            after_run: command(REPORT),
            ..hooks::Hooks::default()
        };
        let (response, _) = harness
            .run_skill(hooks.clone(), |update| {
                if matches!(update, SessionUpdate::ToolCallUpdate(update)
                    if update.tool_call_id.to_string() == "call-1"
                        && update.fields.status == Some(ToolCallStatus::InProgress))
                {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;
        assert_eq!(response.unwrap().stop_reason, StopReason::Cancelled);
        let report = reported(&harness);
        assert_eq!(report["outcome"], "cancelled");
        assert!(report["answer"].is_null());

        let harness = Harness::new(vec![text_reply("Done.")]).await;
        let (response, transcript) = harness
            .run_skill(hooks, |update| match update {
                SessionUpdate::SessionInfoUpdate(_) => {
                    Err(Error::internal_error().data("connection closed"))
                }
                _ => Ok(()),
            })
            .await;
        assert!(error_text(response).contains("connection closed"));
        assert_eq!(transcript, [model(), auto(), invocation("Pass the tests.")]);
        let report = reported(&harness);
        assert_eq!(report["outcome"], "failed");
        assert!(
            report["error"]
                .as_str()
                .unwrap()
                .contains("connection closed")
        );

        for after_run in ["exit 7", "sleep 30"] {
            let mut harness = Harness::new(vec![text_reply("Done.")]).await;
            harness.global_hooks = Some(RunHooks {
                skill: None,
                directory: harness.workspace.0.clone(),
                hooks: hooks::Hooks {
                    after_run: command(after_run),
                    ..hooks::Hooks::default()
                },
            });
            let hooks = hooks::Hooks {
                after_run: command(REPORT),
                ..hooks::Hooks::default()
            };
            let (response, _) = harness.run_skill(hooks, |_| Ok(())).await;
            assert_eq!(response.unwrap().answer.as_deref(), Some("Done."));
            assert_eq!(reported(&harness)["outcome"], "finished");
            let updates = described_updates(&harness);
            assert!(updates.iter().any(|update| update == "hook failed"));
            assert_eq!(updates.last().unwrap(), "hook completed");
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
                hooks: Vec::new(),
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
            hooks: Vec::new(),
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
            "Feedback from the skill /goal before_stop hook:\nTwo tests still fail."
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
                hooks: Vec::new(),
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
                hooks: Vec::new(),
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
                "usage",
                "text",
                "usage",
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
