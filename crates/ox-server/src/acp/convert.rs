//! Converts ACP prompt content into user messages and transcript entries into
//! ACP updates for live output and session replay.

use agent_client_protocol::{
    Error, Result,
    schema::v1::{
        ContentBlock, ContentChunk, Cost, Diff, Meta, PermissionOption, PermissionOptionKind,
        RequestPermissionRequest, SessionUpdate, TextContent, ToolCall as AcpToolCall,
        ToolCallContent, ToolCallId, ToolCallStatus, ToolCallUpdate, ToolCallUpdateFields,
        ToolKind, UsageUpdate,
    },
};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde_json::Value;

use super::prompt::AcpIdentity;
use crate::{
    model::ModelRequestParameters,
    sessions::{
        self, ImageAttachment, SubagentMessage, SubagentMessageContent, ToolCall, ToolContent,
        ToolOutcome, ToolStatus, TranscriptEntry, TurnInput, UserMessage, UserMessagePart,
    },
    tools,
};

const MAX_IMAGES: usize = 4;
const MAX_IMAGE_BYTES: usize = 10 * 1024 * 1024;

/// Preserve text and image order. Resource links contribute text and are not
/// fetched.
pub fn prompt_message(content: &[ContentBlock]) -> Result<UserMessage> {
    let mut parts = Vec::new();
    let mut image_count = 0;
    let mut image_bytes = 0;
    for block in content {
        match block {
            ContentBlock::Text(text) => parts.push(UserMessagePart::Text(text.text.clone())),
            ContentBlock::ResourceLink(link) => {
                parts.push(UserMessagePart::Text(format!(
                    "Resource link: {}\nURI: {}",
                    link.name, link.uri
                )));
            }
            ContentBlock::Image(image) => {
                image_count += 1;
                if image_count > MAX_IMAGES {
                    return Err(Error::invalid_params().data("prompt contains too many images"));
                }
                if !matches!(
                    image.mime_type.as_str(),
                    "image/png" | "image/jpeg" | "image/webp" | "image/gif"
                ) {
                    return Err(Error::invalid_params().data("unsupported image MIME type"));
                }
                if image.data.len() > (MAX_IMAGE_BYTES - image_bytes).div_ceil(3) * 4 + 4 {
                    return Err(Error::invalid_params().data("prompt images exceed 10 MiB"));
                }
                let decoded = STANDARD
                    .decode(&image.data)
                    .map_err(|_| Error::invalid_params().data("image data is not valid base64"))?;
                if decoded.is_empty() || decoded.len() > MAX_IMAGE_BYTES - image_bytes {
                    return Err(Error::invalid_params().data("prompt images exceed 10 MiB"));
                }
                image_bytes += decoded.len();
                parts.push(UserMessagePart::Image(ImageAttachment {
                    data: image.data.clone(),
                    mime_type: image.mime_type.clone(),
                }));
            }
            _ => {
                return Err(Error::invalid_params()
                    .data("prompts may contain only text, resource links, and images"));
            }
        }
    }
    let message = UserMessage { parts };
    if message.text().trim().is_empty() && !message.has_images() {
        return Err(Error::invalid_params().data("prompt contains no text or image"));
    }
    Ok(message)
}

pub fn user_message_chunk(text: &str) -> SessionUpdate {
    SessionUpdate::UserMessageChunk(text_chunk(text))
}

fn user_image_chunk(image: &ImageAttachment) -> SessionUpdate {
    SessionUpdate::UserMessageChunk(ContentChunk::new(ContentBlock::Image(
        agent_client_protocol::schema::v1::ImageContent::new(&image.data, &image.mime_type),
    )))
}

fn user_message_updates(message: &UserMessage) -> impl Iterator<Item = SessionUpdate> + '_ {
    message.parts.iter().map(|part| match part {
        UserMessagePart::Text(text) => user_message_chunk(text),
        UserMessagePart::Image(image) => user_image_chunk(image),
    })
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

