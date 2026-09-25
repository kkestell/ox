//! Runs one ACP prompt request. It saves the user message or skill invocation,
//! makes model requests, runs tools in call order, saves each complete
//! assistant batch, and returns an outcome carrying the answer when finished.

use std::{fmt, future::Future, io, ops::ControlFlow};

use agent_client_protocol::{
    Client, ConnectionTo, Error, Result,
    schema::v1::{
        RequestPermissionOutcome, RequestPermissionResponse, SessionId, SessionInfoUpdate,
        SessionNotification, SessionUpdate,
    },
};

use super::convert;
use crate::cancellation::PromptCancellation;
use crate::{
    compaction,
    openrouter::{self, ModelRequestParameters},
    sessions::{
        AssistantBatch, AssistantMessage, SessionMode, SessionSettings, SessionStore,
        SessionSummary, ToolCall, ToolOutcome, TranscriptEntry, TurnInput, TurnStart,
    },
    shell_processes::ShellProcesses,
    subagents::{Launch, Subagents},
    system_prompt,
    tools::{self, ToolContext},
};

/// The main session ID and, for a subagent, its child session ID. ACP updates
/// and permission requests are addressed with it; the session store uses the
/// agent's own session ID instead.
#[derive(Debug, Clone)]
pub struct AcpIdentity {
    pub session_id: SessionId,
    pub subagent_id: Option<SessionId>,
}

impl AcpIdentity {
    fn main(session_id: SessionId) -> Self {
        Self {
            session_id,
            subagent_id: None,
        }
    }
}

/// Where one agent's ACP updates and Ask mode permission requests go. The
/// captured session mode, not this value, decides whether shell approval is
/// required.
pub struct Presentation {
    identity: AcpIdentity,
    target: Target,
}

enum Target {
    /// Headless execution sends nothing and has no one to ask.
    None,
    /// A subagent's permission requests use the main session's connection.
    Acp { connection: ConnectionTo<Client> },
    /// Tests observe updates here. The mutex lets the observer mutate
    /// through a shared reference.
    #[cfg(test)]
    Observed(std::sync::Mutex<Box<dyn FnMut(SessionUpdate) -> Result<()> + Send>>),
}

impl Presentation {
    /// The main agent of an ACP prompt request.
    pub fn acp(connection: ConnectionTo<Client>, session_id: SessionId) -> Self {
        Self {
            identity: AcpIdentity::main(session_id),
            target: Target::Acp { connection },
        }
    }

    /// A headless run, which saves Auto mode and so never asks.
    pub fn headless(session_id: SessionId) -> Self {
        Self {
            identity: AcpIdentity::main(session_id),
            target: Target::None,
        }
    }

    /// A subagent of the main session `main_session_id`. Its updates are
    /// suppressed; its permission requests use the main agent's connection,
    /// when there is one.
    pub(crate) fn subagent(
        connection: Option<ConnectionTo<Client>>,
        main_session_id: SessionId,
        subagent_id: SessionId,
    ) -> Self {
        Self {
            identity: AcpIdentity {
                session_id: main_session_id,
                subagent_id: Some(subagent_id),
            },
            target: match connection {
                Some(connection) => Target::Acp { connection },
                None => Target::None,
            },
        }
    }

    /// The main agent of a test prompt, whose updates go to `observe`.
    #[cfg(test)]
    pub fn observed(
        session_id: SessionId,
        observe: impl FnMut(SessionUpdate) -> Result<()> + Send + 'static,
    ) -> Self {
        Self {
            identity: AcpIdentity::main(session_id),
            target: Target::Observed(std::sync::Mutex::new(Box::new(observe))),
        }
    }

    /// Only an agent presented under a subagent ID is a subagent.
    fn role(&self) -> tools::Role {
        match self.identity.subagent_id {
            Some(_) => tools::Role::Subagent,
            None => tools::Role::Main,
        }
    }

    /// The connection a subagent's permission requests use.
    fn connection(&self) -> Option<ConnectionTo<Client>> {
        match &self.target {
            Target::Acp { connection, .. } => Some(connection.clone()),
            Target::None => None,
            #[cfg(test)]
            Target::Observed(_) => None,
        }
    }

    /// Whether ACP updates are sent rather than suppressed.
    fn sends_updates(&self) -> bool {
        match &self.target {
            Target::None => false,
            Target::Acp { .. } => self.identity.subagent_id.is_none(),
            #[cfg(test)]
            Target::Observed(_) => true,
        }
    }

    /// Sends one ACP update for the main session. A suppressed update
    /// succeeds without being sent.
    fn send(&self, update: SessionUpdate) -> Result<()> {
        match &self.target {
            Target::None => Ok(()),
            Target::Acp { connection } if self.identity.subagent_id.is_none() => connection
                .send_notification(SessionNotification::new(
                    self.identity.session_id.clone(),
                    update,
                )),
            Target::Acp { .. } => Ok(()),
            #[cfg(test)]
            Target::Observed(observe) => (observe.lock().expect("observer mutex poisoned"))(update),
        }
    }

    /// Asks the ACP client whether `call` may run, under the main session.
    fn request_permission(
        &self,
        call: &ToolCall,
        workspace: &std::path::Path,
        permission: &tools::Permission,
    ) -> impl Future<Output = Result<RequestPermissionResponse>> + Send + use<> {
        let Target::Acp { connection, .. } = &self.target else {
            panic!("Ask mode requires an ACP permission-request connection");
        };
        let request =
            convert::shell_permission_request(&self.identity, call, workspace, permission);
        let connection = connection.clone();
        async move { connection.send_request(request).block_task().await }
    }
}

pub(crate) struct PromptInput {
    pub session_id: SessionId,
    /// The user message or skill invocation that starts the turn.
    pub turn_input: TurnInput,
    /// The ACP selections the turn starts with, used for every model request
    /// in the turn, including automatic compaction.
    pub selected_settings: SessionSettings,
    /// The complete system prompt captured when the session became active.
    pub system_prompt: String,
    /// The active session's shell processes, which outlive this run. The
    /// run reaches only the ones its own session ID started.
    pub shell_processes: ShellProcesses,
}

/// A prompt run's outcome, carrying an answer only when it finishes.
#[derive(Debug, PartialEq)]
pub enum PromptOutput {
    Finished(String),
    Cancelled,
    TokenLimit,
    Refused,
}

pub fn run(
    store: SessionStore,
    openrouter: openrouter::Client,
    input: PromptInput,
    cancellation: PromptCancellation,
    presentation: Presentation,
) -> Result<impl Future<Output = Result<PromptOutput>>> {
    let mut run = AgentTurn::open(store, openrouter, &input, cancellation, presentation)?;
    let update = run.save_turn_start(input.turn_input)?;
    Ok(async move {
        let Some(update) = update else {
            return Ok(PromptOutput::Cancelled);
        };
        let outcome = match run.presentation.send(update) {
            Ok(()) => run.run_model_loop().await,
            Err(error) => PromptOutcome::AcpUpdate(error),
        };
        let result = outcome.into_output();
        run.stop_subagents().await;
        result
    })
}

