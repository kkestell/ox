use std::path::PathBuf;
use std::time::Duration;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::*;
use agent_client_protocol::{
    AcpAgent, AcpAgentConfig, Agent, Client, ConnectTo, ConnectionTo, LineDirection, Responder,
    on_receive_notification, on_receive_request,
};
use tokio::sync::mpsc::{UnboundedReceiver, UnboundedSender, unbounded_channel};

use crate::{config::ServerConfig, tui};

pub enum Event {
    Update(SessionId, SessionUpdate),
    Permission(
        SessionId,
        RequestPermissionRequest,
        Responder<RequestPermissionResponse>,
    ),
    Finished(SessionId, agent_client_protocol::Result<PromptResponse>),
    Diagnostic(String),
}

pub struct Session {
    connection: ConnectionTo<Agent>,
    id: SessionId,
    active: bool,
    directory: PathBuf,
    can_resume: bool,
    events: UnboundedSender<Event>,
    /// The server runs tool calls in order, so at most one request is
    /// pending.
    pub permission_request: Option<(
        RequestPermissionRequest,
        Responder<RequestPermissionResponse>,
    )>,
    pub busy: bool,
    cancelling: bool,
    pub config_options: Vec<SessionConfigOption>,
    /// The names in the latest available commands update.
    pub commands: Vec<String>,
    pub usage: Option<UsageUpdate>,
    /// The prompt sent during a turn, held until the cancelled turn finishes.
    pub queued: Option<String>,
}

impl Session {
    #[cfg(test)]
    pub fn id(&self) -> &SessionId {
        &self.id
    }
    pub fn active(&self) -> bool {
        self.active
    }

    pub fn accepts(&self, id: &SessionId) -> bool {
        self.active && self.id == *id
    }

    pub fn can_resume(&self) -> bool {
        self.can_resume
    }

    pub async fn list(&mut self) -> anyhow::Result<Vec<SessionInfo>> {
        let mut sessions = Vec::new();
        let mut cursor = None;
        loop {
            let response = self
                .connection
                .send_request(
                    ListSessionsRequest::new()
                        .cwd(self.directory.clone())
                        .cursor(cursor),
                )
                .block_task()
                .await?;
            sessions.extend(response.sessions);
            cursor = response.next_cursor;
            if cursor.is_none() {
                return Ok(sessions);
            }
        }
    }

    pub async fn close(&mut self) -> anyhow::Result<()> {
        self.connection
            .send_request(CloseSessionRequest::new(self.id.clone()))
            .block_task()
            .await?;
        self.active = false;
        self.busy = false;
        self.cancelling = false;
        self.queued = None;
        self.permission_request = None;
        self.config_options.clear();
        self.commands.clear();
        self.usage = None;
        Ok(())
    }

    pub async fn create(&mut self) -> anyhow::Result<()> {
        let response = self
            .connection
            .send_request(NewSessionRequest::new(self.directory.clone()))
            .block_task()
            .await?;
        self.id = response.session_id;
        self.active = true;
        self.config_options = response.config_options.unwrap_or_default();
        Ok(())
    }

    pub async fn load(&mut self, id: SessionId) -> anyhow::Result<()> {
        let response = self
            .connection
            .send_request(LoadSessionRequest::new(id.clone(), self.directory.clone()))
            .block_task()
            .await?;
        self.id = id;
        self.active = true;
        self.config_options = response.config_options.unwrap_or_default();
        Ok(())
    }
    pub async fn set_config_option(
        &mut self,
        id: SessionConfigId,
        value: SessionConfigValueId,
    ) -> anyhow::Result<()> {
        let response = self
            .connection
            .send_request(SetSessionConfigOptionRequest::new(
                self.id.clone(),
                id,
                value,
            ))
            .block_task()
            .await?;
        self.config_options = response.config_options;
        Ok(())
    }

