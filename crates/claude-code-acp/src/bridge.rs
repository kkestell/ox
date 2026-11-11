use std::{
    collections::HashMap,
    future::Future,
    sync::{Arc, Mutex},
};

use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    self as acp, ContentBlock, ContentChunk, SessionConfigOption, SessionConfigOptionCategory,
    SessionConfigSelectOption, SessionUpdate, StopReason, TextContent,
};
use agent_client_protocol::{Client, Error};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use tokio::sync::watch;
use uuid::Uuid;

use crate::{
    config::Config,
    history::{History, HistoryEntry},
    runner::{self, Finish, McpServer, RunError, RunRequest},
    store::{Session, Store},
    updates::{Updates, notify},
    validation,
};

/// A prompt turn registered for its session.
struct Turn {
    running: Running,
    session: Session,
    prompt: String,
    cancelled: watch::Receiver<bool>,
}

/// A session's entry in `Bridge::running`, removed when dropped.
struct Running {
    bridge: Arc<Bridge>,
    id: String,
}

impl Drop for Running {
    fn drop(&mut self) {
        self.bridge.running.lock().unwrap().remove(&self.id);
    }
}

pub struct Bridge {
    config: Config,
    store: Store,
    history: History,
    running: Mutex<HashMap<String, watch::Sender<bool>>>,
}

impl Bridge {
    pub fn new(config: Config) -> Arc<Self> {
        Arc::new(Self {
            store: Store::new(config.store_dir.clone()),
            history: History::new(config.claude_dir.clone()),
            config,
            running: Mutex::new(HashMap::new()),
        })
    }

    pub fn initialize(&self, request: acp::InitializeRequest) -> acp::InitializeResponse {
        acp::InitializeResponse::new(request.protocol_version)
            .agent_info(
                acp::Implementation::new("claude-code-acp", env!("CARGO_PKG_VERSION"))
                    .title("Claude Code ACP Bridge"),
            )
            .agent_capabilities(
                acp::AgentCapabilities::new()
                    .load_session(true)
                    .session_capabilities(
                        acp::SessionCapabilities::new()
                            .list(acp::SessionListCapabilities::new())
                            .resume(acp::SessionResumeCapabilities::new()),
                    ),
            )
    }

    pub fn new_session(
        &self,
        request: acp::NewSessionRequest,
    ) -> std::result::Result<acp::NewSessionResponse, Error> {
        let cwd = validation::cwd(&request.cwd).map_err(invalid)?;
        let servers = parse_mcp(request.mcp_servers)?;
        let id = Uuid::new_v4().simple().to_string();
        let session = Session::new(cwd, servers);
        self.store.create(id.clone(), session.clone());
        Ok(acp::NewSessionResponse::new(id).config_options(self.options(&session)))
    }

    pub fn load_session(
        &self,
        request: acp::LoadSessionRequest,
    ) -> std::result::Result<acp::LoadSessionResponse, Error> {
        let id = request.session_id.to_string();
        let cwd = validation::cwd(&request.cwd).map_err(invalid)?;
        let servers = parse_mcp(request.mcp_servers)?;
        let session = self.require(&id)?;
        if session.cwd != cwd {
            return Err(invalid("Session cwd does not match"));
        }
        if !servers.is_empty() {
            self.store
                .update(&id, |session| session.mcp_servers = servers);
        }
        let session = self.require(&id)?;
        Ok(acp::LoadSessionResponse::new().config_options(self.options(&session)))
    }

