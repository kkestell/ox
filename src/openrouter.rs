//! Sends model requests to OpenRouter and assembles their streamed responses.

use std::{
    collections::{BTreeMap, VecDeque},
    io::{self, ErrorKind},
    time::Duration,
};

use serde::Deserialize;
use serde_json::{Value, json};

use crate::{
    sessions::{
        AssistantMessage, EffortLevel, HookFeedback, ImageAttachment, ModelUsage, SkillInvocation,
        ToolCall, TranscriptEntry, UserMessage, UserMessagePart,
    },
    tools,
};

#[derive(Debug)]
pub struct CatalogModel {
    pub id: String,
    pub name: String,
    pub context_limit: usize,
    pub accepts_images: bool,
    /// `Default` followed by the efforts OpenRouter lists, in ascending order.
    pub efforts: Vec<EffortLevel>,
}

impl CatalogModel {
    pub fn supports(&self, effort: EffortLevel) -> bool {
        self.efforts.contains(&effort)
    }

    /// The lowest effort that still reasons, for summarizer requests.
    pub fn summarizer_effort(&self) -> EffortLevel {
        self.efforts
            .iter()
            .copied()
            .find(|effort| !matches!(effort, EffortLevel::Default | EffortLevel::None))
            .unwrap_or(EffortLevel::Default)
    }
}

/// The fields Ox reads from one entry of OpenRouter's `GET /models`.
#[derive(Deserialize)]
struct OpenRouterModel {
    id: String,
    name: String,
    context_length: usize,
    /// Unix seconds when OpenRouter added the model.
    created: i64,
    architecture: Architecture,
    supported_parameters: Vec<String>,
    #[serde(default)]
    reasoning: Option<Reasoning>,
}

#[derive(Deserialize)]
struct Architecture {
    input_modalities: Vec<String>,
    output_modalities: Vec<String>,
}

#[derive(Deserialize)]
struct Reasoning {
    #[serde(default)]
    supported_efforts: Option<Vec<String>>,
}

#[derive(Deserialize)]
struct ModelsResponse {
    data: Vec<OpenRouterModel>,
}

/// Parses OpenRouter's `GET /models` response and applies the catalog filter:
/// a model must not be a `:batch` variant, which the chat-completions endpoint
/// does not serve, and must accept tools, take and produce text, have a context
/// limit above
/// the 8,000 tokens compaction reserves, and have been released within
/// `RECENT_SECONDS` of `now`. Efforts Ox does not know are dropped. Models are
/// sorted by name.
/// About six months.
const RECENT_SECONDS: i64 = 183 * 24 * 60 * 60;

pub fn parse_catalog(text: &str, now: i64) -> io::Result<Vec<CatalogModel>> {
    let response: ModelsResponse = serde_json::from_str(text).map_err(|error| {
        io::Error::new(
            ErrorKind::InvalidData,
            format!("malformed OpenRouter model catalog: {error}"),
        )
    })?;
    let has = |values: &[String], wanted: &str| values.iter().any(|value| value == wanted);
    let mut models = response
        .data
        .into_iter()
        .filter(|model| {
            !model.id.ends_with(":batch")
                && has(&model.supported_parameters, "tools")
                && has(&model.architecture.input_modalities, "text")
                && has(&model.architecture.output_modalities, "text")
                && model.context_length > 8_000
                && model.created >= now - RECENT_SECONDS
        })
        .map(|model| {
            let listed = model
                .reasoning
                .and_then(|reasoning| reasoning.supported_efforts)
                .unwrap_or_default();
            let efforts = EffortLevel::ALL
                .into_iter()
                .filter(|effort| *effort == EffortLevel::Default || has(&listed, effort.id()))
                .collect();
            CatalogModel {
                id: model.id,
                name: model.name,
                context_limit: model.context_length,
                accepts_images: has(&model.architecture.input_modalities, "image"),
                efforts,
            }
        })
        .collect::<Vec<_>>();
    models.sort_by_key(|model| model.name.to_lowercase());
    if models.is_empty() {
        return Err(io::Error::new(
            ErrorKind::InvalidData,
            "the OpenRouter model catalog has no usable models",
        ));
    }
    Ok(models)
}

/// Downloads and filters OpenRouter's model catalog. It needs no API key.
pub async fn fetch_catalog() -> io::Result<Vec<CatalogModel>> {
    let response = reqwest::Client::new()
        .get(format!("{ENDPOINT}/models"))
        .timeout(Duration::from_secs(15))
        .send()
        .await
        .map_err(transport)?;
    let status = response.status();
    if !status.is_success() {
        return Err(io::Error::other(format!(
            "OpenRouter model catalog returned {status}"
        )));
    }
    parse_catalog(
        &response.text().await.map_err(transport)?,
        chrono::Utc::now().timestamp(),
    )
}

struct Catalog {
    models: Vec<CatalogModel>,
    default_model: String,
}

static CATALOG: std::sync::OnceLock<Catalog> = std::sync::OnceLock::new();

/// Installs the fetched model catalog and the default model, once per process.
pub fn install_catalog(models: Vec<CatalogModel>, default_model: String) -> io::Result<()> {
    if !models.iter().any(|model| model.id == default_model) {
        return Err(io::Error::new(
            ErrorKind::InvalidData,
            format!("model {default_model} is not in the OpenRouter model catalog"),
        ));
    }
    if CATALOG
        .set(Catalog {
            models,
            default_model,
        })
        .is_err()
    {
        panic!("the model catalog is installed once");
    }
    Ok(())
}

