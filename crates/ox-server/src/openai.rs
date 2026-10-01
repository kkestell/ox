//! Stateless subscription Responses requests and validated streamed completions.

use crate::{
    model::{
        self, CatalogModel, Completion, InputContextOverflow, ModelRequestParameters, Stop,
        StreamItem,
    },
    openai_auth::Authentication,
    sessions::{
        AssistantMessage, ModelUsage, ToolCall, TranscriptEntry, TurnInput, UserMessage,
        UserMessagePart,
    },
    tools,
};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, VecDeque},
    io::{self, ErrorKind},
    time::Duration,
};

const ENDPOINT: &str = "https://api.openai.com/v1";
const STALL_TIMEOUT: Duration = Duration::from_secs(120);
const USAGE_URL: &str = "https://chatgpt.com/settings/usage";

// Fields verified against the authenticated public catalog on 2026-10-01.
#[derive(serde::Deserialize)]
struct CatalogResponse {
    models: Vec<OpenAIModel>,
}

#[derive(serde::Deserialize)]
struct OpenAIModel {
    slug: String,
    display_name: String,
    visibility: String,
    context_window: usize,
    input_modalities: Vec<String>,
    supported_in_api: bool,
    supported_reasoning_levels: Vec<ReasoningLevel>,
}

#[derive(serde::Deserialize)]
struct ReasoningLevel {
    effort: String,
}

pub(crate) fn parse_catalog(text: &str) -> io::Result<Vec<CatalogModel>> {
    let response: CatalogResponse =
        serde_json::from_str(text).map_err(|_| malformed("model catalog is invalid"))?;
    let mut catalog = Vec::new();
    for model in response.models {
        if model.visibility != "list"
            || !model.supported_in_api
            || model.context_window <= 8000
            || !model
                .input_modalities
                .iter()
                .any(|modality| modality == "text")
        {
            continue;
        }
        if model.slug.trim().is_empty()
            || model.display_name.trim().is_empty()
            || catalog
                .iter()
                .any(|existing: &CatalogModel| existing.id == model.slug)
        {
            return Err(malformed(
                "model catalog has an empty or duplicate model ID or name",
            ));
        }
        catalog.push(CatalogModel {
            provider: model::Provider::OpenAI,
            id: model.slug,
            name: model.display_name,
            context_limit: model.context_window,
            input_price: None,
            output_price: None,
            accepts_images: model
                .input_modalities
                .iter()
                .any(|modality| modality == "image"),
            efforts: crate::sessions::EffortLevel::ALL
                .into_iter()
                .filter(|effort| {
                    *effort == crate::sessions::EffortLevel::Default
                        || model
                            .supported_reasoning_levels
                            .iter()
                            .any(|level| level.effort == effort.id())
                })
                .collect(),
            providers: Vec::new(),
        });
    }
    if catalog.is_empty() {
        return Err(malformed("model catalog has no usable models"));
    }
    Ok(catalog)
}

#[derive(Clone)]
pub struct Client {
    http: reqwest::Client,
    authentication: Authentication,
    endpoint: String,
    stall_timeout: Duration,
}

impl Client {
    pub(crate) fn with_authentication(authentication: Authentication) -> Self {
        Self {
            http: reqwest::Client::builder()
                .retry(reqwest::retry::never())
                .build()
                .expect("HTTP client builds"),
            authentication,
            endpoint: ENDPOINT.to_owned(),
            stall_timeout: STALL_TIMEOUT,
        }
    }

    pub async fn fetch_catalog(&self) -> io::Result<Vec<CatalogModel>> {
        let token = self.authentication.access_token().await?;
        let response = self
            .http
            .get(format!("{}/models", self.endpoint))
            .bearer_auth(token)
            .timeout(Duration::from_secs(15))
            .send()
            .await
            .map_err(transport)?;
        let status = response.status();
        let body = response.text().await.map_err(transport)?;
        if !status.is_success() {
            return Err(provider_error(
                &serde_json::from_str::<Value>(&body).unwrap_or_else(|_| json!({"detail":body})),
                &format!("OpenAI model catalog returned {status}"),
            ));
        }
        parse_catalog(&body)
    }

    pub async fn stream_completion(
        &self,
        parameters: &ModelRequestParameters,
        input: Vec<Value>,
    ) -> io::Result<CompletionStream> {
        self.stream_body(&ordinary_body(parameters, input)).await
    }

