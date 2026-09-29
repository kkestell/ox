//! The fake server: a scripted ACP server for testing ox without a model
//! provider or ox-acp. Its binary serves terminal tests; its library serves ACP tests.

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    AgentCapabilities, AvailableCommand, AvailableCommandsUpdate, CancelNotification,
    CloseSessionRequest, CloseSessionResponse, ConfigOptionUpdate, ContentBlock, ContentChunk,
    Cost, DeleteSessionRequest, DeleteSessionResponse, Diff, InitializeRequest, InitializeResponse,
    ListSessionsRequest, ListSessionsResponse, LoadSessionRequest, LoadSessionResponse,
    NewSessionRequest, NewSessionResponse, PermissionOption, PermissionOptionKind,
    PromptCapabilities, PromptRequest, PromptResponse, RequestPermissionOutcome,
    RequestPermissionRequest, SessionCapabilities, SessionCloseCapabilities, SessionConfigOption,
    SessionConfigOptionCategory, SessionConfigOptionValue, SessionConfigSelectOption,
    SessionDeleteCapabilities, SessionId, SessionInfo, SessionInfoUpdate, SessionListCapabilities,
    SessionNotification, SessionUpdate, SetSessionConfigOptionRequest,
    SetSessionConfigOptionResponse, StopReason, ToolCall, ToolCallContent, ToolCallStatus,
    ToolCallUpdate, ToolCallUpdateFields, ToolKind, UsageUpdate,
};
use agent_client_protocol::{
    Agent, Client, ConnectTo, ConnectionTo, on_receive_notification, on_receive_request,
};
use serde::{Deserialize, Serialize};
use tokio::sync::Notify;

/// Ends a running `hold` script, or the next one if none is running.
#[derive(Clone, Default)]
pub struct Hold(Arc<Notify>);

impl Hold {
    pub fn release(&self) {
        self.0.notify_one();
    }
}

/// Pauses a selected fake-server request until the test releases it.
#[derive(Clone, Default)]
pub struct Pending {
    enabled: Arc<AtomicBool>,
    entered: Arc<Notify>,
    release: Arc<Notify>,
}

impl Pending {
    pub fn pause(&self) {
        self.enabled.store(true, Ordering::SeqCst);
    }

    pub async fn wait_started(&self) {
        self.entered.notified().await;
    }

    pub fn release(&self) {
        self.enabled.store(false, Ordering::SeqCst);
        self.release.notify_one();
    }

    async fn wait_if_paused(&self) {
        if self.enabled.load(Ordering::SeqCst) {
            self.entered.notify_one();
            self.release.notified().await;
        }
    }
}

/// The fake server's saved sessions: every session it created, with its `cwd`,
/// session title, and the updates to replay. Clones share it, so saved
/// sessions outlive one ACP connection.
#[derive(Clone)]
pub struct SavedSessions(Arc<Mutex<Saved>>, Arc<RequestHolds>);

#[derive(Default)]
struct RequestHolds {
    load: Pending,
    delete: Pending,
}

#[derive(Serialize, Deserialize)]
struct Saved {
    /// The file the saved sessions is written to after every change, if any.
    #[serde(skip)]
    file: Option<PathBuf>,
    /// Whether the fake server advertises saved-session operations.
    advertised: bool,
    image: bool,
    delete: bool,
    /// The number of sessions created so far, so IDs are never reused.
    created: u32,
    sessions: Vec<SavedSession>,
}

#[derive(Serialize, Deserialize)]
struct SavedSession {
    id: SessionId,
    cwd: PathBuf,
    title: Option<String>,
    #[serde(default)]
    updated_at: Option<String>,
    /// Every update sent for the session, and a `user_message_chunk` for each
    /// prompt's text.
    updates: Vec<SessionUpdate>,
    /// Set by the `unloadable` script.
    unloadable: bool,
    /// The current value of the `pace` config option: `steady` or `brisk`.
    pace: String,
    mode: String,
    model: String,
    effort: String,
}

