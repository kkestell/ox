use std::convert::Infallible;

use rig::tool::{Tool, ToolContext};

#[derive(serde::Deserialize)]
pub(crate) struct WeatherArgs {
    location: String,
}

#[derive(Clone)]
pub(crate) struct GetWeather;

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

pub(crate) fn tool_call_title(name: &str, arguments: &serde_json::Value) -> String {
    match name {
        GetWeather::NAME => arguments
            .get("location")
            .and_then(serde_json::Value::as_str)
            .map(|location| format!("Weather {location}"))
            .unwrap_or_else(|| "Weather".to_owned()),
        _ => name.to_owned(),
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
}
