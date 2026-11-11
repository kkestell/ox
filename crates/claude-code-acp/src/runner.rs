use std::{
    collections::{BTreeMap, HashMap},
    future::Future,
    os::fd::AsRawFd,
    path::Path,
    pin::pin,
    process::Stdio,
    sync::{Arc, Mutex},
    task::{Context, Poll, Waker},
};

use agent_client_protocol::v1::schema::StopReason;
use ox_protocol::AgentState;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStderr, ChildStdin, Command},
    sync::{mpsc, oneshot},
    task::{AbortHandle, JoinHandle},
};
use uuid::Uuid;

use crate::updates::Updates;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct McpServer {
    pub name: String,
    pub command: String,
    pub args: Vec<String>,
    pub env: BTreeMap<String, String>,
}

#[derive(Clone, Debug)]
pub enum Event {
    /// Text from the user, which a transcript carries as a message. The
    /// client already has the prompts of live turns.
    UserText(String),
    UserImage {
        data: String,
        mime_type: String,
    },
    UserDocument {
        data: String,
        mime_type: String,
    },
    Text(String),
    Thought(String),
    Tool {
        id: Option<String>,
        name: String,
        input: Value,
    },
    ToolResult {
        id: String,
        is_error: bool,
        output: Vec<ToolOutput>,
    },
}

/// Part of what a tool returned.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ToolOutput {
    Text(String),
    Image { data: String, mime_type: String },
}

#[derive(Debug)]
pub struct PermissionRequest {
    pub name: String,
    pub input: Value,
    pub tool_use_id: String,
    pub title: Option<String>,
    pub suggestions: Vec<Value>,
    pub suppress_always_allow_rule: bool,
}

#[derive(Debug)]
pub enum RunError {
    Cancelled,
    Failed(String),
}

pub struct StartRequest<'a> {
    pub cwd: &'a Path,
    pub resume: Option<&'a str>,
    pub model: &'a str,
    pub mode: &'a str,
    pub mcp_servers: &'a [McpServer],
}

#[derive(Debug)]
pub enum Finish {
    EndTurn,
    MaxTurns,
}

/// How a prompt's turn ended.
pub struct RunOutcome {
    /// The Claude session id claude reported, whatever the outcome.
    pub session_id: Option<String>,
    pub result: Result<Finish, RunError>,
    /// Dropped once the prompt's response is sent. What claude wrote after
    /// the turn waits for that, so the client reads the response first.
    pub responding: Option<oneshot::Sender<()>>,
}

/// Why claude did not take a prompt.
pub enum Refused {
    /// An earlier prompt is waiting for its result.
    Busy,
    /// Claude has exited, for this reason.
    Exited(String),
}

impl std::fmt::Display for Refused {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Busy => f.write_str("A prompt is already running for this session"),
            Self::Exited(error) => f.write_str(error),
        }
    }
}

/// A prompt waiting for its turn's result.
struct Prompt {
    uuid: String,
    /// Claude has taken the prompt into a turn, so the next result is its.
    started: bool,
    done: oneshot::Sender<RunOutcome>,
}

#[derive(Default)]
struct State {
    prompt: Option<Prompt>,
    /// Control requests waiting for claude's response, by request ID.
    controls: HashMap<String, oneshot::Sender<Result<(), String>>>,
    /// The tasks asking the client about claude's permission requests, by
    /// request ID.
    permissions: HashMap<String, AbortHandle>,
    control_counter: u64,
    session_id: Option<String>,
    /// Why claude exited, once it has.
    exited: Option<String>,
}

/// A `claude -p` process that reads stream-json messages from stdin for as
/// long as the session lives. It takes each prompt as a turn of one
/// conversation, and starts turns of its own when a background task it
/// started reports back. Everything it writes becomes session updates,
/// whether or not a prompt is waiting. Dropping it closes claude's stdin,
/// which ends claude.
pub struct Claude {
    input: mpsc::UnboundedSender<Value>,
    state: Arc<Mutex<State>>,
}

