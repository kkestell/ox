use rig::{
    agent::{Agent, AgentHook, StreamingResult, ToolResultAction, ToolResultEvent},
    client::ProviderClientError,
    completion::Message,
    prelude::{AgentClientExt, StreamingChat, VerifyClient},
    providers::openrouter,
    tool::{Tool, ToolContext},
};
use std::{
    collections::HashMap,
    convert::Infallible,
    error::Error,
    sync::{Arc, Mutex},
};

const MODEL: &str = "openai/gpt-5.6-luna";
const MAX_TURNS: usize = 8;

#[derive(serde::Deserialize)]
struct WeatherArgs {
    location: String,
}

#[derive(Clone)]
struct GetWeather;

impl Tool for GetWeather {
    const NAME: &'static str = "get_weather";
    type Args = WeatherArgs;
    type Output = String;
    type Error = Infallible;

    fn description(&self) -> String {
        "Get the weather for a location".to_owned()
    }

    fn parameters(&self) -> serde_json::Value {
        serde_json::json!({
            "type": "object",
            "properties": { "location": { "type": "string" } },
            "required": ["location"],
        })
    }

    async fn call(
        &self,
        _context: &mut ToolContext,
        args: Self::Args,
    ) -> Result<Self::Output, Self::Error> {
        Ok(format!(
            "The weather in {} is warm and sunny.",
            args.location
        ))
    }
}

pub fn tool_call_title(name: &str, arguments: &serde_json::Value) -> String {
    match name {
        GetWeather::NAME => arguments
            .get("location")
            .and_then(serde_json::Value::as_str)
            .map(|location| format!("Weather {location}"))
            .unwrap_or_else(|| "Weather".to_owned()),
        _ => name.to_owned(),
    }
}

#[derive(Clone)]
pub struct OxAgent(Agent);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ToolOutcomeStatus {
    Completed,
    Failed,
}

fn tool_outcome_status(result: &rig::tool::ToolResult) -> ToolOutcomeStatus {
    if result.is_success() {
        ToolOutcomeStatus::Completed
    } else {
        ToolOutcomeStatus::Failed
    }
}

#[derive(Clone, Default)]
pub struct ToolOutcomeTracker(Arc<Mutex<HashMap<String, ToolOutcomeStatus>>>);

impl ToolOutcomeTracker {
    fn record(&self, internal_call_id: &str, status: ToolOutcomeStatus) {
        self.0
            .lock()
            .expect("tool outcome tracker mutex poisoned")
            .insert(internal_call_id.to_owned(), status);
    }

    pub fn take(&self, internal_call_id: &str) -> ToolOutcomeStatus {
        self.0
            .lock()
            .expect("tool outcome tracker mutex poisoned")
            .remove(internal_call_id)
            .expect("tool result hook runs before the streamed tool result")
    }
}

impl AgentHook for ToolOutcomeTracker {
    async fn on_tool_result(
        &self,
        _context: &rig::agent::HookContext,
        event: ToolResultEvent<'_>,
    ) -> ToolResultAction {
        self.record(
            event.internal_call_id,
            tool_outcome_status(event.raw_result),
        );
        ToolResultAction::keep()
    }
}

impl OxAgent {
    pub fn new(api_key: &str) -> Result<Self, ProviderClientError> {
        let client = openrouter::Client::new(api_key)?;
        Ok(Self(client.agent(MODEL).tool(GetWeather).build()))
    }

    pub async fn stream(
        &self,
        prompt: String,
        history: Vec<Message>,
    ) -> (StreamingResult, ToolOutcomeTracker) {
        let outcomes = ToolOutcomeTracker::default();
        let stream = self
            .0
            .stream_chat(prompt, history)
            .max_turns(MAX_TURNS)
            .add_hook(outcomes.clone())
            .await;
        (stream, outcomes)
    }
}

pub async fn verify_api_key(api_key: &str) -> Result<(), Box<dyn Error>> {
    openrouter::Client::new(api_key)?.verify().await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn weather_is_canned() {
        let result = futures::executor::block_on(GetWeather.call(
            &mut ToolContext::new(),
            WeatherArgs {
                location: "Chicago".to_owned(),
            },
        ))
        .unwrap();
        assert_eq!(result, "The weather in Chicago is warm and sunny.");
    }

    #[test]
    fn weather_tool_call_title_includes_the_location() {
        assert_eq!(
            tool_call_title(
                GetWeather::NAME,
                &serde_json::json!({ "location": "Minneapolis, MN" }),
            ),
            "Weather Minneapolis, MN"
        );
        assert_eq!(
            tool_call_title(GetWeather::NAME, &serde_json::json!({})),
            "Weather"
        );
    }

    #[test]
    fn tool_outcomes_use_rigs_structured_status() {
        let completed = rig::tool::ToolResult::success(rig::tool::ToolOutput::text("done"));
        let failed = rig::tool::ToolResult::failed(rig::tool::ToolExecutionError::other("broken"));

        assert_eq!(
            tool_outcome_status(&completed),
            ToolOutcomeStatus::Completed
        );
        assert_eq!(tool_outcome_status(&failed), ToolOutcomeStatus::Failed);

        let outcomes = ToolOutcomeTracker::default();
        outcomes.record("completed", tool_outcome_status(&completed));
        outcomes.record("failed", tool_outcome_status(&failed));

        assert_eq!(outcomes.take("completed"), ToolOutcomeStatus::Completed);
        assert_eq!(outcomes.take("failed"), ToolOutcomeStatus::Failed);
    }
}