/// The values of the `pace` config option.
const PACES: [(&str, &str); 2] = [("steady", "Steady"), ("brisk", "Brisk")];

/// The values of the `model` config option, with the prices and context limit
/// ox-acp sends in each choice's `_meta`.
const MODELS: [(&str, &str, f64, f64, u64); 2] = [
    (
        "deepseek",
        "DeepSeek: DeepSeek Reasoner",
        0.28,
        0.42,
        131_072,
    ),
    ("gemma", "Google: Gemma Vision", 0.04, 0.08, 32_768),
];

/// The fake server's pace, mode, and model options. The second model is
/// described as accepting images, as ox-acp describes such models.
fn config_options(pace: &str, mode: &str, model: &str, effort: &str) -> Vec<SessionConfigOption> {
    let values: Vec<_> = PACES
        .iter()
        .map(|(value, name)| SessionConfigSelectOption::new(*value, *name))
        .collect();
    let models: Vec<_> = MODELS
        .iter()
        .map(|(value, name, input, output, context)| {
            let meta = serde_json::Map::from_iter([
                ("inputPrice".to_owned(), (*input).into()),
                ("outputPrice".to_owned(), (*output).into()),
                ("contextLimit".to_owned(), (*context).into()),
            ]);
            let choice = SessionConfigSelectOption::new(*value, *name).meta(meta);
            if *value == "gemma" {
                choice.description("Accepts images")
            } else {
                choice
            }
        })
        .collect();
    vec![
        SessionConfigOption::select("pace", "Pace", pace.to_string(), values),
        SessionConfigOption::select(
            "mode",
            "Mode",
            mode.to_string(),
            vec![
                SessionConfigSelectOption::new("ask", "Ask"),
                SessionConfigSelectOption::new("auto", "Auto"),
            ],
        )
        .category(SessionConfigOptionCategory::Mode),
        SessionConfigOption::select("model", "Model", model.to_string(), models)
            .category(SessionConfigOptionCategory::Model),
        SessionConfigOption::select(
            "effort",
            "Effort",
            effort.to_string(),
            vec![
                SessionConfigSelectOption::new("low", "Low"),
                SessionConfigSelectOption::new("high", "High"),
            ],
        )
        .category(SessionConfigOptionCategory::ThoughtLevel),
    ]
}

impl Default for SavedSessions {
    /// Saved sessions with load, list, close, and delete
    /// advertised.
    fn default() -> SavedSessions {
        SavedSessions::new(true)
    }
}

impl SavedSessions {
    /// Saved sessions without load, list, close, or delete. Those methods answer
    /// method not found.
    pub fn unadvertised() -> SavedSessions {
        SavedSessions::new(false)
    }

    /// Saved sessions, advertised, read from `file` when it exists and written
    /// to it after every change, so it outlives the fake server process.
    pub fn file(file: PathBuf) -> SavedSessions {
        let mut saved = match std::fs::read(&file) {
            Ok(json) => serde_json::from_slice(&json).expect("the saved sessions file is valid"),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Saved {
                file: None,
                advertised: true,
                image: true,
                delete: true,
                created: 0,
                sessions: Vec::new(),
            },
            Err(error) => panic!("reading {}: {error}", file.display()),
        };
        saved.file = Some(file);
        SavedSessions(Arc::new(Mutex::new(saved)), Arc::default())
    }

    fn new(advertised: bool) -> SavedSessions {
        SavedSessions(
            Arc::new(Mutex::new(Saved {
                file: None,
                advertised,
                image: true,
                delete: advertised,
                created: 0,
                sessions: Vec::new(),
            })),
            Arc::default(),
        )
    }

    pub fn hold_load(&self) -> Pending {
        self.1.load.clone()
    }

    pub fn hold_delete(&self) -> Pending {
        self.1.delete.clone()
    }

    /// Shares saved sessions while giving another connection independent request holds.
    pub fn with_new_holds(&self) -> SavedSessions {
        SavedSessions(self.0.clone(), Arc::default())
    }

