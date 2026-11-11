use std::{
    collections::BTreeMap, future::Future, os::fd::AsRawFd, path::PathBuf, process::Stdio,
    sync::Mutex, time::Duration,
};

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader},
    process::{Child, ChildStderr, Command},
    sync::{oneshot, watch},
    task::JoinHandle,
    time::{Instant, sleep_until, timeout},
};

use crate::config::Config;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct McpServer {
    pub name: String,
    pub command: String,
    pub args: Vec<String>,
    pub env: BTreeMap<String, String>,
}

#[derive(Clone, Debug)]
pub enum Event {
    /// Text from the user. Live turns carry the prompt in the request; a
    /// transcript carries it as a message.
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
    },
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

pub struct RunRequest<'a> {
    pub cwd: &'a PathBuf,
    /// The content of the user message.
    pub content: &'a [Value],
    pub resume: Option<&'a str>,
    pub model: &'a str,
    pub mode: &'a str,
    pub mcp_servers: &'a [McpServer],
}

/// How long claude gets to exit after reporting its result.
const EXIT_GRACE: Duration = Duration::from_secs(5);
/// How long claude gets to exit after `SIGTERM` before its process group is
/// killed.
const TERMINATE_GRACE: Duration = Duration::from_secs(2);

#[derive(Debug)]
pub enum Finish {
    EndTurn,
    MaxTurns,
}

pub struct RunOutcome {
    /// The Claude session id claude reported, whatever the outcome.
    pub session_id: Option<String>,
    pub result: Result<Finish, RunError>,
}

async fn write_message(
    stdin: &mut tokio::process::ChildStdin,
    value: &Value,
) -> Result<(), RunError> {
    let mut bytes = serde_json::to_vec(value).map_err(|e| RunError::Failed(e.to_string()))?;
    bytes.push(b'\n');
    stdin
        .write_all(&bytes)
        .await
        .map_err(|e| RunError::Failed(format!("failed writing to claude: {e}")))
}

pub async fn run<F: Future<Output = Value>>(
    config: &Config,
    request: RunRequest<'_>,
    cancelled: watch::Receiver<bool>,
    on_event: impl FnMut(Event),
    on_permission: impl FnMut(PermissionRequest) -> F,
) -> RunOutcome {
    let session_id = Mutex::new(None);
    let result = run_claude(
        config,
        request,
        cancelled,
        &session_id,
        on_event,
        on_permission,
    )
    .await;
    RunOutcome {
        session_id: session_id.into_inner().unwrap(),
        result,
    }
}

