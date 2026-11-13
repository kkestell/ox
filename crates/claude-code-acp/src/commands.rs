//! Claude's command descriptors translated to ACP's slash-command catalog.

use std::collections::HashSet;

use agent_client_protocol::v2::schema::{
    AvailableCommand, AvailableCommandInput, Command, TextCommandInput,
};
use serde_json::Value;

pub fn catalog(value: &Value) -> Vec<AvailableCommand> {
    let mut names = HashSet::new();
    value
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|entry| {
            let name = entry["name"].as_str()?;
            let description = entry["description"].as_str()?;
            if name.is_empty() || name.chars().any(char::is_whitespace) || !names.insert(name) {
                return None;
            }
            let input = entry["argumentHint"]
                .as_str()
                .filter(|hint| !hint.is_empty())
                .map(|hint| AvailableCommandInput::Text(TextCommandInput::new(hint)));
            Some(AvailableCommand::Command(
                Command::new(name, description).input(input),
            ))
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    #[test]
    fn translates_native_descriptors_in_order_with_optional_hints() {
        let commands = catalog(&json!([
            {"name":"plugin:review", "description":"Review", "argumentHint":"file"},
            {"name":"clear", "description":"Clear", "argumentHint":"", "builtin":true},
            {"name":"plugin:review", "description":"Duplicate"},
            {"name":"bad name", "description":"Invalid"},
            {"name":"", "description":"Empty"},
            {"name":"missing-description"}
        ]));
        assert_eq!(
            serde_json::to_value(commands).unwrap(),
            json!([
                {"type":"command", "name":"plugin:review", "description":"Review",
                 "input":{"type":"text", "hint":"file"}},
                {"type":"command", "name":"clear", "description":"Clear"}
            ])
        );
        assert!(catalog(&Value::Null).is_empty());
        assert!(catalog(&json!([])).is_empty());
    }
}