    fn advertised(&self) -> bool {
        self.0.lock().unwrap().advertised
    }

    /// Selects independently advertised image and delete capabilities for tests.
    pub fn capabilities(&self, image: bool, delete: bool) {
        self.edit(|saved| {
            saved.image = image;
            saved.delete = delete;
        });
    }

    fn image(&self) -> bool {
        self.0.lock().unwrap().image
    }
    fn can_delete(&self) -> bool {
        self.0.lock().unwrap().delete
    }

    /// Runs `f` on the saved sessions, then writes it to its file, if any.
    fn edit<R>(&self, f: impl FnOnce(&mut Saved) -> R) -> R {
        let mut saved = self.0.lock().unwrap();
        let result = f(&mut saved);
        if let Some(file) = &saved.file {
            let json = serde_json::to_vec(&*saved).expect("the saved sessions serializes");
            std::fs::write(file, json).expect("the saved sessions file is writable");
        }
        result
    }

    fn with_session<R>(&self, id: &SessionId, f: impl FnOnce(&mut SavedSession) -> R) -> Option<R> {
        self.edit(|saved| {
            saved
                .sessions
                .iter_mut()
                .find(|session| session.id == *id)
                .map(f)
        })
    }

    fn save(&self, id: &SessionId, update: SessionUpdate) {
        self.with_session(id, |session| session.updates.push(update));
    }
}

