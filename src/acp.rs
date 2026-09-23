//! The ACP connection: request handlers over one shared process state.

pub(crate) mod convert;
pub(crate) mod operations;
mod prompt;

use std::{
    collections::HashMap,
    error::Error as StdError,
    io::{self, ErrorKind},
    path::Path,
    sync::{Arc, Mutex},
};

use agent_client_protocol::{
    Agent, Client, ConnectTo, ConnectionTo, Error, JsonRpcResponse, Responder, Result, Stdio,
    schema::ProtocolVersion,
    schema::v1::{
        AgentAuthCapabilities, AgentCapabilities, AuthMethod, AuthMethodTerminal, AvailableCommand,
        AvailableCommandInput, AvailableCommandsUpdate, CancelNotification, DeleteSessionRequest,
        DeleteSessionResponse, InitializeRequest, InitializeResponse, ListSessionsRequest,
        ListSessionsResponse, LoadSessionRequest, LoadSessionResponse, LogoutCapabilities,
        LogoutRequest, LogoutResponse, NewSessionRequest, NewSessionResponse, PromptRequest,
        PromptResponse, SessionCapabilities, SessionConfigOption, SessionConfigOptionCategory,
        SessionConfigOptionValue, SessionConfigSelectOption, SessionDeleteCapabilities, SessionId,
        SessionInfo, SessionListCapabilities, SessionNotification, SessionUpdate,
        SetSessionConfigOptionRequest, SetSessionConfigOptionResponse, StopReason,
        UnstructuredCommandInput,
    },
};

use crate::{
    auth,
    cancellation::PromptCancellation,
    compaction, hooks, openrouter,
    sessions::{
        self, EffortLevel, SessionMode, SessionSettings, SessionStore, SessionSummary,
        SkillInvocation, TranscriptEntry,
    },
    settings,
    skills::{self, Skill},
    system_prompt,
};
use operations::SessionOperations;

fn default_settings() -> SessionSettings {
    SessionSettings::new(openrouter::DEFAULT_MODEL, EffortLevel::Default)
}

fn validate_settings(settings: &SessionSettings) -> Result<()> {
    if openrouter::catalog_model(&settings.model).is_some() {
        return Ok(());
    }
    Err(Error::into_internal_error(io::Error::new(
        ErrorKind::InvalidData,
        format!(
            "session model {} is not in the model catalog",
            settings.model
        ),
    )))
}

