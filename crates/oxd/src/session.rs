use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, MutexGuard};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::schema::{
    CancelNotification, ContentBlock, InitializeRequest, LoadSessionRequest, NewSessionRequest,
    PromptRequest, RequestPermissionOutcome, RequestPermissionRequest, RequestPermissionResponse,
    SessionConfigOption, SessionConfigOptionValue, SessionId, SessionNotification,
    SetSessionConfigOptionRequest,
};
use agent_client_protocol::v1::{ConnectionTo, Responder};
use agent_client_protocol::{AcpAgent, AcpAgentConfig, Agent, Client};
use ox_protocol::{
    AgentStateNotification, ConfigValue, ErrorBody, Event, SessionInfo, SessionState, code,
};
use tokio::sync::{mpsc, oneshot, watch};
use tokio::task::JoinHandle;

use crate::state::{self, Change, Request, State, invalid_state};

type ConfigReply = oneshot::Sender<Result<Vec<SessionConfigOption>, String>>;

enum Command {
    Prompt(Vec<ContentBlock>),
    Cancel,
    SetConfigOption {
        config_id: String,
        value: SessionConfigOptionValue,
        reply: ConfigReply,
    },
}

/// One ACP agent process and its event history.
pub struct Session {
    pub id: String,
    pub number: u64,
    command: Vec<String>,
    cwd: PathBuf,
    state: Mutex<State>,
    /// The seq of the session's last event. Updated under the state lock.
    last_seq: watch::Sender<u64>,
    /// The daemon's signal that the sessions list changed, sent when this
    /// session's `SessionInfo` changes.
    sessions_changed: watch::Sender<()>,
    commands: mpsc::UnboundedSender<Command>,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl Session {
    /// Spawns the agent on a new ACP session, or on the saved session `load`.
    /// The session records `AgentStarted` once the ACP session is open, or
    /// `AgentExited` if it cannot be opened.
    pub fn spawn(
        number: u64,
        command: Vec<String>,
        cwd: PathBuf,
        env: BTreeMap<String, String>,
        load: Option<String>,
        sessions_changed: watch::Sender<()>,
    ) -> Arc<Self> {
        let (commands, commands_rx) = mpsc::unbounded_channel();
        let id = uuid::Uuid::new_v4().to_string();
        let session = Arc::new(Self {
            state: Mutex::new(State::new(id.clone())),
            id,
            number,
            command,
            cwd,
            last_seq: watch::Sender::new(0),
            sessions_changed,
            commands,
            task: Mutex::new(None),
        });
        let task = tokio::spawn(session.clone().run(commands_rx, env, load));
        *session.task.lock().unwrap() = Some(task);
        session
    }

    pub fn info(&self) -> SessionInfo {
        self.info_of(&self.lock())
    }

    fn info_of(&self, state: &State) -> SessionInfo {
        let agent = state.agent();
        SessionInfo {
            id: self.id.clone(),
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
            let _ = self.commands.send(Command::Prompt(prompt));
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
            ConfigValue::Id(id) => SessionConfigOptionValue::value_id(id),
        };
        let (reply, result) = oneshot::channel();
        let _ = self.commands.send(Command::SetConfigOption {
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
        let _ = self.commands.send(Command::Cancel);
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

    /// Changes the state, publishes the seq of its last event, and signals
    /// the sessions list if the session's `SessionInfo` changed.
    fn update<R>(&self, change: impl FnOnce(&mut State) -> R) -> R {
        let mut state = self.lock();
        let before = self.info_of(&state);
        let result = change(&mut state);
        self.last_seq.send_replace(state.last_seq());
        if self.info_of(&state) != before {
            self.sessions_changed.send_replace(());
        }
        result
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
        let result = Client
            .v1()
            .name("oxd")
            .on_receive_notification(
                {
                    let session = self.clone();
                    async move |notification: SessionNotification,
                                _connection: ConnectionTo<Agent>| {
                        session.record(Change::Update(notification.update));
                        Ok(())
                    }
                },
                agent_client_protocol::on_receive_notification!(),
            )
            .on_receive_notification(
                {
                    let session = self.clone();
                    async move |notification: AgentStateNotification,
                                _connection: ConnectionTo<Agent>| {
                        session.record(Change::AgentState(notification.state));
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
                            let outcome =
                                outcome.await.unwrap_or(RequestPermissionOutcome::Cancelled);
                            responder.respond(RequestPermissionResponse::new(outcome))
                        })
                    }
                },
                agent_client_protocol::on_receive_request!(),
            )
            .connect_with(
                AcpAgent::new(config),
                async move |connection: ConnectionTo<Agent>| {
                    let initialized = connection
                        .send_request(InitializeRequest::new(ProtocolVersion::V1))
                        .block_task()
                        .await?;
                    // A loading agent replays the conversation as
                    // notifications before it answers.
                    let (acp_session, config_options) = match load {
                        Some(id) => {
                            if !initialized.agent_capabilities.load_session {
                                return Err(agent_client_protocol::Error::invalid_request()
                                    .data("the agent cannot load sessions"));
                            }
                            let loaded = connection
                                .send_request(LoadSessionRequest::new(
                                    id.clone(),
                                    session.cwd.clone(),
                                ))
                                .block_task()
                                .await?;
                            (SessionId::new(id), loaded.config_options)
                        }
                        None => {
                            let created = connection
                                .send_request(NewSessionRequest::new(session.cwd.clone()))
                                .block_task()
                                .await?;
                            (created.session_id, created.config_options)
                        }
                    };
                    session.record(Change::Started(state::Agent {
                        session_id: acp_session.to_string(),
                        prompt_capabilities: initialized.agent_capabilities.prompt_capabilities,
                        config_options: config_options.unwrap_or_default(),
                    }));
                    loop {
                        let command = tokio::select! {
                            () = connection.incoming_closed() => break,
                            command = commands.recv() => command,
                        };
                        match command {
                            None => break,
                            Some(Command::Prompt(prompt)) => {
                                let session = session.clone();
                                connection
                                    .send_request(PromptRequest::new(acp_session.clone(), prompt))
                                    .on_receiving_result(async move |result| {
                                        session.record(Change::TurnEnded(
                                            result
                                                .map(|response| response.stop_reason)
                                                .map_err(|error| error.to_string()),
                                        ));
                                        Ok(())
                                    })?;
                            }
                            Some(Command::Cancel) => {
                                connection.send_notification(CancelNotification::new(
                                    acp_session.clone(),
                                ))?;
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
                                            session
                                                .record(Change::ConfigOptionsSet(options.clone()));
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
