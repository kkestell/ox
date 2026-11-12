//! A session's conversation: its events, folded in order into the items the
//! UI renders.

use std::collections::HashMap;

use ox_protocol::schema::{
    ContentBlock, EmbeddedResourceResource, MessageId, PermissionOption, RequestPermissionSubject,
    SessionUpdate, StopReason, ToolCallContent, ToolCallId, ToolCallStatus, ToolCallUpdate,
};
use ox_protocol::{Event, EventKind, MaybeUndefined};
use serde::Serialize;

/// One element of a conversation. Every item has a `key`; the UI creates
/// the element for a key it has not seen and updates the one it has.
#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Item {
    /// A prompt: its text and the names of its attachments.
    Prompt {
        key: String,
        text: String,
        attachments: Vec<String>,
    },
    /// Agent message text for block `key`: appended to it, or replacing it
    /// when `replace` is set.
    Text {
        key: String,
        text: String,
        replace: bool,
    },
    /// Agent thought text for block `key`, as for `Text`.
    Thought {
        key: String,
        text: String,
        replace: bool,
    },
    /// A tool call's current state: its Markdown title, its input, and the
    /// text and images of its content.
    ToolCall {
        key: String,
        title: String,
        status: ToolCallStatus,
        raw_input: Option<serde_json::Value>,
        content: Vec<ToolContent>,
    },
    /// A permission request, with its Markdown title. `answered` is set once
    /// it is resolved.
    Permission {
        key: String,
        request_id: String,
        title: String,
        options: Vec<PermissionOption>,
        answered: bool,
    },
    /// A turn that ended with an error, or stopped short of its end.
    TurnError {
        key: String,
        message: String,
    },
    AgentExited {
        key: String,
        error: Option<String>,
    },
    SessionClosed {
        key: String,
    },
}

/// A text or image block of a tool call's content.
#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ToolContent {
    Text { text: String },
    Image { mime_type: String, data: String },
}

/// A permission request awaiting its outcome.
struct Request {
    title: String,
    options: Vec<PermissionOption>,
}

/// A tool call as its updates have built it.
#[derive(Default)]
struct Call {
    name: Option<String>,
    title: Option<String>,
    status: ToolCallStatus,
    raw_input: Option<serde_json::Value>,
    content: Vec<ToolCallContent>,
}

impl Call {
    /// Applies a patch: an omitted field is unchanged, `null` resets it, and
    /// a value replaces it.
    fn patch(&mut self, update: &ToolCallUpdate) {
        update.name.clone().update_to(&mut self.name);
        update.title.clone().update_to(&mut self.title);
        update.raw_input.clone().update_to(&mut self.raw_input);
        match &update.status {
            MaybeUndefined::Undefined => {}
            MaybeUndefined::Null => self.status = ToolCallStatus::default(),
            MaybeUndefined::Value(status) => self.status = status.clone(),
        }
        match &update.content {
            MaybeUndefined::Undefined => {}
            MaybeUndefined::Null => self.content.clear(),
            MaybeUndefined::Value(content) => self.content.clone_from(content),
        }
    }

    fn item(&self, id: &ToolCallId) -> Item {
        Item::ToolCall {
            key: format!("tool:{id}"),
            title: self
                .title
                .clone()
                .or_else(|| self.name.clone())
                .unwrap_or_else(|| "Tool call".into()),
            status: self.status.clone(),
            raw_input: self.raw_input.clone(),
            content: self
                .content
                .iter()
                .filter_map(|content| match content {
                    ToolCallContent::Content(content) => match &content.content {
                        ContentBlock::Text(text) => Some(ToolContent::Text {
                            text: text.text.clone(),
                        }),
                        ContentBlock::Image(image) => Some(ToolContent::Image {
                            mime_type: image.mime_type.to_string(),
                            data: image.data.clone(),
                        }),
                        _ => None,
                    },
                    _ => None,
                })
                .collect(),
        }
    }
}

/// A user message replayed outside a turn, with what it holds so far.
#[derive(Default)]
struct UserMessage {
    text: String,
    attachments: Vec<String>,
}

