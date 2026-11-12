use std::{
    collections::{HashMap, HashSet},
    future::Future,
    io,
    sync::{Arc, Mutex},
};

use agent_client_protocol::v2::schema::{
    self as acp, SessionConfigOption, SessionConfigOptionCategory, SessionConfigSelectOption,
    SessionUpdate,
};
use agent_client_protocol::v2::{ConnectionTo, Responder};
use agent_client_protocol::{Client, Error};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use tokio::sync::oneshot;
use uuid::Uuid;

use crate::{
    config::Config,
    content,
    history::{History, HistoryEntry},
    runner::{Claude, McpServer, StartRequest, TurnEnd},
    store::{Session, Store},
    updates::{Updates, notify},
    validation,
};

/// A config option to change.
enum Setting {
    Mode(String),
    Model(String),
    Effort(String),
}

/// A config change sent to its session's claude.
struct ConfigChange {
    id: String,
    setting: Setting,
    /// The receiver of claude's answer, when the session has a claude.
    accepted: Option<oneshot::Receiver<Result<(), String>>>,
}

pub struct Bridge {
    store: Store,
    history: History,
    /// The sessions opened with `session/new` or `session/resume` and not
    /// closed.
    active: Mutex<HashSet<String>>,
    /// The claude of each session that has taken a prompt.
    claudes: Mutex<HashMap<String, Claude>>,
}

impl Bridge {
    pub fn new(config: Config) -> Arc<Self> {
        Arc::new(Self {
            history: History::new(&config.claude_dir),
            store: Store::new(config.store_dir),
            active: Mutex::new(HashSet::new()),
            claudes: Mutex::new(HashMap::new()),
        })
    }

    pub fn initialize(request: &acp::InitializeRequest) -> acp::InitializeResponse {
        acp::InitializeResponse::new(
            request.protocol_version,
            acp::Implementation::new("claude-code-acp", env!("CARGO_PKG_VERSION"))
                .title("Claude Code ACP Bridge"),
        )
        .capabilities(
            acp::AgentCapabilities::new().session(
                acp::SessionCapabilities::new()
                    .prompt(
                        acp::PromptCapabilities::new()
                            .image(acp::PromptImageCapabilities::new())
                            .embedded_context(acp::PromptEmbeddedContextCapabilities::new()),
                    )
                    .mcp(acp::McpCapabilities::new().stdio(acp::McpStdioCapabilities::new())),
            ),
        )
    }

    pub fn new_session(
        &self,
        request: acp::NewSessionRequest,
    ) -> std::result::Result<acp::NewSessionResponse, Error> {
        let cwd = validation::cwd(&request.cwd.0).map_err(invalid)?;
        let servers = parse_mcp(request.mcp_servers)?;
        let id = Uuid::new_v4().simple().to_string();
        let session = Session::new(cwd, servers);
        self.store
            .create(id.clone(), &session)
            .map_err(store_error)?;
        self.active.lock().unwrap().insert(id.clone());
        Ok(acp::NewSessionResponse::new(id).config_options(Self::options(&session)))
    }

    /// Opens a session. `replayFrom: start` replays its conversation from
    /// Claude's transcript first. The session's current state follows, and
    /// then the response.
    pub fn resume_session(
        &self,
        request: acp::ResumeSessionRequest,
        connection: &ConnectionTo<Client>,
    ) -> std::result::Result<acp::ResumeSessionResponse, Error> {
        let replay = match request.replay_from {
            None => false,
            Some(acp::ReplayFrom::Start(_)) => true,
            Some(_) => return Err(invalid("Unsupported replay cursor")),
        };
        let id = request.session_id.to_string();
        let session = self.open(&id, &request.cwd.0, request.mcp_servers)?;
        if replay && let Some(claude_id) = &session.claude_session_id {
            Updates::new(connection.clone(), &id).replay(
                self.history.messages(claude_id, &session.cwd),
                self.history.subagents(claude_id, &session.cwd),
            );
        }
        let state = match self.claudes.lock().unwrap().get(&id) {
            Some(claude) => claude.state(),
            None => acp::StateUpdate::Idle(acp::IdleStateUpdate::new()),
        };
        notify(connection, &id, SessionUpdate::StateUpdate(state));
        Ok(acp::ResumeSessionResponse::new().config_options(Self::options(&session)))
    }