async fn run_claude<F: Future<Output = Value>>(
    config: &Config,
    request: RunRequest<'_>,
    mut cancelled: watch::Receiver<bool>,
    session_id: &Mutex<Option<String>>,
    on_event: impl FnMut(Event),
    on_permission: impl FnMut(PermissionRequest) -> F,
) -> Result<Finish, RunError> {
    if *cancelled.borrow() {
        return Err(RunError::Cancelled);
    }
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
        ])
        .args(["--model", request.model]);
    if let Some(id) = request.resume {
        command.arg("--resume").arg(id);
    }
    if let Some(turns) = config.max_turns {
        command.arg("--max-turns").arg(turns.to_string());
    }
    if config.skip_permissions {
        command.arg("--dangerously-skip-permissions");
    } else {
        command.args([
            "--permission-mode",
            request.mode,
            "--permission-prompt-tool",
            "stdio",
        ]);
    }
    for tool in &config.allowed_tools {
        command.arg("--allowedTools").arg(tool);
    }

    let mut mcp_file = None;
    if !request.mcp_servers.is_empty() {
        let mut file =
            tempfile::NamedTempFile::new().map_err(|e| RunError::Failed(e.to_string()))?;
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
        serde_json::to_writer(&mut file, &json!({"mcpServers": servers}))
            .map_err(|e| RunError::Failed(e.to_string()))?;
        command.arg("--mcp-config").arg(file.path());
        mcp_file = Some(file);
    }

    command
        .current_dir(request.cwd)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .env_remove("ANTHROPIC_API_KEY")
        .env_remove("ANTHROPIC_AUTH_TOKEN")
        .kill_on_drop(true)
        .process_group(0);
    let mut child = command
        .spawn()
        .map_err(|e| RunError::Failed(format!("failed to start claude: {e}")))?;
    let group = ProcessGroup(child.id());
    let mut stderr = Stderr::collect(child.stderr.take().expect("piped stderr"));
    let budget = Budget::new(config.timeout);
    let exchange = async {
        let reported = read_output(
            &mut child,
            request.content,
            &budget,
            session_id,
            on_event,
            on_permission,
        )
        .await?;
        if let Some(outcome) = reported {
            let _ = timeout(EXIT_GRACE, child.wait()).await;
            return outcome;
        }
        let status = timeout(EXIT_GRACE, child.wait())
            .await
            .map_err(|_| RunError::Failed("claude closed its output without exiting".into()))?
            .map_err(|e| RunError::Failed(format!("claude process failed: {e}")))?;
        if status.success() {
            return Ok(Finish::EndTurn);
        }
        let stderr = stderr.finish().await;
        Err(RunError::Failed(format!(
            "claude exited with {status}: {}",
            String::from_utf8_lossy(&stderr).trim()
        )))
    };
    let result = tokio::select! {
        result = exchange => result,
        () = budget.exhausted() => Err(RunError::Failed("claude timed out".into())),
        _ = cancelled.wait_for(|cancelled| *cancelled) => Err(RunError::Cancelled),
    };
    if result.is_err() {
        group.signal(libc::SIGTERM);
        let _ = timeout(TERMINATE_GRACE, child.wait()).await;
    }
    drop(mcp_file);
    result
}

/// Claude's process group, which also holds the processes claude starts.
/// Dropping it kills every process left in the group.
struct ProcessGroup(Option<u32>);

impl ProcessGroup {
    fn signal(&self, signal: libc::c_int) {
        if let Some(pid) = self.0 {
            // SAFETY: killpg only sends a signal; the group id is claude's pid,
            // which leads its own group because of `process_group(0)`.
            unsafe {
                libc::killpg(pid as libc::pid_t, signal);
            }
        }
    }
}

impl Drop for ProcessGroup {
    fn drop(&mut self) {
        self.signal(libc::SIGKILL);
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
        buffer.extend_from_slice(&chunk[..n as usize]);
    }
}

/// Limits how long claude works on a turn. Time spent waiting for a
/// permission answer does not count, and each answer starts a full budget.
struct Budget {
    limit: Option<Duration>,
    deadline: watch::Sender<Option<Instant>>,
}

impl Budget {
    fn new(limit: Option<Duration>) -> Self {
        let budget = Self {
            limit,
            deadline: watch::Sender::new(None),
        };
        budget.restart();
        budget
    }

    fn restart(&self) {
        self.deadline
            .send_replace(self.limit.map(|limit| Instant::now() + limit));
    }

    fn pause(&self) {
        self.deadline.send_replace(None);
    }

    /// Resolves once the running budget runs out.
    async fn exhausted(&self) {
        let mut deadline = self.deadline.subscribe();
        loop {
            let current = *deadline.borrow_and_update();
            match current {
                Some(at) => tokio::select! {
                    () = sleep_until(at) => return,
                    _ = deadline.changed() => {}
                },
                None => {
                    let _ = deadline.changed().await;
                }
            }
        }
    }
}

