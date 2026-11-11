//! oxctl: a command-line client for oxd.

use std::collections::BTreeMap;
use std::io::Write;
use std::path::PathBuf;
use std::process::ExitCode;

use clap::{Parser, Subcommand};
use ox_protocol::schema::{ContentBlock, RequestPermissionOutcome, SessionUpdate};
use ox_protocol::{Client, Error, Event, EventKind};

#[derive(Parser)]
#[command(name = "oxctl", about = "Control oxd sessions")]
struct Cli {
    /// oxd socket [default: $OX_SOCKET, else <data dir>/ox/oxd.sock]
    #[arg(long, global = true)]
    socket: Option<PathBuf>,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// List sessions
    Ls,
    /// Start a session and print its id
    New {
        /// Working directory [default: current directory]
        #[arg(long)]
        cwd: Option<PathBuf>,
        /// Agent command [default: claude-code-acp]
        #[arg(last = true)]
        command: Vec<String>,
    },
    /// Send a prompt
    Prompt { session: String, text: String },
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

async fn run(cli: Cli) -> Result<(), Error> {
    let socket = cli.socket.unwrap_or_else(ox_protocol::default_socket_path);
    let client = Client::connect_or_start(&socket).await?;
    match cli.command {
        Command::Ls => {
            for session in client.list().await? {
                let state = serde_json::to_value(session.state)?;
                println!(
                    "{}\t{}\t{}\t{}\t{}",
                    session.id,
                    state.as_str().unwrap_or_default(),
                    session.last_seq,
                    session.cwd.display(),
                    session.command.join(" "),
                );
            }
        }
        Command::New { cwd, command } => {
            let cwd = match cwd {
                Some(cwd) => cwd,
                None => std::env::current_dir()?,
            }
            .canonicalize()?;
            let command = if command.is_empty() {
                vec!["claude-code-acp".to_owned()]
            } else {
                command
            };
            let session = client.new_session(command, cwd, utf8_env()).await?;
            println!("{}", session.id);
        }
        Command::Prompt { session, text } => client.prompt(&session, &text).await?,
        Command::Attach {
            session,
            after,
            json,
        } => {
            let mut events = client.subscribe(&session, after).await?;
            let mut printer = Printer::default();
            while let Some(event) = events.next().await {
                if json {
                    println!("{}", serde_json::to_string(&event)?);
                } else {
                    printer.print(&event);
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
        EventKind::PromptStarted { text } => format!("prompt: {text}"),
        EventKind::Update(update) => describe_update(update),
        EventKind::PermissionRequested {
            request_id,
            tool_call,
            options,
        } => {
            let title = tool_call.fields.title.as_deref().unwrap_or("tool call");
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
            (Some(reason), None) => format!("turn ended: {}", tag(reason)),
            (None, None) => "turn ended".into(),
        },
        EventKind::AgentExited { error: Some(error) } => format!("agent exited: {error}"),
        EventKind::AgentExited { error: None } => "agent exited".into(),
        EventKind::SessionClosed => "session closed".into(),
    }
}

fn describe_update(update: &SessionUpdate) -> String {
    match update {
        SessionUpdate::AgentThoughtChunk(chunk) | SessionUpdate::UserMessageChunk(chunk) => {
            let text = match &chunk.content {
                ContentBlock::Text(text) => text.text.as_str(),
                _ => "",
            };
            format!("{}: {text}", update_tag(update))
        }
        SessionUpdate::ToolCall(call) => {
            format!("tool call {}: {}", call.tool_call_id.0, call.title)
        }
        SessionUpdate::ToolCallUpdate(call) => {
            let status = call.fields.status.as_ref().map(tag).unwrap_or_default();
            format!("tool call {}: {status}", call.tool_call_id.0)
        }
        _ => update_tag(update),
    }
}

/// The `sessionUpdate` discriminator of an update.
fn update_tag(update: &SessionUpdate) -> String {
    serde_json::to_value(update)
        .ok()
        .and_then(|value| value.get("sessionUpdate")?.as_str().map(str::to_owned))
        .unwrap_or_default()
}

/// The serialized name of a unit enum value.
fn tag(value: &impl serde::Serialize) -> String {
    serde_json::to_value(value)
        .ok()
        .and_then(|value| value.as_str().map(str::to_owned))
        .unwrap_or_default()
}