impl Claude {
    pub fn start(request: &StartRequest<'_>, updates: Updates) -> Result<Self, String> {
        let mut command = Command::new("claude");
        command
            .arg("-p")
            .args([
                "--output-format",
                "stream-json",
                "--input-format",
                "stream-json",
                "--verbose",
                "--include-partial-messages",
                "--replay-user-messages",
            ])
            .args(["--model", request.model])
            .args([
                "--permission-mode",
                request.mode,
                "--permission-prompt-tool",
                "stdio",
            ]);
        if let Some(id) = request.resume {
            command.arg("--resume").arg(id);
        }
        if !request.mcp_servers.is_empty() {
            let servers: BTreeMap<_, _> = request
                .mcp_servers
                .iter()
                .map(|s| {
                    (
                        s.name.clone(),
                        json!({"command": s.command, "args": s.args, "env": s.env}),
                    )
                })
                .collect();
            command
                .arg("--mcp-config")
                .arg(json!({"mcpServers": servers}).to_string());
        }
        let mut child = command
            .current_dir(request.cwd)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .env_remove("ANTHROPIC_API_KEY")
            .env_remove("ANTHROPIC_AUTH_TOKEN")
            // Claude reports when it starts and stops working only if asked.
            .env("CLAUDE_CODE_SDK_READS_SESSION_STATE", "1")
            .kill_on_drop(true)
            .spawn()
            .map_err(|e| format!("failed to start claude: {e}"))?;
        let (input, messages) = mpsc::unbounded_channel();
        let state = Arc::new(Mutex::new(State::default()));
        tokio::spawn(write_input(
            child.stdin.take().expect("piped stdin"),
            messages,
        ));
        let output = Output {
            updates: Arc::new(updates),
            state: state.clone(),
            input: input.downgrade(),
            saw_text_delta: false,
            saw_thought_delta: false,
            unclaimed_error: None,
            running: false,
            ended: None,
        };
        tokio::spawn(output.read(child));
        Ok(Self { input, state })
    }

    /// Sends a prompt and returns the receiver of its turn's outcome.
    /// Messages reach claude in the order they are sent, so an interrupt
    /// sent after this call stops this prompt's turn.
    pub fn prompt(&self, content: &[Value]) -> Result<oneshot::Receiver<RunOutcome>, Refused> {
        let mut state = self.state.lock().unwrap();
        if let Some(error) = &state.exited {
            return Err(Refused::Exited(error.clone()));
        }
        if state.prompt.is_some() {
            return Err(Refused::Busy);
        }
        let uuid = Uuid::new_v4().to_string();
        let (done, outcome) = oneshot::channel();
        state.prompt = Some(Prompt {
            uuid: uuid.clone(),
            started: false,
            done,
        });
        let _ = self.input.send(json!({
            "type": "user",
            "uuid": uuid,
            "session_id": "",
            "message": {"role": "user", "content": content},
            "parent_tool_use_id": null
        }));
        Ok(outcome)
    }

    /// Stops the running turn and drops a prompt claude has not started.
    pub fn interrupt(&self) {
        drop(self.control(&json!({"subtype": "interrupt", "cancel_queued": true})));
    }

    /// Asks claude to change its permission mode and returns the receiver
    /// of its answer.
    pub fn set_permission_mode(&self, mode: &str) -> oneshot::Receiver<Result<(), String>> {
        self.control(&json!({"subtype": "set_permission_mode", "mode": mode}))
    }

    /// Asks claude to change its model and returns the receiver of its
    /// answer.
    pub fn set_model(&self, model: &str) -> oneshot::Receiver<Result<(), String>> {
        self.control(&json!({"subtype": "set_model", "model": model}))
    }

    /// Sends a control request and returns the receiver of claude's answer,
    /// which closes if claude exits first.
    fn control(&self, request: &Value) -> oneshot::Receiver<Result<(), String>> {
        let (reply, answer) = oneshot::channel();
        let mut state = self.state.lock().unwrap();
        if state.exited.is_none() {
            state.control_counter += 1;
            let id = format!("control-{}", state.control_counter);
            state.controls.insert(id.clone(), reply);
            let _ = self.input.send(json!({
                "type": "control_request",
                "request_id": id,
                "request": request
            }));
        }
        answer
    }
}

/// Writes each message to claude's stdin as a line, until the session drops
/// its `Claude` or claude closes its stdin.
async fn write_input(mut stdin: ChildStdin, mut messages: mpsc::UnboundedReceiver<Value>) {
    while let Some(message) = messages.recv().await {
        let mut line = message.to_string().into_bytes();
        line.push(b'\n');
        if stdin.write_all(&line).await.is_err() {
            return;
        }
    }
}

