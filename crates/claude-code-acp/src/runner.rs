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

use crate::updates::Updates;
use agent_client_protocol::v2::Responder;
use agent_client_protocol::v2::schema::{
    ContentBlock, ErrorStopReason, PromptResponse, StateUpdate, StopReason,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStderr, ChildStdin, Command},
    sync::{mpsc, oneshot},
    task::{AbortHandle, JoinHandle},
};

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

/// How a turn ended, as far as the session's record of it goes.
pub struct TurnEnd {
    /// The Claude session id claude reported, whatever the outcome.
    pub session_id: Option<String>,
    /// The turn finished, rather than being cancelled or failing.
    pub finished: bool,
}

/// Told when a turn ends, before the client learns claude is idle.
pub type OnTurnEnd = Arc<dyn Fn(TurnEnd) + Send + Sync>;

/// A prompt waiting for claude to take it into a turn, which inserts it into
/// the conversation.
struct Prompt {
    content: Vec<ContentBlock>,
    responder: Responder<PromptResponse>,
}

impl Prompt {
    /// Answers the prompt with the error that kept claude from taking it.
    fn fail(self, error: RunError) {
        let _ = self.responder.respond_with_error(match error {
            RunError::Cancelled => agent_client_protocol::Error::request_cancelled(),
            RunError::Failed(error) => agent_client_protocol::Error::new(-32603, error),
        });
    }
}

#[derive(Default)]
struct State {
    /// Prompts claude has not taken yet, by the `uuid` claude knows them by.
    prompts: HashMap<String, Prompt>,
    /// Control requests waiting for claude's response, by request ID.
    controls: HashMap<String, oneshot::Sender<Result<(), String>>>,
    /// The tasks asking the client about claude's permission requests, by
    /// request ID.
    permissions: HashMap<String, AbortHandle>,
    control_counter: u64,
    session_id: Option<String>,
    /// Why claude exited, once it has.
    exited: Option<String>,
    /// The session is closing, so claude's exit is a cancellation.
    closing: bool,
    /// The client cancelled the running turn, so the turn ends as cancelled
    /// however claude stops it.
    cancelling: bool,
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
    updates: Arc<Updates>,
    /// Closes once claude has exited.
    exited: oneshot::Receiver<()>,
}

impl Claude {
    pub fn start(
        request: &StartRequest<'_>,
        updates: Updates,
        on_turn_end: OnTurnEnd,
    ) -> Result<Self, String> {
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
        let updates = Arc::new(updates);
        let (exit, exited) = oneshot::channel();
        let output = Output {
            updates: updates.clone(),
            state: state.clone(),
            input: input.downgrade(),
            on_turn_end,
            text_id: None,
            thought_id: None,
            unclaimed_error: None,
            _exit: exit,
        };
        tokio::spawn(output.read(child));
        Ok(Self {
            input,
            state,
            updates,
            exited,
        })
    }

    /// What claude is doing now, for a client that resumes the session.
    pub fn state(&self) -> StateUpdate {
        self.updates.state()
    }

    /// Stops claude and returns once it has exited. Its turn is cancelled,
    /// and so are the prompts it has not taken.
    pub async fn stop(self) {
        self.state.lock().unwrap().closing = true;
        self.interrupt();
        let Self { input, exited, .. } = self;
        // Claude exits when its stdin closes.
        drop(input);
        let _ = exited.await;
    }

    /// Sends a prompt. Once claude takes it into a turn, which inserts it
    /// into the conversation, the session gets it as the user message
    /// `message_id` and `responder` answers with that ID. Messages reach
    /// claude in the order they are sent, so an interrupt sent after this
    /// call stops this prompt's turn or drops the prompt. If claude has
    /// exited, the responder comes back.
    pub fn prompt(
        &self,
        message_id: &str,
        blocks: Vec<ContentBlock>,
        content: &[Value],
        responder: Responder<PromptResponse>,
    ) -> Result<(), Box<Responder<PromptResponse>>> {
        let mut state = self.state.lock().unwrap();
        if state.exited.is_some() {
            return Err(Box::new(responder));
        }
        state.prompts.insert(
            message_id.to_owned(),
            Prompt {
                content: blocks,
                responder,
            },
        );
        let _ = self.input.send(json!({
            "type": "user",
            "uuid": message_id,
            "session_id": "",
            "message": {"role": "user", "content": content},
            "parent_tool_use_id": null
        }));
        Ok(())
    }