    pub fn list_sessions(
        &self,
        request: acp::ListSessionsRequest,
    ) -> std::result::Result<acp::ListSessionsResponse, Error> {
        let mut history: HashMap<String, HistoryEntry> = self
            .history
            .list(request.cwd.as_deref())
            .into_iter()
            .map(|entry| (entry.id.clone(), entry))
            .collect();
        let mut entries = Vec::new();
        for (id, session) in self.store.list(request.cwd.as_deref()) {
            let transcript = session
                .claude_session_id
                .as_ref()
                .and_then(|claude_id| history.remove(claude_id));
            history.remove(&id);
            let (title, updated_at) = match transcript {
                Some(transcript) => (
                    session.title.or(transcript.title),
                    session.updated_at.max(transcript.updated_at),
                ),
                None => (session.title, session.updated_at),
            };
            entries.push(
                acp::SessionInfo::new(id, session.cwd)
                    .title(title)
                    .updated_at(updated_at),
            );
        }
        entries.extend(history.into_values().map(|entry| {
            acp::SessionInfo::new(entry.id, entry.cwd)
                .title(entry.title)
                .updated_at(entry.updated_at)
        }));
        entries.sort_by(|a, b| {
            b.updated_at
                .cmp(&a.updated_at)
                .then(a.session_id.to_string().cmp(&b.session_id.to_string()))
        });
        let offset = if let Some(cursor) = request.cursor {
            let id = URL_SAFE_NO_PAD
                .decode(cursor)
                .ok()
                .and_then(|s| String::from_utf8(s).ok())
                .ok_or_else(|| invalid("Invalid session list cursor"))?;
            entries
                .iter()
                .position(|entry| entry.session_id.to_string() == id)
                .map(|index| index + 1)
                .ok_or_else(|| invalid("Invalid session list cursor"))?
        } else {
            0
        };
        let next = if offset + 50 < entries.len() {
            entries
                .get(offset + 49)
                .map(|entry| URL_SAFE_NO_PAD.encode(entry.session_id.to_string()))
        } else {
            None
        };
        Ok(
            acp::ListSessionsResponse::new(entries.into_iter().skip(offset).take(50).collect())
                .next_cursor(next),
        )
    }

    pub fn resume_session(
        &self,
        request: acp::ResumeSessionRequest,
    ) -> std::result::Result<acp::ResumeSessionResponse, Error> {
        let id = request.session_id.to_string();
        let cwd = validation::cwd(&request.cwd).map_err(invalid)?;
        let servers = parse_mcp(request.mcp_servers)?;
        if let Some(session) = self.store.get(&id) {
            if session.cwd != cwd {
                return Err(invalid("Session cwd does not match"));
            }
            self.store.update(&id, |session| {
                if !servers.is_empty() {
                    session.mcp_servers = servers;
                }
            });
        } else if self.history.find(&id, &cwd) {
            let mut session = Session::new(cwd, servers);
            session.claude_session_id = Some(id.clone());
            self.store.create(id.clone(), session);
        } else {
            return Err(Error::new(
                -32002,
                format!("Claude Code session {id} not found"),
            ));
        }
        let session = self.require(&id)?;
        Ok(acp::ResumeSessionResponse::new().config_options(self.options(&session)))
    }

    pub fn set_config(
        &self,
        request: acp::SetSessionConfigOptionRequest,
        connection: &ConnectionTo<Client>,
    ) -> std::result::Result<acp::SetSessionConfigOptionResponse, Error> {
        let id = request.session_id.to_string();
        self.require(&id)?;
        let key = request.config_id.to_string();
        let value = request
            .value
            .as_value_id()
            .ok_or_else(|| invalid("Expected a select value"))?
            .to_string();
        match key.as_str() {
            "mode" if is_mode(&value) => {
                self.store
                    .update(&id, |session| session.mode = Some(value.clone()));
            }
            "model" => {
                validation::model_id(&value).map_err(invalid)?;
                if !self
                    .model_ids(&self.require(&id)?)
                    .iter()
                    .any(|candidate| candidate == &value)
                {
                    return Err(invalid(format!("Invalid model: {value}")));
                }
                self.store
                    .update(&id, |session| session.model = Some(value));
            }
            _ => return Err(invalid("Unknown configuration option or value")),
        }
        let options = self.options(&self.require(&id)?);
        notify(
            connection,
            &id,
            SessionUpdate::ConfigOptionUpdate(acp::ConfigOptionUpdate::new(options.clone())),
        );
        Ok(acp::SetSessionConfigOptionResponse::new(options))
    }

