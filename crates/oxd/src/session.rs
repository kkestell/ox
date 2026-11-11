use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::{Arc, Mutex, MutexGuard};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::schema::{
    CancelNotification, ContentBlock, InitializeRequest, NewSessionRequest, PermissionOption,
    PromptRequest, PromptResponse, RequestPermissionOutcome, RequestPermissionRequest,
    RequestPermissionResponse, SelectedPermissionOutcome, SessionNotification, TextContent,
};
use agent_client_protocol::v1::{ConnectionTo, Responder};
use agent_client_protocol::{AcpAgent, AcpAgentConfig, Agent, Client};
use ox_protocol::{ErrorBody, Event, EventKind, SessionInfo, SessionState, code};
use tokio::sync::{broadcast, mpsc, oneshot};
use tokio::task::JoinHandle;

/// Live events buffered per subscriber before it counts as lagging.
const BROADCAST_CAPACITY: usize = 1024;

enum Command {
    Prompt(String),
    Cancel,
}

struct Pending {
    options: Vec<PermissionOption>,
    sender: oneshot::Sender<RequestPermissionOutcome>,
}

struct Inner {
    state: SessionState,
    events: Vec<Event>,
    sender: broadcast::Sender<Event>,
    pending: HashMap<String, Pending>,
    next_request: u64,
    turn_active: bool,
    ready: Option<oneshot::Sender<Result<(), String>>>,
}

impl Inner {
    /// Derives the state of a live session from its turn and pending
    /// permission requests.
    fn settle(&mut self) {
        if matches!(self.state, SessionState::Starting | SessionState::Exited) {
            return;
        }
        self.state = if !self.pending.is_empty() {
            SessionState::AwaitingPermission
        } else if self.turn_active {
            SessionState::Running
        } else {
            SessionState::Idle
        };
    }
}

/// One ACP agent process and its event history.
pub struct Session {
    pub id: String,
    pub number: u64,
    command: Vec<String>,
    cwd: PathBuf,
    inner: Mutex<Inner>,
    commands: mpsc::UnboundedSender<Command>,
    task: Mutex<Option<JoinHandle<()>>>,
}

impl Session {
    /// Spawns the agent. The receiver resolves once the ACP session exists,
    /// or with the reason it could not be created.
    pub fn spawn(
        number: u64,
        command: Vec<String>,
        cwd: PathBuf,
        env: BTreeMap<String, String>,
    ) -> (Arc<Self>, oneshot::Receiver<Result<(), String>>) {
        let (commands, commands_rx) = mpsc::unbounded_channel();
        let (ready, ready_rx) = oneshot::channel();
        let session = Arc::new(Self {
            id: uuid::Uuid::new_v4().to_string(),
            number,
            command,
            cwd,
            inner: Mutex::new(Inner {
                state: SessionState::Starting,
                events: Vec::new(),
                sender: broadcast::channel(BROADCAST_CAPACITY).0,
                pending: HashMap::new(),
                next_request: 1,
                turn_active: false,
                ready: Some(ready),
            }),
            commands,
            task: Mutex::new(None),
        });
        let task = tokio::spawn(session.clone().run(commands_rx, env));
        *session.task.lock().unwrap() = Some(task);
        (session, ready_rx)
    }

    pub fn info(&self) -> SessionInfo {
        let inner = self.lock();
        SessionInfo {
            id: self.id.clone(),
            command: self.command.clone(),
            cwd: self.cwd.clone(),
            state: inner.state,
            last_seq: inner.events.len() as u64,
        }
    }

    /// Events with `seq > after_seq`, and a receiver for every later event.
    pub fn subscribe(&self, after_seq: u64) -> (Vec<Event>, broadcast::Receiver<Event>) {
        let inner = self.lock();
        let start = (after_seq as usize).min(inner.events.len());
        (inner.events[start..].to_vec(), inner.sender.subscribe())
    }

    pub fn prompt(&self, text: String) -> Result<(), ErrorBody> {
        let mut inner = self.lock();
        if inner.state != SessionState::Idle {
            return Err(invalid_state(inner.state));
        }
        inner.turn_active = true;
        inner.settle();
        append(
            &self.id,
            &mut inner,
            EventKind::PromptStarted { text: text.clone() },
        );
        let _ = self.commands.send(Command::Prompt(text));
        Ok(())
    }

    /// Cancels the running turn and every pending permission request.
    pub fn cancel(&self) {
        let _ = self.commands.send(Command::Cancel);
    }