    pub fn list_sessions(
        &self,
        request: acp::ListSessionsRequest,
    ) -> std::result::Result<acp::ListSessionsResponse, Error> {
        let mut history: HashMap<String, HistoryEntry> = self
            .history
            .list(request.cwd.as_ref().map(|cwd| cwd.0.as_path()))
            .into_iter()
            .map(|entry| (entry.id.clone(), entry))
            .collect();
        let mut entries = Vec::new();
        for (id, session) in self
            .store
            .list(request.cwd.as_ref().map(|cwd| cwd.0.as_path()))
            .map_err(store_error)?
        {
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
                .decode(cursor.as_ref() as &str)
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
                .next_cursor(next.map(acp::SessionListCursor::new)),
        )
    }

    /// Finds a bridge session, or a Claude session from its transcripts,
    /// which becomes a bridge session under the same ID. The request's MCP
    /// servers replace the session's, and its next prompt starts a claude
    /// that has them.
    fn open(
        &self,
        id: &str,
        cwd: &std::path::Path,
        servers: Vec<acp::McpServer>,
    ) -> std::result::Result<Session, Error> {
        let cwd = validation::cwd(cwd).map_err(invalid)?;
        let servers = parse_mcp(servers)?;
        if let Some(session) = self.store.get(id).map_err(store_error)? {
            if session.cwd != cwd {
                return Err(invalid("Session cwd does not match"));
            }
            if !servers.is_empty() {
                self.store
                    .update(id, |session| session.mcp_servers = servers)
                    .map_err(store_error)?;
                self.claudes.lock().unwrap().remove(id);
            }
        } else if self.history.find(id, &cwd) {
            let mut session = Session::new(cwd, servers);
            session.claude_session_id = Some(id.to_owned());
            self.store
                .create(id.to_owned(), &session)
                .map_err(store_error)?;
        } else {
            return Err(Error::new(
                -32002,
                format!("Claude Code session {id} not found"),
            ));
        }
        self.active.lock().unwrap().insert(id.to_owned());
        self.require(id)
    }

