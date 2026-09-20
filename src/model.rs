//! One OpenRouter chat completion at a time, streamed over server-sent events.

use std::{
    collections::{BTreeMap, VecDeque},
    io::{self, ErrorKind},
};

use serde::Deserialize;
use serde_json::{Value, json};

use crate::{
    sessions::{AssistantMessage, ToolCall, TranscriptEvent},
    tools,
};

pub const MODEL: &str = "openai/gpt-5.6-luna";
const ENDPOINT: &str = "https://openrouter.ai/api/v1";

/// Immutable credentials and a connection pool; safe to clone per prompt.
#[derive(Clone)]
pub struct ModelClient {
    http: reqwest::Client,
    api_key: String,
    endpoint: String,
}

#[derive(Debug, Clone, PartialEq)]
pub enum ModelEvent {
    TextDelta(String),
    ReasoningDelta(String),
    Completed(ModelCompletion),
}

#[derive(Debug, Clone, PartialEq)]
pub struct ModelCompletion {
    pub message: AssistantMessage,
    pub stop: ModelStop,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ModelStop {
    Finished,
    ToolCalls,
    TokenLimit,
    Refused,
}

impl ModelClient {
    pub fn new(api_key: String) -> Self {
        Self::for_endpoint(api_key, ENDPOINT.to_owned())
    }

    fn for_endpoint(api_key: String, endpoint: String) -> Self {
        Self {
            http: reqwest::Client::new(),
            api_key,
            endpoint,
        }
    }

    /// Checks the key against OpenRouter's key endpoint without generating.
    pub async fn verify(&self) -> io::Result<()> {
        let response = self
            .http
            .get(format!("{}/key", self.endpoint))
            .bearer_auth(&self.api_key)
            .send()
            .await
            .map_err(transport)?;
        match response.status() {
            status if status.is_success() => Ok(()),
            reqwest::StatusCode::UNAUTHORIZED => Err(io::Error::new(
                ErrorKind::PermissionDenied,
                "OpenRouter rejected the API key",
            )),
            status => Err(io::Error::other(format!(
                "OpenRouter key check returned {status}"
            ))),
        }
    }

    /// Starts one streamed completion over `history`. Read the returned
    /// request until it yields a completion.
    pub async fn complete(&self, history: &[TranscriptEvent]) -> io::Result<ModelRequest> {
        let body = json!({
            "model": MODEL,
            "messages": request_messages(history)?,
            "tools": tools::schemas(),
            "stream": true,
        });
        let response = self
            .http
            .post(format!("{}/chat/completions", self.endpoint))
            .bearer_auth(&self.api_key)
            .json(&body)
            .send()
            .await
            .map_err(transport)?;
        let status = response.status();
        if !status.is_success() {
            let detail = response.text().await.map_err(transport)?;
            return Err(io::Error::other(format!(
                "OpenRouter returned {status}: {}",
                detail.trim()
            )));
        }
        Ok(ModelRequest {
            response,
            buffer: Vec::new(),
            data: String::new(),
            assembly: Some(Assembly::default()),
            pending: VecDeque::new(),
        })
    }
}

/// Encodes accepted history as chat messages. Continuation metadata is sent
/// back only for the model that produced it; visible reasoning is sent only
/// when there is no structured metadata carrying it.
pub(crate) fn request_messages(history: &[TranscriptEvent]) -> io::Result<Vec<Value>> {
    history
        .iter()
        .map(|event| {
            Ok(match event {
                TranscriptEvent::UserMessage(text) => json!({ "role": "user", "content": text }),
                TranscriptEvent::AssistantMessage(message) => {
                    if !message.reasoning_details.is_empty() && message.model != MODEL {
                        return Err(io::Error::new(
                            ErrorKind::Unsupported,
                            format!(
                                "stored reasoning from model {} cannot continue with {MODEL}",
                                message.model
                            ),
                        ));
                    }
                    let content = if message.text.is_empty() {
                        Value::Null
                    } else {
                        Value::String(message.text.clone())
                    };
                    let mut value = json!({ "role": "assistant", "content": content });
                    if !message.tool_calls.is_empty() {
                        value["tool_calls"] = message
                            .tool_calls
                            .iter()
                            .map(|call| {
                                json!({
                                    "id": call.call_id,
                                    "type": "function",
                                    "function": { "name": call.name, "arguments": call.arguments },
                                })
                            })
                            .collect();
                    }
                    if !message.reasoning_details.is_empty() {
                        value["reasoning_details"] =
                            Value::Array(message.reasoning_details.clone());
                    } else if !message.reasoning.is_empty() {
                        value["reasoning"] = Value::String(message.reasoning.clone());
                    }
                    value
                }
                TranscriptEvent::ToolResult(result) => json!({
                    "role": "tool",
                    "tool_call_id": result.call_id,
                    "content": result.outcome.text(),
                }),
            })
        })
        .collect()
}

fn transport(error: reqwest::Error) -> io::Error {
    io::Error::other(format!("OpenRouter request failed: {error}"))
}

fn malformed(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message.into())
}