/// Polls `future` once, for a result that needs no waiting.
fn ready<T>(future: impl Future<Output = T>) -> Option<T> {
    match pin!(future).poll(&mut Context::from_waker(Waker::noop())) {
        Poll::Ready(value) => Some(value),
        Poll::Pending => None,
    }
}

/// Claude's output, translated as it arrives.
struct Output {
    updates: Arc<Updates>,
    state: Arc<Mutex<State>>,
    /// Claude's stdin, for answers to its requests. It is weak so that the
    /// session's `Claude` alone keeps stdin open.
    input: mpsc::WeakUnboundedSender<Value>,
    /// The message being streamed has sent text, or thought, as deltas.
    saw_text_delta: bool,
    saw_thought_delta: bool,
    /// The error of a turn that ended while a prompt waited to start. It is
    /// why the prompt fails if claude exits, as when claude cannot resume
    /// the conversation, and otherwise the error of a turn claude started.
    unclaimed_error: Option<String>,
    /// Claude last said it was working.
    running: bool,
    /// How the last turn no prompt claimed ended, for the client to learn
    /// when claude next says it is idle.
    ended: Option<StopReason>,
}

impl Output {
    /// Translates claude's output until claude exits, then ends what it left
    /// open.
    async fn read(mut self, mut child: Child) {
        let mut stderr = Stderr::collect(child.stderr.take().expect("piped stderr"));
        let mut lines = BufReader::new(child.stdout.take().expect("piped stdout")).lines();
        let status = loop {
            tokio::select! {
                biased;
                line = lines.next_line() => match line {
                    Ok(Some(line)) => self.line(&line).await,
                    _ => break child.wait().await,
                },
                status = child.wait() => {
                    // Everything claude wrote is in the pipe; a process it
                    // started may hold the pipe open.
                    while let Some(Ok(Some(line))) = ready(lines.next_line()) {
                        self.line(&line).await;
                    }
                    break status;
                }
            }
        };
        let error = match status {
            Ok(status) => format!(
                "claude exited with {status}: {}",
                String::from_utf8_lossy(&stderr.finish().await).trim()
            ),
            Err(error) => format!("claude process failed: {error}"),
        };
        let (prompt, session_id) = {
            let mut state = self.state.lock().unwrap();
            state.exited = Some(error.clone());
            state.controls.clear();
            for (_, permission) in state.permissions.drain() {
                permission.abort();
            }
            (state.prompt.take(), state.session_id.clone())
        };
        self.updates.fail_open_tools();
        self.state(false);
        if let Some(prompt) = prompt {
            let _ = prompt.done.send(RunOutcome {
                session_id,
                result: Err(RunError::Failed(
                    self.unclaimed_error.take().unwrap_or(error),
                )),
                responding: None,
            });
        }
    }

    /// Tells the client when claude starts or stops working.
    fn state(&mut self, running: bool) {
        if running != self.running {
            self.running = running;
            self.updates.state(if running {
                AgentState::Running
            } else {
                AgentState::Idle {
                    stop_reason: self.ended.take(),
                }
            });
        }
    }

    /// Translates a line of claude's output. After the result of a prompt's
    /// turn, it waits until the prompt's response is sent.
    async fn line(&mut self, line: &str) {
        if let Some(responded) = self.translate(line) {
            let _ = responded.await;
        }
    }

    fn send(&self, message: Value) {
        if let Some(input) = self.input.upgrade() {
            let _ = input.send(message);
        }
    }