/// The fold of a session's events, from its first.
#[derive(Default)]
pub struct Conversation {
    /// Whether a turn that `prompt_started` opened is running.
    in_turn: bool,
    tool_calls: HashMap<ToolCallId, Call>,
    user_messages: HashMap<String, UserMessage>,
    requests: HashMap<String, Request>,
}

impl Conversation {
    /// The items that the next event creates or updates, in order.
    pub fn apply(&mut self, event: &Event) -> Vec<Item> {
        let key = event.seq.to_string();
        match &event.kind {
            EventKind::AgentStarted { .. } | EventKind::TurnStarted | EventKind::AgentParked => {
                Vec::new()
            }
            EventKind::PromptStarted { prompt } => {
                self.in_turn = true;
                let mut text = Vec::new();
                let mut attachments = Vec::new();
                for block in prompt {
                    add_content(block, &mut text, &mut attachments);
                }
                vec![Item::Prompt {
                    key,
                    text: text.join("\n\n"),
                    attachments,
                }]
            }
            EventKind::Update(update) => self.update(update),
            EventKind::PermissionRequested {
                request_id,
                title,
                subject,
                options,
                ..
            } => {
                // A tool call subject carries the tool call's details.
                let mut items = match subject {
                    Some(RequestPermissionSubject::ToolCall(subject)) => {
                        self.patch_tool_call(&subject.tool_call)
                    }
                    _ => Vec::new(),
                };
                let request = Request {
                    title: title.clone(),
                    options: options.clone(),
                };
                items.push(permission(request_id, &request, false));
                self.requests.insert(request_id.clone(), request);
                items
            }
            EventKind::PermissionResolved { request_id, .. } => {
                let Some(request) = self.requests.remove(request_id) else {
                    return Vec::new();
                };
                vec![permission(request_id, &request, true)]
            }
            EventKind::TurnEnded { stop_reason, error } => {
                self.in_turn = false;
                let message = match (error, stop_reason) {
                    (Some(error), _) => error.clone(),
                    (None, Some(StopReason::Error(details))) => details
                        .error
                        .as_ref()
                        .map_or_else(|| "The agent failed".into(), |error| error.message.clone()),
                    (None, Some(StopReason::MaxTokens)) => {
                        "The agent reached its token limit".into()
                    }
                    (None, Some(StopReason::MaxTurnRequests)) => {
                        "The agent reached its limit of requests in a turn".into()
                    }
                    (None, Some(StopReason::Refusal)) => "The agent refused".into(),
                    (None, Some(StopReason::EndTurn | StopReason::Cancelled) | None) => {
                        return Vec::new();
                    }
                    (None, Some(_)) => "The agent stopped".into(),
                };
                vec![Item::TurnError { key, message }]
            }
            EventKind::AgentExited { error } => {
                self.in_turn = false;
                vec![Item::AgentExited {
                    key,
                    error: error.clone(),
                }]
            }
            EventKind::SessionClosed => vec![Item::SessionClosed { key }],
        }
    }