/// An in-flight streamed completion. All assembly state lives here, so
/// dropping the request stops consumption and discards provisional output.
pub struct ModelRequest {
    response: reqwest::Response,
    buffer: Vec<u8>,
    data: String,
    assembly: Option<Assembly>,
    pending: VecDeque<ModelEvent>,
}

impl ModelRequest {
    /// The next provisional delta or the single completion. `Ok(None)` is
    /// the end of a stream that has already delivered its completion; a
    /// stream ending before that is an error.
    pub async fn next(&mut self) -> io::Result<Option<ModelEvent>> {
        loop {
            if let Some(event) = self.pending.pop_front() {
                return Ok(Some(event));
            }
            let Some(chunk) = self.response.chunk().await.map_err(transport)? else {
                return if self.assembly.is_none() {
                    Ok(None)
                } else {
                    Err(io::Error::new(
                        ErrorKind::UnexpectedEof,
                        "model stream ended before the response finished",
                    ))
                };
            };
            self.buffer.extend_from_slice(&chunk);
            while let Some(newline) = self.buffer.iter().position(|&byte| byte == b'\n') {
                let line = self.buffer.drain(..=newline).collect::<Vec<u8>>();
                let line = std::str::from_utf8(&line)
                    .map_err(|_| malformed("model stream is not UTF-8"))?
                    .trim_end_matches(['\r', '\n']);
                self.line(line)?;
            }
        }
    }

    fn line(&mut self, line: &str) -> io::Result<()> {
        if line.is_empty() {
            if !self.data.is_empty() {
                let data = std::mem::take(&mut self.data);
                self.event(&data)?;
            }
        } else if let Some(data) = line.strip_prefix("data:") {
            if !self.data.is_empty() {
                self.data.push('\n');
            }
            self.data.push_str(data.strip_prefix(' ').unwrap_or(data));
        }
        Ok(())
    }

    fn event(&mut self, data: &str) -> io::Result<()> {
        if data == "[DONE]" {
            return Ok(());
        }
        let chunk: Chunk = serde_json::from_str(data)
            .map_err(|error| malformed(format!("malformed model stream chunk: {error}")))?;
        if let Some(error) = chunk.error {
            return Err(io::Error::other(format!(
                "OpenRouter reported an error: {} ({})",
                error.message, error.code
            )));
        }
        // The usage chunk after the final one repeats the finish reason with
        // an empty delta; nothing after acceptance changes the message.
        let Some(assembly) = self.assembly.as_mut() else {
            return Ok(());
        };
        let Some(choice) = chunk.choices.into_iter().next() else {
            return Ok(());
        };
        if let Some(text) = choice.delta.content.filter(|text| !text.is_empty()) {
            assembly.text.push_str(&text);
            self.pending.push_back(ModelEvent::TextDelta(text));
        }
        if let Some(reasoning) = choice.delta.reasoning.filter(|text| !text.is_empty()) {
            assembly.reasoning.push_str(&reasoning);
            self.pending
                .push_back(ModelEvent::ReasoningDelta(reasoning));
        }
        for call in choice.delta.tool_calls.into_iter().flatten() {
            assembly.tool_call(call);
        }
        for detail in choice.delta.reasoning_details.into_iter().flatten() {
            assembly.reasoning_detail(detail);
        }
        if let Some(reason) = choice.finish_reason.filter(|reason| !reason.is_empty()) {
            let assembly = self
                .assembly
                .take()
                .expect("assembly is present until acceptance");
            self.pending
                .push_back(ModelEvent::Completed(assembly.finish(&reason)?));
        }
        Ok(())
    }
}

