//! A scripted OpenRouter for tests, and the environment that points the Ox
//! server at it. The server is the `ox-server` binary built next to the test
//! binaries.

use std::{
    collections::VecDeque,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

use serde_json::{Value, json};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
};

/// The default qualified model of the test settings, in `CATALOG`.
pub const DEFAULT_MODEL: &str = "openrouter:deepseek/deepseek-v4.1-flash";
pub const PROVIDER_MODEL: &str = "deepseek/deepseek-v4.1-flash";

/// The time `CATALOG` is written for: 2026-09-23.
const NOW: i64 = 1_790_121_600;

/// An OpenRouter `GET /models` response.
const CATALOG: &str = r#"{"data": [
    {"id": "acme/plain", "name": "Plain", "context_length": 8001, "created": 1774310400,
     "pricing": {"prompt": "0", "completion": "0"},
     "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
     "supported_parameters": ["tools"]},
    {"id": "z-ai/glm-5.3-flash", "name": "GLM 5.3 Flash", "context_length": 1310720, "created": 1788393600,
     "pricing": {"prompt": "0.00000004", "completion": "0.00000014"},
     "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["text"]},
     "supported_parameters": ["tools"],
     "reasoning": {"supported_efforts": ["max", "xhigh", "high", "medium", "low"]}},
    {"id": "deepseek/deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash", "context_length": 1048576, "created": 1789689600,
     "pricing": {"prompt": "0.00000003", "completion": "0.0000006"},
     "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
     "supported_parameters": ["reasoning", "tools"],
     "reasoning": {"supported_efforts": ["max", "high", "medium", "low"]}},
    {"id": "meta/muse-spark-1.3-contributor", "name": "Muse Spark 1.3 Contributor", "context_length": 1048576, "created": 1788998400,
     "pricing": {"prompt": "0.000001", "completion": "0.000004"},
     "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
     "supported_parameters": ["tools"],
     "reasoning": {"supported_efforts": ["xhigh", "high", "medium", "none"]}}
]}"#;

pub enum Reply {
    /// A complete SSE body, one HTTP chunk per event.
    Stream(String),
    /// A status code with a JSON body.
    Status(u16, String),
    /// The start of an SSE body, then the connection stays open forever.
    Hang(String),
    /// A reply built from the request it answers.
    From(Box<dyn FnOnce(&Value) -> Reply + Send>),
}

/// A local HTTP server that answers each request with the next scripted
/// reply. The first reply answers the server's catalog request.
pub struct Server {
    url: String,
}

impl Server {
    /// Serves the catalog, then `replies` in request order.
    pub async fn start(replies: Vec<Reply>) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let replies = Arc::new(Mutex::new(VecDeque::from(replies)));
        replies.lock().unwrap().push_front(catalog_reply());
        tokio::spawn(async move {
            loop {
                let (socket, _) = listener.accept().await.unwrap();
                tokio::spawn(serve(socket, replies.clone()));
            }
        });
        Self { url }
    }

    pub fn endpoint(&self) -> &str {
        &self.url
    }
}

async fn serve(mut socket: TcpStream, replies: Arc<Mutex<VecDeque<Reply>>>) {
    while let Some(body) = read_request(&mut socket).await {
        let request = if body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&body).unwrap()
        };
        let Some(mut reply) = replies.lock().unwrap().pop_front() else {
            break;
        };
        while let Reply::From(build) = reply {
            reply = build(&request);
        }
        match reply {
            Reply::Stream(body) => socket.write_all(stream(&body).as_bytes()).await.unwrap(),
            Reply::Status(status, body) => {
                let response = format!(
                    "HTTP/1.1 {status} Error\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
                    body.len()
                );
                socket.write_all(response.as_bytes()).await.unwrap();
            }
            Reply::Hang(prefix) => {
                let head = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\
                     Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n{:x}\r\n{prefix}\r\n",
                    prefix.len()
                );
                socket.write_all(head.as_bytes()).await.unwrap();
                std::future::pending::<()>().await;
            }
            Reply::From(_) => unreachable!("resolved above"),
        }
    }
    socket.shutdown().await.ok();
}

/// One request's body; `None` once the client closes the connection.
async fn read_request(socket: &mut TcpStream) -> Option<Vec<u8>> {
    let mut bytes = Vec::new();
    let mut chunk = [0u8; 4096];
    loop {
        let read = socket.read(&mut chunk).await.ok()?;
        if read == 0 {
            return None;
        }
        bytes.extend_from_slice(&chunk[..read]);
        let Some(end) = bytes.windows(4).position(|window| window == b"\r\n\r\n") else {
            continue;
        };
        let headers = String::from_utf8_lossy(&bytes[..end]).to_ascii_lowercase();
        let length = headers
            .lines()
            .find_map(|line| line.strip_prefix("content-length:"))
            .map_or(0, |value| value.trim().parse::<usize>().unwrap());
        let body_start = end + 4;
        if bytes.len() >= body_start + length {
            return Some(bytes[body_start..body_start + length].to_vec());
        }
    }
}