    fn update(&mut self, update: &SessionUpdate) -> Vec<Item> {
        match update {
            // The prompt that opened the turn is its item already.
            SessionUpdate::UserMessageChunk(_) | SessionUpdate::UserMessage(_) if self.in_turn => {
                Vec::new()
            }
            SessionUpdate::UserMessageChunk(chunk) => {
                let message = self
                    .user_messages
                    .entry(chunk.message_id.to_string())
                    .or_default();
                let mut texts = Vec::new();
                add_content(&chunk.content, &mut texts, &mut message.attachments);
                message.text.extend(texts);
                vec![user_item(&chunk.message_id, message)]
            }
            SessionUpdate::UserMessage(update) => {
                // Without content, the update creates an empty message or
                // leaves the existing one as it is.
                let Some(content) = content_of(&update.content) else {
                    let message = self
                        .user_messages
                        .entry(update.message_id.to_string())
                        .or_default();
                    return vec![user_item(&update.message_id, message)];
                };
                let mut texts = Vec::new();
                let mut message = UserMessage::default();
                for block in content {
                    add_content(block, &mut texts, &mut message.attachments);
                }
                message.text = texts.join("\n\n");
                let item = user_item(&update.message_id, &message);
                self.user_messages
                    .insert(update.message_id.to_string(), message);
                vec![item]
            }
            SessionUpdate::AgentMessageChunk(chunk) => match &chunk.content {
                ContentBlock::Text(text) => vec![Item::Text {
                    key: format!("text:{}", chunk.message_id),
                    text: text.text.clone(),
                    replace: false,
                }],
                _ => Vec::new(),
            },
            SessionUpdate::AgentThoughtChunk(chunk) => match &chunk.content {
                ContentBlock::Text(text) => vec![Item::Thought {
                    key: format!("thought:{}", chunk.message_id),
                    text: text.text.clone(),
                    replace: false,
                }],
                _ => Vec::new(),
            },
            // Without content, a whole message appends nothing: it creates an
            // empty message or leaves the existing one as it is.
            SessionUpdate::AgentMessage(message) => {
                let content = content_of(&message.content);
                vec![Item::Text {
                    key: format!("text:{}", message.message_id),
                    replace: content.is_some(),
                    text: content.map(texts).unwrap_or_default(),
                }]
            }
            SessionUpdate::AgentThought(thought) => {
                let content = content_of(&thought.content);
                vec![Item::Thought {
                    key: format!("thought:{}", thought.message_id),
                    replace: content.is_some(),
                    text: content.map(texts).unwrap_or_default(),
                }]
            }
            SessionUpdate::ToolCallUpdate(update) => self.patch_tool_call(update),
            SessionUpdate::ToolCallContentChunk(chunk) => {
                let call = self
                    .tool_calls
                    .entry(chunk.tool_call_id.clone())
                    .or_default();
                call.content.push(chunk.content.clone());
                vec![call.item(&chunk.tool_call_id)]
            }
            _ => Vec::new(),
        }
    }

    /// Creates the tool call for a new ID, or patches the existing one.
    fn patch_tool_call(&mut self, update: &ToolCallUpdate) -> Vec<Item> {
        let call = self
            .tool_calls
            .entry(update.tool_call_id.clone())
            .or_default();
        call.patch(update);
        vec![call.item(&update.tool_call_id)]
    }
}

/// The content of a message update, unless the update leaves it unchanged.
/// A `null` content is an empty message.
fn content_of(content: &MaybeUndefined<Vec<ContentBlock>>) -> Option<&[ContentBlock]> {
    match content {
        MaybeUndefined::Undefined => None,
        MaybeUndefined::Null => Some(&[]),
        MaybeUndefined::Value(content) => Some(content),
    }
}

/// The text blocks of a message, joined.
fn texts(content: &[ContentBlock]) -> String {
    content
        .iter()
        .filter_map(|block| match block {
            ContentBlock::Text(text) => Some(text.text.as_str()),
            _ => None,
        })
        .collect()
}

fn user_item(id: &MessageId, message: &UserMessage) -> Item {
    Item::Prompt {
        key: format!("user:{id}"),
        text: message.text.clone(),
        attachments: message.attachments.clone(),
    }
}

fn permission(request_id: &str, request: &Request, answered: bool) -> Item {
    Item::Permission {
        key: format!("permission:{request_id}"),
        request_id: request_id.into(),
        title: request.title.clone(),
        options: request.options.clone(),
        answered,
    }
}

/// Adds a content block's text to `text`, or its name to `attachments`.
fn add_content(block: &ContentBlock, text: &mut Vec<String>, attachments: &mut Vec<String>) {
    match block {
        ContentBlock::Text(content) => text.push(content.text.clone()),
        ContentBlock::ResourceLink(link) => attachments.push(link.name.clone()),
        ContentBlock::Image(_) => attachments.push("Image".into()),
        ContentBlock::Audio(_) => attachments.push("Audio".into()),
        ContentBlock::Resource(resource) => {
            let uri = match &resource.resource {
                EmbeddedResourceResource::TextResourceContents(text) => &text.uri,
                EmbeddedResourceResource::BlobResourceContents(blob) => &blob.uri,
                _ => return attachments.push("Resource".into()),
            };
            let name = uri.rsplit('/').next().unwrap_or(uri);
            attachments.push(name.into());
        }
        _ => attachments.push("Attachment".into()),
    }
}