#[derive(Default)]
struct Assembly {
    text: String,
    reasoning: String,
    tool_calls: BTreeMap<u64, PartialCall>,
    reasoning_details: Vec<Value>,
}

#[derive(Default)]
struct PartialCall {
    id: String,
    name: String,
    arguments: String,
}

impl Assembly {
    fn tool_call(&mut self, delta: ToolCallDelta) {
        let call = self.tool_calls.entry(delta.index).or_default();
        if let Some(id) = delta.id.filter(|id| !id.is_empty()) {
            call.id = id;
        }
        if let Some(name) = delta.function.name.filter(|name| !name.is_empty()) {
            call.name = name;
        }
        if let Some(arguments) = delta.function.arguments {
            call.arguments.push_str(&arguments);
        }
    }

    /// Streaming indices can repeat across distinct reasoning blocks. Only
    /// consecutive text/summary fragments merge; encrypted blocks stay opaque.
    fn reasoning_detail(&mut self, detail: Value) {
        let kind = detail.get("type").and_then(Value::as_str);
        if matches!(kind, Some("reasoning.text" | "reasoning.summary"))
            && let Some(target) = self
                .reasoning_details
                .last_mut()
                .filter(|existing| {
                    existing.get("type").and_then(Value::as_str) == kind
                        && ["id", "index"].into_iter().all(|field| {
                            let previous = existing.get(field).filter(|value| !value.is_null());
                            let incoming = detail.get(field).filter(|value| !value.is_null());
                            previous.zip(incoming).is_none_or(|(a, b)| a == b)
                        })
                })
                .and_then(Value::as_object_mut)
            && let Some(source) = detail.as_object()
        {
            for (field, value) in source {
                if value.is_null() {
                    continue;
                }
                if matches!(field.as_str(), "text" | "summary")
                    && let (Some(Value::String(current)), Value::String(fragment)) =
                        (target.get_mut(field), value)
                {
                    current.push_str(fragment);
                    continue;
                }
                target.insert(field.clone(), value.clone());
            }
            return;
        }
        self.reasoning_details.push(detail);
    }

    fn finish(self, reason: &str) -> io::Result<ModelCompletion> {
        let tool_calls = self
            .tool_calls
            .into_values()
            .map(|call| ToolCall {
                call_id: call.id,
                name: call.name,
                arguments: call.arguments,
            })
            .collect::<Vec<_>>();
        let has_calls = !tool_calls.is_empty();
        let stop = match (reason, has_calls) {
            ("stop", false) => ModelStop::Finished,
            ("stop" | "tool_calls", true) => ModelStop::ToolCalls,
            ("tool_calls", false) => {
                return Err(malformed("finish reason tool_calls without any tool call"));
            }
            ("length", false) => ModelStop::TokenLimit,
            ("content_filter", false) => ModelStop::Refused,
            ("length" | "content_filter", true) => {
                return Err(malformed(format!(
                    "finish reason {reason} with tool calls is not supported"
                )));
            }
            ("error", _) => {
                return Err(io::Error::other(
                    "OpenRouter reported an error while generating",
                ));
            }
            _ => return Err(malformed(format!("unknown finish reason {reason:?}"))),
        };
        let message = AssistantMessage {
            model: MODEL.to_owned(),
            text: self.text,
            reasoning: self.reasoning,
            tool_calls,
            reasoning_details: self.reasoning_details,
        };
        message.validate().map_err(|error| {
            malformed(format!("incomplete tool call in model response: {error}"))
        })?;
        Ok(ModelCompletion { message, stop })
    }
}