fn installed() -> &'static Catalog {
    #[cfg(test)]
    CATALOG.get_or_init(|| Catalog {
        models: parse_catalog(fixture::CATALOG, fixture::NOW).unwrap(),
        default_model: "deepseek/deepseek-v4.1-flash".to_owned(),
    });
    CATALOG.get().expect("the model catalog is installed")
}

pub fn catalog() -> &'static [CatalogModel] {
    &installed().models
}

/// The model named by `model` in the settings file.
pub fn default_model() -> &'static str {
    &installed().default_model
}

const ENDPOINT: &str = "https://openrouter.ai/api/v1";

pub fn catalog_model(id: &str) -> Option<&'static CatalogModel> {
    catalog().iter().find(|model| model.id == id)
}

/// OpenRouter credentials and a reusable HTTP connection pool.
#[derive(Clone)]
pub struct Client {
    http: reqwest::Client,
    api_key: String,
    endpoint: String,
}

#[derive(Debug, Clone, PartialEq)]
pub enum StreamItem {
    TextDelta(String),
    ReasoningDelta(String),
    Completion(Completion),
}

#[derive(Debug, Clone, PartialEq)]
pub struct Completion {
    pub message: AssistantMessage,
    pub stop: Stop,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Stop {
    Finished,
    ToolCalls,
    TokenLimit,
    Refused,
}

#[derive(Debug)]
pub struct InputContextOverflow;

impl std::fmt::Display for InputContextOverflow {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "OpenRouter rejected the input as too large for the model context"
        )
    }
}

impl std::error::Error for InputContextOverflow {}

pub fn is_input_context_overflow(error: &io::Error) -> bool {
    error
        .get_ref()
        .is_some_and(|inner| inner.is::<InputContextOverflow>())
}

/// The validated catalog model, effort level, and system prompt that every
/// ordinary model request in a turn sends with the transcript.
#[derive(Debug, Clone)]
pub struct ModelRequestParameters {
    pub model: &'static CatalogModel,
    pub effort: EffortLevel,
    pub system_prompt: String,
}

impl ModelRequestParameters {
    /// Rejects saved or selected settings the fetched model catalog no longer
    /// accepts.
    pub fn new(model_id: &str, effort: EffortLevel, system_prompt: String) -> io::Result<Self> {
        let model = catalog_model(model_id).ok_or_else(|| {
            io::Error::new(
                ErrorKind::InvalidData,
                format!("session model {model_id} is not in the model catalog"),
            )
        })?;
        if !model.supports(effort) {
            return Err(io::Error::new(
                ErrorKind::InvalidData,
                format!(
                    "session model {model_id} does not accept effort {}",
                    effort.id()
                ),
            ));
        }
        Ok(Self {
            model,
            effort,
            system_prompt,
        })
    }
}

pub(crate) fn ordinary_body(
    parameters: &ModelRequestParameters,
    transcript: &[TranscriptEntry],
) -> Value {
    let messages =
        std::iter::once(json!({ "role": "system", "content": parameters.system_prompt }))
            .chain(chat_messages(transcript))
            .collect::<Vec<_>>();
    let mut body = json!({
        "model": parameters.model.id,
        "messages": messages,
        "tools": tools::schemas(),
        "stream": true,
        "usage": { "include": true },
    });
    if let Some(effort) = parameters.effort.openrouter_effort() {
        body["reasoning"] = json!({ "effort": effort });
    }
    body
}

pub(crate) fn summarizer_body(model: &CatalogModel, previous: &str, piece: &str) -> Value {
    let mut body = json!({
        "model": model.id,
        "messages": [
            {"role": "system", "content": include_str!("prompts/compaction_prompt.md")},
            {"role": "user", "content": format!("Previous summary:\n{previous}\n\nNew conversation material:\n{piece}")},
        ],
        "max_tokens": 4096,
        "stream": true,
        "usage": { "include": true },
    });
    if let Some(effort) = model.summarizer_effort().openrouter_effort() {
        body["reasoning"] = json!({ "effort": effort });
    }
    body
}

impl Client {
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

    /// Starts one streamed completion using `parameters` and `transcript`. The
    /// system prompt precedes the transcript.
    pub async fn stream_completion(
        &self,
        parameters: &ModelRequestParameters,
        transcript: &[TranscriptEntry],
    ) -> io::Result<CompletionStream> {
        self.stream_body(&ordinary_body(parameters, transcript))
            .await
    }

    /// Returns the summary and the usage OpenRouter reported for it.
    pub async fn summarize(
        &self,
        model: &CatalogModel,
        previous: &str,
        piece: &str,
    ) -> io::Result<(String, Option<ModelUsage>)> {
        let mut stream = self
            .stream_body(&summarizer_body(model, previous, piece))
            .await?;
        while let Some(item) = stream.next().await? {
            if let StreamItem::Completion(completion) = item {
                if completion.stop != Stop::Finished
                    || !completion.message.tool_calls.is_empty()
                    || completion.message.text.trim().is_empty()
                {
                    return Err(malformed(
                        "compaction summary was not a finished, nonempty text completion",
                    ));
                }
                return Ok((completion.message.text, completion.message.usage));
            }
        }
        Err(malformed("compaction summary ended without a completion"))
    }