    pub async fn summarize(
        &self,
        model: &CatalogModel,
        previous: &str,
        piece: &str,
    ) -> io::Result<(String, Option<ModelUsage>)> {
        let mut stream = self
            .stream_body(&summarizer_body(model, previous, piece))
            .await?;
        loop {
            let StreamItem::Completion(completion) = stream.next().await? else {
                continue;
            };
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

    async fn stream_body(&self, body: &Value) -> io::Result<CompletionStream> {
        let token = self.authentication.access_token().await?;
        let request = self
            .http
            .post(format!("{}/responses", self.endpoint))
            .bearer_auth(token)
            .json(body)
            .send();
        let response = tokio::time::timeout(self.stall_timeout, request)
            .await
            .map_err(|_| stalled(self.stall_timeout))?
            .map_err(transport)?;
        if !response.status().is_success() {
            let status = response.status();
            let request_id = response
                .headers()
                .get("x-request-id")
                .and_then(|id| id.to_str().ok())
                .unwrap_or("unknown")
                .to_owned();
            let body = tokio::time::timeout(self.stall_timeout, response.text())
                .await
                .map_err(|_| stalled(self.stall_timeout))?
                .map_err(transport)?;
            let error =
                serde_json::from_str::<Value>(&body).unwrap_or_else(|_| json!({"detail": body}));
            return Err(model::status_error(
                status,
                provider_error(
                    &error,
                    &format!("OpenAI returned {status}, request {request_id}"),
                ),
            ));
        }
        Ok(CompletionStream {
            response,
            buffer: Vec::new(),
            data: String::new(),
            items: VecDeque::new(),
            assembly: Assembly::default(),
            terminal: false,
            stall_timeout: self.stall_timeout,
            deadline: tokio::time::Instant::now() + self.stall_timeout,
        })
    }
}

pub(crate) fn user_message(message: &UserMessage) -> Value {
    let content = message.parts.iter().map(|part| match part {
        UserMessagePart::Text(text) => json!({"type":"input_text", "text":text}),
        UserMessagePart::Image(image) => json!({"type":"input_image", "image_url":format!("data:{};base64,{}", image.mime_type, image.data)}),
    }).collect::<Vec<_>>();
    json!({"type":"message", "role":"user", "content":content})
}

pub(crate) fn input(
    transcript: &[TranscriptEntry],
    mut turn_provider: Option<model::Provider>,
) -> Vec<Value> {
    let mut input = Vec::new();
    for entry in transcript {
        match entry {
            TranscriptEntry::CompactionCheckpoint(_) => {}
            TranscriptEntry::TurnStart(start) => {
                turn_provider = model::Provider::from_qualified_model_id(&start.model);
                input.push(match &start.input {
                    TurnInput::UserMessage(message) => user_message(message),
                    TurnInput::SkillInvocation(invocation) => {
                        user_message(&model::skill_invocation_message(invocation))
                    }
                });
            }
            TranscriptEntry::SubagentMessages(messages) => input.extend(
                messages
                    .iter()
                    .map(|message| user_message(&model::subagent_message_text(message).into())),
            ),
            TranscriptEntry::AssistantBatch(batch) => {
                let message = &batch.message;
                if turn_provider == Some(model::Provider::OpenAI) {
                    input.extend(message.continuation_metadata.clone());
                }
                if !message.text.is_empty() {
                    input.push(json!({"type":"message", "role":"assistant", "content":[{"type":"output_text", "text":message.text, "annotations":[]}]}));
                }
                input.extend(message.tool_calls.iter().map(|call| json!({
                    "type":"function_call", "call_id":call.call_id, "name":call.name, "namespace":"ox", "arguments":call.arguments,
                })));
                input.extend(message.tool_calls.iter().zip(&batch.outcomes).map(|(call,outcome)| json!({
                    "type":"function_call_output", "call_id":call.call_id, "name":call.name, "namespace":"ox", "output":outcome.text,
                })));
            }
        }
    }
    input
}

pub(crate) fn ordinary_body(parameters: &ModelRequestParameters, input: Vec<Value>) -> Value {
    let functions = tools::schemas(parameters.role)
        .into_iter()
        .map(|schema| {
            let mut function = schema["function"].clone();
            function["type"] = json!("function");
            function["strict"] = json!(false);
            function
        })
        .collect::<Vec<_>>();
    let mut body = base_body(
        parameters.model,
        parameters.effort,
        &parameters.system_prompt,
        input,
    );
    body["tools"] = json!([{"type":"namespace", "name":"ox", "description":"Ox workspace, shell, and subagent tools.", "tools":functions}]);
    body
}

pub(crate) fn summarizer_body(model: &CatalogModel, previous: &str, piece: &str) -> Value {
    let instructions = format!(
        "{}\n\nKeep the summary within {} tokens.",
        include_str!("prompts/compaction_prompt.md"),
        model::SUMMARIZER_MAX_TOKENS
    );
    base_body(
        model,
        model.summarizer_effort(),
        &instructions,
        vec![user_message(
            &format!("Previous summary:\n{previous}\n\nNew conversation material:\n{piece}").into(),
        )],
    )
}

fn base_body(
    model: &CatalogModel,
    effort: crate::sessions::EffortLevel,
    instructions: &str,
    input: Vec<Value>,
) -> Value {
    let mut body = json!({
        "model":model.id, "instructions":instructions, "input":input, "store":false, "stream":true,
        "include":["reasoning.encrypted_content"],
    });
    if effort != crate::sessions::EffortLevel::Default {
        body["reasoning"] = json!({"effort":effort.id(), "summary":"auto"});
    }
    body
}

fn transport(error: reqwest::Error) -> io::Error {
    io::Error::other(format!("OpenAI request failed: {}", error.without_url()))
}
fn malformed(message: &str) -> io::Error {
    io::Error::new(
        ErrorKind::InvalidData,
        format!("malformed OpenAI response: {message}"),
    )
}
fn stalled(timeout: Duration) -> io::Error {
    io::Error::new(
        ErrorKind::TimedOut,
        format!("OpenAI sent no response data for {timeout:?}"),
    )
}

fn provider_error(body: &Value, prefix: &str) -> io::Error {
    let error = body
        .get("error")
        .filter(|value| !value.is_null())
        .unwrap_or(body);
    let code = error["code"].as_str().unwrap_or("unknown_error");
    if code == "context_length_exceeded" {
        return io::Error::other(InputContextOverflow);
    }
    let detail = error["message"]
        .as_str()
        .or_else(|| error["detail"].as_str())
        .unwrap_or("unspecified provider error");
    let mut message = format!("{prefix}: {detail} ({code})");
    if matches!(
        code,
        "subscription_sharing_usage_limit_exceeded" | "subscription_sharing_usage_unavailable"
    ) {
        message.push_str(&format!("; ChatGPT usage settings: {USAGE_URL}"));
    }
    if matches!(
        code,
        "invalid_api_key" | "subscription_sharing_invalid_user"
    ) {
        message.push_str("; run `ox auth login openai`");
    }
    io::Error::other(message)
}

pub struct CompletionStream {
    response: reqwest::Response,
    buffer: Vec<u8>,
    data: String,
    items: VecDeque<StreamItem>,
    assembly: Assembly,
    terminal: bool,
    stall_timeout: Duration,
    deadline: tokio::time::Instant,
}

impl CompletionStream {
    pub async fn next(&mut self) -> io::Result<StreamItem> {
        loop {
            if let Some(item) = self.items.pop_front() {
                return Ok(item);
            }
            if self.terminal {
                return Err(malformed("stream read after its terminal event"));
            }
            let chunk = tokio::time::timeout_at(self.deadline, self.response.chunk())
                .await
                .map_err(|_| stalled(self.stall_timeout))?
                .map_err(transport)?;
            let Some(chunk) = chunk else {
                return Err(io::Error::new(
                    ErrorKind::UnexpectedEof,
                    "OpenAI stream ended without response.completed",
                ));
            };
            self.buffer.extend_from_slice(&chunk);
            while let Some(newline) = self.buffer.iter().position(|&byte| byte == b'\n') {
                let bytes = self.buffer.drain(..=newline).collect::<Vec<_>>();
                let line = std::str::from_utf8(&bytes)
                    .map_err(|_| malformed("stream is not UTF-8"))?
                    .trim_end_matches(['\r', '\n']);
                if line.is_empty() && !self.data.is_empty() {
                    let data = std::mem::take(&mut self.data);
                    if data == "[DONE]" {
                        if !self.terminal {
                            return Err(malformed("[DONE] arrived before response.completed"));
                        }
                        continue;
                    }
                    let event = serde_json::from_str::<Value>(&data)
                        .map_err(|_| malformed("invalid stream event JSON"))?;
                    self.process(event)?;
                } else if let Some(data) = line.strip_prefix("data:") {
                    self.deadline = tokio::time::Instant::now() + self.stall_timeout;
                    if !self.data.is_empty() {
                        self.data.push('\n');
                    }
                    self.data.push_str(data.strip_prefix(' ').unwrap_or(data));
                }
            }
        }
    }

    fn process(&mut self, event: Value) -> io::Result<()> {
        if self.terminal {
            return Err(malformed("output arrived after response.completed"));
        }
        match event["type"]
            .as_str()
            .ok_or_else(|| malformed("stream event has no type"))?
        {
            "response.output_text.delta" | "response.refusal.delta" => {
                let delta = string(&event, "delta")?.to_owned();
                self.assembly
                    .text
                    .entry(indices(&event, "content_index")?)
                    .or_default()
                    .push_str(&delta);
                self.items.push_back(StreamItem::TextDelta(delta));
            }
            "response.reasoning_summary_text.delta" | "response.reasoning_text.delta" => {
                let delta = string(&event, "delta")?.to_owned();
                self.assembly.reasoning.push_str(&delta);
                self.items.push_back(StreamItem::ReasoningDelta(delta));
            }
            "response.output_item.added" => {
                let index = index(&event)?;
                if self
                    .assembly
                    .added
                    .insert(index, event["item"].clone())
                    .is_some()
                {
                    return Err(malformed("repeated output item index"));
                }
            }
            "response.function_call_arguments.delta" => {
                let delta = string(&event, "delta")?;
                self.assembly
                    .arguments
                    .entry(index(&event)?)
                    .or_default()
                    .push_str(delta);
            }
            "response.output_item.done" => {
                if self
                    .assembly
                    .done
                    .insert(index(&event)?, event["item"].clone())
                    .is_some()
                {
                    return Err(malformed("repeated completed output item index"));
                }
            }
            "response.completed" => {
                let completion = self.assembly.finish(&event["response"])?;
                self.items.push_back(StreamItem::Completion(completion));
                self.terminal = true;
            }
            "response.failed" => {
                return Err(provider_error(
                    &event["response"],
                    "OpenAI generation failed",
                ));
            }
            "error" => return Err(provider_error(&event, "OpenAI stream error")),
            "response.incomplete" | "response.cancelled" => {
                return Err(provider_error(
                    &event["response"],
                    "OpenAI generation did not complete",
                ));
            }
            "response.created"
            | "response.in_progress"
            | "response.content_part.added"
            | "response.content_part.done"
            | "response.output_text.done"
            | "response.reasoning_summary_part.added"
            | "response.reasoning_summary_part.done"
            | "response.reasoning_summary_text.done"
            | "response.reasoning_text.done"
            | "response.function_call_arguments.done"
            | "response.refusal.done" => {}
            _ => return Err(malformed("unsupported stream event type")),
        }
        Ok(())
    }
}

fn string<'a>(value: &'a Value, key: &str) -> io::Result<&'a str> {
    value[key]
        .as_str()
        .ok_or_else(|| malformed("required string is missing"))
}
fn index(event: &Value) -> io::Result<usize> {
    event["output_index"]
        .as_u64()
        .and_then(|index| index.try_into().ok())
        .ok_or_else(|| malformed("output index is missing"))
}
fn indices(event: &Value, part: &str) -> io::Result<(usize, usize)> {
    Ok((
        index(event)?,
        event[part]
            .as_u64()
            .and_then(|index| index.try_into().ok())
            .ok_or_else(|| malformed("content index is missing"))?,
    ))
}