#[derive(Deserialize)]
struct Chunk {
    #[serde(default)]
    choices: Vec<Choice>,
    error: Option<ApiError>,
}

#[derive(Deserialize)]
struct Choice {
    #[serde(default)]
    delta: Delta,
    finish_reason: Option<String>,
}

#[derive(Deserialize, Default)]
struct Delta {
    content: Option<String>,
    reasoning: Option<String>,
    tool_calls: Option<Vec<ToolCallDelta>>,
    reasoning_details: Option<Vec<Value>>,
}

#[derive(Deserialize)]
struct ToolCallDelta {
    #[serde(default)]
    index: u64,
    id: Option<String>,
    #[serde(default)]
    function: FunctionDelta,
}

#[derive(Deserialize, Default)]
struct FunctionDelta {
    name: Option<String>,
    arguments: Option<String>,
}

#[derive(Deserialize)]
struct ApiError {
    #[serde(default)]
    code: Value,
    #[serde(default)]
    message: String,
}

/// A local HTTP server that answers each connection with the next scripted
/// reply and records the request bodies it received.
#[cfg(test)]
pub(crate) mod fixture {
    use std::sync::{Arc, Mutex};

    use serde_json::{Value, json};
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::{TcpListener, TcpStream},
    };

    use super::{MODEL, ModelClient};

    pub enum Reply {
        /// A complete SSE body, then the connection closes.
        Stream(String),
        /// A status code with a JSON body.
        Status(u16, String),
        /// The start of an SSE body, then the connection stays open forever.
        Hang(String),
    }

    pub struct Server {
        url: String,
        requests: Arc<Mutex<Vec<Value>>>,
    }

    impl Server {
        pub async fn start(replies: Vec<Reply>) -> Self {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let requests = Arc::new(Mutex::new(Vec::new()));
            let seen = requests.clone();
            tokio::spawn(async move {
                for reply in replies {
                    let (mut socket, _) = listener.accept().await.unwrap();
                    let body = read_request(&mut socket).await;
                    if !body.is_empty() {
                        seen.lock()
                            .unwrap()
                            .push(serde_json::from_slice(&body).unwrap());
                    }
                    match reply {
                        Reply::Stream(body) => {
                            socket
                                .write_all(response(200, "text/event-stream", &body).as_bytes())
                                .await
                                .unwrap();
                        }
                        Reply::Status(status, body) => {
                            socket
                                .write_all(response(status, "application/json", &body).as_bytes())
                                .await
                                .unwrap();
                        }
                        Reply::Hang(prefix) => {
                            let head = format!(
                                "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\
                                 Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n\
                                 {:x}\r\n{prefix}\r\n",
                                prefix.len()
                            );
                            socket.write_all(head.as_bytes()).await.unwrap();
                            std::future::pending::<()>().await;
                        }
                    }
                    socket.shutdown().await.ok();
                }
            });
            Self { url, requests }
        }

        pub fn client(&self) -> ModelClient {
            ModelClient::for_endpoint("test-key".to_owned(), self.url.clone())
        }

        pub fn requests(&self) -> Vec<Value> {
            self.requests.lock().unwrap().clone()
        }
    }

    async fn read_request(socket: &mut TcpStream) -> Vec<u8> {
        let mut bytes = Vec::new();
        let mut chunk = [0u8; 4096];
        loop {
            let read = socket.read(&mut chunk).await.unwrap();
            if read == 0 {
                return Vec::new();
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
                return bytes[body_start..body_start + length].to_vec();
            }
        }
    }

    fn response(status: u16, content_type: &str, body: &str) -> String {
        let reason = match status {
            200 => "OK",
            401 => "Unauthorized",
            _ => "Error",
        };
        format!(
            "HTTP/1.1 {status} {reason}\r\nContent-Type: {content_type}\r\n\
             Content-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        )
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
            "model": MODEL,
            "choices": [{ "index": 0, "delta": delta, "finish_reason": finish_reason }],
        })
    }

    pub fn text_reply(text: &str) -> Reply {
        Reply::Stream(sse(&[
            delta(json!({ "role": "assistant", "content": text }), None),
            delta(json!({}), Some("stop")),
        ]))
    }

    /// One assistant message calling `get_weather` once per `(call_id, location)`.
    pub fn tool_reply(calls: &[(&str, &str)]) -> Reply {
        let tool_calls = calls
            .iter()
            .enumerate()
            .map(|(index, (id, location))| {
                json!({
                    "index": index,
                    "id": id,
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        "arguments": json!({ "location": location }).to_string(),
                    },
                })
            })
            .collect::<Vec<_>>();
        Reply::Stream(sse(&[delta(
            json!({ "role": "assistant", "tool_calls": tool_calls }),
            Some("tool_calls"),
        )]))
    }
}

