use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, MutexGuard};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v2::schema::{
    CancelSessionNotification, CancelledPermissionOutcome, ContentBlock, Implementation,
    InitializeRequest, NewSessionRequest, PromptRequest, ReplayFrom, ReplayFromStart,
    RequestPermissionRequest, RequestPermissionResponse, ResumeSessionRequest, SessionConfigOption,
    SessionConfigOptionValue, SessionId, SetSessionConfigOptionRequest, UpdateSessionNotification,
};
use agent_client_protocol::v2::{ConnectionTo, InitializeOutcome, Responder};
use agent_client_protocol::{AcpAgent, AcpAgentConfig, Agent, Client};
use ox_protocol::{ConfigValue, ErrorBody, Event, SessionInfo, SessionState, code};
use tokio::sync::{mpsc, oneshot, watch};
use tokio::task::JoinHandle;

use crate::state::{self, Change, Request, State, invalid_state};
use crate::store::{SavedSession, Store};

type ConfigReply = oneshot::Sender<Result<Vec<SessionConfigOption>, String>>;

enum Command {
    /// A prompt, with the seq of the `PromptStarted` event that records it.
    Prompt {
        prompt: Vec<ContentBlock>,
        seq: u64,
    },
    Cancel,
    SetConfigOption {
        config_id: String,
        value: SessionConfigOptionValue,
        reply: ConfigReply,
    },
}

/// One ACP agent process and its event history. The database keeps the
/// session's workspace, command, directory, title, and agent session ID.
pub struct Session {
    pub id: String,
    pub number: u64,
    workspace_id: String,
    command: Vec<String>,
    cwd: PathBuf,
    store: Arc<Store>,
    state: Mutex<State>,
    /// The seq of the session's last event. Updated under the state lock.
    last_seq: watch::Sender<u64>,
    /// The daemon's signal that the lists or the sessions' states changed,
    /// sent when this session's `SessionInfo` changes.
    changed: watch::Sender<()>,
    /// Sends to the running agent task. A load replaces it.
    commands: Mutex<mpsc::UnboundedSender<Command>>,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl Session {
    fn new(
        store: Arc<Store>,
        changed: watch::Sender<()>,
        saved: SavedSession,
        state: State,
    ) -> Arc<Self> {
        Arc::new(Self {
            state: Mutex::new(state),
            id: saved.id,
            number: saved.number,
            workspace_id: saved.workspace_id,
            command: saved.command,
            cwd: saved.cwd,
            store,
            last_seq: watch::Sender::new(0),
            changed,
            commands: Mutex::new(mpsc::unbounded_channel().0),
            task: Mutex::new(None),
        })
    }

    /// Spawns the agent of a session just saved. The session records
    /// `AgentStarted` once the ACP session is open, or `AgentExited` if it
    /// cannot be opened.
    pub fn spawn(
        store: Arc<Store>,
        changed: watch::Sender<()>,
        saved: SavedSession,
        env: BTreeMap<String, String>,
    ) -> Arc<Self> {
        let state = State::new(saved.id.clone());
        let session = Self::new(store, changed, saved, state);
        session.launch(&mut session.task.lock().unwrap(), env, None);
        session
    }

    /// A saved session whose agent is not running: it is `exited` and has no
    /// events until it loads.
    pub fn restore(
        store: Arc<Store>,
        changed: watch::Sender<()>,
        saved: SavedSession,
    ) -> Option<Arc<Self>> {
        let agent_session_id = saved.agent_session_id.clone()?;
        let state = State::restored(saved.id.clone(), saved.title.clone(), agent_session_id);
        Some(Self::new(store, changed, saved, state))
    }

    /// Starts the agent task in `task`, which opens a new ACP session or
    /// resumes the agent session `load`.
    fn launch(
        self: &Arc<Self>,
        task: &mut Option<JoinHandle<()>>,
        env: BTreeMap<String, String>,
        load: Option<String>,
    ) {
        let (commands, commands_rx) = mpsc::unbounded_channel();
        *self.commands.lock().unwrap() = commands;
        *task = Some(tokio::spawn(self.clone().run(commands_rx, env, load)));
    }

    /// Starts a new agent on an exited session that has an agent session ID,
    /// which resumes the session and replays its conversation. The session
    /// records `AgentStarted` once the agent has resumed it, or `AgentExited`
    /// if it cannot.
    pub fn load(self: &Arc<Self>, env: BTreeMap<String, String>) -> Result<(), ErrorBody> {
        // Holding the task slot keeps a concurrent `close` from missing the
        // new task.
        let mut task = self.task.lock().unwrap();
        let agent_session_id = self.update(State::load)?;
        self.launch(&mut task, env, Some(agent_session_id));
        Ok(())
    }

    pub fn info(&self) -> SessionInfo {
        self.info_of(&self.lock())
    }

    pub fn state(&self) -> SessionState {
        self.lock().state()
    }

    pub fn workspace_id(&self) -> &str {
        &self.workspace_id
    }

    fn info_of(&self, state: &State) -> SessionInfo {
        let agent = state.agent();
        SessionInfo {
            id: self.id.clone(),
            workspace_id: self.workspace_id.clone(),
            command: self.command.clone(),
            cwd: self.cwd.clone(),
            state: state.state(),
            title: state.title().map(String::from),
            agent_session_id: agent.map(|agent| agent.session_id.clone()),
            prompt_capabilities: agent
                .map(|agent| agent.prompt_capabilities.clone())
                .unwrap_or_default(),
            config_options: agent
                .map(|agent| agent.config_options.clone())
                .unwrap_or_default(),
        }
    }

    /// Checks that events follow `after_seq`, now or later.
    pub fn subscribe(&self, after_seq: u64) -> Result<(), ErrorBody> {
        self.lock().subscribe(after_seq)
    }

    /// Waits for events with `seq > after_seq` and returns the first of
    /// them, in order.
    pub async fn events_after(&self, after_seq: u64) -> Vec<Event> {
        self.last_seq
            .subscribe()
            .wait_for(|last_seq| *last_seq > after_seq)
            .await
            .expect("the session holds the sender");
        self.lock().events_after(after_seq)
    }

    pub fn prompt(&self, prompt: Vec<ContentBlock>) -> Result<(), ErrorBody> {
        self.update(|state| {
            state.request(Request::Prompt(prompt.clone()))?;
            self.send(Command::Prompt {
                prompt,
                seq: state.last_seq(),
            });
            Ok(())
        })
    }

    /// Asks the agent to change a config option and returns its resulting
    /// options.
    pub async fn set_config_option(
        &self,
        config_id: String,
        value: ConfigValue,
    ) -> Result<Vec<SessionConfigOption>, ErrorBody> {
        let state = self.lock().state();
        if matches!(state, SessionState::Starting | SessionState::Exited) {
            return Err(invalid_state(state));
        }
        let value = match value {
            ConfigValue::Boolean(value) => SessionConfigOptionValue::boolean(value),
            ConfigValue::Id(id) => SessionConfigOptionValue::id(id),
        };
        let (reply, result) = oneshot::channel();
        self.send(Command::SetConfigOption {
            config_id,
            value,
            reply,
        });
        result
            .await
            .unwrap_or_else(|_| Err("the session ended".into()))
            .map_err(|message| ErrorBody {
                code: code::AGENT,
                message,
            })
    }

    /// Cancels the running turn and every pending permission request.
    pub fn cancel(&self) {
        self.send(Command::Cancel);
    }

    pub fn permit(&self, request_id: &str, option_id: &str) -> Result<(), ErrorBody> {
        self.update(|state| {
            state.request(Request::Permit {
                request_id: request_id.into(),
                option_id: option_id.into(),
            })
        })
    }

    /// Stops the agent, killing its process group, and ends every
    /// subscription with `SessionClosed`.
    pub fn close(&self) {
        self.record(Change::Closed);
        let task = self.task.lock().unwrap().take();
        if let Some(task) = task {
            task.abort();
        }
    }

    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap()
    }