/// The fake server. It advertises protocol version 1, image prompts, and,
/// unless `saved_sessions` is unadvertised, load, list, close, and delete. It names
/// sessions `fake-1`, `fake-2`, and so on, and
/// sends an `available_commands_update` right after each `session/new`
/// response. Every session has the select config options `pace`, `mode`, and
/// `model`, which `session/new` and `session/load` return. `session/list` returns one session
/// per page. `session/prompt` answers an error for a session not created or
/// loaded during this ACP connection, and otherwise runs the script its text
/// blocks name: `hold`, `tool`, `tools`, `reject`, `fail`, `title`,
/// `unloadable`, `pace`, `options`, `usage`, `render`, or anything else for a reply.
pub fn fake_server(hold: Hold, saved_sessions: SavedSessions) -> impl ConnectTo<Client> {
    // The sessions created or loaded during this ACP connection.
    let loaded = Arc::new(Mutex::new(HashSet::<SessionId>::new()));
    let cancelled = Arc::new(Mutex::new(HashMap::<SessionId, Arc<Notify>>::new()));
    Agent
        .builder()
        .on_receive_notification(
            {
                let cancelled = cancelled.clone();
                async move |request: CancelNotification, _connection| {
                    if let Some(notify) = cancelled.lock().unwrap().get(&request.session_id) {
                        notify.notify_one();
                    }
                    Ok(())
                }
            },
            on_receive_notification!(),
        )
        .name("ox-fake-server")
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                async move |_: InitializeRequest, responder, _connection| {
                    let mut capabilities = AgentCapabilities::new().prompt_capabilities(
                        PromptCapabilities::new().image(saved_sessions.image()),
                    );
                    if saved_sessions.advertised() {
                        let mut sessions = SessionCapabilities::new()
                            .list(SessionListCapabilities::new())
                            .close(SessionCloseCapabilities::new());
                        if saved_sessions.can_delete() {
                            sessions = sessions.delete(SessionDeleteCapabilities::new());
                        }
                        capabilities = capabilities
                            .load_session(true)
                            .session_capabilities(sessions);
                    }
                    responder.respond(
                        InitializeResponse::new(ProtocolVersion::V1)
                            .agent_capabilities(capabilities),
                    )
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                let loaded = loaded.clone();
                async move |request: NewSessionRequest,
                            responder,
                            connection: ConnectionTo<Client>| {
                    let session = saved_sessions.edit(|saved| {
                        saved.created += 1;
                        let session = SessionId::from(format!("fake-{}", saved.created));
                        saved.sessions.push(SavedSession {
                            id: session.clone(),
                            cwd: request.cwd,
                            title: None,
                            updated_at: Some(format!(
                                "2026-09-{:02}T12:00:00Z",
                                saved.created.min(28)
                            )),
                            updates: Vec::new(),
                            unloadable: false,
                            pace: "steady".to_string(),
                            mode: "ask".to_string(),
                            model: "deepseek".to_string(),
                            effort: "low".to_string(),
                        });
                        session
                    });
                    loaded.lock().unwrap().insert(session.clone());
                    responder.respond(
                        NewSessionResponse::new(session.clone())
                            .config_options(config_options("steady", "ask", "deepseek", "low")),
                    )?;
                    let command = AvailableCommand::new("tally", "count the tallies");
                    let update =
                        SessionUpdate::AvailableCommandsUpdate(AvailableCommandsUpdate::new(vec![
                            command,
                        ]));
                    saved_sessions.save(&session, update.clone());
                    connection.send_notification(SessionNotification::new(session, update))
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                async move |request: ListSessionsRequest, responder, _connection| {
                    if !saved_sessions.advertised() {
                        return responder
                            .respond_with_error(agent_client_protocol::Error::method_not_found());
                    }
                    let saved = saved_sessions.0.lock().unwrap();
                    let matching: Vec<_> = saved
                        .sessions
                        .iter()
                        .filter(|session| {
                            request.cwd.as_ref().is_none_or(|cwd| session.cwd == *cwd)
                        })
                        .collect();
                    let index = match request.cursor.as_deref().map(str::parse::<usize>) {
                        None => 0,
                        Some(Ok(index)) => index,
                        Some(Err(_)) => {
                            return responder.respond_with_error(
                                agent_client_protocol::Error::invalid_params(),
                            );
                        }
                    };
                    let page = matching
                        .get(index)
                        .map(|session| {
                            SessionInfo::new(session.id.clone(), session.cwd.clone())
                                .title(session.title.clone())
                                .updated_at(session.updated_at.clone())
                        })
                        .into_iter()
                        .collect();
                    let next = (index + 1 < matching.len()).then(|| (index + 1).to_string());
                    responder.respond(ListSessionsResponse::new(page).next_cursor(next))
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                let loaded = loaded.clone();
                async move |request: LoadSessionRequest,
                            responder,
                            connection: ConnectionTo<Client>| {
                    saved_sessions.1.load.wait_if_paused().await;
                    if !saved_sessions.advertised() {
                        return responder
                            .respond_with_error(agent_client_protocol::Error::method_not_found());
                    }
                    let session = request.session_id;
                    let replay = saved_sessions.with_session(&session, |saved| {
                        (
                            saved.unloadable,
                            saved.updates.clone(),
                            saved.pace.clone(),
                            saved.mode.clone(),
                            saved.model.clone(),
                            saved.effort.clone(),
                        )
                    });
                    let (updates, pace, mode, model, effort) = match replay {
                        None => {
                            return responder
                                .respond_with_internal_error(format!("no session {session}"));
                        }
                        Some((true, ..)) => {
                            return responder.respond_with_internal_error(
                                "the fake server cannot load this session",
                            );
                        }
                        Some((false, updates, pace, mode, model, effort)) => {
                            (updates, pace, mode, model, effort)
                        }
                    };
                    for update in updates {
                        connection
                            .send_notification(SessionNotification::new(session.clone(), update))?;
                    }
                    loaded.lock().unwrap().insert(session);
                    responder.respond(
                        LoadSessionResponse::new()
                            .config_options(config_options(&pace, &mode, &model, &effort)),
                    )
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                async move |request: SetSessionConfigOptionRequest, responder, _connection| {
                    let value = match (&*request.config_id.0, &request.value) {
                        ("pace", SessionConfigOptionValue::ValueId { value })
                            if PACES.iter().any(|(pace, _)| **pace == *value.0) =>
                        {
                            value.0.to_string()
                        }
                        ("mode", SessionConfigOptionValue::ValueId { value })
                            if matches!(&*value.0, "ask" | "auto") =>
                        {
                            value.0.to_string()
                        }
                        ("model", SessionConfigOptionValue::ValueId { value })
                            if MODELS.iter().any(|(model, ..)| **model == *value.0) =>
                        {
                            value.0.to_string()
                        }
                        ("effort", SessionConfigOptionValue::ValueId { value })
                            if matches!(&*value.0, "low" | "high") =>
                        {
                            value.0.to_string()
                        }
                        _ => {
                            return responder.respond_with_error(
                                agent_client_protocol::Error::invalid_params(),
                            );
                        }
                    };
                    let set = saved_sessions.with_session(&request.session_id, |saved| {
                        match &*request.config_id.0 {
                            "pace" => saved.pace = value,
                            "mode" => saved.mode = value,
                            "effort" => saved.effort = value,
                            _ => saved.model = value,
                        }
                        (
                            saved.pace.clone(),
                            saved.mode.clone(),
                            saved.model.clone(),
                            saved.effort.clone(),
                        )
                    });
                    let Some((pace, mode, model, effort)) = set else {
                        return responder.respond_with_internal_error(format!(
                            "no session {}",
                            request.session_id
                        ));
                    };
                    responder.respond(SetSessionConfigOptionResponse::new(config_options(
                        &pace, &mode, &model, &effort,
                    )))
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                let loaded = loaded.clone();
                async move |request: CloseSessionRequest, responder, _connection| {
                    if !saved_sessions.advertised() {
                        return responder
                            .respond_with_error(agent_client_protocol::Error::method_not_found());
                    }
                    if !loaded.lock().unwrap().remove(&request.session_id) {
                        return responder.respond_with_internal_error(format!(
                            "session {} is not loaded",
                            request.session_id
                        ));
                    }
                    responder.respond(CloseSessionResponse::new())
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            {
                let saved_sessions = saved_sessions.clone();
                let loaded = loaded.clone();
                async move |request: DeleteSessionRequest, responder, _connection| {
                    saved_sessions.1.delete.wait_if_paused().await;
                    if !saved_sessions.advertised() {
                        return responder
                            .respond_with_error(agent_client_protocol::Error::method_not_found());
                    }
                    let session = request.session_id;
                    saved_sessions.edit(|saved| saved.sessions.retain(|saved| saved.id != session));
                    loaded.lock().unwrap().remove(&session);
                    responder.respond(DeleteSessionResponse::new())
                }
            },
            on_receive_request!(),
        )
        .on_receive_request(
            async move |request: PromptRequest, responder, connection: ConnectionTo<Client>| {
                let mut text = String::new();
                let mut images = 0;
                for block in &request.prompt {
                    match block {
                        ContentBlock::Text(block) => text.push_str(&block.text),
                        ContentBlock::Image(_) => images += 1,
                        other => {
                            return responder.respond_with_internal_error(format!(
                                "expected text and image blocks: {other:?}"
                            ));
                        }
                    }
                }
                let session = request.session_id;
                if !loaded.lock().unwrap().contains(&session) {
                    return responder
                        .respond_with_internal_error(format!("session {session} is not loaded"));
                }
                // Save user content for replay without echoing it to the client.
                for block in request.prompt {
                    saved_sessions.save(
                        &session,
                        SessionUpdate::UserMessageChunk(ContentChunk::new(block)),
                    );
                }
                // Scripts wait for a permission answer or the hold, so they run
                // outside the dispatch loop.
                let cancellation = Arc::new(Notify::new());
                if text == "running" {
                    cancelled
                        .lock()
                        .unwrap()
                        .insert(session.clone(), cancellation.clone());
                }
                let cancelled = cancelled.clone();
                let hold = hold.clone();
                let saved_sessions = saved_sessions.clone();
                connection.spawn({
                    let connection = connection.clone();
                    async move {
                        let script = Script {
                            connection,
                            session,
                            saved_sessions,
                        };
                        let stop = match text.as_str() {
                            "running" => {
                                script.message("running; waiting for cancellation")?;
                                cancellation.notified().await;
                                cancelled.lock().unwrap().remove(&script.session);
                                script.message("cancelled")?;
                                StopReason::Cancelled
                            }
                            "stream" => {
                                for text in ["stream ", "arrives ", "in order\n"] {
                                    script.message(text)?;
                                    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
                                }
                                StopReason::EndTurn
                            }
                            "exit" => {
                                return Err(agent_client_protocol::Error::internal_error()
                                    .data("scripted server exit"));
                            }
                            "hold" => script.hold(&hold).await?,
                            "tool" => script.tool().await?,
                            "tools" => script.tools().await?,
                            "reject" => {
                                return responder.respond_with_internal_error(
                                    "the fake server rejects this prompt",
                                );
                            }
                            "fail" => {
                                script.message("failing")?;
                                return responder
                                    .respond_with_internal_error("the fake server failed");
                            }
                            "title" => script.title()?,
                            "unloadable" => script.unloadable()?,
                            "render" => script.render()?,
                            "pace" => script.pace()?,
                            "options" => script.options()?,
                            "usage" => script.usage()?,
                            _ => script.reply(&text, images)?,
                        };
                        responder.respond(PromptResponse::new(stop))
                    }
                })
            },
            on_receive_request!(),
        )
}

/// One prompt's script.
struct Script {
    connection: ConnectionTo<Client>,
    session: SessionId,
    saved_sessions: SavedSessions,
}

impl Script {
    async fn hold(&self, hold: &Hold) -> agent_client_protocol::Result<StopReason> {
        self.message("holding")?;
        hold.0.notified().await;
        self.message("released")?;
        Ok(StopReason::EndTurn)
    }

    /// Asks permission for one tool call. A selected option completes the
    /// tool call, and an error fails it.
    async fn tool(&self) -> agent_client_protocol::Result<StopReason> {
        let (status, message) = match self.ask("tally-1").await {
            Ok(RequestPermissionOutcome::Selected(selected)) => (
                ToolCallStatus::Completed,
                format!("selected {}", selected.option_id),
            ),
            Ok(RequestPermissionOutcome::Cancelled) => return Ok(StopReason::Cancelled),
            other => (ToolCallStatus::Failed, outcome(&other)),
        };
        self.update(SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
            "tally-1",
            ToolCallUpdateFields::new().status(status),
        )))?;
        self.message(&message)?;
        Ok(StopReason::EndTurn)
    }

    /// Asks permission for two tool calls at once. If either is cancelled, it
    /// asks for a third, which a well-behaved server would not, so tests can
    /// check that the ACP client answers it `Cancelled`. Then it sends one message
    /// with each outcome, such as `tally-1: go, tally-2: cancelled`.
    async fn tools(&self) -> agent_client_protocol::Result<StopReason> {
        let (first, second) = tokio::join!(self.ask("tally-1"), self.ask("tally-2"));
        let mut outcomes = vec![("tally-1", first), ("tally-2", second)];
        let cancelled = |outcomes: &[(&str, _)]| {
            outcomes
                .iter()
                .any(|(_, outcome)| matches!(outcome, Ok(RequestPermissionOutcome::Cancelled)))
        };
        if cancelled(&outcomes) {
            outcomes.push(("tally-3", self.ask("tally-3").await));
        }
        let text: Vec<_> = outcomes
            .iter()
            .map(|(id, result)| format!("{id}: {}", outcome(result)))
            .collect();
        self.message(&text.join(", "))?;
        Ok(if cancelled(&outcomes) {
            StopReason::Cancelled
        } else {
            StopReason::EndTurn
        })
    }

    /// Sends a tool call with content and asks permission for it, with the
    /// options `go` and `stop`. The request carries no content of its own.
    async fn ask(
        &self,
        tool_call_id: &str,
    ) -> agent_client_protocol::Result<RequestPermissionOutcome> {
        let tool_call = ToolCall::new(tool_call_id.to_string(), "count the tallies")
            .kind(ToolKind::Search)
            .raw_input(serde_json::json!({ "glob": "*.tally" }))
            .content(vec![ToolCallContent::from("every *.tally file")]);
        self.update(SessionUpdate::ToolCall(tool_call))?;
        let request = RequestPermissionRequest::new(
            self.session.clone(),
            ToolCallUpdate::new(tool_call_id.to_string(), ToolCallUpdateFields::new()),
            vec![
                PermissionOption::new("go", "Go ahead", PermissionOptionKind::AllowOnce),
                PermissionOption::new("stop", "Hold off", PermissionOptionKind::RejectOnce),
            ],
        );
        let response = self.connection.send_request(request).block_task().await?;
        Ok(response.outcome)
    }

    /// Sets the session title `tallies`.
    fn title(&self) -> agent_client_protocol::Result<StopReason> {
        self.saved_sessions.with_session(&self.session, |saved| {
            saved.title = Some("tallies".to_string());
        });
        self.update(SessionUpdate::SessionInfoUpdate(
            SessionInfoUpdate::new().title("tallies".to_string()),
        ))?;
        Ok(StopReason::EndTurn)
    }

    /// Replies, then makes every later `session/load` of the session fail.
    fn unloadable(&self) -> agent_client_protocol::Result<StopReason> {
        self.saved_sessions
            .with_session(&self.session, |saved| saved.unloadable = true);
        self.reply("unloadable", 0)
    }

    /// Sets `pace` to `brisk` with a `config_option_update`.
    fn pace(&self) -> agent_client_protocol::Result<StopReason> {
        let (mode, model, effort) = self
            .saved_sessions
            .with_session(&self.session, |saved| {
                saved.pace = "brisk".to_string();
                (
                    saved.mode.clone(),
                    saved.model.clone(),
                    saved.effort.clone(),
                )
            })
            .expect("session exists");
        self.update(SessionUpdate::ConfigOptionUpdate(ConfigOptionUpdate::new(
            config_options("brisk", &mode, &model, &effort),
        )))?;
        Ok(StopReason::EndTurn)
    }

    /// Sends the model, effort, and mode options, each with its category,
    /// plus `pace` without one.
    fn options(&self) -> agent_client_protocol::Result<StopReason> {
        let [pace, _, model, _] = config_options("steady", "ask", "deepseek", "low")
            .try_into()
            .expect("four options");
        let mut options = vec![
            model,
            SessionConfigOption::select(
                "effort",
                "Effort",
                "high".to_string(),
                vec![
                    SessionConfigSelectOption::new("low", "Low"),
                    SessionConfigSelectOption::new("high", "High"),
                ],
            )
            .category(SessionConfigOptionCategory::ThoughtLevel),
        ];
        options.push(pace);
        options.push(
            SessionConfigOption::select(
                "approval",
                "Approval",
                "auto".to_string(),
                vec![SessionConfigSelectOption::new("auto", "Auto")],
            )
            .category(SessionConfigOptionCategory::Mode),
        );
        self.update(SessionUpdate::ConfigOptionUpdate(ConfigOptionUpdate::new(
            options,
        )))?;
        Ok(StopReason::EndTurn)
    }

    /// Reports 1200 of 8000 tokens used, at a cost of 0.25 USD.
    fn usage(&self) -> agent_client_protocol::Result<StopReason> {
        self.update(SessionUpdate::UsageUpdate(
            UsageUpdate::new(1200, 8000).cost(Cost::new(0.25, "USD")),
        ))?;
        Ok(StopReason::EndTurn)
    }

    /// Streams reasoning and a reply with surrounding whitespace and words
    /// split across chunks. Between them, it sends four tool calls with raw
    /// names and JSON arguments, each updated to in progress and then to
    /// completed with content, the last with a diff block, and one completed
    /// tool call without a raw name, as a subagent's answer arrives.
    fn render(&self) -> agent_client_protocol::Result<StopReason> {
        for text in ["  \n weigh", "ing the ", "tallies  \n"] {
            self.update(SessionUpdate::AgentThoughtChunk(ContentChunk::new(
                text.into(),
            )))?;
        }
        for (id, title, kind, name, input, content) in [
            (
                "run-1",
                "ls",
                ToolKind::Execute,
                "shell",
                serde_json::json!({"command": "ls"}),
                vec![ToolCallContent::from("a.tally\nb.tally")],
            ),
            (
                "read-1",
                "Read tallies/2026/september/archive/a.tally",
                ToolKind::Read,
                "read_file",
                serde_json::json!({"path": "tallies/2026/september/archive/a.tally"}),
                vec![ToolCallContent::from("Lines 1–2 of 2")],
            ),
            (
                "run-2",
                "Background: npm run dev",
                ToolKind::Execute,
                "shell",
                serde_json::json!({"command": "npm run dev", "background": true}),
                vec![ToolCallContent::from("a.tally\nb.tally")],
            ),
            (
                "patch-1",
                "Apply patch to a.tally",
                ToolKind::Edit,
                "apply_patch",
                serde_json::json!({"patch": "*** Begin Patch\n*** Update File: a.tally\n@@\n-one\n+two\n*** End Patch\n"}),
                vec![
                    ToolCallContent::from("Modified a.tally"),
                    ToolCallContent::from(
                        Diff::new("/workspace/a.tally", "two\n").old_text("one\n".to_string()),
                    ),
                ],
            ),
        ] {
            self.update(SessionUpdate::ToolCall(
                ToolCall::new(id, title)
                    .name(name.to_string())
                    .kind(kind)
                    .raw_input(input),
            ))?;
            for fields in [
                ToolCallUpdateFields::new().status(ToolCallStatus::InProgress),
                ToolCallUpdateFields::new()
                    .status(ToolCallStatus::Completed)
                    .content(content),
            ] {
                self.update(SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
                    id, fields,
                )))?;
            }
        }
        self.update(SessionUpdate::ToolCall(
            ToolCall::new("answer-1", "Final answer from subagent child-1")
                .kind(ToolKind::Other)
                .status(ToolCallStatus::Completed)
                .content(vec![ToolCallContent::from("Fixed.")]),
        ))?;
        for text in [
            "\n  Two tal",
            "lies were counted in the workspace:\n\n",
            "a.tally and b.tally  \n\n",
        ] {
            self.message(text)?;
        }
        Ok(StopReason::EndTurn)
    }

    /// Replies `you said: <text>`, one agent message chunk per word, followed
    /// by `(N images)` when the prompt had any.
    fn reply(&self, text: &str, images: usize) -> agent_client_protocol::Result<StopReason> {
        let mut reply = format!("you said: {text}");
        match images {
            0 => {}
            1 => reply.push_str(" (1 image)"),
            n => reply.push_str(&format!(" ({n} images)")),
        }
        for word in reply.split_inclusive(' ') {
            self.message(word)?;
        }
        Ok(StopReason::EndTurn)
    }

    fn message(&self, text: &str) -> agent_client_protocol::Result<()> {
        self.update(SessionUpdate::AgentMessageChunk(ContentChunk::new(
            text.into(),
        )))
    }

    /// Sends the update and saves it for replay.
    fn update(&self, update: SessionUpdate) -> agent_client_protocol::Result<()> {
        self.saved_sessions.save(&self.session, update.clone());
        self.connection
            .send_notification(SessionNotification::new(self.session.clone(), update))
    }
}

/// A permission outcome as the scripts' messages show it: the selected option
/// ID, `cancelled`, or the error.
fn outcome(result: &agent_client_protocol::Result<RequestPermissionOutcome>) -> String {
    match result {
        Ok(RequestPermissionOutcome::Selected(selected)) => selected.option_id.to_string(),
        Ok(RequestPermissionOutcome::Cancelled) => "cancelled".to_string(),
        Ok(other) => format!("unknown outcome {other:?}"),
        Err(error) => format!("permission failed: {error}"),
    }
}