#[cfg(test)]
mod tests {
    use super::{
        fixture::{Reply, Server, delta, sse},
        *,
    };
    use crate::sessions::{ToolOutcome, ToolResult};

    async fn drain(request: &mut ModelRequest) -> io::Result<Vec<ModelEvent>> {
        let mut events = Vec::new();
        while let Some(event) = request.next().await? {
            events.push(event);
        }
        Ok(events)
    }

    async fn complete_with(chunks: &[Value]) -> io::Result<Vec<ModelEvent>> {
        let server = Server::start(vec![Reply::Stream(sse(chunks))]).await;
        let mut request = server
            .client()
            .complete(&[TranscriptEvent::UserMessage("hi".to_owned())])
            .await?;
        drain(&mut request).await
    }

    fn completion(events: &[ModelEvent]) -> ModelCompletion {
        let completions = events
            .iter()
            .filter_map(|event| match event {
                ModelEvent::Completed(completion) => Some(completion.clone()),
                _ => None,
            })
            .collect::<Vec<_>>();
        assert_eq!(completions.len(), 1, "exactly one completion is accepted");
        completions.into_iter().next().unwrap()
    }

    fn call(id: &str, location: &str) -> ToolCall {
        ToolCall {
            call_id: id.to_owned(),
            name: "get_weather".to_owned(),
            arguments: json!({ "location": location }).to_string(),
        }
    }

