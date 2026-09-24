//! The ACP connection: request handlers over one shared process state.

pub(crate) mod convert;
pub(crate) mod operations;
mod prompt;

use std::{
    collections::HashMap,
    error::Error as StdError,
    io::{self, ErrorKind},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

use agent_client_protocol::{
    Agent, Channel, Client, ConnectTo, ConnectionTo, Error, JsonRpcResponse, Responder, Result,
    Stdio,
    schema::ProtocolVersion,
    schema::v1::{
        AgentAuthCapabilities, AgentCapabilities, AuthMethod, AuthMethodTerminal, AvailableCommand,
        AvailableCommandInput, AvailableCommandsUpdate, CancelNotification, DeleteSessionRequest,
        DeleteSessionResponse, InitializeRequest, InitializeResponse, ListSessionsRequest,
        ListSessionsResponse, LoadSessionRequest, LoadSessionResponse, LogoutCapabilities,
        LogoutRequest, LogoutResponse, NewSessionRequest, NewSessionResponse, PromptCapabilities,
        PromptRequest, PromptResponse, SessionCapabilities, SessionConfigOption,
        SessionConfigOptionCategory, SessionConfigOptionValue, SessionConfigSelectOption,
        SessionDeleteCapabilities, SessionId, SessionInfo, SessionListCapabilities,
        SessionNotification, SessionUpdate, SetSessionConfigOptionRequest,
        SetSessionConfigOptionResponse, StopReason, UnstructuredCommandInput,
    },
};
use futures::StreamExt;

use crate::{
    auth,
    cancellation::PromptCancellation,
    compaction, hooks,
    openrouter::{self, ModelRequestParameters},
    sessions::{
        self, EffortLevel, SessionMode, SessionSettings, SessionStore, SessionSummary,
        SkillInvocation, TurnInput, UserMessage, UserMessagePart,
    },
    settings::{self, Settings},
    shell_processes::ShellProcesses,
    skills::{self, Skill},
    system_prompt,
};
use operations::{OperationGuard, SessionOperations};

fn config_options(settings: &SessionSettings) -> Vec<SessionConfigOption> {
    let model = openrouter::catalog_model(&settings.model)
        .expect("a session model comes from the model catalog");
    let model_option = |model: &openrouter::CatalogModel| {
        let option = SessionConfigSelectOption::new(model.id.clone(), model.name.clone());
        if model.accepts_images {
            option.description("Accepts images")
        } else {
            option
        }
    };
    let models: Vec<_> = openrouter::catalog().iter().map(model_option).collect();
    vec![
        SessionConfigOption::select("model", "Model", settings.model.clone(), models)
            .category(SessionConfigOptionCategory::Model),
        SessionConfigOption::select(
            "effort",
            "Effort",
            settings.effort.id(),
            model
                .efforts
                .iter()
                .map(|effort| SessionConfigSelectOption::new(effort.id(), effort.name()))
                .collect::<Vec<_>>(),
        )
        .category(SessionConfigOptionCategory::ThoughtLevel),
        SessionConfigOption::select(
            "mode",
            "Mode",
            settings.mode.id(),
            SessionMode::ALL
                .into_iter()
                .map(|mode| {
                    SessionConfigSelectOption::new(mode.id(), mode.name())
                        .description(mode.description())
                })
                .collect::<Vec<_>>(),
        )
        .category(SessionConfigOptionCategory::Mode),
    ]
}

/// The built-in `/compact` followed by every skill in the catalog.
fn available_commands(skills: &[Skill]) -> SessionUpdate {
    let mut commands = vec![AvailableCommand::new(
        "compact",
        "Compact the conversation context.",
    )];
    commands.extend(skills.iter().map(|skill| {
        let command = AvailableCommand::new(skill.name.clone(), skill.description.clone());
        match &skill.argument_hint {
            Some(hint) => command.input(AvailableCommandInput::Unstructured(
                UnstructuredCommandInput::new(hint.clone()),
            )),
            None => command,
        }
    }));
    SessionUpdate::AvailableCommandsUpdate(AvailableCommandsUpdate::new(commands))
}

/// What a prompt request asks for.
#[derive(Debug, PartialEq)]
enum Dispatch {
    Compact,
    Skill {
        invocation: SkillInvocation,
        hook_source: Box<hooks::HookSource>,
    },
    UserMessage(UserMessage),
}

/// A prompt whose first word is `/compact` or `/<name>` for a catalog skill is
/// a command; the rest of its text, trimmed, is literal skill arguments. Any
/// other text is a user message.
fn dispatch(message: UserMessage, skills: &[Skill]) -> Dispatch {
    let prompt_text = message.text();
    let text = prompt_text.trim();
    let (word, rest) = text.split_once(char::is_whitespace).unwrap_or((text, ""));
    if word == "/compact" && !message.has_images() {
        return Dispatch::Compact;
    }
    let Some(skill) = word
        .strip_prefix('/')
        .and_then(|name| skills.iter().find(|skill| skill.name == name))
    else {
        return Dispatch::UserMessage(message);
    };
    let arguments = rest.trim().to_owned();
    Dispatch::Skill {
        hook_source: Box::new(hooks::HookSource {
            hooks: skill.hooks.clone(),
            skill: Some(skill.name.clone()),
            directory: skill.directory.clone(),
        }),
        invocation: SkillInvocation {
            name: skill.name.clone(),
            arguments,
            instructions: skill.instructions.clone(),
            images: message
                .parts
                .into_iter()
                .filter_map(|part| match part {
                    UserMessagePart::Image(image) => Some(image),
                    UserMessagePart::Text(_) => None,
                })
                .collect(),
        },
    }
}

#[derive(Clone)]
struct ServerState {
    store: SessionStore,
    /// The settings file's settings. Each session applies its workspace
    /// settings file to them when it becomes active.
    settings: Settings,
    /// The home directory read at startup, which holds the user skills
    /// directories.
    home: PathBuf,
    openrouter: Arc<Mutex<Option<openrouter::Client>>>,
    operations: SessionOperations,
    /// The sessions created or loaded in this process. Only an active session
    /// can be configured or prompted.
    active: Arc<Mutex<HashMap<SessionId, ActiveSession>>>,
}

/// Process state for one active session.
#[derive(Clone)]
struct ActiveSession {
    /// The latest ACP selections. A prompt copies these before it starts, so
    /// changes during the prompt apply to the next turn.
    selections: SessionSettings,
    /// The complete system prompt assembled when the session became active.
    system_prompt: String,
    /// The skill catalog loaded when the session became active.
    skills: Vec<Skill>,
    /// Background commands started in this session. They outlive prompt runs
    /// and repeated loads, and stop when the session is deleted or the
    /// connection shuts down.
    shell_processes: ShellProcesses,
}

impl ServerState {
    fn new(store: SessionStore, settings: Settings, home: PathBuf) -> Self {
        Self {
            store,
            settings,
            home,
            openrouter: Arc::default(),
            operations: SessionOperations::default(),
            active: Arc::default(),
        }
    }

    /// Credentials are read on first use, so the process serves listing,
    /// deletion, and terminal login before a key exists, and a key saved by
    /// `ox auth login` is picked up by the next request without a restart.
    fn openrouter_client(&self) -> Result<openrouter::Client> {
        let mut slot = self
            .openrouter
            .lock()
            .expect("OpenRouter client mutex poisoned");
        if let Some(client) = slot.as_ref() {
            return Ok(client.clone());
        }
        let api_key = auth::api_key()
            .map_err(Error::into_internal_error)?
            .ok_or_else(Error::auth_required)?;
        let client = openrouter::Client::new(api_key);
        *slot = Some(client.clone());
        Ok(client)
    }

    async fn compact_session(
        &self,
        session_id: &SessionId,
        active: ActiveSession,
        cancellation: &PromptCancellation,
        mut send_update: impl FnMut(SessionUpdate) -> Result<()>,
    ) -> Result<PromptResponse> {
        let stored = self
            .store
            .read(session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| not_found(session_id))?;
        if !compaction::has_candidate(&stored.transcript) {
            return Ok(PromptResponse::new(StopReason::EndTurn));
        }
        let client = self.openrouter_client()?;
        // Compaction prepares the next turn, which uses the selected model.
        let parameters = ModelRequestParameters::new(
            &active.selections.model,
            active.selections.effort,
            active.system_prompt,
        )
        .map_err(Error::into_internal_error)?;
        let mut transcript = stored.transcript;
        match compaction::compact(
            &self.store,
            &client,
            cancellation,
            session_id,
            &parameters,
            &mut transcript,
        )
        .await
        {
            Ok(compacted) => {
                if compacted && let Some(update) = convert::usage_update(&transcript, &parameters) {
                    send_update(update)?;
                }
                Ok(PromptResponse::new(if cancellation.is_cancelled() {
                    StopReason::Cancelled
                } else {
                    StopReason::EndTurn
                }))
            }
            Err(error) if error.kind() == ErrorKind::Interrupted => {
                Ok(PromptResponse::new(StopReason::Cancelled))
            }
            Err(error) => Err(Error::into_internal_error(error)),
        }
    }

    /// Loads the skill catalog for a session becoming active, writing each
    /// skipped definition to stderr.
    fn skill_catalog(&self, workspace_path: &Path) -> Vec<Skill> {
        let loaded = skills::load(&self.home, workspace_path);
        for message in loaded.skipped {
            eprintln!("{message}");
        }
        loaded.skills
    }

    /// The session settings of a new session in `workspace_path`.
    fn default_settings(&self, workspace_path: &Path) -> Result<SessionSettings> {
        let settings = self
            .settings
            .for_workspace(workspace_path)
            .map_err(Error::into_internal_error)?;
        Ok(SessionSettings::new(
            settings.default_model,
            EffortLevel::Default,
        ))
    }

    fn new_session(&self, request: &NewSessionRequest) -> Result<NewSessionResponse> {
        self.openrouter_client()?;
        let system_prompt =
            system_prompt::for_workspace(&request.cwd).map_err(Error::into_internal_error)?;
        let skills = self.skill_catalog(&request.cwd);
        let settings = self.default_settings(&request.cwd)?;
        let summary = self.store.create(&request.cwd).map_err(store_error)?;
        self.activate(
            summary.id.clone(),
            ActiveSession {
                selections: settings.clone(),
                system_prompt,
                skills,
                shell_processes: ShellProcesses::default(),
            },
        );
        Ok(NewSessionResponse::new(summary.id).config_options(config_options(&settings)))
    }

    fn set_config_option(
        &self,
        request: &SetSessionConfigOptionRequest,
    ) -> Result<SetSessionConfigOptionResponse> {
        let SessionConfigOptionValue::ValueId { value } = &request.value else {
            return Err(Error::invalid_params().data("every configuration option is a selector"));
        };
        let mut active = self.active.lock().expect("active sessions mutex poisoned");
        let selections = &mut active
            .get_mut(&request.session_id)
            .ok_or_else(|| inactive(&request.session_id))?
            .selections;
        match request.config_id.0.as_ref() {
            "model" => {
                let model = openrouter::catalog_model(value.0.as_ref()).ok_or_else(|| {
                    Error::invalid_params().data(format!(
                        "{} is not a choice of configuration option {}",
                        value, request.config_id
                    ))
                })?;
                selections.model.clone_from(&model.id);
                if !model.supports(selections.effort) {
                    selections.effort = EffortLevel::Default;
                }
            }
            "effort" => {
                let model = openrouter::catalog_model(&selections.model)
                    .expect("a session model comes from the model catalog");
                selections.effort = EffortLevel::from_id(value.0.as_ref())
                    .filter(|effort| model.supports(*effort))
                    .ok_or_else(|| {
                        Error::invalid_params().data(format!(
                            "{} is not a choice of configuration option {}",
                            value, request.config_id
                        ))
                    })?;
            }
            "mode" => {
                selections.mode = SessionMode::from_id(value.0.as_ref()).ok_or_else(|| {
                    Error::invalid_params().data(format!(
                        "{} is not a choice of configuration option {}",
                        value, request.config_id
                    ))
                })?;
            }
            _ => {
                return Err(Error::invalid_params()
                    .data(format!("no configuration option {}", request.config_id)));
            }
        }
        let options = config_options(selections);
        drop(active);
        Ok(SetSessionConfigOptionResponse::new(options))
    }

    fn load_session(
        &self,
        request: &LoadSessionRequest,
        mut send_update: impl FnMut(SessionUpdate) -> Result<()>,
    ) -> Result<LoadSessionResponse> {
        self.openrouter_client()?;
        let stored = self
            .store
            .read(&request.session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| not_found(&request.session_id))?;
        if stored.summary.workspace_path.as_os_str() != request.cwd.as_os_str() {
            return Err(Error::invalid_params().data(format!(
                "session {} belongs to workspace {}, not {}",
                request.session_id,
                stored.summary.workspace_path.display(),
                request.cwd.display()
            )));
        }
        let saved_settings =
            stored.saved_settings(&self.default_settings(&stored.summary.workspace_path)?);
        // A repeated load keeps the system prompt, skill catalog, and shell
        // processes of the first load.
        let (system_prompt, skills, shell_processes) =
            match self.active_session(&request.session_id) {
                Some(active) => (active.system_prompt, active.skills, active.shell_processes),
                None => (
                    system_prompt::for_workspace(&stored.summary.workspace_path)
                        .map_err(Error::into_internal_error)?,
                    self.skill_catalog(&stored.summary.workspace_path),
                    ShellProcesses::default(),
                ),
            };
        let parameters = ModelRequestParameters::new(
            &saved_settings.model,
            saved_settings.effort,
            system_prompt.clone(),
        )
        .map_err(Error::into_internal_error)?;
        let usage = convert::usage_update(&stored.transcript, &parameters);
        self.activate(
            request.session_id.clone(),
            ActiveSession {
                selections: saved_settings.clone(),
                system_prompt,
                skills,
                shell_processes,
            },
        );
        convert::replay_transcript(&stored.transcript, &mut send_update)?;
        if let Some(usage) = usage {
            send_update(usage)?;
        }
        Ok(LoadSessionResponse::new().config_options(config_options(&saved_settings)))
    }

    fn activate(&self, session_id: SessionId, active: ActiveSession) {
        self.active
            .lock()
            .expect("active sessions mutex poisoned")
            .insert(session_id, active);
    }

    /// Advertises `/compact` and the session's skill catalog. A session
    /// deleted after activation has nothing to advertise.
    fn send_available_commands(
        &self,
        connection: &ConnectionTo<Client>,
        session_id: SessionId,
    ) -> Result<()> {
        let Some(active) = self.active_session(&session_id) else {
            return Ok(());
        };
        connection.send_notification(SessionNotification::new(
            session_id,
            available_commands(&active.skills),
        ))
    }

    /// The process state of a session created or loaded in this process.
    fn active_session(&self, session_id: &SessionId) -> Option<ActiveSession> {
        self.active
            .lock()
            .expect("active sessions mutex poisoned")
            .get(session_id)
            .cloned()
    }

    fn list_sessions(&self, request: &ListSessionsRequest) -> Result<ListSessionsResponse> {
        if request
            .cursor
            .as_deref()
            .is_some_and(|cursor| !cursor.is_empty())
        {
            return Err(
                Error::invalid_params().data("session listing is not paginated; omit the cursor")
            );
        }
        let summaries = self
            .store
            .list(request.cwd.as_deref())
            .map_err(Error::into_internal_error)?;
        Ok(ListSessionsResponse::new(
            summaries.into_iter().map(session_info).collect(),
        ))
    }

    /// Stops the session's shell processes only after the database deletion
    /// succeeds. The active session stays registered until they are cleaned
    /// up, so connection shutdown can still reach them.
    async fn delete_session(
        &self,
        request: &DeleteSessionRequest,
    ) -> Result<DeleteSessionResponse> {
        self.store
            .delete(&request.session_id)
            .map_err(Error::into_internal_error)?;
        if let Some(active) = self.active_session(&request.session_id) {
            active.shell_processes.shutdown().await;
        }
        self.active
            .lock()
            .expect("active sessions mutex poisoned")
            .remove(&request.session_id);
        Ok(DeleteSessionResponse::new())
    }

    fn all_shell_processes(&self) -> Vec<ShellProcesses> {
        self.active
            .lock()
            .expect("active sessions mutex poisoned")
            .values()
            .map(|active| active.shell_processes.clone())
            .collect()
    }

    /// Rejects new session operations, cancels active prompts, and asks every
    /// active session's shell processes to stop, without waiting.
    fn begin_shutdown(&self) {
        self.operations.close();
        for shell_processes in self.all_shell_processes() {
            shell_processes.begin_shutdown();
        }
    }

    /// Signals the shell processes of every active session before waiting
    /// for any, so cleanup time does not grow with the number of sessions.
    async fn shutdown_shell_processes(&self) {
        let owners = self.all_shell_processes();
        for shell_processes in &owners {
            shell_processes.begin_shutdown();
        }
        futures::future::join_all(owners.iter().map(ShellProcesses::shutdown)).await;
    }

    /// Why a session operation could not start.
    fn unavailable(&self) -> Error {
        if self.operations.is_closed() {
            Error::invalid_request().data("Ox is shutting down")
        } else {
            Error::invalid_request().data("session has an operation in progress")
        }
    }

    /// The cached client is cleared even when removing the saved key fails,
    /// and that failure is reported: the key may still load on a later request.
    fn logout(&self) -> Result<LogoutResponse> {
        let removed = auth::delete_api_key();
        self.openrouter
            .lock()
            .expect("OpenRouter client mutex poisoned")
            .take();
        removed.map_err(Error::into_internal_error)?;
        Ok(LogoutResponse::new())
    }

    fn respond_to_new_session(
        &self,
        request: &NewSessionRequest,
        responder: Responder<NewSessionResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        match self.new_session(request) {
            Ok(response) => {
                let session_id = response.session_id.clone();
                responder.respond(response)?;
                self.send_available_commands(connection, session_id)
            }
            Err(error) => responder.respond_with_error(error),
        }
    }

    /// Replays the saved transcript as ACP updates before the response.
    fn respond_to_load_session(
        &self,
        request: LoadSessionRequest,
        responder: Responder<LoadSessionResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        let Some(_guard) = self.operations.try_load(&request.session_id) else {
            return responder.respond_with_error(self.unavailable());
        };
        let send_update = acp_update_sender(connection.clone(), request.session_id.clone());
        match self.load_session(&request, send_update) {
            Ok(response) => {
                responder.respond(response)?;
                self.send_available_commands(connection, request.session_id)
            }
            Err(error) => responder.respond_with_error(error),
        }
    }

    /// Spawns the deletion under its operation guard, so waiting for shell
    /// process cleanup leaves the connection free for other sessions.
    fn respond_to_delete_session(
        &self,
        request: DeleteSessionRequest,
        responder: Responder<DeleteSessionResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        let Some(guard) = self.operations.try_delete(&request.session_id) else {
            return responder.respond_with_error(self.unavailable());
        };
        let state = self.clone();
        connection.spawn(async move {
            let _guard = guard;
            let result = state.delete_session(&request).await;
            reply(responder, result)
        })
    }

    /// Rejects a prompt request that cannot start, or spawns `/compact` or a
    /// prompt run that responds when it finishes.
    fn start_prompt(
        &self,
        request: PromptRequest,
        responder: Responder<PromptResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        let message = match convert::prompt_message(&request.prompt) {
            Ok(message) => message,
            Err(error) => return responder.respond_with_error(error),
        };
        let Some(operation) = self.operations.try_prompt(&request.session_id) else {
            return responder.respond_with_error(self.unavailable());
        };
        let Some(active) = self.active_session(&request.session_id) else {
            return responder.respond_with_error(inactive(&request.session_id));
        };
        let turn = match dispatch(message, &active.skills) {
            Dispatch::Compact => {
                return self.spawn_compaction(
                    request.session_id,
                    active,
                    operation,
                    responder,
                    connection,
                );
            }
            Dispatch::Skill {
                invocation,
                hook_source,
            } => (TurnInput::SkillInvocation(invocation), Some(*hook_source)),
            Dispatch::UserMessage(message) => (TurnInput::UserMessage(message), None),
        };
        self.spawn_prompt_run(
            request.session_id,
            active,
            turn,
            operation,
            responder,
            connection,
        )
    }

    fn spawn_compaction(
        &self,
        session_id: SessionId,
        active: ActiveSession,
        (guard, cancellation): (OperationGuard, PromptCancellation),
        responder: Responder<PromptResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        let state = self.clone();
        let send_update = acp_update_sender(connection.clone(), session_id.clone());
        connection.spawn(async move {
            let _guard = guard;
            let result = state
                .compact_session(&session_id, active, &cancellation, send_update)
                .await;
            reply(responder, result)
        })
    }

    fn spawn_prompt_run(
        &self,
        session_id: SessionId,
        active: ActiveSession,
        (turn_input, skill_source): (TurnInput, Option<hooks::HookSource>),
        (guard, cancellation): (OperationGuard, PromptCancellation),
        responder: Responder<PromptResponse>,
        connection: &ConnectionTo<Client>,
    ) -> Result<()> {
        let openrouter = match self.openrouter_client() {
            Ok(openrouter) => openrouter,
            Err(error) => return responder.respond_with_error(error),
        };
        let run = match prompt::run(
            self.store.clone(),
            openrouter,
            prompt::PromptInput {
                session_id: session_id.clone(),
                turn_input,
                hook_sources: self
                    .settings
                    .global_hooks
                    .clone()
                    .into_iter()
                    .chain(skill_source)
                    .collect(),
                selected_settings: active.selections,
                system_prompt: active.system_prompt,
                shell_processes: active.shell_processes,
            },
            cancellation,
            prompt::Presentation::acp(connection.clone(), session_id),
        ) {
            Ok(run) => run,
            Err(error) => return responder.respond_with_error(error),
        };
        connection.spawn(async move {
            let _guard = guard;
            let result = run.await;
            reply(
                responder,
                result.map(|output| {
                    PromptResponse::new(match output {
                        prompt::PromptOutput::Finished(_) => StopReason::EndTurn,
                        prompt::PromptOutput::Cancelled => StopReason::Cancelled,
                        prompt::PromptOutput::TokenLimit => StopReason::MaxTokens,
                        prompt::PromptOutput::Refused => StopReason::Refusal,
                    })
                }),
            )
        })
    }
}

fn store_error(error: io::Error) -> Error {
    match error.kind() {
        ErrorKind::InvalidInput => Error::invalid_params().data(error.to_string()),
        _ => Error::into_internal_error(error),
    }
}

fn not_found(session_id: &SessionId) -> Error {
    Error::resource_not_found(Some(session_id.to_string()))
}

fn inactive(session_id: &SessionId) -> Error {
    Error::invalid_request().data(format!(
        "session {session_id} is not active; create or load it first"
    ))
}

fn session_info(summary: SessionSummary) -> SessionInfo {
    SessionInfo::new(summary.id, summary.workspace_path)
        .title(summary.session_title)
        .updated_at(summary.updated_at)
}

fn reply<T: JsonRpcResponse>(responder: Responder<T>, result: Result<T>) -> Result<()> {
    match result {
        Ok(response) => responder.respond(response),
        Err(error) => responder.respond_with_error(error),
    }
}

/// Sends each ACP update as a notification for `session_id`.
fn acp_update_sender(
    connection: ConnectionTo<Client>,
    session_id: SessionId,
) -> impl FnMut(SessionUpdate) -> Result<()> + Send + 'static {
    move |update| connection.send_notification(SessionNotification::new(session_id.clone(), update))
}