    /// Sends a command to the running agent task. A session whose agent has
    /// exited has none, and its commands are dropped.
    fn send(&self, command: Command) {
        let _ = self.commands.lock().unwrap().send(command);
    }

    /// Changes the state, publishes the seq of its last event, saves a
    /// changed title or agent session ID, and signals the daemon if the
    /// session's `SessionInfo` changed.
    fn update<R>(&self, change: impl FnOnce(&mut State) -> R) -> R {
        let mut state = self.lock();
        let before = self.info_of(&state);
        let result = change(&mut state);
        self.last_seq.send_replace(state.last_seq());
        let after = self.info_of(&state);
        if after != before {
            self.save(&before, &after);
            self.changed.send_replace(());
        }
        result
    }

    /// Writes what differs between two infos to the session's row. A failed
    /// write goes to oxd's log and the session carries on.
    fn save(&self, before: &SessionInfo, after: &SessionInfo) {
        if after.title != before.title
            && let Err(error) = self.store.set_title(&self.id, after.title.as_deref())
        {
            eprintln!("oxd: saving the title of session {}: {error}", self.id);
        }
        if after.agent_session_id != before.agent_session_id
            && let Some(agent_session_id) = &after.agent_session_id
            && let Err(error) = self.store.set_agent_session_id(&self.id, agent_session_id)
        {
            eprintln!(
                "oxd: saving the agent session of session {}: {error}",
                self.id
            );
        }
    }

    fn record(&self, change: Change) {
        self.update(|state| state.record(change));
    }