    async fn stream_body(&self, body: &Value) -> io::Result<CompletionStream> {
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
            if matches!(status.as_u16(), 400 | 413 | 422) && explicit_context_overflow(&detail) {
                return Err(io::Error::other(InputContextOverflow));
            }
            return Err(io::Error::other(format!(
                "OpenRouter returned {status}: {}",
                detail.trim()
            )));
        }
        Ok(CompletionStream {
            response,
            buffer: Vec::new(),
            data: String::new(),
            assembly: Some(Assembly::default()),
            buffered_items: VecDeque::new(),
            usage: None,
        })
    }
}

fn explicit_context_overflow(detail: &str) -> bool {
    let lower = detail.to_ascii_lowercase();
    (lower.contains("context") || lower.contains("prompt tokens"))
        && [
            "exceed",
            "too long",
            "too large",
            "maximum",
            "max context",
            "length",
        ]
        .iter()
        .any(|term| lower.contains(term))
}

/// The user message that gives the model a skill invocation: its text
/// followed by its image attachments.
pub(crate) fn skill_invocation_message(invocation: &SkillInvocation) -> UserMessage {
    let text = format!(
        "Skill /{} invoked.\n\nInstructions:\n{}\n\nArguments:\n{}",
        invocation.name, invocation.instructions, invocation.arguments
    );
    UserMessage {
        parts: std::iter::once(UserMessagePart::Text(text))
            .chain(
                invocation
                    .images
                    .iter()
                    .cloned()
                    .map(UserMessagePart::Image),
            )
            .collect(),
    }
}

/// The user-role text that gives the model a hook's feedback.
pub(crate) fn hook_feedback_text(feedback: &HookFeedback) -> String {
    format!(
        "Feedback from the {}:\n{}",
        feedback.label(),
        feedback.message()
    )
}

fn image_part(image: &ImageAttachment) -> Value {
    json!({
        "type": "image_url",
        "image_url": { "url": format!("data:{};base64,{}", image.mime_type, image.data) },
    })
}

fn user_content(message: &UserMessage) -> Value {
    if !message.has_images() {
        return Value::String(message.text());
    }
    Value::Array(
        message
            .parts
            .iter()
            .map(|part| match part {
                UserMessagePart::Text(text) => json!({ "type": "text", "text": text }),
                UserMessagePart::Image(image) => image_part(image),
            })
            .collect(),
    )
}

/// Encodes the saved transcript as OpenRouter chat messages. Visible reasoning
/// is sent only when no continuation metadata carries it.
pub(crate) fn chat_messages(transcript: &[TranscriptEntry]) -> Vec<Value> {
    let mut messages = Vec::new();
    for entry in transcript {
        let value = match entry {
            TranscriptEntry::Model(_)
            | TranscriptEntry::Effort(_)
            | TranscriptEntry::Mode(_)
            | TranscriptEntry::CompactionCheckpoint(_) => continue,
            TranscriptEntry::UserMessage(message) => {
                json!({ "role": "user", "content": user_content(message) })
            }
            TranscriptEntry::SkillInvocation(invocation) => {
                let content = user_content(&skill_invocation_message(invocation));
                json!({ "role": "user", "content": content })
            }
            TranscriptEntry::HookFeedback(feedback) => {
                json!({ "role": "user", "content": hook_feedback_text(feedback) })
            }
            TranscriptEntry::AssistantBatch(batch) => {
                let message = &batch.message;
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
                if !message.continuation_metadata.is_empty() {
                    value["reasoning_details"] =
                        Value::Array(message.continuation_metadata.clone());
                } else if !message.reasoning.is_empty() {
                    value["reasoning"] = Value::String(message.reasoning.clone());
                }
                messages.push(value);
                messages.extend(batch.results.iter().map(|result| {
                    json!({
                        "role": "tool",
                        "tool_call_id": result.call_id,
                        "content": result.outcome.text(),
                    })
                }));
                continue;
            }
        };
        messages.push(value);
    }
    messages
}

fn transport(error: reqwest::Error) -> io::Error {
    io::Error::other(format!("OpenRouter request failed: {error}"))
}

fn malformed(message: impl Into<String>) -> io::Error {
    io::Error::new(ErrorKind::InvalidData, message.into())
}

/// One streamed OpenRouter response. Dropping it stops reading and discards
/// text received before a complete message was validated.
pub struct CompletionStream {
    response: reqwest::Response,
    buffer: Vec<u8>,
    data: String,
    assembly: Option<Assembly>,
    buffered_items: VecDeque<StreamItem>,
    /// OpenRouter reports usage once, in the final chunk.
    usage: Option<ModelUsage>,
}

impl CompletionStream {
    /// Returns the next output delta or the one validated completion.
    /// `Ok(None)` follows a completion; ending before one is an error.
    pub async fn next(&mut self) -> io::Result<Option<StreamItem>> {
        while self.buffered_items.is_empty() {
            if !self.read_chunk().await? {
                return if self.assembly.is_none() {
                    Ok(None)
                } else {
                    Err(io::Error::new(
                        ErrorKind::UnexpectedEof,
                        "OpenRouter stream ended before the response finished",
                    ))
                };
            }
        }
        if matches!(self.buffered_items.front(), Some(StreamItem::Completion(_))) {
            // The finish chunk may be followed by a usage chunk and `[DONE]`.
            // Read them before yielding the completion, so the completion
            // carries the usage and the HTTP/1.1 connection, which returns to
            // the pool only once its body is read to the end, can be reused.
            while self.read_chunk().await? {}
            if let Some(StreamItem::Completion(completion)) = self.buffered_items.front_mut() {
                completion.message.usage = self.usage.take();
            }
        }
        Ok(self.buffered_items.pop_front())
    }