    pub fn permit(&self, request_id: &str, option_id: &str) -> Result<(), ErrorBody> {
        let mut inner = self.lock();
        let Some(pending) = inner.pending.get(request_id) else {
            return Err(ErrorBody {
                code: code::INVALID_STATE,
                message: format!("permission request {request_id} is not pending"),
            });
        };
        if !pending
            .options
            .iter()
            .any(|option| &*option.option_id.0 == option_id)
        {
            return Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: format!("{option_id} is not an option of request {request_id}"),
            });
        }
        let pending = inner.pending.remove(request_id).unwrap();
        let _ = pending.sender.send(RequestPermissionOutcome::Selected(
            SelectedPermissionOutcome::new(option_id.to_owned()),
        ));
        Ok(())
    }

    /// Stops the agent, killing its process group, and ends every
    /// subscription with `SessionClosed`.
    pub fn close(&self) {
        {
            let mut inner = self.lock();
            inner.state = SessionState::Exited;
            inner.pending.clear();
            if let Some(ready) = inner.ready.take() {
                let _ = ready.send(Err("session closed".into()));
            }
            append(&self.id, &mut inner, EventKind::SessionClosed);
        }
        if let Some(task) = self.task.lock().unwrap().take() {
            task.abort();
        }
    }

    fn lock(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap()
    }

    fn append(&self, kind: EventKind) {
        append(&self.id, &mut self.lock(), kind);
    }

    fn started(&self) {
        let mut inner = self.lock();
        inner.state = SessionState::Idle;
        if let Some(ready) = inner.ready.take() {
            let _ = ready.send(Ok(()));
        }
    }

    fn request_permission(
        &self,
        request: RequestPermissionRequest,
    ) -> (String, oneshot::Receiver<RequestPermissionOutcome>) {
        let mut inner = self.lock();
        let request_id = inner.next_request.to_string();
        inner.next_request += 1;
        let (sender, receiver) = oneshot::channel();
        inner.pending.insert(
            request_id.clone(),
            Pending {
                options: request.options.clone(),
                sender,
            },
        );
        inner.settle();
        append(
            &self.id,
            &mut inner,
            EventKind::PermissionRequested {
                request_id: request_id.clone(),
                tool_call: request.tool_call,
                options: request.options,
            },
        );
        (request_id, receiver)
    }

    fn permission_resolved(&self, request_id: String, outcome: RequestPermissionOutcome) {
        let mut inner = self.lock();
        inner.pending.remove(&request_id);
        inner.settle();
        append(
            &self.id,
            &mut inner,
            EventKind::PermissionResolved {
                request_id,
                outcome,
            },
        );
    }

    fn turn_ended(&self, result: Result<PromptResponse, agent_client_protocol::Error>) {
        let mut inner = self.lock();
        inner.turn_active = false;
        inner.settle();
        let kind = match result {
            Ok(response) => EventKind::TurnEnded {
                stop_reason: Some(response.stop_reason),
                error: None,
            },
            Err(error) => EventKind::TurnEnded {
                stop_reason: None,
                error: Some(error.to_string()),
            },
        };
        append(&self.id, &mut inner, kind);
    }

    /// Answers every pending permission request with `Cancelled`.
    fn cancel_pending(&self) {
        let pending: Vec<Pending> = self.lock().pending.drain().map(|(_, p)| p).collect();
        for pending in pending {
            let _ = pending.sender.send(RequestPermissionOutcome::Cancelled);
        }
    }

    fn exited(&self, error: Option<String>) {
        let mut inner = self.lock();
        if inner.state == SessionState::Exited {
            return;
        }
        inner.state = SessionState::Exited;
        inner.pending.clear();
        if let Some(ready) = inner.ready.take() {
            let reason = error
                .clone()
                .unwrap_or_else(|| "agent exited during startup".into());
            let _ = ready.send(Err(reason));
        }
        append(&self.id, &mut inner, EventKind::AgentExited { error });
    }

    async fn run(
        self: Arc<Self>,
        mut commands: mpsc::UnboundedReceiver<Command>,
        env: BTreeMap<String, String>,
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
                        session.append(EventKind::Update(notification.update));
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
                        let (request_id, outcome) = session.request_permission(request);
                        let session = session.clone();
                        connection.spawn(async move {
                            let outcome =
                                outcome.await.unwrap_or(RequestPermissionOutcome::Cancelled);
                            session.permission_resolved(request_id, outcome.clone());
                            responder.respond(RequestPermissionResponse::new(outcome))
                        })
                    }
                },
                agent_client_protocol::on_receive_request!(),
            )
            .connect_with(
                AcpAgent::new(config),
                async move |connection: ConnectionTo<Agent>| {
                    connection
                        .send_request(InitializeRequest::new(ProtocolVersion::V1))
                        .block_task()
                        .await?;
                    let acp_session = connection
                        .send_request(NewSessionRequest::new(session.cwd.clone()))
                        .block_task()
                        .await?
                        .session_id;
                    session.started();
                    loop {
                        let command = tokio::select! {
                            () = connection.incoming_closed() => break,
                            command = commands.recv() => command,
                        };
                        match command {
                            None => break,
                            Some(Command::Prompt(text)) => {
                                let session = session.clone();
                                connection
                                    .send_request(PromptRequest::new(
                                        acp_session.clone(),
                                        vec![ContentBlock::Text(TextContent::new(text))],
                                    ))
                                    .on_receiving_result(async move |result| {
                                        session.turn_ended(result);
                                        Ok(())
                                    })?;
                            }
                            Some(Command::Cancel) => {
                                connection.send_notification(CancelNotification::new(
                                    acp_session.clone(),
                                ))?;
                                session.cancel_pending();
                            }
                        }
                    }
                    Ok(())
                },
            )
            .await;
        self.exited(result.err().map(|error| error.to_string()));
    }
}

fn append(session_id: &str, inner: &mut Inner, kind: EventKind) {
    let event = Event {
        session_id: session_id.to_owned(),
        seq: inner.events.len() as u64 + 1,
        kind,
    };
    inner.events.push(event.clone());
    let _ = inner.sender.send(event);
}

fn invalid_state(state: SessionState) -> ErrorBody {
    ErrorBody {
        code: code::INVALID_STATE,
        message: format!("session is {}", state_name(state)),
    }
}

fn state_name(state: SessionState) -> &'static str {
    match state {
        SessionState::Starting => "starting",
        SessionState::Idle => "idle",
        SessionState::Running => "running",
        SessionState::AwaitingPermission => "awaiting permission",
        SessionState::Exited => "exited",
    }
}