    #[tokio::test]
    async fn requests_group_messages_and_send_continuation_metadata_once() {
        let server = Server::start(vec![Reply::Stream(sse(&[delta(
            json!({ "role": "assistant", "content": "Both sunny." }),
            Some("stop"),
        )]))])
        .await;
        let details = vec![json!({
            "type": "reasoning.encrypted",
            "data": "opaque",
            "id": "rs_1",
            "format": "openai-responses-v1",
            "index": 0,
        })];
        let history = vec![
            TranscriptEvent::UserMessage("Weather in Chicago and Denver?".to_owned()),
            TranscriptEvent::AssistantMessage(AssistantMessage {
                model: MODEL.to_owned(),
                text: String::new(),
                reasoning: "Need both cities.".to_owned(),
                tool_calls: vec![call("call-1", "Chicago"), call("call-2", "Denver")],
                reasoning_details: details.clone(),
            }),
            TranscriptEvent::ToolResult(ToolResult {
                call_id: "call-1".to_owned(),
                name: "get_weather".to_owned(),
                outcome: ToolOutcome::Completed("Sunny.".to_owned()),
            }),
            TranscriptEvent::ToolResult(ToolResult {
                call_id: "call-2".to_owned(),
                name: "get_weather".to_owned(),
                outcome: ToolOutcome::Failed("Unavailable.".to_owned()),
            }),
        ];

        let mut request = server.client().complete(&history).await.unwrap();
        let events = drain(&mut request).await.unwrap();
        assert_eq!(completion(&events).stop, ModelStop::Finished);

        let body = &server.requests()[0];
        assert_eq!(body["model"], MODEL);
        assert_eq!(body["stream"], true);
        assert_eq!(body["tools"][0]["function"]["name"], "get_weather");
        let messages = body["messages"].as_array().unwrap();
        assert_eq!(messages.len(), 4);
        assert_eq!(
            messages[0],
            json!({ "role": "user", "content": "Weather in Chicago and Denver?" })
        );
        assert_eq!(messages[1]["role"], "assistant");
        assert_eq!(messages[1]["content"], Value::Null);
        assert_eq!(messages[1]["tool_calls"].as_array().unwrap().len(), 2);
        assert_eq!(
            messages[1]["tool_calls"][0]["function"]["name"],
            "get_weather"
        );
        assert_eq!(messages[1]["tool_calls"][1]["id"], "call-2");
        assert_eq!(messages[1]["reasoning_details"], json!(details));
        assert!(messages[1].get("reasoning").is_none());
        assert_eq!(
            messages[2],
            json!({ "role": "tool", "tool_call_id": "call-1", "content": "Sunny." })
        );
        assert_eq!(messages[3]["tool_call_id"], "call-2");
    }

    #[test]
    fn visible_reasoning_is_sent_only_without_metadata_and_only_for_this_model() {
        let plain = AssistantMessage {
            model: MODEL.to_owned(),
            text: "Four.".to_owned(),
            reasoning: "Add them.".to_owned(),
            tool_calls: vec![],
            reasoning_details: vec![],
        };
        let messages =
            request_messages(&[TranscriptEvent::AssistantMessage(plain.clone())]).unwrap();
        assert_eq!(messages[0]["reasoning"], "Add them.");
        assert!(messages[0].get("reasoning_details").is_none());
        assert!(messages[0].get("tool_calls").is_none());

        let foreign = AssistantMessage {
            model: "other/model".to_owned(),
            reasoning_details: vec![json!({ "type": "reasoning.encrypted", "data": "x" })],
            ..plain
        };
        let error = request_messages(&[TranscriptEvent::AssistantMessage(foreign)]).unwrap_err();
        assert_eq!(error.kind(), ErrorKind::Unsupported);
    }

    #[tokio::test]
    async fn streams_assemble_text_reasoning_and_fragmented_tool_calls() {
        let events = complete_with(&[
            delta(
                json!({
                    "role": "assistant",
                    "reasoning": "Let me ",
                    "reasoning_details": [{ "type": "reasoning.text", "text": "Let me ", "index": 0, "format": "x" }],
                }),
                None,
            ),
            delta(
                json!({
                    "reasoning": "check.",
                    "reasoning_details": [{ "type": "reasoning.text", "text": "check.", "index": 0, "format": "x", "signature": "sig" }],
                }),
                None,
            ),
            delta(
                json!({
                    "reasoning_details": [{ "type": "reasoning.encrypted", "data": "blob", "id": "rs_1", "index": 1 }],
                }),
                None,
            ),
            delta(json!({ "content": "Checking " }), None),
            delta(json!({ "content": "now." }), None),
            delta(
                json!({ "tool_calls": [{ "index": 0, "id": "call-1", "type": "function", "function": { "name": "get_weather", "arguments": "{\"loc" } }] }),
                None,
            ),
            delta(
                json!({ "tool_calls": [
                    { "index": 0, "function": { "arguments": "ation\":\"Chicago\"}" } },
                    { "index": 1, "id": "call-2", "type": "function", "function": { "name": "get_weather", "arguments": "" } },
                ] }),
                None,
            ),
            delta(
                json!({ "tool_calls": [{ "index": 1, "function": { "arguments": "{\"location\":\"Denver\"}" } }] }),
                Some("tool_calls"),
            ),
            delta(json!({ "content": "" }), Some("tool_calls")),
        ])
        .await
        .unwrap();

        let deltas = events
            .iter()
            .filter_map(|event| match event {
                ModelEvent::TextDelta(text) => Some(("text", text.as_str())),
                ModelEvent::ReasoningDelta(text) => Some(("reasoning", text.as_str())),
                ModelEvent::Completed(_) => None,
            })
            .collect::<Vec<_>>();
        assert_eq!(
            deltas,
            vec![
                ("reasoning", "Let me "),
                ("reasoning", "check."),
                ("text", "Checking "),
                ("text", "now."),
            ]
        );
        let completion = completion(&events);
        assert_eq!(completion.stop, ModelStop::ToolCalls);
        assert_eq!(
            completion.message,
            AssistantMessage {
                model: MODEL.to_owned(),
                text: "Checking now.".to_owned(),
                reasoning: "Let me check.".to_owned(),
                tool_calls: vec![call("call-1", "Chicago"), call("call-2", "Denver")],
                reasoning_details: vec![
                    json!({ "type": "reasoning.text", "text": "Let me check.", "index": 0, "format": "x", "signature": "sig" }),
                    json!({ "type": "reasoning.encrypted", "data": "blob", "id": "rs_1", "index": 1 }),
                ],
            }
        );
        assert!(matches!(events.last(), Some(ModelEvent::Completed(_))));
    }

