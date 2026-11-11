use std::{
    collections::{HashMap, HashSet},
    path::{Path, PathBuf},
    sync::Mutex,
};

use agent_client_protocol::Client;
use agent_client_protocol::v2::ConnectionTo;
use agent_client_protocol::v2::schema::{
    self as acp, ContentBlock, ContentChunk, MessageId, PermissionOption, PermissionOptionKind,
    RequestPermissionOutcome, RequestPermissionRequest, SessionUpdate, StateUpdate, StopReason,
    TextContent, ToolCallContent, ToolCallStatus, ToolKind, UpdateSessionNotification,
};
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

/// What the session is doing, as far as the client is concerned.
#[derive(Default)]
struct Work {
    /// Claude is working on a turn.
    working: bool,
    /// Permission requests waiting for the client.
    asking: usize,
    /// The last state sent to the client; a new session is idle.
    sent: Sent,
}

#[derive(Default, PartialEq, Clone, Copy)]
enum Sent {
    #[default]
    Idle,
    Running,
    RequiresAction,
}

/// Translates a session's Claude events and permission requests into ACP updates.
pub struct Updates {
    connection: ConnectionTo<Client>,
    session_id: String,
    cwd: PathBuf,
    tools: Mutex<Tools>,
    work: Mutex<Work>,
}

impl Updates {
    pub fn new(connection: ConnectionTo<Client>, session_id: &str, cwd: &Path) -> Self {
        Self {
            connection,
            session_id: session_id.to_owned(),
            cwd: cwd.to_owned(),
            tools: Mutex::new(Tools::default()),
            work: Mutex::new(Work::default()),
        }
    }

    fn send(&self, update: SessionUpdate) {
        notify(&self.connection, &self.session_id, update);
    }

    /// Tells the client that Claude started working, if it has not said so.
    pub fn working(&self) {
        let mut work = self.work.lock().unwrap();
        work.working = true;
        self.publish(&mut work, None);
    }

    /// Whether Claude is working on a turn.
    pub fn is_working(&self) -> bool {
        self.work.lock().unwrap().working
    }

    /// Tells the client that Claude stopped working, with why. A turn that
    /// failed without Claude saying it started is reported as running and
    /// then idle, so the client sees its error.
    pub fn idle(&self, stop_reason: StopReason) {
        let mut work = self.work.lock().unwrap();
        if !work.working && matches!(stop_reason, StopReason::Error(_)) {
            work.working = true;
            self.publish(&mut work, None);
        }
        work.working = false;
        self.publish(&mut work, Some(stop_reason));
    }

    /// Tells the client that Claude stopped working, without a reason.
    pub fn idle_without_reason(&self) {
        let mut work = self.work.lock().unwrap();
        work.working = false;
        self.publish(&mut work, None);
    }

    /// The state to give a client that resumes the session.
    pub fn state(&self) -> StateUpdate {
        let work = self.work.lock().unwrap();
        state_update(Self::current(&work), None)
    }

    fn current(work: &Work) -> Sent {
        if !work.working {
            Sent::Idle
        } else if work.asking > 0 {
            Sent::RequiresAction
        } else {
            Sent::Running
        }
    }

    /// Sends the session's state when it differs from the last one sent.
    fn publish(&self, work: &mut Work, stop_reason: Option<StopReason>) {
        let current = Self::current(work);
        if current != work.sent {
            work.sent = current;
            self.send(SessionUpdate::StateUpdate(state_update(
                current,
                stop_reason,
            )));
        }
    }

    /// Sends a user message, which a prompt that claude took into a turn
    /// carries into the conversation.
    pub fn user_message(&self, message_id: &str, content: Vec<ContentBlock>) {
        self.send(SessionUpdate::UserMessage(
            acp::UserMessage::new(message_id).content(content),
        ));
    }

    /// Sends a piece of the message claude is streaming.
    pub fn text_chunk(&self, message_id: &str, text: String) {
        self.send(SessionUpdate::AgentMessageChunk(ContentChunk::new(
            ContentBlock::Text(TextContent::new(text)),
            message_id,
        )));
    }

    /// Sends a piece of the thought claude is streaming.
    pub fn thought_chunk(&self, message_id: &str, text: String) {
        self.send(SessionUpdate::AgentThoughtChunk(ContentChunk::new(
            ContentBlock::Text(TextContent::new(text)),
            message_id,
        )));
    }