    /// Stops the running turn and drops the prompts claude has not taken.
    pub fn interrupt(&self) {
        if self.updates.is_working() {
            self.state.lock().unwrap().cancelling = true;
        }
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
    on_turn_end: OnTurnEnd,
    /// The message and thought being streamed, once they have sent a delta.
    text_id: Option<String>,
    thought_id: Option<String>,
    /// The error of a turn that ended while a prompt waited to be taken. It
    /// is why the prompt fails if claude exits, as when claude cannot resume
    /// the conversation.
    unclaimed_error: Option<String>,
    /// Dropped once claude has exited.
    _exit: oneshot::Sender<()>,
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
                    Ok(Some(line)) => self.translate(&line),
                    _ => break child.wait().await,
                },
                status = child.wait() => {
                    // Everything claude wrote is in the pipe; a process it
                    // started may hold the pipe open.
                    while let Some(Ok(Some(line))) = ready(lines.next_line()) {
                        self.translate(&line);
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
        let (prompts, session_id, closing, cancelling) = {
            let mut state = self.state.lock().unwrap();
            state.exited = Some(error.clone());
            state.controls.clear();
            for (_, permission) in state.permissions.drain() {
                permission.abort();
            }
            (
                std::mem::take(&mut state.prompts),
                state.session_id.clone(),
                state.closing,
                state.cancelling,
            )
        };
        self.updates.fail_open_tools();
        let result = if closing || cancelling {
            Err(RunError::Cancelled)
        } else {
            Err(RunError::Failed(error.clone()))
        };
        if self.updates.is_working() {
            self.end_turn(session_id, &result);
        }
        let failure = self.unclaimed_error.take().unwrap_or(error);
        for (_, prompt) in prompts {
            prompt.fail(if closing {
                RunError::Cancelled
            } else {
                RunError::Failed(failure.clone())
            });
        }
    }

    /// Records how a turn ended, then tells the client claude is idle.
    fn end_turn(&self, session_id: Option<String>, result: &Result<Finish, RunError>) {
        (self.on_turn_end)(TurnEnd {
            session_id,
            finished: result.is_ok(),
        });
        self.updates.idle(stop_reason(result));
    }

    fn send(&self, message: Value) {
        if let Some(input) = self.input.upgrade() {
            let _ = input.send(message);
        }
    }

    /// Translates a line of claude's output.
    fn translate(&mut self, line: &str) {
        let Ok(value) = serde_json::from_str::<Value>(line) else {
            return;
        };
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
            return;
        }
        // Claude echoes a prompt once its turn has taken it.
        if value["isReplay"] == true {
            if let Some(uuid) = value["uuid"].as_str() {
                self.started(uuid);
            }
            return;
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
                        let id = self.text_id.get_or_insert_with(new_id);
                        self.updates.text_chunk(id, text.into());
                    }
                }
                Some("thinking_delta") => {
                    if let Some(text) = event["delta"]["thinking"]
                        .as_str()
                        .filter(|text| !text.is_empty())
                    {
                        let id = self.thought_id.get_or_insert_with(new_id);
                        self.updates.thought_chunk(id, text.into());
                    }
                }
                _ => {}
            },
            Some("assistant" | "user") => {
                for event in message_events(&value) {
                    match event {
                        // Deltas already streamed this text.
                        Event::Text(_) if self.text_id.is_some() => {}
                        Event::Thought(_) if self.thought_id.is_some() => {}
                        // The client has the prompts it sent; the rest of
                        // what claude reports as the user is its own notes.
                        Event::UserText(_)
                        | Event::UserImage { .. }
                        | Event::UserDocument { .. } => {}
                        event => self.updates.event(event),
                    }
                }
                self.text_id = None;
                self.thought_id = None;
            }
            Some("system") if event["subtype"] == "session_state_changed" => {
                // Claude's third state, waiting for a permission, is part of
                // working; the client has the request itself.
                match event["state"].as_str() {
                    Some("running") => self.updates.working(),
                    Some("idle") => self.updates.idle_without_reason(),
                    _ => {}
                }
            }
            Some("result") => self.result(&value),
            _ => {}
        }
    }

    /// Notes that claude took the prompt sent as `uuid` into a turn, which
    /// inserts it into the conversation. Claude says so with a `started`
    /// lifecycle line and again by echoing the prompt; the first one counts.
    fn started(&mut self, uuid: &str) {
        let prompt = self.state.lock().unwrap().prompts.remove(uuid);
        if let Some(prompt) = prompt {
            self.unclaimed_error = None;
            self.updates.user_message(uuid, prompt.content);
            let _ = prompt.responder.respond(PromptResponse::new(uuid));
            self.updates.working();
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

    /// Ends a turn. An interrupt also drops the prompts claude had not taken.
    fn result(&mut self, result: &Value) {
        let aborted = result["terminal_reason"]
            .as_str()
            .is_some_and(|reason| reason.starts_with("aborted"));
        let (dropped, waiting, session_id, cancelling) = {
            let mut state = self.state.lock().unwrap();
            for (_, permission) in state.permissions.drain() {
                permission.abort();
            }
            let dropped = if aborted {
                std::mem::take(&mut state.prompts)
            } else {
                HashMap::new()
            };
            (
                dropped,
                !state.prompts.is_empty(),
                state.session_id.clone(),
                std::mem::take(&mut state.cancelling),
            )
        };
        let outcome = match result_outcome(result) {
            _ if aborted => Err(RunError::Cancelled),
            Err(_) if cancelling => Err(RunError::Cancelled),
            outcome => outcome,
        };
        self.updates.fail_open_tools();
        self.text_id = None;
        self.thought_id = None;
        for (_, prompt) in dropped {
            prompt.fail(RunError::Cancelled);
        }
        // A failure while a prompt waits and no turn runs is not a turn's:
        // the error is why the prompt fails if claude exits.
        if let (Err(RunError::Failed(error)), true, false) =
            (&outcome, waiting, self.updates.is_working())
        {
            self.unclaimed_error = Some(error.clone());
            return;
        }
        self.end_turn(session_id, &outcome);
    }
}

fn new_id() -> String {
    uuid::Uuid::new_v4().to_string()
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

/// The ACP stop reason of a turn's outcome. A failed turn ends with the
/// error as its details.
pub fn stop_reason(result: &Result<Finish, RunError>) -> StopReason {
    match result {
        Ok(Finish::EndTurn) => StopReason::EndTurn,
        Ok(Finish::MaxTurns) => StopReason::MaxTurnRequests,
        Err(RunError::Cancelled) => StopReason::Cancelled,
        Err(RunError::Failed(error)) => StopReason::Error(ErrorStopReason::new().error(
            agent_client_protocol::v2::schema::Error::new(-32603, error.clone()),
        )),
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
/// blocks show as their JSON.
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
        .filter_map(|block| match block["type"].as_str() {
            Some("text") => block["text"]
                .as_str()
                .filter(|text| !text.is_empty())
                .map(|text| ToolOutput::Text(text.to_owned())),
            Some("image") if block["source"]["type"] == "base64" => Some(ToolOutput::Image {
                data: block["source"]["data"].as_str()?.to_owned(),
                mime_type: block["source"]["media_type"].as_str()?.to_owned(),
            }),
            _ => Some(ToolOutput::Text(block.to_string())),
        })
        .collect()
}