    /// Translates a line of claude's output and returns, for the result of
    /// a prompt's turn, the receiver that closes once the prompt's response
    /// is sent.
    fn translate(&mut self, line: &str) -> Option<oneshot::Receiver<()>> {
        let value = serde_json::from_str::<Value>(line).ok()?;
        if let Some(id) = value
            .get("session_id")
            .and_then(Value::as_str)
            .filter(|id| !id.is_empty())
        {
            self.state.lock().unwrap().session_id = Some(id.to_owned());
        }
        // A subagent's messages show as activity of the tool call that
        // started it; its streamed text is not shown.
        if let Some(parent) = value["parent_tool_use_id"].as_str() {
            if matches!(value["type"].as_str(), Some("assistant" | "user")) {
                for event in message_events(&value) {
                    self.updates.subagent_event(parent, event);
                }
            }
            return None;
        }
        // Claude echoes a prompt once its turn has taken it.
        if value["isReplay"] == true {
            if let Some(uuid) = value["uuid"].as_str() {
                self.started(uuid);
            }
            return None;
        }
        let event = if value["type"] == "stream_event" {
            &value["event"]
        } else {
            &value
        };
        match event.get("type").and_then(Value::as_str) {
            Some("control_request") => self.control_request(event),
            Some("control_cancel_request") => {
                let permission = event["request_id"]
                    .as_str()
                    .and_then(|id| self.state.lock().unwrap().permissions.remove(id));
                if let Some(permission) = permission {
                    permission.abort();
                }
            }
            Some("control_response") => {
                let response = &event["response"];
                let reply = response["request_id"]
                    .as_str()
                    .and_then(|id| self.state.lock().unwrap().controls.remove(id));
                if let Some(reply) = reply {
                    let _ = reply.send(if response["subtype"] == "error" {
                        Err(response["error"]
                            .as_str()
                            .unwrap_or("claude refused the request")
                            .to_owned())
                    } else {
                        Ok(())
                    });
                }
            }
            Some("command_lifecycle") if event["state"] == "started" => {
                if let Some(uuid) = event["command_uuid"].as_str() {
                    self.started(uuid);
                }
            }
            Some("content_block_delta") => match event["delta"]["type"].as_str() {
                Some("text_delta") => {
                    if let Some(text) = event["delta"]["text"].as_str() {
                        self.saw_text_delta = true;
                        self.updates.event(Event::Text(text.into()));
                    }
                }
                Some("thinking_delta") => {
                    if let Some(text) = event["delta"]["thinking"].as_str() {
                        self.saw_thought_delta = true;
                        self.updates.event(Event::Thought(text.into()));
                    }
                }
                _ => {}
            },
            Some("assistant" | "user") => {
                for event in message_events(&value) {
                    match event {
                        // Deltas already streamed this text.
                        Event::Text(_) if self.saw_text_delta => {}
                        Event::Thought(_) if self.saw_thought_delta => {}
                        // The client has the prompts it sent; the rest of
                        // what claude reports as the user is its own notes.
                        Event::UserText(_)
                        | Event::UserImage { .. }
                        | Event::UserDocument { .. } => {}
                        event => self.updates.event(event),
                    }
                }
                self.saw_text_delta = false;
                self.saw_thought_delta = false;
            }
            Some("system") if event["subtype"] == "session_state_changed" => {
                // Claude's third state, waiting for a permission, is part of
                // working; the client has the request itself.
                match event["state"].as_str() {
                    Some("running") => self.state(true),
                    Some("idle") => self.state(false),
                    _ => {}
                }
            }
            Some("result") => return self.result(&value),
            _ => {}
        }
        None
    }

    /// Notes that claude took the prompt sent as `uuid` into a turn, so the
    /// turn's result is the prompt's. Claude says so with a `started`
    /// lifecycle line and again by echoing the prompt; the first one counts.
    fn started(&mut self, uuid: &str) {
        let mut state = self.state.lock().unwrap();
        if let Some(prompt) = &mut state.prompt
            && prompt.uuid == uuid
            && !prompt.started
        {
            prompt.started = true;
            drop(state);
            if let Some(error) = self.unclaimed_error.take() {
                self.updates.error(&error);
            }
        }
    }

    /// Asks the client about a tool permission while output keeps flowing,
    /// and refuses any other request.
    fn control_request(&self, event: &Value) {
        let Some(request_id) = event["request_id"].as_str() else {
            return;
        };
        let request = &event["request"];
        if request["subtype"] != "can_use_tool" {
            self.send(json!({
                "type": "control_response",
                "response": {
                    "subtype": "error",
                    "request_id": request_id,
                    "error": "Unsupported control request"
                }
            }));
            return;
        }
        let permission = PermissionRequest {
            name: request["tool_name"].as_str().unwrap_or("Tool").to_owned(),
            input: request["input"].clone(),
            tool_use_id: request["tool_use_id"].as_str().unwrap_or("").to_owned(),
            title: request["title"].as_str().map(str::to_owned),
            suggestions: request["permission_suggestions"]
                .as_array()
                .cloned()
                .unwrap_or_default(),
            suppress_always_allow_rule: request["suppress_always_allow_rule"]
                .as_bool()
                .unwrap_or(false),
        };
        let updates = self.updates.clone();
        let input = self.input.clone();
        let id = request_id.to_owned();
        let task = tokio::spawn(async move {
            let answer = updates.permission(permission).await;
            if let Some(input) = input.upgrade() {
                let _ = input.send(json!({
                    "type": "control_response",
                    "response": {"subtype": "success", "request_id": id, "response": answer}
                }));
            }
        });
        self.state
            .lock()
            .unwrap()
            .permissions
            .insert(request_id.to_owned(), task.abort_handle());
    }