    /// Closes a session: its claude stops and the session must be resumed
    /// to take another prompt. The returned future finishes once claude has
    /// exited. A session that is not open closes without effect.
    pub fn close_session(&self, id: &str) -> impl Future<Output = ()> + Send + 'static {
        self.active.lock().unwrap().remove(id);
        let claude = self.claudes.lock().unwrap().remove(id);
        async move {
            if let Some(claude) = claude {
                claude.stop().await;
            }
        }
    }

    /// Sends a config change to its session's claude and returns the future
    /// that saves it once claude accepts it. The change is sent before the
    /// future is first polled, so it reaches claude ahead of a prompt that
    /// follows it.
    pub fn set_config(
        self: &Arc<Self>,
        request: &acp::SetSessionConfigOptionRequest,
        connection: ConnectionTo<Client>,
    ) -> impl Future<Output = std::result::Result<acp::SetSessionConfigOptionResponse, Error>>
    + Send
    + 'static {
        let change = self.send_config(request);
        let bridge = self.clone();
        async move { bridge.save_config(change?, &connection).await }
    }

    fn send_config(
        &self,
        request: &acp::SetSessionConfigOptionRequest,
    ) -> std::result::Result<ConfigChange, Error> {
        let id = request.session_id.to_string();
        self.require(&id)?;
        let acp::SessionConfigOptionValue::Id { value } = &request.value else {
            return Err(invalid("Expected a select value"));
        };
        let value = value.to_string();
        let claudes = self.claudes.lock().unwrap();
        let claude = claudes.get(&id);
        let (setting, accepted) = match request.config_id.to_string().as_str() {
            "mode" if is_mode(&value) => {
                let accepted = claude.map(|claude| claude.set_permission_mode(&value));
                (Setting::Mode(value), accepted)
            }
            "model" if is_model(&value) => {
                let accepted = claude.map(|claude| claude.set_model(&value));
                (Setting::Model(value), accepted)
            }
            "effort" if is_effort(&value) => {
                let accepted = claude.map(|claude| claude.set_effort(&value));
                (Setting::Effort(value), accepted)
            }
            _ => return Err(invalid("Unknown configuration option or value")),
        };
        Ok(ConfigChange {
            id,
            setting,
            accepted,
        })
    }

    async fn save_config(
        &self,
        change: ConfigChange,
        connection: &ConnectionTo<Client>,
    ) -> std::result::Result<acp::SetSessionConfigOptionResponse, Error> {
        let ConfigChange {
            id,
            setting,
            accepted,
        } = change;
        // A claude that exits before it answers leaves the change to the
        // next claude, which starts with the saved options.
        if let Some(Ok(Err(error))) = match accepted {
            Some(accepted) => Some(accepted.await),
            None => None,
        } {
            return Err(Error::new(-32603, error));
        }
        self.store
            .update(&id, |session| match setting {
                Setting::Mode(mode) => session.mode = Some(mode),
                Setting::Model(model) => session.model = Some(model),
                Setting::Effort(effort) => session.effort = Some(effort),
            })
            .map_err(store_error)?;
        let options = Self::options(&self.require(&id)?);
        notify(
            connection,
            &id,
            SessionUpdate::ConfigOptionUpdate(acp::ConfigOptionUpdate::new(options.clone())),
        );
        Ok(acp::SetSessionConfigOptionResponse::new(options))
    }

    /// Interrupts the session's turn, whether a prompt or claude started it.
    pub fn cancel(&self, id: &str) {
        if let Some(claude) = self.claudes.lock().unwrap().get(id) {
            claude.interrupt();
        }
    }

    /// Sends a prompt to its session's claude, which answers `responder`
    /// once it takes the prompt into a turn. The prompt is sent before this
    /// returns, so a cancel that arrives right after the prompt drops it.
    pub fn prompt(
        self: &Arc<Self>,
        request: &acp::PromptRequest,
        responder: Responder<acp::PromptResponse>,
        connection: &ConnectionTo<Client>,
    ) -> agent_client_protocol::Result<()> {
        let id = request.session_id.to_string();
        let session = match self.require(&id) {
            Ok(session) => session,
            Err(error) => return responder.respond_with_error(error),
        };
        let content = match content::claude_content(&request.prompt) {
            Ok(content) => content,
            Err(error) => return responder.respond_with_error(invalid(error)),
        };
        let text = content::prompt_text(&request.prompt);
        let message_id = Uuid::new_v4().to_string();

        let mut claudes = self.claudes.lock().unwrap();
        // A session whose claude has exited or that has none yet starts a
        // new one, which continues the conversation.
        let responder = match claudes.get(&id) {
            Some(claude) => claude
                .prompt(&message_id, request.prompt.clone(), &content, responder)
                .err()
                .map(|responder| *responder),
            None => Some(responder),
        };
        if let Some(responder) = responder {
            let claude = Claude::start(
                &StartRequest {
                    cwd: &session.cwd,
                    resume: session.claude_session_id.as_deref(),
                    model: session_model(&session),
                    effort: session_effort(&session),
                    mode: session_mode(&session),
                    mcp_servers: &session.mcp_servers,
                },
                Updates::new(connection.clone(), &id),
                self.turn_recorder(&id, &session.cwd, connection),
            );
            match claude {
                Ok(claude) => {
                    if let Err(responder) =
                        claude.prompt(&message_id, request.prompt.clone(), &content, responder)
                    {
                        return responder.respond_with_error(Error::new(-32603, "claude exited"));
                    }
                    claudes.insert(id.clone(), claude);
                }
                Err(error) => return responder.respond_with_error(Error::new(-32603, error)),
            }
        }
        drop(claudes);

        if session.title.is_none() && !text.trim().is_empty() {
            let title: String = text
                .split_whitespace()
                .collect::<Vec<_>>()
                .join(" ")
                .chars()
                .take(120)
                .collect();
            // The prompt is already with claude, so a failed save is logged.
            match self
                .store
                .update(&id, |session| session.title = Some(title.clone()))
            {
                Ok(()) => notify(
                    connection,
                    &id,
                    SessionUpdate::SessionInfoUpdate(acp::SessionInfoUpdate::new().title(title)),
                ),
                Err(error) => eprintln!("failed to save session {id}: {error}"),
            }
        }
        Ok(())
    }

    /// What a session's claude calls when a turn ends: it saves the Claude
    /// session ID and tells the client the session was updated.
    fn turn_recorder(
        self: &Arc<Self>,
        id: &str,
        cwd: &std::path::Path,
        connection: &ConnectionTo<Client>,
    ) -> Arc<dyn Fn(TurnEnd) + Send + Sync> {
        let bridge = self.clone();
        let id = id.to_owned();
        let cwd = cwd.to_owned();
        let connection = connection.clone();
        Arc::new(move |end| {
            // A turn that did not finish only keeps its Claude session when
            // Claude saved a transcript that `--resume` can continue.
            let claude_id = end
                .session_id
                .filter(|claude_id| end.finished || bridge.history.find(claude_id, &cwd));
            if let Some(claude_id) = claude_id
                && let Err(error) = bridge.store.update(&id, |session| {
                    session.claude_session_id = Some(claude_id);
                })
            {
                eprintln!("failed to save session {id}: {error}");
            }
            if !end.finished {
                return;
            }
            match bridge.store.get(&id) {
                Ok(Some(session)) => notify(
                    &connection,
                    &id,
                    SessionUpdate::SessionInfoUpdate(
                        acp::SessionInfoUpdate::new().updated_at(session.updated_at),
                    ),
                ),
                Ok(None) => {}
                Err(error) => eprintln!("failed to read session {id}: {error}"),
            }
        })
    }

    /// A session opened on this connection.
    fn require(&self, id: &str) -> std::result::Result<Session, Error> {
        self.store
            .get(id)
            .map_err(store_error)?
            .filter(|_| self.active.lock().unwrap().contains(id))
            .ok_or_else(|| acp::Error::session_not_found(id.to_owned()).into())
    }

    fn options(session: &Session) -> Vec<SessionConfigOption> {
        let mode = session_mode(session);
        let model = session_model(session);
        let effort = session_effort(session);
        vec![
            SessionConfigOption::select("model", "Model", model.to_owned(), model_options())
                .category(SessionConfigOptionCategory::Model),
            SessionConfigOption::select("effort", "Effort", effort.to_owned(), effort_options())
                .category(SessionConfigOptionCategory::ThoughtLevel),
            SessionConfigOption::select("mode", "Mode", mode.to_owned(), mode_options())
                .category(SessionConfigOptionCategory::Mode),
        ]
    }
}