    #[tokio::test]
    async fn reasoning_blocks_survive_reused_or_missing_stream_indices() {
        for (kind, field) in [("reasoning.summary", "summary"), ("reasoning.text", "text")] {
            for index in [Some(0), None] {
                let detail = |kind: &str, field: &str, value: &str| {
                    let mut detail = json!({ "type": kind, field: value });
                    if let Some(index) = index {
                        detail["index"] = json!(index);
                    }
                    detail
                };
                let mut first_encrypted = detail("reasoning.encrypted", "data", "opaque-first");
                first_encrypted["id"] = json!("rs_first");
                first_encrypted["extra"] = json!({ "nested": [1, 2] });
                let mut second_encrypted = detail("reasoning.encrypted", "data", "opaque-second");
                second_encrypted["id"] = json!("rs_second");
                let mut third_encrypted = detail("reasoning.encrypted", "data", "opaque-third");
                third_encrypted["id"] = json!("rs_third");
                let fragments = vec![
                    detail(kind, field, "First "),
                    detail(kind, field, "block."),
                    first_encrypted.clone(),
                    detail(kind, field, "Second "),
                    detail(kind, field, "block."),
                    second_encrypted.clone(),
                    third_encrypted.clone(),
                ];
                let mut chunks = fragments
                    .into_iter()
                    .map(|detail| delta(json!({ "reasoning_details": [detail] }), None))
                    .collect::<Vec<_>>();
                chunks.push(delta(json!({ "content": "Done." }), Some("stop")));
                let events = complete_with(&chunks).await.unwrap();
                let message = completion(&events).message;
                let expected = vec![
                    detail(kind, field, "First block."),
                    first_encrypted,
                    detail(kind, field, "Second block."),
                    second_encrypted,
                    third_encrypted,
                ];
                assert_eq!(message.reasoning_details, expected);
                let messages =
                    request_messages(&[TranscriptEvent::AssistantMessage(message)]).unwrap();
                assert_eq!(messages[0]["reasoning_details"], json!(expected));
            }
        }
    }

    #[tokio::test]
    async fn adjacent_reasoning_blocks_keep_distinct_ids_and_indices() {
        for (field, first, second) in [
            ("id", json!("rs_first"), json!("rs_second")),
            ("index", json!(0), json!(1)),
        ] {
            let details = vec![
                json!({ "type": "reasoning.summary", "summary": "First.", field: first }),
                json!({ "type": "reasoning.summary", "summary": "Second.", field: second }),
            ];
            let mut chunks = details
                .iter()
                .map(|detail| delta(json!({ "reasoning_details": [detail] }), None))
                .collect::<Vec<_>>();
            chunks.push(delta(json!({}), Some("stop")));
            let events = complete_with(&chunks).await.unwrap();
            assert_eq!(completion(&events).message.reasoning_details, details);
        }
    }