/// An ACP error as readable text. Its Display quotes string data as JSON.
pub(crate) fn error_text(error: &Error) -> String {
    match error.data.as_ref().and_then(serde_json::Value::as_str) {
        Some(data) => format!("{}: {data}", error.message),
        None => error.to_string(),
    }
}

/// Why this prompt run stopped. Each variant requires a different final response.
enum PromptOutcome {
    Finished(String),
    Cancelled,
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
            Self::Finished(_) => write!(f, "the answer finished"),
            Self::Cancelled => write!(f, "the prompt was cancelled"),
            Self::TokenLimit => write!(f, "the model reached its token limit"),
            Self::Refused => write!(f, "the model refused"),
            Self::OpenRouter(error) => write!(f, "the model request failed: {error}"),
            Self::AcpUpdate(error) => write!(f, "sending an ACP update failed: {error}"),
            Self::Permission(error) => write!(f, "requesting shell permission failed: {error}"),
            Self::Storage(error) => write!(f, "saving the transcript failed: {error}"),
        }
    }
}

struct AgentTurn {
    store: SessionStore,
    openrouter: openrouter::Client,
    /// Sent with the transcript on every model request of this run.
    parameters: ModelRequestParameters,
    mode: SessionMode,
    summary: SessionSummary,
    cancellation: PromptCancellation,
    presentation: Presentation,
    tools: ToolContext,
    /// Saved transcript, extended only after a database transaction succeeds.
    transcript: Vec<TranscriptEntry>,
}

/// A validated assistant message whose tool calls do not all have outcomes
/// yet. `AgentTurn::process_batch` owns it until the batch is saved.
struct UncommittedAssistantBatch {
    message: AssistantMessage,
    /// Sequential execution makes observed outcomes a prefix of the tool calls.
    outcomes: Vec<ToolOutcome>,
}

impl UncommittedAssistantBatch {
    fn new(message: AssistantMessage) -> Self {
        Self {
            message,
            outcomes: Vec::new(),
        }
    }

    /// Gives every unstarted call `outcome` and returns the finished updates
    /// for those calls.
    fn fill_remaining(&mut self, outcome: &ToolOutcome) -> Vec<SessionUpdate> {
        let updates = self.message.tool_calls[self.outcomes.len()..]
            .iter()
            .map(|call| convert::finished_tool_call_update(call, outcome))
            .collect();
        self.outcomes
            .resize(self.message.tool_calls.len(), outcome.clone());
        updates
    }

    fn complete(self) -> AssistantBatch {
        AssistantBatch::new(self.message, self.outcomes)
            .expect("a complete assistant batch has one outcome for each call")
    }
}

impl AgentTurn {
    fn open(
        store: SessionStore,
        openrouter: openrouter::Client,
        input: &PromptInput,
        cancellation: PromptCancellation,
        presentation: Presentation,
    ) -> Result<Self> {
        let session_id = &input.session_id;
        let stored = store
            .read(session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| Error::resource_not_found(Some(session_id.to_string())))?;
        let settings = &input.selected_settings;
        let role = presentation.role();
        let parameters = ModelRequestParameters::new(
            &settings.model,
            settings.effort,
            input.system_prompt.clone(),
            role,
        )
        .map_err(Error::into_internal_error)?;
        let subagents = (role == tools::Role::Main).then(|| {
            Subagents::new(Launch {
                store: store.clone(),
                openrouter: openrouter.clone(),
                main_session_id: stored.summary.id.clone(),
                workspace_path: stored.summary.workspace_path.clone(),
                settings: settings.clone(),
                system_prompt: system_prompt::for_subagent(&input.system_prompt),
                shell_processes: input.shell_processes.clone(),
                connection: presentation.connection(),
                cancellation: cancellation.clone(),
            })
        });
        Ok(Self {
            store,
            openrouter,
            parameters,
            mode: settings.mode,
            tools: ToolContext {
                workspace_path: stored.summary.workspace_path.clone(),
                session_id: stored.summary.id.clone(),
                shell_processes: input.shell_processes.clone(),
                subagents,
            },
            summary: stored.summary,
            cancellation,
            presentation,
            transcript: stored.transcript,
        })
    }

    /// Saves the turn start with the captured model, effort level, and session
    /// mode before any model request, and returns the session update that
    /// announces it. `None` when cancellation was already observed, in which
    /// case nothing is written.
    fn save_turn_start(&mut self, input: TurnInput) -> Result<Option<SessionUpdate>> {
        if self.cancellation.is_cancelled() {
            return Ok(None);
        }
        let turn_start = TurnStart {
            model: self.parameters.model.id.clone(),
            effort: self.parameters.effort,
            mode: self.mode,
            input,
        };
        let mut prospective = self.transcript.clone();
        prospective.push(TranscriptEntry::TurnStart(turn_start.clone()));
        // An earlier image fails every request to a model without image input.
        if !self.parameters.model.accepts_images && compaction::has_images(&prospective) {
            return Err(Error::invalid_params().data(
                "the selected model does not accept images; choose a model that accepts images",
            ));
        }
        if !compaction::input_fits(&self.parameters, &prospective) {
            return Err(Error::invalid_params().data("prompt exceeds the model context limit"));
        }
        let updated = self
            .store
            .append_turn_start(&self.summary.id, &turn_start)
            .map_err(Error::into_internal_error)?;
        self.transcript = prospective;
        let mut info = SessionInfoUpdate::new().updated_at(updated.updated_at);
        if self.summary.session_title.is_none()
            && let Some(session_title) = updated.session_title
        {
            info = info.title(session_title);
        }
        Ok(Some(SessionUpdate::SessionInfoUpdate(info)))
    }

    async fn run_model_loop(&mut self) -> PromptOutcome {
        loop {
            match self.run_model_step().await {
                Ok(ControlFlow::Continue(())) => {}
                Ok(ControlFlow::Break(outcome)) | Err(outcome) => return outcome,
            }
        }
    }

    /// Makes one model request, runs its tool calls, and commits its batch.
    async fn run_model_step(
        &mut self,
    ) -> std::result::Result<ControlFlow<PromptOutcome>, PromptOutcome> {
        if self.cancellation.is_cancelled() {
            return Err(PromptOutcome::Cancelled);
        }
        self.deliver_agent_messages()?;
        let openrouter::Completion { message, stop } = self.request_completion().await?;
        let text = message.text.clone();
        self.process_batch(message).await?;
        self.send_usage()?;
        match stop {
            openrouter::Stop::ToolCalls => Ok(ControlFlow::Continue(())),
            // Messages that arrived by the time the answer committed
            // supersede it. Later ones may be discarded when the run ends.
            openrouter::Stop::Finished if self.deliver_agent_messages()? => {
                Ok(ControlFlow::Continue(()))
            }
            openrouter::Stop::Finished => Ok(ControlFlow::Break(PromptOutcome::Finished(text))),
            openrouter::Stop::TokenLimit => Ok(ControlFlow::Break(PromptOutcome::TokenLimit)),
            openrouter::Stop::Refused => Ok(ControlFlow::Break(PromptOutcome::Refused)),
        }
    }

