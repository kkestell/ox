use std::{
    collections::{HashMap, HashSet},
    sync::Mutex,
};

use agent_client_protocol::Client;
use agent_client_protocol::v2::ConnectionTo;
use agent_client_protocol::v2::schema::{
    self as acp, BackgroundWork, ContentBlock, ContentChunk, CreateElicitationRequest,
    ElicitationAction, ElicitationFormMode, ElicitationSessionScope, MessageId, PermissionOption,
    PermissionOptionKind, RequestPermissionOutcome, RequestPermissionRequest, SessionUpdate,
    StateUpdate, StopReason, TextContent, ToolCallContent, ToolCallStatus, ToolKind,
    UpdateSessionNotification,
};
use serde_json::{Value, json};
use uuid::Uuid;

use crate::background::{Background, HeldStatus, now_ms};
use crate::questions;
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
    /// Tool calls that have a result but stand for background work that
    /// goes on, with the content they showed.
    held: HashMap<String, Vec<ToolCallContent>>,
}

/// What the session is doing, as far as the client is concerned.
#[derive(Default)]
struct Work {
    /// Claude is working on a turn.
    working: bool,
    /// Permission requests and questions waiting for the client.
    asking: usize,
    /// The last state sent to the client; a new session is idle.
    sent: Sent,
    /// The last background list sent to the client; a new session has none.
    sent_background: Vec<BackgroundWork>,
    /// Claude's background tasks and scheduled prompts.
    background: Background,
    /// When the running turn started, in epoch milliseconds.
    turn_started: Option<u64>,
}

#[derive(Default, PartialEq, Clone, Copy)]
enum Sent {
    #[default]
    Idle,
    Running,
    RequiresAction,
}

/// Translates a session's Claude events, permission requests, and questions
/// into ACP updates.
pub struct Updates {
    connection: ConnectionTo<Client>,
    session_id: String,
    /// The client takes form elicitations, so `AskUserQuestion`'s questions
    /// can be asked.
    forms: bool,
    commands: Mutex<Vec<acp::AvailableCommand>>,
    tools: Mutex<Tools>,
    work: Mutex<Work>,
}

impl Updates {
    pub fn new(connection: ConnectionTo<Client>, session_id: &str, forms: bool) -> Self {
        Self {
            connection,
            session_id: session_id.to_owned(),
            forms,
            commands: Mutex::new(Vec::new()),
            tools: Mutex::new(Tools::default()),
            work: Mutex::new(Work::default()),
        }
    }

    fn send(&self, update: SessionUpdate) {
        notify(&self.connection, &self.session_id, update);
    }

    pub fn commands(&self) -> Vec<acp::AvailableCommand> {
        self.commands.lock().unwrap().clone()
    }

    pub fn set_commands(&self, commands: Vec<acp::AvailableCommand>) {
        self.commands.lock().unwrap().clone_from(&commands);
        self.send(SessionUpdate::AvailableCommandsUpdate(
            acp::AvailableCommandsUpdate::new(commands),
        ));
    }

    /// Tells the client that Claude started working, if it has not said so.
    pub fn working(&self) {
        let mut work = self.work.lock().unwrap();
        Self::start_turn(&mut work);
        self.publish(&mut work, None);
    }

    fn start_turn(work: &mut Work) {
        if !work.working {
            work.turn_started = Some(now_ms());
        }
        work.working = true;
    }