/// A chunked SSE response with each event in its own HTTP chunk, the way
/// OpenRouter delivers a stream it is still generating.
fn stream(body: &str) -> String {
    let mut response = String::from(
        "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n",
    );
    for event in body.split_inclusive("\n\n") {
        response.push_str(&format!("{:x}\r\n{event}\r\n", event.len()));
    }
    response.push_str("0\r\n\r\n");
    response
}

/// An SSE body: a keep-alive comment, one data event per chunk, `[DONE]`.
pub fn sse(chunks: &[Value]) -> String {
    let mut body = String::from(": OPENROUTER PROCESSING\n\n");
    for chunk in chunks {
        body.push_str(&format!("data: {chunk}\n\n"));
    }
    body.push_str("data: [DONE]\n\n");
    body
}

pub fn delta(delta: Value, finish_reason: Option<&str>) -> Value {
    json!({
        "id": "gen-1",
        "object": "chat.completion.chunk",
        "model": PROVIDER_MODEL,
        "choices": [{ "index": 0, "delta": delta, "finish_reason": finish_reason }],
    })
}

/// A usage chunk like the one OpenRouter sends after the finish chunk.
pub fn usage(input: u64, output: u64, cost: f64) -> Value {
    json!({
        "id": "gen-1",
        "object": "chat.completion.chunk",
        "model": PROVIDER_MODEL,
        "choices": [{ "index": 0, "delta": { "content": "" }, "finish_reason": "stop" }],
        "usage": { "prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output, "cost": cost },
    })
}

pub fn text_reply(text: &str) -> Reply {
    Reply::Stream(sse(&[
        delta(json!({ "role": "assistant", "content": text }), None),
        delta(json!({}), Some("stop")),
    ]))
}

/// A reply that echoes the latest user message.
pub fn echo_reply() -> Reply {
    Reply::From(Box::new(|request| {
        let text = request["messages"]
            .as_array()
            .and_then(|messages| {
                messages
                    .iter()
                    .rev()
                    .find(|message| message["role"] == "user")
            })
            .and_then(|message| message["content"].as_str())
            .expect("an echo request has a user message");
        text_reply(&format!("you said: {text}"))
    }))
}

/// One assistant message with one shell call per `(command, timeout)`.
pub fn shell_reply(commands: &[(&str, u64)]) -> Reply {
    let calls: Vec<_> = commands
        .iter()
        .enumerate()
        .map(|(index, (command, seconds))| {
            json!({
                "index": index, "id": format!("shell-{index}"), "type": "function",
                "function": {"name": "shell", "arguments": json!({
                    "command": command, "timeout_seconds": seconds
                }).to_string()}
            })
        })
        .collect();
    Reply::Stream(sse(&[delta(
        json!({"role":"assistant", "tool_calls":calls}),
        Some("tool_calls"),
    )]))
}

/// The catalog shifted so its model ages match `NOW` today.
fn catalog_reply() -> Reply {
    let mut catalog: Value = serde_json::from_str(CATALOG).unwrap();
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    for model in catalog["data"].as_array_mut().unwrap() {
        let created = model["created"].as_i64().unwrap();
        model["created"] = (created + now - NOW).into();
    }
    Reply::Status(200, catalog.to_string())
}

/// The `ox-server` binary built next to the test binaries, which run from
/// the profile's `deps` directory.
pub fn server_binary() -> PathBuf {
    let exe = std::env::current_exe().unwrap();
    let profile = exe.parent().unwrap().parent().unwrap();
    let server = profile.join("ox-server");
    assert!(
        server.exists(),
        "{} is missing; build it with `make server`",
        server.display()
    );
    server
}

/// A home directory whose settings name `DEFAULT_MODEL`, a data directory,
/// and the variables that point the Ox server at `endpoint`.
pub fn environment(root: &Path, endpoint: &str) -> Vec<(String, String)> {
    let config = root.join(".config/ox");
    std::fs::create_dir_all(&config).unwrap();
    std::fs::write(
        config.join("settings.json"),
        json!({"model": DEFAULT_MODEL}).to_string(),
    )
    .unwrap();
    [
        ("HOME", root.display().to_string()),
        ("OX_DATA_DIR", root.join("data").display().to_string()),
        ("OPENROUTER_API_KEY", "test-key".to_owned()),
        ("OX_OPENROUTER_ENDPOINT", endpoint.to_owned()),
    ]
    .into_iter()
    .map(|(name, value)| (name.to_owned(), value))
    .collect()
}