    /// Ends a turn. The result belongs to the waiting prompt once claude has
    /// started it; an interrupt also drops a prompt claude had not started.
    /// Returns the receiver that closes once that prompt's response is sent.
    fn result(&mut self, result: &Value) -> Option<oneshot::Receiver<()>> {
        let aborted = result["terminal_reason"]
            .as_str()
            .is_some_and(|reason| reason.starts_with("aborted"));
        let outcome = if aborted {
            Err(RunError::Cancelled)
        } else {
            result_outcome(result)
        };
        let (prompt, waiting, session_id) = {
            let mut state = self.state.lock().unwrap();
            for (_, permission) in state.permissions.drain() {
                permission.abort();
            }
            let prompt = if aborted || state.prompt.as_ref().is_some_and(|p| p.started) {
                state.prompt.take()
            } else {
                None
            };
            (prompt, state.prompt.is_some(), state.session_id.clone())
        };
        self.updates.fail_open_tools();
        self.saw_text_delta = false;
        self.saw_thought_delta = false;
        match (prompt, outcome) {
            (Some(prompt), result) => {
                let (responding, responded) = oneshot::channel();
                let _ = prompt.done.send(RunOutcome {
                    session_id,
                    result,
                    responding: Some(responding),
                });
                return Some(responded);
            }
            (None, Err(RunError::Failed(error))) if waiting => self.unclaimed_error = Some(error),
            (None, result) => {
                if let Err(RunError::Failed(error)) = &result {
                    self.updates.error(error);
                }
                self.ended = Some(stop_reason(&result));
            }
        }
        None
    }
}

/// Reads claude's stderr while claude runs, so a full pipe never blocks it.
struct Stderr {
    finish: Option<oneshot::Sender<()>>,
    task: JoinHandle<Vec<u8>>,
}

impl Stderr {
    fn collect(stderr: ChildStderr) -> Self {
        let (finish, finished) = oneshot::channel();
        Self {
            finish: Some(finish),
            task: tokio::spawn(read_stderr(stderr, finished)),
        }
    }

    /// Everything claude wrote to stderr. Call it once claude has exited:
    /// what it wrote is then in the pipe, and a process it started that still
    /// holds the pipe open does not delay the result.
    async fn finish(&mut self) -> Vec<u8> {
        if let Some(finish) = self.finish.take() {
            let _ = finish.send(());
        }
        (&mut self.task).await.unwrap_or_default()
    }
}