async fn read_output<F: Future<Output = Value>>(
    child: &mut Child,
    content: &[Value],
    budget: &Budget,
    session_id: &Mutex<Option<String>>,
    mut on_event: impl FnMut(Event),
    mut on_permission: impl FnMut(PermissionRequest) -> F,
) -> Result<Option<Result<Finish, RunError>>, RunError> {
    let mut stdin = child.stdin.take().expect("piped stdin");
    let user = json!({
        "type": "user",
        "session_id": "",
        "message": {"role": "user", "content": content},
        "parent_tool_use_id": null
    });
    write_message(&mut stdin, &user).await?;
    let stdout = child.stdout.take().expect("piped stdout");
    let mut lines = BufReader::new(stdout).lines();
    let mut reported = None;
    let mut saw_text_delta = false;
    let mut saw_thought_delta = false;

    loop {
        let line = lines
            .next_line()
            .await
            .map_err(|e| RunError::Failed(format!("failed reading claude output: {e}")))?;
        let Some(line) = line else {
            break;
        };
        let Ok(value) = serde_json::from_str::<Value>(&line) else {
            continue;
        };
        if let Some(id) = value
            .get("session_id")
            .and_then(Value::as_str)
            .filter(|id| !id.is_empty())
        {
            *session_id.lock().unwrap() = Some(id.to_owned());
        }
        let event = if value["type"] == "stream_event" {
            &value["event"]
        } else {
            &value
        };
        match event.get("type").and_then(Value::as_str) {
            Some("control_request") if event["request"]["subtype"] == "can_use_tool" => {
                let Some(request_id) = event["request_id"].as_str() else {
                    continue;
                };
                let permission = PermissionRequest {
                    name: event["request"]["tool_name"]
                        .as_str()
                        .unwrap_or("Tool")
                        .to_owned(),
                    input: event["request"]["input"].clone(),
                    tool_use_id: event["request"]["tool_use_id"]
                        .as_str()
                        .unwrap_or("")
                        .to_owned(),
                    title: event["request"]["title"].as_str().map(str::to_owned),
                    suggestions: event["request"]["permission_suggestions"]
                        .as_array()
                        .cloned()
                        .unwrap_or_default(),
                    suppress_always_allow_rule: event["request"]["suppress_always_allow_rule"]
                        .as_bool()
                        .unwrap_or(false),
                };
                budget.pause();
                let answer = on_permission(permission).await;
                budget.restart();
                write_message(
                    &mut stdin,
                    &json!({
                        "type": "control_response",
                        "response": {
                            "subtype": "success",
                            "request_id": request_id,
                            "response": answer
                        }
                    }),
                )
                .await?;
            }
            Some("control_request") => {
                if let Some(request_id) = event["request_id"].as_str() {
                    write_message(
                        &mut stdin,
                        &json!({
                            "type": "control_response",
                            "response": {
                                "subtype": "error",
                                "request_id": request_id,
                                "error": "Unsupported control request"
                            }
                        }),
                    )
                    .await?;
                }
            }
            Some("content_block_delta") => match event["delta"]["type"].as_str() {
                Some("text_delta") => {
                    if let Some(text) = event["delta"]["text"].as_str() {
                        saw_text_delta = true;
                        on_event(Event::Text(text.into()));
                    }
                }
                Some("thinking_delta") => {
                    if let Some(text) = event["delta"]["thinking"].as_str() {
                        saw_thought_delta = true;
                        on_event(Event::Thought(text.into()));
                    }
                }
                _ => {}
            },
            Some("assistant" | "user") => {
                for event in message_events(&value) {
                    match event {
                        // Deltas already streamed this text.
                        Event::Text(_) if saw_text_delta => {}
                        Event::Thought(_) if saw_thought_delta => {}
                        event => on_event(event),
                    }
                }
                saw_text_delta = false;
                saw_thought_delta = false;
            }
            Some("result") => {
                reported = Some(result_outcome(&value));
                break;
            }
            _ => {}
        }
    }
    drop(stdin);
    Ok(reported)
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
/// claude's stream-json output, or the same entry in its transcript. Meta
/// messages, which Claude adds for the model, and sidechain messages, which
/// belong to subagents, have none, and neither do empty text and thinking
/// blocks, which Claude records when it does not display thinking.
pub fn message_events(entry: &Value) -> Vec<Event> {
    if entry["isMeta"] == true || entry["isSidechain"] == true {
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