/// Reports the context tokens, the context limit, and the session cost: the
/// main transcript's saved cost plus `children_cost`, the saved cost of its
/// child sessions. The context tokens are the latest assistant message's
/// reported usage, or 0 when it reported none. `None` before the first
/// assistant message.
pub fn usage_update(
    transcript: &[TranscriptEntry],
    parameters: &ModelRequestParameters,
    children_cost: Option<f64>,
) -> Option<SessionUpdate> {
    let latest = transcript.iter().rev().find_map(|entry| match entry {
        TranscriptEntry::AssistantBatch(batch) => Some(batch),
        _ => None,
    })?;
    let used = latest
        .message
        .usage
        .as_ref()
        .map_or(0, |usage| usage.input_tokens + usage.output_tokens);
    let size = parameters.model.context_limit as u64;
    let cost = match (sessions::transcript_cost(transcript), children_cost) {
        (Some(main), Some(children)) => Some(main + children),
        (main, children) => main.or(children),
    }
    .map(|amount| Cost::new(amount, "USD"));
    Some(SessionUpdate::UsageUpdate(
        UsageUpdate::new(used, size).cost(cost),
    ))
}

/// Announces a call the model made, before anything runs.
pub fn pending_tool_call(call: &ToolCall) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(
            ToolCallId::new(call.call_id.clone()),
            tools::tool_call_title(call),
        )
        .name(call.name.clone())
        .kind(tool_kind(call))
        .status(ToolCallStatus::Pending)
        .raw_input(raw_input(call)),
    )
}

/// The kind an ACP client uses to pick an icon for a call.
fn tool_kind(call: &ToolCall) -> ToolKind {
    match call.name.as_str() {
        tools::SHELL | tools::SHELL_PROCESS => ToolKind::Execute,
        tools::READ_FILE => ToolKind::Read,
        tools::GLOB | tools::GREP => ToolKind::Search,
        tools::WRITE_FILE | tools::EDIT_FILE => ToolKind::Edit,
        _ => ToolKind::Other,
    }
}

pub fn in_progress_tool_call_update(call_id: &str) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(call_id.to_owned()),
        ToolCallUpdateFields::new().status(ToolCallStatus::InProgress),
    ))
}

/// Asks whether one shell command may run or one input may be sent to a shell
/// process. Clients can omit rawInput from the approval UI, so the content
/// shows everything being approved. A subagent's request goes to the main
/// session, with its tool call ID and title scoped by the subagent ID so they
/// cannot collide with the main agent's.
pub fn shell_permission_request(
    identity: &AcpIdentity,
    call: &ToolCall,
    workspace: &std::path::Path,
    permission: &tools::Permission,
) -> RequestPermissionRequest {
    let input = raw_input(call);
    let tool_call_title = tools::tool_call_title(call);
    let mut content = match permission {
        tools::Permission::NotRequired => {
            unreachable!("a call that needs no permission sends no permission request")
        }
        tools::Permission::Command => {
            let (label, text) = match input.get("command").and_then(Value::as_str) {
                Some(command) => ("Command", command),
                None => ("Arguments", call.arguments.as_str()),
            };
            format!(
                "Working directory: {}\n\n{label}:\n\n{}",
                workspace.display(),
                indented(text)
            )
        }
        tools::Permission::Input {
            process_id,
            command,
            text,
            close_stdin,
        } => {
            let command = match command {
                Some(command) => indented(command),
                None => "    (no shell process with this ID that this agent started)".to_owned(),
            };
            let text = if text.is_empty() {
                "    (none)".to_owned()
            } else {
                indented(text)
            };
            let closes = if *close_stdin { "yes" } else { "no" };
            format!(
                "Shell process: {process_id}\n\nCommand:\n\n{command}\n\nInput:\n\n{text}\n\nCloses stdin afterward: {closes}"
            )
        }
    };
    let (tool_call_id, tool_call_title, meta) = match &identity.subagent_id {
        Some(subagent_id) => {
            content = format!("Subagent: {subagent_id}\n\n{content}");
            let mut meta = Meta::new();
            meta.insert(
                "subagent_id".to_owned(),
                Value::String(subagent_id.to_string()),
            );
            (
                format!("{subagent_id}:{}", call.call_id),
                format!("Subagent {subagent_id}: {tool_call_title}"),
                Some(meta),
            )
        }
        None => (call.call_id.clone(), tool_call_title, None),
    };
    RequestPermissionRequest::new(
        identity.session_id.clone(),
        ToolCallUpdate::new(
            ToolCallId::new(tool_call_id),
            ToolCallUpdateFields::new()
                .title(tool_call_title)
                .name(call.name.clone())
                .kind(tool_kind(call))
                .status(ToolCallStatus::Pending)
                .raw_input(input)
                .content(vec![ToolCallContent::from(ContentBlock::Text(
                    TextContent::new(content),
                ))]),
        )
        .meta(meta),
        vec![
            PermissionOption::new("approve", "Yes", PermissionOptionKind::AllowOnce),
            PermissionOption::new("deny", "No", PermissionOptionKind::RejectOnce),
        ],
    )
}

