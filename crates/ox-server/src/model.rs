//! The selected model provider, installed catalog, and validated completions.

use crate::{
    openai, openrouter,
    sessions::{AssistantMessage, EffortLevel, SkillInvocation, UserMessage, UserMessagePart},
};
use serde::Deserialize;
use std::io::{self, ErrorKind};

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Provider {
    #[default]
    OpenRouter,
    OpenAI,
}

impl Provider {
    pub fn id(self) -> &'static str {
        match self {
            Self::OpenRouter => "openrouter",
            Self::OpenAI => "openai",
        }
    }
    pub fn name(self) -> &'static str {
        match self {
            Self::OpenRouter => "OpenRouter",
            Self::OpenAI => "OpenAI",
        }
    }
    pub fn from_id(id: &str) -> Option<Self> {
        match id {
            "openrouter" => Some(Self::OpenRouter),
            "openai" => Some(Self::OpenAI),
            _ => None,
        }
    }

    /// Splits a qualified model ID into its provider and provider-native ID.
    pub fn split_qualified_model_id(id: &str) -> Option<(Self, &str)> {
        let (provider, model) = id.split_once(':')?;
        Some((Self::from_id(provider)?, model)).filter(|_| !model.is_empty())
    }

    pub fn from_qualified_model_id(id: &str) -> Option<Self> {
        Self::split_qualified_model_id(id).map(|(provider, _)| provider)
    }

    pub fn qualify(self, model_id: &str) -> String {
        format!("{}:{model_id}", self.id())
    }
}

#[derive(Debug)]
pub struct CatalogModel {
    pub provider: Provider,
    pub id: String,
    pub name: String,
    pub context_limit: usize,
    /// USD per million input tokens.
    pub input_price: Option<f64>,
    /// USD per million output tokens.
    pub output_price: Option<f64>,
    pub accepts_images: bool,
    /// `Default` followed by the provider's listed efforts, in ascending order.
    pub efforts: Vec<EffortLevel>,
    /// The provider pin from the global settings file: model requests go only
    /// to these OpenRouter provider slugs, tried in order. Empty lets
    /// OpenRouter choose.
    pub providers: Vec<String>,
}

impl Provider {
    /// Encodes `transcript` for this provider.
    pub(crate) fn transcript(
        self,
        transcript: &[crate::sessions::TranscriptEntry],
    ) -> Vec<serde_json::Value> {
        match self {
            Self::OpenRouter => openrouter::chat_messages(transcript),
            Self::OpenAI => openai::input(transcript),
        }
    }
}

#[derive(Clone, Default)]
pub struct Clients {
    pub(crate) openrouter: Option<openrouter::Client>,
    pub(crate) openai: Option<openai::Client>,
}

impl From<openrouter::Client> for Clients {
    fn from(client: openrouter::Client) -> Self {
        Self {
            openrouter: Some(client),
            openai: None,
        }
    }
}

impl From<openai::Client> for Clients {
    fn from(client: openai::Client) -> Self {
        Self {
            openrouter: None,
            openai: Some(client),
        }
    }
}

impl Clients {
    pub async fn stream_completion(
        &self,
        session_id: &str,
        parameters: &ModelRequestParameters,
        input: Vec<serde_json::Value>,
    ) -> io::Result<CompletionStream> {
        Ok(match parameters.model.provider {
            Provider::OpenRouter => CompletionStream::OpenRouter(
                self.openrouter()
                    .stream_completion(parameters, input)
                    .await?,
            ),
            Provider::OpenAI => CompletionStream::OpenAI(
                self.openai()
                    .stream_completion(session_id, parameters, input)
                    .await?,
            ),
        })
    }

    // The model catalog only holds models from providers whose model client was
    // built, so a missing client is a bug.
    fn openrouter(&self) -> &openrouter::Client {
        self.openrouter
            .as_ref()
            .expect("every installed model provider has a client")
    }

    fn openai(&self) -> &openai::Client {
        self.openai
            .as_ref()
            .expect("every installed model provider has a client")
    }
}

pub enum CompletionStream {
    OpenRouter(openrouter::CompletionStream),
    OpenAI(openai::CompletionStream),
}

