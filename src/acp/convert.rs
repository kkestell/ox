//! Converts ACP prompt content into a user message and transcript entries into
//! ACP updates for live output and session replay.

use agent_client_protocol::{
    Error, Result,
    schema::v1::{
        ContentBlock, ContentChunk, PermissionOption, PermissionOptionKind,
        RequestPermissionRequest, SessionId, SessionUpdate, TextContent, ToolCall as AcpToolCall,
        ToolCallContent, ToolCallId, ToolCallStatus, ToolCallUpdate, ToolCallUpdateFields,
        ToolKind,
    },
};
use serde_json::Value;

use crate::{
    sessions::{ToolCall, ToolOutcome, ToolResult, TranscriptEntry},
    tools,
};

/// Text blocks and resource links become one text input; a link contributes
/// its name and URI and is not fetched. Anything else is invalid input.
pub fn prompt_to_user_message(content: &[ContentBlock]) -> Result<String> {
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

pub fn user_message_chunk(text: &str) -> SessionUpdate {
    SessionUpdate::UserMessageChunk(text_chunk(text))
}

pub fn agent_message_chunk(text: &str) -> SessionUpdate {
    SessionUpdate::AgentMessageChunk(text_chunk(text))
}

pub fn agent_thought_chunk(text: &str) -> SessionUpdate {
    SessionUpdate::AgentThoughtChunk(text_chunk(text))
}

fn text_chunk(text: &str) -> ContentChunk {
    ContentChunk::new(ContentBlock::Text(TextContent::new(text)))
}

/// Announces a call the model made, before anything runs.
pub fn pending_tool_call(call: &ToolCall) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(
            ToolCallId::new(call.call_id.clone()),
            tools::tool_call_title(call),
        )
        .kind(tool_kind(call))
        .status(ToolCallStatus::Pending)
        .raw_input(raw_input(call)),
    )
}

/// The kind an ACP client uses to pick an icon for a call.
fn tool_kind(call: &ToolCall) -> ToolKind {
    match call.name.as_str() {
        tools::SHELL => ToolKind::Execute,
        tools::READ_FILE => ToolKind::Read,
        tools::GLOB | tools::GREP => ToolKind::Search,
        tools::APPLY_PATCH => ToolKind::Edit,
        _ => ToolKind::Other,
    }
}

pub fn in_progress_tool_call_update(call_id: &str) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(call_id.to_owned()),
        ToolCallUpdateFields::new().status(ToolCallStatus::InProgress),
    ))
}

pub fn shell_permission_request(
    session_id: SessionId,
    call: &ToolCall,
    workspace: &std::path::Path,
) -> RequestPermissionRequest {
    let input = raw_input(call);
    let (label, text) = match input.get("command").and_then(Value::as_str) {
        Some(command) => ("Command", command),
        None => ("Arguments", call.arguments.as_str()),
    };
    let tool_call_title = tools::tool_call_title(call);
    let mut content = format!("Working directory: {}", workspace.display());
    // The tool call title is one shortened line, so repeat the command as
    // content only when the tool call title does not already show all of it.
    // Clients can omit rawInput from the approval UI, so content is the only
    // other place it appears.
    if tool_call_title != text.trim() {
        let indented: String = text
            .split_inclusive('\n')
            .map(|line| format!("    {line}"))
            .collect();
        content.push_str(&format!("\n\n{label}:\n\n{indented}"));
    }
    RequestPermissionRequest::new(
        session_id,
        ToolCallUpdate::new(
            ToolCallId::new(call.call_id.clone()),
            ToolCallUpdateFields::new()
                .title(tool_call_title)
                .kind(tool_kind(call))
                .status(ToolCallStatus::Pending)
                .raw_input(input)
                .content(vec![ToolCallContent::from(ContentBlock::Text(
                    TextContent::new(content),
                ))]),
        ),
        vec![
            PermissionOption::new("approve", "Approve", PermissionOptionKind::AllowOnce),
            PermissionOption::new("deny", "Deny", PermissionOptionKind::RejectOnce),
        ],
    )
}

pub fn finished_tool_call_update(result: &ToolResult) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(result.call_id.clone()),
        ToolCallUpdateFields::new()
            .status(status(&result.outcome))
            .content(vec![output_content(&result.outcome)])
            .raw_output(raw_output(&result.outcome)),
    ))
}