#[derive(Default)]
struct Assembly {
    added: BTreeMap<usize, Value>,
    done: BTreeMap<usize, Value>,
    text: BTreeMap<(usize, usize), String>,
    arguments: BTreeMap<usize, String>,
    reasoning: String,
}

impl Assembly {
    fn finish(&self, response: &Value) -> io::Result<Completion> {
        if response["status"] != "completed"
            || !response["error"].is_null()
            || !response["incomplete_details"].is_null()
        {
            return Err(malformed("terminal response is not completed"));
        }
        let terminal_output = response["output"]
            .as_array()
            .ok_or_else(|| malformed("terminal output is missing"))?;
        // The subscription route finishes items individually, then sends an
        // empty terminal output array. They remain provisional until this event.
        let assembled;
        let output = if terminal_output.is_empty() {
            if self.done.keys().copied().ne(0..self.done.len()) {
                return Err(malformed(
                    "completed output item indices are not contiguous",
                ));
            }
            assembled = self.done.values().cloned().collect::<Vec<_>>();
            &assembled
        } else {
            for (&index, item) in &self.done {
                if terminal_output.get(index) != Some(item) {
                    return Err(malformed("terminal output differs from its completed item"));
                }
            }
            terminal_output
        };
        if output.is_empty() {
            return Err(malformed("terminal output is empty"));
        }
        let mut message = AssistantMessage {
            text: String::new(),
            reasoning: String::new(),
            tool_calls: Vec::new(),
            continuation_metadata: Vec::new(),
            usage: None,
        };
        let mut refused = false;
        for (index, item) in output.iter().enumerate() {
            if item
                .get("status")
                .is_some_and(|status| status != "completed")
            {
                return Err(malformed("terminal output item is incomplete"));
            }
            if let Some(added) = self.added.get(&index) {
                for key in ["id", "type", "name", "namespace", "call_id"] {
                    if added
                        .get(key)
                        .is_some_and(|value| item.get(key) != Some(value))
                    {
                        return Err(malformed("terminal output differs from the streamed item"));
                    }
                }
            }
            match string(item, "type")? {
                "message" => {
                    if item["role"] != "assistant" {
                        return Err(malformed("output message is not from the assistant"));
                    }
                    for (content_index, part) in item["content"]
                        .as_array()
                        .ok_or_else(|| malformed("output message content is missing"))?
                        .iter()
                        .enumerate()
                    {
                        match string(part, "type")? {
                            "output_text" => {
                                let text = string(part, "text")?;
                                if self
                                    .text
                                    .get(&(index, content_index))
                                    .is_some_and(|delta| delta != text)
                                {
                                    return Err(malformed("terminal text differs from its deltas"));
                                }
                                message.text.push_str(text);
                            }
                            "refusal" => {
                                refused = true;
                                let text = string(part, "refusal")?;
                                if self
                                    .text
                                    .get(&(index, content_index))
                                    .is_some_and(|delta| delta != text)
                                {
                                    return Err(malformed(
                                        "terminal refusal differs from its deltas",
                                    ));
                                }
                                message.text.push_str(text);
                            }
                            _ => return Err(malformed("unsupported output content")),
                        }
                    }
                }
                "function_call" => {
                    if item["namespace"] != "ox" {
                        return Err(malformed("tool call is outside the ox namespace"));
                    }
                    let arguments = string(item, "arguments")?;
                    if self
                        .arguments
                        .get(&index)
                        .is_some_and(|delta| delta != arguments)
                    {
                        return Err(malformed(
                            "terminal tool arguments differ from their deltas",
                        ));
                    }
                    message.tool_calls.push(ToolCall {
                        call_id: string(item, "call_id")?.to_owned(),
                        name: string(item, "name")?.to_owned(),
                        arguments: arguments.to_owned(),
                    });
                }
                "reasoning" => {
                    for part in item["summary"]
                        .as_array()
                        .ok_or_else(|| malformed("reasoning summary is missing"))?
                    {
                        if part["type"] != "summary_text" {
                            return Err(malformed("unsupported reasoning summary content"));
                        }
                        message.reasoning.push_str(string(part, "text")?);
                    }
                    message.continuation_metadata.push(item.clone());
                }
                _ => return Err(malformed("unsupported output item")),
            }
        }
        if self.added.keys().any(|&index| index >= output.len())
            || self.arguments.keys().any(|&index| {
                output
                    .get(index)
                    .is_none_or(|item| item["type"] != "function_call")
            })
            || self.text.keys().any(|&(index, part)| {
                output
                    .get(index)
                    .and_then(|item| item["content"].as_array())
                    .is_none_or(|content| part >= content.len())
            })
        {
            return Err(malformed(
                "streamed output is absent from the terminal response",
            ));
        }
        if !self.reasoning.is_empty() && self.reasoning != message.reasoning {
            return Err(malformed("terminal reasoning differs from its deltas"));
        }
        if !response["usage"].is_null() {
            let usage = &response["usage"];
            let count = |key: &str| {
                usage[key]
                    .as_u64()
                    .ok_or_else(|| malformed("token usage is invalid"))
            };
            message.usage = Some(ModelUsage {
                input_tokens: count("input_tokens")?,
                output_tokens: count("output_tokens")?,
                cached_tokens: usage["input_tokens_details"]["cached_tokens"]
                    .as_u64()
                    .unwrap_or(0),
                reasoning_tokens: usage["output_tokens_details"]["reasoning_tokens"]
                    .as_u64()
                    .unwrap_or(0),
                cost: None,
            });
        }
        message.validate()?;
        if refused && !message.tool_calls.is_empty() {
            return Err(malformed("refusal includes tool calls"));
        }
        let stop = if refused {
            Stop::Refused
        } else if message.tool_calls.is_empty() {
            Stop::Finished
        } else {
            Stop::ToolCalls
        };
        Ok(Completion { message, stop })
    }
}

