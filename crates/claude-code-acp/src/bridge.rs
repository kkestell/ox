use std::{
    collections::HashMap,
    future::Future,
    sync::{Arc, Mutex},
};

use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    self as acp, SessionConfigOption, SessionConfigOptionCategory, SessionConfigSelectOption,
    SessionUpdate, StopReason,
};
use agent_client_protocol::{Client, Error};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use tokio::sync::oneshot;
use uuid::Uuid;

use crate::{
    config::Config,
    content,
    history::{History, HistoryEntry},
    runner::{Claude, Finish, McpServer, Refused, RunError, RunOutcome, StartRequest},
    store::{Session, Store},
    updates::{Updates, error_message, notify},
    validation,
};

/// A prompt sent to its session's claude.
struct Turn {
    id: String,
    session: Session,
    /// The receiver of the turn's outcome, or why claude could not start.
    outcome: Result<oneshot::Receiver<RunOutcome>, String>,
}

/// Held until a prompt's response is sent; claude's output after the turn
/// waits for it to drop.
pub type Responding = Option<oneshot::Sender<()>>;

/// A config option to change.
enum Setting {
    Mode(String),
    Model(String),
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
    /// The claude of each session that has taken a prompt.
    claudes: Mutex<HashMap<String, Claude>>,
}

impl Bridge {
    pub fn new(config: Config) -> Arc<Self> {
        Arc::new(Self {
            history: History::new(&config.claude_dir),
            store: Store::new(config.store_dir),
            claudes: Mutex::new(HashMap::new()),
        })
    }