impl Drop for Stderr {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn read_stderr(mut stderr: ChildStderr, mut finished: oneshot::Receiver<()>) -> Vec<u8> {
    let mut buffer = Vec::new();
    let mut chunk = [0; 8192];
    loop {
        tokio::select! {
            read = stderr.read(&mut chunk) => match read {
                Ok(n) if n > 0 => buffer.extend_from_slice(&chunk[..n]),
                _ => return buffer,
            },
            _ = &mut finished => {
                read_buffered(&stderr, &mut buffer);
                return buffer;
            }
        }
    }
}

/// Reads what the pipe holds now. Tokio opens child pipes non-blocking, so
/// `read` fails with `EAGAIN` instead of waiting once the pipe is empty.
fn read_buffered(stderr: &ChildStderr, buffer: &mut Vec<u8>) {
    let mut chunk = [0u8; 8192];
    loop {
        // SAFETY: reads at most `chunk.len()` bytes into `chunk` from a
        // descriptor `stderr` owns and keeps open for the call.
        let n = unsafe { libc::read(stderr.as_raw_fd(), chunk.as_mut_ptr().cast(), chunk.len()) };
        if n <= 0 {
            return;
        }
        buffer.extend_from_slice(&chunk[..n.cast_unsigned()]);
    }
}

/// The ACP stop reason of a turn's outcome. A failed turn reports its error
/// as a message and ends as a turn does.
pub fn stop_reason(result: &Result<Finish, RunError>) -> StopReason {
    match result {
        Ok(Finish::EndTurn) | Err(RunError::Failed(_)) => StopReason::EndTurn,
        Ok(Finish::MaxTurns) => StopReason::MaxTurnRequests,
        Err(RunError::Cancelled) => StopReason::Cancelled,
    }
}

fn result_outcome(result: &Value) -> Result<Finish, RunError> {
    if result["subtype"] == "error_max_turns" {
        return Ok(Finish::MaxTurns);
    }
    if result["is_error"].as_bool() != Some(true) {
        return Ok(Finish::EndTurn);
    }
    let errors = result["errors"]
        .as_array()
        .map(|errors| {
            errors
                .iter()
                .filter_map(Value::as_str)
                .collect::<Vec<_>>()
                .join("; ")
        })
        .filter(|errors| !errors.is_empty());
    let message = result["result"]
        .as_str()
        .filter(|message| !message.is_empty())
        .map(str::to_owned)
        .or(errors)
        .unwrap_or_else(|| "claude reported an error".into());
    Err(RunError::Failed(message))
}

/// The events of one Claude message: an `assistant` or `user` line of
/// claude's stream-json output, or the same entry in a transcript. Meta
/// messages, which Claude adds for the model, have none, and neither do
/// empty text and thinking blocks, which Claude records when it does not
/// display thinking.
pub fn message_events(entry: &Value) -> Vec<Event> {
    if entry["isMeta"] == true {
        return Vec::new();
    }
    let user = entry["type"] == "user";
    let content = &entry["message"]["content"];
    if let Some(text) = content.as_str() {
        return if user {
            vec![Event::UserText(text.to_owned())]
        } else {
            vec![Event::Text(text.to_owned())]
        };
    }
    let Some(blocks) = content.as_array() else {
        return Vec::new();
    };
    blocks
        .iter()
        .filter_map(|block| {
            let text = |key: &str| {
                block[key]
                    .as_str()
                    .filter(|text| !text.is_empty())
                    .map(str::to_owned)
            };
            match (user, block["type"].as_str()?) {
                (true, "text") => text("text").map(Event::UserText),
                (true, "image") => Some(Event::UserImage {
                    data: block["source"]["data"].as_str()?.to_owned(),
                    mime_type: block["source"]["media_type"].as_str()?.to_owned(),
                }),
                (true, "document") => Some(Event::UserDocument {
                    data: block["source"]["data"].as_str()?.to_owned(),
                    mime_type: block["source"]["media_type"].as_str()?.to_owned(),
                }),
                (true, "tool_result") => Some(Event::ToolResult {
                    id: text("tool_use_id")?,
                    is_error: block["is_error"].as_bool().unwrap_or(false),
                    output: tool_output(&block["content"]),
                }),
                (false, "text") => text("text").map(Event::Text),
                (false, "thinking") => text("thinking").map(Event::Thought),
                (false, "tool_use") => Some(Event::Tool {
                    id: text("id"),
                    name: text("name")?,
                    input: block["input"].clone(),
                }),
                _ => None,
            }
        })
        .collect()
}

/// What a tool result holds: a string, or text and image blocks. Other
/// blocks show as their type.
fn tool_output(content: &Value) -> Vec<ToolOutput> {
    if let Some(text) = content.as_str() {
        return if text.is_empty() {
            Vec::new()
        } else {
            vec![ToolOutput::Text(text.to_owned())]
        };
    }
    let Some(blocks) = content.as_array() else {
        return Vec::new();
    };
    blocks
        .iter()
        .filter_map(|block| match block["type"].as_str()? {
            "text" => block["text"]
                .as_str()
                .filter(|text| !text.is_empty())
                .map(|text| ToolOutput::Text(text.to_owned())),
            "image" if block["source"]["type"] == "base64" => Some(ToolOutput::Image {
                data: block["source"]["data"].as_str()?.to_owned(),
                mime_type: block["source"]["media_type"].as_str()?.to_owned(),
            }),
            other => Some(ToolOutput::Text(format!("[{other}]"))),
        })
        .collect()
}