    /// Sends the prompt, or cancels the running turn and sends it when that
    /// turn finishes. Returns false while another prompt is already queued.
    pub fn prompt(&mut self, text: String) -> anyhow::Result<bool> {
        if self.busy {
            if self.queued.is_some() {
                return Ok(false);
            }
            self.queued = Some(text);
            self.cancel()?;
            return Ok(true);
        }
        self.busy = true;
        let connection = self.connection.clone();
        let events = self.events.clone();
        let request = PromptRequest::new(self.id.clone(), vec![text.into()]);
        let id = self.id.clone();
        self.connection.spawn(async move {
            let response = connection.send_request(request).block_task().await;
            let _ = events.send(Event::Finished(id, response));
            Ok(())
        })?;
        Ok(true)
    }

    pub fn permission(
        &mut self,
        request: RequestPermissionRequest,
        responder: Responder<RequestPermissionResponse>,
    ) -> anyhow::Result<()> {
        if self.cancelling {
            responder.respond(RequestPermissionResponse::new(
                RequestPermissionOutcome::Cancelled,
            ))?;
        } else {
            assert!(
                self.permission_request.is_none(),
                "a permission request arrived while another was pending"
            );
            self.permission_request = Some((request, responder));
        }
        Ok(())
    }

    pub fn answer(&mut self, number: usize) -> anyhow::Result<bool> {
        let Some((request, _)) = &self.permission_request else {
            return Ok(false);
        };
        let Some(option) = number
            .checked_sub(1)
            .and_then(|index| request.options.get(index))
        else {
            return Ok(false);
        };
        let outcome = RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(
            option.option_id.clone(),
        ));
        self.permission_request
            .take()
            .unwrap()
            .1
            .respond(RequestPermissionResponse::new(outcome))?;
        Ok(true)
    }

    pub fn cancel(&mut self) -> anyhow::Result<()> {
        self.cancelling = self.busy;
        if let Some((_, responder)) = self.permission_request.take() {
            responder.respond(RequestPermissionResponse::new(
                RequestPermissionOutcome::Cancelled,
            ))?;
        }
        if self.busy {
            self.connection
                .send_notification(CancelNotification::new(self.id.clone()))?;
        }
        Ok(())
    }

    /// Ends the turn and sends the queued prompt, returning its text.
    pub fn finished(&mut self) -> anyhow::Result<Option<String>> {
        // A completed turn cannot leave an unanswered permission behind.
        if let Some((_, responder)) = self.permission_request.take() {
            responder.respond(RequestPermissionResponse::new(
                RequestPermissionOutcome::Cancelled,
            ))?;
        }
        self.busy = false;
        self.cancelling = false;
        let queued = self.queued.take();
        if let Some(text) = &queued {
            self.prompt(text.clone())?;
        }
        Ok(queued)
    }

    /// Records the session settings, available commands, and usage an update
    /// carries.
    pub fn update(&mut self, update: &SessionUpdate) {
        match update {
            SessionUpdate::ConfigOptionUpdate(update) => {
                self.config_options = update.config_options.clone();
            }
            SessionUpdate::AvailableCommandsUpdate(update) => {
                self.commands = update
                    .available_commands
                    .iter()
                    .map(|command| command.name.clone())
                    .collect();
            }
            SessionUpdate::UsageUpdate(update) => self.usage = Some(update.clone()),
            _ => {}
        }
    }

    pub fn closed(&self) -> impl Future<Output = ()> + Send + use<> {
        let connection = self.connection.clone();
        async move {
            connection.incoming_closed().await;
        }
    }
}

pub async fn initialize(connection: &ConnectionTo<Agent>) -> anyhow::Result<AgentCapabilities> {
    let response = connection
        .send_request(
            InitializeRequest::new(ProtocolVersion::V1)
                .client_info(Implementation::new("ox", env!("CARGO_PKG_VERSION"))),
        )
        .block_task()
        .await?;
    anyhow::ensure!(
        response.protocol_version == ProtocolVersion::V1,
        "the server uses ACP version {}; ox supports only version 1",
        response.protocol_version
    );
    Ok(response.agent_capabilities)
}

