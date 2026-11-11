use std::{collections::HashSet, path::Path, sync::Mutex};

use agent_client_protocol::Client;
use agent_client_protocol::v1::ConnectionTo;
use agent_client_protocol::v1::schema::{
    self as acp, ContentBlock, ContentChunk, PermissionOption, PermissionOptionKind,
    RequestPermissionOutcome, RequestPermissionRequest, SessionNotification, SessionUpdate,
    TextContent, ToolCall, ToolCallContent, ToolCallStatus, ToolKind,
};
use serde_json::{Value, json};
use uuid::Uuid;

use crate::runner::{Event, PermissionRequest};

#[derive(Default)]
struct Tools {
    counter: u64,
    seen: HashSet<String>,
    /// Tool calls that have started and not reported a result.
    open: HashSet<String>,
}

/// Translates one prompt's Claude events and permission requests into ACP updates.
pub struct Updates<'a> {
    connection: &'a ConnectionTo<Client>,
    session_id: &'a str,
    cwd: &'a Path,
    tools: Mutex<Tools>,
}

impl<'a> Updates<'a> {
    pub fn new(connection: &'a ConnectionTo<Client>, session_id: &'a str, cwd: &'a Path) -> Self {
        Self {
            connection,
            session_id,
            cwd,
            tools: Mutex::new(Tools::default()),
        }
    }

    fn send(&self, update: SessionUpdate) {
        notify(self.connection, self.session_id, update);
    }

    pub fn event(&self, event: Event) {
        let update = match event {
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
            Event::ToolResult { id, is_error } => {
                self.tools.lock().unwrap().open.remove(&id);
                tool_status(
                    id,
                    if is_error {
                        ToolCallStatus::Failed
                    } else {
                        ToolCallStatus::Completed
                    },
                )
            }
        };
        self.send(update);
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
            permission_content(&permission, self.cwd),
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
                self.session_id.to_owned(),
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

pub fn notify(connection: &ConnectionTo<Client>, id: &str, update: SessionUpdate) {
    if let Err(error) =
        connection.send_notification(SessionNotification::new(id.to_owned(), update))
    {
        eprintln!("failed to send session update: {error}");
    }
}