fn config_options(settings: &SessionSettings, model_locked: bool) -> Vec<SessionConfigOption> {
    let models = if model_locked {
        let model = openrouter::catalog_model(&settings.model)
            .expect("a session model comes from the model catalog");
        vec![SessionConfigSelectOption::new(model.id, model.name)]
    } else {
        openrouter::MODEL_CATALOG
            .iter()
            .map(|model| SessionConfigSelectOption::new(model.id, model.name))
            .collect()
    };
    vec![
        SessionConfigOption::select("model", "Model", settings.model.clone(), models)
            .category(SessionConfigOptionCategory::Model),
        SessionConfigOption::select(
            "effort",
            "Effort",
            settings.effort.id(),
            EffortLevel::ALL
                .into_iter()
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
        hooks: Box<hooks::RunHooks>,
    },
    UserMessage(String),
}

/// A prompt whose first word is `/compact` or `/<name>` for a catalog skill is
/// a command; the rest of its text, trimmed, is literal skill arguments. Any
/// other text is a user message.
fn dispatch(user_message: String, skills: &[Skill]) -> Dispatch {
    let text = user_message.trim();
    let (word, rest) = text.split_once(char::is_whitespace).unwrap_or((text, ""));
    if word == "/compact" {
        return Dispatch::Compact;
    }
    let Some(skill) = word
        .strip_prefix('/')
        .and_then(|name| skills.iter().find(|skill| skill.name == name))
    else {
        return Dispatch::UserMessage(user_message);
    };
    let arguments = rest.trim().to_owned();
    Dispatch::Skill {
        hooks: Box::new(hooks::RunHooks {
            hooks: skill.hooks.clone(),
            skill: Some(skill.name.clone()),
            directory: skill.directory.clone(),
        }),
        invocation: SkillInvocation {
            name: skill.name.clone(),
            arguments,
            instructions: skill.instructions.clone(),
        },
    }
}

#[derive(Clone)]
struct ServerState {
    store: SessionStore,
    global_hooks: Option<hooks::RunHooks>,
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
}

impl ServerState {
    fn new(store: SessionStore, global_hooks: Option<hooks::RunHooks>) -> Self {
        Self {
            store,
            global_hooks,
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
        let mut settings = stored.saved_settings(&default_settings());
        validate_settings(&settings)?;
        settings.effort = active.selections.effort;
        let mut transcript = stored.transcript;
        match compaction::compact(
            &self.store,
            &client,
            cancellation,
            session_id,
            &settings,
            &active.system_prompt,
            &mut transcript,
        )
        .await
        {
            Ok(_) => Ok(PromptResponse::new(if cancellation.is_cancelled() {
                StopReason::Cancelled
            } else {
                StopReason::EndTurn
            })),
            Err(error) if error.kind() == ErrorKind::Interrupted => {
                Ok(PromptResponse::new(StopReason::Cancelled))
            }
            Err(error) => Err(Error::into_internal_error(error)),
        }
    }

    fn new_session(&self, request: &NewSessionRequest) -> Result<NewSessionResponse> {
        self.openrouter_client()?;
        let system_prompt =
            system_prompt::for_workspace(&request.cwd).map_err(Error::into_internal_error)?;
        let skills = skills::load(&request.cwd).map_err(Error::into_internal_error)?;
        let summary = self.store.create(&request.cwd).map_err(store_error)?;
        let settings = default_settings();
        self.activate(
            summary.id.clone(),
            ActiveSession {
                selections: settings.clone(),
                system_prompt,
                skills,
            },
        );
        Ok(NewSessionResponse::new(summary.id).config_options(config_options(&settings, false)))
    }

    fn set_config_option(
        &self,
        request: &SetSessionConfigOptionRequest,
    ) -> Result<SetSessionConfigOptionResponse> {
        let SessionConfigOptionValue::ValueId { value } = &request.value else {
            return Err(Error::invalid_params().data("every configuration option is a selector"));
        };
        let stored = self
            .store
            .read(&request.session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| not_found(&request.session_id))?;
        let saved_settings = stored.saved_settings(&default_settings());
        validate_settings(&saved_settings)?;
        let model_locked = !stored.transcript.is_empty();
        let mut active = self.active.lock().expect("active sessions mutex poisoned");
        let selections = &mut active
            .get_mut(&request.session_id)
            .ok_or_else(|| inactive(&request.session_id))?
            .selections;
        if model_locked {
            selections.model.clone_from(&saved_settings.model);
        }
        match request.config_id.0.as_ref() {
            "model" => {
                if model_locked {
                    return Err(Error::invalid_params().data("the session model cannot be changed"));
                }
                let model = openrouter::catalog_model(value.0.as_ref()).ok_or_else(|| {
                    Error::invalid_params().data(format!(
                        "{} is not a choice of configuration option {}",
                        value, request.config_id
                    ))
                })?;
                selections.model = model.id.to_owned();
            }
            "effort" => {
                selections.effort = EffortLevel::from_id(value.0.as_ref()).ok_or_else(|| {
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
        let options = config_options(selections, model_locked);
        drop(active);
        Ok(SetSessionConfigOptionResponse::new(options))
    }

    fn load_session(
        &self,
        request: &LoadSessionRequest,
        send_update: impl FnMut(SessionUpdate) -> Result<()>,
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
        let saved_settings = stored.saved_settings(&default_settings());
        validate_settings(&saved_settings)?;
        let model_locked = !stored.transcript.is_empty();
        // A repeated load keeps the prefix and skill catalog captured by the
        // first load.
        let (system_prompt, skills) = match self.active_session(&request.session_id) {
            Some(active) => (active.system_prompt, active.skills),
            None => (
                system_prompt::for_workspace(&stored.summary.workspace_path)
                    .map_err(Error::into_internal_error)?,
                skills::load(&stored.summary.workspace_path).map_err(Error::into_internal_error)?,
            ),
        };
        self.activate(
            request.session_id.clone(),
            ActiveSession {
                selections: saved_settings.clone(),
                system_prompt,
                skills,
            },
        );
        convert::replay_transcript(&stored.transcript, send_update)?;
        Ok(
            LoadSessionResponse::new()
                .config_options(config_options(&saved_settings, model_locked)),
        )
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

    fn delete_session(&self, request: &DeleteSessionRequest) -> Result<DeleteSessionResponse> {
        self.store
            .delete(&request.session_id)
            .map_err(Error::into_internal_error)?;
        self.active
            .lock()
            .expect("active sessions mutex poisoned")
            .remove(&request.session_id);
        Ok(DeleteSessionResponse::new())
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

fn busy() -> Error {
    Error::invalid_request().data("session has an operation in progress")
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

pub async fn serve_stdio() -> std::result::Result<(), Box<dyn StdError>> {
    let store = SessionStore::open(&sessions::database_path()?)?;
    serve(ServerState::new(store, settings::load()?), Stdio::new()).await?;
    Ok(())
}

/// Runs one prompt in a new session and returns the final answer. Skills are
/// not invoked.
pub async fn run_headless(
    workspace_path: &Path,
    model: String,
    effort: EffortLevel,
    user_message: String,
) -> std::result::Result<String, Box<dyn StdError>> {
    let api_key = auth::api_key()?.ok_or_else(|| {
        io::Error::new(
            ErrorKind::PermissionDenied,
            "OpenRouter authentication required; run `ox auth login`",
        )
    })?;
    let system_prompt = system_prompt::for_workspace(workspace_path)?;
    let hooks = settings::load()?.into_iter().collect();
    let store = SessionStore::open(&sessions::database_path()?)?;
    let session = store.create(workspace_path)?;
    run_headless_prompt(
        store,
        openrouter::Client::new(api_key),
        session.id,
        SessionSettings::new(model, effort),
        system_prompt,
        user_message,
        hooks,
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
    hooks: Vec<hooks::RunHooks>,
) -> std::result::Result<String, Box<dyn StdError>> {
    use tokio::signal::unix::{SignalKind, signal};
    let mut interrupt = signal(SignalKind::interrupt())?;
    let mut terminate = signal(SignalKind::terminate())?;
    let cancellation = PromptCancellation::new();
    let run = prompt::run(
        store,
        openrouter,
        prompt::PromptInput {
            session_id,
            turn_start: TranscriptEntry::UserMessage(user_message),
            hooks,
            selected_settings: Some(settings.with_mode(SessionMode::Auto)),
            system_prompt,
        },
        cancellation.clone(),
        |_| Ok(()),
        prompt::PermissionTransport::None,
    )?;
    tokio::pin!(run);
    let output = loop {
        tokio::select! {
            biased;
            output = &mut run => break output?,
            signal = interrupt.recv() => {
                signal.expect("SIGINT listener remains open");
                cancellation.cancel();
            }
            signal = terminate.recv() => {
                signal.expect("SIGTERM listener remains open");
                cancellation.cancel();
            }
        }
    };
    match output.answer {
        Some(answer) => Ok(answer),
        None => {
            Err(io::Error::other(format!("prompt stopped with {:?}", output.stop_reason)).into())
        }
    }
}

async fn serve(state: ServerState, transport: impl ConnectTo<Agent> + 'static) -> Result<()> {
    let shutdown_operations = state.operations.clone();
    let new_state = state.clone();
    let load_state = state.clone();
    let list_state = state.clone();
    let delete_state = state.clone();
    let config_state = state.clone();
    let logout_state = state.clone();
    let prompt_state = state.clone();
    let cancel_state = state;

    Agent
        .builder()
        .name("ox")
        .on_close(async move |_connection| {
            shutdown_operations.shutdown().await;
            Ok(())
        })
        .on_receive_request(
            async move |initialize: InitializeRequest, responder, _connection| {
                responder.respond(initialize_response(&initialize))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: NewSessionRequest, responder, connection| match new_state
                .new_session(&request)
            {
                Ok(response) => {
                    let session_id = response.session_id.clone();
                    responder.respond(response)?;
                    new_state.send_available_commands(&connection, session_id)
                }
                Err(error) => responder.respond_with_error(error),
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: LoadSessionRequest, responder, connection| {
                let Some(_guard) = load_state.operations.try_load(&request.session_id) else {
                    return responder.respond_with_error(busy());
                };
                let result = load_state.load_session(&request, |update| {
                    connection.send_notification(SessionNotification::new(
                        request.session_id.clone(),
                        update,
                    ))
                });
                match result {
                    Ok(response) => {
                        responder.respond(response)?;
                        load_state.send_available_commands(&connection, request.session_id)
                    }
                    Err(error) => responder.respond_with_error(error),
                }
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
            async move |request: DeleteSessionRequest, responder, _connection| {
                let Some(_guard) = delete_state.operations.try_delete(&request.session_id) else {
                    return responder.respond_with_error(busy());
                };
                reply(responder, delete_state.delete_session(&request))
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
                let user_message = match convert::prompt_to_user_message(&request.prompt) {
                    Ok(user_message) => user_message,
                    Err(error) => return responder.respond_with_error(error),
                };
                let Some((guard, cancellation)) =
                    prompt_state.operations.try_prompt(&request.session_id)
                else {
                    return responder.respond_with_error(busy());
                };
                let Some(active) = prompt_state.active_session(&request.session_id) else {
                    return responder.respond_with_error(inactive(&request.session_id));
                };
                let (turn_start, hooks) = match dispatch(user_message, &active.skills) {
                    Dispatch::Compact => {
                        let session_id = request.session_id;
                        let compact_state = prompt_state.clone();
                        return connection.spawn(async move {
                            let _guard = guard;
                            let result = compact_state
                                .compact_session(&session_id, active, &cancellation)
                                .await;
                            reply(responder, result)
                        });
                    }
                    Dispatch::Skill { invocation, hooks } => {
                        (TranscriptEntry::SkillInvocation(invocation), Some(*hooks))
                    }
                    Dispatch::UserMessage(text) => (TranscriptEntry::UserMessage(text), None),
                };
                let openrouter = match prompt_state.openrouter_client() {
                    Ok(openrouter) => openrouter,
                    Err(error) => return responder.respond_with_error(error),
                };
                let session_id = request.session_id;
                let task_connection = connection.clone();
                let send_update = {
                    let task_connection = task_connection.clone();
                    let session_id = session_id.clone();
                    move |update| {
                        task_connection
                            .send_notification(SessionNotification::new(session_id.clone(), update))
                    }
                };
                let run = match prompt::run(
                    prompt_state.store.clone(),
                    openrouter,
                    prompt::PromptInput {
                        session_id: session_id.clone(),
                        turn_start,
                        hooks: prompt_state
                            .global_hooks
                            .clone()
                            .into_iter()
                            .chain(hooks)
                            .collect(),
                        selected_settings: Some(active.selections),
                        system_prompt: active.system_prompt,
                    },
                    cancellation,
                    send_update,
                    prompt::PermissionTransport::Acp(task_connection),
                ) {
                    Ok(run) => run,
                    Err(error) => return responder.respond_with_error(error),
                };
                connection.spawn(async move {
                    let _guard = guard;
                    let result = run.await;
                    reply(
                        responder,
                        result.map(|output| PromptResponse::new(output.stop_reason)),
                    )
                })
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |cancel: CancelNotification, _connection| {
                cancel_state.operations.cancel(&cancel.session_id);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(transport)
        .await
}

#[cfg(test)]
mod tests {
    use std::{fs, path::Path};

    use agent_client_protocol::schema::v1::{AuthCapabilities, ClientCapabilities, ErrorCode};

    use super::*;
    use crate::tools::fixture::Workspace;

    #[tokio::test]
    async fn manual_compact_command_uses_the_active_prompt_without_saving_a_message() {
        use crate::{
            openrouter::fixture::{Server, text_reply},
            sessions::{AssistantBatch, AssistantMessage, SessionSettingsChange, TranscriptEntry},
        };
        let store = SessionStore::in_memory();
        let state = ServerState::new(store.clone(), None);
        let id = store.create(Path::new("/workspace")).unwrap().id;
        let active = ActiveSession {
            selections: default_settings(),
            system_prompt: "captured system".to_owned(),
            skills: vec![],
        };
        let empty = state
            .compact_session(&id, active.clone(), &PromptCancellation::new())
            .await
            .unwrap();
        assert_eq!(empty.stop_reason, StopReason::EndTurn);
        assert!(
            state.openrouter.lock().unwrap().is_none(),
            "empty command needs no client"
        );

        store
            .append_user(
                &id,
                &SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("previous work ".repeat(3000)),
            )
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
                    },
                    vec![],
                )
                .unwrap(),
            )
            .unwrap();
        let before = store.read(&id).unwrap().unwrap().transcript;
        let server = Server::start(vec![text_reply("Previous work complete.")]).await;
        *state.openrouter.lock().unwrap() = Some(server.client());
        let response = state
            .compact_session(&id, active, &PromptCancellation::new())
            .await
            .unwrap();
        assert_eq!(response.stop_reason, StopReason::EndTurn);
        let after = store.read(&id).unwrap().unwrap().transcript;
        assert_eq!(after.len(), before.len() + 1);
        assert!(matches!(
            after.last(),
            Some(TranscriptEntry::CompactionCheckpoint(_))
        ));
        assert_eq!(
            server.requests()[0]["messages"][0]["content"],
            include_str!("prompts/compaction_prompt.md")
        );
        assert!(after.iter().all(|entry| !matches!(entry,
            TranscriptEntry::UserMessage(text) if text == "/compact")));
    }
    fn state() -> ServerState {
        state_over(SessionStore::in_memory())
    }

    /// A server state over `store`, as a later process would open it.
    fn state_over(store: SessionStore) -> ServerState {
        let state = ServerState::new(store, None);
        *state.openrouter.lock().unwrap() = Some(openrouter::Client::new("test-key".to_owned()));
        state
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
            assert_eq!(dispatch(command.to_owned(), &skills), Dispatch::Compact);
        }
        let arguments = "Fix the \"tests\" in $HOME\n  and more";
        assert_eq!(
            dispatch(format!(" /goal\t{arguments} \n"), &skills),
            Dispatch::Skill {
                invocation: SkillInvocation {
                    name: "goal".to_owned(),
                    arguments: arguments.to_owned(),
                    instructions: "Follow the goal steps.".to_owned(),
                },
                hooks: Box::new(hooks::RunHooks {
                    hooks: skills[0].hooks.clone(),
                    skill: Some("goal".to_owned()),
                    directory: skills[0].directory.clone(),
                }),
            }
        );
        assert_eq!(
            dispatch("/init".to_owned(), &skills),
            Dispatch::Skill {
                invocation: SkillInvocation {
                    name: "init".to_owned(),
                    arguments: String::new(),
                    instructions: "Follow the init steps.".to_owned(),
                },
                hooks: Box::new(hooks::RunHooks {
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
                dispatch(user_message.to_owned(), &skills),
                Dispatch::UserMessage(user_message.to_owned())
            );
        }
    }

    #[test]
    fn activation_validates_the_session_and_captures_the_system_prompt_once() {
        let workspace = Workspace::new();
        let agents_md = workspace.0.join("AGENTS.md");
        fs::write(&agents_md, "Answer in French.\n").unwrap();
        let skills_dir = workspace.0.join(".agents/skills");
        let write_skill = |name: &str, text: &str| {
            fs::create_dir_all(skills_dir.join(name)).unwrap();
            fs::write(skills_dir.join(name).join("SKILL.md"), text).unwrap();
        };
        write_skill(
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
        let repository = skills::load(Path::new(env!("CARGO_MANIFEST_DIR"))).unwrap();
        assert!(repository.iter().any(|skill| skill.name == "init"));
        let mut updates = Vec::new();
        let mut send_update = |update| {
            updates.push(update);
            Ok(())
        };

        let missing = state
            .load_session(
                &LoadSessionRequest::new(SessionId::new("missing"), &workspace.0),
                &mut send_update,
            )
            .unwrap_err();
        assert_eq!(missing.code, ErrorCode::ResourceNotFound);

        let elsewhere = state
            .load_session(
                &LoadSessionRequest::new(id.clone(), Path::new("/elsewhere")),
                &mut send_update,
            )
            .unwrap_err();
        assert_eq!(elsewhere.code, ErrorCode::InvalidParams);

        fs::write(&agents_md, "Answer in German.\n").unwrap();
        fs::remove_dir_all(skills_dir.join("goal")).unwrap();
        state
            .load_session(
                &LoadSessionRequest::new(id.clone(), &workspace.0),
                &mut send_update,
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

        state
            .delete_session(&DeleteSessionRequest::new(id.clone()))
            .unwrap();
        assert!(
            state.active_session(&id).is_none(),
            "delete removes the active session"
        );

        for hooks in ["{}", "{PreToolUse: [{matcher: shell}]}"] {
            write_skill(
                "shared",
                &format!("---\nname: shared\ndescription: Shared.\nhooks: {hooks}\n---\nBody\n"),
            );
            let id = create_session(&state, &workspace.0);
            assert_eq!(
                state.active_session(&id).unwrap().skills[0].hooks,
                hooks::Hooks::default()
            );
            let later = state_over(state.store.clone());
            later
                .load_session(&LoadSessionRequest::new(id.clone(), &workspace.0), |_| {
                    Ok(())
                })
                .unwrap();
            assert_eq!(
                later.active_session(&id).unwrap().skills[0].hooks,
                hooks::Hooks::default()
            );
            state
                .delete_session(&DeleteSessionRequest::new(id))
                .unwrap();
            fs::remove_dir_all(skills_dir.join("shared")).unwrap();
        }

        for (name, text, error) in [
            (
                "compact",
                "---\nname: compact\ndescription: Shadow.\n---\nBody\n",
                "compact is the built-in /compact command",
            ),
            (
                "Goal",
                "---\nname: Goal\ndescription: Goal.\n---\nBody\n",
                "may contain only lowercase letters, digits, and hyphens",
            ),
            (
                "other",
                "---\nname: goal\ndescription: Goal.\n---\nBody\n",
                "does not match its directory",
            ),
            (
                "blank",
                "---\nname: blank\ndescription: \" \"\n---\nBody\n",
                "description is blank",
            ),
            (
                "empty",
                "---\nname: empty\ndescription: Empty.\n---\n\n",
                "instructions are blank",
            ),
            (
                "plain",
                "No frontmatter.\n",
                "does not begin with --- delimited YAML",
            ),
            (
                "hooked",
                "---\nname: hooked\ndescription: Hooked.\nhooks:\n  before_stop:\n    command: 'true'\n    extra: true\n---\nBody\n",
                "unknown field `extra`",
            ),
            (
                "incomplete",
                "---\nname: incomplete\ndescription: Incomplete.\nhooks:\n  before_stop: {}\n---\nBody\n",
                "missing field `command`",
            ),
            (
                "unhooked",
                "---\nname: unhooked\ndescription: Unhooked.\nhooks:\n  after_run:\n    command: \" \"\n---\nBody\n",
                "after_run hook command is blank",
            ),
            (
                "extra",
                "---\nname: extra\ndescription: Extra.\nhooks:\n  before_tool:\n    command: 'true'\n    tools: [shell]\n---\nBody\n",
                "unknown field `tools`",
            ),
            ("missing", "", "No such file or directory"),
        ] {
            if text.is_empty() {
                fs::create_dir_all(skills_dir.join(name)).unwrap();
            } else {
                write_skill(name, text);
            }
            let invalid = state
                .new_session(&NewSessionRequest::new(&workspace.0))
                .unwrap_err();
            let data = invalid.data.unwrap();
            let data = data.as_str().unwrap();
            assert!(
                data.starts_with(&format!(".agents/skills/{name}/SKILL.md: "))
                    && data.contains(error),
                "{name}: {data}"
            );
            fs::remove_dir_all(skills_dir.join(name)).unwrap();
        }

        fs::write(&agents_md, [0xff, 0xfe]).unwrap();
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
    fn load_and_list_use_the_same_exact_workspace_path() {
        let state = state();
        let created = state
            .new_session(&NewSessionRequest::new("/workspace/"))
            .unwrap();
        state
            .store
            .append_user(
                &created.session_id,
                &sessions::SessionSettingsChange {
                    model: Some(openrouter::DEFAULT_MODEL.to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("Hello".to_owned()),
            )
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
        state
            .store
            .append_user(
                &created.session_id,
                &sessions::SessionSettingsChange {
                    model: Some("retired/model".to_owned()),
                    effort: None,
                    mode: None,
                },
                &TranscriptEntry::UserMessage("Hello".to_owned()),
            )
            .unwrap();
        let mut updates = Vec::new();

        let error = state
            .load_session(
                &LoadSessionRequest::new(created.session_id, "/workspace"),
                |update| {
                    updates.push(update);
                    Ok(())
                },
            )
            .unwrap_err();

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
        assert_eq!(new_options["configOptions"][2]["id"], "mode");
        assert_eq!(new_options["configOptions"][2]["category"], "mode");
        assert_eq!(new_options["configOptions"][2]["currentValue"], "ask");
        assert_eq!(
            new_options["configOptions"][2]["options"],
            serde_json::json!([
                {
                    "value": "ask",
                    "name": "Ask",
                    "description": "Ask before running each shell command."
                },
                {
                    "value": "auto",
                    "name": "Auto",
                    "description": "Run shell commands without asking."
                }
            ])
        );
        let chosen = openrouter::MODEL_CATALOG[1].id;
        let response = state
            .set_config_option(&SetSessionConfigOptionRequest::new(
                created.session_id.clone(),
                "model",
                chosen,
            ))
            .unwrap();
        let response = serde_json::to_value(response).unwrap();
        assert_eq!(response["configOptions"][0]["currentValue"], chosen);
        assert_eq!(
            response["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            openrouter::MODEL_CATALOG.len()
        );
        let response = state
            .set_config_option(&SetSessionConfigOptionRequest::new(
                created.session_id.clone(),
                "mode",
                "auto",
            ))
            .unwrap();
        assert_eq!(
            serde_json::to_value(response).unwrap()["configOptions"][2]["currentValue"],
            "auto"
        );
        assert_eq!(
            state
                .set_config_option(&SetSessionConfigOptionRequest::new(
                    created.session_id.clone(),
                    "mode",
                    "unknown",
                ))
                .unwrap_err()
                .code,
            ErrorCode::InvalidParams
        );

        state
            .store
            .append_user(
                &created.session_id,
                &sessions::SessionSettingsChange {
                    model: Some(chosen.to_owned()),
                    effort: None,
                    mode: Some(SessionMode::Auto),
                },
                &TranscriptEntry::UserMessage("Hello".to_owned()),
            )
            .unwrap();
        let error = state
            .set_config_option(&SetSessionConfigOptionRequest::new(
                created.session_id.clone(),
                "model",
                openrouter::DEFAULT_MODEL,
            ))
            .unwrap_err();
        assert_eq!(error.code, ErrorCode::InvalidParams);

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
        assert_eq!(replayed.len(), 1, "setting entries are not replayed");
        assert_eq!(
            loaded["configOptions"][0]["options"]
                .as_array()
                .unwrap()
                .len(),
            1
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
                turn_start: TranscriptEntry::UserMessage(user_message.to_owned()),
                hooks: Vec::new(),
                selected_settings: Some(active.selections),
                system_prompt: active.system_prompt,
            }
        };

        let cancellation = PromptCancellation::new();
        let first = prompt::run(
            state.store.clone(),
            server.client(),
            input("first"),
            cancellation.clone(),
            |_| Ok(()),
            prompt::PermissionTransport::None,
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
                    "effort",
                    "high",
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
        assert_eq!(first_response.unwrap().stop_reason, StopReason::Cancelled);

        let second = prompt::run(
            state.store.clone(),
            server.client(),
            input("second"),
            PromptCancellation::new(),
            |_| Ok(()),
            prompt::PermissionTransport::None,
        )
        .unwrap();
        assert_eq!(second.await.unwrap().stop_reason, StopReason::EndTurn);

        let stored = state.store.read(&id).unwrap().unwrap();
        assert!(matches!(
            &stored.transcript[..6],
            [
                TranscriptEntry::Model(_),
                TranscriptEntry::Effort(EffortLevel::Low),
                TranscriptEntry::UserMessage(first),
                TranscriptEntry::Effort(EffortLevel::High),
                TranscriptEntry::Mode(SessionMode::Auto),
                TranscriptEntry::UserMessage(second),
            ] if first == "first" && second == "second"
        ));
        let requests = server.requests();
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
                "jsonrpc": "2.0", "id": 2, "method": "session/prompt",
                "params": { "sessionId": inactive, "prompt": [{ "type": "text", "text": "Hello" }] },
            }),
            json!({
                "jsonrpc": "2.0", "id": 3, "method": "session/prompt",
                "params": { "sessionId": id, "prompt": [{ "type": "text", "text": "Hello" }] },
            }),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }

        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut rejected = None;
            let mut response = None;
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                if message["params"]["update"]["sessionUpdate"] == "agent_message_chunk" {
                    assert!(operations.try_load(&id).is_none());
                    drop(incoming_tx.take());
                }
                if message["id"] == 2 {
                    rejected = Some(message);
                } else if message["id"] == 3 {
                    response = Some(message);
                }
            }
            assert!(incoming_tx.is_none(), "EOF was sent during inference");
            let rejected = rejected.expect("the inactive session was answered");
            assert_eq!(rejected["error"]["code"], -32600);
            assert_eq!(
                rejected["error"]["data"],
                format!("session {inactive} is not active; create or load it first")
            );
            let response = response.expect("the final response was drained before shutdown");
            assert_eq!(response["result"]["stopReason"], "cancelled");
        };
        let (result, ()) = futures::join!(serve(state, transport), client);
        result.unwrap();
        assert!(
            operations.try_load(&id).is_some(),
            "the session became available"
        );
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
            vec![
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                TranscriptEntry::UserMessage("Hello".to_owned())
            ],
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

    #[tokio::test]
    async fn shell_permissions_control_execution_and_save_results() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        use crate::openrouter::fixture::{Server, shell_reply, text_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;

        for decision in [
            "approve",
            "auto",
            "deny",
            "mixed",
            "hook",
            "cancelled",
            "cancel",
            "eof",
            "unknown",
            "error",
        ] {
            let workspace = Workspace::new();
            let continues = matches!(decision, "approve" | "auto" | "deny" | "mixed" | "hook");
            let mut replies = vec![shell_reply(&[("touch first", 5), ("touch second", 5)])];
            if continues {
                replies.push(text_reply("Done"));
            }
            let server = Server::start(replies).await;
            let mut state = state();
            *state.openrouter.lock().unwrap() = Some(server.client());
            let store = state.store.clone();
            let operations = state.operations.clone();
            let mut text = "Run commands";
            if decision == "hook" {
                let skill = workspace.0.join(".agents/skills/check");
                fs::create_dir_all(&skill).unwrap();
                fs::write(
                    skill.join("SKILL.md"),
                    r#"---
name: check
description: Check each shell call.
hooks:
  before_tool:
    command: "case \"$(cat)\" in *'touch first'*) echo '{\"decision\":\"deny\",\"message\":\"Leave first alone.\"}';; *) echo '{\"decision\":\"allow\"}';; esac"
---
Run the commands.
"#,
                )
                .unwrap();
                state.global_hooks = Some(hooks::RunHooks {
                    skill: None,
                    directory: workspace.0.clone(),
                    hooks: skills::load(&workspace.0).unwrap().remove(0).hooks,
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
            let (incoming_tx, incoming_rx) = mpsc::unbounded();
            let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
            let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
            for message in [
                json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
                json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":text}]}}),
            ] {
                incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
            }
            let client = async {
                let mut incoming_tx = Some(incoming_tx);
                let mut requested = 0;
                let mut announced = Vec::new();
                let mut auto_updates = Vec::new();
                let mut response = None;
                while let Some(line) = outgoing_rx.next().await {
                    let message: Value = serde_json::from_str(&line).unwrap();
                    let update = &message["params"]["update"];
                    if decision == "approve" && update["sessionUpdate"] == "tool_call" {
                        assert!(
                            update["status"].is_null() || update["status"] == "pending",
                            "{decision}"
                        );
                        announced.push(update["toolCallId"].clone());
                    }
                    if decision == "auto" {
                        match update["sessionUpdate"].as_str() {
                            Some("tool_call") => auto_updates.push(format!(
                                "{} pending",
                                update["toolCallId"].as_str().unwrap()
                            )),
                            Some("tool_call_update") => auto_updates.push(format!(
                                "{} {}",
                                update["toolCallId"].as_str().unwrap(),
                                update["status"].as_str().unwrap()
                            )),
                            _ => {}
                        }
                    }
                    if message["method"] == "session/request_permission" {
                        let params = &message["params"];
                        let file = if requested == 0 { "first" } else { "second" };
                        if decision == "hook" {
                            assert_eq!(params["toolCall"]["toolCallId"], "shell-1");
                        }
                        if decision == "approve" {
                            assert_eq!(params["sessionId"], id.to_string());
                            assert_eq!(
                                params["toolCall"]["toolCallId"],
                                format!("shell-{requested}")
                            );
                            assert!(announced.contains(&params["toolCall"]["toolCallId"]));
                            assert_eq!(params["toolCall"]["status"], "pending");
                            assert_eq!(params["toolCall"]["kind"], "execute");
                            assert_eq!(
                                params["toolCall"]["rawInput"]["command"],
                                format!("touch {file}")
                            );
                            assert_eq!(params["toolCall"]["title"], format!("touch {file}"));
                            assert_eq!(
                                params["toolCall"]["content"][0]["content"]["text"],
                                format!("Working directory: {}", workspace.0.display())
                            );
                            assert!(!workspace.0.join(file).exists());
                            assert_eq!(
                                params["options"],
                                json!([
                                    {"optionId":"approve","name":"Approve","kind":"allow_once"},
                                    {"optionId":"deny","name":"Deny","kind":"reject_once"},
                                ])
                            );
                            assert!(operations.try_load(&id).is_none());
                            assert!(operations.try_delete(&id).is_none());
                            assert!(operations.try_prompt(&id).is_none());
                        }
                        requested += 1;
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
                                        "mixed" if requested == 1 => "deny",
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
                        if decision == "approve" {
                            assert_eq!(
                                store
                                    .read(&id)
                                    .unwrap()
                                    .unwrap()
                                    .transcript
                                    .iter()
                                    .filter(|entry| {
                                        matches!(entry, TranscriptEntry::ToolResult(_))
                                    })
                                    .count(),
                                2,
                                "the batch is saved before responding"
                            );
                        }
                        response = Some(message);
                        drop(incoming_tx.take());
                    }
                }
                if matches!(decision, "approve" | "auto" | "hook") {
                    let expected = match decision {
                        "approve" => 2,
                        "hook" => 1,
                        _ => 0,
                    };
                    assert_eq!(requested, expected);
                }
                if decision == "auto" {
                    assert_eq!(
                        auto_updates,
                        [
                            "shell-0 pending",
                            "shell-1 pending",
                            "shell-0 in_progress",
                            "shell-0 completed",
                            "shell-1 in_progress",
                            "shell-1 completed",
                        ]
                    );
                }
                let response =
                    response.unwrap_or_else(|| panic!("{decision}: missing prompt response"));
                if matches!(decision, "unknown" | "error") {
                    assert_eq!(response["error"]["code"], -32603, "{decision}");
                    if decision == "unknown" {
                        assert_eq!(
                            response["error"]["data"], "Unknown shell permission option: unknown",
                            "{decision}"
                        );
                    }
                } else {
                    assert_eq!(
                        response["result"]["stopReason"],
                        if continues { "end_turn" } else { "cancelled" },
                        "{decision}"
                    );
                }
            };
            let (result, ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
                tokio::join!(serve(state, transport), client)
            })
            .await
            .unwrap_or_else(|error| panic!("{decision}: timed out: {error}"));
            result.unwrap_or_else(|error| panic!("{decision}: ACP connection failed: {error}"));
            if decision == "approve" {
                assert!(operations.try_load(&id).is_some());
            }
            assert_eq!(
                workspace.0.join("first").exists(),
                matches!(decision, "approve" | "auto"),
                "{decision}"
            );
            assert_eq!(
                workspace.0.join("second").exists(),
                matches!(decision, "approve" | "auto" | "mixed" | "hook"),
                "{decision}"
            );
            let transcript = store.read(&id).unwrap().unwrap().transcript;
            let results: Vec<_> = transcript
                .iter()
                .filter_map(|entry| match entry {
                    TranscriptEntry::ToolResult(result) => Some(result),
                    _ => None,
                })
                .collect();
            assert_eq!(results.len(), 2, "{decision}");
            if decision == "auto" {
                assert!(matches!(
                    &transcript[..3],
                    [
                        TranscriptEntry::Model(_),
                        TranscriptEntry::Mode(SessionMode::Auto),
                        TranscriptEntry::UserMessage(_),
                    ]
                ));
            }
            for (index, result) in results.iter().enumerate() {
                match decision {
                    "approve" | "auto" => assert!(
                        matches!(result.outcome, ToolOutcome::Completed(_)),
                        "{decision}, result {index}"
                    ),
                    "hook" if index == 0 => {
                        let denied = "global before_tool hook denied this call: Leave first alone.\nskill /check before_tool hook denied this call: Leave first alone.";
                        assert_eq!(result.outcome, ToolOutcome::Failed(denied.to_owned()));
                        assert_eq!(server.requests()[1]["messages"][3]["content"], denied);
                    }
                    "deny" | "mixed" if decision == "deny" || index == 0 => assert_eq!(
                        result.outcome,
                        ToolOutcome::Failed(
                            "User denied permission to run this command.".to_owned()
                        ),
                        "{decision}, result {index}"
                    ),
                    "mixed" | "hook" => assert!(
                        matches!(result.outcome, ToolOutcome::Completed(_)),
                        "{decision}, result {index}"
                    ),
                    "unknown" | "error" => {
                        assert!(
                            matches!(result.outcome, ToolOutcome::Failed(_)),
                            "{decision}, result {index}"
                        )
                    }
                    _ => assert!(
                        matches!(result.outcome, ToolOutcome::Cancelled(_)),
                        "{decision}, result {index}"
                    ),
                }
            }
        }
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
        use crate::openrouter::fixture::{Server, shell_reply, text_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;
        use rustix::process::{Pid, Signal, kill_process, kill_process_group};

        const FLAG: &str = "OX_HEADLESS_SIGNAL_TEST";
        if let Some(path) = std::env::var_os(FLAG) {
            let path = Path::new(&path);
            let store = SessionStore::open(&path.join("ox.db")).unwrap();
            let command = "echo $$ > shell; python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; sleep 30 & echo $! > child; printf started; touch ready; wait";
            let server = Server::start(vec![
                text_reply("Finished answer."),
                shell_reply(&[(command, 30), ("touch wrong", 5)]),
            ])
            .await;
            let answered = store.create(path).unwrap();
            let answer = run_headless_prompt(
                store.clone(),
                server.client(),
                answered.id,
                SessionSettings::new(openrouter::DEFAULT_MODEL, EffortLevel::Default),
                system_prompt::for_workspace(path).unwrap(),
                "Answer".into(),
                settings::load().unwrap().into_iter().collect(),
            )
            .await
            .unwrap();
            assert_eq!(answer, "Finished answer.", "ox run prints this answer");
            assert_eq!(
                fs::read_to_string(path.join(".config/ox/reported")).unwrap(),
                "1"
            );
            let session = store.create(path).unwrap();
            let response = run_headless_prompt(
                store.clone(),
                server.client(),
                session.id.clone(),
                SessionSettings::new(openrouter::MODEL_CATALOG[1].id, EffortLevel::High),
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
                &transcript[..4],
                [
                    TranscriptEntry::Model(openrouter::MODEL_CATALOG[1].id.to_owned()),
                    TranscriptEntry::Effort(EffortLevel::High),
                    TranscriptEntry::Mode(SessionMode::Auto),
                    TranscriptEntry::UserMessage("Run commands".to_owned()),
                ]
            );
            assert_eq!(transcript.iter().filter(|entry| matches!(entry, TranscriptEntry::ToolResult(result) if matches!(result.outcome, ToolOutcome::Cancelled(_)))).count(), 2);
            std::fs::write(path.join("saved"), "yes").unwrap();
            return;
        }
        let workspace = Workspace::new();
        fs::create_dir_all(workspace.0.join(".config/ox")).unwrap();
        fs::write(workspace.0.join(".config/ox/settings.json"), r#"{"hooks":{"after_run":{"command":"printf %s \"$OX_IN_HOOK\" > reported; echo '{}'"}}}"#).unwrap();
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
        kill_process(pid, Signal::TERM).unwrap();
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        kill_process(pid, Signal::INT).unwrap();
        let status = tokio::time::timeout(std::time::Duration::from_secs(5), child.wait()).await;
        let detached = std::fs::read_to_string(workspace.0.join("detached"))
            .unwrap()
            .parse()
            .unwrap();
        kill_process_group(Pid::from_raw(detached).unwrap(), Signal::KILL).unwrap();
        assert!(status.unwrap().unwrap().success());
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert!(workspace.0.join("saved").exists());
        assert!(!workspace.0.join("wrong").exists());
        let store = SessionStore::open(&workspace.0.join("ox.db")).unwrap();
        assert!(store.list(None).unwrap().iter().any(|session| store.read(&session.id).unwrap().unwrap().transcript.iter().any(|entry| matches!(entry,
            TranscriptEntry::ToolResult(result) if matches!(&result.outcome, ToolOutcome::Cancelled(text) if text.contains("started") && text.contains("partial changes"))))));
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
                            .any(|entry| matches!(entry, TranscriptEntry::ToolResult(_))),
                        "saved before response"
                    );
                    response = Some(message);
                }
            }
            assert_eq!(response.unwrap()["result"]["stopReason"], "cancelled");
        };
        let (result, ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(serve(state, transport), client)
        })
        .await
        .unwrap();
        result.unwrap();
        assert!(operations.try_load(&id).is_some());
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert!(!workspace.0.join("wrong").exists());
        let transcript = store.read(&id).unwrap().unwrap().transcript;
        assert_eq!(transcript.iter().filter(|entry| matches!(entry, TranscriptEntry::ToolResult(result) if matches!(result.outcome, ToolOutcome::Cancelled(_)))).count(), 2);
    }

    #[tokio::test]
    async fn transport_error_stops_the_shell_process_group() {
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
            tokio::join!(serve(state, transport), fail_transport, drain_output)
        })
        .await
        .unwrap();
        assert!(
            result.is_err(),
            "the transport error reaches the ACP server"
        );
        assert_process_stopped(&workspace.0.join("shell"), false).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
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