// TODO: Test terminal authentication with the preview version of Zed.
fn terminal_auth_method() -> AuthMethod {
    AuthMethod::Terminal(
        AuthMethodTerminal::new("openrouter", "Log in to OpenRouter")
            .description("Enter an OpenRouter API key and save it in the system keyring")
            .args(vec!["auth".to_owned(), "login".to_owned()]),
    )
}

/// The agent speaks exactly one protocol version, so the response always
/// names it; a client that needs a different one disconnects.
fn initialize_response(initialize: &InitializeRequest) -> InitializeResponse {
    let mut response = InitializeResponse::new(ProtocolVersion::LATEST).agent_capabilities(
        AgentCapabilities::new()
            .load_session(true)
            .prompt_capabilities(PromptCapabilities::new().image(true))
            .session_capabilities(
                SessionCapabilities::new()
                    .list(SessionListCapabilities::new())
                    .delete(SessionDeleteCapabilities::new()),
            )
            .auth(AgentAuthCapabilities::new().logout(LogoutCapabilities::new())),
    );
    if initialize.client_capabilities.auth.terminal {
        response = response.auth_methods(vec![terminal_auth_method()]);
    }
    response
}

pub async fn serve_stdio(settings: Settings) -> std::result::Result<(), Box<dyn StdError>> {
    let store = SessionStore::open(&sessions::database_path()?)?;
    serve(
        ServerState::new(store, settings, settings::home_dir()?),
        Stdio::new(),
        termination_signal()?,
    )
    .await?;
    Ok(())
}

/// Completes on the first SIGINT, SIGTERM, or SIGHUP.
fn termination_signal() -> io::Result<impl Future<Output = ()>> {
    use tokio::signal::unix::{SignalKind, signal};
    let mut interrupt = signal(SignalKind::interrupt())?;
    let mut terminate = signal(SignalKind::terminate())?;
    let mut hangup = signal(SignalKind::hangup())?;
    Ok(async move {
        tokio::select! {
            _ = interrupt.recv() => {}
            _ = terminate.recv() => {}
            _ = hangup.recv() => {}
        }
    })
}

/// Runs one prompt in a new session and returns the final answer. Skills are
/// not invoked.
pub async fn run_headless(
    workspace_path: &Path,
    model: String,
    effort: EffortLevel,
    user_message: String,
    global_hooks: Option<hooks::HookSource>,
) -> std::result::Result<String, Box<dyn StdError>> {
    let api_key = auth::api_key()?.ok_or_else(|| {
        io::Error::new(
            ErrorKind::PermissionDenied,
            "OpenRouter authentication required; run `ox auth login`",
        )
    })?;
    let system_prompt = system_prompt::for_workspace(workspace_path)?;
    let store = SessionStore::open(&sessions::database_path()?)?;
    let session = store.create(workspace_path)?;
    run_headless_prompt(
        store,
        openrouter::Client::new(api_key),
        session.id,
        SessionSettings::new(model, effort),
        system_prompt,
        user_message,
        global_hooks.into_iter().collect(),
    )
    .await
}

async fn run_headless_prompt(
    store: SessionStore,
    openrouter: openrouter::Client,
    session_id: SessionId,
    settings: SessionSettings,
    system_prompt: String,
    user_message: String,
    hook_sources: Vec<hooks::HookSource>,
) -> std::result::Result<String, Box<dyn StdError>> {
    let signalled = termination_signal()?;
    let cancellation = PromptCancellation::new();
    let shell_processes = ShellProcesses::default();
    let run = prompt::run(
        store,
        openrouter,
        prompt::PromptInput {
            session_id: session_id.clone(),
            turn_input: TurnInput::UserMessage(user_message.into()),
            hook_sources,
            selected_settings: settings.with_mode(SessionMode::Auto),
            system_prompt,
            shell_processes: shell_processes.clone(),
        },
        cancellation.clone(),
        prompt::Presentation::headless(session_id),
    )?;
    tokio::pin!(run, signalled);
    // The first signal stops the shell processes at once, before the prompt
    // and its hooks finish, so a parent hook's grace period is not spent
    // waiting. The handlers stay installed, so later signals are ignored.
    let mut stopping = false;
    let output = loop {
        tokio::select! {
            biased;
            output = &mut run => break output,
            () = &mut signalled, if !stopping => {
                stopping = true;
                cancellation.cancel();
                shell_processes.begin_shutdown();
            }
        }
    };
    shell_processes.shutdown().await;
    match output? {
        prompt::PromptOutput::Finished(answer) => Ok(answer),
        other => Err(io::Error::other(format!("prompt stopped with {other:?}")).into()),
    }
}