    async fn run(
        self: Arc<Self>,
        mut commands: mpsc::UnboundedReceiver<Command>,
        env: BTreeMap<String, String>,
        load: Option<String>,
    ) {
        let config = AcpAgentConfig::new(&self.command[0])
            .args(self.command[1..].iter().cloned())
            .envs(env);
        let session = self.clone();
        let result =
            Client
                .v2()
                .name("oxd")
                .on_receive_notification(
                    {
                        let session = self.clone();
                        async move |notification: UpdateSessionNotification,
                                    _connection: ConnectionTo<Agent>| {
                            session.record(Change::Update(notification.update));
                            Ok(())
                        }
                    },
                    agent_client_protocol::on_receive_notification!(),
                )
                .on_receive_request(
                    {
                        let session = self.clone();
                        async move |request: RequestPermissionRequest,
                                    responder: Responder<RequestPermissionResponse>,
                                    connection: ConnectionTo<Agent>| {
                            let (answer, outcome) = oneshot::channel();
                            session.record(Change::PermissionRequested { request, answer });
                            connection.spawn(async move {
                                let outcome = outcome
                                    .await
                                    .unwrap_or_else(|_| CancelledPermissionOutcome::new().into());
                                responder.respond(RequestPermissionResponse::new(outcome))
                            })
                        }
                    },
                    agent_client_protocol::on_receive_request!(),
                )
                .connect_with(
                    AcpAgent::new(config),
                    async move |connection: ConnectionTo<Agent>| {
                        let InitializeOutcome::Initialized(initialized) = connection
                            .send_initialize(InitializeRequest::new(
                                ProtocolVersion::V2,
                                Implementation::new("oxd", env!("CARGO_PKG_VERSION")),
                            ))
                            .block_task()
                            .await?
                        else {
                            return Err(agent_client_protocol::Error::invalid_request()
                                .data("the agent does not speak ACP v2"));
                        };
                        let Some(capabilities) = initialized.capabilities.session else {
                            return Err(agent_client_protocol::Error::invalid_request()
                                .data("the agent does not support sessions"));
                        };
                        // A resuming agent replays the conversation and reports
                        // its state as notifications before it answers. The
                        // session starts in an ordered callback on the answer,
                        // which the dispatch loop finishes before it reads what
                        // the agent sends next (the SDK's `concepts::ordering`
                        // chapter), so every later update finds it live.
                        let prompt_capabilities = capabilities.prompt.unwrap_or_default();
                        let (opened, open) = oneshot::channel();
                        let start = {
                            let session = session.clone();
                            move |result: Result<(SessionId, Vec<SessionConfigOption>), _>| {
                                let result = result.map(|(acp_session, config_options)| {
                                    session.record(Change::Started(state::Agent {
                                        session_id: acp_session.to_string(),
                                        prompt_capabilities,
                                        config_options,
                                    }));
                                    acp_session
                                });
                                let _ = opened.send(result);
                            }
                        };
                        match load {
                            Some(id) => connection
                                .prepare_request(
                                    ResumeSessionRequest::new(id.clone(), session.cwd.clone())
                                        .replay_from(ReplayFrom::Start(ReplayFromStart::new())),
                                )
                                .on_receiving_result(async move |result| {
                                    start(result.map(|resumed| {
                                        (SessionId::new(id), resumed.config_options)
                                    }));
                                    Ok(())
                                })?,
                            None => connection
                                .prepare_request(NewSessionRequest::new(session.cwd.clone()))
                                .on_receiving_result(async move |result| {
                                    start(result.map(|created| {
                                        (created.session_id, created.config_options)
                                    }));
                                    Ok(())
                                })?,
                        }
                        let acp_session = open.await.map_err(|_| {
                            agent_client_protocol::Error::internal_error()
                                .data("the agent closed the connection before the session opened")
                        })??;
                        loop {
                            let command = tokio::select! {
                                () = connection.incoming_closed() => break,
                                command = commands.recv() => command,
                            };
                            match command {
                                None => break,
                                Some(Command::Prompt { prompt, seq }) => {
                                    let session = session.clone();
                                    connection
                                        .prepare_request(PromptRequest::new(
                                            acp_session.clone(),
                                            prompt,
                                        ))
                                        .on_receiving_result(async move |result| {
                                            // The turn runs and ends on the
                                            // agent's `state_update`s. An error
                                            // answer means the prompt was not
                                            // inserted.
                                            if let Err(error) = result {
                                                session.record(Change::PromptRejected {
                                                    prompt: seq,
                                                    error: error.to_string(),
                                                });
                                            }
                                            Ok(())
                                        })?;
                                }
                                Some(Command::Cancel) => {
                                    connection.send_notification(
                                        CancelSessionNotification::new(acp_session.clone()),
                                    )?;
                                    session.record(Change::Cancelled);
                                }
                                Some(Command::SetConfigOption {
                                    config_id,
                                    value,
                                    reply,
                                }) => {
                                    let session = session.clone();
                                    connection
                                        .send_request(SetSessionConfigOptionRequest::new(
                                            acp_session.clone(),
                                            config_id,
                                            value,
                                        ))
                                        .on_receiving_result(async move |result| {
                                            let result = result
                                                .map(|response| response.config_options)
                                                .map_err(|error| error.to_string());
                                            if let Ok(options) = &result {
                                                session.record(Change::ConfigOptionsSet(
                                                    options.clone(),
                                                ));
                                            }
                                            let _ = reply.send(result);
                                            Ok(())
                                        })?;
                                }
                            }
                        }
                        Ok(())
                    },
                )
                .await;
        self.record(Change::Exited(result.err().map(|error| error.to_string())));
    }
}