#[cfg(any(test, feature = "test-support"))]
pub(crate) const FIXTURE_CATALOG: &str = r#"{"models":[
    {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","context_window":272000,"max_context_window":872000,"input_modalities":["text","image"],"supported_in_api":true,"supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"},{"effort":"max"},{"effort":"ultra"}]},
    {"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list","context_window":272000,"input_modalities":["text","image"],"supported_in_api":true,"supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]},
    {"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","context_window":272000,"input_modalities":["text","image"],"supported_in_api":true,"supported_reasoning_levels":[]}
]}"#;

#[cfg(test)]
pub(crate) mod fixture {
    use super::*;
    pub use crate::openrouter::fixture::{Gate, Reply, sse};
    use crate::tools::fixture::Workspace;

    pub const DEFAULT_MODEL: &str = "openai:gpt-6-astra";
    pub const PROVIDER_MODEL: &str = "gpt-6-astra";
    pub const CATALOG: &str = super::FIXTURE_CATALOG;

    pub struct Server {
        inner: crate::openrouter::fixture::Server,
        authentication: Authentication,
        _workspace: Workspace,
    }
    impl Server {
        pub async fn start(replies: Vec<Reply>) -> Self {
            Self::routed(vec![("", replies)]).await
        }
        pub async fn routed(routes: Vec<(&str, Vec<Reply>)>) -> Self {
            let workspace = Workspace::new();
            let authentication = Authentication::fixture(workspace.0.clone());
            Self {
                inner: crate::openrouter::fixture::Server::routed(routes).await,
                authentication,
                _workspace: workspace,
            }
        }
        pub fn client(&self) -> Client {
            self.http_client(STALL_TIMEOUT)
        }
        pub fn http_client(&self, timeout: Duration) -> Client {
            Client {
                http: reqwest::Client::builder()
                    .retry(reqwest::retry::never())
                    .build()
                    .unwrap(),
                authentication: self.authentication.clone(),
                endpoint: self.inner.endpoint().to_owned(),
                stall_timeout: timeout,
            }
        }
        pub fn requests(&self) -> Vec<Value> {
            self.inner.requests()
        }
        pub async fn wait_for_requests(&self, marker: &str, count: usize) {
            self.inner.wait_for_requests(marker, count).await;
        }
    }
    pub fn completed(output: Vec<Value>) -> Value {
        json!({"type":"response.completed", "response":{"id":"resp_test", "status":"completed", "output":output,
            "usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":25},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10}}}})
    }
    pub fn message(text: &str) -> Value {
        json!({"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":text,"annotations":[]}]})
    }
    pub fn reasoning() -> Value {
        json!({"id":"rs_test","type":"reasoning","summary":[{"type":"summary_text","text":"Thinking."}],"encrypted_content":"opaque-continuation"})
    }
    pub fn call(id: &str, name: &str, arguments: Value) -> Value {
        json!({"id":format!("fc_{id}"),"type":"function_call","status":"completed","call_id":id,"namespace":"ox","name":name,"arguments":arguments.to_string()})
    }
    pub fn text_reply(text: &str) -> Reply {
        Reply::Stream(sse(&[
            json!({"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":text}),
            json!({"type":"response.output_item.done","output_index":0,"item":message(text)}),
            completed(vec![]),
        ]))
    }
    pub fn calls_reply(calls: Vec<Value>) -> Reply {
        let mut events = calls.into_iter().enumerate().map(|(index, item)| json!({"type":"response.output_item.done","output_index":index,"item":item})).collect::<Vec<_>>();
        events.push(completed(vec![]));
        Reply::Stream(sse(&events))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sessions::{
        AssistantBatch, EffortLevel, ImageAttachment, SkillInvocation, ToolOutcome,
        TranscriptEntry, TurnStart,
    };
    use fixture::{Reply, Server, call, completed, message, reasoning, sse, text_reply};

    fn parameters() -> ModelRequestParameters {
        ModelRequestParameters::new(
            fixture::DEFAULT_MODEL,
            EffortLevel::Medium,
            "You are Ox.".to_owned(),
            tools::Role::Main,
        )
        .unwrap()
    }
    async fn drain(mut stream: CompletionStream) -> io::Result<Vec<StreamItem>> {
        let mut items = Vec::new();
        loop {
            let item = stream.next().await?;
            let terminal = matches!(item, StreamItem::Completion(_));
            items.push(item);
            if terminal {
                return Ok(items);
            }
        }
    }

    #[tokio::test]
    async fn authenticated_catalog_preserves_display_order_capabilities_and_absent_prices() {
        let server = Server::start(vec![Reply::Status(200, fixture::CATALOG.to_owned())]).await;
        let models = server
            .http_client(STALL_TIMEOUT)
            .fetch_catalog()
            .await
            .unwrap();
        assert_eq!(
            models
                .iter()
                .map(|model| model.id.as_str())
                .collect::<Vec<_>>(),
            ["gpt-6-astra", "gpt-5.5"]
        );
        assert_eq!(models[0].context_limit, 272000);
        assert!(models[0].accepts_images && models[0].supports(EffortLevel::Max));
        assert!(models.iter().all(|model| model.input_price.is_none()
            && model.output_price.is_none()
            && model.provider == model::Provider::OpenAI));
        assert!(parse_catalog(r#"{"data":[]}"#).is_err());
    }

    #[test]
    fn continuation_metadata_is_only_sent_for_openai_turns() {
        let batch = |text: &str, metadata| {
            TranscriptEntry::AssistantBatch(AssistantBatch {
                message: AssistantMessage {
                    text: text.to_owned(),
                    reasoning: "Visible reasoning".to_owned(),
                    tool_calls: vec![],
                    continuation_metadata: vec![metadata],
                    usage: None,
                },
                outcomes: vec![],
            })
        };
        let mut openrouter_start = TurnStart::test("OpenRouter question".to_owned());
        openrouter_start.model = crate::openrouter::fixture::DEFAULT_MODEL.to_owned();
        let mut openai_start = TurnStart::test("OpenAI question".to_owned());
        openai_start.model = fixture::DEFAULT_MODEL.to_owned();
        let openrouter_metadata = json!({"type":"reasoning.encrypted", "data":"openrouter"});
        let openai_metadata = reasoning();
        let projected = input(
            &[
                TranscriptEntry::TurnStart(openrouter_start),
                batch("OpenRouter answer", openrouter_metadata.clone()),
                TranscriptEntry::TurnStart(openai_start),
                batch("OpenAI answer", openai_metadata.clone()),
            ],
            None,
        );

        assert_eq!(projected.len(), 5);
        assert_eq!(projected[0]["content"][0]["text"], "OpenRouter question");
        assert_eq!(projected[1]["content"][0]["text"], "OpenRouter answer");
        assert!(!projected.contains(&openrouter_metadata));
        assert_eq!(projected[2]["content"][0]["text"], "OpenAI question");
        assert_eq!(projected[3], openai_metadata);
        assert_eq!(projected[4]["content"][0]["text"], "OpenAI answer");
    }

    #[tokio::test]
    async fn subscription_requests_encode_transcript_tools_and_reasoning_directly() {
        let server = Server::start(vec![text_reply("Hello."), text_reply("summary")]).await;
        let image = ImageAttachment {
            mime_type: "image/png".to_owned(),
            data: "YWJj".to_owned(),
        };
        let user = UserMessage {
            parts: vec![
                UserMessagePart::Text("Before".to_owned()),
                UserMessagePart::Image(image.clone()),
                UserMessagePart::Text("After".to_owned()),
            ],
        };
        let batch = AssistantBatch::new(
            AssistantMessage {
                text: "Calling tools.".to_owned(),
                reasoning: "Thinking.".to_owned(),
                continuation_metadata: vec![reasoning()],
                usage: None,
                tool_calls: vec![ToolCall {
                    call_id: "call1".to_owned(),
                    name: "read_file".to_owned(),
                    arguments: r#"{"path":"a"}"#.to_owned(),
                }],
            },
            vec![ToolOutcome::completed("Contents")],
        )
        .unwrap();
        let mut transcript = vec![
            TranscriptEntry::turn(user),
            TranscriptEntry::turn(SkillInvocation {
                name: "work".to_owned(),
                instructions: "Do work.".to_owned(),
                arguments: "args".to_owned(),
                images: vec![image],
            }),
            TranscriptEntry::AssistantBatch(batch),
            TranscriptEntry::SubagentMessages(vec![crate::sessions::SubagentMessage {
                subagent_id: "child-1".to_owned(),
                content: crate::sessions::SubagentMessageContent::FinalAnswer(
                    "Found it.".to_owned(),
                ),
            }]),
        ];
        for entry in &mut transcript {
            if let TranscriptEntry::TurnStart(start) = entry {
                start.model = fixture::DEFAULT_MODEL.to_owned();
            }
        }
        let client = server.http_client(STALL_TIMEOUT);
        let items = drain(
            client
                .stream_completion(&parameters(), input(&transcript, None))
                .await
                .unwrap(),
        )
        .await
        .unwrap();
        let StreamItem::Completion(completion) = items.last().unwrap() else {
            panic!("no completion");
        };
        assert_eq!(
            completion.message.usage.as_ref().unwrap(),
            &ModelUsage {
                input_tokens: 100,
                cached_tokens: 25,
                output_tokens: 30,
                reasoning_tokens: 10,
                cost: None
            }
        );
        client
            .summarize(parameters().model, "previous", "piece")
            .await
            .unwrap();
        let requests = server.requests();
        let request = &requests[0];
        for body in &requests {
            assert_eq!(body["store"], false);
            assert_eq!(body["stream"], true);
            assert!(
                body.get("messages").is_none()
                    && body.get("max_output_tokens").is_none()
                    && body.get("previous_response_id").is_none()
            );
        }
        assert_eq!(request["instructions"], "You are Ox.");
        assert_eq!(request["include"], json!(["reasoning.encrypted_content"]));
        assert_eq!(
            request["input"][0]["content"]
                .as_array()
                .unwrap()
                .iter()
                .map(|part| part["type"].as_str().unwrap())
                .collect::<Vec<_>>(),
            ["input_text", "input_image", "input_text"]
        );
        assert!(
            request["input"][1]["content"][0]["text"]
                .as_str()
                .unwrap()
                .contains("Skill /work invoked.")
        );
        assert_eq!(request["input"][2], reasoning());
        assert_eq!(request["input"][4]["call_id"], "call1");
        assert_eq!(request["input"][5]["namespace"], "ox");
        assert_eq!(request["input"][5]["output"], "Contents");
        assert_eq!(
            request["input"][6]["content"][0]["text"],
            "Final answer from subagent child-1:\nFound it."
        );
        assert_eq!(request["tools"][0]["type"], "namespace");
        assert_eq!(request["tools"][0]["name"], "ox");
        assert!(
            request["tools"][0]["tools"]
                .as_array()
                .unwrap()
                .iter()
                .all(|function| function["strict"] == false)
        );
        assert!(
            requests[1]["instructions"]
                .as_str()
                .unwrap()
                .contains("4096 tokens")
        );
        assert!(requests[1].get("tools").is_none());
    }

    #[tokio::test]
    async fn stream_assembles_multiple_fragmented_calls_and_final_reasoning() {
        let first = call("one", "read_file", json!({"path":"a"}));
        let second = call("two", "glob", json!({"pattern":"*"}));
        let mut added = first.clone();
        added["status"] = json!("in_progress");
        added["arguments"] = json!("");
        let events = [
            json!({"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Thinking."}),
            json!({"type":"response.output_item.added","output_index":1,"item":added}),
            json!({"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":"}),
            json!({"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"a\"}"}),
            completed(vec![reasoning(), first, second]),
        ];
        let server = Server::start(vec![Reply::Stream(sse(&events))]).await;
        let items = drain(
            server
                .http_client(STALL_TIMEOUT)
                .stream_completion(&parameters(), vec![])
                .await
                .unwrap(),
        )
        .await
        .unwrap();
        assert_eq!(items[0], StreamItem::ReasoningDelta("Thinking.".to_owned()));
        let StreamItem::Completion(completion) = items.last().unwrap() else {
            panic!("no completion");
        };
        assert_eq!(completion.stop, Stop::ToolCalls);
        assert_eq!(completion.message.tool_calls.len(), 2);
        assert_eq!(completion.message.continuation_metadata, vec![reasoning()]);
        assert_eq!(completion.message.reasoning, "Thinking.");
    }

    #[tokio::test]
    async fn unsuccessful_or_malformed_terminal_streams_never_return_a_completion() {
        let proposed = call("unsafe", "shell", json!({"command":"touch should-not-run"}));
        let mut bad_namespace = proposed.clone();
        bad_namespace["namespace"] = json!("other");
        let mut bad_status = proposed.clone();
        bad_status["status"] = json!("incomplete");
        for (name, terminal) in [
            (
                "failed",
                json!({"type":"response.failed","response":{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"App usage limit reached"}}}),
            ),
            (
                "incomplete",
                json!({"type":"response.incomplete","response":{"status":"incomplete"}}),
            ),
            (
                "cancelled",
                json!({"type":"response.cancelled","response":{"status":"cancelled"}}),
            ),
            (
                "missing output",
                json!({"type":"response.completed","response":{"status":"completed"}}),
            ),
            ("bad namespace", completed(vec![bad_namespace])),
            ("bad status", completed(vec![bad_status])),
            (
                "duplicate calls",
                completed(vec![proposed.clone(), proposed.clone()]),
            ),
            ("empty output", completed(vec![])),
        ] {
            let server = Server::start(vec![Reply::Stream(sse(&[
                json!({"type":"response.output_item.added","output_index":0,"item":proposed.clone()}),terminal,
            ]))]).await;
            let error = drain(
                server
                    .http_client(STALL_TIMEOUT)
                    .stream_completion(&parameters(), vec![])
                    .await
                    .unwrap(),
            )
            .await
            .unwrap_err();
            if name == "failed" {
                assert!(
                    error.to_string().contains(USAGE_URL)
                        && error.to_string().contains("App usage limit reached")
                );
            }
        }
    }

    #[tokio::test]
    async fn separately_completed_items_require_a_successful_terminal_event() {
        let output = [
            reasoning(),
            call("write", "write_file", json!({"path":"a","content":"b"})),
            message("Done."),
        ];
        let events = output.iter().enumerate().map(|(index, item)| json!({"type":"response.output_item.done","output_index":index,"item":item})).collect::<Vec<_>>();
        for terminal in [
            completed(vec![]),
            json!({"type":"response.failed","response":{"status":"failed","error":{"message":"Failed"}}}),
        ] {
            let successful = terminal["type"] == "response.completed";
            let mut stream_events = events.clone();
            stream_events.push(terminal);
            let server = Server::start(vec![Reply::Stream(sse(&stream_events))]).await;
            let result = drain(
                server
                    .http_client(STALL_TIMEOUT)
                    .stream_completion(&parameters(), vec![])
                    .await
                    .unwrap(),
            )
            .await;
            if successful {
                let StreamItem::Completion(completion) = result.unwrap().pop().unwrap() else {
                    panic!("no completion")
                };
                assert_eq!(completion.message.text, "Done.");
                assert_eq!(completion.message.tool_calls.len(), 1);
                assert_eq!(completion.message.continuation_metadata, vec![reasoning()]);
            } else {
                assert!(result.is_err());
            }
        }
        for done in [
            vec![
                json!({"type":"response.output_item.done","output_index":1,"item":message("Gap")}),
            ],
            vec![
                json!({"type":"response.output_item.done","output_index":0,"item":message("First")}),
                json!({"type":"response.output_item.done","output_index":0,"item":message("Duplicate")}),
            ],
            vec![
                json!({"type":"response.output_item.added","output_index":1,"item":reasoning()}),
                json!({"type":"response.output_item.done","output_index":0,"item":message("Missing item")}),
            ],
        ] {
            let mut events = done;
            events.push(completed(vec![]));
            let server = Server::start(vec![Reply::Stream(sse(&events))]).await;
            assert!(
                drain(
                    server
                        .http_client(STALL_TIMEOUT)
                        .stream_completion(&parameters(), vec![])
                        .await
                        .unwrap()
                )
                .await
                .is_err()
            );
        }
    }

    #[tokio::test]
    async fn refusal_context_overflow_truncation_and_stalls_have_distinct_outcomes() {
        let refusal = json!({"id":"refusal","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"Cannot help."}]});
        let server = Server::start(vec![Reply::Stream(sse(&[
            json!({"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"Cannot help."}),
            completed(vec![refusal]),
        ]))]).await;
        let items = drain(
            server
                .http_client(STALL_TIMEOUT)
                .stream_completion(&parameters(), vec![])
                .await
                .unwrap(),
        )
        .await
        .unwrap();
        assert!(matches!(
            items.last(),
            Some(StreamItem::Completion(Completion {
                stop: Stop::Refused,
                ..
            }))
        ));
        let server = Server::start(vec![Reply::Status(
            400,
            json!({"error":{"code":"context_length_exceeded","message":"Too large"}}).to_string(),
        )])
        .await;
        let error = server
            .http_client(STALL_TIMEOUT)
            .stream_completion(&parameters(), vec![])
            .await
            .err()
            .unwrap();
        assert!(model::is_input_context_overflow(&error));
        for (reply, kind) in [
            (
                Reply::Stream("data: {\"type\":\"response.in_progress\"}\n\n".to_owned()),
                ErrorKind::UnexpectedEof,
            ),
            (
                Reply::Hang(": keepalive\n\n".to_owned()),
                ErrorKind::TimedOut,
            ),
        ] {
            let server = Server::start(vec![reply]).await;
            let error = drain(
                server
                    .http_client(Duration::from_millis(30))
                    .stream_completion(&parameters(), vec![])
                    .await
                    .unwrap(),
            )
            .await
            .unwrap_err();
            assert_eq!(error.kind(), kind);
        }
    }
}
