//! The concrete tool set: schemas sent to the model, display titles, and
//! execution of one complete call.

use std::path::Path;

use serde::Deserialize;
use serde_json::{Value, json};

use crate::sessions::{ToolCall, ToolOutcome};

mod patch;

pub const GET_WEATHER: &str = "get_weather";
pub const APPLY_PATCH: &str = "apply_patch";

pub fn schemas() -> Vec<Value> {
    vec![
        json!({
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
        }),
        json!({
            "type": "function",
            "function": {
                "name": APPLY_PATCH,
                "description": include_str!("tools/patch-guide.txt"),
                "parameters": {
                    "type": "object",
                    "properties": {
                        "patch": {
                            "type": "string",
                            "description": "A patch beginning with *** Begin Patch and ending with *** End Patch."
                        }
                    },
                    "required": ["patch"],
                    "additionalProperties": false
                }
            }
        }),
    ]
}

#[derive(Deserialize)]
struct WeatherArgs {
    location: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PatchArgs {
    patch: String,
}

pub fn title(call: &ToolCall) -> String {
    match call.name.as_str() {
        APPLY_PATCH => "Apply patch".to_owned(),
        GET_WEATHER => match serde_json::from_str::<WeatherArgs>(&call.arguments) {
            Ok(args) => format!("Weather {}", args.location),
            Err(_) => "Weather".to_owned(),
        },
        other => other.to_owned(),
    }
}

/// Unknown names and invalid arguments are failed results the model can read
/// on its next request, not errors that end the prompt.
pub async fn execute(workspace_path: &Path, call: &ToolCall) -> ToolOutcome {
    match call.name.as_str() {
        APPLY_PATCH => match serde_json::from_str::<PatchArgs>(&call.arguments) {
            Ok(args) => match patch::apply(workspace_path, &args.patch) {
                Ok(summary) => ToolOutcome::Completed(summary),
                Err(error) => ToolOutcome::Failed(error),
            },
            Err(error) => ToolOutcome::Failed(format!("arguments: {error}")),
        },
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
pub(crate) mod fixture {
    use std::{fs, path::PathBuf};

    pub struct Workspace(pub PathBuf);

    impl Workspace {
        pub fn new() -> Self {
            let path = std::env::temp_dir().join(format!("ox-patch-{}", uuid::Uuid::new_v4()));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for Workspace {
        fn drop(&mut self) {
            fs::remove_dir_all(&self.0).unwrap();
        }
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
    async fn patch_schema_title_and_argument_errors() {
        let schema = schemas()
            .into_iter()
            .find(|schema| schema["function"]["name"] == APPLY_PATCH)
            .unwrap();
        assert_eq!(
            schema["function"]["parameters"]["required"],
            json!(["patch"])
        );
        assert_eq!(
            schema["function"]["parameters"]["additionalProperties"],
            false
        );
        assert!(
            schema["function"]["description"]
                .as_str()
                .unwrap()
                .contains("*** Begin Patch")
        );
        for arguments in ["{", "{}", r#"{"patch": 1}"#, r#"{"patch": "", "cwd": "/"}"#] {
            let call = call(APPLY_PATCH, arguments);
            assert_eq!(title(&call), "Apply patch");
            assert!(
                matches!(execute(Path::new("/unused"), &call).await, ToolOutcome::Failed(error) if error.starts_with("arguments:"))
            );
        }
    }

    #[tokio::test]
    async fn weather_is_canned_and_bad_calls_fail_as_results() {
        assert_eq!(
            execute(
                Path::new("/workspace"),
                &call(GET_WEATHER, r#"{"location":"Chicago"}"#),
            )
            .await,
            ToolOutcome::Completed("The weather in Chicago is warm and sunny.".to_owned())
        );
        assert!(matches!(
            execute(Path::new("/workspace"), &call(GET_WEATHER, r#"{"loc"#)).await,
            ToolOutcome::Failed(message) if message.starts_with("Invalid arguments for get_weather")
        ));
        assert_eq!(
            execute(Path::new("/workspace"), &call("launch", "{}")).await,
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