#[cfg(test)]
mod tests {
    use ox_protocol::schema::{
        AgentMessage, CancelledPermissionOutcome, ContentChunk, ErrorStopReason, ImageContent,
        PermissionOptionKind, RequestPermissionOutcome, ResourceLink, SelectedPermissionOutcome,
        TextContent, ToolCallContentChunk, ToolCallPermissionSubject,
        UserMessage as UserMessageUpdate,
    };

    use serde_json::json;

    use super::*;

    /// Folds `kinds` as a session's events and returns every item.
    fn fold(kinds: Vec<EventKind>) -> Vec<Item> {
        let mut conversation = Conversation::default();
        kinds
            .into_iter()
            .zip(1..)
            .flat_map(|(kind, seq)| {
                conversation.apply(&Event {
                    session_id: "s".into(),
                    seq,
                    kind,
                })
            })
            .collect()
    }

    fn text(text: &str) -> ContentBlock {
        ContentBlock::Text(TextContent::new(text))
    }

    fn agent(id: &str, chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::AgentMessageChunk(ContentChunk::new(
            text(chunk),
            id,
        )))
    }

    fn thought(id: &str, chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::AgentThoughtChunk(ContentChunk::new(
            text(chunk),
            id,
        )))
    }

    fn user(id: &str, chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::UserMessageChunk(ContentChunk::new(
            text(chunk),
            id,
        )))
    }

    fn prompt(prompt: &str) -> EventKind {
        EventKind::PromptStarted {
            prompt: vec![
                text(prompt),
                ContentBlock::ResourceLink(ResourceLink::new("a.rs", "file:///a.rs")),
            ],
        }
    }

    fn turn_ended() -> EventKind {
        EventKind::TurnEnded {
            stop_reason: Some(StopReason::EndTurn),
            error: None,
        }
    }

    fn item_text(key: &str, text: &str, replace: bool) -> Item {
        Item::Text {
            key: key.into(),
            text: text.into(),
            replace,
        }
    }

    fn item_thought(key: &str, text: &str) -> Item {
        Item::Thought {
            key: key.into(),
            text: text.into(),
            replace: false,
        }
    }

    fn tool_update(update: ToolCallUpdate) -> EventKind {
        EventKind::Update(SessionUpdate::ToolCallUpdate(update))
    }

    fn tool(status: ToolCallStatus, content: Vec<ToolContent>) -> Item {
        Item::ToolCall {
            key: "tool:t".into(),
            title: "Read a.rs".into(),
            status,
            raw_input: Some(json!({"file_path": "a.rs"})),
            content,
        }
    }

    #[test]
    fn chunks_belong_to_their_message() {
        let items = fold(vec![
            prompt("hi"),
            agent("m1", "a"),
            agent("m1", "b"),
            thought("m2", "c"),
            thought("m2", "d"),
            agent("m3", "e"),
            turn_ended(),
        ]);
        assert_eq!(
            items[1..],
            [
                item_text("text:m1", "a", false),
                item_text("text:m1", "b", false),
                item_thought("thought:m2", "c"),
                item_thought("thought:m2", "d"),
                item_text("text:m3", "e", false),
            ]
        );
    }

    #[test]
    fn a_whole_message_replaces_the_message_with_its_id() {
        let whole = |id: &str, content: MaybeUndefined<Vec<ContentBlock>>| {
            let mut message = AgentMessage::new(id);
            message.content = content;
            EventKind::Update(SessionUpdate::AgentMessage(message))
        };
        let items = fold(vec![
            agent("m", "a"),
            whole("m", MaybeUndefined::Value(vec![text("one "), text("two")])),
            whole("m", MaybeUndefined::Undefined),
            whole("m", MaybeUndefined::Null),
        ]);
        assert_eq!(
            items,
            [
                item_text("text:m", "a", false),
                item_text("text:m", "one two", true),
                // Without content the message stays as it is.
                item_text("text:m", "", false),
                item_text("text:m", "", true),
            ]
        );
    }

    #[test]
    fn tool_call_update_creates_and_patches_a_tool_call() {
        let output = vec![
            ToolCallContent::from(text("fn main() {}")),
            ToolCallContent::from(ContentBlock::Image(ImageContent::new(
                "iVBORw0KGgo=",
                "image/png",
            ))),
        ];
        let mut created = ToolCallUpdate::new("t");
        created.title = MaybeUndefined::Value("Read a.rs".into());
        created.raw_input = MaybeUndefined::Value(json!({"file_path": "a.rs"}));
        let mut progress = ToolCallUpdate::new("t");
        progress.status = MaybeUndefined::Value(ToolCallStatus::InProgress);
        let mut done = ToolCallUpdate::new("t");
        done.status = MaybeUndefined::Value(ToolCallStatus::Completed);
        done.content = MaybeUndefined::Value(output);
        let items = fold(vec![
            tool_update(created),
            tool_update(progress),
            tool_update(done),
            agent("m", "b"),
        ]);
        assert_eq!(
            items,
            [
                tool(ToolCallStatus::Pending, Vec::new()),
                tool(ToolCallStatus::InProgress, Vec::new()),
                tool(
                    ToolCallStatus::Completed,
                    vec![
                        ToolContent::Text {
                            text: "fn main() {}".into()
                        },
                        ToolContent::Image {
                            mime_type: "image/png".into(),
                            data: "iVBORw0KGgo=".into()
                        },
                    ]
                ),
                item_text("text:m", "b", false),
            ]
        );
    }

    #[test]
    fn content_chunks_append_to_a_tool_calls_content() {
        let mut created = ToolCallUpdate::new("t");
        created.title = MaybeUndefined::Value("Read a.rs".into());
        created.raw_input = MaybeUndefined::Value(json!({"file_path": "a.rs"}));
        let chunk = |content: &str| {
            EventKind::Update(SessionUpdate::ToolCallContentChunk(
                ToolCallContentChunk::new("t", ToolCallContent::from(text(content))),
            ))
        };
        let items = fold(vec![tool_update(created), chunk("a"), chunk("b")]);
        let read = |content: &[&str]| {
            tool(
                ToolCallStatus::Pending,
                content
                    .iter()
                    .map(|text| ToolContent::Text {
                        text: (*text).into(),
                    })
                    .collect(),
            )
        };
        assert_eq!(items, [read(&[]), read(&["a"]), read(&["a", "b"])]);
    }

    #[test]
    fn user_messages_are_prompts_only_outside_a_turn() {
        let whole = |id: &str, content: &str| {
            let mut message = UserMessageUpdate::new(id);
            message.content = MaybeUndefined::Value(vec![text(content)]);
            EventKind::Update(SessionUpdate::UserMessage(message))
        };
        let items = fold(vec![
            user("u1", "replayed "),
            user("u1", "prompt"),
            whole("u2", "whole"),
            agent("m", "answer"),
            prompt("live"),
            user("u3", "live"),
            whole("u3", "live"),
            turn_ended(),
        ]);
        let replayed = |id: &str, text: &str| Item::Prompt {
            key: format!("user:{id}"),
            text: text.into(),
            attachments: Vec::new(),
        };
        assert_eq!(
            items,
            [
                replayed("u1", "replayed "),
                replayed("u1", "replayed prompt"),
                replayed("u2", "whole"),
                item_text("text:m", "answer", false),
                Item::Prompt {
                    key: "5".into(),
                    text: "live".into(),
                    attachments: vec!["a.rs".into()],
                },
            ]
        );
    }

    #[test]
    fn a_turn_ends_as_an_error_with_its_details() {
        let ended = |stop_reason, error| EventKind::TurnEnded { stop_reason, error };
        let error = |message: &str| {
            StopReason::Error(
                ErrorStopReason::new().error(ox_protocol::schema::Error::new(-32603, message)),
            )
        };
        let items = fold(vec![
            ended(Some(error("claude failed")), None),
            ended(Some(StopReason::Error(ErrorStopReason::new())), None),
            ended(None, Some("agent exited".into())),
            ended(Some(StopReason::Refusal), None),
            ended(Some(StopReason::Cancelled), None),
        ]);
        let messages: Vec<&str> = items
            .iter()
            .map(|item| match item {
                Item::TurnError { message, .. } => message.as_str(),
                other => panic!("{other:?}"),
            })
            .collect();
        assert_eq!(
            messages,
            [
                "claude failed",
                "The agent failed",
                "agent exited",
                "The agent refused"
            ]
        );
    }

    #[test]
    fn a_permission_opens_and_closes_by_request_id() {
        let requested = |request_id: &str| EventKind::PermissionRequested {
            request_id: request_id.into(),
            title: "Edit a.rs".into(),
            description: None,
            subject: None,
            options: vec![
                PermissionOption::new("allow", "Allow", PermissionOptionKind::AllowOnce),
                PermissionOption::new("reject", "Reject", PermissionOptionKind::RejectOnce),
            ],
        };
        let items = fold(vec![
            requested("1"),
            requested("2"),
            EventKind::PermissionResolved {
                request_id: "2".into(),
                outcome: RequestPermissionOutcome::Cancelled(CancelledPermissionOutcome::new()),
            },
            EventKind::PermissionResolved {
                request_id: "1".into(),
                outcome: RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(
                    "allow",
                )),
            },
        ]);
        let answers: Vec<(String, bool)> = items
            .into_iter()
            .map(|item| match item {
                Item::Permission { key, answered, .. } => (key, answered),
                other => panic!("{other:?}"),
            })
            .collect();
        assert_eq!(
            answers,
            [
                ("permission:1".into(), false),
                ("permission:2".into(), false),
                ("permission:2".into(), true),
                ("permission:1".into(), true),
            ]
        );
    }

    #[test]
    fn a_tool_call_subject_patches_its_tool_call() {
        let mut details = ToolCallUpdate::new("t");
        details.title = MaybeUndefined::Value("Read a.rs".into());
        details.raw_input = MaybeUndefined::Value(json!({"file_path": "a.rs"}));
        let items = fold(vec![EventKind::PermissionRequested {
            request_id: "1".into(),
            title: "Allow Read?".into(),
            description: None,
            subject: Some(RequestPermissionSubject::ToolCall(Box::new(
                ToolCallPermissionSubject::new(details),
            ))),
            options: Vec::new(),
        }]);
        assert!(matches!(
            &items[..],
            [first, Item::Permission { title, .. }]
                if *first == tool(ToolCallStatus::Pending, Vec::new()) && title == "Allow Read?"
        ));
    }

    #[test]
    fn a_contentless_message_keeps_its_place() {
        let items = fold(vec![
            EventKind::Update(SessionUpdate::AgentMessage(AgentMessage::new("first"))),
            agent("second", "Second message"),
            agent("first", "First message"),
        ]);
        // The UI stores items by key in first-appearance order.
        let mut keys = Vec::new();
        for item in items {
            let Item::Text { key, .. } = item else {
                panic!("expected an agent message");
            };
            if !keys.contains(&key) {
                keys.push(key);
            }
        }
        assert_eq!(keys, ["text:first", "text:second"]);
    }

    #[test]
    fn a_contentless_thought_keeps_its_place() {
        let items = fold(vec![
            EventKind::Update(SessionUpdate::AgentThought(
                ox_protocol::schema::AgentThought::new("first"),
            )),
            thought("second", "Second thought"),
            thought("first", "First thought"),
        ]);
        let mut keys = Vec::new();
        for item in items {
            let Item::Thought { key, .. } = item else {
                panic!("expected an agent thought");
            };
            if !keys.contains(&key) {
                keys.push(key);
            }
        }
        assert_eq!(keys, ["thought:first", "thought:second"]);
    }

    #[test]
    fn a_contentless_user_keeps_its_place() {
        let items = fold(vec![
            EventKind::Update(SessionUpdate::UserMessage(UserMessageUpdate::new("first"))),
            user("second", "Second prompt"),
            user("first", "First prompt"),
        ]);
        let mut keys = Vec::new();
        for item in items {
            let Item::Prompt { key, .. } = item else {
                panic!("expected a user message");
            };
            if !keys.contains(&key) {
                keys.push(key);
            }
        }
        assert_eq!(keys, ["user:first", "user:second"]);
    }
}