fn indented(text: &str) -> String {
    text.split_inclusive('\n')
        .map(|line| format!("    {line}"))
        .collect()
}

pub fn finished_tool_call_update(call: &ToolCall, outcome: &ToolOutcome) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
        ToolCallId::new(call.call_id.clone()),
        ToolCallUpdateFields::new()
            .status(status(outcome))
            .content(output_content(outcome))
            .raw_output(raw_output(outcome)),
    ))
}

/// Shows each saved subagent message as a finished tool call attributed to
/// its subagent, both live and in replay. They are not model tool calls, so
/// Ox generates their IDs.
pub fn subagent_message_updates(messages: &[SubagentMessage]) -> Vec<SessionUpdate> {
    messages
        .iter()
        .map(|message| {
            let status = match message.content {
                SubagentMessageContent::FinalAnswer(_) => ToolCallStatus::Completed,
                SubagentMessageContent::Failure(_) => ToolCallStatus::Failed,
            };
            SessionUpdate::ToolCall(
                AcpToolCall::new(
                    ToolCallId::new(format!("subagent-message-{}", uuid::Uuid::new_v4())),
                    message.label(),
                )
                .kind(ToolKind::Other)
                .status(status)
                .content(vec![text_content(message.text())])
                .raw_output(Value::String(message.text().to_owned())),
            )
        })
        .collect()
}

/// Shows a saved turn error in replay as a failed tool call, so it stays apart
/// from model text. A live turn reports its error in the prompt's response
/// instead.
pub fn turn_error_update(text: &str) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(
            ToolCallId::new(format!("turn-error-{}", uuid::Uuid::new_v4())),
            "Turn error",
        )
        .kind(ToolKind::Other)
        .status(ToolCallStatus::Failed)
        .content(vec![text_content(text)])
        .raw_output(Value::String(text.to_owned())),
    )
}

/// Sends the saved transcript as displayable content and final tool states.
/// The model and continuation metadata are never shown.
pub fn replay_transcript(
    transcript: &[TranscriptEntry],
    mut send_update: impl FnMut(SessionUpdate) -> Result<()>,
) -> Result<()> {
    for entry in transcript {
        match entry {
            TranscriptEntry::TurnStart(turn_start) => match &turn_start.input {
                TurnInput::UserMessage(message) => {
                    for update in user_message_updates(message) {
                        send_update(update)?;
                    }
                }
                TurnInput::SkillInvocation(invocation) => {
                    send_update(user_message_chunk(&invocation.command_text()))?;
                    for image in &invocation.images {
                        send_update(user_image_chunk(image))?;
                    }
                }
            },
            TranscriptEntry::SubagentMessages(messages) => {
                for update in subagent_message_updates(messages) {
                    send_update(update)?;
                }
            }
            TranscriptEntry::TurnError(text) => send_update(turn_error_update(text))?,
            TranscriptEntry::AssistantBatch(batch) => {
                let message = &batch.message;
                if !message.reasoning.is_empty() {
                    send_update(agent_thought_chunk(&message.reasoning))?;
                }
                if !message.text.is_empty() {
                    send_update(agent_message_chunk(&message.text))?;
                }
                for (call, outcome) in message.tool_calls.iter().zip(&batch.outcomes) {
                    send_update(replayed_tool_call(call, outcome))?;
                }
            }
        }
    }
    Ok(())
}