/// Sends the saved transcript as displayable content and final tool states.
/// The model and continuation metadata are never shown.
pub fn replay_transcript(
    transcript: &[TranscriptEntry],
    mut send_update: impl FnMut(SessionUpdate) -> Result<()>,
) -> Result<()> {
    let mut calls: &[ToolCall] = &[];
    for entry in transcript {
        match entry {
            TranscriptEntry::Model(_) | TranscriptEntry::Effort(_) | TranscriptEntry::Mode(_) => {}
            TranscriptEntry::UserMessage(text) => send_update(user_message_chunk(text))?,
            TranscriptEntry::AssistantMessage(message) => {
                if !message.reasoning.is_empty() {
                    send_update(agent_thought_chunk(&message.reasoning))?;
                }
                if !message.text.is_empty() {
                    send_update(agent_message_chunk(&message.text))?;
                }
                calls = &message.tool_calls;
            }
            TranscriptEntry::ToolResult(result) => {
                let call = calls
                    .iter()
                    .find(|call| call.call_id == result.call_id)
                    .expect("a stored tool result follows the assistant message that called it");
                send_update(replayed_tool_call(call, result))?;
            }
        }
    }
    Ok(())
}

fn replayed_tool_call(call: &ToolCall, result: &ToolResult) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(
            ToolCallId::new(call.call_id.clone()),
            tools::tool_call_title(call),
        )
        .kind(tool_kind(call))
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
    fn prompt_to_user_message_keeps_links_and_rejects_other_or_blank_content() {
        let text = prompt_to_user_message(&[
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
            prompt_to_user_message(&[ContentBlock::Image(ImageContent::new("", "image/png"))])
                .unwrap_err();
        assert_eq!(image.code, ErrorCode::InvalidParams);

        let blank =
            prompt_to_user_message(&[ContentBlock::Text(TextContent::new("  \n\t"))]).unwrap_err();
        assert_eq!(blank.code, ErrorCode::InvalidParams);
        assert_eq!(
            prompt_to_user_message(&[]).unwrap_err().code,
            ErrorCode::InvalidParams
        );
    }

    #[test]
    fn shell_approval_content_shows_a_command_the_tool_call_title_cannot_show_in_full() {
        let command = "printf '%s\\n' '```'\n  echo \"$HOME\"\n";
        let arguments = serde_json::json!({"command": command, "timeout_seconds": 5});
        let mut call = ToolCall {
            call_id: "shell-1".to_owned(),
            name: tools::SHELL.to_owned(),
            arguments: arguments.to_string(),
        };
        for (input, expected) in [
            (
                arguments.to_string(),
                "Working directory: /workspace\n\nCommand:\n\n    printf '%s\\n' '```'\n      echo \"$HOME\"\n",
            ),
            // A one-line command already appears in full as the tool call title.
            (
                serde_json::json!({"command": "echo hello  \n\n"}).to_string(),
                "Working directory: /workspace",
            ),
            (
                "{bad json".to_owned(),
                "Working directory: /workspace\n\nArguments:\n\n    {bad json",
            ),
        ] {
            call.arguments = input;
            let request = shell_permission_request(
                SessionId::new("session-1"),
                &call,
                std::path::Path::new("/workspace"),
            );
            let request = serde_json::to_value(request).unwrap();
            assert_eq!(
                request["toolCall"]["content"][0]["content"]["text"],
                expected
            );
            assert_eq!(request["toolCall"]["rawInput"], raw_input(&call));
        }
    }

    #[test]
    fn tool_updates_carry_tool_call_titles_failed_statuses_and_unparsable_arguments() {
        let call = ToolCall {
            call_id: "call-1".to_owned(),
            name: tools::SHELL.to_owned(),
            arguments: "{\"comm".to_owned(),
        };
        assert!(matches!(
            pending_tool_call(&call),
            SessionUpdate::ToolCall(update)
                if update.status == ToolCallStatus::Pending
                    && update.title == "Run shell command"
                    && update.raw_input == Some(Value::String("{\"comm".to_owned()))
        ));
        let cancelled = ToolResult {
            call_id: "call-1".to_owned(),
            name: tools::SHELL.to_owned(),
            outcome: ToolOutcome::Cancelled("Cancelled before this tool was started.".to_owned()),
        };
        assert!(matches!(
            finished_tool_call_update(&cancelled),
            SessionUpdate::ToolCallUpdate(update)
                if update.fields.status == Some(ToolCallStatus::Failed)
                    && update.fields.raw_output
                        == Some(Value::String("Cancelled before this tool was started.".to_owned()))
        ));
    }
}