    pub fn cancel(&self, id: &str) {
        if let Some(sender) = self.running.lock().unwrap().get(id) {
            let _ = sender.send(true);
        }
    }

    /// Registers a prompt turn for its session and returns the future that
    /// runs it. Registration happens before the future is first polled, so a
    /// cancel that arrives right after the prompt reaches this turn.
    pub fn prompt(
        self: &Arc<Self>,
        request: acp::PromptRequest,
        connection: ConnectionTo<Client>,
    ) -> impl Future<Output = std::result::Result<acp::PromptResponse, Error>> + Send + 'static
    {
        let turn = self.start_turn(&request);
        let bridge = self.clone();
        async move { bridge.run_turn(turn?, connection).await }
    }

    fn start_turn(
        self: &Arc<Self>,
        request: &acp::PromptRequest,
    ) -> std::result::Result<Turn, Error> {
        let id = request.session_id.to_string();
        let session = self.require(&id)?;
        let prompt = request
            .prompt
            .iter()
            .filter_map(|block| match block {
                ContentBlock::Text(text) => Some(text.text.as_str()),
                _ => None,
            })
            .collect::<Vec<_>>()
            .join("\n");
        if prompt.trim().is_empty() {
            return Err(invalid("Empty prompt text"));
        }
        let (cancel_tx, cancelled) = watch::channel(false);
        {
            let mut running = self.running.lock().unwrap();
            if running.contains_key(&id) {
                return Err(invalid("A prompt is already running for this session"));
            }
            running.insert(id.clone(), cancel_tx);
        }
        Ok(Turn {
            running: Running {
                bridge: self.clone(),
                id,
            },
            session,
            prompt,
            cancelled,
        })
    }

    async fn run_turn(
        &self,
        turn: Turn,
        connection: ConnectionTo<Client>,
    ) -> std::result::Result<acp::PromptResponse, Error> {
        let Turn {
            running,
            session,
            prompt,
            cancelled,
        } = turn;
        let id = running.id.as_str();
        if session.title.is_none() {
            let title: String = prompt
                .split_whitespace()
                .collect::<Vec<_>>()
                .join(" ")
                .chars()
                .take(120)
                .collect();
            self.store
                .update(id, |session| session.title = Some(title.clone()));
            notify(
                &connection,
                id,
                SessionUpdate::SessionInfoUpdate(acp::SessionInfoUpdate::new().title(title)),
            );
        }

        let updates = Updates::new(&connection, id, &session.cwd);
        let model = session.model.as_deref().unwrap_or(&self.config.model);
        let outcome = runner::run(
            &self.config,
            RunRequest {
                cwd: &session.cwd,
                prompt: &prompt,
                resume: session.claude_session_id.as_deref(),
                model,
                mode: session_mode(&session),
                mcp_servers: &session.mcp_servers,
            },
            cancelled,
            |event| updates.event(event),
            |permission| updates.permission(permission),
        )
        .await;
        // A turn that did not finish only keeps its Claude session when Claude
        // saved a transcript that `--resume` can continue.
        let claude_id = outcome.session_id.filter(|claude_id| {
            outcome.result.is_ok() || self.history.find(claude_id, &session.cwd)
        });
        if let Some(claude_id) = claude_id {
            self.store.update(id, |session| {
                session.claude_session_id = Some(claude_id);
            });
        }
        let stop_reason = match outcome.result {
            Ok(finish) => {
                if let Some(session) = self.store.get(id) {
                    notify(
                        &connection,
                        id,
                        SessionUpdate::SessionInfoUpdate(
                            acp::SessionInfoUpdate::new().updated_at(session.updated_at),
                        ),
                    );
                }
                match finish {
                    Finish::EndTurn => StopReason::EndTurn,
                    Finish::MaxTurns => StopReason::MaxTurnRequests,
                }
            }
            Err(RunError::Cancelled) => {
                updates.fail_open_tools();
                StopReason::Cancelled
            }
            Err(RunError::Failed(error)) => {
                updates.fail_open_tools();
                notify(
                    &connection,
                    id,
                    SessionUpdate::AgentMessageChunk(ContentChunk::new(ContentBlock::Text(
                        TextContent::new(format!("Error: {error}")),
                    ))),
                );
                StopReason::EndTurn
            }
        };
        drop(running);
        Ok(acp::PromptResponse::new(stop_reason))
    }