pub async fn start(
    server: &ServerConfig,
    directory: PathBuf,
    favorites: Vec<String>,
    config_path: PathBuf,
) -> anyhow::Result<()> {
    let (events, receiver) = unbounded_channel();
    let diagnostics = events.clone();
    let server = AcpAgent::new(AcpAgentConfig::new(&server.command).args(server.args.clone()))
        .with_debug(move |line, direction| {
            if matches!(direction, LineDirection::Stderr) {
                let _ = diagnostics.send(Event::Diagnostic(line.to_string()));
            }
        });
    run(
        server,
        directory,
        events,
        receiver,
        async |session, receiver| tui::run(session, receiver, favorites, config_path).await,
    )
    .await
}

async fn run<F, Fut>(
    server: impl ConnectTo<Client> + 'static,
    directory: PathBuf,
    events: UnboundedSender<Event>,
    receiver: UnboundedReceiver<Event>,
    body: F,
) -> anyhow::Result<()>
where
    F: FnOnce(Session, UnboundedReceiver<Event>) -> Fut + Send,
    Fut: Future<Output = anyhow::Result<()>> + Send,
{
    Client
        .builder()
        .on_receive_notification(
            {
                let events = events.clone();
                async move |notification: SessionNotification, _connection| {
                    let _ =
                        events.send(Event::Update(notification.session_id, notification.update));
                    Ok(())
                }
            },
            on_receive_notification!(),
        )
        .on_receive_request(
            {
                let events = events.clone();
                async move |request: RequestPermissionRequest, responder, _connection| {
                    let _ = events.send(Event::Permission(
                        request.session_id.clone(),
                        request,
                        responder,
                    ));
                    Ok(())
                }
            },
            on_receive_request!(),
        )
        .connect_with(server, async move |connection: ConnectionTo<Agent>| {
            let started = tokio::time::timeout(Duration::from_secs(30), async {
                let capabilities = initialize(&connection).await?;
                Ok::<_, anyhow::Error>((
                    capabilities,
                    connection
                        .send_request(NewSessionRequest::new(directory.clone()))
                        .block_task()
                        .await?,
                ))
            })
            .await;
            let (capabilities, session) = match started {
                Ok(Ok(session)) => session,
                Ok(Err(error)) => return Ok(Err(error)),
                Err(_) => return Ok(Err(anyhow::anyhow!("server startup timed out"))),
            };
            Ok(body(
                Session {
                    connection,
                    id: session.session_id,
                    active: true,
                    directory,
                    can_resume: capabilities.load_session
                        && capabilities.session_capabilities.list.is_some()
                        && capabilities.session_capabilities.close.is_some(),
                    events,
                    permission_request: None,
                    busy: false,
                    cancelling: false,
                    config_options: session.config_options.unwrap_or_default(),
                    commands: Vec::new(),
                    usage: None,
                    queued: None,
                },
                receiver,
            )
            .await)
        })
        .await??;
    Ok(())
}

#[cfg(test)]
pub mod tests {
    use super::*;
    use crate::fixture::{
        Reply, Server, delta, echo_reply, environment, server_binary, shell_reply, sse, text_reply,
    };
    use serde_json::json;

    pub async fn with_session<F, Fut>(replies: Vec<Reply>, body: F)
    where
        F: FnOnce(Session, UnboundedReceiver<Event>) -> Fut + Send,
        Fut: Future<Output = anyhow::Result<()>> + Send,
    {
        with_server(Server::start(replies).await, body).await;
    }

    async fn with_server<F, Fut>(server: Server, body: F)
    where
        F: FnOnce(Session, UnboundedReceiver<Event>) -> Fut + Send,
        Fut: Future<Output = anyhow::Result<()>> + Send,
    {
        let root = tempfile::tempdir().unwrap();
        let workspace = root.path().join("workspace");
        std::fs::create_dir(&workspace).unwrap();
        let agent = AcpAgent::new(
            AcpAgentConfig::new(server_binary())
                .arg("acp")
                .envs(environment(root.path(), server.endpoint())),
        );
        let (events, receiver) = unbounded_channel();
        tokio::time::timeout(
            Duration::from_secs(10),
            run(agent, workspace, events, receiver, body),
        )
        .await
        .expect("ACP test timed out")
        .unwrap();
    }