/// Gives a signal-driven ACP connection a clean incoming EOF after active
/// operations finish. The physical transport then drains accepted responses
/// through its output sink before the connection returns.
struct SignalAwareAgent<C, S> {
    agent: C,
    state: ServerState,
    signalled: S,
}

impl<C, S> ConnectTo<Client> for SignalAwareAgent<C, S>
where
    C: ConnectTo<Client>,
    S: Future<Output = ()> + Send + 'static,
{
    async fn connect_to(self, transport: impl ConnectTo<Agent>) -> Result<()> {
        let (
            Channel {
                rx: mut from_agent,
                tx: to_agent,
            },
            agent_future,
        ) = self.agent.into_channel_and_future();
        let (
            Channel {
                rx: mut from_transport,
                tx: to_transport,
            },
            transport_future,
        ) = transport.into_channel_and_future();

        let incoming = async move {
            let shutdown = async move {
                self.signalled.await;
                self.state.begin_shutdown();
                self.state.operations.shutdown().await;
            };
            tokio::pin!(shutdown);
            loop {
                tokio::select! {
                    biased;
                    () = &mut shutdown => break,
                    frame = from_transport.next() => match frame {
                        Some(frame) => to_agent.unbounded_send(frame).map_err(|_| {
                            Error::internal_error().data("agent input closed")
                        })?,
                        None => break,
                    },
                }
            }
            Ok::<(), Error>(())
        };
        let outgoing = async move {
            while let Some(frame) = from_agent.next().await {
                to_transport
                    .unbounded_send(frame)
                    .map_err(|_| Error::internal_error().data("ACP output transport closed"))?;
            }
            Ok::<(), Error>(())
        };
        futures::try_join!(incoming, outgoing, agent_future, transport_future)?;
        Ok(())
    }
}

