//! Translation between ACP content and transcript records: prompt input to
//! accepted text, and records to session updates for live delivery and replay.

use agent_client_protocol::{
    Error, Result,
    schema::v1::{
        ContentBlock, ContentChunk, SessionUpdate, TextContent, ToolCall as AcpToolCall,
        ToolCallContent, ToolCallId, ToolCallStatus, ToolCallUpdate, ToolCallUpdateFields,
    },
};
use serde_json::Value;

use crate::{
    sessions::{ToolCall, ToolOutcome, ToolResult, TranscriptEvent},
    tools,
};

/// Text blocks and resource links become one text input; a link contributes
/// its name and URI and is not fetched. Anything else is invalid input.
pub fn prompt_text(content: &[ContentBlock]) -> Result<String> {
    let mut parts = Vec::new();
    for block in content {
        match block {
            ContentBlock::Text(text) => parts.push(text.text.clone()),
            ContentBlock::ResourceLink(link) => {
                parts.push(format!("Resource link: {}\nURI: {}", link.name, link.uri));
            }
            _ => {
                return Err(Error::invalid_params()
                    .data("prompts may contain only text and resource links"));
            }
        }
    }
    let text = parts.join("\n");
    if text.trim().is_empty() {
        return Err(Error::invalid_params().data("prompt contains no text"));
    }
    Ok(text)
}

pub fn user_message(text: &str) -> SessionUpdate {
    SessionUpdate::UserMessageChunk(text_chunk(text))
}

pub fn agent_text(text: &str) -> SessionUpdate {
    SessionUpdate::AgentMessageChunk(text_chunk(text))
}

pub fn agent_reasoning(text: &str) -> SessionUpdate {
    SessionUpdate::AgentThoughtChunk(text_chunk(text))
}

fn text_chunk(text: &str) -> ContentChunk {
    ContentChunk::new(ContentBlock::Text(TextContent::new(text)))
}

/// Announces a call the model made, before anything runs.
pub fn tool_call_pending(call: &ToolCall) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(ToolCallId::new(call.call_id.clone()), tools::title(call))
            .status(ToolCallStatus::Pending)
            .raw_input(raw_input(call)),
    )
}

pub fn tool_call_in_progress(call_id: &str) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(call_id.to_owned()),
        ToolCallUpdateFields::new().status(ToolCallStatus::InProgress),
    ))
}

pub fn tool_result(result: &ToolResult) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(result.call_id.clone()),
        ToolCallUpdateFields::new()
            .status(status(&result.outcome))
            .content(vec![output_content(&result.outcome)])
            .raw_output(raw_output(&result.outcome)),
    ))
}

/// Replays committed history as displayable content and terminal tool states.
/// The model and continuation metadata are never shown.
pub fn replay(
    transcript: &[TranscriptEvent],
    mut deliver: impl FnMut(SessionUpdate) -> Result<()>,
) -> Result<()> {
    let mut calls: &[ToolCall] = &[];
    for event in transcript {
        match event {
            TranscriptEvent::Model(_) => {}
            TranscriptEvent::UserMessage(text) => deliver(user_message(text))?,
            TranscriptEvent::AssistantMessage(message) => {
                if !message.reasoning.is_empty() {
                    deliver(agent_reasoning(&message.reasoning))?;
                }
                if !message.text.is_empty() {
                    deliver(agent_text(&message.text))?;
                }
                calls = &message.tool_calls;
            }
            TranscriptEvent::ToolResult(result) => {
                let call = calls
                    .iter()
                    .find(|call| call.call_id == result.call_id)
                    .expect("a stored tool result follows the assistant message that called it");
                deliver(replayed_tool_call(call, result))?;
            }
        }
    }
    Ok(())
}

fn replayed_tool_call(call: &ToolCall, result: &ToolResult) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(ToolCallId::new(call.call_id.clone()), tools::title(call))
            .status(status(&result.outcome))
            .raw_input(raw_input(call))
            .content(vec![output_content(&result.outcome)])
            .raw_output(raw_output(&result.outcome)),
    )
}

fn raw_input(call: &ToolCall) -> Value {
    serde_json::from_str(&call.arguments).unwrap_or_else(|_| Value::String(call.arguments.clone()))
}

fn raw_output(outcome: &ToolOutcome) -> Value {
    Value::String(outcome.text().to_owned())
}

fn output_content(outcome: &ToolOutcome) -> ToolCallContent {
    ToolCallContent::from(ContentBlock::Text(TextContent::new(outcome.text())))
}

fn status(outcome: &ToolOutcome) -> ToolCallStatus {
    match outcome {
        ToolOutcome::Completed(_) => ToolCallStatus::Completed,
        ToolOutcome::Failed(_) | ToolOutcome::Cancelled(_) => ToolCallStatus::Failed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use agent_client_protocol::schema::v1::{ErrorCode, ImageContent, ResourceLink};

    #[test]
    fn prompt_text_keeps_resource_links_and_rejects_other_or_blank_content() {
        let text = prompt_text(&[
            ContentBlock::Text(TextContent::new("Review this")),
            ContentBlock::ResourceLink(ResourceLink::new(
                "src/main.rs",
                "file:///workspace/src/main.rs",
            )),
        ])
        .unwrap();
        assert_eq!(
            text,
            "Review this\nResource link: src/main.rs\nURI: file:///workspace/src/main.rs"
        );

        let image =
            prompt_text(&[ContentBlock::Image(ImageContent::new("", "image/png"))]).unwrap_err();
        assert_eq!(image.code, ErrorCode::InvalidParams);

        let blank = prompt_text(&[ContentBlock::Text(TextContent::new("  \n\t"))]).unwrap_err();
        assert_eq!(blank.code, ErrorCode::InvalidParams);
        assert_eq!(prompt_text(&[]).unwrap_err().code, ErrorCode::InvalidParams);
    }

    #[test]
    fn tool_updates_carry_titles_terminal_states_and_unparsable_arguments() {
        let call = ToolCall {
            call_id: "call-1".to_owned(),
            name: "get_weather".to_owned(),
            arguments: "{\"loc".to_owned(),
        };
        assert!(matches!(
            tool_call_pending(&call),
            SessionUpdate::ToolCall(update)
                if update.status == ToolCallStatus::Pending
                    && update.title == "Weather"
                    && update.raw_input == Some(Value::String("{\"loc".to_owned()))
        ));
        let cancelled = ToolResult {
            call_id: "call-1".to_owned(),
            name: "get_weather".to_owned(),
            outcome: ToolOutcome::Cancelled("Cancelled before this tool was started.".to_owned()),
        };
        assert!(matches!(
            tool_result(&cancelled),
            SessionUpdate::ToolCallUpdate(update)
                if update.fields.status == Some(ToolCallStatus::Failed)
                    && update.fields.raw_output
                        == Some(Value::String("Cancelled before this tool was started.".to_owned()))
        ));
    }
}