    #[tokio::test]
    async fn finish_reasons_classify_or_fail_acceptance() {
        let finished = complete_with(&[delta(json!({ "content": "Done." }), Some("stop"))])
            .await
            .unwrap();
        assert_eq!(completion(&finished).stop, ModelStop::Finished);

        let limited = complete_with(&[delta(json!({ "content": "Cut" }), Some("length"))])
            .await
            .unwrap();
        assert_eq!(completion(&limited).stop, ModelStop::TokenLimit);

        let refused = complete_with(&[delta(json!({}), Some("content_filter"))])
            .await
            .unwrap();
        assert_eq!(completion(&refused).stop, ModelStop::Refused);

        assert!(
            complete_with(&[delta(json!({}), Some("tool_calls"))])
                .await
                .is_err()
        );
        let call = json!({ "tool_calls": [{ "index": 0, "id": "call-1", "function": { "name": "get_weather", "arguments": "{" } }] });
        assert!(
            complete_with(&[delta(call.clone(), Some("length"))])
                .await
                .is_err()
        );
        assert!(
            complete_with(&[delta(json!({}), Some("weird"))])
                .await
                .is_err()
        );
        assert!(
            complete_with(&[delta(json!({}), Some("error"))])
                .await
                .is_err()
        );
    }

    #[tokio::test]
    async fn incomplete_or_malformed_streams_are_errors_and_partial_calls_never_complete() {
        let unfinished = complete_with(&[delta(json!({ "content": "Hello" }), None)])
            .await
            .unwrap_err();
        assert_eq!(unfinished.kind(), ErrorKind::UnexpectedEof);

        let nameless = json!({ "tool_calls": [{ "index": 0, "id": "call-1", "function": { "arguments": "{}" } }] });
        assert!(
            complete_with(&[delta(nameless, Some("tool_calls"))])
                .await
                .is_err()
        );

        let anonymous = json!({ "tool_calls": [{ "index": 0, "function": { "name": "get_weather", "arguments": "{}" } }] });
        assert!(
            complete_with(&[delta(anonymous, Some("tool_calls"))])
                .await
                .is_err()
        );

        let mid_stream_error = json!({
            "id": "gen-1",
            "error": { "code": "server_error", "message": "Provider disconnected" },
            "choices": [{ "index": 0, "delta": { "content": "" }, "finish_reason": "error" }],
        });
        let error = complete_with(&[delta(json!({ "content": "Hel" }), None), mid_stream_error])
            .await
            .unwrap_err();
        assert!(error.to_string().contains("Provider disconnected"));

        let server = Server::start(vec![Reply::Stream("data: not json\n\n".to_owned())]).await;
        let mut request = server.client().complete(&[]).await.unwrap();
        assert!(drain(&mut request).await.is_err());

        let server = Server::start(vec![Reply::Status(
            429,
            r#"{"error":"slow down"}"#.to_owned(),
        )])
        .await;
        let error = server
            .client()
            .complete(&[])
            .await
            .err()
            .expect("a failed status is an error");
        assert!(error.to_string().contains("429"));
    }

    #[tokio::test]
    async fn verify_checks_the_key_without_generating() {
        let server = Server::start(vec![
            Reply::Status(200, r#"{"data":{"label":"ok"}}"#.to_owned()),
            Reply::Status(401, r#"{"error":{"message":"bad key"}}"#.to_owned()),
        ])
        .await;
        let client = server.client();
        client.verify().await.unwrap();
        assert_eq!(
            client.verify().await.unwrap_err().kind(),
            ErrorKind::PermissionDenied
        );
        assert!(server.requests().is_empty(), "verification sends no body");
    }
}