impl CompletionStream {
    pub async fn next(&mut self) -> io::Result<StreamItem> {
        match self {
            Self::OpenRouter(stream) => stream.next().await,
            Self::OpenAI(stream) => stream.next().await,
        }
    }
}

impl CatalogModel {
    pub fn qualified_id(&self) -> String {
        self.provider.qualify(&self.id)
    }

    pub fn supports(&self, effort: EffortLevel) -> bool {
        self.efforts.contains(&effort)
    }
}

static CATALOG: std::sync::OnceLock<Vec<CatalogModel>> = std::sync::OnceLock::new();

/// Installs the fetched model catalog, once per process.
pub fn install_catalog(models: Vec<CatalogModel>) {
    if CATALOG.set(models).is_err() {
        panic!("the model catalog is installed once");
    }
}

pub fn catalog() -> &'static [CatalogModel] {
    #[cfg(any(test, feature = "test-support"))]
    CATALOG.get_or_init(|| {
        let mut models =
            openrouter::parse_catalog(openrouter::fixture::CATALOG, openrouter::fixture::NOW)
                .unwrap();
        models.extend(openai::parse_catalog(openai::FIXTURE_CATALOG).unwrap());
        models
    });
    CATALOG.get().expect("the model catalog is installed")
}

pub fn catalog_model(id: &str) -> Option<&'static CatalogModel> {
    catalog_model_in(catalog(), id)
}

pub(crate) fn catalog_model_in<'a>(
    catalog: &'a [CatalogModel],
    id: &str,
) -> Option<&'a CatalogModel> {
    let (provider, provider_id) = Provider::split_qualified_model_id(id)?;
    catalog
        .iter()
        .find(|model| model.provider == provider && model.id == provider_id)
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
            "the model provider rejected the input as too large for the model context"
        )
    }
}

impl std::error::Error for InputContextOverflow {}

pub fn is_input_context_overflow(error: &io::Error) -> bool {
    error
        .get_ref()
        .is_some_and(|inner| inner.is::<InputContextOverflow>())
}

/// A temporary failure: a model request failure that a later attempt may not
/// repeat. It keeps the provider's message.
#[derive(Debug)]
struct Temporary(String);

impl std::fmt::Display for Temporary {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for Temporary {}

/// True for a temporary failure: a stall, or an HTTP 429 or 5xx status.
pub fn is_temporary(error: &io::Error) -> bool {
    error.kind() == ErrorKind::TimedOut
        || error.get_ref().is_some_and(|inner| inner.is::<Temporary>())
}

/// Marks the error of a failed HTTP status temporary when the status is 429
/// or 5xx.
pub(crate) fn mark_temporary(status: reqwest::StatusCode, error: io::Error) -> io::Error {
    if status == reqwest::StatusCode::TOO_MANY_REQUESTS || status.is_server_error() {
        io::Error::other(Temporary(error.to_string()))
    } else {
        error
    }
}

/// The validated catalog model, effort level, and system prompt, which every
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

#[cfg(test)]
mod tests {
    use super::*;

    fn model(provider: Provider) -> CatalogModel {
        CatalogModel {
            provider,
            id: "same-model".to_owned(),
            name: "Same Model".to_owned(),
            context_limit: 8_000,
            input_price: None,
            output_price: None,
            accepts_images: false,
            efforts: vec![EffortLevel::Default],
            providers: vec![],
        }
    }

    #[test]
    fn qualified_model_lookup_uses_both_provider_and_native_id() {
        let catalog = [model(Provider::OpenRouter), model(Provider::OpenAI)];
        for (id, provider) in [
            ("openrouter:same-model", Provider::OpenRouter),
            ("openai:same-model", Provider::OpenAI),
        ] {
            assert_eq!(catalog_model_in(&catalog, id).unwrap().provider, provider);
            assert_eq!(Provider::from_qualified_model_id(id), Some(provider));
        }
        for id in [
            "same-model",
            "unknown:same-model",
            "openrouter:",
            ":same-model",
        ] {
            assert!(catalog_model_in(&catalog, id).is_none(), "accepted {id}");
            assert_eq!(Provider::from_qualified_model_id(id), None, "accepted {id}");
        }
    }
}