    pub async fn turn(
        session: &mut Session,
        events: &mut UnboundedReceiver<Event>,
        choices: &[usize],
    ) -> (String, bool) {
        let mut text = String::new();
        let mut choices = choices.iter();
        loop {
            match events.recv().await.unwrap() {
                Event::Update(_, SessionUpdate::AgentMessageChunk(chunk)) => {
                    if let ContentBlock::Text(chunk) = chunk.content {
                        text.push_str(&chunk.text);
                    }
                }
                Event::Permission(_, request, responder) => {
                    session.permission(request, responder).unwrap();
                    if let Some(choice) = choices.next() {
                        assert!(session.answer(*choice).unwrap());
                    }
                }
                Event::Finished(_, result) => {
                    assert!(session.finished().unwrap().is_none());
                    return (text, result.is_ok());
                }
                _ => {}
            }
        }
    }

    #[tokio::test]
    async fn multiple_prompts_share_one_session_and_stream_in_order() {
        let streamed = Reply::Stream(sse(&[
            delta(json!({"role":"assistant", "content":"stream "}), None),
            delta(json!({"content":"arrives "}), None),
            delta(json!({"content":"in order\n"}), Some("stop")),
        ]));
        with_session(
            vec![streamed, echo_reply()],
            async |mut session, mut events| {
                let id = session.id.clone();
                session.prompt("stream".into())?;
                assert_eq!(
                    turn(&mut session, &mut events, &[]).await,
                    ("stream arrives in order\n".into(), true)
                );
                session.prompt("second".into())?;
                assert!(
                    turn(&mut session, &mut events, &[])
                        .await
                        .0
                        .contains("second")
                );
                assert_eq!(session.id, id);
                Ok(())
            },
        )
        .await;
    }

    /// Receives the next available commands update.
    async fn commands(events: &mut UnboundedReceiver<Event>) -> (SessionId, SessionUpdate) {
        loop {
            if let Event::Update(id, update @ SessionUpdate::AvailableCommandsUpdate(_)) =
                events.recv().await.unwrap()
            {
                return (id, update);
            }
        }
    }

