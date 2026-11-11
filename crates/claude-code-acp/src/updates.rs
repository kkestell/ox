use std::{
    collections::{HashMap, HashSet},
    path::{Path, PathBuf},
    sync::Mutex,
};

use agent_client_protocol::Client;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    self as acp, ContentBlock, ContentChunk, PermissionOption, PermissionOptionKind,
    RequestPermissionOutcome, RequestPermissionRequest, SessionNotification, SessionUpdate,
    TextContent, ToolCall, ToolCallContent, ToolCallStatus, ToolKind,
};
use ox_protocol::{AgentState, AgentStateNotification};
use serde_json::{Value, json};
use uuid::Uuid;

use crate::runner::{Event, PermissionRequest, ToolOutput, message_events};

#[derive(Default)]
struct Tools {
    counter: u64,
    seen: HashSet<String>,
    /// Tool calls that have started and not reported a result.
    open: HashSet<String>,
    /// The activity of each subagent, by the tool call that started it: a
    /// line for each tool the subagent called.
    activity: HashMap<String, Vec<ToolCallContent>>,
}

/// Translates a session's Claude events and permission requests into ACP updates.
pub struct Updates {
    connection: ConnectionTo<Client>,
    session_id: String,
    cwd: PathBuf,
    tools: Mutex<Tools>,
}

impl Updates {
    pub fn new(connection: ConnectionTo<Client>, session_id: &str, cwd: &Path) -> Self {
        Self {
            connection,
            session_id: session_id.to_owned(),
            cwd: cwd.to_owned(),
            tools: Mutex::new(Tools::default()),
        }
    }

    fn send(&self, update: SessionUpdate) {
        notify(&self.connection, &self.session_id, update);
    }

    /// Tells the client that Claude started or stopped working.
    pub fn state(&self, state: AgentState) {
        let notification = AgentStateNotification {
            session_id: acp::SessionId::new(self.session_id.clone()),
            state,
        };
        if let Err(error) = self.connection.send_notification(notification) {
            eprintln!("failed to send the agent's state: {error}");
        }
    }

    /// Reports an error of a turn no prompt is waiting on.
    pub fn error(&self, error: &str) {
        self.send(error_message(error));
    }

    pub fn event(&self, event: Event) {
        let update = match event {
            Event::UserText(text) => SessionUpdate::UserMessageChunk(ContentChunk::new(
                ContentBlock::Text(TextContent::new(text)),
            )),
            Event::UserImage { data, mime_type } => SessionUpdate::UserMessageChunk(
                ContentChunk::new(ContentBlock::Image(acp::ImageContent::new(data, mime_type))),
            ),
            // Claude keeps a document's contents, not where it came from.
            Event::UserDocument { data, mime_type } => SessionUpdate::UserMessageChunk(
                ContentChunk::new(ContentBlock::Resource(acp::EmbeddedResource::new(
                    acp::EmbeddedResourceResource::BlobResourceContents(
                        acp::BlobResourceContents::new(data, "document").mime_type(mime_type),
                    ),
                ))),
            ),
            Event::Text(text) => SessionUpdate::AgentMessageChunk(ContentChunk::new(
                ContentBlock::Text(TextContent::new(text)),
            )),
            Event::Thought(text) => SessionUpdate::AgentThoughtChunk(ContentChunk::new(
                ContentBlock::Text(TextContent::new(text)),
            )),
            Event::Tool { id, name, input } => {
                let tool_id = {
                    let mut tools = self.tools.lock().unwrap();
                    tools.counter += 1;
                    let id = id.unwrap_or_else(|| format!("call_{}", tools.counter));
                    tools.seen.insert(id.clone());
                    tools.open.insert(id.clone());
                    id
                };
                SessionUpdate::ToolCall(
                    ToolCall::new(tool_id, tool_title(&name, &input))
                        .name(name)
                        .kind(ToolKind::Execute)
                        .status(ToolCallStatus::Pending)
                        .raw_input(input),
                )
            }
            Event::ToolResult {
                id,
                is_error,
                output,
            } => {
                let output = output.into_iter().map(|output| match output {
                    ToolOutput::Text(text) => {
                        ToolCallContent::from(ContentBlock::Text(TextContent::new(text)))
                    }
                    ToolOutput::Image { data, mime_type } => ToolCallContent::from(
                        ContentBlock::Image(acp::ImageContent::new(data, mime_type)),
                    ),
                });
                let content = {
                    let mut tools = self.tools.lock().unwrap();
                    tools.open.remove(&id);
                    // A subagent may go on in the background after its tool
                    // call's result, and its activity follows the result.
                    match tools.activity.get_mut(&id) {
                        Some(activity) => {
                            activity.extend(output);
                            activity.clone()
                        }
                        None => output.collect(),
                    }
                };
                let status = if is_error {
                    ToolCallStatus::Failed
                } else {
                    ToolCallStatus::Completed
                };
                let mut fields = acp::ToolCallUpdateFields::new().status(status);
                if !content.is_empty() {
                    fields = fields.content(content);
                }
                SessionUpdate::ToolCallUpdate(acp::ToolCallUpdate::new(id, fields))
            }
        };
        self.send(update);
    }

