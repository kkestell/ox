//! oxctl: a command-line client for oxd.

use std::collections::BTreeMap;
use std::error::Error;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use clap::{Parser, Subcommand};
use ox_protocol::schema::{
    ContentBlock, EmbeddedResourceResource, ImageContent, RequestPermissionOutcome,
    SessionConfigKind, SessionConfigOption, SessionConfigSelectOptions, SessionUpdate, StateUpdate,
    StopReason, TextContent, ToolCallStatus,
};
use ox_protocol::{Client, ConfigValue, End, Event, EventKind, SessionState, content};

#[derive(Parser)]
#[command(name = "oxctl", about = "Control oxd sessions")]
struct Cli {
    /// oxd socket [default: $`OX_SOCKET`, else <data dir>/ox/oxd.sock]
    #[arg(long, global = true)]
    socket: Option<PathBuf>,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// List sessions
    Ls,
    /// Add, list, and remove workspaces
    Workspace {
        #[command(subcommand)]
        command: WorkspaceCommand,
    },
    /// Start a session in a workspace and print its id
    New {
        /// The workspace's id, as `workspace ls` shows it
        #[arg(long)]
        workspace: String,
        /// Agent command [default: claude-code-acp]
        #[arg(last = true)]
        command: Vec<String>,
    },
    /// Restart the agent of an exited session, replaying its conversation,
    /// and print the session's id
    Load {
        /// The session's id, as `ls` shows it
        session: String,
    },
    /// Send a prompt
    Prompt {
        session: String,
        text: String,
        /// Attach an image (PNG, JPEG, GIF or WebP)
        #[arg(long)]
        image: Vec<PathBuf>,
        /// Reference a file the agent can read
        #[arg(long)]
        file: Vec<PathBuf>,
    },
    /// Print a session's config options, or set one
    Config {
        session: String,
        /// Option to set
        #[arg(requires = "value")]
        option: Option<String>,
        /// New value: a value id, or true or false
        value: Option<String>,
    },
    /// Print a session's events, then follow live events
    Attach {
        session: String,
        /// Print only events after this seq
        #[arg(long, default_value_t = 0)]
        after: u64,
        /// Print each event as a JSON line
        #[arg(long)]
        json: bool,
    },
    /// Answer a permission request
    Permit {
        session: String,
        request_id: String,
        option_id: String,
    },
    /// Cancel the running turn
    Cancel { session: String },
    /// Stop a session's agent and forget the session
    Close { session: String },
}

#[derive(Subcommand)]
enum WorkspaceCommand {
    /// Add a directory as a workspace and print its id
    Add { path: PathBuf },
    /// List workspaces
    Ls,
    /// Remove a workspace that has no sessions
    Rm { workspace: String },
}

#[tokio::main]
async fn main() -> ExitCode {
    let cli = Cli::parse();
    match run(cli).await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("oxctl: {error}");
            ExitCode::FAILURE
        }
    }
}

