//! A session's conversation: its events, folded in order into the items the
//! UI renders.

use std::collections::HashMap;

use ox_protocol::schema::{
    ContentBlock, EmbeddedResourceResource, PermissionOption, RequestPermissionOutcome,
    SessionUpdate, StopReason, ToolCall, ToolCallStatus, ToolKind,
};
use ox_protocol::{Event, EventKind};
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
    /// Agent message text to append to block `key`.
    Text {
        key: String,
        text: String,
    },
    /// Agent thought text to append to block `key`.
    Thought {
        key: String,
        text: String,
    },
    /// A tool call's current state.
    ToolCall {
        key: String,
        title: String,
        kind: ToolKind,
        status: ToolCallStatus,
    },
    /// A permission request. `outcome` is set once it is resolved: the name
    /// of the chosen option, or `Cancelled`.
    Permission {
        key: String,
        request_id: String,
        title: String,
        options: Vec<PermissionOption>,
        outcome: Option<String>,
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

/// The block that the next chunk of its kind extends.
enum Block {
    Text(String),
    Thought(String),
    /// A user message replayed outside a turn, with what it holds so far.
    User {
        key: String,
        text: String,
        attachments: Vec<String>,
    },
}

/// A permission request awaiting its outcome.
struct Request {
    title: String,
    options: Vec<PermissionOption>,
}

/// The fold of a session's events, from its first.
#[derive(Default)]
pub struct Conversation {
    /// Whether a turn that `prompt_started` opened is running.
    in_turn: bool,
    block: Option<Block>,
    tool_calls: HashMap<String, ToolCall>,
    requests: HashMap<String, Request>,
}

impl Conversation {
    /// The item that the next event creates or updates, if any.
    pub fn apply(&mut self, event: &Event) -> Option<Item> {
        let key = event.seq.to_string();
        match &event.kind {
            EventKind::AgentStarted { .. } => None,
            EventKind::PromptStarted { prompt } => {
                self.in_turn = true;
                let mut text = Vec::new();
                let mut attachments = Vec::new();
                for block in prompt {
                    add_content(block, &mut text, &mut attachments);
                }
                Some(self.create(Item::Prompt {
                    key,
                    text: text.join("\n\n"),
                    attachments,
                }))
            }
            EventKind::Update(update) => self.update(key, update),
            EventKind::PermissionRequested {
                request_id,
                tool_call,
                options,
            } => {
                let request = Request {
                    title: tool_call
                        .fields
                        .title
                        .clone()
                        .unwrap_or_else(|| "Tool call".into()),
                    options: options.clone(),
                };
                let item = permission(request_id, &request, None);
                self.requests.insert(request_id.clone(), request);
                Some(self.create(item))
            }
            EventKind::PermissionResolved {
                request_id,
                outcome,
            } => {
                let request = self.requests.remove(request_id)?;
                let outcome = match outcome {
                    RequestPermissionOutcome::Selected(selected) => request
                        .options
                        .iter()
                        .find(|option| option.option_id == selected.option_id)
                        .map(|option| option.name.clone())
                        .unwrap_or_else(|| selected.option_id.to_string()),
                    _ => "Cancelled".into(),
                };
                Some(permission(request_id, &request, Some(outcome)))
            }
            EventKind::TurnEnded { stop_reason, error } => {
                self.in_turn = false;
                let message = match (error, stop_reason) {
                    (Some(error), _) => error.clone(),
                    (None, Some(StopReason::MaxTokens)) => {
                        "The agent reached its token limit".into()
                    }
                    (None, Some(StopReason::MaxTurnRequests)) => {
                        "The agent reached its limit of requests in a turn".into()
                    }
                    (None, Some(StopReason::Refusal)) => "The agent refused".into(),
                    (None, Some(StopReason::EndTurn | StopReason::Cancelled) | None) => {
                        return None;
                    }
                    (None, Some(_)) => "The agent stopped".into(),
                };
                Some(self.create(Item::TurnError { key, message }))
            }
            EventKind::AgentExited { error } => {
                self.in_turn = false;
                Some(self.create(Item::AgentExited {
                    key,
                    error: error.clone(),
                }))
            }
            EventKind::SessionClosed => Some(self.create(Item::SessionClosed { key })),
        }
    }

    fn update(&mut self, key: String, update: &SessionUpdate) -> Option<Item> {
        match update {
            // The prompt that opened the turn is its item already.
            SessionUpdate::UserMessageChunk(_) if self.in_turn => None,
            SessionUpdate::UserMessageChunk(chunk) => {
                if !matches!(self.block, Some(Block::User { .. })) {
                    self.block = Some(Block::User {
                        key,
                        text: String::new(),
                        attachments: Vec::new(),
                    });
                }
                let Some(Block::User {
                    key,
                    text,
                    attachments,
                }) = &mut self.block
                else {
                    unreachable!("the block was just set");
                };
                let mut texts = Vec::new();
                add_content(&chunk.content, &mut texts, attachments);
                text.extend(texts);
                Some(Item::Prompt {
                    key: key.clone(),
                    text: text.clone(),
                    attachments: attachments.clone(),
                })
            }
            SessionUpdate::AgentMessageChunk(chunk) => {
                let ContentBlock::Text(text) = &chunk.content else {
                    return None;
                };
                let key = match &self.block {
                    Some(Block::Text(key)) => key.clone(),
                    _ => {
                        self.block = Some(Block::Text(key.clone()));
                        key
                    }
                };
                Some(Item::Text {
                    key,
                    text: text.text.clone(),
                })
            }
            SessionUpdate::AgentThoughtChunk(chunk) => {
                let ContentBlock::Text(text) = &chunk.content else {
                    return None;
                };
                let key = match &self.block {
                    Some(Block::Thought(key)) => key.clone(),
                    _ => {
                        self.block = Some(Block::Thought(key.clone()));
                        key
                    }
                };
                Some(Item::Thought {
                    key,
                    text: text.text.clone(),
                })
            }
            SessionUpdate::ToolCall(call) => {
                let item = tool_call(call);
                self.tool_calls
                    .insert(call.tool_call_id.to_string(), call.clone());
                Some(self.create(item))
            }
            SessionUpdate::ToolCallUpdate(update) => {
                let id = update.tool_call_id.to_string();
                if let Some(call) = self.tool_calls.get_mut(&id) {
                    call.update(update.fields.clone());
                    return Some(tool_call(call));
                }
                // An update may report a tool call the agent never announced.
                let call = ToolCall::try_from(update.clone()).ok()?;
                let item = tool_call(&call);
                self.tool_calls.insert(id, call);
                Some(self.create(item))
            }
            SessionUpdate::Plan(_)
            | SessionUpdate::AvailableCommandsUpdate(_)
            | SessionUpdate::CurrentModeUpdate(_)
            | SessionUpdate::ConfigOptionUpdate(_)
            | SessionUpdate::SessionInfoUpdate(_)
            | SessionUpdate::UsageUpdate(_) => None,
            _ => None,
        }
    }

    /// Returns a new item, which ends the open block.
    fn create(&mut self, item: Item) -> Item {
        self.block = None;
        item
    }
}

fn tool_call(call: &ToolCall) -> Item {
    Item::ToolCall {
        key: format!("tool:{}", call.tool_call_id),
        title: call.title.clone(),
        kind: call.kind,
        status: call.status,
    }
}

fn permission(request_id: &str, request: &Request, outcome: Option<String>) -> Item {
    Item::Permission {
        key: format!("permission:{request_id}"),
        request_id: request_id.into(),
        title: request.title.clone(),
        options: request.options.clone(),
        outcome,
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
        ContentChunk, PermissionOptionKind, ResourceLink, SelectedPermissionOutcome, TextContent,
        ToolCallUpdate, ToolCallUpdateFields,
    };

    use super::*;

    /// Folds `kinds` as a session's events and returns every item.
    fn fold(kinds: Vec<EventKind>) -> Vec<Item> {
        let mut conversation = Conversation::default();
        kinds
            .into_iter()
            .zip(1..)
            .filter_map(|(kind, seq)| {
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

    fn agent(chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::AgentMessageChunk(ContentChunk::new(text(
            chunk,
        ))))
    }

    fn thought(chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::AgentThoughtChunk(ContentChunk::new(text(
            chunk,
        ))))
    }

    fn user(chunk: &str) -> EventKind {
        EventKind::Update(SessionUpdate::UserMessageChunk(ContentChunk::new(text(
            chunk,
        ))))
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

    fn item_text(key: &str, text: &str) -> Item {
        Item::Text {
            key: key.into(),
            text: text.into(),
        }
    }

    fn item_thought(key: &str, text: &str) -> Item {
        Item::Thought {
            key: key.into(),
            text: text.into(),
        }
    }

    #[test]
    fn agent_chunks_share_a_block_until_another_item() {
        let items = fold(vec![
            prompt("hi"),
            agent("a"),
            agent("b"),
            thought("c"),
            thought("d"),
            agent("e"),
            turn_ended(),
            prompt("again"),
            agent("f"),
        ]);
        assert_eq!(
            items[1..7],
            [
                item_text("2", "a"),
                item_text("2", "b"),
                item_thought("4", "c"),
                item_thought("4", "d"),
                item_text("6", "e"),
                Item::Prompt {
                    key: "8".into(),
                    text: "again".into(),
                    attachments: vec!["a.rs".into()],
                },
            ]
        );
        assert_eq!(items[7], item_text("9", "f"));
    }

    #[test]
    fn tool_call_update_updates_its_tool_call() {
        let call = ToolCall::new("t", "Read a.rs").kind(ToolKind::Read);
        let update = |status| {
            EventKind::Update(SessionUpdate::ToolCallUpdate(ToolCallUpdate::new(
                "t",
                ToolCallUpdateFields::new().status(status),
            )))
        };
        let items = fold(vec![
            agent("a"),
            EventKind::Update(SessionUpdate::ToolCall(call)),
            update(ToolCallStatus::InProgress),
            update(ToolCallStatus::Completed),
            agent("b"),
        ]);
        let tool = |status| Item::ToolCall {
            key: "tool:t".into(),
            title: "Read a.rs".into(),
            kind: ToolKind::Read,
            status,
        };
        assert_eq!(
            items,
            [
                item_text("1", "a"),
                tool(ToolCallStatus::Pending),
                tool(ToolCallStatus::InProgress),
                tool(ToolCallStatus::Completed),
                item_text("5", "b"),
            ]
        );
    }

    #[test]
    fn user_chunks_are_prompts_only_outside_a_turn() {
        let items = fold(vec![
            user("replayed "),
            user("prompt"),
            agent("answer"),
            prompt("live"),
            user("live"),
            turn_ended(),
        ]);
        let replayed = |text: &str| Item::Prompt {
            key: "1".into(),
            text: text.into(),
            attachments: Vec::new(),
        };
        assert_eq!(
            items,
            [
                replayed("replayed "),
                replayed("replayed prompt"),
                item_text("3", "answer"),
                Item::Prompt {
                    key: "4".into(),
                    text: "live".into(),
                    attachments: vec!["a.rs".into()],
                },
            ]
        );
    }

    #[test]
    fn a_permission_opens_and_closes_by_request_id() {
        let requested = |request_id: &str| EventKind::PermissionRequested {
            request_id: request_id.into(),
            tool_call: ToolCallUpdate::new("t", ToolCallUpdateFields::new().title("Edit a.rs")),
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
                outcome: RequestPermissionOutcome::Cancelled,
            },
            EventKind::PermissionResolved {
                request_id: "1".into(),
                outcome: RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(
                    "allow",
                )),
            },
        ]);
        let outcomes: Vec<(String, Option<String>)> = items
            .into_iter()
            .map(|item| match item {
                Item::Permission { key, outcome, .. } => (key, outcome),
                other => panic!("{other:?}"),
            })
            .collect();
        assert_eq!(
            outcomes,
            [
                ("permission:1".into(), None),
                ("permission:2".into(), None),
                ("permission:2".into(), Some("Cancelled".into())),
                ("permission:1".into(), Some("Allow".into())),
            ]
        );
    }
}
