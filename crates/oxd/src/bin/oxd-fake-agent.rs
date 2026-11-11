//! A scripted ACP agent for testing oxd.
//!
//! Each prompt's text is a script, one directive per line:
//!
//! - `say <n> <delay_ms>`: n `agent_message_chunk` updates `chunk-1..n`,
//!   spaced by the delay
//! - `pid`: one chunk containing the process id
//! - `permission`: a `session/request_permission` with allow and reject
//!   options, then a chunk `allowed`, `rejected` or `cancelled`
//! - `late-permission`: after answering the prompt, a
//!   `session/request_permission` outside any turn
//! - `exit <code>`: exit the process
//! - `describe`: one chunk listing the prompt's blocks, such as
//!   `text image:image/png link:file:///a resource:file:///b`
//! - `mode`: one chunk with the current value of the `mode` config option
//! - `wait`: keep the turn open until it is cancelled
//! - `title <text>`: a `session_info_update` setting the title to the rest of
//!   the line
//! - `title-null`: a `session_info_update` clearing the title
//! - `touch`: a `session_info_update` with only `updated_at`
//!
//! The agent accepts image and embedded resource prompts, and has one config
//! option, `mode`, with values `ask` and `code`. Setting it sends a
//! `config_option_update` before the response.
//!
//! Each session records its prompts and message chunks in
//! `fake-agent-<session id>.jsonl` in its cwd; `session/load` replays them as
//! user and agent message chunks.
//!
//! Flags:
//!
//! - `--hang-initialize`: never answer `initialize`
//! - `--text-only`: advertise no prompt capabilities
//! - `--no-load`: advertise no `loadSession` capability
//! - `--silent-config`: send no `config_option_update` when an option is set
//! - `--late-config-update`: send the `config_option_update` after the
//!   response
//! - `--hold <path>`: open `path`, a FIFO, for writing and keep it open
//!   until the process exits, so its reader sees end of file then

use std::collections::HashMap;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::schema as acp;
use agent_client_protocol::v1::{ConnectionTo, Responder};
use agent_client_protocol::{Client, Error, Stdio};
use tokio::sync::watch;

struct State {
    /// Whether the current turn was cancelled.
    cancelled: watch::Sender<bool>,
    mode: Mutex<String>,
    /// Each session's record of prompts and message chunks.
    logs: Mutex<HashMap<String, PathBuf>>,
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
}

