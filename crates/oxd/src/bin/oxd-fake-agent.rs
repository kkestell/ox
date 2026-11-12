//! A scripted ACP v2 agent for testing oxd.
//!
//! Each prompt's text is a script, one directive per line. The agent
//! answers the prompt when it inserts it, echoing it as a `user_message`,
//! then runs the script as a turn: `running`, the directives' output, and
//! `idle` with a stop reason.
//!
//! - `say <n> <delay_ms>`: n `agent_message_chunk` updates `chunk-1..n` of
//!   one message, spaced by the delay
//! - `pid`: one chunk containing the process id
//! - `permission`: `requires_action` and a `session/request_permission` with
//!   allow and reject options, then `running` and a chunk `allowed`,
//!   `rejected` or `cancelled`
//! - `late-permission`: after the turn ends, a `session/request_permission`
//!   outside any turn
//! - `own-turn`: after the turn ends, a turn the agent starts: it says it is
//!   running, sends the chunk `own-turn`, and says it is idle once it is
//!   cancelled
//! - `exit <code>`: exit the process
//! - `describe`: one chunk listing the prompt's blocks, such as
//!   `text image:image/png link:file:///a resource:file:///b`
//! - `config <id>`: one chunk with the current value of the config option
//!   `id`
//! - `wait`: keep the turn open until it is cancelled
//! - `title <text>`: a `session_info_update` setting the title to the rest of
//!   the line
//! - `title-null`: a `session_info_update` clearing the title
//! - `touch`: a `session_info_update` with only `updated_at`
//! - `tool <text>`: a tool call `tool-<n>` titled `Fake tool` followed by
//!   the rest of the line as a code span fenced with two backticks, with the
//!   input `{"text": <text>}`, then its completion with the rest of the line
//!   and a 1×1 PNG as its content
//! - `fail <text>`: end the turn with the stop reason `error` and the rest
//!   of the line as its message
//! - `reject <text>`: answer the prompt with an error carrying the rest of
//!   the line, which means the agent did not insert it
//! - `deferred-reject`: report `running` and `idle` without inserting the
//!   prompt, and answer it with an error only when the next prompt arrives
//!
//! The agent accepts image and embedded resource prompts, and has three
//! select config options: `mode`, with values `ask` and `code`; `model`, in
//! the `model` category, with values `fast` and `smart`; and `effort`, in the
//! `thought_level` category, with values `low`, `medium`, and `high`. Setting
//! one sends a `config_option_update` before the response.
//!
//! Each session records its prompts and message chunks in
//! `fake-agent-<session id>.jsonl` in its cwd; `session/resume` with
//! `replayFrom: start` replays them as user and agent messages.
//!
//! Flags:
//!
//! - `--hang-initialize`: never answer `initialize`
//! - `--text-only`: advertise no prompt capabilities
//! - `--no-sessions`: advertise no session capabilities
//! - `--busy-on-resume`: report `running` when a session resumes, and
//!   `idle` with the stop reason `cancelled` once it is cancelled
//! - `--turn-after-resume`: after answering `session/resume`, run a turn of
//!   its own: `running`, the chunk `after-resume`, and `idle`
//! - `--silent-config`: send no `config_option_update` when an option is set
//! - `--late-config-update`: send the `config_option_update` after the
//!   response
//! - `--hold <path>`: open `path`, a FIFO, for writing and keep it open
//!   until the process exits, so its reader sees end of file then

use std::collections::HashMap;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v2::schema as acp;
use agent_client_protocol::v2::{ConnectionTo, Responder};
use agent_client_protocol::{Client, Error, Stdio};
use tokio::sync::watch;

/// A select config option: its ID, name, category, values by ID and name,
/// and the value it starts with.
struct ConfigOption {
    id: &'static str,
    name: &'static str,
    category: Option<fn() -> acp::SessionConfigOptionCategory>,
    values: &'static [(&'static str, &'static str)],
    initial: &'static str,
}