async fn run(cli: Cli) -> Result<(), Box<dyn Error>> {
    let socket = cli.socket.unwrap_or_else(ox_protocol::default_socket_path);
    let client = Client::connect_or_start(&socket).await?;
    match cli.command {
        Command::Ls => {
            for session in client.list().await? {
                println!(
                    "{}\t{}\t{}\t{}\t{}\t{}\t{}",
                    session.id,
                    session.workspace_id,
                    session.state.as_str(),
                    session.cwd.display(),
                    session.command.join(" "),
                    session.title.as_deref().unwrap_or("-"),
                    session.agent_session_id.as_deref().unwrap_or("-"),
                );
            }
        }
        Command::Workspace { command } => match command {
            WorkspaceCommand::Add { path } => {
                let workspace = client.add_workspace(path.canonicalize()?).await?;
                println!("{}", workspace.id);
            }
            WorkspaceCommand::Ls => {
                for workspace in client.list_workspaces().await? {
                    println!("{}\t{}", workspace.id, workspace.path.display());
                }
            }
            WorkspaceCommand::Rm { workspace } => client.remove_workspace(&workspace).await?,
        },
        Command::New { workspace, command } => {
            let command = if command.is_empty() {
                vec!["claude-code-acp".to_owned()]
            } else {
                command
            };
            let session = client.new_session(&workspace, command, utf8_env()).await?;
            println!("{}", started(&client, &session.id).await?);
        }
        Command::Load { session } => {
            client.load_session(&session, utf8_env()).await?;
            println!("{}", loaded(&client, &session).await?);
        }
        Command::Prompt {
            session,
            text,
            image,
            file,
        } => {
            let mut prompt = vec![ContentBlock::Text(TextContent::new(text))];
            for path in image {
                prompt.push(image_block(&path)?);
            }
            for path in file {
                prompt.push(content::file_link(&path)?);
            }
            client.prompt(&session, prompt).await?;
        }
        Command::Config {
            session,
            option,
            value,
        } => {
            let info = client
                .list()
                .await?
                .into_iter()
                .find(|info| info.id == session)
                .ok_or_else(|| format!("no session {session}"))?;
            let options = match (option, value) {
                (Some(option), Some(value)) => {
                    let boolean = info.config_options.iter().any(|candidate| {
                        *candidate.config_id.0 == *option
                            && matches!(candidate.kind, SessionConfigKind::Boolean(_))
                    });
                    let value =
                        if boolean {
                            ConfigValue::Boolean(value.parse().map_err(|_| {
                                format!("{option} takes true or false, not {value}")
                            })?)
                        } else {
                            ConfigValue::Id(value)
                        };
                    client.set_config_option(&session, &option, value).await?
                }
                _ => info.config_options,
            };
            for option in &options {
                println!("{}", describe_option(option));
            }
        }
        Command::Attach {
            session,
            after,
            json,
        } => {
            let mut events = client.subscribe(&session, after).await?;
            let mut printer = Printer::default();
            loop {
                match events.next().await {
                    Ok(event) if json => println!("{}", serde_json::to_string(&event)?),
                    Ok(event) => printer.print(&event),
                    Err(End::SessionClosed) => break,
                    Err(End::Disconnected) => return Err(ox_protocol::Error::Closed.into()),
                }
            }
        }
        Command::Permit {
            session,
            request_id,
            option_id,
        } => client.permit(&session, &request_id, &option_id).await?,
        Command::Cancel { session } => client.cancel(&session).await?,
        Command::Close { session } => client.close(&session).await?,
    }
    Ok(())
}

fn image_block(path: &Path) -> Result<ContentBlock, Box<dyn Error>> {
    let extension = path
        .extension()
        .and_then(|extension| extension.to_str())
        .map(str::to_ascii_lowercase);
    let mime_type = match extension.as_deref() {
        Some("png") => "image/png",
        Some("jpg" | "jpeg") => "image/jpeg",
        Some("gif") => "image/gif",
        Some("webp") => "image/webp",
        _ => {
            return Err(format!("{} is not a PNG, JPEG, GIF or WebP image", path.display()).into());
        }
    };
    let data = std::fs::read(path).map_err(|error| format!("{}: {error}", path.display()))?;
    Ok(ContentBlock::Image(ImageContent::new(
        STANDARD.encode(data),
        mime_type,
    )))
}

/// One line per option: id, current value, and the values it takes.
fn describe_option(option: &SessionConfigOption) -> String {
    match &option.kind {
        SessionConfigKind::Select(select) => {
            let values: Vec<String> = match &select.options {
                SessionConfigSelectOptions::Ungrouped(options) => {
                    options.iter().map(|o| o.value.to_string()).collect()
                }
                SessionConfigSelectOptions::Grouped(groups) => groups
                    .iter()
                    .flat_map(|group| &group.options)
                    .map(|o| o.value.to_string())
                    .collect(),
                _ => Vec::new(),
            };
            format!(
                "{}\t{}\t{}",
                option.config_id.0,
                select.current_value,
                values.join(", ")
            )
        }
        SessionConfigKind::Boolean(boolean) => {
            format!(
                "{}\t{}\ttrue, false",
                option.config_id.0, boolean.current_value
            )
        }
        _ => format!("{}\t?", option.config_id.0),
    }
}

/// Waits for a session's agent to start and returns the session's id. A
/// session whose agent fails to start is closed.
async fn started(client: &Client, session_id: &str) -> Result<String, Box<dyn Error>> {
    let mut events = client.subscribe(session_id, 0).await?;
    loop {
        match events.next().await {
            Ok(event) => match event.kind {
                EventKind::AgentStarted { .. } => return Ok(session_id.to_owned()),
                EventKind::AgentExited { error } => {
                    client.close(session_id).await?;
                    let error = error.unwrap_or_else(|| "it exited".into());
                    return Err(format!("the agent did not start: {error}").into());
                }
                _ => {}
            },
            Err(End::SessionClosed) => {
                return Err("the session closed while its agent was starting".into());
            }
            Err(End::Disconnected) => return Err(ox_protocol::Error::Closed.into()),
        }
    }
}