    /// Makes one model request, forwards provisional output, and returns its
    /// validated completion.
    async fn request_completion(
        &mut self,
    ) -> std::result::Result<openrouter::Completion, PromptOutcome> {
        let compaction::Budget {
            admission,
            automatic_threshold,
            ..
        } = compaction::budget(self.parameters.model);
        let estimate = compaction::request_estimate(&self.parameters, &self.transcript);
        if estimate >= automatic_threshold && compaction::has_candidate(&self.transcript) {
            self.compact().await?;
        }
        if compaction::request_estimate(&self.parameters, &self.transcript) > admission {
            return Err(PromptOutcome::OpenRouter(compaction::context_error()));
        }
        let mut retried = false;
        loop {
            let projected = compaction::projection(&self.transcript);
            let started = tokio::select! {
                biased;
                () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
                started = self.openrouter.stream_completion(&self.parameters, projected) => started,
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
                        self.presentation
                            .send(convert::agent_message_chunk(&text))
                            .map_err(PromptOutcome::AcpUpdate)?;
                    }
                    Some(openrouter::StreamItem::ReasoningDelta(text)) => {
                        self.presentation
                            .send(convert::agent_thought_chunk(&text))
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
            &self.parameters,
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

    /// Saves the subagent messages published since the last request
    /// boundary, after they pass input admission, and presents them. Returns
    /// whether there were any; a subagent has none.
    fn deliver_agent_messages(&mut self) -> std::result::Result<bool, PromptOutcome> {
        let Some(subagents) = &self.tools.subagents else {
            return Ok(false);
        };
        let messages = subagents.take_messages();
        if messages.is_empty() {
            return Ok(false);
        }
        let mut prospective = self.transcript.clone();
        prospective.push(TranscriptEntry::AgentMessages(messages.clone()));
        if !compaction::input_fits(&self.parameters, &prospective) {
            return Err(PromptOutcome::OpenRouter(io::Error::new(
                io::ErrorKind::InvalidInput,
                "subagent messages exceed the model context limit",
            )));
        }
        self.store
            .append_agent_messages(&self.summary.id, &messages)
            .map_err(PromptOutcome::Storage)?;
        self.transcript = prospective;
        for update in convert::agent_message_updates(&messages) {
            self.presentation
                .send(update)
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(true)
    }

    /// Stops the main agent's subagents once its result is known, waiting
    /// for each to save its interrupted batch, and reports the session cost
    /// their saved work may have changed. Its ACP update is best effort.
    async fn stop_subagents(&mut self) {
        let Some(subagents) = &self.tools.subagents else {
            return;
        };
        if subagents.shutdown().await {
            let _ = self.send_usage();
        }
    }

    /// Reports the context tokens of the saved transcript and the session
    /// cost, which includes the saved cost of its child sessions.
    fn send_usage(&mut self) -> std::result::Result<(), PromptOutcome> {
        if !self.presentation.sends_updates() {
            return Ok(());
        }
        let children_cost = self
            .store
            .children_cost(&self.summary.id)
            .map_err(PromptOutcome::Storage)?;
        if let Some(update) =
            convert::usage_update(&self.transcript, &self.parameters, children_cost)
        {
            self.presentation
                .send(update)
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(())
    }

    /// Announces, runs, and saves the tool calls of one validated assistant
    /// message. Every exit gives each call a final outcome and attempts to save
    /// the batch, so a stopped turn keeps the effects already observed.
    async fn process_batch(
        &mut self,
        message: AssistantMessage,
    ) -> std::result::Result<(), PromptOutcome> {
        let mut batch = UncommittedAssistantBatch::new(message);
        match self.execute(&mut batch).await {
            Ok(()) => self
                .commit(batch.complete())
                .map_err(PromptOutcome::Storage),
            Err(outcome) => Err(self.complete_interrupted(batch, outcome)),
        }
    }

    /// Announces the calls, then runs them in order. Each outcome enters the
    /// batch before its ACP update is sent, so an update failure does not
    /// erase completed work.
    async fn execute(
        &mut self,
        batch: &mut UncommittedAssistantBatch,
    ) -> std::result::Result<(), PromptOutcome> {
        let calls = batch.message.tool_calls.clone();
        for call in &calls {
            self.presentation
                .send(convert::pending_tool_call(call))
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        for call in &calls {
            if self.cancellation.is_cancelled() {
                return Err(PromptOutcome::Cancelled);
            }
            let denial = self.request_permission(call).await?;
            let outcome = if let Some(message) = denial {
                ToolOutcome::Failed(message)
            } else {
                self.presentation
                    .send(convert::in_progress_tool_call_update(&call.call_id))
                    .map_err(PromptOutcome::AcpUpdate)?;
                // The dispatcher polls tools first, so a synchronous patch can finish
                // before it observes cancellation that arrived while sending the update.
                if self.cancellation.is_cancelled() {
                    return Err(PromptOutcome::Cancelled);
                }
                tools::execute(&self.tools, call, self.cancellation.cancelled()).await
            };
            let update = convert::finished_tool_call_update(call, &outcome);
            batch.outcomes.push(outcome);
            self.presentation
                .send(update)
                .map_err(PromptOutcome::AcpUpdate)?;
        }
        Ok(())
    }

    /// Requests Ask mode permission when the call needs it. `Some` holds the
    /// denial message.
    async fn request_permission(
        &self,
        call: &ToolCall,
    ) -> std::result::Result<Option<String>, PromptOutcome> {
        let permission = tools::permission(&self.tools, call);
        let denial = match &permission {
            tools::Permission::NotRequired => return Ok(None),
            tools::Permission::Command { .. } => "User denied permission to run this command.",
            tools::Permission::Input { .. } => "User denied permission to send this input.",
        };
        if self.mode == SessionMode::Auto {
            return Ok(None);
        }
        let response = tokio::select! {
            biased;
            () = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled),
            response = self.presentation.request_permission(
                call, &self.summary.workspace_path, &permission,
            ) => response.map_err(PromptOutcome::Permission)?,
        };
        match response.outcome {
            RequestPermissionOutcome::Selected(selected) => match selected.option_id.0.as_ref() {
                "approve" => Ok(None),
                "deny" => Ok(Some(denial.to_owned())),
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
    fn commit(&mut self, batch: AssistantBatch) -> io::Result<()> {
        self.store.append_batch(&self.summary.id, &batch)?;
        self.transcript.push(TranscriptEntry::AssistantBatch(batch));
        Ok(())
    }

    /// Gives every unstarted call an outcome saying why it did not run, saves
    /// the batch, sends the remaining updates, and returns the outcome that
    /// ends the turn.
    fn complete_interrupted(
        &mut self,
        mut batch: UncommittedAssistantBatch,
        mut outcome: PromptOutcome,
    ) -> PromptOutcome {
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
            PromptOutcome::Finished(_)
            | PromptOutcome::TokenLimit
            | PromptOutcome::Refused
            | PromptOutcome::OpenRouter(_)
            | PromptOutcome::Storage(_) => {
                unreachable!("{outcome} does not interrupt tool execution")
            }
        };
        let remaining = batch.fill_remaining(&placeholder);
        if let Err(error) = self.commit(batch.complete()) {
            outcome = PromptOutcome::Storage(io::Error::other(format!(
                "{error}; the batch was being completed because {outcome}"
            )));
        }
        if !matches!(outcome, PromptOutcome::AcpUpdate(_)) {
            for update in remaining {
                if let Err(error) = self.presentation.send(update) {
                    outcome = PromptOutcome::AcpUpdate(error);
                    break;
                }
            }
        }
        outcome
    }
}

impl PromptOutcome {
    fn into_output(self) -> Result<PromptOutput> {
        match self {
            Self::Finished(answer) => Ok(PromptOutput::Finished(answer)),
            Self::Cancelled => Ok(PromptOutput::Cancelled),
            Self::TokenLimit => Ok(PromptOutput::TokenLimit),
            Self::Refused => Ok(PromptOutput::Refused),
            Self::OpenRouter(error) | Self::Storage(error) => {
                Err(Error::into_internal_error(error))
            }
            Self::AcpUpdate(error) | Self::Permission(error) => Err(error),
        }
    }
}

#[cfg(test)]
mod tests {
    use std::{
        fs,
        sync::{Arc, Mutex},
    };

    use agent_client_protocol::schema::v1::ToolCallStatus;

    use super::*;
    use serde_json::json;

    use crate::{
        openrouter::{
            catalog,
            fixture::{
                DEFAULT_MODEL, Gate, Reply, Server, calls_reply, delta, sse, text_reply,
                tool_reply, usage,
            },
        },
        sessions::{EffortLevel, ModelUsage},
        system_prompt,
        tools::fixture::Workspace,
    };

    type Updates = Arc<Mutex<Vec<SessionUpdate>>>;

    struct Harness {
        server: Server,
        store: SessionStore,
        session_id: SessionId,
        cancellation: PromptCancellation,
        updates: Updates,
        workspace: Workspace,
        shell_processes: ShellProcesses,
    }

    impl Harness {
        async fn new(replies: Vec<Reply>) -> Self {
            Self::routed(vec![("", replies)]).await
        }

        /// A harness whose server routes each request by its first message
        /// after the system prompt, so the main agent and each subagent
        /// follow their own scripts.
        async fn routed(routes: Vec<(&str, Vec<Reply>)>) -> Self {
            let workspace = Workspace::new();
            let store = SessionStore::in_memory();
            let session_id = store.create(&workspace.0).unwrap().id;
            Self {
                server: Server::routed(routes).await,
                store,
                session_id,
                cancellation: PromptCancellation::new(),
                updates: Arc::default(),
                workspace,
                shell_processes: ShellProcesses::default(),
            }
        }

        /// Runs the prompt with an update closure that records every update
        /// and calls `on_update` before returning its result.
        async fn run(
            &self,
            input: &str,
            on_update: impl FnMut(&SessionUpdate) -> Result<()> + Send + 'static,
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
            on_update: impl FnMut(&SessionUpdate) -> Result<()> + Send + 'static,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            self.run_turn(user(input), settings, on_update).await
        }

        async fn run_turn(
            &self,
            turn_input: TurnInput,
            selected_settings: SessionSettings,
            mut on_update: impl FnMut(&SessionUpdate) -> Result<()> + Send + 'static,
        ) -> (Result<PromptOutput>, Vec<TranscriptEntry>) {
            let updates = self.updates.clone();
            let send_update = move |update: SessionUpdate| {
                updates.lock().unwrap().push(update.clone());
                on_update(&update)
            };
            let prompt = run(
                self.store.clone(),
                self.server.client(),
                PromptInput {
                    session_id: self.session_id.clone(),
                    turn_input,
                    selected_settings,
                    system_prompt: system_prompt::for_workspace(&self.workspace.0).unwrap(),
                    shell_processes: self.shell_processes.clone(),
                },
                self.cancellation.clone(),
                Presentation::observed(self.session_id.clone(), send_update),
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

        fn stored_child(&self, id: &str) -> Vec<TranscriptEntry> {
            self.store
                .read(&SessionId::new(id))
                .unwrap()
                .unwrap()
                .transcript
        }

        fn updates(&self) -> Vec<SessionUpdate> {
            self.updates.lock().unwrap().clone()
        }
    }

    /// A turn start saved by `Harness::run`: Default effort in Auto mode.
    fn turn(input: TurnInput) -> TranscriptEntry {
        turn_with(EffortLevel::Default, SessionMode::Auto, input)
    }

    fn turn_with(effort: EffortLevel, mode: SessionMode, input: TurnInput) -> TranscriptEntry {
        TranscriptEntry::TurnStart(TurnStart {
            effort,
            mode,
            ..TurnStart::test(input)
        })
    }

    fn user(text: &str) -> TurnInput {
        TurnInput::UserMessage(text.to_owned().into())
    }

    fn answer(text: &str) -> TranscriptEntry {
        TranscriptEntry::AssistantBatch(
            AssistantBatch::new(
                AssistantMessage {
                    text: text.to_owned(),
                    reasoning: String::new(),
                    tool_calls: vec![],
                    continuation_metadata: vec![],
                    usage: None,
                },
                vec![],
            )
            .unwrap(),
        )
    }

    fn calls(calls: &[(&str, &str)], outcomes: Vec<ToolOutcome>) -> TranscriptEntry {
        TranscriptEntry::AssistantBatch(
            AssistantBatch::new(
                AssistantMessage {
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
                },
                outcomes,
            )
            .unwrap(),
        )
    }

    /// The completed outcome of a shell call that printed `text`.
    fn printed(text: &str) -> ToolOutcome {
        ToolOutcome::Completed(format!(
            "Exit code: 0\n\nstdout:\n{text}\n\nstderr:\n(empty)"
        ))
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

    fn patch_outcome(transcript: &[TranscriptEntry]) -> &ToolOutcome {
        transcript
            .iter()
            .find_map(|entry| match entry {
                TranscriptEntry::AssistantBatch(batch) => batch.outcomes.first(),
                _ => None,
            })
            .expect("the patch call left one tool outcome")
    }

    fn sent_patch_call(harness: &Harness) -> bool {
        harness.updates().iter().any(
            move |update| matches!(update, SessionUpdate::ToolCall(call) if call.title == "Apply patch to 2 files"),
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

        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        assert_eq!(
            fs::read_to_string(harness.workspace.0.join("first")).unwrap(),
            "one\n"
        );
        assert_eq!(
            fs::read_to_string(harness.workspace.0.join("second")).unwrap(),
            "two\n"
        );

        let outcome = patch_outcome(&transcript);
        assert_eq!(*outcome, ToolOutcome::Completed(APPLIED.to_owned()));
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn cancelling_before_execution_leaves_the_workspace_untouched() {
        let harness = patch_harness("tool_calls").await;
        let cancel = harness.cancellation.clone();
        let (response, transcript) = harness
            .run("Apply the patch", move |update| {
                if matches!(update, SessionUpdate::ToolCallUpdate(update)
                    if update.fields.status == Some(ToolCallStatus::InProgress))
                {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap(), PromptOutput::Cancelled);
        assert_eq!(fs::read_dir(&harness.workspace.0).unwrap().count(), 0);
        let outcome = patch_outcome(&transcript);
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
                .run("Apply the patch", move |update| {
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
                assert_eq!(response.unwrap(), PromptOutput::Cancelled);
            }
            assert_eq!(
                fs::read_to_string(harness.workspace.0.join("first")).unwrap(),
                "one\n"
            );
            assert_eq!(
                *patch_outcome(&transcript),
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
        assert_eq!(transcript, vec![turn(user("Apply the patch"))]);
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

        let (response, transcript) = harness
            .run_with_settings(
                "Hi",
                SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default),
                |_| Ok(()),
            )
            .await;

        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        let request = &harness.server.requests()[0];
        assert_eq!(request["model"], DEFAULT_MODEL);
        assert!(request.get("reasoning").is_none());
        let TranscriptEntry::AssistantBatch(AssistantBatch {
            message: mut answered,
            ..
        }) = answer("Hello there.")
        else {
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
                turn_with(EffortLevel::Default, SessionMode::Ask, user("Hi")),
                TranscriptEntry::AssistantBatch(AssistantBatch::new(answered, vec![]).unwrap())
            ]
        );
        assert_eq!(harness.stored(), transcript);
        let updates = harness.updates();
        assert!(matches!(
            &updates[0],
            SessionUpdate::SessionInfoUpdate(info) if info.title.contains_value(&"Hi".to_owned())
        ));
        assert_eq!(
            updates.iter().map(describe).collect::<Vec<_>>(),
            vec!["info", "text", "usage"]
        );
        let SessionUpdate::UsageUpdate(reported) = updates.last().unwrap() else {
            unreachable!()
        };
        assert_eq!(
            (reported.used, reported.size),
            (150, catalog()[0].context_limit as u64)
        );
        assert_eq!(
            reported.cost,
            Some(agent_client_protocol::schema::v1::Cost::new(0.25, "USD"))
        );
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
                turn_input: user(&oversized),
                selected_settings: SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default),
                system_prompt: "system".to_owned(),
                shell_processes: ShellProcesses::default(),
            },
            PromptCancellation::new(),
            Presentation::headless(harness.session_id.clone()),
        );
        assert!(rejected.is_err());
        assert!(harness.stored().is_empty());
        let valid = "v".repeat(2_300_000);
        let (response, transcript) = harness.run(&valid, |_| Ok(())).await;
        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        assert_eq!(transcript.len(), 2);
        assert_eq!(
            harness.server.requests().len(),
            1,
            "a valid request above the automatic threshold runs when no prefix can be cut"
        );

        let directory =
            std::env::temp_dir().join(format!("ox-compaction-{}", uuid::Uuid::new_v4()));
        let database = directory.join("ox.db");
        let store = SessionStore::open(&database).unwrap();
        let id = store.create(&harness.workspace.0).unwrap().id;
        store
            .append_turn_start(&id, &TurnStart::test("unanswered ".repeat(180_000)))
            .unwrap();
        drop(store);
        let reopened = SessionStore::open(&database).unwrap();
        let before = reopened.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![text_reply("Accepted after reload")]).await;
        let input = |user_message: String| PromptInput {
            session_id: id.clone(),
            turn_input: user(&user_message),
            selected_settings: SessionSettings::new(DEFAULT_MODEL, EffortLevel::Default),
            system_prompt: "system".to_owned(),
            shell_processes: ShellProcesses::default(),
        };
        assert!(
            run(
                reopened.clone(),
                server.client(),
                input("b".repeat(1_100_000)),
                PromptCancellation::new(),
                Presentation::headless(id.clone()),
            )
            .is_err()
        );
        assert_eq!(reopened.read(&id).unwrap().unwrap().transcript, before);
        let accepted = run(
            reopened.clone(),
            server.client(),
            input("small".to_owned()),
            PromptCancellation::new(),
            Presentation::headless(id.clone()),
        )
        .unwrap();
        assert!(matches!(accepted.await.unwrap(), PromptOutput::Finished(_)));
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
            .append_turn_start(&harness.session_id, &TurnStart::test(old.clone()))
            .unwrap();
        let TranscriptEntry::AssistantBatch(batch) = answer("Earlier answer") else {
            unreachable!()
        };
        harness
            .store
            .append_batch(&harness.session_id, &batch)
            .unwrap();
        let (response, transcript) = harness.run("next request", |_| Ok(())).await;
        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        let requests = harness.server.requests();
        assert_eq!(requests.len(), 2);
        assert!(requests[0].get("tools").is_none());
        assert_eq!(
            requests[1]["messages"][1]["content"],
            "Compaction summary of earlier conversation:\nOlder work summarized."
        );
        assert_eq!(requests[1]["messages"][2]["content"], "next request");
        assert!(matches!(
            &transcript[3],
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
        let parameters = ModelRequestParameters::new(
            DEFAULT_MODEL,
            EffortLevel::Default,
            system,
            tools::Role::Main,
        )
        .unwrap();
        let automatic_threshold = compaction::budget(parameters.model).automatic_threshold;
        let base = "x".repeat(2_000_000);
        let prospective = vec![
            turn(user(&base)),
            answer("Earlier answer"),
            turn(user("next request")),
        ];
        let base_estimate = compaction::request_estimate(&parameters, &prospective);
        let old = "x".repeat(2_000_000 + (automatic_threshold - base_estimate - 50) * 3);
        between
            .store
            .append_turn_start(
                &between.session_id,
                &TurnStart {
                    mode: SessionMode::Auto,
                    ..TurnStart::test(user(&old))
                },
            )
            .unwrap();
        let TranscriptEntry::AssistantBatch(batch) = answer("Earlier answer") else {
            unreachable!()
        };
        between
            .store
            .append_batch(&between.session_id, &batch)
            .unwrap();
        let (response, transcript) = between.run("next request", |_| Ok(())).await;
        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
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
            .append_turn_start(
                &harness.session_id,
                &TurnStart::test("history ".repeat(4000)),
            )
            .unwrap();
        let TranscriptEntry::AssistantBatch(batch) = answer("Earlier answer") else {
            unreachable!()
        };
        harness
            .store
            .append_batch(&harness.session_id, &batch)
            .unwrap();
        let (response, transcript) = harness.run("next request", |_| Ok(())).await;
        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        assert_eq!(harness.server.requests().len(), 3);
        assert!(matches!(
            &transcript[3],
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
            .append_turn_start(&no_reduction.session_id, &TurnStart::test("old".to_owned()))
            .unwrap();
        let TranscriptEntry::AssistantBatch(batch) = answer("done") else {
            unreachable!()
        };
        no_reduction
            .store
            .append_batch(&no_reduction.session_id, &batch)
            .unwrap();
        let (response, transcript) = no_reduction.run("next", |_| Ok(())).await;
        assert!(response.is_err());
        assert_eq!(no_reduction.server.requests().len(), 2);
        assert_eq!(transcript.len(), 3, "the new user message remains saved");

        for reply in [
            Reply::Status(500, "server unavailable".to_owned()),
            Reply::Stream(sse(&[delta(
                json!({"role":"assistant", "content":"partial"}),
                Some("length"),
            )])),
        ] {
            let harness = Harness::new(vec![reply]).await;
            let (response, transcript) = harness.run("small", |_| Ok(())).await;
            assert!(response.is_err() || response.unwrap() == PromptOutput::TokenLimit);
            assert_eq!(harness.server.requests().len(), 1);
            assert!(
                transcript
                    .iter()
                    .all(|entry| !matches!(entry, TranscriptEntry::CompactionCheckpoint(_)))
            );
        }
    }

    #[tokio::test]
    async fn each_turn_saves_and_sends_its_own_model_and_effort() {
        let harness = Harness::new(vec![text_reply("First"), text_reply("Second")]).await;
        let selected = Arc::new(Mutex::new(EffortLevel::Low));
        let changed = selected.clone();
        let first_model = catalog()[1].id.as_str();
        let second_model = catalog()[2].id.as_str();
        let first_settings = SessionSettings::new(first_model, *selected.lock().unwrap());

        let (first, _) = harness
            .run_with_settings("one", first_settings, move |update| {
                if matches!(update, SessionUpdate::SessionInfoUpdate(_)) {
                    *changed.lock().unwrap() = EffortLevel::XHigh;
                }
                Ok(())
            })
            .await;
        assert!(matches!(first.unwrap(), PromptOutput::Finished(_)));
        let second_settings = SessionSettings::new(second_model, *selected.lock().unwrap());
        let (second, transcript) = harness
            .run_with_settings("two", second_settings, |_| Ok(()))
            .await;
        assert!(matches!(second.unwrap(), PromptOutput::Finished(_)));
        assert_eq!(
            transcript,
            vec![
                TranscriptEntry::TurnStart(TurnStart {
                    model: first_model.to_owned(),
                    effort: EffortLevel::Low,
                    ..TurnStart::test(user("one"))
                }),
                answer("First"),
                TranscriptEntry::TurnStart(TurnStart {
                    model: second_model.to_owned(),
                    effort: EffortLevel::XHigh,
                    ..TurnStart::test(user("two"))
                }),
                answer("Second"),
            ]
        );
        let requests = harness.server.requests();
        assert_eq!(requests[0]["model"], first_model);
        assert_eq!(requests[1]["model"], second_model);
        assert_eq!(requests[0]["reasoning"]["effort"], "low");
        assert_eq!(requests[1]["reasoning"]["effort"], "xhigh");
    }

    #[tokio::test]
    async fn invalid_prompt_startup_sends_no_request_or_user_message() {
        let workspace = Workspace::new();
        let store = SessionStore::in_memory();
        let server = Server::start(vec![]).await;
        let input = |session_id: &SessionId, turn_input: TurnInput, model: &str| PromptInput {
            session_id: session_id.clone(),
            turn_input,
            selected_settings: SessionSettings::new(model, EffortLevel::Default),
            system_prompt: system_prompt::for_workspace(&workspace.0).unwrap(),
            shell_processes: ShellProcesses::default(),
        };
        let start = |client: openrouter::Client, input: PromptInput| {
            let session_id = input.session_id.clone();
            run(
                store.clone(),
                client,
                input,
                PromptCancellation::new(),
                Presentation::headless(session_id),
            )
        };
        let without_images =
            "the selected model does not accept images; choose a model that accepts images";
        let missing = SessionId::new("missing");
        let missing_run = start(
            server.client(),
            input(&missing, user("not saved"), DEFAULT_MODEL),
        );
        assert!(missing_run.is_err());

        let image_input = TurnInput::UserMessage(crate::sessions::UserMessage {
            parts: vec![crate::sessions::UserMessagePart::Image(
                crate::sessions::ImageAttachment {
                    data: "aGVsbG8=".to_owned(),
                    mime_type: "image/png".to_owned(),
                },
            )],
        });
        let image_session = store.create(&workspace.0).unwrap().id;
        let Err(error) = start(
            server.client(),
            input(&image_session, image_input.clone(), DEFAULT_MODEL),
        ) else {
            panic!("an image prompt to a model without image input is rejected");
        };
        assert_eq!(error.data.unwrap(), without_images);
        assert!(
            store
                .read(&image_session)
                .unwrap()
                .unwrap()
                .transcript
                .is_empty()
        );
        assert!(server.requests().is_empty());

        let vision_server = Server::start(vec![text_reply("I see it.")]).await;
        let vision_session = store.create(&workspace.0).unwrap().id;
        let vision_model = catalog()[1].id.as_str();
        let vision_run = start(
            vision_server.client(),
            input(&vision_session, image_input.clone(), vision_model),
        )
        .unwrap();
        assert_eq!(
            vision_run.await.unwrap(),
            PromptOutput::Finished("I see it.".to_owned())
        );
        let saved = store.read(&vision_session).unwrap().unwrap().transcript;
        assert_eq!(
            saved[0],
            TranscriptEntry::TurnStart(TurnStart {
                model: vision_model.to_owned(),
                ..TurnStart::test(image_input)
            })
        );
        assert_eq!(
            vision_server.requests()[0]["messages"][1]["content"][0]["image_url"]["url"],
            "data:image/png;base64,aGVsbG8="
        );

        // The earlier image would be sent with a text prompt too.
        let Err(error) = start(
            vision_server.client(),
            input(&vision_session, user("Describe it again"), DEFAULT_MODEL),
        ) else {
            panic!("a model without image input cannot continue a session with an image");
        };
        assert_eq!(error.data.unwrap(), without_images);
        assert_eq!(
            store.read(&vision_session).unwrap().unwrap().transcript,
            saved
        );
        assert_eq!(vision_server.requests().len(), 1);
    }

    #[tokio::test]
    async fn several_tool_calls_in_one_message_get_ordered_results() {
        let harness = Harness::new(vec![
            tool_reply(&[("call-1", "printf Chicago"), ("call-2", "printf Denver")]),
            text_reply("Both sunny."),
        ])
        .await;

        let (response, transcript) = harness.run("Weather?", |_| Ok(())).await;

        assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
        assert_eq!(
            transcript,
            vec![
                turn(user("Weather?")),
                calls(
                    &[("call-1", "printf Chicago"), ("call-2", "printf Denver")],
                    vec![printed("Chicago"), printed("Denver")]
                ),
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
                "usage",
                "text",
                "usage",
            ]
        );
        let second_request = &harness.server.requests()[1];
        assert_eq!(second_request["messages"].as_array().unwrap().len(), 5);
        for (index, call_id, text) in [(3, "call-1", "Chicago"), (4, "call-2", "Denver")] {
            assert_eq!(
                second_request["messages"][index],
                json!({
                    "role": "tool",
                    "tool_call_id": call_id,
                    "content": printed(text).text(),
                })
            );
        }
    }

    #[tokio::test]
    async fn a_background_start_saves_one_outcome_while_its_command_keeps_running() {
        let not_started = ToolOutcome::Failed(
            "Not started: the client connection failed before this tool ran.".to_owned(),
        );
        for failing in [None, Some("start completed")] {
            let harness = Harness::new(vec![
                calls_reply(&[
                    (
                        "start",
                        tools::SHELL,
                        json!({"command":"exec sleep 30","background":true}),
                    ),
                    ("next", tools::SHELL, json!({"command":"printf next"})),
                ]),
                text_reply("Started."),
            ])
            .await;

            let (response, transcript) = harness
                .run("Start the server", move |update| match failing {
                    Some(failing) if describe(update) == failing => {
                        Err(Error::internal_error().data("connection closed"))
                    }
                    _ => Ok(()),
                })
                .await;

            let process = harness.shell_processes.list(&harness.session_id).remove(0);
            assert_eq!(
                process.state(),
                crate::shell_processes::State::Running,
                "{failing:?}"
            );
            let Some(TranscriptEntry::AssistantBatch(batch)) = transcript.get(1) else {
                panic!("{failing:?}: the batch was saved");
            };
            let started = ToolOutcome::Completed(format!(
                "Started shell process {}.\nThe command is running in the background. This confirms that it started, not that it finished or is ready. Use shell_process to read its output, write to its stdin, or stop it.",
                process.id()
            ));
            match failing {
                None => {
                    assert!(matches!(response.unwrap(), PromptOutput::Finished(_)));
                    assert_eq!(batch.outcomes, [started, printed("next")]);
                }
                Some(_) => {
                    assert!(response.is_err());
                    assert_eq!(batch.outcomes, [started, not_started.clone()]);
                }
            }
            harness.shell_processes.shutdown().await;
        }
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
            .run("Weather?", move |update| {
                if finished_tool_call_update_for(update, "call-1") {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap(), PromptOutput::Cancelled);
        assert_eq!(
            transcript,
            vec![
                turn(user("Weather?")),
                calls(
                    &[("call-1", "printf Chicago"), ("call-2", "printf Denver")],
                    vec![
                        printed("Chicago"),
                        ToolOutcome::Cancelled(
                            "Cancelled before this tool was started.".to_owned()
                        )
                    ]
                ),
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
            .run("Hi", move |update| {
                if matches!(update, SessionUpdate::AgentMessageChunk(_)) {
                    cancel.cancel();
                }
                Ok(())
            })
            .await;

        assert_eq!(response.unwrap(), PromptOutput::Cancelled);
        assert_eq!(transcript, vec![turn(user("Hi"))]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn a_failed_batch_append_leaves_the_transcript_unchanged() {
        let harness = Harness::new(vec![text_reply("Hello there.")]).await;
        harness.store.with_connection(|connection| {
            connection
                .execute_batch(
                    "CREATE TRIGGER refuse BEFORE INSERT ON transcript_entries
                     WHEN NEW.kind = 'assistant_batch'
                     BEGIN SELECT RAISE(ABORT, 'disk full'); END;",
                )
                .unwrap()
        });

        let (response, transcript) = harness.run("Hi", |_| Ok(())).await;

        let error = response.unwrap_err();
        assert!(
            error.to_string().contains("disk full") || format!("{error:?}").contains("disk full")
        );
        assert_eq!(transcript, vec![turn(user("Hi"))]);
        assert_eq!(harness.stored(), transcript);
    }

    #[tokio::test]
    async fn update_failures_during_a_batch_still_commit_it() {
        let not_started = || {
            ToolOutcome::Failed(
                "Not started: the client connection failed before this tool ran.".to_owned(),
            )
        };
        for (failing, outcomes, sent) in [
            (
                "call-2 pending",
                vec![not_started(), not_started()],
                vec!["info", "call-1 pending", "call-2 pending"],
            ),
            (
                "call-1 completed",
                vec![printed("Chicago"), not_started()],
                vec![
                    "info",
                    "call-1 pending",
                    "call-2 pending",
                    "call-1 running",
                    "call-1 completed",
                ],
            ),
        ] {
            let harness = Harness::new(vec![tool_reply(&[
                ("call-1", "printf Chicago"),
                ("call-2", "printf Denver"),
            ])])
            .await;

            let (response, transcript) = harness
                .run("Weather?", move |update| {
                    if describe(update) == failing {
                        return Err(Error::internal_error().data("connection closed"));
                    }
                    Ok(())
                })
                .await;

            assert!(response.is_err(), "{failing}");
            assert_eq!(
                transcript,
                vec![
                    turn(user("Weather?")),
                    calls(
                        &[("call-1", "printf Chicago"), ("call-2", "printf Denver")],
                        outcomes
                    ),
                ],
                "{failing}"
            );
            assert_eq!(harness.stored(), transcript);
            assert_eq!(
                harness.updates().iter().map(describe).collect::<Vec<_>>(),
                sent,
                "nothing more is attempted after sending an update fails"
            );
        }
    }

    /// The subagent ID a start result reports.
    fn started_subagent(text: &str) -> Option<String> {
        let (id, _) = text.strip_prefix("Started subagent ")?.split_once('.')?;
        Some(id.to_owned())
    }

    /// The subagent IDs that start results in `request` report, in order.
    fn started_ids(request: &serde_json::Value) -> Vec<String> {
        request["messages"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|message| started_subagent(message["content"].as_str()?))
            .collect()
    }

    /// The subagent ID a start outcome saved in `transcript` reports.
    fn started_id(transcript: &[TranscriptEntry]) -> String {
        transcript
            .iter()
            .find_map(|entry| match entry {
                TranscriptEntry::AssistantBatch(batch) => batch
                    .outcomes
                    .iter()
                    .find_map(|outcome| started_subagent(outcome.text())),
                _ => None,
            })
            .expect("a subagent started")
    }

    fn final_answer(subagent_id: &str, text: &str) -> crate::sessions::AgentMessage {
        crate::sessions::AgentMessage {
            subagent_id: subagent_id.to_owned(),
            content: crate::sessions::AgentMessageContent::FinalAnswer(text.to_owned()),
        }
    }

    fn start_call(id: &'static str, task: &str) -> (&'static str, &'static str, serde_json::Value) {
        (id, tools::START_SUBAGENT, json!({ "prompt": task }))
    }

    fn wait_call(id: &'static str) -> (&'static str, &'static str, serde_json::Value) {
        (id, tools::WAIT, json!({ "seconds": 600 }))
    }

    fn answer_with_cost(text: &str, cost: f64) -> Reply {
        Reply::Stream(sse(&[
            delta(json!({ "role": "assistant", "content": text }), None),
            delta(json!({}), Some("stop")),
            usage(10, 5, cost),
        ]))
    }

    async fn wait_for_path(path: &std::path::Path) {
        tokio::time::timeout(std::time::Duration::from_secs(10), async {
            while !path.exists() {
                tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            }
        })
        .await
        .unwrap_or_else(|_| panic!("{} was not created", path.display()));
    }

    #[tokio::test]
    async fn subagents_run_concurrently_and_their_final_answers_reach_the_main_agent() {
        let (a, b) = (Gate::new(), Gate::new());
        let harness = Harness::routed(vec![
            ("Task A", vec![a.hold(text_reply("A found it."))]),
            ("Task B", vec![b.hold(answer_with_cost("B found it.", 0.5))]),
            (
                "Coordinate",
                vec![
                    calls_reply(&[
                        start_call("start-a", "Task A: find the parser."),
                        start_call("start-b", "Task B: find the tests."),
                    ]),
                    calls_reply(&[wait_call("wait-1")]),
                    calls_reply(&[wait_call("wait-2")]),
                    answer_with_cost("Both done.", 0.25),
                ],
            ),
        ])
        .await;
        fs::write(harness.workspace.0.join("AGENTS.md"), "Answer in French.\n").unwrap();
        let server = &harness.server;
        let driver = async {
            // Both subagents' requests are in flight before either returns.
            server.wait_for_requests("Task A", 1).await;
            server.wait_for_requests("Task B", 1).await;
            server.wait_for_requests("Coordinate", 2).await;
            b.open();
            server.wait_for_requests("Coordinate", 3).await;
            a.open();
        };
        let ((response, transcript), ()) =
            tokio::join!(harness.run("Coordinate the search.", |_| Ok(())), driver);

        assert_eq!(
            response.unwrap(),
            PromptOutput::Finished("Both done.".to_owned())
        );
        let [id_a, id_b] =
            <[String; 2]>::try_from(started_ids(&server.requests_for("Coordinate")[1])).unwrap();
        let outcomes = |index: usize| match &transcript[index] {
            TranscriptEntry::AssistantBatch(batch) => batch.outcomes.clone(),
            other => panic!("entry {index} is {other:?}"),
        };
        assert_eq!(
            outcomes(2),
            [ToolOutcome::Completed(format!(
                "1 subagent message arrived; it follows this result.\n\n{id_a}: busy\n{id_b}: idle"
            ))]
        );
        assert_eq!(
            transcript[3],
            TranscriptEntry::AgentMessages(vec![final_answer(&id_b, "B found it.")])
        );
        assert_eq!(
            transcript[5],
            TranscriptEntry::AgentMessages(vec![final_answer(&id_a, "A found it.")])
        );
        assert_eq!(transcript.len(), 7);
        let main_requests = server.requests_for("Coordinate");
        for (request, id, text) in [(2, &id_b, "B found it."), (3, &id_a, "A found it.")] {
            assert_eq!(
                main_requests[request]["messages"]
                    .as_array()
                    .unwrap()
                    .last()
                    .unwrap(),
                &json!({
                    "role": "user",
                    "content": format!("Final answer from subagent {id}:\n{text}"),
                })
            );
        }

        // Each child starts fresh with the inherited settings and instructions.
        let main_prompt = system_prompt::for_workspace(&harness.workspace.0).unwrap();
        for (id, marker, task) in [
            (&id_a, "Task A", "Task A: find the parser."),
            (&id_b, "Task B", "Task B: find the tests."),
        ] {
            let child = harness.stored_child(id);
            assert_eq!(child[0], turn(user(task)));
            assert_eq!(child.len(), 2);
            let request = &server.requests_for(marker)[0];
            assert_eq!(
                request["messages"],
                json!([
                    {"role": "system", "content": system_prompt::for_subagent(&main_prompt)},
                    {"role": "user", "content": task},
                ])
            );
            assert!(main_prompt.contains("Answer in French."));
            assert_eq!(
                request["tools"].as_array().unwrap().len(),
                tools::schemas(tools::Role::Subagent).len()
            );
        }

        // Only the main agent reports, including once after its subagents stop.
        let updates = harness.updates();
        assert!(!updates.iter().any(|update| matches!(update,
            SessionUpdate::AgentMessageChunk(chunk) if format!("{chunk:?}").contains("found it"))));
        let usages: Vec<_> = updates
            .iter()
            .filter_map(|update| match update {
                SessionUpdate::UsageUpdate(usage) => Some(usage.cost.clone()),
                _ => None,
            })
            .collect();
        assert_eq!(usages.len(), 5);
        assert_eq!(
            usages.last().unwrap(),
            &Some(agent_client_protocol::schema::v1::Cost::new(0.75, "USD"))
        );
    }

    #[tokio::test]
    async fn published_subagent_messages_supersede_a_finished_answer() {
        let (child_gate, main_gate, never) = (Gate::new(), Gate::new(), Gate::new());
        let harness = Harness::routed(vec![
            (
                "Task S",
                vec![
                    child_gate.hold(text_reply("Child answer.")),
                    never.hold(text_reply("Unused.")),
                ],
            ),
            (
                "Main task",
                vec![
                    calls_reply(&[start_call("start", "Task S: check the parser.")]),
                    Reply::from(|request| {
                        let id = started_ids(request).remove(0);
                        calls_reply(&[(
                            "send",
                            tools::SEND_MESSAGE,
                            json!({ "subagent_id": id, "message": "Keep going." }),
                        )])
                    }),
                    main_gate.hold(text_reply("Premature answer.")),
                    text_reply("Final answer."),
                ],
            ),
        ])
        .await;
        let server = &harness.server;
        let driver = async {
            server.wait_for_requests("Main task", 3).await;
            child_gate.open();
            server.wait_for_requests("Task S", 2).await;
            main_gate.open();
        };
        let ((response, transcript), ()) =
            tokio::join!(harness.run("Main task", |_| Ok(())), driver);
        assert_eq!(
            response.unwrap(),
            PromptOutput::Finished("Final answer.".to_owned())
        );
        let id = started_id(&transcript);
        assert_eq!(
            transcript[3..],
            [
                answer("Premature answer."),
                TranscriptEntry::AgentMessages(vec![final_answer(&id, "Child answer.")]),
                answer("Final answer."),
            ]
        );
        let child = harness.stored_child(&id);
        assert_eq!(
            child[..2],
            [
                turn(user("Task S: check the parser.")),
                answer("Child answer.")
            ]
        );
        assert_eq!(child[2], turn(user("Keep going.")));
        assert_eq!(child.len(), 3, "the follow-up turn was cancelled in flight");
    }

    #[tokio::test]
    async fn dropping_the_prompt_cancels_its_subagents_which_still_save_their_own_batches() {
        let never = Gate::new();
        let harness = Harness::routed(vec![
            (
                "Task D",
                vec![tool_reply(&[("sleeper", "touch running; sleep 30")])],
            ),
            (
                "Start",
                vec![
                    calls_reply(&[start_call("start", "Task D: a long job.")]),
                    never.hold(text_reply("Never.")),
                ],
            ),
        ])
        .await;
        let running = harness.workspace.0.join("running");
        tokio::select! {
            _ = harness.run("Start the job.", |_| Ok(())) => panic!("the prompt finished"),
            () = async {
                wait_for_path(&running).await;
                harness.server.wait_for_requests("Start", 2).await;
            } => {}
        }

        let transcript = harness.stored();
        assert_eq!(transcript.len(), 2);
        let id = started_id(&transcript);
        let child = tokio::time::timeout(std::time::Duration::from_secs(10), async {
            loop {
                let child = harness.stored_child(&id);
                if child.len() == 2 {
                    return child;
                }
                tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("the cancelled child saves its interrupted batch");
        assert!(matches!(&child[1], TranscriptEntry::AssistantBatch(batch)
            if matches!(batch.outcomes[..], [ToolOutcome::Cancelled(_)])));
        assert_eq!(harness.stored(), transcript);
    }
}