const CONFIG: [ConfigOption; 3] = [
    ConfigOption {
        id: "model",
        name: "Model",
        category: Some(|| acp::SessionConfigOptionCategory::Model),
        values: &[("fast", "Fast"), ("smart", "Smart")],
        initial: "smart",
    },
    ConfigOption {
        id: "effort",
        name: "Effort",
        category: Some(|| acp::SessionConfigOptionCategory::ThoughtLevel),
        values: &[("low", "Low"), ("medium", "Medium"), ("high", "High")],
        initial: "medium",
    },
    ConfigOption {
        id: "mode",
        name: "Mode",
        category: None,
        values: &[("ask", "Ask"), ("code", "Code")],
        initial: "ask",
    },
];

struct State {
    /// Whether the current turn was cancelled.
    cancelled: watch::Sender<bool>,
    /// The current value of each config option, by ID.
    config: Mutex<HashMap<&'static str, String>>,
    /// Each session's record of prompts and message chunks.
    logs: Mutex<HashMap<String, PathBuf>>,
    /// The number of tool calls made, which numbers the next.
    tools: AtomicU64,
    deferred_rejection: Mutex<Option<Responder<acp::PromptResponse>>>,
}

impl State {
    fn open_log(&self, session_id: &acp::SessionId, cwd: &Path) -> PathBuf {
        let path = cwd.join(format!("fake-agent-{session_id}.jsonl"));
        self.logs
            .lock()
            .unwrap()
            .insert(session_id.to_string(), path.clone());
        path
    }

    fn record(&self, session_id: &acp::SessionId, role: &str, text: &str) {
        let Some(path) = self
            .logs
            .lock()
            .unwrap()
            .get(&session_id.to_string())
            .cloned()
        else {
            return;
        };
        let mut log = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)
            .expect("open the session log");
        writeln!(log, "{}", serde_json::json!({"role": role, "text": text}))
            .expect("write the session log");
    }

    fn config_options(&self) -> Vec<acp::SessionConfigOption> {
        let config = self.config.lock().unwrap();
        CONFIG
            .iter()
            .map(|option| {
                let select = acp::SessionConfigOption::select(
                    option.id,
                    option.name,
                    config[option.id].clone(),
                    option
                        .values
                        .iter()
                        .map(|(value, name)| acp::SessionConfigSelectOption::new(*value, *name))
                        .collect::<Vec<_>>(),
                );
                match option.category {
                    Some(category) => select.category(category()),
                    None => select,
                }
            })
            .collect()
    }

    /// Sets config option `id` to `value`, if it has that value.
    fn set_config(&self, id: &str, value: &str) -> bool {
        let Some(option) = CONFIG.iter().find(|option| option.id == id) else {
            return false;
        };
        let known = option
            .values
            .iter()
            .any(|(candidate, _)| *candidate == value);
        if known {
            self.config
                .lock()
                .unwrap()
                .insert(option.id, value.to_owned());
        }
        known
    }

    /// Waits until the current turn is cancelled.
    async fn until_cancelled(&self) {
        let _ = self
            .cancelled
            .subscribe()
            .wait_for(|cancelled| *cancelled)
            .await;
    }
}

fn flag(name: &str) -> bool {
    std::env::args().any(|arg| arg == name)
}

fn value(name: &str) -> Option<String> {
    let mut args = std::env::args().skip_while(|arg| arg != name);
    args.next();
    args.next()
}

fn message_id() -> acp::MessageId {
    acp::MessageId::new(uuid::Uuid::new_v4().to_string())
}

fn notify(
    connection: &ConnectionTo<Client>,
    session_id: &acp::SessionId,
    update: acp::SessionUpdate,
) -> agent_client_protocol::Result<()> {
    connection.send_notification(acp::UpdateSessionNotification::new(
        session_id.clone(),
        update,
    ))
}

fn state_update(
    connection: &ConnectionTo<Client>,
    session_id: &acp::SessionId,
    state: acp::StateUpdate,
) -> agent_client_protocol::Result<()> {
    notify(
        connection,
        session_id,
        acp::SessionUpdate::StateUpdate(state),
    )
}

fn running() -> acp::StateUpdate {
    acp::StateUpdate::Running(acp::RunningStateUpdate::new())
}

fn idle(stop_reason: acp::StopReason) -> acp::StateUpdate {
    acp::StateUpdate::Idle(acp::IdleStateUpdate::new().stop_reason(stop_reason))
}

fn message_chunk(id: &acp::MessageId, text: impl Into<String>) -> acp::SessionUpdate {
    acp::SessionUpdate::AgentMessageChunk(acp::ContentChunk::new(
        acp::ContentBlock::Text(acp::TextContent::new(text)),
        id.clone(),
    ))
}

