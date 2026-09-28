use std::collections::VecDeque;
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
    Update(SessionUpdate),
    Permission(
        RequestPermissionRequest,
        Responder<RequestPermissionResponse>,
    ),
    Finished(agent_client_protocol::Result<PromptResponse>),
    Diagnostic(String),
}

pub struct Session {
    connection: ConnectionTo<Agent>,
    id: SessionId,
    events: UnboundedSender<Event>,
    pub pending: VecDeque<(
        RequestPermissionRequest,
        Responder<RequestPermissionResponse>,
    )>,
    pub busy: bool,
    cancelling: bool,
    pub config_options: Vec<SessionConfigOption>,
    pub usage: Option<UsageUpdate>,
    /// The prompt sent during a turn, held until the cancelled turn finishes.
    pub queued: Option<String>,
}

impl Session {
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
    /// turn finishes.
    pub fn prompt(&mut self, text: String) -> anyhow::Result<()> {
        if self.busy {
            self.queued = Some(text);
            return self.cancel();
        }
        self.busy = true;
        let connection = self.connection.clone();
        let events = self.events.clone();
        let request = PromptRequest::new(self.id.clone(), vec![text.into()]);
        self.connection.spawn(async move {
            let response = connection.send_request(request).block_task().await;
            let _ = events.send(Event::Finished(response));
            Ok(())
        })?;
        Ok(())
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
            self.pending.push_back((request, responder));
        }
        Ok(())
    }

    pub fn answer(&mut self, number: usize) -> anyhow::Result<bool> {
        let Some((request, _)) = self.pending.front() else {
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
        self.pending
            .pop_front()
            .unwrap()
            .1
            .respond(RequestPermissionResponse::new(outcome))?;
        Ok(true)
    }

    pub fn cancel(&mut self) -> anyhow::Result<()> {
        self.cancelling = self.busy;
        while let Some((_, responder)) = self.pending.pop_front() {
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
        while let Some((_, responder)) = self.pending.pop_front() {
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

    /// Records the session settings and usage an update carries.
    pub fn update(&mut self, update: &SessionUpdate) {
        match update {
            SessionUpdate::ConfigOptionUpdate(update) => {
                self.config_options = update.config_options.clone();
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

pub async fn initialize(connection: &ConnectionTo<Agent>) -> anyhow::Result<()> {
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
    Ok(())
}

pub async fn start(server: &ServerConfig, directory: PathBuf) -> anyhow::Result<()> {
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
        async |session, receiver| tui::run(session, receiver).await,
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
                    let _ = events.send(Event::Update(notification.update));
                    Ok(())
                }
            },
            on_receive_notification!(),
        )
        .on_receive_request(
            {
                let events = events.clone();
                async move |request: RequestPermissionRequest, responder, _connection| {
                    let _ = events.send(Event::Permission(request, responder));
                    Ok(())
                }
            },
            on_receive_request!(),
        )
        .connect_with(server, async move |connection: ConnectionTo<Agent>| {
            let started = tokio::time::timeout(Duration::from_secs(30), async {
                initialize(&connection).await?;
                Ok::<_, anyhow::Error>(
                    connection
                        .send_request(NewSessionRequest::new(directory))
                        .block_task()
                        .await?,
                )
            })
            .await;
            let session = match started {
                Ok(Ok(session)) => session,
                Ok(Err(error)) => return Ok(Err(error)),
                Err(_) => return Ok(Err(anyhow::anyhow!("server startup timed out"))),
            };
            Ok(body(
                Session {
                    connection,
                    id: session.session_id,
                    events,
                    pending: VecDeque::new(),
                    busy: false,
                    cancelling: false,
                    config_options: session.config_options.unwrap_or_default(),
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
    use ox_fake_server::{Hold, SavedHistory, fake_server};

    pub async fn with_session<F, Fut>(body: F)
    where
        F: FnOnce(Session, UnboundedReceiver<Event>) -> Fut + Send,
        Fut: Future<Output = anyhow::Result<()>> + Send,
    {
        let (events, receiver) = unbounded_channel();
        tokio::time::timeout(
            Duration::from_secs(5),
            run(
                fake_server(Hold::default(), SavedHistory::default()),
                std::env::current_dir().unwrap(),
                events,
                receiver,
                body,
            ),
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
                Event::Update(SessionUpdate::AgentMessageChunk(chunk)) => {
                    if let ContentBlock::Text(chunk) = chunk.content {
                        text.push_str(&chunk.text);
                    }
                }
                Event::Permission(request, responder) => {
                    session.permission(request, responder).unwrap();
                    if let Some(choice) = choices.next() {
                        assert!(session.answer(*choice).unwrap());
                    }
                }
                Event::Finished(result) => {
                    assert!(session.finished().unwrap().is_none());
                    return (text, result.is_ok());
                }
                _ => {}
            }
        }
    }

    #[tokio::test]
    async fn multiple_prompts_share_one_session_and_stream_in_order() {
        with_session(async |mut session, mut events| {
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
        })
        .await;
    }

    #[tokio::test]
    async fn simultaneous_permissions_keep_their_supplied_option_ids() {
        with_session(async |mut session, mut events| {
            session.prompt("tools".into())?;
            while session.pending.len() < 2 {
                if let Event::Permission(request, responder) = events.recv().await.unwrap() {
                    session.permission(request, responder)?;
                }
            }
            assert!(!session.answer(0)?);
            assert!(!session.answer(3)?);
            assert_eq!(session.pending.len(), 2);
            assert!(session.answer(2)?);
            assert_eq!(session.pending.len(), 1);
            assert!(session.answer(1)?);
            let (text, ok) = turn(&mut session, &mut events, &[]).await;
            assert!(ok);
            assert!(text.contains("tally-1: stop"), "{text}");
            assert!(text.contains("tally-2: go"), "{text}");
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn cancellation_answers_pending_and_late_permissions_before_next_prompt() {
        with_session(async |mut session, mut events| {
            session.prompt("tools".into())?;
            while session.pending.len() < 2 {
                if let Event::Permission(request, responder) = events.recv().await.unwrap() {
                    session.permission(request, responder)?;
                }
            }
            session.cancel()?;
            assert!(session.pending.is_empty());
            let (text, ok) = turn(&mut session, &mut events, &[]).await;
            assert!(ok);
            for id in ["tally-1", "tally-2", "tally-3"] {
                assert!(text.contains(&format!("{id}: cancelled")), "{text}");
            }
            session.prompt("after".into())?;
            assert!(turn(&mut session, &mut events, &[]).await.1);
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn running_turn_accepts_cancel_without_a_permission_request() {
        with_session(async |mut session, mut events| {
            session.prompt("running".into())?;
            while !matches!(
                events.recv().await.unwrap(),
                Event::Update(SessionUpdate::AgentMessageChunk(_))
            ) {}
            session.cancel()?;
            assert_eq!(
                turn(&mut session, &mut events, &[]).await,
                ("cancelled".into(), true)
            );
            Ok(())
        })
        .await;
    }

    #[tokio::test]
    async fn a_prompt_during_a_turn_cancels_it_and_is_sent_after_it_finishes() {
        with_session(async |mut session, mut events| {
            session.prompt("running".into())?;
            while !matches!(
                events.recv().await.unwrap(),
                Event::Update(SessionUpdate::AgentMessageChunk(_))
            ) {}
            session.prompt("next".into())?;
            assert_eq!(session.queued.as_deref(), Some("next"));
            let mut text = String::new();
            let queued = loop {
                match events.recv().await.unwrap() {
                    Event::Update(SessionUpdate::AgentMessageChunk(chunk)) => {
                        if let ContentBlock::Text(chunk) = chunk.content {
                            text.push_str(&chunk.text);
                        }
                    }
                    Event::Finished(result) => {
                        assert!(result.is_ok());
                        break session.finished()?;
                    }
                    _ => {}
                }
            };
            assert_eq!(text, "cancelled");
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
        with_session(async |mut session, mut events| {
            for prompt in ["reject", "fail"] {
                session.prompt(prompt.into())?;
                assert!(!turn(&mut session, &mut events, &[]).await.1);
            }
            session.prompt("after".into())?;
            assert!(turn(&mut session, &mut events, &[]).await.1);
            Ok(())
        })
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
        let (client, server) = agent_client_protocol::Channel::duplex();
        let task =
            tokio::spawn(fake_server(Hold::default(), SavedHistory::default()).connect_to(server));
        let (events, receiver) = unbounded_channel();
        let result = tokio::time::timeout(
            Duration::from_secs(5),
            run(
                client,
                PathBuf::from("/"),
                events,
                receiver,
                async |mut session, mut events| {
                    session.prompt("tools".into())?;
                    while session.pending.len() < 2 {
                        if let Event::Permission(request, responder) = events.recv().await.unwrap()
                        {
                            session.permission(request, responder)?;
                        }
                    }
                    task.abort();
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