    /// Reads one network chunk into stream items; `false` at the end of the body.
    async fn read_chunk(&mut self) -> io::Result<bool> {
        let Some(chunk) = self.response.chunk().await.map_err(transport)? else {
            return Ok(false);
        };
        self.buffer.extend_from_slice(&chunk);
        while let Some(newline) = self.buffer.iter().position(|&byte| byte == b'\n') {
            let line = self.buffer.drain(..=newline).collect::<Vec<u8>>();
            let line = std::str::from_utf8(&line)
                .map_err(|_| malformed("OpenRouter stream is not UTF-8"))?
                .trim_end_matches(['\r', '\n']);
            self.line(line)?;
        }
        Ok(true)
    }

    fn line(&mut self, line: &str) -> io::Result<()> {
        if line.is_empty() {
            if !self.data.is_empty() {
                let data = std::mem::take(&mut self.data);
                self.process_sse_event(&data)?;
            }
        } else if let Some(data) = line.strip_prefix("data:") {
            if !self.data.is_empty() {
                self.data.push('\n');
            }
            self.data.push_str(data.strip_prefix(' ').unwrap_or(data));
        }
        Ok(())
    }

    fn process_sse_event(&mut self, data: &str) -> io::Result<()> {
        if data == "[DONE]" {
            return Ok(());
        }
        let chunk: Chunk = serde_json::from_str(data)
            .map_err(|error| malformed(format!("malformed OpenRouter stream chunk: {error}")))?;
        if let Some(error) = chunk.error {
            return Err(io::Error::other(format!(
                "OpenRouter reported an error: {} ({})",
                error.message, error.code
            )));
        }
        if let Some(usage) = chunk.usage {
            self.usage = Some(ModelUsage {
                input_tokens: usage.prompt_tokens,
                output_tokens: usage.completion_tokens,
                cost: usage.cost,
            });
        }
        // The usage chunk after the final one repeats the finish reason with
        // an empty delta; it cannot change the validated message.
        let Some(assembly) = self.assembly.as_mut() else {
            return Ok(());
        };
        let Some(choice) = chunk.choices.into_iter().next() else {
            return Ok(());
        };
        if let Some(text) = choice.delta.content.filter(|text| !text.is_empty()) {
            assembly.text.push_str(&text);
            self.buffered_items.push_back(StreamItem::TextDelta(text));
        }
        if let Some(reasoning) = choice.delta.reasoning.filter(|text| !text.is_empty()) {
            assembly.reasoning.push_str(&reasoning);
            self.buffered_items
                .push_back(StreamItem::ReasoningDelta(reasoning));
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
                .expect("assembly is present until the message is validated");
            self.buffered_items
                .push_back(StreamItem::Completion(assembly.finish(&reason)?));
        }
        Ok(())
    }
}

#[derive(Default)]
struct Assembly {
    text: String,
    reasoning: String,
    tool_calls: BTreeMap<u64, PartialCall>,
    continuation_metadata: Vec<Value>,
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
                .continuation_metadata
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
        self.continuation_metadata.push(detail);
    }

    fn finish(self, reason: &str) -> io::Result<Completion> {
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
            ("stop", false) => Stop::Finished,
            ("stop" | "tool_calls", true) => Stop::ToolCalls,
            ("tool_calls", false) => {
                return Err(malformed("finish reason tool_calls without any tool call"));
            }
            ("length", false) => Stop::TokenLimit,
            ("content_filter", false) => Stop::Refused,
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
            text: self.text,
            reasoning: self.reasoning,
            tool_calls,
            continuation_metadata: self.continuation_metadata,
            usage: None,
        };
        message.validate().map_err(|error| {
            malformed(format!("incomplete tool call in model response: {error}"))
        })?;
        Ok(Completion { message, stop })
    }
}

#[derive(Deserialize)]
struct Chunk {
    #[serde(default)]
    choices: Vec<Choice>,
    error: Option<ApiError>,
    usage: Option<ApiUsage>,
}

#[derive(Deserialize)]
struct ApiUsage {
    prompt_tokens: u64,
    completion_tokens: u64,
    cost: f64,
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
    use std::{
        collections::VecDeque,
        sync::{
            Arc, Mutex,
            atomic::{AtomicUsize, Ordering},
        },
    };

    use serde_json::{Value, json};
    use tokio::{
        io::{AsyncReadExt, AsyncWriteExt},
        net::{TcpListener, TcpStream},
    };

    use super::{Client, default_model};
    use crate::tools;

    /// The time `CATALOG` is filtered at: 2026-09-23.
    pub const NOW: i64 = 1_790_121_600;

