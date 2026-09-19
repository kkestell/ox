use rig::{
    agent::{Agent, StreamingResult},
    client::ProviderClientError,
    completion::Message,
    prelude::{AgentClientExt, ProviderClient, StreamingChat},
    providers::openrouter,
    tool::{Tool, ToolContext},
};
use std::convert::Infallible;

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

#[derive(Clone)]
pub struct OxAgent(Agent);

impl OxAgent {
    pub fn new() -> Result<Self, ProviderClientError> {
        let client = openrouter::Client::from_env()?;
        Ok(Self(client.agent(MODEL).tool(GetWeather).build()))
    }

    pub async fn stream(&self, prompt: String, history: Vec<Message>) -> StreamingResult {
        self.0
            .stream_chat(prompt, history)
            .max_turns(MAX_TURNS)
            .await
    }
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
}