fn replayed_tool_call(call: &ToolCall, outcome: &ToolOutcome) -> SessionUpdate {
    SessionUpdate::ToolCall(
        AcpToolCall::new(
            ToolCallId::new(call.call_id.clone()),
            tools::tool_call_title(call),
        )
        .name(call.name.clone())
        .kind(tool_kind(call))
        .status(status(outcome))
        .raw_input(raw_input(call))
        .content(output_content(outcome))
        .raw_output(raw_output(outcome)),
    )
}

fn raw_input(call: &ToolCall) -> Value {
    serde_json::from_str(&call.arguments).unwrap_or_else(|_| Value::String(call.arguments.clone()))
}

fn raw_output(outcome: &ToolOutcome) -> Value {
    Value::String(outcome.text.clone())
}

/// The outcome's content blocks, or its text when it has none.
fn output_content(outcome: &ToolOutcome) -> Vec<ToolCallContent> {
    if outcome.content.is_empty() {
        return vec![text_content(&outcome.text)];
    }
    outcome
        .content
        .iter()
        .map(|content| match content {
            ToolContent::Text(text) => text_content(text),
            ToolContent::Diff {
                path,
                old_text,
                new_text,
            } => ToolCallContent::from(Diff::new(path, new_text).old_text(old_text.clone())),
        })
        .collect()
}

fn text_content(text: &str) -> ToolCallContent {
    ToolCallContent::from(ContentBlock::Text(TextContent::new(text)))
}