    fn require(&self, id: &str) -> std::result::Result<Session, Error> {
        self.store
            .get(id)
            .ok_or_else(|| Error::new(-32002, format!("Session {id} not found")))
    }

    fn options(&self, session: &Session) -> Vec<SessionConfigOption> {
        let mode = session_mode(session);
        let model = session.model.as_deref().unwrap_or(&self.config.model);
        let model_options = self
            .model_ids(session)
            .into_iter()
            .map(|id| {
                let name = match id.as_str() {
                    "claude-fable-5-2" => "Fable 5.2",
                    "claude-opus-5-5" => "Opus 5.5",
                    "claude-sonnet-5-5" => "Sonnet 5.5",
                    "claude-haiku-5-5" => "Haiku 5.5",
                    _ => id.as_str(),
                };
                SessionConfigSelectOption::new(id.clone(), name.to_owned())
            })
            .collect::<Vec<_>>();
        vec![
            SessionConfigOption::select("model", "Model", model.to_owned(), model_options)
                .category(SessionConfigOptionCategory::Model),
            SessionConfigOption::select("mode", "Mode", mode.to_owned(), mode_options())
                .category(SessionConfigOptionCategory::Mode),
        ]
    }

    fn model_ids(&self, session: &Session) -> Vec<String> {
        let mut models: Vec<String> = [
            "claude-fable-5-2",
            "claude-opus-5-5",
            "claude-sonnet-5-5",
            "claude-haiku-5-5",
        ]
        .into_iter()
        .map(str::to_owned)
        .collect();
        for model in [
            &self.config.model,
            session.model.as_ref().unwrap_or(&self.config.model),
        ] {
            if !models.contains(model) {
                models.push(model.clone());
            }
        }
        models
    }
}

fn is_mode(value: &str) -> bool {
    ["auto", "manual", "acceptEdits", "plan"].contains(&value)
}

fn session_mode(session: &Session) -> &str {
    session
        .mode
        .as_deref()
        .filter(|mode| is_mode(mode))
        .unwrap_or("manual")
}

fn mode_options() -> Vec<SessionConfigSelectOption> {
    [
        ("auto", "Auto", "Let Claude assess actions automatically"),
        ("manual", "Manual", "Ask before edits and commands"),
        (
            "acceptEdits",
            "Accept edits",
            "Accept file edits automatically",
        ),
        ("plan", "Plan", "Explore and plan without editing files"),
    ]
    .into_iter()
    .map(|(id, name, description)| {
        SessionConfigSelectOption::new(id, name).description(description)
    })
    .collect()
}

fn parse_mcp(servers: Vec<acp::McpServer>) -> std::result::Result<Vec<McpServer>, Error> {
    servers
        .into_iter()
        .filter_map(|server| match server {
            acp::McpServer::Stdio(server) => Some(server),
            _ => None,
        })
        .map(|server| {
            validation::mcp_command(&server.command).map_err(invalid)?;
            for arg in &server.args {
                validation::mcp_arg(arg).map_err(invalid)?;
            }
            Ok(McpServer {
                name: server.name,
                command: server.command.to_string_lossy().into_owned(),
                args: server.args,
                env: server
                    .env
                    .into_iter()
                    .map(|var| (var.name, var.value))
                    .collect(),
            })
        })
        .collect()
}

fn invalid(message: impl ToString) -> Error {
    Error::new(-32602, message.to_string())
}