#[tokio::main]
async fn main() -> agent_client_protocol::Result<()> {
    let _held = value("--hold").map(|path| {
        std::fs::OpenOptions::new()
            .write(true)
            .open(path)
            .expect("open the --hold FIFO")
    });
    let state = Arc::new(State {
        cancelled: watch::Sender::new(false),
        config: Mutex::new(
            CONFIG
                .iter()
                .map(|option| (option.id, option.initial.to_owned()))
                .collect(),
        ),
        logs: Mutex::default(),
        tools: AtomicU64::new(0),
        deferred_rejection: Mutex::new(None),
    });
    agent_client_protocol::Agent
        .v2()
        .name("oxd-fake-agent")
        .on_receive_request(
            async move |_request: acp::InitializeRequest,
                        responder: Responder<acp::InitializeResponse>,
                        connection: ConnectionTo<Client>| {
                let info = acp::Implementation::new("oxd-fake-agent", env!("CARGO_PKG_VERSION"));
                if flag("--hang-initialize") {
                    return connection.spawn(async move {
                        std::future::pending::<()>().await;
                        responder.respond(acp::InitializeResponse::new(ProtocolVersion::V2, info))
                    });
                }
                let mut capabilities = acp::AgentCapabilities::new();
                if !flag("--no-sessions") {
                    let mut session = acp::SessionCapabilities::new();
                    if !flag("--text-only") {
                        session = session.prompt(
                            acp::PromptCapabilities::new()
                                .image(acp::PromptImageCapabilities::new())
                                .embedded_context(acp::PromptEmbeddedContextCapabilities::new()),
                        );
                    }
                    capabilities = capabilities.session(session);
                }
                responder.respond(
                    acp::InitializeResponse::new(ProtocolVersion::V2, info)
                        .capabilities(capabilities),
                )
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |request: acp::NewSessionRequest,
                            responder: Responder<acp::NewSessionResponse>,
                            _connection: ConnectionTo<Client>| {
                    let id = acp::SessionId::new(format!("fake-{}", uuid::Uuid::new_v4()));
                    state.open_log(&id, &request.cwd.0);
                    responder.respond(
                        acp::NewSessionResponse::new(id).config_options(state.config_options()),
                    )
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |_request: acp::ListSessionsRequest,
                            responder: Responder<acp::ListSessionsResponse>,
                            _connection: ConnectionTo<Client>| {
                    let sessions = state
                        .logs
                        .lock()
                        .unwrap()
                        .iter()
                        .map(|(id, log)| {
                            acp::SessionInfo::new(id.as_str(), log.parent().unwrap_or(log))
                        })
                        .collect();
                    responder.respond(acp::ListSessionsResponse::new(sessions))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |request: acp::ResumeSessionRequest,
                            responder: Responder<acp::ResumeSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    let replay = match request.replay_from {
                        None => false,
                        Some(acp::ReplayFrom::Start(_)) => true,
                        Some(_) => {
                            return responder.respond_with_error(
                                Error::invalid_params().data("unsupported replay cursor"),
                            );
                        }
                    };
                    let path = state.open_log(&request.session_id, &request.cwd.0);
                    let Ok(log) = std::fs::read_to_string(path) else {
                        return responder
                            .respond_with_error(Error::invalid_params().data("no such session"));
                    };
                    let session_id = request.session_id;
                    for line in log.lines().filter(|_| replay) {
                        let entry: serde_json::Value = serde_json::from_str(line).unwrap();
                        let text = entry["text"].as_str().unwrap();
                        let update = if entry["role"] == "user" {
                            acp::SessionUpdate::UserMessage(
                                acp::UserMessage::new(message_id()).content(vec![
                                    acp::ContentBlock::Text(acp::TextContent::new(text)),
                                ]),
                            )
                        } else {
                            message_chunk(&message_id(), text)
                        };
                        notify(&connection, &session_id, update)?;
                    }
                    let busy = flag("--busy-on-resume");
                    state_update(
                        &connection,
                        &session_id,
                        if busy {
                            running()
                        } else {
                            acp::StateUpdate::Idle(acp::IdleStateUpdate::new())
                        },
                    )?;
                    responder.respond(
                        acp::ResumeSessionResponse::new().config_options(state.config_options()),
                    )?;
                    if flag("--turn-after-resume") {
                        state_update(&connection, &session_id, running())?;
                        notify(
                            &connection,
                            &session_id,
                            message_chunk(&message_id(), "after-resume"),
                        )?;
                        state_update(&connection, &session_id, idle(acp::StopReason::EndTurn))?;
                    }
                    if busy {
                        let state = state.clone();
                        let task_connection = connection.clone();
                        connection.spawn(async move {
                            state.until_cancelled().await;
                            state_update(
                                &task_connection,
                                &session_id,
                                idle(acp::StopReason::Cancelled),
                            )
                        })?;
                    }
                    Ok(())
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |_request: acp::CloseSessionRequest,
                            responder: Responder<acp::CloseSessionResponse>,
                            _connection: ConnectionTo<Client>| {
                    state.cancelled.send_replace(true);
                    responder.respond(acp::CloseSessionResponse::new())
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |request: acp::SetSessionConfigOptionRequest,
                            responder: Responder<acp::SetSessionConfigOptionResponse>,
                            connection: ConnectionTo<Client>| {
                    let value = request.value.as_id().map(ToString::to_string);
                    if !value.is_some_and(|value| state.set_config(&request.config_id.0, &value)) {
                        return responder.respond_with_error(
                            Error::invalid_params().data("unknown option or value"),
                        );
                    }
                    let options = state.config_options();
                    let update = acp::UpdateSessionNotification::new(
                        request.session_id,
                        acp::SessionUpdate::ConfigOptionUpdate(acp::ConfigOptionUpdate::new(
                            options.clone(),
                        )),
                    );
                    if flag("--silent-config") {
                        responder.respond(acp::SetSessionConfigOptionResponse::new(options))
                    } else if flag("--late-config-update") {
                        responder.respond(acp::SetSessionConfigOptionResponse::new(options))?;
                        connection.send_notification(update)
                    } else {
                        connection.send_notification(update)?;
                        responder.respond(acp::SetSessionConfigOptionResponse::new(options))
                    }
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |request: acp::PromptRequest,
                            responder: Responder<acp::PromptResponse>,
                            connection: ConnectionTo<Client>| {
                    // A prompt the agent cannot run is answered with an
                    // error: it was not inserted.
                    let script = script(&request.prompt);
                    if script == "deferred-reject" {
                        state_update(&connection, &request.session_id, running())?;
                        state_update(
                            &connection,
                            &request.session_id,
                            idle(acp::StopReason::EndTurn),
                        )?;
                        *state.deferred_rejection.lock().unwrap() = Some(responder);
                        return Ok(());
                    }
                    let deferred = state.deferred_rejection.lock().unwrap().take();
                    if let Some(previous) = deferred {
                        previous.respond_with_error(
                            Error::invalid_params().data("previous prompt was not inserted"),
                        )?;
                    }
                    let directives = match parse(&script) {
                        Ok(directives) => directives,
                        Err(error) => return responder.respond_with_error(error),
                    };
                    state.cancelled.send_replace(false);
                    state.record(&request.session_id, "user", &script);
                    let id = message_id();
                    notify(
                        &connection,
                        &request.session_id,
                        acp::SessionUpdate::UserMessage(
                            acp::UserMessage::new(id.clone()).content(request.prompt.clone()),
                        ),
                    )?;
                    responder.respond(acp::PromptResponse::new(id))?;
                    let state = state.clone();
                    let task_connection = connection.clone();
                    connection.spawn(async move {
                        turn(&state, &task_connection, request, directives).await
                    })
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |_notification: acp::CancelSessionNotification,
                        _connection: ConnectionTo<Client>| {
                state.cancelled.send_replace(true);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}

enum Directive {
    Say { count: u64, delay: Duration },
    Pid,
    Permission,
    LatePermission,
    OwnTurn,
    Describe,
    Config(String),
    Wait,
    Title(String),
    TitleNull,
    Touch,
    Tool(String),
    Exit(i32),
    Fail(String),
}

/// The directives of a script, or the error that answers its prompt.
fn parse(script: &str) -> Result<Vec<Directive>, Error> {
    let mut directives = Vec::new();
    for line in script.lines() {
        let line = line.trim();
        let rest = |name: &str| line.strip_prefix(name).unwrap().trim().to_owned();
        let words: Vec<&str> = line.split_whitespace().collect();
        directives.push(match words.as_slice() {
            [] => continue,
            ["say", count, delay] => Directive::Say {
                count: parse_number(count)?,
                delay: Duration::from_millis(parse_number(delay)?),
            },
            ["pid"] => Directive::Pid,
            ["permission"] => Directive::Permission,
            ["late-permission"] => Directive::LatePermission,
            ["own-turn"] => Directive::OwnTurn,
            ["describe"] => Directive::Describe,
            ["config", id] => Directive::Config((*id).to_owned()),
            ["wait"] => Directive::Wait,
            ["title", ..] => Directive::Title(rest("title")),
            ["title-null"] => Directive::TitleNull,
            ["touch"] => Directive::Touch,
            ["tool", ..] => Directive::Tool(rest("tool")),
            ["exit", code] => Directive::Exit(parse_number(code)?),
            ["fail", ..] => Directive::Fail(rest("fail")),
            ["reject", ..] => return Err(Error::invalid_params().data(rest("reject"))),
            _ => return Err(Error::invalid_params().data(format!("unknown directive: {line}"))),
        });
    }
    Ok(directives)
}

/// Runs a prompt's turn after the agent answered it: `running`, the
/// directives, `idle`, and then the turns and requests that follow it.
async fn turn(
    state: &State,
    connection: &ConnectionTo<Client>,
    request: acp::PromptRequest,
    directives: Vec<Directive>,
) -> agent_client_protocol::Result<()> {
    let session_id = request.session_id.clone();
    let late_permission = directives
        .iter()
        .any(|directive| matches!(directive, Directive::LatePermission));
    let own_turn = directives
        .iter()
        .any(|directive| matches!(directive, Directive::OwnTurn));
    state_update(connection, &session_id, running())?;
    let stop_reason = run(state, connection, request, directives).await?;
    state_update(connection, &session_id, idle(stop_reason))?;
    if late_permission {
        connection
            .send_request(permission_request(session_id.clone()))
            .block_task()
            .await?;
    }
    if own_turn {
        state_update(connection, &session_id, running())?;
        notify(
            connection,
            &session_id,
            message_chunk(&message_id(), "own-turn"),
        )?;
        state.until_cancelled().await;
        state_update(connection, &session_id, idle(acp::StopReason::Cancelled))?;
    }
    Ok(())
}

async fn run(
    state: &State,
    connection: &ConnectionTo<Client>,
    request: acp::PromptRequest,
    directives: Vec<Directive>,
) -> agent_client_protocol::Result<acp::StopReason> {
    let description = describe(&request.prompt);
    let session_id = request.session_id;
    // A turn's chunks are one message.
    let message = message_id();
    let say = |text: String| {
        state.record(&session_id, "agent", &text);
        notify(connection, &session_id, message_chunk(&message, text))
    };
    let info = |update: acp::SessionInfoUpdate| {
        notify(
            connection,
            &session_id,
            acp::SessionUpdate::SessionInfoUpdate(update),
        )
    };
    for directive in directives {
        match directive {
            Directive::Say { count, delay } => {
                for i in 1..=count {
                    if *state.cancelled.borrow() {
                        return Ok(acp::StopReason::Cancelled);
                    }
                    say(format!("chunk-{i}"))?;
                    if i < count {
                        tokio::time::sleep(delay).await;
                    }
                }
            }
            Directive::Pid => say(std::process::id().to_string())?,
            Directive::Permission => {
                state_update(
                    connection,
                    &session_id,
                    acp::StateUpdate::RequiresAction(acp::RequiresActionStateUpdate::new()),
                )?;
                let response = connection
                    .send_request(permission_request(session_id.clone()))
                    .block_task()
                    .await?;
                state_update(connection, &session_id, running())?;
                match response.outcome {
                    acp::RequestPermissionOutcome::Selected(selected)
                        if &*selected.option_id.0 == "allow" =>
                    {
                        say("allowed".into())?;
                    }
                    acp::RequestPermissionOutcome::Selected(_) => say("rejected".into())?,
                    _ => {
                        say("cancelled".into())?;
                        return Ok(acp::StopReason::Cancelled);
                    }
                }
            }
            Directive::LatePermission | Directive::OwnTurn => {}
            Directive::Describe => say(description.clone())?,
            Directive::Config(id) => {
                let value = state.config.lock().unwrap().get(id.as_str()).cloned();
                say(value.unwrap_or_else(|| format!("no config option {id}")))?;
            }
            Directive::Wait => {
                state.until_cancelled().await;
                return Ok(acp::StopReason::Cancelled);
            }
            Directive::Title(title) => info(acp::SessionInfoUpdate::new().title(title))?,
            Directive::TitleNull => info(acp::SessionInfoUpdate::new().title(None::<String>))?,
            Directive::Touch => {
                info(acp::SessionInfoUpdate::new().updated_at("2026-11-11T00:00:00Z"))?;
            }
            Directive::Tool(output) => {
                let id = format!("tool-{}", state.tools.fetch_add(1, Ordering::Relaxed) + 1);
                let update = |update: acp::ToolCallUpdate| {
                    notify(
                        connection,
                        &session_id,
                        acp::SessionUpdate::ToolCallUpdate(update),
                    )
                };
                update(
                    acp::ToolCallUpdate::new(id.as_str())
                        .name("fake_tool")
                        .title(format!("Fake tool `` {output} ``"))
                        .status(acp::ToolCallStatus::Pending)
                        .raw_input(serde_json::json!({ "text": output })),
                )?;
                update(
                    acp::ToolCallUpdate::new(id.as_str())
                        .status(acp::ToolCallStatus::Completed)
                        .content(vec![
                            acp::ToolCallContent::from(acp::ContentBlock::Text(
                                acp::TextContent::new(output),
                            )),
                            acp::ToolCallContent::from(acp::ContentBlock::Image(
                                acp::ImageContent::new(PIXEL, "image/png"),
                            )),
                        ]),
                )?;
            }
            Directive::Exit(code) => std::process::exit(code),
            Directive::Fail(message) => {
                return Ok(acp::StopReason::Error(
                    acp::ErrorStopReason::new().error(acp::Error::new(-32603, message)),
                ));
            }
        }
    }
    if *state.cancelled.borrow() {
        Ok(acp::StopReason::Cancelled)
    } else {
        Ok(acp::StopReason::EndTurn)
    }
}

/// A 1×1 PNG.
const PIXEL: &str =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";

fn script(prompt: &[acp::ContentBlock]) -> String {
    prompt
        .iter()
        .filter_map(|block| match block {
            acp::ContentBlock::Text(text) => Some(text.text.as_str()),
            _ => None,
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn describe(prompt: &[acp::ContentBlock]) -> String {
    prompt
        .iter()
        .map(|block| match block {
            acp::ContentBlock::Text(_) => "text".to_owned(),
            acp::ContentBlock::Image(image) => format!("image:{}", image.mime_type),
            acp::ContentBlock::Audio(audio) => format!("audio:{}", audio.mime_type),
            acp::ContentBlock::ResourceLink(link) => format!("link:{}", link.uri),
            acp::ContentBlock::Resource(resource) => match &resource.resource {
                acp::EmbeddedResourceResource::TextResourceContents(text) => {
                    format!("resource:{}", text.uri)
                }
                acp::EmbeddedResourceResource::BlobResourceContents(blob) => {
                    format!("resource:{}", blob.uri)
                }
                _ => "resource".to_owned(),
            },
            _ => "unknown".to_owned(),
        })
        .collect::<Vec<_>>()
        .join(" ")
}

fn permission_request(session_id: acp::SessionId) -> acp::RequestPermissionRequest {
    acp::RequestPermissionRequest::new(
        session_id,
        "Fake tool",
        vec![
            acp::PermissionOption::new("allow", "Allow", acp::PermissionOptionKind::AllowOnce),
            acp::PermissionOption::new("reject", "Reject", acp::PermissionOptionKind::RejectOnce),
        ],
    )
    .subject(acp::RequestPermissionSubject::from(
        acp::ToolCallPermissionSubject::new(
            acp::ToolCallUpdate::new("fake-tool").title("Fake tool"),
        ),
    ))
}

fn parse_number<T: std::str::FromStr>(word: &str) -> Result<T, Error> {
    word.parse()
        .map_err(|_| Error::invalid_params().data(format!("not a number: {word}")))
}