/// Waits for a loading session's agent to start and returns the session's id.
/// A session whose agent fails to start stays exited, and can be loaded again.
async fn loaded(client: &Client, session_id: &str) -> Result<String, Box<dyn Error>> {
    let mut watch = client.watch().await?;
    loop {
        let listing = watch.next().await.map_err(|_| ox_protocol::Error::Closed)?;
        let Some(session) = listing
            .sessions
            .iter()
            .find(|session| session.id == session_id)
        else {
            return Err("the session closed while its agent was starting".into());
        };
        match session.state {
            SessionState::Starting => {}
            SessionState::Exited => {
                return Err(format!(
                    "the agent did not start; `oxctl attach {session_id}` shows why"
                )
                .into());
            }
            _ => return Ok(session_id.to_owned()),
        }
    }
}

/// The environment variables whose names and values are UTF-8.
fn utf8_env() -> BTreeMap<String, String> {
    std::env::vars_os()
        .filter_map(|(name, value)| Some((name.into_string().ok()?, value.into_string().ok()?)))
        .collect()
}

/// Prints agent message chunks as running text and every other event on a
/// line of its own, prefixed with its seq.
#[derive(Default)]
struct Printer {
    mid_line: bool,
}

impl Printer {
    fn print(&mut self, event: &Event) {
        let mut stdout = std::io::stdout().lock();
        if let EventKind::Update(SessionUpdate::AgentMessageChunk(chunk)) = &event.kind
            && let ContentBlock::Text(text) = &chunk.content
        {
            let _ = write!(stdout, "{}", text.text);
            let _ = stdout.flush();
            self.mid_line = !text.text.ends_with('\n');
            return;
        }
        if self.mid_line {
            let _ = writeln!(stdout);
            self.mid_line = false;
        }
        let _ = writeln!(stdout, "{} {}", event.seq, describe(&event.kind));
    }
}

fn describe(kind: &EventKind) -> String {
    match kind {
        EventKind::AgentStarted { .. } => "agent started".into(),
        EventKind::PromptStarted { prompt } => {
            let parts: Vec<String> = prompt.iter().map(describe_block).collect();
            format!("prompt: {}", parts.join(" "))
        }
        EventKind::TurnStarted => "turn started".into(),
        EventKind::Update(update) => describe_update(update),
        EventKind::PermissionRequested {
            request_id,
            title,
            options,
            ..
        } => {
            let options: Vec<String> = options
                .iter()
                .map(|option| format!("{} ({})", option.option_id.0, option.name))
                .collect();
            format!(
                "permission {request_id}: {title}; options: {}",
                options.join(", ")
            )
        }
        EventKind::PermissionResolved {
            request_id,
            outcome,
        } => match outcome {
            RequestPermissionOutcome::Selected(selected) => {
                format!("permission {request_id}: {}", selected.option_id.0)
            }
            _ => format!("permission {request_id}: cancelled"),
        },
        EventKind::TurnEnded { stop_reason, error } => match (stop_reason, error) {
            (_, Some(error)) => format!("turn ended: {error}"),
            (Some(reason), None) => format!("turn ended: {}", stop_reason_name(reason)),
            (None, None) => "turn ended".into(),
        },
        EventKind::AgentExited { error: Some(error) } => format!("agent exited: {error}"),
        EventKind::AgentExited { error: None } => "agent exited".into(),
        EventKind::SessionClosed => "session closed".into(),
    }
}

fn describe_block(block: &ContentBlock) -> String {
    match block {
        ContentBlock::Text(text) => text.text.clone(),
        ContentBlock::Image(image) => format!("[image {}]", image.mime_type),
        ContentBlock::Audio(audio) => format!("[audio {}]", audio.mime_type),
        ContentBlock::ResourceLink(link) => format!("[{}]", link.uri),
        ContentBlock::Resource(resource) => match &resource.resource {
            EmbeddedResourceResource::TextResourceContents(text) => format!("[{}]", text.uri),
            EmbeddedResourceResource::BlobResourceContents(blob) => format!("[{}]", blob.uri),
            _ => "[resource]".into(),
        },
        _ => "[content]".into(),
    }
}