    pub fn initialize(request: &acp::InitializeRequest) -> acp::InitializeResponse {
        acp::InitializeResponse::new(request.protocol_version)
            .agent_info(
                acp::Implementation::new("claude-code-acp", env!("CARGO_PKG_VERSION"))
                    .title("Claude Code ACP Bridge"),
            )
            .agent_capabilities(
                acp::AgentCapabilities::new()
                    .load_session(true)
                    .prompt_capabilities(
                        acp::PromptCapabilities::new()
                            .image(true)
                            .embedded_context(true),
                    )
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
        self.store.create(id.clone(), &session);
        Ok(acp::NewSessionResponse::new(id).config_options(Self::options(&session)))
    }

    /// Opens a session and replays its conversation from Claude's
    /// transcript before answering, as `session/load` requires.
    pub fn load_session(
        &self,
        request: acp::LoadSessionRequest,
        connection: &ConnectionTo<Client>,
    ) -> std::result::Result<acp::LoadSessionResponse, Error> {
        let id = request.session_id.to_string();
        let session = self.open(&id, &request.cwd, request.mcp_servers)?;
        if let Some(claude_id) = &session.claude_session_id {
            Updates::new(connection.clone(), &id, &session.cwd)
                .replay(self.history.messages(claude_id, &session.cwd));
        }
        Ok(acp::LoadSessionResponse::new().config_options(Self::options(&session)))
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
        let session = self.open(&id, &request.cwd, request.mcp_servers)?;
        Ok(acp::ResumeSessionResponse::new().config_options(Self::options(&session)))
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
        if let Some(session) = self.store.get(id) {
            if session.cwd != cwd {
                return Err(invalid("Session cwd does not match"));
            }
            if !servers.is_empty() {
                self.store
                    .update(id, |session| session.mcp_servers = servers);
                self.claudes.lock().unwrap().remove(id);
            }
        } else if self.history.find(id, &cwd) {
            let mut session = Session::new(cwd, servers);
            session.claude_session_id = Some(id.to_owned());
            self.store.create(id.to_owned(), &session);
        } else {
            return Err(Error::new(
                -32002,
                format!("Claude Code session {id} not found"),
            ));
        }
        self.require(id)
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
        let value = request
            .value
            .as_value_id()
            .ok_or_else(|| invalid("Expected a select value"))?
            .to_string();
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
        self.store.update(&id, |session| match setting {
            Setting::Mode(mode) => session.mode = Some(mode),
            Setting::Model(model) => session.model = Some(model),
        });
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

    /// Sends a prompt to its session's claude and returns the future that
    /// waits for its turn. The prompt is sent before the future is first
    /// polled, so a cancel that arrives right after the prompt stops this
    /// turn. The future also returns what to drop once the response is sent.
    pub fn prompt(
        self: &Arc<Self>,
        request: &acp::PromptRequest,
        connection: ConnectionTo<Client>,
    ) -> impl Future<Output = (std::result::Result<acp::PromptResponse, Error>, Responding)>
    + Send
    + 'static {
        let turn = self.start_turn(request, &connection);
        let bridge = self.clone();
        async move {
            match turn {
                Ok(turn) => {
                    let (response, responding) = bridge.finish_turn(turn, &connection).await;
                    (Ok(response), responding)
                }
                Err(error) => (Err(error), None),
            }
        }
    }

    fn start_turn(
        &self,
        request: &acp::PromptRequest,
        connection: &ConnectionTo<Client>,
    ) -> std::result::Result<Turn, Error> {
        let id = request.session_id.to_string();
        let session = self.require(&id)?;
        let content = content::claude_content(&request.prompt).map_err(invalid)?;
        let text = content::prompt_text(&request.prompt);

        let mut claudes = self.claudes.lock().unwrap();
        let outcome = match claudes.get(&id).map(|claude| claude.prompt(&content)) {
            Some(Ok(outcome)) => Ok(outcome),
            Some(Err(Refused::Busy)) => return Err(invalid(Refused::Busy.to_string())),
            // The session's claude has exited or it has none yet, so the
            // prompt goes to a new one, which continues the conversation.
            Some(Err(Refused::Exited(_))) | None => Claude::start(
                &StartRequest {
                    cwd: &session.cwd,
                    resume: session.claude_session_id.as_deref(),
                    model: session_model(&session),
                    mode: session_mode(&session),
                    mcp_servers: &session.mcp_servers,
                },
                Updates::new(connection.clone(), &id, &session.cwd),
            )
            .and_then(|claude| {
                let outcome = claude.prompt(&content);
                claudes.insert(id.clone(), claude);
                outcome.map_err(|refused| refused.to_string())
            }),
        };
        drop(claudes);

        if session.title.is_none() && !text.trim().is_empty() {
            let title: String = text
                .split_whitespace()
                .collect::<Vec<_>>()
                .join(" ")
                .chars()
                .take(120)
                .collect();
            self.store
                .update(&id, |session| session.title = Some(title.clone()));
            notify(
                connection,
                &id,
                SessionUpdate::SessionInfoUpdate(acp::SessionInfoUpdate::new().title(title)),
            );
        }
        Ok(Turn {
            id,
            session,
            outcome,
        })
    }

    async fn finish_turn(
        &self,
        turn: Turn,
        connection: &ConnectionTo<Client>,
    ) -> (acp::PromptResponse, Responding) {
        let Turn {
            id,
            session,
            outcome,
        } = turn;
        let id = id.as_str();
        let failed = |error: String| RunOutcome {
            session_id: None,
            result: Err(RunError::Failed(error)),
            responding: None,
        };
        let outcome = match outcome {
            Ok(outcome) => outcome
                .await
                .unwrap_or_else(|_| failed("claude stopped".into())),
            Err(error) => failed(error),
        };
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
                        connection,
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
            Err(RunError::Cancelled) => StopReason::Cancelled,
            Err(RunError::Failed(error)) => {
                notify(connection, id, error_message(&error));
                StopReason::EndTurn
            }
        };
        (acp::PromptResponse::new(stop_reason), outcome.responding)
    }

    fn require(&self, id: &str) -> std::result::Result<Session, Error> {
        self.store
            .get(id)
            .ok_or_else(|| Error::new(-32002, format!("Session {id} not found")))
    }

    fn options(session: &Session) -> Vec<SessionConfigOption> {
        let mode = session_mode(session);
        let model = session_model(session);
        vec![
            SessionConfigOption::select("model", "Model", model.to_owned(), model_options())
                .category(SessionConfigOptionCategory::Model),
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

fn invalid(message: impl Into<String>) -> Error {
    Error::new(-32602, message)
}