impl State {
    fn config_options(&self) -> Vec<acp::SessionConfigOption> {
        vec![acp::SessionConfigOption::select(
            "mode",
            "Mode",
            self.mode.lock().unwrap().clone(),
            vec![
                acp::SessionConfigSelectOption::new("ask", "Ask"),
                acp::SessionConfigSelectOption::new("code", "Code"),
            ],
        )]
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
        mode: Mutex::new("ask".into()),
        logs: Mutex::default(),
    });
    agent_client_protocol::Agent
        .v1()
        .name("oxd-fake-agent")
        .on_receive_request(
            async move |_request: acp::InitializeRequest,
                        responder: Responder<acp::InitializeResponse>,
                        connection: ConnectionTo<Client>| {
                if flag("--hang-initialize") {
                    return connection.spawn(async move {
                        std::future::pending::<()>().await;
                        responder.respond(acp::InitializeResponse::new(ProtocolVersion::V1))
                    });
                }
                let mut capabilities =
                    acp::AgentCapabilities::new().load_session(!flag("--no-load"));
                if !flag("--text-only") {
                    capabilities = capabilities.prompt_capabilities(
                        acp::PromptCapabilities::new()
                            .image(true)
                            .embedded_context(true),
                    );
                }
                responder.respond(
                    acp::InitializeResponse::new(ProtocolVersion::V1)
                        .agent_capabilities(capabilities),
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
                    state.open_log(&id, &request.cwd);
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
                async move |request: acp::LoadSessionRequest,
                            responder: Responder<acp::LoadSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    let path = state.open_log(&request.session_id, &request.cwd);
                    let Ok(log) = std::fs::read_to_string(path) else {
                        return responder
                            .respond_with_error(Error::invalid_params().data("no such session"));
                    };
                    for line in log.lines() {
                        let entry: serde_json::Value = serde_json::from_str(line).unwrap();
                        let chunk = acp::ContentChunk::new(acp::ContentBlock::Text(
                            acp::TextContent::new(entry["text"].as_str().unwrap()),
                        ));
                        let update = if entry["role"] == "user" {
                            acp::SessionUpdate::UserMessageChunk(chunk)
                        } else {
                            acp::SessionUpdate::AgentMessageChunk(chunk)
                        };
                        connection.send_notification(acp::SessionNotification::new(
                            request.session_id.clone(),
                            update,
                        ))?;
                    }
                    responder.respond(
                        acp::LoadSessionResponse::new().config_options(state.config_options()),
                    )
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
                    let value = request.value.as_value_id().map(ToString::to_string);
                    match value.as_deref() {
                        Some(value @ ("ask" | "code")) if &*request.config_id.0 == "mode" => {
                            *state.mode.lock().unwrap() = value.to_owned();
                        }
                        _ => {
                            return responder.respond_with_error(
                                Error::invalid_params().data("unknown option or value"),
                            );
                        }
                    }
                    let options = state.config_options();
                    let update = acp::SessionNotification::new(
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
                    state.cancelled.send_replace(false);
                    let state = state.clone();
                    let task_connection = connection.clone();
                    connection.spawn(async move {
                        let session_id = request.session_id.clone();
                        let late_permission = script(&request)
                            .lines()
                            .any(|line| line.trim() == "late-permission");
                        match run(&state, &task_connection, request).await {
                            Ok(stop_reason) => {
                                responder.respond(acp::PromptResponse::new(stop_reason))?
                            }
                            Err(error) => responder.respond_with_error(error)?,
                        }
                        if late_permission {
                            task_connection
                                .send_request(permission_request(session_id))
                                .block_task()
                                .await?;
                        }
                        Ok(())
                    })
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |_notification: acp::CancelNotification,
                        _connection: ConnectionTo<Client>| {
                state.cancelled.send_replace(true);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}

async fn run(
    state: &State,
    connection: &ConnectionTo<Client>,
    request: acp::PromptRequest,
) -> Result<acp::StopReason, Error> {
    let script = script(&request);
    let description = describe(&request.prompt);
    let session_id = request.session_id;
    state.record(&session_id, "user", &script);
    let say = |text: String| {
        state.record(&session_id, "agent", &text);
        connection.send_notification(acp::SessionNotification::new(
            session_id.clone(),
            acp::SessionUpdate::AgentMessageChunk(acp::ContentChunk::new(acp::ContentBlock::Text(
                acp::TextContent::new(text),
            ))),
        ))
    };
    let info = |update: acp::SessionInfoUpdate| {
        connection.send_notification(acp::SessionNotification::new(
            session_id.clone(),
            acp::SessionUpdate::SessionInfoUpdate(update),
        ))
    };
    for line in script.lines() {
        let words: Vec<&str> = line.split_whitespace().collect();
        match words.as_slice() {
            [] => {}
            ["say", n, delay] => {
                let n: u64 = parse(n)?;
                let delay = Duration::from_millis(parse(delay)?);
                for i in 1..=n {
                    if *state.cancelled.borrow() {
                        return Ok(acp::StopReason::Cancelled);
                    }
                    say(format!("chunk-{i}"))?;
                    if i < n {
                        tokio::time::sleep(delay).await;
                    }
                }
            }
            ["pid"] => say(std::process::id().to_string())?,
            ["permission"] => {
                let request = permission_request(session_id.clone());
                let response = connection.send_request(request).block_task().await?;
                match response.outcome {
                    acp::RequestPermissionOutcome::Selected(selected)
                        if &*selected.option_id.0 == "allow" =>
                    {
                        say("allowed".into())?
                    }
                    acp::RequestPermissionOutcome::Selected(_) => say("rejected".into())?,
                    _ => {
                        say("cancelled".into())?;
                        return Ok(acp::StopReason::Cancelled);
                    }
                }
            }
            ["late-permission"] => {}
            ["describe"] => say(description.clone())?,
            ["mode"] => say(state.mode.lock().unwrap().clone())?,
            ["wait"] => {
                let _ = state
                    .cancelled
                    .subscribe()
                    .wait_for(|cancelled| *cancelled)
                    .await;
                return Ok(acp::StopReason::Cancelled);
            }
            ["title", ..] => {
                let title = line.trim().strip_prefix("title").unwrap().trim();
                info(acp::SessionInfoUpdate::new().title(title))?
            }
            ["title-null"] => info(acp::SessionInfoUpdate::new().title(None::<String>))?,
            ["touch"] => info(acp::SessionInfoUpdate::new().updated_at("2026-11-11T00:00:00Z"))?,
            ["exit", code] => std::process::exit(parse(code)?),
            _ => return Err(Error::invalid_params().data(format!("unknown directive: {line}"))),
        }
    }
    if *state.cancelled.borrow() {
        Ok(acp::StopReason::Cancelled)
    } else {
        Ok(acp::StopReason::EndTurn)
    }
}

fn script(request: &acp::PromptRequest) -> String {
    request
        .prompt
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
        acp::ToolCallUpdate::new(
            "fake-tool",
            acp::ToolCallUpdateFields::new().title("Fake tool"),
        ),
        vec![
            acp::PermissionOption::new("allow", "Allow", acp::PermissionOptionKind::AllowOnce),
            acp::PermissionOption::new("reject", "Reject", acp::PermissionOptionKind::RejectOnce),
        ],
    )
}

fn parse<T: std::str::FromStr>(word: &str) -> Result<T, Error> {
    word.parse()
        .map_err(|_| Error::invalid_params().data(format!("not a number: {word}")))
}