fn describe_update(update: &SessionUpdate) -> String {
    match update {
        SessionUpdate::AgentThoughtChunk(chunk)
        | SessionUpdate::UserMessageChunk(chunk)
        | SessionUpdate::AgentMessageChunk(chunk) => {
            format!("{}: {}", update_name(update), block_text(&chunk.content))
        }
        SessionUpdate::UserMessage(message) => {
            format!(
                "{}: {}",
                update_name(update),
                blocks_text(message.content.clone().take().as_deref())
            )
        }
        SessionUpdate::AgentMessage(message) => {
            format!(
                "{}: {}",
                update_name(update),
                blocks_text(message.content.clone().take().as_deref())
            )
        }
        SessionUpdate::AgentThought(thought) => {
            format!(
                "{}: {}",
                update_name(update),
                blocks_text(thought.content.clone().take().as_deref())
            )
        }
        SessionUpdate::StateUpdate(state) => match state {
            StateUpdate::Idle(idle) => match &idle.stop_reason {
                Some(reason) => format!("state: idle ({})", stop_reason_name(reason)),
                None => "state: idle".into(),
            },
            StateUpdate::Running(_) => "state: running".into(),
            StateUpdate::RequiresAction(_) => "state: requires_action".into(),
            _ => "state: other".into(),
        },
        SessionUpdate::ConfigOptionUpdate(update) => {
            let options: Vec<String> = update
                .config_options
                .iter()
                .map(|option| match &option.kind {
                    SessionConfigKind::Select(select) => {
                        format!("{}={}", option.config_id.0, select.current_value)
                    }
                    SessionConfigKind::Boolean(boolean) => {
                        format!("{}={}", option.config_id.0, boolean.current_value)
                    }
                    _ => option.config_id.0.to_string(),
                })
                .collect();
            format!("config: {}", options.join(", "))
        }
        SessionUpdate::ToolCallUpdate(call) => {
            let detail = match (call.title.clone().take(), call.status.clone().take()) {
                (Some(title), _) => title,
                (None, Some(status)) => status_name(&status).into(),
                (None, None) => "updated".into(),
            };
            format!("tool call {}: {detail}", call.tool_call_id.0)
        }
        _ => update_name(update).into(),
    }
}

/// The text of a content block, empty for a block that is not text.
fn block_text(block: &ContentBlock) -> &str {
    match block {
        ContentBlock::Text(text) => text.text.as_str(),
        _ => "",
    }
}

/// The text of a message's content, which a snapshot may leave unchanged or
/// clear.
fn blocks_text(content: Option<&[ContentBlock]>) -> String {
    content.unwrap_or_default().iter().map(block_text).collect()
}

/// The `sessionUpdate` name of an update.
fn update_name(update: &SessionUpdate) -> &'static str {
    match update {
        SessionUpdate::UserMessageChunk(_) => "user_message_chunk",
        SessionUpdate::UserMessage(_) => "user_message",
        SessionUpdate::AgentMessageChunk(_) => "agent_message_chunk",
        SessionUpdate::AgentMessage(_) => "agent_message",
        SessionUpdate::AgentThoughtChunk(_) => "agent_thought_chunk",
        SessionUpdate::AgentThought(_) => "agent_thought",
        SessionUpdate::StateUpdate(_) => "state_update",
        SessionUpdate::ToolCallContentChunk(_) => "tool_call_content_chunk",
        SessionUpdate::ToolCallUpdate(_) => "tool_call_update",
        SessionUpdate::TerminalUpdate(_) => "terminal_update",
        SessionUpdate::TerminalOutputChunk(_) => "terminal_output_chunk",
        SessionUpdate::PlanUpdate(_) => "plan_update",
        SessionUpdate::AvailableCommandsUpdate(_) => "available_commands_update",
        SessionUpdate::ConfigOptionUpdate(_) => "config_option_update",
        SessionUpdate::SessionInfoUpdate(_) => "session_info_update",
        SessionUpdate::UsageUpdate(_) => "usage_update",
        SessionUpdate::Notice(_) => "notice",
        SessionUpdate::CompactionUpdate(_) => "compaction_update",
        SessionUpdate::CompactionSummaryChunk(_) => "compaction_summary_chunk",
        _ => "update",
    }
}

/// The name of a stop reason, with the failure's message for `error`.
fn stop_reason_name(reason: &StopReason) -> String {
    match reason {
        StopReason::EndTurn => "end_turn".into(),
        StopReason::MaxTokens => "max_tokens".into(),
        StopReason::MaxTurnRequests => "max_turn_requests".into(),
        StopReason::Refusal => "refusal".into(),
        StopReason::Cancelled => "cancelled".into(),
        StopReason::Error(details) => match &details.error {
            Some(error) => format!("error: {}", error.message),
            None => "error".into(),
        },
        StopReason::Other(other) => other.stop_reason.clone(),
        _ => "other".into(),
    }
}

fn status_name(status: &ToolCallStatus) -> &'static str {
    match status {
        ToolCallStatus::Pending => "pending",
        ToolCallStatus::InProgress => "in_progress",
        ToolCallStatus::Completed => "completed",
        ToolCallStatus::Failed => "failed",
        _ => "other",
    }
}