fn status(outcome: &ToolOutcome) -> ToolCallStatus {
    match outcome.status {
        ToolStatus::Completed => ToolCallStatus::Completed,
        ToolStatus::Failed | ToolStatus::Cancelled => ToolCallStatus::Failed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sessions::{AssistantBatch, AssistantMessage};

    #[test]
    fn unpriced_usage_reports_tokens_without_a_dollar_cost() {
        let parameters = ModelRequestParameters::new(
            crate::openai::fixture::DEFAULT_MODEL,
            crate::sessions::EffortLevel::Low,
            "Ox".to_owned(),
            crate::tools::Role::Main,
        )
        .unwrap();
        let message = AssistantMessage {
            text: "Done".to_owned(),
            reasoning: String::new(),
            tool_calls: vec![],
            continuation_metadata: vec![],
            usage: Some(crate::sessions::ModelUsage {
                input_tokens: 100,
                cached_tokens: 20,
                output_tokens: 30,
                reasoning_tokens: 10,
                cost: None,
            }),
        };
        let transcript = [TranscriptEntry::AssistantBatch(
            AssistantBatch::new(message, vec![]).unwrap(),
        )];
        let Some(SessionUpdate::UsageUpdate(usage)) = usage_update(&transcript, &parameters, None)
        else {
            panic!("no usage update");
        };
        assert_eq!(usage.used, 130);
        assert_eq!(usage.size, 272000);
        assert!(usage.cost.is_none());
    }
    use agent_client_protocol::schema::v1::{AudioContent, ErrorCode, ImageContent, ResourceLink};

    #[test]
    fn prompt_message_keeps_links_and_images_and_rejects_invalid_content() {
        let message = prompt_message(&[
            ContentBlock::Text(TextContent::new("Review this")),
            ContentBlock::ResourceLink(ResourceLink::new(
                "src/main.rs",
                "file:///workspace/src/main.rs",
            )),
            ContentBlock::Image(ImageContent::new("aGVsbG8=", "image/png")),
        ])
        .unwrap();
        assert_eq!(
            message.text(),
            "Review this\nResource link: src/main.rs\nURI: file:///workspace/src/main.rs"
        );
        assert!(
            matches!(&message.parts[2], UserMessagePart::Image(image) if image.data == "aGVsbG8=")
        );
        assert!(
            prompt_message(&[ContentBlock::Image(ImageContent::new(
                "aGVsbG8=",
                "image/png"
            ))])
            .is_ok()
        );
        let store = sessions::SessionStore::in_memory();
        let id = store.create(std::path::Path::new("/workspace")).unwrap().id;
        store
            .append_turn_start(&id, &sessions::TurnStart::test(message.clone()))
            .unwrap();
        let stored = store.read(&id).unwrap().unwrap();
        assert_eq!(stored.transcript[0], TranscriptEntry::turn(message));
        let mut updates = Vec::new();
        replay_transcript(&stored.transcript, |update| {
            updates.push(update);
            Ok(())
        })
        .unwrap();
        assert!(matches!(&updates[2], SessionUpdate::UserMessageChunk(chunk)
            if matches!(&chunk.content, ContentBlock::Image(image) if image.data == "aGVsbG8=" && image.mime_type == "image/png")));

        for image in [
            ImageContent::new("", "image/png"),
            ImageContent::new("bad", "image/png"),
            ImageContent::new("aGVsbG8=", "image/svg+xml"),
        ] {
            assert_eq!(
                prompt_message(&[ContentBlock::Image(image)])
                    .unwrap_err()
                    .code,
                ErrorCode::InvalidParams
            );
        }
        for mime in ["image/png", "image/jpeg", "image/webp", "image/gif"] {
            assert!(
                prompt_message(&[ContentBlock::Image(ImageContent::new("aGVsbG8=", mime))]).is_ok()
            );
        }
        let five = (0..5)
            .map(|_| ContentBlock::Image(ImageContent::new("aGVsbG8=", "image/png")))
            .collect::<Vec<_>>();
        assert_eq!(
            prompt_message(&five).unwrap_err().code,
            ErrorCode::InvalidParams
        );
        let too_large = ContentBlock::Image(ImageContent::new(
            "A".repeat(MAX_IMAGE_BYTES * 4 / 3 + 8),
            "image/png",
        ));
        assert_eq!(
            prompt_message(&[too_large]).unwrap_err().code,
            ErrorCode::InvalidParams
        );

        let blank = prompt_message(&[ContentBlock::Text(TextContent::new("  \n\t"))]).unwrap_err();
        assert_eq!(blank.code, ErrorCode::InvalidParams);
        assert_eq!(
            prompt_message(&[]).unwrap_err().code,
            ErrorCode::InvalidParams
        );
        assert_eq!(
            prompt_message(&[ContentBlock::Audio(AudioContent::new(
                "aGVsbG8=",
                "audio/wav"
            ))])
            .unwrap_err()
            .code,
            ErrorCode::InvalidParams
        );
    }

    #[test]
    fn permission_content_shows_everything_being_approved() {
        let command = "printf '%s\\n' '```'\n  echo \"$HOME\"\n";
        let arguments = serde_json::json!({"command": command, "timeout_seconds": 5});
        let input = |command: Option<&str>, text: &str, close_stdin| tools::Permission::Input {
            process_id: "p-1".to_owned(),
            command: command.map(str::to_owned),
            text: text.to_owned(),
            close_stdin,
        };
        for (name, input_arguments, permission, expected) in [
            (
                tools::SHELL,
                arguments.to_string(),
                tools::Permission::Command,
                "Working directory: /workspace\n\nCommand:\n\n    printf '%s\\n' '```'\n      echo \"$HOME\"\n".to_owned(),
            ),
            (
                tools::SHELL,
                serde_json::json!({"command": "echo hello"}).to_string(),
                tools::Permission::Command,
                "Working directory: /workspace\n\nCommand:\n\n    echo hello".to_owned(),
            ),
            (
                tools::SHELL,
                "{bad json".to_owned(),
                tools::Permission::Command,
                "Working directory: /workspace\n\nArguments:\n\n    {bad json".to_owned(),
            ),
            (
                tools::SHELL,
                serde_json::json!({"command": "npm run dev", "background": true}).to_string(),
                tools::Permission::Command,
                "Working directory: /workspace\n\nCommand:\n\n    npm run dev".to_owned(),
            ),
            (
                tools::SHELL_PROCESS,
                serde_json::json!({"action": "write", "process_id": "p-1", "text": "y\nquit\n"}).to_string(),
                input(Some("python3 -i\nexit"), "y\nquit\n", false),
                "Shell process: p-1\n\nCommand:\n\n    python3 -i\n    exit\n\nInput:\n\n    y\n    quit\n\n\nCloses stdin afterward: no".to_owned(),
            ),
            (
                tools::SHELL_PROCESS,
                serde_json::json!({"action": "write", "process_id": "p-1", "text": "", "close_stdin": true}).to_string(),
                input(None, "", true),
                "Shell process: p-1\n\nCommand:\n\n    (no shell process with this ID that this agent started)\n\nInput:\n\n    (none)\n\nCloses stdin afterward: yes".to_owned(),
            ),
        ] {
            let call = ToolCall {
                call_id: "shell-1".to_owned(),
                name: name.to_owned(),
                arguments: input_arguments,
            };
            let request = shell_permission_request(
                &AcpIdentity {
                    session_id: agent_client_protocol::schema::v1::SessionId::new("session-1"),
                    subagent_id: None,
                },
                &call,
                std::path::Path::new("/workspace"),
                &permission,
            );
            let request = serde_json::to_value(request).unwrap();
            assert_eq!(
                request["toolCall"]["content"][0]["content"]["text"],
                expected,
                "{}",
                call.arguments
            );
            assert_eq!(request["toolCall"]["name"], name);
            assert_eq!(request["toolCall"]["rawInput"], raw_input(&call));
            assert_eq!(request["toolCall"]["kind"], "execute");
            assert!(request["toolCall"].get("_meta").is_none());
        }
    }

    #[test]
    fn a_subagent_permission_request_goes_to_the_main_session_with_a_scoped_tool_call() {
        let call = ToolCall {
            call_id: "shell-1".to_owned(),
            name: tools::SHELL.to_owned(),
            arguments: serde_json::json!({"command": "touch first"}).to_string(),
        };
        let request = shell_permission_request(
            &AcpIdentity {
                session_id: agent_client_protocol::schema::v1::SessionId::new("main"),
                subagent_id: Some(agent_client_protocol::schema::v1::SessionId::new("child")),
            },
            &call,
            std::path::Path::new("/workspace"),
            &tools::Permission::Command,
        );
        let request = serde_json::to_value(request).unwrap();
        assert_eq!(request["sessionId"], "main");
        assert_eq!(request["toolCall"]["toolCallId"], "child:shell-1");
        assert_eq!(request["toolCall"]["title"], "Subagent child: touch first");
        assert_eq!(
            request["toolCall"]["content"][0]["content"]["text"],
            "Subagent: child\n\nWorking directory: /workspace\n\nCommand:\n\n    touch first"
        );
        assert_eq!(request["toolCall"]["rawInput"]["command"], "touch first");
        assert_eq!(request["toolCall"]["_meta"]["subagent_id"], "child");
    }

    #[test]
    fn turn_errors_are_replayed_as_failed_tool_calls() {
        let text = "the model request failed: OpenAI returned 503";
        let mut replay = Vec::new();
        replay_transcript(&[TranscriptEntry::TurnError(text.to_owned())], |update| {
            replay.push(update);
            Ok(())
        })
        .unwrap();
        let [SessionUpdate::ToolCall(call)] = replay.as_slice() else {
            panic!("unexpected replay {replay:?}");
        };
        assert!(call.tool_call_id.to_string().starts_with("turn-error-"));
        assert_eq!(call.title, "Turn error");
        assert_eq!(call.status, ToolCallStatus::Failed);
        assert_eq!(call.content, vec![text_content(text)]);
    }

    #[test]
    fn subagent_messages_are_shown_as_attributed_tool_calls_live_and_in_replay() {
        let messages = vec![
            SubagentMessage {
                subagent_id: "child".to_owned(),
                content: SubagentMessageContent::FinalAnswer("Fixed.".to_owned()),
            },
            SubagentMessage {
                subagent_id: "child".to_owned(),
                content: SubagentMessageContent::Failure("The model refused.".to_owned()),
            },
        ];
        let mut replay = Vec::new();
        replay_transcript(
            &[TranscriptEntry::SubagentMessages(messages.clone())],
            |update| {
                replay.push(update);
                Ok(())
            },
        )
        .unwrap();
        for updates in [subagent_message_updates(&messages), replay] {
            let shown: Vec<_> = updates
                .iter()
                .map(|update| match update {
                    SessionUpdate::ToolCall(call) => {
                        assert!(
                            call.tool_call_id
                                .to_string()
                                .starts_with("subagent-message-")
                        );
                        (call.title.clone(), call.status, call.raw_output.clone())
                    }
                    other => panic!("unexpected update {other:?}"),
                })
                .collect();
            assert_eq!(
                shown,
                [
                    (
                        "Final answer from subagent child".to_owned(),
                        ToolCallStatus::Completed,
                        Some(Value::String("Fixed.".to_owned()))
                    ),
                    (
                        "Failure of subagent child".to_owned(),
                        ToolCallStatus::Failed,
                        Some(Value::String("The model refused.".to_owned()))
                    ),
                ]
            );
        }
    }

    #[test]
    fn tool_updates_carry_tool_call_titles_names_failed_statuses_and_unparsable_arguments() {
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
                    && update.name.as_deref() == Some(tools::SHELL)
                    && update.raw_input == Some(Value::String("{\"comm".to_owned()))
        ));
        let cancelled = ToolOutcome::cancelled("Cancelled before this tool was started.");
        let SessionUpdate::ToolCall(update) = replayed_tool_call(&call, &cancelled) else {
            panic!("a replayed call is a tool call");
        };
        assert_eq!(update.status, ToolCallStatus::Failed);
        assert_eq!(update.title, "Run shell command");
        assert_eq!(update.name.as_deref(), Some(tools::SHELL));
        assert_eq!(update.raw_input, Some(Value::String("{\"comm".to_owned())));
        assert_eq!(
            update.content,
            vec![text_content("Cancelled before this tool was started.")],
            "empty content sends the text"
        );
        let SessionUpdate::ToolCallUpdate(update) = finished_tool_call_update(&call, &cancelled)
        else {
            panic!("a finished call is a tool call update");
        };
        assert_eq!(update.fields.status, Some(ToolCallStatus::Failed));
        assert_eq!(
            update.fields.raw_output,
            Some(Value::String(
                "Cancelled before this tool was started.".to_owned()
            ))
        );
        assert_eq!(
            update.fields.content,
            Some(vec![text_content(
                "Cancelled before this tool was started."
            )])
        );
    }

    #[test]
    fn tool_call_content_becomes_acp_blocks_and_raw_output_stays_the_text() {
        let call = ToolCall {
            call_id: "write-1".to_owned(),
            name: tools::WRITE_FILE.to_owned(),
            arguments: "{}".to_owned(),
        };
        let edit = ToolCall {
            name: tools::EDIT_FILE.to_owned(),
            ..call.clone()
        };
        assert_eq!(tool_kind(&edit), ToolKind::Edit);
        let outcome = ToolOutcome::completed("Added a").with_content(vec![
            ToolContent::Text("Added a".to_owned()),
            ToolContent::Diff {
                path: "/workspace/a".into(),
                old_text: None,
                new_text: "one\n".to_owned(),
            },
            ToolContent::Diff {
                path: "/workspace/b".into(),
                old_text: Some("old\n".to_owned()),
                new_text: "new\n".to_owned(),
            },
        ]);
        let expected = vec![
            text_content("Added a"),
            ToolCallContent::from(Diff::new("/workspace/a", "one\n")),
            ToolCallContent::from(Diff::new("/workspace/b", "new\n").old_text("old\n".to_owned())),
        ];
        let SessionUpdate::ToolCall(replayed) = replayed_tool_call(&call, &outcome) else {
            panic!("a replayed call is a tool call");
        };
        assert_eq!(replayed.kind, ToolKind::Edit);
        assert_eq!(replayed.content, expected);
        assert_eq!(
            replayed.raw_output,
            Some(Value::String("Added a".to_owned()))
        );
        let SessionUpdate::ToolCallUpdate(finished) = finished_tool_call_update(&call, &outcome)
        else {
            panic!("a finished call is a tool call update");
        };
        assert_eq!(finished.fields.content, Some(expected));
        assert_eq!(
            finished.fields.raw_output,
            Some(Value::String("Added a".to_owned()))
        );
    }
}