    /// Shows an event of the subagent that tool call `parent` started. Each
    /// tool the subagent calls becomes a line of the parent's content, ahead
    /// of the parent's result; the subagent's messages are its own, and its
    /// answer is the parent's result. A tool call of the subagent that the
    /// client has, because it asked permission for it, gets its result.
    pub fn subagent_event(&self, parent: &str, event: Event) {
        let update = {
            let mut tools = self.tools.lock().unwrap();
            match event {
                Event::Tool { name, input, .. } if tools.seen.contains(parent) => {
                    let activity = tools.activity.entry(parent.to_owned()).or_default();
                    activity.push(ToolCallContent::from(ContentBlock::Text(TextContent::new(
                        tool_title(&name, &input),
                    ))));
                    acp::ToolCallUpdate::new(
                        parent.to_owned(),
                        acp::ToolCallUpdateFields::new().content(activity.clone()),
                    )
                }
                Event::ToolResult { ref id, .. } if tools.seen.contains(id) => {
                    drop(tools);
                    return self.event(event);
                }
                _ => return,
            }
        };
        self.send(SessionUpdate::ToolCallUpdate(update));
    }

    /// Sends a conversation from Claude's transcript as the updates its turns
    /// produced, with the transcript of each subagent, keyed by the tool call
    /// that started it, after that tool call. Tool calls left without a
    /// result belong to interrupted turns and end as failed.
    pub fn replay(
        &self,
        entries: impl IntoIterator<Item = Value>,
        mut subagents: HashMap<String, Vec<Value>>,
    ) {
        for entry in entries {
            for event in message_events(&entry) {
                let subagent = match &event {
                    Event::Tool { id: Some(id), .. } => subagents.remove_entry(id),
                    _ => None,
                };
                self.event(event);
                if let Some((parent, entries)) = subagent {
                    for event in entries.iter().flat_map(message_events) {
                        self.subagent_event(&parent, event);
                    }
                }
            }
        }
        self.fail_open_tools();
    }

    /// Marks every tool call that started without reporting a result as failed.
    pub fn fail_open_tools(&self) {
        let open: Vec<String> = self.tools.lock().unwrap().open.drain().collect();
        for id in open {
            self.send(tool_status(id, ToolCallStatus::Failed));
        }
    }

