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
//!
//! With `--hang-initialize` the agent never answers `initialize`.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::Duration;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::v1::schema as acp;
use agent_client_protocol::v1::{ConnectionTo, Responder};
use agent_client_protocol::{Client, Error, Stdio};

#[derive(Default)]
struct State {
    cancelled: AtomicBool,
    next_session: AtomicU64,
}

#[tokio::main]
async fn main() -> agent_client_protocol::Result<()> {
    let state = Arc::new(State::default());
    agent_client_protocol::Agent
        .v1()
        .name("oxd-fake-agent")
        .on_receive_request(
            async move |_request: acp::InitializeRequest,
                        responder: Responder<acp::InitializeResponse>,
                        connection: ConnectionTo<Client>| {
                if std::env::args().any(|arg| arg == "--hang-initialize") {
                    return connection.spawn(async move {
                        std::future::pending::<()>().await;
                        responder.respond(acp::InitializeResponse::new(ProtocolVersion::V1))
                    });
                }
                responder.respond(acp::InitializeResponse::new(ProtocolVersion::V1))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let state = state.clone();
                async move |_request: acp::NewSessionRequest,
                            responder: Responder<acp::NewSessionResponse>,
                            _connection: ConnectionTo<Client>| {
                    let id = state.next_session.fetch_add(1, Ordering::Relaxed);
                    responder.respond(acp::NewSessionResponse::new(format!("fake-{id}")))
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
                    state.cancelled.store(false, Ordering::SeqCst);
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
                state.cancelled.store(true, Ordering::SeqCst);
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
    let session_id = request.session_id;
    let say = |text: String| {
        connection.send_notification(acp::SessionNotification::new(
            session_id.clone(),
            acp::SessionUpdate::AgentMessageChunk(acp::ContentChunk::new(acp::ContentBlock::Text(
                acp::TextContent::new(text),
            ))),
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
                    if state.cancelled.load(Ordering::SeqCst) {
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
            ["exit", code] => std::process::exit(parse(code)?),
            _ => return Err(Error::invalid_params().data(format!("unknown directive: {line}"))),
        }
    }
    if state.cancelled.load(Ordering::SeqCst) {
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