/// The models a session can use, by id and name.
const MODELS: [(&str, &str); 4] = [
    ("claude-fable-5-2", "Fable 5.2"),
    ("claude-opus-5-5", "Opus 5.5"),
    ("claude-sonnet-5-5", "Sonnet 5.5"),
    ("claude-haiku-5-5", "Haiku 5.5"),
];

fn is_model(value: &str) -> bool {
    MODELS.iter().any(|(id, _)| *id == value)
}

fn session_model(session: &Session) -> &str {
    session
        .model
        .as_deref()
        .filter(|model| is_model(model))
        .unwrap_or("claude-opus-5-5")
}

fn model_options() -> Vec<SessionConfigSelectOption> {
    MODELS
        .into_iter()
        .map(|(id, name)| SessionConfigSelectOption::new(id, name))
        .collect()
}

/// The effort levels a session can use, by id and name.
const EFFORTS: [(&str, &str); 4] = [
    ("low", "Low"),
    ("medium", "Medium"),
    ("high", "High"),
    ("xhigh", "XHigh"),
];

fn is_effort(value: &str) -> bool {
    EFFORTS.iter().any(|(id, _)| *id == value)
}

fn session_effort(session: &Session) -> &str {
    session
        .effort
        .as_deref()
        .filter(|effort| is_effort(effort))
        .unwrap_or("high")
}

fn effort_options() -> Vec<SessionConfigSelectOption> {
    EFFORTS
        .into_iter()
        .map(|(id, name)| SessionConfigSelectOption::new(id, name))
        .collect()
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
        .map(|server| {
            let acp::McpServer::Stdio(server) = server else {
                return Err(invalid("only stdio MCP servers are supported"));
            };
            validation::mcp_command(&server.command.0).map_err(invalid)?;
            Ok(McpServer {
                name: server.name,
                command: server.command.0.to_string_lossy().into_owned(),
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

fn invalid(message: impl Into<String>) -> Error {
    Error::new(-32602, message)
}

#[expect(
    clippy::needless_pass_by_value,
    reason = "`map_err` passes the error by value"
)]
fn store_error(error: io::Error) -> Error {
    Error::new(-32603, format!("session registry: {error}"))
}
