//! The concrete tool set: schemas sent to the model, display titles, and
//! execution of one complete call.

use serde::Deserialize;
use serde_json::{Value, json};

use crate::sessions::{ToolCall, ToolOutcome};

pub const GET_WEATHER: &str = "get_weather";

pub fn schemas() -> Vec<Value> {
    vec![json!({
        "type": "function",
        "function": {
            "name": GET_WEATHER,
            "description": "Get the weather for a location",
            "parameters": {
                "type": "object",
                "properties": { "location": { "type": "string" } },
                "required": ["location"],
            },
        },
    })]
}

#[derive(Deserialize)]
struct WeatherArgs {
    location: String,
}

pub fn title(call: &ToolCall) -> String {
    match call.name.as_str() {
        GET_WEATHER => match serde_json::from_str::<WeatherArgs>(&call.arguments) {
            Ok(args) => format!("Weather {}", args.location),
            Err(_) => "Weather".to_owned(),
        },
        other => other.to_owned(),
    }
}

/// Unknown names and invalid arguments are failed results the model can read
/// on its next request, not errors that end the prompt.
pub async fn execute(call: &ToolCall) -> ToolOutcome {
    match call.name.as_str() {
        GET_WEATHER => match serde_json::from_str::<WeatherArgs>(&call.arguments) {
            Ok(args) => ToolOutcome::Completed(format!(
                "The weather in {} is warm and sunny.",
                args.location
            )),
            Err(error) => {
                ToolOutcome::Failed(format!("Invalid arguments for {GET_WEATHER}: {error}"))
            }
        },
        other => ToolOutcome::Failed(format!("Unknown tool: {other}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn call(name: &str, arguments: &str) -> ToolCall {
        ToolCall {
            call_id: "call-1".to_owned(),
            name: name.to_owned(),
            arguments: arguments.to_owned(),
        }
    }

    #[tokio::test]
    async fn weather_is_canned_and_bad_calls_fail_as_results() {
        assert_eq!(
            execute(&call(GET_WEATHER, r#"{"location":"Chicago"}"#)).await,
            ToolOutcome::Completed("The weather in Chicago is warm and sunny.".to_owned())
        );
        assert!(matches!(
            execute(&call(GET_WEATHER, r#"{"loc"#)).await,
            ToolOutcome::Failed(message) if message.starts_with("Invalid arguments for get_weather")
        ));
        assert_eq!(
            execute(&call("launch", "{}")).await,
            ToolOutcome::Failed("Unknown tool: launch".to_owned())
        );
    }

    #[test]
    fn weather_title_includes_the_location_when_parsable() {
        assert_eq!(
            title(&call(GET_WEATHER, r#"{"location":"Minneapolis, MN"}"#)),
            "Weather Minneapolis, MN"
        );
        assert_eq!(title(&call(GET_WEATHER, "{}")), "Weather");
        assert_eq!(title(&call("launch", "{}")), "launch");
    }
}