/// Serves one ACP connection until incoming EOF or `signalled`. Either begins
/// shutdown: new operations are rejected, active prompts are cancelled, and
/// shell processes are signalled, while the connection keeps running until
/// the active operations finish. Every shell process is cleaned up before
/// returning, even after a transport error.
async fn serve(
    state: ServerState,
    transport: impl ConnectTo<Agent> + 'static,
    signalled: impl Future<Output = ()> + Send + 'static,
) -> Result<()> {
    let close_state = state.clone();
    let signal_state = state.clone();
    let cleanup_state = state.clone();
    let new_state = state.clone();
    let load_state = state.clone();
    let list_state = state.clone();
    let delete_state = state.clone();
    let config_state = state.clone();
    let logout_state = state.clone();
    let prompt_state = state.clone();
    let cancel_state = state;

    let agent = Agent
        .builder()
        .name("ox")
        .on_close(async move |_connection| {
            close_state.begin_shutdown();
            close_state.operations.shutdown().await;
            Ok(())
        })
        .on_receive_request(
            async move |initialize: InitializeRequest, responder, _connection| {
                responder.respond(initialize_response(&initialize))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: NewSessionRequest, responder, connection| {
                new_state.respond_to_new_session(&request, responder, &connection)
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: LoadSessionRequest, responder, connection| {
                load_state.respond_to_load_session(request, responder, &connection)
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: ListSessionsRequest, responder, _connection| {
                reply(responder, list_state.list_sessions(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: DeleteSessionRequest, responder, connection| {
                delete_state.respond_to_delete_session(request, responder, &connection)
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: SetSessionConfigOptionRequest, responder, _connection| {
                reply(responder, config_state.set_config_option(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |_request: LogoutRequest, responder, _connection| {
                reply(responder, logout_state.logout())
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: PromptRequest, responder, connection| {
                prompt_state.start_prompt(request, responder, &connection)
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |cancel: CancelNotification, _connection| {
                cancel_state.operations.cancel(&cancel.session_id);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        );
    let result = transport
        .connect_to(SignalAwareAgent {
            agent,
            state: signal_state,
            signalled,
        })
        .await;
    cleanup_state.shutdown_shell_processes().await;
    result
}

#[cfg(test)]
mod tests {
    use std::{fs, path::Path};

    use agent_client_protocol::schema::v1::{AuthCapabilities, ClientCapabilities, ErrorCode};

    use super::*;
    use crate::{
        sessions::{ToolOutcome, TranscriptEntry, TurnStart},
        tools::{self, fixture::Workspace},
    };

    #[tokio::test]
    async fn manual_compact_command_uses_the_active_prompt_without_saving_a_message() {
        use crate::{
            openrouter::fixture::{Reply, Server, delta, sse, usage},
            sessions::{AssistantBatch, AssistantMessage, ModelUsage},
        };
        let store = SessionStore::in_memory();
        let state = ServerState::new(store.clone(), test_settings(), no_home());
        let id = store.create(Path::new("/workspace")).unwrap().id;
        // The saved turn start uses the default model; compaction prepares the
        // next turn, which uses the selected one.
        let selected = openrouter::catalog()[1].id.as_str();
        let active = ActiveSession {
            selections: SessionSettings::new(selected, EffortLevel::Default),
            system_prompt: "captured system".to_owned(),
            shell_processes: ShellProcesses::default(),
            skills: vec![],
        };
        let mut updates = Vec::new();
        let empty = state
            .compact_session(&id, active.clone(), &PromptCancellation::new(), |update| {
                updates.push(update);
                Ok(())
            })
            .await
            .unwrap();
        assert_eq!(empty.stop_reason, StopReason::EndTurn);
        assert!(
            state.openrouter.lock().unwrap().is_none(),
            "empty command needs no client"
        );

        store
            .append_turn_start(&id, &TurnStart::test("previous work ".repeat(3000)))
            .unwrap();
        store
            .append_batch(
                &id,
                &AssistantBatch::new(
                    AssistantMessage {
                        text: "done".to_owned(),
                        reasoning: String::new(),
                        tool_calls: vec![],
                        continuation_metadata: vec![],
                        usage: Some(ModelUsage {
                            input_tokens: 9000,
                            output_tokens: 10,
                            cost: 0.25,
                        }),
                    },
                    vec![],
                )
                .unwrap(),
            )
            .unwrap();
        let before = store.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![Reply::Stream(sse(&[
            delta(
                serde_json::json!({ "role": "assistant", "content": "Previous work complete." }),
                Some("stop"),
            ),
            usage(9000, 20, 0.125),
        ]))])
        .await;
        *state.openrouter.lock().unwrap() = Some(server.client());
        let response = state
            .compact_session(&id, active, &PromptCancellation::new(), |update| {
                updates.push(update);
                Ok(())
            })
            .await
            .unwrap();
        assert_eq!(response.stop_reason, StopReason::EndTurn);
        let after = store.read(&id).unwrap().unwrap().transcript;
        assert_eq!(after.len(), before.len() + 1);
        assert!(matches!(
            after.last(),
            Some(TranscriptEntry::CompactionCheckpoint(checkpoint))
                if checkpoint.summarizer_cost == Some(0.125)
        ));
        let estimate = compaction::request_estimate(
            &ModelRequestParameters::new(
                selected,
                EffortLevel::Default,
                "captured system".to_owned(),
            )
            .unwrap(),
            &after,
        );
        assert!(matches!(
            &updates[..],
            [SessionUpdate::UsageUpdate(usage)]
                if usage.used == estimate as u64
                    && usage.cost.as_ref().is_some_and(|cost| cost.amount == 0.375 && cost.currency == "USD")
        ));
        let summarizer = &server.requests()[0];
        assert_eq!(summarizer["model"], selected);
        assert_eq!(
            summarizer["messages"][0]["content"],
            include_str!("prompts/compaction_prompt.md")
        );
        assert!(after.iter().all(|entry| !matches!(entry,
            TranscriptEntry::TurnStart(TurnStart { input: TurnInput::UserMessage(message), .. })
                if message.text() == "/compact")));
    }
    fn state() -> ServerState {
        state_over(SessionStore::in_memory())
    }

    /// Settings with the test catalog's default model and no global hooks.
    fn test_settings() -> Settings {
        Settings {
            default_model: openrouter::fixture::DEFAULT_MODEL.to_owned(),
            global_hooks: None,
        }
    }

    /// A home directory that does not exist, so skills installed on the
    /// developer's machine never enter a test catalog.
    fn no_home() -> PathBuf {
        std::env::temp_dir().join(format!("ox-no-home-{}", uuid::Uuid::new_v4()))
    }

    /// A server state over `store`, as a later process would open it.
    fn state_over(store: SessionStore) -> ServerState {
        let state = ServerState::new(store, test_settings(), no_home());
        *state.openrouter.lock().unwrap() = Some(openrouter::Client::new("test-key".to_owned()));
        state
    }

    /// Whether every operation guard has been dropped. Admission stays closed
    /// after connection shutdown, so this waits on no operation rather than
    /// acquiring one.
    fn operations_idle(operations: &SessionOperations) -> bool {
        use futures::FutureExt;
        operations.shutdown().now_or_never().is_some()
    }

    /// Creates an active session for `workspace_path` and returns its ID.
    fn create_session(state: &ServerState, workspace_path: &Path) -> SessionId {
        state
            .new_session(&NewSessionRequest::new(workspace_path))
            .unwrap()
            .session_id
    }

    #[test]
    fn terminal_login_is_advertised_only_to_supporting_clients() {
        let response = initialize_response(&InitializeRequest::new(ProtocolVersion::V1));
        assert!(response.auth_methods.is_empty());
        assert!(response.agent_capabilities.prompt_capabilities.image);

        let initialize = InitializeRequest::new(ProtocolVersion::V1).client_capabilities(
            ClientCapabilities::new().auth(AuthCapabilities::new().terminal(true)),
        );
        let response = initialize_response(&initialize);

        assert!(response.agent_capabilities.auth.logout.is_some());
        assert!(matches!(
            &response.auth_methods[..],
            [AuthMethod::Terminal(method)]
                if method.id.to_string() == "openrouter"
                    && method.args == ["auth", "login"]
        ));
    }

    #[test]
    fn slash_commands_have_acp_metadata_and_prompt_dispatch() {
        let skill = |name: &str, argument_hint: Option<&str>, before_stop: Option<&str>| Skill {
            name: name.to_owned(),
            description: format!("The {name} skill."),
            argument_hint: argument_hint.map(str::to_owned),
            instructions: format!("Follow the {name} steps."),
            directory: Path::new("/workspace/.agents/skills").join(name),
            hooks: hooks::Hooks {
                before_stop: before_stop.map(|command| hooks::HookCommand {
                    command: command.to_owned(),
                }),
                ..hooks::Hooks::default()
            },
        };
        let skills = [
            skill(
                "goal",
                Some("<objective>"),
                Some("python3 scripts/check.py"),
            ),
            skill("init", None, None),
        ];
        assert_eq!(
            serde_json::to_value(available_commands(&skills)).unwrap(),
            serde_json::json!({
                "sessionUpdate": "available_commands_update",
                "availableCommands": [
                    {
                        "name": "compact",
                        "description": "Compact the conversation context."
                    },
                    {
                        "name": "goal",
                        "description": "The goal skill.",
                        "input": { "hint": "<objective>" }
                    },
                    {
                        "name": "init",
                        "description": "The init skill."
                    }
                ]
            })
        );
        for command in ["/compact", " /compact now\n"] {
            assert_eq!(
                dispatch(command.to_owned().into(), &skills),
                Dispatch::Compact
            );
        }
        let arguments = "Fix the \"tests\" in $HOME\n  and more";
        assert_eq!(
            dispatch(format!(" /goal\t{arguments} \n").into(), &skills),
            Dispatch::Skill {
                invocation: SkillInvocation {
                    name: "goal".to_owned(),
                    arguments: arguments.to_owned(),
                    instructions: "Follow the goal steps.".to_owned(),
                    images: vec![],
                },
                hook_source: Box::new(hooks::HookSource {
                    hooks: skills[0].hooks.clone(),
                    skill: Some("goal".to_owned()),
                    directory: skills[0].directory.clone(),
                }),
            }
        );
        assert_eq!(
            dispatch("/init".to_owned().into(), &skills),
            Dispatch::Skill {
                invocation: SkillInvocation {
                    name: "init".to_owned(),
                    arguments: String::new(),
                    instructions: "Follow the init steps.".to_owned(),
                    images: vec![],
                },
                hook_source: Box::new(hooks::HookSource {
                    skill: Some("init".to_owned()),
                    hooks: hooks::Hooks::default(),
                    directory: skills[1].directory.clone(),
                }),
            }
        );
        for user_message in [
            "compact",
            "/compactness",
            "/",
            "/goals now",
            "/review it",
            "do /goal",
        ] {
            assert_eq!(
                dispatch(user_message.to_owned().into(), &skills),
                Dispatch::UserMessage(user_message.to_owned().into())
            );
        }
        let with_image = UserMessage {
            parts: vec![
                UserMessagePart::Text("/goal inspect".to_owned()),
                UserMessagePart::Image(sessions::ImageAttachment {
                    data: "aGVsbG8=".to_owned(),
                    mime_type: "image/png".to_owned(),
                }),
            ],
        };
        assert!(
            matches!(dispatch(with_image, &skills), Dispatch::Skill { invocation, .. }
            if invocation.arguments == "inspect" && invocation.images.len() == 1)
        );
    }

    fn write_skill(skills_directory: &Path, name: &str, text: &str) {
        let directory = skills_directory.join(name);
        fs::create_dir_all(&directory).unwrap();
        fs::write(directory.join("SKILL.md"), text).unwrap();
    }

    #[test]
    fn activation_captures_the_system_prompt_and_skill_catalog_once_per_process() {
        let workspace = Workspace::new();
        let agents_md = workspace.0.join("AGENTS.md");
        fs::write(&agents_md, "Answer in French.\n").unwrap();
        let skills_dir = workspace.0.join(".agents/skills");
        write_skill(
            &skills_dir,
            "goal",
            "---\nname: goal\ndescription: \"Work toward an objective: verify it.\"\nargument-hint: \"<objective>\"\nallowed-tools: [shell]\nmetadata:\n  owner: ox\nhooks:\n  PreToolUse: [{matcher: shell}]\n  before_run:\n    command: python3 scripts/context.py\n  before_tool:\n    command: python3 scripts/check_call.py\n  after_tools:\n    command: python3 scripts/format.py\n  before_stop:\n    command: python3 scripts/check.py\n  after_run:\n    command: python3 scripts/report.py\n---\n\nWork toward the objective.\n",
        );
        fs::write(skills_dir.join(".DS_Store"), "").unwrap();
        let goal = Skill {
            name: "goal".to_owned(),
            description: "Work toward an objective: verify it.".to_owned(),
            argument_hint: Some("<objective>".to_owned()),
            instructions: "Work toward the objective.".to_owned(),
            directory: skills_dir.join("goal"),
            hooks: hooks::Hooks {
                before_run: Some(hooks::HookCommand {
                    command: "python3 scripts/context.py".to_owned(),
                }),
                before_tool: Some(hooks::HookCommand {
                    command: "python3 scripts/check_call.py".to_owned(),
                }),
                after_tools: Some(hooks::HookCommand {
                    command: "python3 scripts/format.py".to_owned(),
                }),
                before_stop: Some(hooks::HookCommand {
                    command: "python3 scripts/check.py".to_owned(),
                }),
                after_run: Some(hooks::HookCommand {
                    command: "python3 scripts/report.py".to_owned(),
                }),
            },
        };
        let french = system_prompt::for_workspace(&workspace.0).unwrap();
        let state = state();
        let id = create_session(&state, &workspace.0);
        assert_eq!(state.active_session(&id).unwrap().system_prompt, french);
        assert_eq!(
            state.active_session(&id).unwrap().skills,
            std::slice::from_ref(&goal)
        );

        fs::write(&agents_md, "Answer in German.\n").unwrap();
        fs::remove_dir_all(skills_dir.join("goal")).unwrap();
        let mut updates = Vec::new();
        state
            .load_session(
                &LoadSessionRequest::new(id.clone(), &workspace.0),
                |update| {
                    updates.push(update);
                    Ok(())
                },
            )
            .unwrap();
        assert!(updates.is_empty(), "a new session replays nothing");
        assert_eq!(
            state.active_session(&id).unwrap().system_prompt,
            french,
            "a repeated load keeps the captured prompt"
        );
        assert_eq!(state.active_session(&id).unwrap().skills, [goal]);

        let later = state_over(state.store.clone());
        later
            .load_session(&LoadSessionRequest::new(id.clone(), &workspace.0), |_| {
                Ok(())
            })
            .unwrap();
        assert_eq!(
            later.active_session(&id).unwrap().system_prompt,
            system_prompt::for_workspace(&workspace.0).unwrap(),
            "a later process reads the current file on its first load"
        );
        assert!(later.active_session(&id).unwrap().skills.is_empty());
    }

    #[tokio::test]
    async fn loading_an_unknown_or_moved_session_fails_and_delete_deactivates_it() {
        let workspace = Workspace::new();
        let state = state();
        let id = create_session(&state, &workspace.0);
        let missing = state
            .load_session(
                &LoadSessionRequest::new(SessionId::new("missing"), &workspace.0),
                |_| Ok(()),
            )
            .unwrap_err();
        assert_eq!(missing.code, ErrorCode::ResourceNotFound);
        let elsewhere = state
            .load_session(
                &LoadSessionRequest::new(id.clone(), Path::new("/elsewhere")),
                |_| Ok(()),
            )
            .unwrap_err();
        assert_eq!(elsewhere.code, ErrorCode::InvalidParams);

        state
            .delete_session(&DeleteSessionRequest::new(id.clone()))
            .await
            .unwrap();
        assert!(
            state.active_session(&id).is_none(),
            "delete removes the active session"
        );
    }

    #[test]
    fn an_empty_or_foreign_hooks_map_declares_no_hooks() {
        let state = state();
        for hooks in ["{}", "{PreToolUse: [{matcher: shell}]}"] {
            let workspace = Workspace::new();
            write_skill(
                &workspace.0.join(".agents/skills"),
                "shared",
                &format!("---\nname: shared\ndescription: Shared.\nhooks: {hooks}\n---\nBody\n"),
            );
            let id = create_session(&state, &workspace.0);
            assert_eq!(
                state.active_session(&id).unwrap().skills[0].hooks,
                hooks::Hooks::default(),
                "{hooks}"
            );
            let later = state_over(state.store.clone());
            later
                .load_session(&LoadSessionRequest::new(id.clone(), &workspace.0), |_| {
                    Ok(())
                })
                .unwrap();
            assert_eq!(
                later.active_session(&id).unwrap().skills[0].hooks,
                hooks::Hooks::default(),
                "{hooks}: on load"
            );
        }
    }

    #[test]
    fn skills_directories_load_in_priority_order() {
        let home = Workspace::new();
        let workspace = Workspace::new();
        let ox = home.0.join(".config/ox/skills");
        let agents = home.0.join(".agents/skills");
        let local = workspace.0.join(".agents/skills");
        let skill = |name: &str, description: &str| {
            format!("---\nname: {name}\ndescription: {description}\n---\nBody\n")
        };
        for directory in [&ox, &agents, &local] {
            write_skill(
                directory,
                "shared",
                &skill("shared", &directory.display().to_string()),
            );
        }
        for directory in [&agents, &local] {
            write_skill(
                directory,
                "personal",
                &skill("personal", &directory.display().to_string()),
            );
        }
        write_skill(&ox, "ox-only", &skill("ox-only", "Ox."));
        write_skill(&agents, "agents-only", &skill("agents-only", "Agents."));
        write_skill(&local, "local-only", &skill("local-only", "Local."));
        write_skill(&local, "broken", "No frontmatter.\n");
        let mut state = state();
        state.home = home.0.clone();
        let id = create_session(&state, &workspace.0);
        let skills = state.active_session(&id).unwrap().skills;
        let loaded: Vec<_> = skills
            .iter()
            .map(|skill| (skill.name.as_str(), skill.directory.clone()))
            .collect();
        assert_eq!(
            loaded,
            [
                ("agents-only", agents.join("agents-only")),
                ("local-only", local.join("local-only")),
                ("ox-only", ox.join("ox-only")),
                ("personal", agents.join("personal")),
                ("shared", ox.join("shared")),
            ],
            "each name once, from its highest-priority skills directory, without the broken skill"
        );
    }

    #[test]
    fn a_non_utf8_agents_md_fails_activation() {
        let workspace = Workspace::new();
        fs::write(workspace.0.join("AGENTS.md"), [0xff, 0xfe]).unwrap();
        let state = state();
        let invalid = state
            .new_session(&NewSessionRequest::new(&workspace.0))
            .unwrap_err();
        assert_eq!(
            invalid.data,
            Some(serde_json::json!("AGENTS.md: not UTF-8 text"))
        );
        assert!(state.store.list(None).unwrap().is_empty());
        assert!(state.active.lock().unwrap().is_empty());
    }

    #[test]
    fn new_sessions_use_the_workspace_default_model() {
        let workspace = Workspace::new();
        let chosen = openrouter::catalog()[1].id.as_str();
        let path = workspace.0.join(".ox/settings.json");
        fs::create_dir(workspace.0.join(".ox")).unwrap();
        fs::write(&path, format!(r#"{{"model":"{chosen}"}}"#)).unwrap();
        let state = state();
        let created = state
            .new_session(&NewSessionRequest::new(&workspace.0))
            .unwrap();
        let options = serde_json::to_value(&created).unwrap();
        assert_eq!(options["configOptions"][0]["currentValue"], chosen);

        let later = state_over(state.store.clone());
        let loaded = later
            .load_session(
                &LoadSessionRequest::new(created.session_id, &workspace.0),
                |_| Ok(()),
            )
            .unwrap();
        let options = serde_json::to_value(&loaded).unwrap();
        assert_eq!(
            options["configOptions"][0]["currentValue"], chosen,
            "a later load of a session without turns"
        );

        fs::write(&path, r#"{"model":"a/b"}"#).unwrap();
        let invalid = state
            .new_session(&NewSessionRequest::new(&workspace.0))
            .unwrap_err();
        let data = invalid.data.unwrap();
        assert!(
            data.as_str()
                .unwrap()
                .starts_with(&format!("{}: model a/b", path.display())),
            "{data}"
        );
        assert_eq!(state.store.list(None).unwrap().len(), 1);
    }

    #[test]
    fn load_and_list_use_the_same_exact_workspace_path() {
        let state = state();
        let created = state
            .new_session(&NewSessionRequest::new("/workspace/"))
            .unwrap();
        state
            .store
            .append_turn_start(&created.session_id, &TurnStart::test("Hello".to_owned()))
            .unwrap();

        for workspace_path in ["/workspace/", "/workspace", "/workspace/./", "//workspace/"] {
            let mut updates = Vec::new();
            let loaded = state.load_session(
                &LoadSessionRequest::new(created.session_id.clone(), workspace_path),
                |update| {
                    updates.push(update);
                    Ok(())
                },
            );
            let listed = state
                .list_sessions(&ListSessionsRequest::new().cwd(workspace_path))
                .unwrap();
            if workspace_path == "/workspace/" {
                loaded.unwrap();
                assert_eq!(listed.sessions.len(), 1);
                assert_eq!(updates.len(), 1);
            } else {
                assert_eq!(loaded.unwrap_err().code, ErrorCode::InvalidParams);
                assert!(listed.sessions.is_empty());
                assert!(updates.is_empty(), "workspace validation precedes replay");
            }
        }
    }

    #[test]
    fn loading_a_session_with_a_model_outside_the_catalog_fails_before_replay() {
        let state = state();
        let created = state
            .new_session(&NewSessionRequest::new("/workspace"))
            .unwrap();
        let append = |model: &str| {
            state
                .store
                .append_turn_start(
                    &created.session_id,
                    &TurnStart {
                        model: model.to_owned(),
                        ..TurnStart::test("Hello".to_owned())
                    },
                )
                .unwrap();
        };
        let mut updates = Vec::new();
        let mut load = || {
            updates.clear();
            state.load_session(
                &LoadSessionRequest::new(created.session_id.clone(), "/workspace"),
                |update| {
                    updates.push(update);
                    Ok(())
                },
            )
        };
        append("retired/model");
        append(openrouter::fixture::DEFAULT_MODEL);
        assert!(
            load().is_ok(),
            "only the latest turn start's model must be in the catalog"
        );

        append("retired/model");
        let error = load().unwrap_err();

        assert_eq!(error.code, ErrorCode::InternalError);
        assert_eq!(
            error.data,
            Some(serde_json::json!(
                "session model retired/model is not in the model catalog"
            ))
        );
        assert!(updates.is_empty());
    }

    #[test]
    fn configuration_selections_validate_and_restore_saved_values() {
        let state = state();
        let workspace_path = Path::new("/workspace");
        let created = state
            .new_session(&NewSessionRequest::new(workspace_path))
            .unwrap();
        let new_options = serde_json::to_value(&created).unwrap();
        assert_eq!(new_options["configOptions"].as_array().unwrap().len(), 3);
        let image_option = new_options["configOptions"][0]["options"]
            .as_array()
            .unwrap()
            .iter()
            .find(|option| option["value"] == "z-ai/glm-5.3-flash")
            .unwrap();
        assert_eq!(image_option["description"], "Accepts images");
        assert_eq!(new_options["configOptions"][2]["id"], "mode");
        assert_eq!(new_options["configOptions"][2]["category"], "mode");
        assert_eq!(new_options["configOptions"][2]["currentValue"], "ask");
        assert_eq!(
            new_options["configOptions"][2]["options"],
            serde_json::json!([
                {
                    "value": "ask",
                    "name": "Ask",
                    "description": "Ask before running each shell command or sending input to one."
                },
                {
                    "value": "auto",
                    "name": "Auto",
                    "description": "Run shell commands and send them input without asking."
                }
            ])
        );
        let set = |id: &'static str, value: &'static str| {
            state
                .set_config_option(&SetSessionConfigOptionRequest::new(
                    created.session_id.clone(),
                    id,
                    value,
                ))
                .map(|response| serde_json::to_value(response).unwrap())
        };
        let effort_values = |options: &serde_json::Value| {
            (
                options["configOptions"][1]["currentValue"].clone(),
                options["configOptions"][1]["options"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|option| option["value"].as_str().unwrap().to_owned())
                    .collect::<Vec<_>>(),
            )
        };
        assert_eq!(
            effort_values(&new_options),
            (
                serde_json::json!("default"),
                ["default", "low", "medium", "high", "max"]
                    .map(String::from)
                    .to_vec()
            )
        );
        assert_eq!(
            set("effort", "xhigh").unwrap_err().code,
            ErrorCode::InvalidParams,
            "the default model does not list xhigh"
        );
        set("effort", "max").unwrap();
        let chosen = openrouter::catalog()[1].id.as_str();
        let response = set("model", openrouter::catalog()[2].id.as_str()).unwrap();
        assert_eq!(
            effort_values(&response),
            (
                serde_json::json!("default"),
                ["default", "none", "medium", "high", "xhigh"]
                    .map(String::from)
                    .to_vec()
            ),
            "a model without the current effort level resets it"
        );
        set("effort", "xhigh").unwrap();
        let response = set("model", chosen).unwrap();
        assert_eq!(response["configOptions"][0]["currentValue"], chosen);
        assert_eq!(response["configOptions"][1]["currentValue"], "xhigh");
        assert_eq!(
            response["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            openrouter::catalog().len()
        );
        assert_eq!(
            set("mode", "auto").unwrap()["configOptions"][2]["currentValue"],
            "auto"
        );
        assert_eq!(
            set("mode", "unknown").unwrap_err().code,
            ErrorCode::InvalidParams
        );

        state
            .store
            .append_turn_start(
                &created.session_id,
                &TurnStart {
                    model: chosen.to_owned(),
                    mode: SessionMode::Auto,
                    ..TurnStart::test("Hello".to_owned())
                },
            )
            .unwrap();
        let changed = set("model", openrouter::fixture::DEFAULT_MODEL).unwrap();
        assert_eq!(
            changed["configOptions"][0]["currentValue"],
            openrouter::fixture::DEFAULT_MODEL,
            "the model changes after a turn start is saved"
        );
        assert_eq!(
            changed["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            openrouter::catalog().len()
        );
        state
            .store
            .append_batch(
                &created.session_id,
                &sessions::AssistantBatch::new(
                    sessions::AssistantMessage {
                        text: "Hi.".to_owned(),
                        reasoning: String::new(),
                        tool_calls: vec![],
                        continuation_metadata: vec![],
                        usage: Some(sessions::ModelUsage {
                            input_tokens: 40,
                            output_tokens: 2,
                            cost: 0.5,
                        }),
                    },
                    vec![],
                )
                .unwrap(),
            )
            .unwrap();

        let mut replayed = Vec::new();
        let loaded = state
            .load_session(
                &LoadSessionRequest::new(created.session_id, workspace_path),
                |update| {
                    replayed.push(update);
                    Ok(())
                },
            )
            .unwrap();
        let loaded = serde_json::to_value(loaded).unwrap();
        assert_eq!(loaded["configOptions"][0]["currentValue"], chosen);
        assert_eq!(loaded["configOptions"][2]["currentValue"], "auto");
        assert_eq!(replayed.len(), 3, "setting entries are not replayed");
        assert!(matches!(
            replayed.last(),
            Some(SessionUpdate::UsageUpdate(usage))
                if usage.used == 42
                    && usage.size == openrouter::catalog()[1].context_limit as u64
                    && usage.cost.as_ref().is_some_and(|cost| cost.amount == 0.5)
        ));
        assert_eq!(
            loaded["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            openrouter::catalog().len()
        );
    }

    #[tokio::test]
    async fn setting_changes_apply_to_the_next_turn_while_the_system_prompt_stays_captured() {
        use crate::openrouter::fixture::{Reply, Server, text_reply};

        let workspace = Workspace::new();
        let agents_md = workspace.0.join("AGENTS.md");
        fs::write(&agents_md, "Answer in French.\n").unwrap();
        let state = state();
        let id = create_session(&state, &workspace.0);
        state
            .set_config_option(&SetSessionConfigOptionRequest::new(
                id.clone(),
                "effort",
                "low",
            ))
            .unwrap();
        let server = Server::start(vec![
            Reply::Hang(": waiting\n\n".to_owned()),
            text_reply("Done"),
        ])
        .await;
        *state.openrouter.lock().unwrap() = Some(server.client());
        let input = |user_message: &str| {
            let active = state.active_session(&id).unwrap();
            prompt::PromptInput {
                session_id: id.clone(),
                turn_input: TurnInput::UserMessage(user_message.to_owned().into()),
                hook_sources: Vec::new(),
                selected_settings: active.selections,
                system_prompt: active.system_prompt,
                shell_processes: active.shell_processes,
            }
        };

        let changed_model = openrouter::catalog()[1].id.as_str();
        let cancellation = PromptCancellation::new();
        let first = prompt::run(
            state.store.clone(),
            server.client(),
            input("first"),
            cancellation.clone(),
            prompt::Presentation::headless(id.clone()),
        )
        .unwrap();
        let change = async {
            while server.requests().is_empty() {
                tokio::task::yield_now().await;
            }
            fs::write(&agents_md, "Answer in German.\n").unwrap();
            state
                .set_config_option(&SetSessionConfigOptionRequest::new(
                    id.clone(),
                    "model",
                    changed_model,
                ))
                .unwrap();
            state
                .set_config_option(&SetSessionConfigOptionRequest::new(
                    id.clone(),
                    "effort",
                    "max",
                ))
                .unwrap();
            state
                .set_config_option(&SetSessionConfigOptionRequest::new(
                    id.clone(),
                    "mode",
                    "auto",
                ))
                .unwrap();
            cancellation.cancel();
        };
        let (first_response, ()) = futures::join!(first, change);
        assert_eq!(first_response.unwrap(), prompt::PromptOutput::Cancelled);

        let second = prompt::run(
            state.store.clone(),
            server.client(),
            input("second"),
            PromptCancellation::new(),
            prompt::Presentation::headless(id.clone()),
        )
        .unwrap();
        assert!(matches!(
            second.await.unwrap(),
            prompt::PromptOutput::Finished(_)
        ));

        let stored = state.store.read(&id).unwrap().unwrap();
        let turn = |model: &str, effort, mode, text: &str| {
            TranscriptEntry::TurnStart(TurnStart {
                model: model.to_owned(),
                effort,
                mode,
                ..TurnStart::test(text.to_owned())
            })
        };
        assert_eq!(
            stored.transcript[0],
            turn(
                openrouter::fixture::DEFAULT_MODEL,
                EffortLevel::Low,
                SessionMode::Ask,
                "first"
            )
        );
        assert_eq!(
            stored.transcript[1],
            turn(changed_model, EffortLevel::Max, SessionMode::Auto, "second")
        );
        let requests = server.requests();
        assert_eq!(requests[0]["model"], openrouter::fixture::DEFAULT_MODEL);
        assert_eq!(requests[1]["model"], changed_model);
        assert_eq!(requests[0]["reasoning"]["effort"], "low");
        assert_eq!(requests[1]["reasoning"]["effort"], "max");
        for request in &requests {
            let prompt = request["messages"][0]["content"].as_str().unwrap();
            assert!(
                prompt.contains("Answer in French."),
                "every request uses the system prompt captured at activation"
            );
        }
        assert_eq!(requests[1]["messages"][1]["content"], "first");
        assert_eq!(requests[1]["messages"][2]["content"], "second");
    }

    #[tokio::test]
    async fn clean_eof_drains_a_blocked_final_response() {
        use agent_client_protocol::Lines;
        use futures::{
            SinkExt, StreamExt,
            channel::{mpsc, oneshot},
        };
        use serde_json::{Value, json};

        let workspace = Workspace::new();
        let state = state();
        let id = create_session(&state, &workspace.0);
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let (reached_tx, reached_rx) = oneshot::channel();
        let (release_tx, release_rx) = oneshot::channel::<()>();
        let mut gate = Some((reached_tx, release_rx));
        let sink = outgoing_tx
            .sink_map_err(io::Error::other)
            .with(move |line: String| {
                let message: Value = serde_json::from_str(&line).unwrap();
                let gate = (message["id"] == 2).then(|| gate.take()).flatten();
                async move {
                    if let Some((reached, release)) = gate {
                        let _ = reached.send(());
                        let _ = release.await;
                    }
                    Ok::<_, io::Error>(line)
                }
            });
        let transport = Lines::new(Box::pin(sink), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Hello"}]}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        drop(incoming_tx);

        let mut serving = Box::pin(serve(state, transport, std::future::pending()));
        tokio::select! {
            result = &mut serving => panic!("serve returned before the final response reached the sink: {result:?}"),
            reached = reached_rx => reached.unwrap(),
        }
        assert!(
            tokio::time::timeout(std::time::Duration::from_millis(100), &mut serving)
                .await
                .is_err(),
            "serve returned while the final response sink was blocked"
        );
        release_tx.send(()).unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(2), &mut serving)
            .await
            .unwrap()
            .unwrap();
        drop(serving);
        let mut final_response = None;
        while let Some(line) = outgoing_rx.next().await {
            let message: Value = serde_json::from_str(&line).unwrap();
            if message["id"] == 2 {
                final_response = Some(message);
            }
        }
        assert!(final_response.is_some());
    }

    #[tokio::test]
    async fn clean_eof_cancels_the_prompt_and_drains_its_response() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        use crate::openrouter::fixture::{Reply, Server, delta};
        use crate::sessions::TranscriptEntry;

        let prefix = format!(
            "data: {}\n\n",
            delta(json!({ "content": "Provisional" }), None),
        );
        let server = Server::start(vec![Reply::Hang(prefix)]).await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let store = state.store.clone();
        let operations = state.operations.clone();
        let inactive = store.create(Path::new("/workspace")).unwrap().id;
        let saved = store.create(Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(&saved, &TurnStart::test("Earlier".to_owned()))
            .unwrap();
        let id = create_session(&state, Path::new("/workspace"));
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);

        for message in [
            json!({
                "jsonrpc": "2.0", "id": 1, "method": "initialize",
                "params": { "protocolVersion": 1, "clientCapabilities": {} },
            }),
            json!({
                "jsonrpc": "2.0", "id": 2, "method": "session/new",
                "params": { "cwd": "/workspace", "mcpServers": [] },
            }),
            json!({
                "jsonrpc": "2.0", "id": 3, "method": "session/load",
                "params": { "sessionId": saved, "cwd": "/workspace", "mcpServers": [] },
            }),
            json!({
                "jsonrpc": "2.0", "id": 4, "method": "session/prompt",
                "params": { "sessionId": inactive, "prompt": [{ "type": "text", "text": "Hello" }] },
            }),
            json!({
                "jsonrpc": "2.0", "id": 5, "method": "session/prompt",
                "params": { "sessionId": id, "prompt": [{ "type": "text", "text": "Hello" }] },
            }),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }

        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut messages = Vec::new();
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                if message["params"]["update"]["sessionUpdate"] == "agent_message_chunk" {
                    assert!(operations.try_load(&id).is_none());
                    drop(incoming_tx.take());
                }
                messages.push(message);
            }
            assert!(incoming_tx.is_none(), "EOF was sent during inference");
            let position = |matches: &dyn Fn(&Value) -> bool| {
                messages
                    .iter()
                    .position(matches)
                    .expect("the message was sent")
            };
            let update = |session_id: &Value, kind: &str| {
                position(&|message: &Value| {
                    message["params"]["sessionId"] == *session_id
                        && message["params"]["update"]["sessionUpdate"] == kind
                })
            };
            let response = |id: u64| position(&|message: &Value| message["id"] == id);

            let created = &messages[response(2)]["result"]["sessionId"];
            assert!(response(2) < update(created, "available_commands_update"));
            let saved = json!(saved);
            assert!(update(&saved, "user_message_chunk") < response(3));
            assert!(messages[response(3)]["error"].is_null());
            assert!(response(3) < update(&saved, "available_commands_update"));

            let rejected = &messages[response(4)];
            assert_eq!(rejected["error"]["code"], -32600);
            assert_eq!(
                rejected["error"]["data"],
                format!("session {inactive} is not active; create or load it first")
            );
            assert_eq!(
                messages[response(5)]["result"]["stopReason"],
                "cancelled",
                "the final response was drained before shutdown"
            );
        };
        let (result, ()) = futures::join!(serve(state, transport, std::future::pending()), client);
        result.unwrap();
        assert!(operations_idle(&operations), "the session became available");
        assert!(
            store
                .read(&inactive)
                .unwrap()
                .unwrap()
                .transcript
                .is_empty(),
            "a rejected prompt saves no user message"
        );
        assert_eq!(
            store.read(&id).unwrap().unwrap().transcript,
            vec![TranscriptEntry::turn("Hello".to_owned())],
        );
    }

    async fn wait_for_file(path: &Path) {
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while !path.exists() {
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    }

    /// One shell permission request as the ACP client received it.
    struct PermissionRequest {
        params: serde_json::Value,
        /// Whether its call was announced as pending before the request.
        announced: bool,
        /// Whether the file its command creates existed at the request.
        target_existed: bool,
        /// Whether load, delete, and prompt were all refused for the session.
        session_busy: bool,
    }

    /// The calls a permission run's model makes in its one tool batch.
    #[derive(Clone, Copy, PartialEq)]
    enum Calls {
        /// `touch first` and `touch second`, each saving its outcome.
        Touch,
        /// `start` runs `echo $$ > started; exec sleep 30` in the background;
        /// then `list`, `write` of `hello\n` with stdin closure to a reader
        /// started before the prompt, which saves its line in `received` once
        /// `started` exists, a two-second `read` of the reader, and `stop` of
        /// a sleeper started before the prompt.
        Processes,
    }

    /// Starts `command` as a shell process of the active session `id`.
    fn start_shell_process(
        state: &ServerState,
        id: &SessionId,
        workspace: &Path,
        command: &str,
    ) -> crate::shell_processes::ShellProcess {
        let mut shell = tokio::process::Command::new("/bin/sh");
        shell.arg("-c").arg(command).current_dir(workspace);
        state
            .active_session(id)
            .unwrap()
            .shell_processes
            .start(shell, command, 1024)
            .unwrap()
    }

    /// A prompt over an ACP connection whose model makes `calls`. The client
    /// answers each permission request according to `decision`.
    struct PermissionRun {
        workspace: Workspace,
        server: crate::openrouter::fixture::Server,
        session_id: SessionId,
        requests: Vec<PermissionRequest>,
        /// Tool call updates as `<call ID> <status>`, in order.
        tool_updates: Vec<String>,
        /// The number of tool outcomes saved when the prompt response arrived.
        saved_at_response: usize,
        response: serde_json::Value,
        transcript: Vec<TranscriptEntry>,
        /// Whether every operation guard was dropped after the connection
        /// closed.
        session_free_after: bool,
        /// For `Calls::Processes`, the IDs of the reader and the sleeper.
        processes: Vec<String>,
    }

    impl PermissionRun {
        async fn new(decision: &'static str) -> Self {
            Self::with_calls(decision, Calls::Touch).await
        }

        async fn with_calls(decision: &'static str, calls: Calls) -> Self {
            use agent_client_protocol::Lines;
            use futures::{SinkExt, StreamExt, channel::mpsc};
            use serde_json::{Value, json};

            use crate::openrouter::fixture::{Server, calls_reply, shell_reply, text_reply};

            let workspace = Workspace::new();
            let mut state = state();
            let store = state.store.clone();
            let operations = state.operations.clone();
            let mut text = "Run commands";
            // The `hook` decision denies the call whose input contains this.
            let (denied, targets) = match calls {
                Calls::Touch => ("touch first", ["first", "second"]),
                Calls::Processes => ("hello", ["started", "received"]),
            };
            if decision == "hook" {
                let skill = workspace.0.join(".agents/skills/check");
                fs::create_dir_all(&skill).unwrap();
                fs::write(
                    skill.join("SKILL.md"),
                    format!(r#"---
name: check
description: Check each shell call.
hooks:
  before_tool:
    command: "case \"$(cat)\" in *'{denied}'*) echo '{{\"decision\":\"deny\",\"message\":\"Leave first alone.\"}}';; *) echo '{{\"decision\":\"allow\"}}';; esac"
---
Run the commands.
"#),
                )
                .unwrap();
                state.settings.global_hooks = Some(hooks::HookSource {
                    skill: None,
                    directory: workspace.0.clone(),
                    hooks: skills::load(&no_home(), &workspace.0)
                        .skills
                        .remove(0)
                        .hooks,
                });
                text = "/check Run commands";
            }
            let id = create_session(&state, &workspace.0);
            if decision == "auto" {
                state
                    .set_config_option(&SetSessionConfigOptionRequest::new(
                        id.clone(),
                        "mode",
                        "auto",
                    ))
                    .unwrap();
            }
            let mut processes = Vec::new();
            let mut replies = match calls {
                Calls::Touch => vec![shell_reply(&[("touch first", 5), ("touch second", 5)])],
                Calls::Processes => {
                    for command in [
                        "read line; while [ ! -e started ]; do sleep 0.01; done; printf %s \"$line\" > received",
                        "echo $$ > sleeper; exec sleep 30",
                    ] {
                        processes.push(
                            start_shell_process(&state, &id, &workspace.0, command)
                                .id()
                                .to_owned(),
                        );
                    }
                    vec![calls_reply(&[
                        (
                            "start",
                            tools::SHELL,
                            json!({"command":"echo $$ > started; exec sleep 30","background":true}),
                        ),
                        ("list", tools::SHELL_PROCESS, json!({"action":"list"})),
                        (
                            "write",
                            tools::SHELL_PROCESS,
                            json!({"action":"write","process_id":processes[0],"text":"hello\n","close_stdin":true}),
                        ),
                        (
                            "read",
                            tools::SHELL_PROCESS,
                            json!({"action":"read","process_id":processes[0],"wait_seconds":2}),
                        ),
                        (
                            "stop",
                            tools::SHELL_PROCESS,
                            json!({"action":"stop","process_id":processes[1]}),
                        ),
                    ])]
                }
            };
            if matches!(decision, "approve" | "auto" | "deny" | "mixed" | "hook") {
                replies.push(text_reply("Done"));
            }
            let server = Server::start(replies).await;
            *state.openrouter.lock().unwrap() = Some(server.client());
            let (incoming_tx, incoming_rx) = mpsc::unbounded();
            let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
            let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
            for message in [
                json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
                json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":text}]}}),
            ] {
                incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
            }
            let saved_outcomes = || {
                store
                    .read(&id)
                    .unwrap()
                    .unwrap()
                    .transcript
                    .iter()
                    .map(|entry| match entry {
                        TranscriptEntry::AssistantBatch(batch) => batch.outcomes.len(),
                        _ => 0,
                    })
                    .sum::<usize>()
            };
            let client = async {
                let mut incoming_tx = Some(incoming_tx);
                let mut requests = Vec::new();
                let mut tool_updates = Vec::new();
                let mut saved_at_response = 0;
                let mut response = None;
                while let Some(line) = outgoing_rx.next().await {
                    let message: Value = serde_json::from_str(&line).unwrap();
                    let update = &message["params"]["update"];
                    let call_id = update["toolCallId"].as_str().unwrap_or_default();
                    match update["sessionUpdate"].as_str() {
                        Some("tool_call") => tool_updates.push(format!("{call_id} pending")),
                        Some("tool_call_update") => tool_updates
                            .push(format!("{call_id} {}", update["status"].as_str().unwrap())),
                        _ => {}
                    }
                    if message["method"] == "session/request_permission" {
                        let params = message["params"].clone();
                        let file = targets[requests.len().min(1)];
                        let pending = format!(
                            "{} pending",
                            params["toolCall"]["toolCallId"].as_str().unwrap()
                        );
                        requests.push(PermissionRequest {
                            announced: tool_updates.contains(&pending),
                            target_existed: workspace.0.join(file).exists(),
                            session_busy: operations.try_load(&id).is_none()
                                && operations.try_delete(&id).is_none()
                                && operations.try_prompt(&id).is_none(),
                            params,
                        });
                        if decision == "eof" {
                            drop(incoming_tx.take());
                            continue;
                        }
                        let reply = match decision {
                            "cancel" => {
                                json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":id}})
                            }
                            "error" => {
                                json!({"jsonrpc":"2.0","id":message["id"],"error":{"code":-32603,"message":"Permission UI failed"}})
                            }
                            _ => {
                                let outcome = if decision == "cancelled" {
                                    json!({"outcome":"cancelled"})
                                } else {
                                    let option = match decision {
                                        "mixed" if requests.len() == 1 => "deny",
                                        "mixed" | "hook" => "approve",
                                        _ => decision,
                                    };
                                    json!({"outcome":"selected","optionId":option})
                                };
                                json!({"jsonrpc":"2.0","id":message["id"],"result":{"outcome":outcome}})
                            }
                        };
                        incoming_tx
                            .as_ref()
                            .unwrap()
                            .unbounded_send(Ok(reply.to_string()))
                            .unwrap();
                    } else if message["id"] == 2 {
                        saved_at_response = saved_outcomes();
                        response = Some(message);
                        drop(incoming_tx.take());
                    }
                }
                let response =
                    response.unwrap_or_else(|| panic!("{decision}: missing prompt response"));
                (requests, tool_updates, saved_at_response, response)
            };
            let (result, (requests, tool_updates, saved_at_response, response)) =
                tokio::time::timeout(std::time::Duration::from_secs(5), async {
                    tokio::join!(serve(state, transport, std::future::pending()), client)
                })
                .await
                .unwrap_or_else(|error| panic!("{decision}: timed out: {error}"));
            result.unwrap_or_else(|error| panic!("{decision}: ACP connection failed: {error}"));
            let session_free_after = operations_idle(&operations);
            let transcript = store.read(&id).unwrap().unwrap().transcript;
            Self {
                workspace,
                server,
                session_id: id,
                requests,
                tool_updates,
                saved_at_response,
                response,
                transcript,
                session_free_after,
                processes,
            }
        }

        fn outcomes(&self) -> Vec<&ToolOutcome> {
            self.transcript
                .iter()
                .flat_map(|entry| match entry {
                    TranscriptEntry::AssistantBatch(batch) => batch.outcomes.as_slice(),
                    _ => &[],
                })
                .collect()
        }
    }

    #[tokio::test]
    async fn shell_permission_decisions_control_execution_and_saved_outcomes() {
        let denied = "User denied permission to run this command.";
        // Each outcome is `completed`, `denied`, `failed`, or `cancelled`.
        for (decision, requests, created, outcomes, response) in [
            (
                "approve",
                2,
                [true, true],
                ["completed", "completed"],
                Ok("end_turn"),
            ),
            (
                "auto",
                0,
                [true, true],
                ["completed", "completed"],
                Ok("end_turn"),
            ),
            (
                "deny",
                2,
                [false, false],
                ["denied", "denied"],
                Ok("end_turn"),
            ),
            (
                "mixed",
                2,
                [false, true],
                ["denied", "completed"],
                Ok("end_turn"),
            ),
            (
                "hook",
                1,
                [false, true],
                ["failed", "completed"],
                Ok("end_turn"),
            ),
            (
                "cancelled",
                1,
                [false, false],
                ["cancelled", "cancelled"],
                Ok("cancelled"),
            ),
            (
                "cancel",
                1,
                [false, false],
                ["cancelled", "cancelled"],
                Ok("cancelled"),
            ),
            (
                "eof",
                1,
                [false, false],
                ["cancelled", "cancelled"],
                Ok("cancelled"),
            ),
            (
                "unknown",
                1,
                [false, false],
                ["failed", "failed"],
                Err(Some("Unknown shell permission option: unknown")),
            ),
            ("error", 1, [false, false], ["failed", "failed"], Err(None)),
        ] {
            let run = PermissionRun::new(decision).await;
            assert_eq!(run.requests.len(), requests, "{decision}");
            for (file, created) in ["first", "second"].into_iter().zip(created) {
                assert_eq!(
                    run.workspace.0.join(file).exists(),
                    created,
                    "{decision}: {file}"
                );
            }
            let saved = run.outcomes();
            assert_eq!(saved.len(), 2, "{decision}");
            for (index, (outcome, expected)) in saved.into_iter().zip(outcomes).enumerate() {
                assert!(
                    match expected {
                        "completed" => matches!(outcome, ToolOutcome::Completed(_)),
                        "denied" => *outcome == ToolOutcome::Failed(denied.to_owned()),
                        "failed" => matches!(outcome, ToolOutcome::Failed(_)),
                        _ => matches!(outcome, ToolOutcome::Cancelled(_)),
                    },
                    "{decision}, outcome {index}: {outcome:?}"
                );
            }
            match response {
                Ok(stop_reason) => {
                    assert_eq!(
                        run.response["result"]["stopReason"], stop_reason,
                        "{decision}"
                    );
                }
                Err(data) => {
                    assert_eq!(run.response["error"]["code"], -32603, "{decision}");
                    if let Some(data) = data {
                        assert_eq!(run.response["error"]["data"], data, "{decision}");
                    }
                }
            }
        }
    }

    #[tokio::test]
    async fn a_shell_permission_request_describes_the_pending_call_while_the_session_is_busy() {
        let run = PermissionRun::new("approve").await;
        for (index, request) in run.requests.iter().enumerate() {
            let file = ["first", "second"][index];
            let call = &request.params["toolCall"];
            assert_eq!(request.params["sessionId"], run.session_id.to_string());
            assert_eq!(call["toolCallId"], format!("shell-{index}"));
            assert!(request.announced, "{file}: announced before the request");
            assert!(!request.target_existed, "{file}: requested before it runs");
            assert!(request.session_busy, "{file}: the session stays busy");
            assert_eq!(call["status"], "pending");
            assert_eq!(call["kind"], "execute");
            assert_eq!(call["rawInput"]["command"], format!("touch {file}"));
            assert_eq!(call["title"], format!("touch {file}"));
            assert_eq!(
                call["content"][0]["content"]["text"],
                format!("Working directory: {}", run.workspace.0.display())
            );
            assert_eq!(
                request.params["options"],
                serde_json::json!([
                    {"optionId":"approve","name":"Approve","kind":"allow_once"},
                    {"optionId":"deny","name":"Deny","kind":"reject_once"},
                ])
            );
        }
        assert_eq!(
            run.saved_at_response, 2,
            "the batch is saved before responding"
        );
        assert!(run.session_free_after);
    }

    #[tokio::test]
    async fn auto_mode_runs_shell_calls_without_permission_requests() {
        let run = PermissionRun::new("auto").await;
        assert!(run.requests.is_empty());
        assert_eq!(
            run.tool_updates,
            [
                "shell-0 pending",
                "shell-1 pending",
                "shell-0 in_progress",
                "shell-0 completed",
                "shell-1 in_progress",
                "shell-1 completed",
            ]
        );
        assert!(matches!(
            &run.transcript[..1],
            [TranscriptEntry::TurnStart(TurnStart {
                mode: SessionMode::Auto,
                ..
            }),]
        ));
    }

    #[tokio::test]
    async fn a_before_tool_denial_skips_the_permission_request() {
        let run = PermissionRun::new("hook").await;
        assert_eq!(run.requests.len(), 1);
        assert_eq!(run.requests[0].params["toolCall"]["toolCallId"], "shell-1");
        let denied = "global before_tool hook denied this call: Leave first alone.\nskill /check before_tool hook denied this call: Leave first alone.";
        assert_eq!(*run.outcomes()[0], ToolOutcome::Failed(denied.to_owned()));
        assert_eq!(run.server.requests()[1]["messages"][3]["content"], denied);
    }

    #[tokio::test]
    async fn shell_process_permissions_cover_starts_and_writes_only() {
        let run_denied = "User denied permission to run this command.";
        let send_denied = "User denied permission to send this input.";
        let hook_denied = "global before_tool hook denied this call: Leave first alone.\nskill /check before_tool hook denied this call: Leave first alone.";
        // Each outcome is `completed`, `running` for a completed read of a
        // running command, or a failed outcome's exact text.
        for (decision, requested, outcomes, received) in [
            (
                "approve",
                &["start", "write"][..],
                [
                    "completed",
                    "completed",
                    "completed",
                    "completed",
                    "completed",
                ],
                true,
            ),
            (
                "auto",
                &[],
                [
                    "completed",
                    "completed",
                    "completed",
                    "completed",
                    "completed",
                ],
                true,
            ),
            (
                "deny",
                &["start", "write"],
                [run_denied, "completed", send_denied, "running", "completed"],
                false,
            ),
            (
                "hook",
                &["start"],
                [
                    "completed",
                    "completed",
                    hook_denied,
                    "running",
                    "completed",
                ],
                false,
            ),
        ] {
            let run = PermissionRun::with_calls(decision, Calls::Processes).await;
            assert_eq!(
                run.requests
                    .iter()
                    .map(|request| request.params["toolCall"]["toolCallId"].as_str().unwrap())
                    .collect::<Vec<_>>(),
                requested,
                "{decision}"
            );
            for request in &run.requests {
                assert!(request.announced && !request.target_existed, "{decision}");
            }
            let saved = run.outcomes();
            assert_eq!(saved.len(), 5, "{decision}");
            for (index, (outcome, expected)) in saved.into_iter().zip(outcomes).enumerate() {
                assert!(
                    match expected {
                        "completed" => matches!(outcome, ToolOutcome::Completed(_)),
                        "running" =>
                            matches!(outcome, ToolOutcome::Completed(text) if text.contains("State: running")),
                        denial => *outcome == ToolOutcome::Failed(denial.to_owned()),
                    },
                    "{decision}, outcome {index}: {outcome:?}"
                );
            }
            assert_eq!(
                run.response["result"]["stopReason"], "end_turn",
                "{decision}"
            );
            assert_eq!(
                fs::read_to_string(run.workspace.0.join("received")).ok(),
                received.then(|| "hello".to_owned()),
                "{decision}"
            );
            assert_process_stopped(&run.workspace.0.join("sleeper"), true).await;
            if outcomes[0] == "completed" {
                // Connection shutdown stops the command started in the prompt.
                assert_process_stopped(&run.workspace.0.join("started"), true).await;
            } else {
                assert!(!run.workspace.0.join("started").exists(), "{decision}");
            }
        }
    }

    #[tokio::test]
    async fn a_write_permission_request_shows_the_process_input_and_stdin_closure() {
        let run = PermissionRun::with_calls("approve", Calls::Processes).await;
        let content = |index: usize| {
            run.requests[index].params["toolCall"]["content"][0]["content"]["text"]
                .as_str()
                .unwrap()
                .to_owned()
        };
        assert!(content(0).ends_with("Approving it does not approve later input."));
        assert_eq!(
            run.requests[0].params["toolCall"]["title"],
            "Background: echo $$ > started; exec sleep 30"
        );
        assert_eq!(
            content(1),
            format!(
                "Shell process: {}\n\nCommand:\n\n    read line; while [ ! -e started ]; do sleep 0.01; done; printf %s \"$line\" > received\n\nInput:\n\n    hello\n\n\nCloses stdin afterward: yes",
                run.processes[0]
            )
        );
        assert_eq!(run.requests[1].params["toolCall"]["kind"], "execute");
    }

    async fn assert_process_stopped(path: &Path, reaped: bool) {
        let pid = std::fs::read_to_string(path).unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(3), async {
            loop {
                let output = tokio::process::Command::new("ps")
                    .args(["-o", "stat=", "-p", pid.trim()])
                    .output()
                    .await
                    .unwrap();
                let state = String::from_utf8(output.stdout).unwrap();
                if state.trim().is_empty() || (!reaped && state.trim().starts_with('Z')) {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn headless_signals_clean_up_and_save_even_when_repeated() {
        use crate::openrouter::fixture::{Reply, Server, calls_reply, text_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;
        use rustix::process::{Pid, Signal, kill_process, kill_process_group};

        const FLAG: &str = "OX_HEADLESS_SIGNAL_TEST";
        if let Some(path) = std::env::var_os(FLAG) {
            let path = Path::new(&path);
            let store = SessionStore::open(&path.join("ox.db")).unwrap();
            let command = "echo $$ > shell; python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; sleep 30 & echo $! > child; printf started; touch ready; wait";
            let background = |file: &str| {
                (
                    "background",
                    tools::SHELL,
                    serde_json::json!({
                        "command": format!("trap '' TERM; echo $$ > {file}; exec sleep 30"),
                        "background": true,
                    }),
                )
            };
            let shell = |command: &str, seconds: u64| serde_json::json!({"command": command, "timeout_seconds": seconds});
            // Each batch waits until its background command has written its
            // PID, so the run ends while it is running.
            let started = |file: &str| format!("while [ ! -e {file} ]; do sleep 0.01; done");
            let server = Server::start(vec![
                calls_reply(&[
                    background("finished-background"),
                    (
                        "wait",
                        tools::SHELL,
                        shell(&started("finished-background"), 5),
                    ),
                ]),
                text_reply("Finished answer."),
                calls_reply(&[
                    background("failed-background"),
                    (
                        "wait",
                        tools::SHELL,
                        shell(&started("failed-background"), 5),
                    ),
                ]),
                Reply::Status(500, "failed".to_owned()),
                calls_reply(&[
                    background("cancelled-background"),
                    (
                        "shell-0",
                        tools::SHELL,
                        shell(
                            &format!("{}; {command}", started("cancelled-background")),
                            30,
                        ),
                    ),
                    ("shell-1", tools::SHELL, shell("touch wrong", 5)),
                ]),
            ])
            .await;
            let answered = store.create(path).unwrap();
            let answer = run_headless_prompt(
                store.clone(),
                server.client(),
                answered.id,
                SessionSettings::new(openrouter::fixture::DEFAULT_MODEL, EffortLevel::Default),
                system_prompt::for_workspace(path).unwrap(),
                "Answer".into(),
                crate::settings::load(openrouter::catalog())
                    .unwrap()
                    .global_hooks
                    .into_iter()
                    .collect(),
            )
            .await
            .unwrap();
            assert_eq!(answer, "Finished answer.", "ox run prints this answer");
            assert_eq!(
                fs::read_to_string(path.join(".config/ox/reported")).unwrap(),
                "1"
            );
            assert_process_stopped(&path.join("finished-background"), true).await;
            let failed = store.create(path).unwrap();
            assert!(
                run_headless_prompt(
                    store.clone(),
                    server.client(),
                    failed.id,
                    SessionSettings::new(openrouter::fixture::DEFAULT_MODEL, EffortLevel::Default),
                    system_prompt::for_workspace(path).unwrap(),
                    "Fail".into(),
                    Vec::new(),
                )
                .await
                .is_err()
            );
            assert_process_stopped(&path.join("failed-background"), true).await;
            let session = store.create(path).unwrap();
            let response = run_headless_prompt(
                store.clone(),
                server.client(),
                session.id.clone(),
                SessionSettings::new(openrouter::catalog()[1].id.as_str(), EffortLevel::High),
                system_prompt::for_workspace(path).unwrap(),
                "Run commands".into(),
                Vec::new(),
            )
            .await;
            assert!(
                response.unwrap_err().to_string().contains("Cancelled"),
                "ox run prints no answer"
            );
            let transcript = store.read(&session.id).unwrap().unwrap().transcript;
            assert_eq!(
                &transcript[..1],
                [TranscriptEntry::TurnStart(TurnStart {
                    model: openrouter::catalog()[1].id.clone(),
                    effort: EffortLevel::High,
                    mode: SessionMode::Auto,
                    input: TurnInput::UserMessage("Run commands".to_owned().into()),
                }),]
            );
            assert_eq!(
                transcript
                    .iter()
                    .filter_map(|entry| match entry {
                        TranscriptEntry::AssistantBatch(batch) => Some(batch),
                        _ => None,
                    })
                    .flat_map(|batch| &batch.outcomes)
                    .filter(|outcome| matches!(outcome, ToolOutcome::Cancelled(_)))
                    .count(),
                2
            );
            std::fs::write(path.join("saved"), "yes").unwrap();
            return;
        }
        let workspace = Workspace::new();
        fs::create_dir_all(workspace.0.join(".config/ox")).unwrap();
        fs::write(workspace.0.join(".config/ox/settings.json"), format!(r#"{{"model":"{}","hooks":{{"after_run":{{"command":"printf %s \"$OX_IN_HOOK\" > reported; echo '{{}}'"}}}}}}"#, openrouter::fixture::DEFAULT_MODEL)).unwrap();
        let mut child = tokio::process::Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "acp::tests::headless_signals_clean_up_and_save_even_when_repeated",
                "--nocapture",
            ])
            .env(FLAG, &workspace.0)
            .env("HOME", &workspace.0)
            .env_remove(hooks::IN_HOOK_ENV)
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        wait_for_file(&workspace.0.join("ready")).await;
        let pid = Pid::from_raw(child.id().unwrap() as i32).unwrap();
        // SIGHUP begins cleanup like SIGINT and SIGTERM, which then repeat
        // harmlessly.
        kill_process(pid, Signal::HUP).unwrap();
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        assert_process_stopped(&workspace.0.join("cancelled-background"), true).await;
        for signal in [Signal::TERM, Signal::INT] {
            kill_process(pid, signal).unwrap();
        }
        let status = tokio::time::timeout(std::time::Duration::from_secs(5), child.wait()).await;
        let detached = std::fs::read_to_string(workspace.0.join("detached"))
            .unwrap()
            .parse()
            .unwrap();
        kill_process_group(Pid::from_raw(detached).unwrap(), Signal::KILL).unwrap();
        assert!(status.unwrap().unwrap().success());
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert_process_stopped(&workspace.0.join("cancelled-background"), true).await;
        assert!(workspace.0.join("saved").exists());
        assert!(!workspace.0.join("wrong").exists());
        let store = SessionStore::open(&workspace.0.join("ox.db")).unwrap();
        assert!(store.list(None).unwrap().iter().any(|session| store.read(&session.id).unwrap().unwrap().transcript.iter().any(|entry| matches!(entry,
            TranscriptEntry::AssistantBatch(batch) if batch.outcomes.iter().any(|outcome| matches!(outcome, ToolOutcome::Cancelled(text) if text.contains("started") && text.contains("partial changes")))))));
    }

    #[tokio::test]
    async fn a_hook_stopping_a_nested_headless_run_stops_its_background_commands_at_once() {
        use crate::openrouter::fixture::{Reply, Server, calls_reply};

        const FLAG: &str = "OX_NESTED_HEADLESS_TEST";
        if let Some(path) = std::env::var_os(FLAG) {
            let path = Path::new(&path);
            let store = SessionStore::open(&path.join("nested.db")).unwrap();
            let server = Server::start(vec![
                calls_reply(&[(
                    "background",
                    tools::SHELL,
                    serde_json::json!({
                        "command": "trap '' TERM; echo $$ > background; exec sleep 30",
                        "background": true,
                    }),
                )]),
                Reply::Hang(": waiting\n\n".to_owned()),
            ])
            .await;
            let session = store.create(path).unwrap();
            let result = run_headless_prompt(
                store,
                server.client(),
                session.id,
                SessionSettings::new(openrouter::fixture::DEFAULT_MODEL, EffortLevel::Default),
                String::new(),
                "Start the server".into(),
                Vec::new(),
            )
            .await;
            assert!(result.is_err(), "the terminated run prints no answer");
            fs::write(path.join("exited"), "yes").unwrap();
            return;
        }
        let workspace = Workspace::new();
        let hook = hooks::HookSource {
            skill: None,
            directory: workspace.0.clone(),
            hooks: hooks::Hooks {
                before_run: Some(hooks::HookCommand {
                    command: format!(
                        "{FLAG}='{}' exec '{}' --exact acp::tests::a_hook_stopping_a_nested_headless_run_stops_its_background_commands_at_once --nocapture",
                        workspace.0.display(),
                        std::env::current_exe().unwrap().display()
                    ),
                }),
                ..hooks::Hooks::default()
            },
        };
        let context = hooks::Context {
            skill: None,
            arguments: String::new(),
            session_id: "session-1".to_owned(),
            mode: SessionMode::Auto,
            run_id: "run-1".to_owned(),
            workspace: workspace.0.clone(),
            model: openrouter::fixture::DEFAULT_MODEL.to_owned(),
            effort: EffortLevel::Default,
        };
        let stopped_at = std::sync::Mutex::new(None);
        let result: io::Result<hooks::Feedback> =
            hooks::run(&hook, &context, &hooks::Event::BeforeRun, async {
                wait_for_file(&workspace.0.join("background")).await;
                *stopped_at.lock().unwrap() = Some(tokio::time::Instant::now());
            })
            .await;
        assert_eq!(result.unwrap_err().kind(), io::ErrorKind::Interrupted);
        let stopped_at = stopped_at.lock().unwrap().unwrap();
        assert!(
            stopped_at.elapsed() < std::time::Duration::from_secs(2),
            "the nested run exited within the hook's grace period"
        );
        assert!(
            workspace.0.join("exited").exists(),
            "the nested run finished its own cleanup"
        );
        assert_process_stopped(&workspace.0.join("background"), true).await;
    }

    #[tokio::test]
    async fn acp_shutdown_waits_for_shell_cleanup_saving_and_response() {
        use crate::openrouter::fixture::{Server, shell_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        let workspace = Workspace::new();
        let server = Server::start(vec![shell_reply(&[
            (
                "echo $$ > shell; sleep 30 & echo $! > child; printf started; touch ready; wait",
                30,
            ),
            ("touch wrong", 5),
        ])])
        .await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let store = state.store.clone();
        let operations = state.operations.clone();
        let id = create_session(&state, &workspace.0);
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Run commands"}]}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut response = None;
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                if message["method"] == "session/request_permission" {
                    incoming_tx
                        .as_ref()
                        .unwrap()
                        .unbounded_send(Ok(json!({
                            "jsonrpc":"2.0", "id":message["id"],
                            "result":{"outcome":{"outcome":"selected","optionId":"approve"}}
                        })
                        .to_string()))
                        .unwrap();
                    wait_for_file(&workspace.0.join("ready")).await;
                    assert!(operations.try_load(&id).is_none());
                    drop(incoming_tx.take());
                } else if message["id"] == 2 {
                    assert!(
                        store
                            .read(&id)
                            .unwrap()
                            .unwrap()
                            .transcript
                            .iter()
                            .any(|entry| matches!(entry, TranscriptEntry::AssistantBatch(batch) if !batch.outcomes.is_empty())),
                        "saved before response"
                    );
                    response = Some(message);
                }
            }
            assert_eq!(response.unwrap()["result"]["stopReason"], "cancelled");
        };
        let (result, ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(serve(state, transport, std::future::pending()), client)
        })
        .await
        .unwrap();
        result.unwrap();
        assert!(operations_idle(&operations));
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert!(!workspace.0.join("wrong").exists());
        let transcript = store.read(&id).unwrap().unwrap().transcript;
        assert_eq!(
            transcript
                .iter()
                .filter_map(|entry| match entry {
                    TranscriptEntry::AssistantBatch(batch) => Some(batch),
                    _ => None,
                })
                .flat_map(|batch| &batch.outcomes)
                .filter(|outcome| matches!(outcome, ToolOutcome::Cancelled(_)))
                .count(),
            2
        );
    }

    #[tokio::test]
    async fn transport_error_stops_the_shell_process_groups() {
        use crate::openrouter::fixture::{Server, shell_reply};
        use crate::tools::fixture::Workspace;
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::json;

        let workspace = Workspace::new();
        let server = Server::start(vec![shell_reply(&[(
            "echo $$ > shell; sleep 30 & echo $! > child; touch ready; wait",
            30,
        )])])
        .await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let id = create_session(&state, &workspace.0);
        start_shell_process(
            &state,
            &id,
            &workspace.0,
            "trap '' TERM; echo $$ > background; exec sleep 30",
        );
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Run commands"}]}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        let fail_transport = async {
            wait_for_file(&workspace.0.join("background")).await;
            wait_for_file(&workspace.0.join("ready")).await;
            incoming_tx
                .unbounded_send(Err(io::Error::other("broken transport")))
                .unwrap();
        };
        let drain_output = async {
            while let Some(line) = outgoing_rx.next().await {
                let message: serde_json::Value = serde_json::from_str(&line).unwrap();
                if message["method"] == "session/request_permission" {
                    incoming_tx
                        .unbounded_send(Ok(json!({
                            "jsonrpc":"2.0", "id":message["id"],
                            "result":{"outcome":{"outcome":"selected","optionId":"approve"}}
                        })
                        .to_string()))
                        .unwrap();
                }
            }
        };
        let (result, (), ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(
                serve(state, transport, std::future::pending()),
                fail_transport,
                drain_output
            )
        })
        .await
        .unwrap();
        assert!(
            result.is_err(),
            "the transport error reaches the ACP server"
        );
        assert_process_stopped(&workspace.0.join("shell"), false).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert_process_stopped(&workspace.0.join("background"), true).await;
    }

    /// Runs one Auto mode turn of the active session `id` against `replies`
    /// and returns its output with the outcomes of its latest tool batch.
    /// With `cancel`, the turn is cancelled when its first tool call finishes.
    async fn run_auto_turn(
        state: &ServerState,
        id: &SessionId,
        replies: Vec<crate::openrouter::fixture::Reply>,
        cancel: bool,
    ) -> (prompt::PromptOutput, Vec<ToolOutcome>) {
        use agent_client_protocol::schema::v1::ToolCallStatus;
        let server = crate::openrouter::fixture::Server::start(replies).await;
        let active = state.active_session(id).unwrap();
        let cancellation = PromptCancellation::new();
        let cancel_after = cancellation.clone();
        let output = prompt::run(
            state.store.clone(),
            server.client(),
            prompt::PromptInput {
                session_id: id.clone(),
                turn_input: TurnInput::UserMessage("Go".to_owned().into()),
                hook_sources: Vec::new(),
                selected_settings: active.selections.with_mode(SessionMode::Auto),
                system_prompt: active.system_prompt,
                shell_processes: active.shell_processes,
            },
            cancellation,
            prompt::Presentation::observed(id.clone(), move |update| {
                if cancel
                    && matches!(&update, SessionUpdate::ToolCallUpdate(update)
                        if update.fields.status == Some(ToolCallStatus::Completed))
                {
                    cancel_after.cancel();
                }
                Ok(())
            }),
        )
        .unwrap()
        .await
        .unwrap();
        let transcript = state.store.read(id).unwrap().unwrap().transcript;
        let outcomes = transcript
            .iter()
            .rev()
            .find_map(|entry| match entry {
                TranscriptEntry::AssistantBatch(batch) if !batch.outcomes.is_empty() => {
                    Some(batch.outcomes.clone())
                }
                _ => None,
            })
            .unwrap_or_default();
        (output, outcomes)
    }

    #[tokio::test]
    async fn shell_processes_outlive_turns_cancellation_and_repeated_load_of_their_session_only() {
        use crate::openrouter::fixture::{calls_reply, text_reply};
        use serde_json::json;

        let workspace = Workspace::new();
        let state = state();
        let id = create_session(&state, &workspace.0);
        let other = create_session(&state, &workspace.0);
        let (output, outcomes) = run_auto_turn(
            &state,
            &id,
            vec![calls_reply(&[(
                "start",
                tools::SHELL,
                json!({"command":"read line; printf 'got:%s' \"$line\"","background":true}),
            )])],
            true,
        )
        .await;
        assert_eq!(output, prompt::PromptOutput::Cancelled);
        let owner = state.active_session(&id).unwrap().shell_processes;
        let process = owner.list().remove(0);
        let process_id = process.id().to_owned();
        assert!(
            matches!(&outcomes[..], [ToolOutcome::Completed(text)]
                if text.starts_with(&format!("Started shell process {process_id}."))),
            "{outcomes:?}"
        );
        assert_eq!(
            process.state(),
            crate::shell_processes::State::Running,
            "prompt cancellation keeps the command running"
        );

        state
            .load_session(&LoadSessionRequest::new(id.clone(), &workspace.0), |_| {
                Ok(())
            })
            .unwrap();
        assert!(
            state
                .active_session(&id)
                .unwrap()
                .shell_processes
                .get(&process_id)
                .is_some(),
            "a repeated load keeps the shell processes"
        );

        let read = json!({"action":"read","process_id":process_id,"wait_seconds":5});
        let (_, outcomes) = run_auto_turn(
            &state,
            &other,
            vec![
                calls_reply(&[("read", tools::SHELL_PROCESS, read.clone())]),
                text_reply("Not mine."),
            ],
            false,
        )
        .await;
        assert!(
            matches!(&outcomes[..], [ToolOutcome::Failed(text)]
                if text.starts_with(&format!("No shell process {process_id} in this session."))),
            "another session cannot reach it: {outcomes:?}"
        );

        let (output, outcomes) = run_auto_turn(
            &state,
            &id,
            vec![
                calls_reply(&[
                    (
                        "write",
                        tools::SHELL_PROCESS,
                        json!({"action":"write","process_id":process_id,"text":"hi\n"}),
                    ),
                    ("read", tools::SHELL_PROCESS, read),
                ]),
                text_reply("Done."),
            ],
            false,
        )
        .await;
        assert!(matches!(output, prompt::PromptOutput::Finished(_)));
        assert!(
            matches!(&outcomes[..], [ToolOutcome::Completed(_), ToolOutcome::Completed(text)]
                if text.contains("State: exited\nExit code: 0") && text.contains("stdout:\ngot:hi")),
            "a later turn uses the command: {outcomes:?}"
        );

        let later = state_over(state.store.clone());
        let mut replayed = Vec::new();
        later
            .load_session(
                &LoadSessionRequest::new(id.clone(), &workspace.0),
                |update| {
                    replayed.push(update);
                    Ok(())
                },
            )
            .unwrap();
        assert!(
            later
                .active_session(&id)
                .unwrap()
                .shell_processes
                .list()
                .is_empty(),
            "loading saved observations creates no process"
        );
        assert!(replayed.iter().any(|update| matches!(update,
            SessionUpdate::ToolCall(call) if call.raw_output.as_ref().and_then(serde_json::Value::as_str)
                .is_some_and(|text| text.starts_with(&format!("Started shell process {process_id}."))))));
    }

    #[tokio::test]
    async fn deletion_stops_its_shell_processes_while_other_sessions_respond() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        let workspace = Workspace::new();
        let state = state();
        let observer = state.clone();
        let doomed = create_session(&state, &workspace.0);
        let kept = create_session(&state, &workspace.0);
        // The detached process keeps the output pipes open, so cleanup waits
        // for the output drain deadline.
        start_shell_process(
            &state,
            &doomed,
            &workspace.0,
            "python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; echo $$ > doomed; exec sleep 30",
        );
        start_shell_process(&state, &kept, &workspace.0, "echo $$ > kept; exec sleep 30");
        for file in ["doomed", "kept"] {
            wait_for_file(&workspace.0.join(file)).await;
        }
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/delete", "params":{"sessionId":doomed}}),
            json!({"jsonrpc":"2.0", "id":3, "method":"session/list", "params":{}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut responses = Vec::new();
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                match message["id"].as_u64() {
                    Some(3) => {
                        assert!(
                            observer.active_session(&doomed).is_some(),
                            "the deleting session keeps its shell processes until cleanup"
                        );
                        responses.push(3);
                    }
                    Some(2) => {
                        assert!(message["error"].is_null(), "{message}");
                        assert!(observer.active_session(&doomed).is_none());
                        responses.push(2);
                        drop(incoming_tx.take());
                    }
                    _ => {}
                }
            }
            responses
        };
        let (result, responses) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(serve(state, transport, std::future::pending()), client)
        })
        .await
        .unwrap();
        let detached = fs::read_to_string(workspace.0.join("detached")).unwrap();
        rustix::process::kill_process(
            rustix::process::Pid::from_raw(detached.parse().unwrap()).unwrap(),
            rustix::process::Signal::KILL,
        )
        .unwrap();
        result.unwrap();
        assert_eq!(
            responses,
            [3, 2],
            "another session responds while deletion waits"
        );
        assert_process_stopped(&workspace.0.join("doomed"), true).await;
        assert_process_stopped(&workspace.0.join("kept"), true).await;
    }

    #[tokio::test]
    async fn a_failed_deletion_keeps_the_session_and_its_shell_processes() {
        let workspace = Workspace::new();
        let state = state();
        let id = create_session(&state, &workspace.0);
        let process = start_shell_process(&state, &id, &workspace.0, "exec sleep 30");
        state.store.with_connection(|connection| {
            connection
                .execute_batch(
                    "CREATE TRIGGER refuse BEFORE DELETE ON sessions
                     BEGIN SELECT RAISE(ABORT, 'disk full'); END;",
                )
                .unwrap()
        });
        assert!(
            state
                .delete_session(&DeleteSessionRequest::new(id.clone()))
                .await
                .is_err()
        );
        assert!(state.active_session(&id).is_some());
        assert_eq!(process.state(), crate::shell_processes::State::Running);
        state.shutdown_shell_processes().await;
    }

    #[tokio::test]
    async fn a_termination_signal_rejects_new_operations_and_stops_every_shell_process() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc, channel::oneshot};
        use serde_json::{Value, json};

        let workspace = Workspace::new();
        let state = state();
        let operations = state.operations.clone();
        let id = create_session(&state, &workspace.0);
        let idle = create_session(&state, &workspace.0);
        start_shell_process(
            &state,
            &id,
            &workspace.0,
            "trap '' TERM; echo $$ > stubborn; exec sleep 30",
        );
        wait_for_file(&workspace.0.join("stubborn")).await;
        // An operation still running when the signal arrives keeps the
        // connection serving while shutdown waits for it.
        let held = operations.try_load(&idle).unwrap();
        let (signal_tx, signal_rx) = oneshot::channel::<()>();
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        incoming_tx
            .unbounded_send(Ok(json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}).to_string()))
            .unwrap();
        let client = async {
            let mut signal_tx = Some(signal_tx);
            let mut held = Some(held);
            let mut rejected = None;
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                match message["id"].as_u64() {
                    Some(1) => {
                        signal_tx.take().unwrap().send(()).unwrap();
                        while !operations.is_closed() {
                            tokio::task::yield_now().await;
                        }
                        incoming_tx
                            .unbounded_send(Ok(json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Hello"}]}}).to_string()))
                            .unwrap();
                    }
                    Some(2) => {
                        rejected = Some(message["error"]["data"].clone());
                        drop(held.take());
                    }
                    _ => {}
                }
            }
            rejected
        };
        let (result, rejected) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(
                serve(state, transport, async {
                    let _ = signal_rx.await;
                }),
                client
            )
        })
        .await
        .unwrap();
        result.unwrap();
        assert_eq!(rejected, Some(serde_json::json!("Ox is shutting down")));
        assert!(
            operations_idle(&operations),
            "the connection ended after its operations, without incoming EOF"
        );
        assert_process_stopped(&workspace.0.join("stubborn"), true).await;
    }

    #[test]
    fn listing_rejects_cursors() {
        let state = state();
        let paged = ListSessionsRequest::new().cursor("next");
        assert_eq!(
            state.list_sessions(&paged).unwrap_err().code,
            ErrorCode::InvalidParams
        );
    }
}