    /// Sends a whole message, thought, or tool call update. Text from the
    /// user is the transcript's, and `replay` sends it.
    pub fn event(&self, event: Event) {
        let update = match event {
            Event::UserText(_) | Event::UserImage { .. } | Event::UserDocument { .. } => return,
            Event::Text(text) => SessionUpdate::AgentMessage(
                acp::AgentMessage::new(new_message_id())
                    .content(vec![ContentBlock::Text(TextContent::new(text))]),
            ),
            Event::Thought(text) => SessionUpdate::AgentThought(
                acp::AgentThought::new(new_message_id())
                    .content(vec![ContentBlock::Text(TextContent::new(text))]),
            ),
            Event::Tool { id, name, input } => {
                let tool_id = {
                    let mut tools = self.tools.lock().unwrap();
                    tools.counter += 1;
                    let id = id.unwrap_or_else(|| format!("call_{}", tools.counter));
                    tools.seen.insert(id.clone());
                    tools.open.insert(id.clone());
                    id
                };
                SessionUpdate::ToolCallUpdate(
                    acp::ToolCallUpdate::new(tool_id)
                        .title(tool_title(&name, &input))
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
                let mut update = acp::ToolCallUpdate::new(id).status(status);
                if !content.is_empty() {
                    update = update.content(content);
                }
                SessionUpdate::ToolCallUpdate(update)
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
                    acp::ToolCallUpdate::new(parent.to_owned()).content(activity.clone())
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
            // The blocks of one user entry make one message, which keeps the
            // entry's `uuid`: the `messageId` it had when it was inserted.
            let mut id = entry["uuid"].as_str().map(str::to_owned);
            let mut user = Vec::new();
            for event in message_events(&entry) {
                if let Some(block) = user_block(&event) {
                    user.push(block);
                    continue;
                }
                self.send_user(&mut id, &mut user);
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
            self.send_user(&mut id, &mut user);
        }
        self.fail_open_tools();
    }

    /// Sends `blocks` as a user message with the entry's ID, or a new one
    /// once a message of the entry has taken it.
    fn send_user(&self, id: &mut Option<String>, blocks: &mut Vec<ContentBlock>) {
        if !blocks.is_empty() {
            let id = id.take().unwrap_or_else(|| new_message_id().to_string());
            self.user_message(&id, std::mem::take(blocks));
        }
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
        let update = acp::ToolCallUpdate::new(tool_id.clone())
            .title(title.clone())
            .name(permission.name.clone())
            .kind(ToolKind::Execute)
            .status(ToolCallStatus::Pending)
            .raw_input(permission.input.clone())
            .content(content);
        {
            let mut tools = self.tools.lock().unwrap();
            tools.open.insert(tool_id.clone());
            tools.seen.insert(tool_id.clone());
        }
        self.send(SessionUpdate::ToolCallUpdate(update.clone()));
        // The turn waits for the client until the guard drops.
        let _asking = Asking::new(self);

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
            .send_request(
                RequestPermissionRequest::new(self.session_id.clone(), title, options)
                    .subject(acp::RequestPermissionSubject::from(update)),
            )
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

/// Holds a permission request open: the session requires action until the
/// guard drops, whether the client answered or the turn ended first.
struct Asking<'a>(&'a Updates);

impl<'a> Asking<'a> {
    fn new(updates: &'a Updates) -> Self {
        let mut work = updates.work.lock().unwrap();
        work.asking += 1;
        work.working = true;
        updates.publish(&mut work, None);
        Self(updates)
    }
}

impl Drop for Asking<'_> {
    fn drop(&mut self) {
        let mut work = self.0.work.lock().unwrap();
        work.asking -= 1;
        self.0.publish(&mut work, None);
    }
}

fn state_update(state: Sent, stop_reason: Option<StopReason>) -> StateUpdate {
    match state {
        Sent::Idle => StateUpdate::Idle(acp::IdleStateUpdate::new().stop_reason(stop_reason)),
        Sent::Running => StateUpdate::Running(acp::RunningStateUpdate::new()),
        Sent::RequiresAction => StateUpdate::RequiresAction(acp::RequiresActionStateUpdate::new()),
    }
}

fn new_message_id() -> MessageId {
    MessageId::new(Uuid::new_v4().to_string())
}

/// The prompt block a user event of a transcript shows as.
fn user_block(event: &Event) -> Option<ContentBlock> {
    match event {
        Event::UserText(text) => Some(ContentBlock::Text(TextContent::new(text.clone()))),
        Event::UserImage { data, mime_type } => Some(ContentBlock::Image(acp::ImageContent::new(
            data.clone(),
            mime_type.clone(),
        ))),
        // Claude keeps a document's contents, not where it came from.
        Event::UserDocument { data, mime_type } => Some(ContentBlock::Resource(
            acp::EmbeddedResource::new(acp::EmbeddedResourceResource::BlobResourceContents(
                acp::BlobResourceContents::new(data.clone(), "document")
                    .mime_type(mime_type.clone()),
            )),
        )),
        _ => None,
    }
}

fn tool_status(id: String, status: ToolCallStatus) -> SessionUpdate {
    SessionUpdate::ToolCallUpdate(acp::ToolCallUpdate::new(id).status(status))
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
        connection.send_notification(UpdateSessionNotification::new(id.to_owned(), update))
    {
        eprintln!("failed to send session update: {error}");
    }
}