    pub async fn permission(&self, permission: PermissionRequest) -> Value {
        let tool_id = if permission.tool_use_id.is_empty() {
            format!("permission_{}", Uuid::new_v4())
        } else {
            permission.tool_use_id.clone()
        };
        let title = tool_title(&permission.name, &permission.input);
        let title = if title == permission.name {
            permission.title.clone().unwrap_or(title)
        } else {
            title
        };
        let content = vec![ToolCallContent::from(ContentBlock::Text(TextContent::new(
            permission_content(&permission, &self.cwd),
        )))];
        let fields = acp::ToolCallUpdateFields::new()
            .title(title.clone())
            .name(permission.name.clone())
            .kind(ToolKind::Execute)
            .status(ToolCallStatus::Pending)
            .raw_input(permission.input.clone())
            .content(content.clone());
        let update = acp::ToolCallUpdate::new(tool_id.clone(), fields);
        let unseen = {
            let mut tools = self.tools.lock().unwrap();
            tools.open.insert(tool_id.clone());
            tools.seen.insert(tool_id.clone())
        };
        if unseen {
            self.send(SessionUpdate::ToolCall(
                ToolCall::new(tool_id.clone(), title)
                    .name(permission.name.clone())
                    .kind(ToolKind::Execute)
                    .status(ToolCallStatus::Pending)
                    .raw_input(permission.input.clone())
                    .content(content),
            ));
        } else {
            self.send(SessionUpdate::ToolCallUpdate(update.clone()));
        }

        let allow_always =
            !permission.suggestions.is_empty() && !permission.suppress_always_allow_rule;
        let mut options = vec![PermissionOption::new(
            "allow_once",
            "Allow once",
            PermissionOptionKind::AllowOnce,
        )];
        if allow_always {
            options.push(PermissionOption::new(
                "allow_always",
                "Allow always",
                PermissionOptionKind::AllowAlways,
            ));
        }
        options.push(PermissionOption::new(
            "reject_once",
            "Reject once",
            PermissionOptionKind::RejectOnce,
        ));
        let response = self
            .connection
            .send_request(RequestPermissionRequest::new(
                self.session_id.clone(),
                update,
                options,
            ))
            .block_task()
            .await;
        let selected = match response {
            Ok(response) => match response.outcome {
                RequestPermissionOutcome::Selected(selection) => {
                    Some(selection.option_id.to_string())
                }
                _ => None,
            },
            Err(_) => None,
        };
        let permanent = selected.as_deref() == Some("allow_always") && allow_always;
        let approved = selected.as_deref() == Some("allow_once") || permanent;
        self.send(tool_status(
            tool_id,
            if approved {
                ToolCallStatus::InProgress
            } else {
                ToolCallStatus::Failed
            },
        ));
        if approved {
            let mut decision = json!({
                "behavior": "allow",
                "updatedInput": if permission.input.is_object() {
                    permission.input
                } else {
                    json!({})
                },
                "toolUseID": permission.tool_use_id,
                "decisionClassification": if permanent { "user_permanent" } else { "user_temporary" }
            });
            if permanent {
                decision["updatedPermissions"] = json!(permission.suggestions);
            }
            decision
        } else {
            json!({
                "behavior": "deny",
                "message": "Permission denied by client",
                "toolUseID": permission.tool_use_id,
                "decisionClassification": "user_reject"
            })
        }
    }
}

fn tool_status(id: String, status: ToolCallStatus) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(acp::ToolCallUpdate::new(
        id,
        acp::ToolCallUpdateFields::new().status(status),
    ))
}

fn tool_title(name: &str, input: &Value) -> String {
    let detail = if name == "Bash" {
        input.get("command").and_then(Value::as_str)
    } else {
        input
            .get("file_path")
            .or_else(|| input.get("path"))
            .and_then(Value::as_str)
    };
    let Some(first_line) = detail.and_then(|text| text.trim().lines().next()) else {
        return name.to_owned();
    };
    let mut title: String = first_line.chars().take(160).collect();
    if first_line.chars().count() > 160 {
        title.push('…');
    }
    if title.is_empty() {
        name.to_owned()
    } else if name == "Bash" {
        title
    } else {
        format!("{name} {title}")
    }
}

fn permission_content(permission: &PermissionRequest, cwd: &Path) -> String {
    let detail = match permission.input.get("command").and_then(Value::as_str) {
        Some(command) if permission.name == "Bash" => format!("Command:\n\n{command}"),
        _ => format!(
            "Arguments:\n\n{}",
            serde_json::to_string_pretty(&permission.input).unwrap_or_default()
        ),
    };
    format!("Working directory: {}\n\n{detail}", cwd.display())
}

/// The message that shows a turn's error.
pub fn error_message(error: &str) -> SessionUpdate {
    SessionUpdate::AgentMessageChunk(ContentChunk::new(ContentBlock::Text(TextContent::new(
        format!("Error: {error}"),
    ))))
}

pub fn notify(connection: &ConnectionTo<Client>, id: &str, update: SessionUpdate) {
    if let Err(error) =
        connection.send_notification(SessionNotification::new(id.to_owned(), update))
    {
        eprintln!("failed to send session update: {error}");
    }
}