    /// An OpenRouter `GET /models` response, out of name order. The last five
    /// models fail the catalog filter.
    pub const CATALOG: &str = r#"{"data": [
        {"id": "acme/plain", "name": "Plain", "context_length": 8001, "created": 1774310400,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"], "reasoning": {"mandatory": false}},
        {"id": "z-ai/glm-5.3-flash", "name": "GLM 5.3 Flash", "context_length": 1310720, "created": 1788393600,
         "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"],
         "reasoning": {"supported_efforts": ["future", "max", "xhigh", "high", "medium", "low"]}},
        {"id": "deepseek/deepseek-v4.1-flash", "name": "DeepSeek V4.1 Flash", "context_length": 1048576, "created": 1789689600,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["reasoning", "tools"],
         "reasoning": {"supported_efforts": ["max", "high", "medium", "low"], "default_effort": "high"}},
        {"id": "meta/muse-spark-1.3-contributor", "name": "Muse Spark 1.3 Contributor", "context_length": 1048576, "created": 1788998400,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"],
         "reasoning": {"supported_efforts": ["xhigh", "high", "medium", "none"]}},
        {"id": "acme/no-tools", "name": "No Tools", "context_length": 1048576, "created": 1789689600,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["reasoning"]},
        {"id": "acme/image-out", "name": "Image Out", "context_length": 1048576, "created": 1789689600,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["image"]},
         "supported_parameters": ["tools"]},
        {"id": "acme/small", "name": "Small", "context_length": 8000, "created": 1789689600,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"]},
        {"id": "deepseek/deepseek-v4.1-flash:batch", "name": "DeepSeek V4.1 Flash (batch)", "context_length": 1048576, "created": 1789689600,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"]},
        {"id": "acme/old", "name": "Old", "context_length": 1048576, "created": 1774310399,
         "architecture": {"input_modalities": ["text"], "output_modalities": ["text"]},
         "supported_parameters": ["tools"]}
    ]}"#;

    pub enum Reply {
        /// A complete SSE body, one HTTP chunk per event, then the
        /// connection stays open for the next request.
        Stream(String),
        /// A status code with a JSON body.
        Status(u16, String),
        /// The start of an SSE body, then the connection stays open forever.
        Hang(String),
    }

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

    pub struct Server {
        url: String,
        requests: Arc<Mutex<Vec<Value>>>,
        connections: Arc<AtomicUsize>,
    }

    impl Server {
        /// Serves `replies` in request order, over as many connections as
        /// the client opens.
        pub async fn start(replies: Vec<Reply>) -> Self {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url = format!("http://{}", listener.local_addr().unwrap());
            let requests = Arc::new(Mutex::new(Vec::new()));
            let connections = Arc::new(AtomicUsize::new(0));
            let replies = Arc::new(Mutex::new(VecDeque::from(replies)));
            let (seen, opened) = (requests.clone(), connections.clone());
            tokio::spawn(async move {
                loop {
                    let (socket, _) = listener.accept().await.unwrap();
                    opened.fetch_add(1, Ordering::SeqCst);
                    tokio::spawn(serve(socket, replies.clone(), seen.clone()));
                }
            });
            Self {
                url,
                requests,
                connections,
            }
        }

        pub fn client(&self) -> Client {
            Client::for_endpoint("test-key".to_owned(), self.url.clone())
        }

        pub fn requests(&self) -> Vec<Value> {
            self.requests.lock().unwrap().clone()
        }

        pub fn connections(&self) -> usize {
            self.connections.load(Ordering::SeqCst)
        }
    }

    async fn serve(
        mut socket: TcpStream,
        replies: Arc<Mutex<VecDeque<Reply>>>,
        seen: Arc<Mutex<Vec<Value>>>,
    ) {
        while let Some(body) = read_request(&mut socket).await {
            if !body.is_empty() {
                seen.lock()
                    .unwrap()
                    .push(serde_json::from_slice(&body).unwrap());
            }
            let Some(reply) = replies.lock().unwrap().pop_front() else {
                break;
            };
            match reply {
                Reply::Stream(body) => {
                    socket.write_all(stream(&body).as_bytes()).await.unwrap();
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
        }
        socket.shutdown().await.ok();
    }

    /// One request body; `None` once the client closes the connection.
    async fn read_request(socket: &mut TcpStream) -> Option<Vec<u8>> {
        let mut bytes = Vec::new();
        let mut chunk = [0u8; 4096];
        loop {
            let read = socket.read(&mut chunk).await.unwrap();
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

    fn response(status: u16, content_type: &str, body: &str) -> String {
        let reason = match status {
            200 => "OK",
            401 => "Unauthorized",
            _ => "Error",
        };
        format!(
            "HTTP/1.1 {status} {reason}\r\nContent-Type: {content_type}\r\n\
             Content-Length: {}\r\n\r\n{body}",
            body.len()
        )
    }

    /// A chunked SSE response with each event in its own HTTP chunk, the way
    /// OpenRouter delivers a stream it is still generating.
    fn stream(body: &str) -> String {
        let mut response = String::from(
            "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\
             Transfer-Encoding: chunked\r\n\r\n",
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
            "model": default_model(),
            "choices": [{ "index": 0, "delta": delta, "finish_reason": finish_reason }],
        })
    }

    /// A usage chunk like the one OpenRouter sends after the finish chunk.
    pub fn usage(input: u64, output: u64, cost: f64) -> Value {
        json!({
            "id": "gen-1",
            "object": "chat.completion.chunk",
            "model": default_model(),
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

    /// One assistant message with one shell call per `(call_id, command)`.
    pub fn tool_reply(calls: &[(&str, &str)]) -> Reply {
        let tool_calls = calls
            .iter()
            .enumerate()
            .map(|(index, (id, command))| {
                json!({
                    "index": index,
                    "id": id,
                    "type": "function",
                    "function": {
                        "name": tools::SHELL,
                        "arguments": json!({ "command": command }).to_string(),
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
        fixture::{Reply, Server, delta, sse, text_reply, usage},
        *,
    };
    use crate::sessions::{AssistantBatch, SessionMode, ToolOutcome, ToolResult};

    const TEST_SYSTEM_PROMPT: &str = "You are Ox.";

    fn test_parameters() -> ModelRequestParameters {
        ModelRequestParameters::new(
            default_model(),
            EffortLevel::Default,
            TEST_SYSTEM_PROMPT.to_owned(),
        )
        .unwrap()
    }

    async fn drain(request: &mut CompletionStream) -> io::Result<Vec<StreamItem>> {
        let mut items = Vec::new();
        while let Some(item) = request.next().await? {
            items.push(item);
        }
        Ok(items)
    }

    async fn complete_with(chunks: &[Value]) -> io::Result<Vec<StreamItem>> {
        let server = Server::start(vec![Reply::Stream(sse(chunks))]).await;
        let mut request = server
            .client()
            .stream_completion(
                &test_parameters(),
                &[TranscriptEntry::UserMessage("hi".to_owned().into())],
            )
            .await?;
        drain(&mut request).await
    }

    fn completion(items: &[StreamItem]) -> Completion {
        let completions = items
            .iter()
            .filter_map(|item| match item {
                StreamItem::Completion(completion) => Some(completion.clone()),
                _ => None,
            })
            .collect::<Vec<_>>();
        assert_eq!(completions.len(), 1, "exactly one completion is returned");
        completions.into_iter().next().unwrap()
    }

    fn call(id: &str, command: &str) -> ToolCall {
        ToolCall {
            call_id: id.to_owned(),
            name: tools::SHELL.to_owned(),
            arguments: json!({ "command": command }).to_string(),
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
        let raw = r#" { "command": "printf Chicago" } "#;
        let first = ToolCall {
            arguments: raw.to_owned(),
            ..call("call-1", "printf Chicago")
        };
        let transcript = vec![
            TranscriptEntry::Model(catalog()[2].id.as_str().to_owned()),
            TranscriptEntry::Mode(SessionMode::Auto),
            TranscriptEntry::UserMessage("Weather in Chicago and Denver?".to_owned().into()),
            TranscriptEntry::AssistantBatch(AssistantBatch {
                message: AssistantMessage {
                    text: String::new(),
                    reasoning: "Need both cities.".to_owned(),
                    tool_calls: vec![first, call("call-2", "printf Denver")],
                    continuation_metadata: details.clone(),
                    usage: None,
                },
                results: vec![
                    ToolResult {
                        call_id: "call-1".to_owned(),
                        name: tools::SHELL.to_owned(),
                        outcome: ToolOutcome::Completed("Sunny.".to_owned()),
                    },
                    ToolResult {
                        call_id: "call-2".to_owned(),
                        name: tools::SHELL.to_owned(),
                        outcome: ToolOutcome::Failed("Unavailable.".to_owned()),
                    },
                ],
            }),
        ];

        let mut request = server
            .client()
            .stream_completion(
                &ModelRequestParameters::new(
                    catalog()[2].id.as_str(),
                    EffortLevel::Default,
                    "You are Ox.\n\n# Workspace instructions from AGENTS.md\n\nAnswer in French."
                        .to_owned(),
                )
                .unwrap(),
                &transcript,
            )
            .await
            .unwrap();
        let items = drain(&mut request).await.unwrap();
        assert_eq!(completion(&items).stop, Stop::Finished);

        let body = &server.requests()[0];
        assert_eq!(body["model"], catalog()[2].id.as_str());
        assert_eq!(body["stream"], true);
        assert_eq!(body["usage"], json!({ "include": true }));
        assert!(
            body["tools"]
                .as_array()
                .unwrap()
                .iter()
                .any(|tool| tool["function"]["name"] == tools::SHELL)
        );
        let messages = body["messages"].as_array().unwrap();
        assert_eq!(messages.len(), 5);
        assert_eq!(
            messages[0],
            json!({
                "role": "system",
                "content": "You are Ox.\n\n# Workspace instructions from AGENTS.md\n\nAnswer in French."
            })
        );
        assert_eq!(
            messages[1],
            json!({ "role": "user", "content": "Weather in Chicago and Denver?" })
        );
        assert_eq!(messages[2]["role"], "assistant");
        assert_eq!(messages[2]["content"], Value::Null);
        assert_eq!(messages[2]["tool_calls"].as_array().unwrap().len(), 2);
        assert_eq!(
            messages[2]["tool_calls"][0]["function"],
            json!({"name": tools::SHELL, "arguments": raw})
        );
        assert_eq!(messages[2]["tool_calls"][1]["id"], "call-2");
        assert_eq!(messages[2]["reasoning_details"], json!(details));
        assert!(messages[2].get("reasoning").is_none());
        assert_eq!(
            messages[3],
            json!({ "role": "tool", "tool_call_id": "call-1", "content": "Sunny." })
        );
        assert_eq!(messages[4]["tool_call_id"], "call-2");

        let mut compacted = transcript.clone();
        compacted.push(TranscriptEntry::CompactionCheckpoint(
            crate::sessions::CompactionCheckpoint {
                summary: "Older summary".to_owned(),
                covered_prefix: 4,
                summarizer_cost: None,
            },
        ));
        compacted.push(TranscriptEntry::UserMessage("middle".to_owned().into()));
        compacted.push(TranscriptEntry::AssistantBatch(AssistantBatch {
            message: AssistantMessage {
                text: "middle answer".to_owned(),
                reasoning: String::new(),
                tool_calls: vec![],
                continuation_metadata: vec![],
                usage: None,
            },
            results: vec![],
        }));
        compacted.push(TranscriptEntry::CompactionCheckpoint(
            crate::sessions::CompactionCheckpoint {
                summary: "Current summary".to_owned(),
                covered_prefix: 7,
                summarizer_cost: None,
            },
        ));
        compacted.push(TranscriptEntry::UserMessage("recent".to_owned().into()));
        compacted.push(TranscriptEntry::AssistantBatch(AssistantBatch {
            message: AssistantMessage {
                text: "recent answer".to_owned(),
                reasoning: "visible".to_owned(),
                tool_calls: vec![],
                continuation_metadata: vec![json!({"type":"reasoning.encrypted", "data":"recent"})],
                usage: None,
            },
            results: vec![],
        }));
        let projected = crate::compaction::projection(&compacted);
        let body = ordinary_body(&test_parameters(), &projected);
        let projected_messages = body["messages"].as_array().unwrap();
        assert_eq!(projected_messages.len(), 4);
        assert_eq!(
            projected_messages[1]["content"],
            "Compaction summary of earlier conversation:\nCurrent summary"
        );
        assert_eq!(projected_messages[2]["content"], "recent");
        assert_eq!(
            projected_messages[3]["reasoning_details"][0]["data"],
            "recent"
        );
        assert!(projected_messages[3].get("reasoning").is_none());

        let image = ImageAttachment {
            data: "aGVsbG8=".to_owned(),
            mime_type: "image/png".to_owned(),
        };
        let messages = chat_messages(&[
            TranscriptEntry::UserMessage(UserMessage {
                parts: vec![
                    UserMessagePart::Text("Before".to_owned()),
                    UserMessagePart::Image(image.clone()),
                    UserMessagePart::Text("After".to_owned()),
                ],
            }),
            TranscriptEntry::SkillInvocation(SkillInvocation {
                name: "goal".to_owned(),
                arguments: "Inspect".to_owned(),
                instructions: "Look at the screenshot.".to_owned(),
                images: vec![image],
            }),
        ]);
        assert_eq!(
            messages[0]["content"],
            json!([
                {"type":"text","text":"Before"},
                {"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}},
                {"type":"text","text":"After"},
            ])
        );
        assert_eq!(
            messages[1]["content"][0]["text"],
            "Skill /goal invoked.\n\nInstructions:\nLook at the screenshot.\n\nArguments:\nInspect"
        );
        assert_eq!(
            messages[1]["content"][1]["image_url"]["url"],
            "data:image/png;base64,aGVsbG8="
        );
    }

    #[test]
    fn catalog_filter_keeps_recent_usable_models_by_name_with_known_efforts() {
        use EffortLevel::*;
        let models = catalog();
        assert_eq!(
            models
                .iter()
                .map(|model| model.id.as_str())
                .collect::<Vec<_>>(),
            [
                "deepseek/deepseek-v4.1-flash",
                "z-ai/glm-5.3-flash",
                "meta/muse-spark-1.3-contributor",
                "acme/plain",
            ]
        );
        assert_eq!(models[1].efforts, [Default, Low, Medium, High, XHigh, Max]);
        assert!(models[1].accepts_images);
        assert!(!models[0].accepts_images);
        assert_eq!(models[2].summarizer_effort(), Medium);
        assert_eq!(models[3].efforts, [Default]);
        assert_eq!(models[3].summarizer_effort(), Default);
        assert!(parse_catalog(r#"{"data": []}"#, fixture::NOW).is_err());
        assert!(parse_catalog(r#"{"data": [{"id": "a/b"}]}"#, fixture::NOW).is_err());
    }

    #[tokio::test]
    async fn requests_send_each_effort_of_each_model() {
        for model in catalog() {
            for effort in model.efforts.iter().copied() {
                let server = Server::start(vec![text_reply("Done")]).await;
                let mut stream = server
                    .client()
                    .stream_completion(
                        &ModelRequestParameters::new(
                            &model.id,
                            effort,
                            TEST_SYSTEM_PROMPT.to_owned(),
                        )
                        .unwrap(),
                        &[],
                    )
                    .await
                    .unwrap();
                drain(&mut stream).await.unwrap();
                let requests = server.requests();
                let body = &requests[0];
                assert_eq!(body["model"], model.id);
                match effort {
                    EffortLevel::Default => assert!(body.get("reasoning").is_none()),
                    _ => assert_eq!(body["reasoning"]["effort"], effort.id()),
                }
            }
        }
    }

    #[test]
    fn visible_reasoning_is_sent_only_without_metadata() {
        let plain = AssistantMessage {
            text: "Four.".to_owned(),
            reasoning: "Add them.".to_owned(),
            tool_calls: vec![],
            continuation_metadata: vec![],
            usage: None,
        };
        let messages = chat_messages(&[TranscriptEntry::AssistantBatch(AssistantBatch {
            message: plain,
            results: vec![],
        })]);
        assert_eq!(messages[0]["reasoning"], "Add them.");
        assert!(messages[0].get("reasoning_details").is_none());
        assert!(messages[0].get("tool_calls").is_none());
    }

    #[tokio::test]
    async fn streams_assemble_text_reasoning_and_fragmented_tool_calls() {
        let items = complete_with(&[
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
                json!({ "tool_calls": [{ "index": 0, "id": "call-1", "type": "function", "function": { "name": "shell", "arguments": "{\"comm" } }] }),
                None,
            ),
            delta(
                json!({ "tool_calls": [
                    { "index": 0, "function": { "arguments": "and\":\"printf Chicago\"}" } },
                    { "index": 1, "id": "call-2", "type": "function", "function": { "name": "shell", "arguments": "" } },
                ] }),
                None,
            ),
            delta(
                json!({ "tool_calls": [{ "index": 1, "function": { "arguments": "{\"command\":\"printf Denver\"}" } }] }),
                Some("tool_calls"),
            ),
            usage(12, 34, 0.25),
        ])
        .await
        .unwrap();

        let deltas = items
            .iter()
            .filter_map(|item| match item {
                StreamItem::TextDelta(text) => Some(("text", text.as_str())),
                StreamItem::ReasoningDelta(text) => Some(("reasoning", text.as_str())),
                StreamItem::Completion(_) => None,
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
        let completion = completion(&items);
        assert_eq!(completion.stop, Stop::ToolCalls);
        assert_eq!(
            completion.message,
            AssistantMessage {
                text: "Checking now.".to_owned(),
                reasoning: "Let me check.".to_owned(),
                tool_calls: vec![
                    call("call-1", "printf Chicago"),
                    call("call-2", "printf Denver"),
                ],
                continuation_metadata: vec![
                    json!({ "type": "reasoning.text", "text": "Let me check.", "index": 0, "format": "x", "signature": "sig" }),
                    json!({ "type": "reasoning.encrypted", "data": "blob", "id": "rs_1", "index": 1 }),
                ],
                usage: Some(ModelUsage {
                    input_tokens: 12,
                    output_tokens: 34,
                    cost: 0.25,
                }),
            }
        );
        assert!(matches!(items.last(), Some(StreamItem::Completion(_))));
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
                let items = complete_with(&chunks).await.unwrap();
                let message = completion(&items).message;
                let expected = vec![
                    detail(kind, field, "First block."),
                    first_encrypted,
                    detail(kind, field, "Second block."),
                    second_encrypted,
                    third_encrypted,
                ];
                assert_eq!(message.continuation_metadata, expected);
                let messages = chat_messages(&[TranscriptEntry::AssistantBatch(AssistantBatch {
                    message,
                    results: vec![],
                })]);
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
            let items = complete_with(&chunks).await.unwrap();
            assert_eq!(completion(&items).message.continuation_metadata, details);
        }
    }

    #[tokio::test]
    async fn finish_reasons_are_classified_or_rejected() {
        let finished = complete_with(&[delta(json!({ "content": "Done." }), Some("stop"))])
            .await
            .unwrap();
        assert_eq!(completion(&finished).stop, Stop::Finished);

        let limited = complete_with(&[delta(json!({ "content": "Cut" }), Some("length"))])
            .await
            .unwrap();
        assert_eq!(completion(&limited).stop, Stop::TokenLimit);

        let refused = complete_with(&[delta(json!({}), Some("content_filter"))])
            .await
            .unwrap();
        assert_eq!(completion(&refused).stop, Stop::Refused);

        assert!(
            complete_with(&[delta(json!({}), Some("tool_calls"))])
                .await
                .is_err()
        );
        let call = json!({ "tool_calls": [{ "index": 0, "id": "call-1", "function": { "name": "shell", "arguments": "{" } }] });
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

        let anonymous = json!({ "tool_calls": [{ "index": 0, "function": { "name": "shell", "arguments": "{}" } }] });
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

        let mut costless = usage(12, 34, 0.25);
        costless["usage"].as_object_mut().unwrap().remove("cost");
        let error = complete_with(&[delta(json!({ "content": "Done." }), Some("stop")), costless])
            .await
            .unwrap_err();
        assert_eq!(error.kind(), ErrorKind::InvalidData);

        let server = Server::start(vec![Reply::Stream("data: not json\n\n".to_owned())]).await;
        let mut request = server
            .client()
            .stream_completion(&test_parameters(), &[])
            .await
            .unwrap();
        assert!(drain(&mut request).await.is_err());

        let server = Server::start(vec![Reply::Status(
            429,
            r#"{"error":"slow down"}"#.to_owned(),
        )])
        .await;
        let error = server
            .client()
            .stream_completion(&test_parameters(), &[])
            .await
            .err()
            .expect("a failed status is an error");
        assert!(error.to_string().contains("429"));
    }

    #[tokio::test]
    async fn completed_requests_reuse_one_connection() {
        let server = Server::start(vec![text_reply("one"), text_reply("two")]).await;
        let client = server.client();
        for _ in 0..2 {
            let mut request = client
                .stream_completion(
                    &test_parameters(),
                    &[TranscriptEntry::UserMessage("hi".to_owned().into())],
                )
                .await
                .unwrap();
            while !matches!(
                request.next().await.unwrap(),
                Some(StreamItem::Completion(_))
            ) {}
            drop(request);
            // The pool takes an idle connection back on a background task.
            tokio::task::yield_now().await;
        }
        assert_eq!(server.requests().len(), 2);
        assert_eq!(server.connections(), 1);
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