    #[tokio::test]
    async fn resume_lists_and_loads_a_different_session_after_close() {
        with_session(vec![text_reply("saved"), echo_reply()], async |mut session, mut events| {
            assert!(session.can_resume());
            assert!(session.commands.is_empty());
            let (id, update) = commands(&mut events).await;
            assert_eq!(id, session.id);
            session.update(&update);
            assert!(session.commands.is_empty());
            let old = session.id.clone();
            session.prompt("First session".into())?;
            turn(&mut session, &mut events, &[]).await;
            let newer = session
                .connection
                .send_request(NewSessionRequest::new(session.directory.clone()))
                .block_task()
                .await?
                .session_id;
            let (id, _) = commands(&mut events).await;
            assert_eq!(id, newer);
            let listed = session.list().await?;
            assert_eq!(listed.len(), 2);
            let saved = listed
                .iter()
                .find(|item| item.session_id == old)
                .expect("the first session is listed");
            assert_eq!(saved.title.as_deref(), Some("First session"));
            assert!(listed.iter().all(|item| item.updated_at.is_some()));
            session.close().await?;
            assert!(!session.active());
            assert!(session.config_options.is_empty());
            assert!(session.commands.is_empty());
            assert!(session.usage.is_none());
            session.load(newer.clone()).await?;
            let (id, update) = commands(&mut events).await;
            assert_eq!(id, newer);
            session.update(&update);
            assert!(session.commands.is_empty());
            assert!(session.active());
            assert_eq!(session.id, newer);
            assert_eq!(session.config_options.len(), 3);
            let mode = session.config_options.iter().find(|option| option.id.0.as_ref() == "mode").unwrap();
            assert!(matches!(&mode.kind, SessionConfigKind::Select(select) if select.current_value.0.as_ref() == "ask"));
            session.prompt("after".into())?;
            assert_eq!(
                turn(&mut session, &mut events, &[]).await.0,
                "you said: after"
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn create_replaces_the_closed_session_with_a_new_one() {
        with_session(vec![], async |mut session, _events| {
            let old = session.id.clone();
            session.close().await?;
            session.create().await?;
            assert!(session.active());
            assert_ne!(session.id, old);
            assert!(!session.config_options.is_empty());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn failed_load_can_be_followed_by_another_selection() {
        with_session(vec![], async |mut session, _events| {
            let other = session
                .connection
                .send_request(NewSessionRequest::new(session.directory.clone()))
                .block_task()
                .await?
                .session_id;
            session.close().await?;
            assert!(session.load(SessionId::new("missing")).await.is_err());
            assert!(!session.active());
            session.load(other).await?;
            assert!(session.active());
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn resume_requires_advertised_list_load_and_close_capabilities() {
        let server = Agent
            .builder()
            .on_receive_request(
                async |_: InitializeRequest, responder, _connection| {
                    responder.respond(
                        InitializeResponse::new(ProtocolVersion::V1)
                            .agent_capabilities(AgentCapabilities::new()),
                    )
                },
                on_receive_request!(),
            )
            .on_receive_request(
                async |_: NewSessionRequest, responder, _connection| {
                    responder.respond(NewSessionResponse::new(SessionId::new("session")))
                },
                on_receive_request!(),
            );
        let (events, receiver) = unbounded_channel();
        run(
            server,
            std::env::current_dir().unwrap(),
            events,
            receiver,
            async |session, _| {
                assert!(!session.can_resume());
                assert!(session.active());
                Ok(())
            },
        )
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn cancellation_answers_pending_permissions_before_next_prompt() {
        with_session(
            vec![shell_reply(&[("touch cancelled", 10)]), echo_reply()],
            async |mut session, mut events| {
                session.prompt("run a command".into())?;
                while session.permission_request.is_none() {
                    if let Event::Permission(_, request, responder) = events.recv().await.unwrap() {
                        session.permission(request, responder)?;
                    }
                }
                session.cancel()?;
                assert!(session.permission_request.is_none());
                let (_text, ok) = turn(&mut session, &mut events, &[]).await;
                assert!(ok);
                session.prompt("after".into())?;
                assert!(turn(&mut session, &mut events, &[]).await.1);
                Ok(())
            },
        )
        .await;
    }

    #[tokio::test]
    async fn running_turn_accepts_cancel_without_a_permission_request() {
        let hang = Reply::Hang(format!(
            "data: {}\n\n",
            delta(json!({"role":"assistant", "content":"cancelled"}), None)
        ));
        with_session(vec![hang], async |mut session, mut events| {
            session.prompt("running".into())?;
            while !matches!(
                events.recv().await.unwrap(),
                Event::Update(_, SessionUpdate::AgentMessageChunk(_))
            ) {}
            session.cancel()?;
            assert_eq!(
                turn(&mut session, &mut events, &[]).await,
                (String::new(), true)
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn closing_a_cancelled_session_queues_the_next_permission_request() {
        let hang = Reply::Hang(format!(
            "data: {}\n\n",
            delta(json!({"role":"assistant", "content":"cancelled"}), None)
        ));
        with_session(
            vec![hang, shell_reply(&[("touch next", 0)])],
            async |mut session, mut events| {
                session.prompt("running".into())?;
                while !matches!(
                    events.recv().await.unwrap(),
                    Event::Update(_, SessionUpdate::AgentMessageChunk(_))
                ) {}
                session.cancel()?;
                session.close().await?;
                session.create().await?;
                session.prompt("run a command".into())?;
                while session.permission_request.is_none() {
                    if let Event::Permission(_, request, responder) = events.recv().await.unwrap() {
                        session.permission(request, responder)?;
                    }
                }
                assert!(session.permission_request.is_some());
                Ok(())
            },
        )
        .await;
    }

    #[tokio::test]
    async fn a_prompt_during_a_turn_cancels_it_and_is_sent_after_it_finishes() {
        let hang = Reply::Hang(format!(
            "data: {}\n\n",
            delta(json!({"role":"assistant", "content":"cancelled"}), None)
        ));
        with_session(vec![hang, echo_reply()], async |mut session, mut events| {
            session.prompt("running".into())?;
            while !matches!(
                events.recv().await.unwrap(),
                Event::Update(_, SessionUpdate::AgentMessageChunk(_))
            ) {}
            session.prompt("next".into())?;
            assert_eq!(session.queued.as_deref(), Some("next"));
            let mut text = String::new();
            let queued = loop {
                match events.recv().await.unwrap() {
                    Event::Update(_, SessionUpdate::AgentMessageChunk(chunk)) => {
                        if let ContentBlock::Text(chunk) = chunk.content {
                            text.push_str(&chunk.text);
                        }
                    }
                    Event::Finished(_, result) => {
                        assert!(result.is_ok());
                        break session.finished()?;
                    }
                    _ => {}
                }
            };
            assert!(text.is_empty());
            assert_eq!(queued.as_deref(), Some("next"));
            assert!(session.busy);
            assert!(session.queued.is_none());
            assert_eq!(
                turn(&mut session, &mut events, &[]).await,
                ("you said: next".into(), true)
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn rejected_and_failed_turns_allow_another_prompt() {
        let failed = Reply::Stream(sse(&[delta(
            json!({"role":"assistant", "content":"failing"}),
            None,
        )]));
        with_session(
            vec![Reply::Status(400, "{}".into()), failed, echo_reply()],
            async |mut session, mut events| {
                for prompt in ["reject", "fail"] {
                    session.prompt(prompt.into())?;
                    assert!(!turn(&mut session, &mut events, &[]).await.1);
                }
                session.prompt("after".into())?;
                assert!(turn(&mut session, &mut events, &[]).await.1);
                Ok(())
            },
        )
        .await;
    }

    #[tokio::test]
    async fn rejects_an_unsupported_protocol_version() {
        let server = Agent.builder().on_receive_request(
            async |_: InitializeRequest, responder, _connection| {
                responder.respond(InitializeResponse::new(ProtocolVersion::from(2)))
            },
            on_receive_request!(),
        );
        let (events, receiver) = unbounded_channel();
        let error = run(
            server,
            PathBuf::from("/"),
            events,
            receiver,
            async |_, _| panic!("must not start terminal"),
        )
        .await
        .unwrap_err();
        assert!(error.to_string().contains("ACP version 2"), "{error}");
    }

    #[tokio::test]
    async fn server_exit_releases_pending_work() {
        let openrouter = Server::start(vec![shell_reply(&[("touch pending", 10)])]).await;
        let root = tempfile::tempdir().unwrap();
        let pid = root.path().join("pid");
        // The shell records its PID and becomes the server, so the test can
        // stop the server.
        let agent = AcpAgent::new(
            AcpAgentConfig::new("/bin/sh")
                .args([
                    "-c".to_owned(),
                    "echo $$ > \"$0\"; exec \"$1\" acp".to_owned(),
                    pid.display().to_string(),
                    server_binary().display().to_string(),
                ])
                .envs(environment(root.path(), openrouter.endpoint())),
        );
        let (events, receiver) = unbounded_channel();
        let result = tokio::time::timeout(
            Duration::from_secs(10),
            run(
                agent,
                root.path().to_owned(),
                events,
                receiver,
                async |mut session, mut events| {
                    session.prompt("run a command".into())?;
                    while session.permission_request.is_none() {
                        if let Event::Permission(_, request, responder) =
                            events.recv().await.unwrap()
                        {
                            session.permission(request, responder)?;
                        }
                    }
                    let pid = std::fs::read_to_string(&pid)?;
                    std::process::Command::new("kill")
                        .arg(pid.trim())
                        .status()?;
                    session.closed().await;
                    Ok(())
                },
            ),
        )
        .await
        .expect("server closure must release pending work");
        // Either the body observes closure or the SDK reports it first.
        if let Err(error) = result {
            assert!(!error.to_string().is_empty());
        }
    }
}