    /// Ends the running turn's part in the schedules: what it fulfilled is
    /// dropped, and so are the tool calls of the work that ended.
    fn end_turn(&self, work: &mut Work) {
        work.working = false;
        if let Some(started) = work.turn_started.take() {
            work.background.turn_ended(started, now_ms());
            self.release(work);
        }
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
            Self::start_turn(&mut work);
            self.publish(&mut work, None);
        }
        self.end_turn(&mut work);
        self.publish(&mut work, Some(stop_reason));
    }

    /// Tells the client that Claude stopped working, without a reason.
    pub fn idle_without_reason(&self) {
        let mut work = self.work.lock().unwrap();
        self.end_turn(&mut work);
        self.publish(&mut work, None);
    }

    /// The state to give a client that resumes the session.
    pub fn state(&self) -> StateUpdate {
        let work = self.work.lock().unwrap();
        state_update(Self::current(&work), None, self.render(&work))
    }

    /// The background list: work whose tool call the client has, and has
    /// not seen end, is listed as that tool call.
    fn render(&self, work: &Work) -> Vec<BackgroundWork> {
        let tools = self.tools.lock().unwrap();
        work.background.items(|id| tools.open.contains(id))
    }

    /// Notes that claude started a task, which `tool_use_id` started.
    pub fn task_started(&self, task_id: &str, tool_use_id: &str) {
        let mut work = self.work.lock().unwrap();
        work.background.task_started(task_id, tool_use_id);
        self.publish(&mut work, None);
    }

    /// Replaces the live background tasks with claude's latest set.
    pub fn background_tasks(&self, tasks: Vec<String>) {
        let mut work = self.work.lock().unwrap();
        work.background.set_tasks(tasks);
        self.release(&work);
        self.publish(&mut work, None);
    }

    /// Notes the scheduling tool calls of a message of claude's output.
    pub fn scheduling(&self, message: &Value, main_thread: bool) {
        let mut work = self.work.lock().unwrap();
        if work.background.observe(message, now_ms(), main_thread) {
            self.release(&work);
            self.publish(&mut work, None);
        }
    }

    /// Seeds the schedules a resumed conversation has from its transcript.
    pub fn seed(&self, transcript: &[Value]) {
        self.work
            .lock()
            .unwrap()
            .background
            .seed(transcript, now_ms());
    }

    /// Gives the final status to the tool call of a task that reported its
    /// end, with its summary added to the content.
    pub fn task_notification(&self, tool_use_id: &str, status: &str, summary: &str) {
        let mut tools = self.tools.lock().unwrap();
        let Some(held) = tools.held.remove(tool_use_id) else {
            return;
        };
        tools.open.remove(tool_use_id);
        let mut content = tools.activity.get(tool_use_id).cloned().unwrap_or(held);
        if !summary.is_empty() {
            content.push(ToolCallContent::from(ContentBlock::Text(TextContent::new(
                summary,
            ))));
        }
        let status = if status == "failed" {
            ToolCallStatus::Failed
        } else {
            ToolCallStatus::Completed
        };
        drop(tools);
        self.send(SessionUpdate::ToolCallUpdate(
            acp::ToolCallUpdate::new(tool_use_id.to_owned())
                .status(status)
                .content(content),
        ));
    }

    /// Completes the held tool calls of work that has ended.
    fn release(&self, work: &Work) {
        let listed = work.background.tool_calls();
        let mut tools = self.tools.lock().unwrap();
        let ended: Vec<String> = tools
            .held
            .keys()
            .filter(|id| !listed.contains(*id))
            .cloned()
            .collect();
        for id in &ended {
            tools.held.remove(id);
            tools.open.remove(id);
        }
        drop(tools);
        for id in ended {
            self.send(tool_status(id, ToolCallStatus::Completed));
        }
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
        let background = self.render(work);
        if current != work.sent || work.sent_background != background {
            work.sent = current;
            work.sent_background.clone_from(&background);
            self.send(SessionUpdate::StateUpdate(state_update(
                current,
                stop_reason,
                background,
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
                // Work that goes on in the background keeps its tool call open.
                let held = if is_error {
                    None
                } else {
                    self.work.lock().unwrap().background.held_status(&id)
                };
                let content = {
                    let mut tools = self.tools.lock().unwrap();
                    // A subagent may go on in the background after its tool
                    // call's result, and its activity follows the result.
                    let content: Vec<ToolCallContent> = match tools.activity.get_mut(&id) {
                        Some(activity) => {
                            activity.extend(output);
                            activity.clone()
                        }
                        None => output.collect(),
                    };
                    if held.is_some() {
                        tools.held.insert(id.clone(), content.clone());
                    } else {
                        tools.open.remove(&id);
                    }
                    content
                };
                let status = match (held, is_error) {
                    (Some(HeldStatus::Pending), _) => ToolCallStatus::Pending,
                    (Some(HeldStatus::InProgress), _) => ToolCallStatus::InProgress,
                    (None, true) => ToolCallStatus::Failed,
                    (None, false) => ToolCallStatus::Completed,
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
                        tool_line(&name, &input),
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

    /// Marks every tool call that started without reporting a result as
    /// failed, except those that stand for listed background work.
    pub fn fail_open_tools(&self) {
        let listed = self.work.lock().unwrap().background.tool_calls();
        let mut tools = self.tools.lock().unwrap();
        let open: Vec<String> = tools
            .open
            .iter()
            .filter(|id| !listed.contains(*id))
            .cloned()
            .collect();
        for id in &open {
            tools.open.remove(id);
        }
        drop(tools);
        for id in open {
            self.send(tool_status(id, ToolCallStatus::Failed));
        }
    }

    /// Ends what claude left open when it exited: every unfinished tool call
    /// fails, and no background work is left.
    pub fn exited(&self) {
        self.work.lock().unwrap().background.clear();
        self.tools.lock().unwrap().held.clear();
        self.fail_open_tools();
        let mut work = self.work.lock().unwrap();
        self.publish(&mut work, None);
    }

    /// Sends the pending tool call a request asks about, which is then open,
    /// and returns it.
    fn ask_about(&self, permission: &PermissionRequest, kind: ToolKind) -> acp::ToolCallUpdate {
        let tool_id = if permission.tool_use_id.is_empty() {
            format!("permission_{}", Uuid::new_v4())
        } else {
            permission.tool_use_id.clone()
        };
        let update = acp::ToolCallUpdate::new(tool_id.clone())
            .title(tool_title(&permission.name, &permission.input))
            .name(permission.name.clone())
            .kind(kind)
            .status(ToolCallStatus::Pending)
            .raw_input(permission.input.clone());
        {
            let mut tools = self.tools.lock().unwrap();
            tools.open.insert(tool_id.clone());
            tools.seen.insert(tool_id);
        }
        self.send(SessionUpdate::ToolCallUpdate(update.clone()));
        update
    }

    pub async fn permission(&self, permission: PermissionRequest) -> Value {
        let update = self.ask_about(&permission, ToolKind::Execute);
        let tool_id = update.tool_call_id.to_string();
        let title = tool_title(&permission.name, &permission.input);
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
            deny(&permission, "Permission denied by client")
        }
    }

    /// Asks the client `AskUserQuestion`'s questions as a form and returns the
    /// tool's decision: allowed with the answers, or denied when the user
    /// does not answer or the questions cannot be asked.
    pub async fn ask_user(&self, permission: PermissionRequest) -> Value {
        // Claude starts without the tool when the client takes no forms.
        if !self.forms {
            return deny(&permission, "The questions could not be shown.");
        }
        let Some((message, schema)) = questions::form(&permission.input) else {
            return deny(&permission, "The questions could not be shown.");
        };
        let update = self.ask_about(&permission, ToolKind::Other);
        let tool_id = update.tool_call_id;
        // The turn waits for the client until the guard drops.
        let _asking = Asking::new(self);
        let response = self
            .connection
            .send_request(CreateElicitationRequest::new(
                ElicitationFormMode::new(
                    ElicitationSessionScope::new(self.session_id.clone())
                        .tool_call_id(tool_id.clone()),
                    schema,
                ),
                message,
            ))
            .block_task()
            .await;
        let decision = match response.map(|response| response.action) {
            Ok(ElicitationAction::Accept(accepted)) => json!({
                "behavior": "allow",
                "updatedInput": questions::answers(
                    &permission.input,
                    &accepted.content.unwrap_or_default(),
                ),
                "toolUseID": permission.tool_use_id,
                "decisionClassification": "user_temporary"
            }),
            Ok(ElicitationAction::Decline) => deny(&permission, "The user declined to answer."),
            _ => deny(&permission, "The user did not answer."),
        };
        self.send(tool_status(
            tool_id.to_string(),
            if decision["behavior"] == "allow" {
                ToolCallStatus::InProgress
            } else {
                ToolCallStatus::Failed
            },
        ));
        decision
    }
}

/// Claude's decision to deny the tool call a request asks about.
fn deny(permission: &PermissionRequest, message: &str) -> Value {
    json!({
        "behavior": "deny",
        "message": message,
        "toolUseID": permission.tool_use_id,
        "decisionClassification": "user_reject"
    })
}

/// Holds a permission request or questions open: the session requires action
/// until the guard drops, whether the client answered or the turn ended first.
struct Asking<'a>(&'a Updates);

impl<'a> Asking<'a> {
    fn new(updates: &'a Updates) -> Self {
        let mut work = updates.work.lock().unwrap();
        work.asking += 1;
        Updates::start_turn(&mut work);
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

fn state_update(
    state: Sent,
    stop_reason: Option<StopReason>,
    background: Vec<BackgroundWork>,
) -> StateUpdate {
    let background = Some(background);
    match state {
        Sent::Idle => StateUpdate::Idle(
            acp::IdleStateUpdate::new()
                .stop_reason(stop_reason)
                .background(background),
        ),
        Sent::Running => {
            StateUpdate::Running(acp::RunningStateUpdate::new().background(background))
        }
        Sent::RequiresAction => StateUpdate::RequiresAction(
            acp::RequiresActionStateUpdate::new().background(background),
        ),
    }
}

/// The state of a session that is idle with nothing running.
pub fn idle_state() -> StateUpdate {
    state_update(Sent::Idle, None, Vec::new())
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

/// The title of a call of tool `name`: the tool's name, followed by its
/// argument as Markdown inline code.
fn tool_title(name: &str, input: &Value) -> String {
    match tool_argument(input) {
        Some(argument) => format!("{name} {}", code_span(&argument)),
        None => name.to_owned(),
    }
}

/// A plain line for a call of tool `name`: the tool's name and its argument.
fn tool_line(name: &str, input: &Value) -> String {
    match tool_argument(input) {
        Some(argument) => format!("{name} {argument}"),
        None => name.to_owned(),
    }
}

/// The first line of a tool call's main argument.
fn tool_argument(input: &Value) -> Option<String> {
    let argument = [
        "command",
        "file_path",
        "notebook_path",
        "pattern",
        "path",
        "url",
        "query",
        "skill",
        "description",
    ]
    .iter()
    .find_map(|key| input.get(key).and_then(Value::as_str));
    argument
        .and_then(|text| text.trim().lines().next())
        .map(|first_line| {
            let mut argument: String = first_line.chars().take(160).collect();
            if first_line.chars().count() > 160 {
                argument.push('…');
            }
            argument
        })
}

/// `text` as a Markdown code span, fenced by a run of backticks longer than
/// any run inside it. A space pads each side where text starting or ending
/// with a backtick would extend the fence, or where Markdown would strip one
/// of the spaces `text` starts and ends with.
fn code_span(text: &str) -> String {
    let longest = text.split(|c| c != '`').map(str::len).max().unwrap_or(0);
    let fence = "`".repeat(longest + 1);
    let stripped = text.starts_with(' ') && text.ends_with(' ') && text.contains(|c| c != ' ');
    let pad = if text.starts_with('`') || text.ends_with('`') || stripped {
        " "
    } else {
        ""
    };
    format!("{fence}{pad}{text}{pad}{fence}")
}

pub fn notify(connection: &ConnectionTo<Client>, id: &str, update: SessionUpdate) {
    if let Err(error) =
        connection.send_notification(UpdateSessionNotification::new(id.to_owned(), update))
    {
        eprintln!("failed to send session update: {error}");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_code_span_fences_backticks_with_a_longer_run() {
        assert_eq!(code_span("ls -la"), "`ls -la`");
        assert_eq!(code_span("a``b"), "```a``b```");
        assert_eq!(code_span("echo `date`"), "`` echo `date` ``");
        assert_eq!(code_span(" a "), "`  a  `");
        assert_eq!(code_span("  "), "`  `");
    }
}
