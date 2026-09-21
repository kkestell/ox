//! The ACP connection: request handlers over one shared process state.

pub(crate) mod convert;
mod operations;
mod prompt;

use std::{
    error::Error as StdError,
    io::{self, ErrorKind},
    path::Path,
    sync::{Arc, Mutex},
};

use agent_client_protocol::{
    Agent, ConnectTo, Error, JsonRpcResponse, Responder, Result, Stdio,
    schema::ProtocolVersion,
    schema::v1::{
        AgentAuthCapabilities, AgentCapabilities, AuthMethod, AuthMethodTerminal,
        CancelNotification, DeleteSessionRequest, DeleteSessionResponse, InitializeRequest,
        InitializeResponse, ListSessionsRequest, ListSessionsResponse, LoadSessionRequest,
        LoadSessionResponse, LogoutCapabilities, LogoutRequest, LogoutResponse, NewSessionRequest,
        NewSessionResponse, PromptRequest, SessionCapabilities, SessionDeleteCapabilities,
        SessionId, SessionInfo, SessionListCapabilities, SessionNotification, SessionUpdate,
        StopReason,
    },
};

use crate::{
    auth, openrouter,
    sessions::{self, SessionStore, SessionSummary},
};
use operations::{PromptCancellation, SessionOperations};

#[derive(Clone)]
struct ServerState {
    store: SessionStore,
    openrouter: Arc<Mutex<Option<openrouter::Client>>>,
    operations: SessionOperations,
}

impl ServerState {
    fn new(store: SessionStore) -> Self {
        Self {
            store,
            openrouter: Arc::default(),
            operations: SessionOperations::default(),
        }
    }

    /// Credentials are read on first use, so the process serves listing,
    /// deletion, and terminal login before a key exists, and a key saved by
    /// `ox auth login` is picked up by the next request without a restart.
    fn openrouter_client(&self) -> Result<openrouter::Client> {
        let mut slot = self
            .openrouter
            .lock()
            .expect("OpenRouter client mutex poisoned");
        if let Some(client) = slot.as_ref() {
            return Ok(client.clone());
        }
        let api_key = auth::api_key()
            .map_err(Error::into_internal_error)?
            .ok_or_else(Error::auth_required)?;
        let client = openrouter::Client::new(api_key);
        *slot = Some(client.clone());
        Ok(client)
    }

    fn new_session(&self, request: &NewSessionRequest) -> Result<NewSessionResponse> {
        self.openrouter_client()?;
        let summary = self
            .store
            .create(&request.cwd, openrouter::DEFAULT_MODEL)
            .map_err(store_error)?;
        Ok(NewSessionResponse::new(summary.id))
    }

    fn load_session(
        &self,
        request: &LoadSessionRequest,
        send_update: impl FnMut(SessionUpdate) -> Result<()>,
    ) -> Result<LoadSessionResponse> {
        self.openrouter_client()?;
        let stored = self
            .store
            .read(&request.session_id)
            .map_err(Error::into_internal_error)?
            .ok_or_else(|| not_found(&request.session_id))?;
        if stored.summary.workspace_path.as_os_str() != request.cwd.as_os_str() {
            return Err(Error::invalid_params().data(format!(
                "session {} belongs to workspace {}, not {}",
                request.session_id,
                stored.summary.workspace_path.display(),
                request.cwd.display()
            )));
        }
        convert::replay_transcript(&stored.transcript, send_update)?;
        Ok(LoadSessionResponse::new())
    }

    fn list_sessions(&self, request: &ListSessionsRequest) -> Result<ListSessionsResponse> {
        if request
            .cursor
            .as_deref()
            .is_some_and(|cursor| !cursor.is_empty())
        {
            return Err(
                Error::invalid_params().data("session listing is not paginated; omit the cursor")
            );
        }
        let summaries = self
            .store
            .list(request.cwd.as_deref())
            .map_err(Error::into_internal_error)?;
        Ok(ListSessionsResponse::new(
            summaries.into_iter().map(session_info).collect(),
        ))
    }

    fn delete_session(&self, request: &DeleteSessionRequest) -> Result<DeleteSessionResponse> {
        self.store
            .delete(&request.session_id)
            .map_err(Error::into_internal_error)?;
        Ok(DeleteSessionResponse::new())
    }

    /// The cached client is cleared even when removing the saved key fails,
    /// and that failure is reported: the key may still load on a later request.
    fn logout(&self) -> Result<LogoutResponse> {
        let removed = auth::delete_api_key();
        self.openrouter
            .lock()
            .expect("OpenRouter client mutex poisoned")
            .take();
        removed.map_err(Error::into_internal_error)?;
        Ok(LogoutResponse::new())
    }
}

fn store_error(error: io::Error) -> Error {
    match error.kind() {
        ErrorKind::InvalidInput => Error::invalid_params().data(error.to_string()),
        _ => Error::into_internal_error(error),
    }
}

fn not_found(session_id: &SessionId) -> Error {
    Error::resource_not_found(Some(session_id.to_string()))
}

fn busy() -> Error {
    Error::invalid_request().data("session has an operation in progress")
}

fn session_info(summary: SessionSummary) -> SessionInfo {
    SessionInfo::new(summary.id, summary.workspace_path)
        .title(summary.title)
        .updated_at(summary.updated_at)
}

fn reply<T: JsonRpcResponse>(responder: Responder<T>, result: Result<T>) -> Result<()> {
    match result {
        Ok(response) => responder.respond(response),
        Err(error) => responder.respond_with_error(error),
    }
}

// TODO: Test terminal authentication with the preview version of Zed.
fn terminal_auth_method() -> AuthMethod {
    AuthMethod::Terminal(
        AuthMethodTerminal::new("openrouter", "Log in to OpenRouter")
            .description("Enter an OpenRouter API key and save it in the system keyring")
            .args(vec!["auth".to_owned(), "login".to_owned()]),
    )
}

/// The agent speaks exactly one protocol version, so the response always
/// names it; a client that needs a different one disconnects.
fn initialize_response(initialize: &InitializeRequest) -> InitializeResponse {
    let mut response = InitializeResponse::new(ProtocolVersion::LATEST).agent_capabilities(
        AgentCapabilities::new()
            .load_session(true)
            .session_capabilities(
                SessionCapabilities::new()
                    .list(SessionListCapabilities::new())
                    .delete(SessionDeleteCapabilities::new()),
            )
            .auth(AgentAuthCapabilities::new().logout(LogoutCapabilities::new())),
    );
    if initialize.client_capabilities.auth.terminal {
        response = response.auth_methods(vec![terminal_auth_method()]);
    }
    response
}

pub async fn run() -> std::result::Result<(), Box<dyn StdError>> {
    let store = SessionStore::open(&sessions::database_path()?)?;
    serve(ServerState::new(store), Stdio::new()).await?;
    Ok(())
}

pub async fn run_headless(
    workspace_path: &Path,
    user_message: String,
) -> std::result::Result<(), Box<dyn StdError>> {
    let api_key = auth::api_key()?.ok_or_else(|| {
        io::Error::new(
            ErrorKind::PermissionDenied,
            "OpenRouter authentication required; run `ox auth login`",
        )
    })?;
    let store = SessionStore::open(&sessions::database_path()?)?;
    let session = store.create(workspace_path, openrouter::DEFAULT_MODEL)?;
    run_headless_prompt(
        store,
        openrouter::Client::new(api_key),
        session.id,
        user_message,
    )
    .await
}

async fn run_headless_prompt(
    store: SessionStore,
    openrouter: openrouter::Client,
    session_id: SessionId,
    user_message: String,
) -> std::result::Result<(), Box<dyn StdError>> {
    let mut interrupt = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::interrupt())?;
    let cancellation = PromptCancellation::new();
    let run = prompt::run(
        store,
        openrouter,
        session_id,
        user_message,
        cancellation.clone(),
        |_| Ok(()),
        prompt::ToolPermissions::AutoApprove,
    );
    tokio::pin!(run);
    let response = loop {
        tokio::select! {
            biased;
            response = &mut run => break response?,
            signal = interrupt.recv() => {
                signal.expect("SIGINT listener remains open");
                cancellation.cancel();
            }
        }
    };
    if response.stop_reason != StopReason::EndTurn {
        return Err(
            io::Error::other(format!("prompt stopped with {:?}", response.stop_reason)).into(),
        );
    }
    Ok(())
}

async fn serve(state: ServerState, transport: impl ConnectTo<Agent> + 'static) -> Result<()> {
    let shutdown_operations = state.operations.clone();
    let new_state = state.clone();
    let load_state = state.clone();
    let list_state = state.clone();
    let delete_state = state.clone();
    let logout_state = state.clone();
    let prompt_state = state.clone();
    let cancel_state = state;

    Agent
        .builder()
        .name("ox")
        .on_close(async move |_connection| {
            shutdown_operations.shutdown().await;
            Ok(())
        })
        .on_receive_request(
            async move |initialize: InitializeRequest, responder, _connection| {
                responder.respond(initialize_response(&initialize))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: NewSessionRequest, responder, _connection| {
                reply(responder, new_state.new_session(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: LoadSessionRequest, responder, connection| {
                let Some(_guard) = load_state.operations.try_load(&request.session_id) else {
                    return responder.respond_with_error(busy());
                };
                let result = load_state.load_session(&request, |update| {
                    connection.send_notification(SessionNotification::new(
                        request.session_id.clone(),
                        update,
                    ))
                });
                reply(responder, result)
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: ListSessionsRequest, responder, _connection| {
                reply(responder, list_state.list_sessions(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: DeleteSessionRequest, responder, _connection| {
                let Some(_guard) = delete_state.operations.try_delete(&request.session_id) else {
                    return responder.respond_with_error(busy());
                };
                reply(responder, delete_state.delete_session(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |_request: LogoutRequest, responder, _connection| {
                reply(responder, logout_state.logout())
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: PromptRequest, responder, connection| {
                let user_message = match convert::prompt_to_user_message(&request.prompt) {
                    Ok(user_message) => user_message,
                    Err(error) => return responder.respond_with_error(error),
                };
                let openrouter = match prompt_state.openrouter_client() {
                    Ok(openrouter) => openrouter,
                    Err(error) => return responder.respond_with_error(error),
                };
                let Some((guard, cancellation)) =
                    prompt_state.operations.try_prompt(&request.session_id)
                else {
                    return responder.respond_with_error(busy());
                };
                let store = prompt_state.store.clone();
                let session_id = request.session_id;
                let task_connection = connection.clone();
                connection.spawn(async move {
                    let _guard = guard;
                    let send_update = |update| {
                        task_connection
                            .send_notification(SessionNotification::new(session_id.clone(), update))
                    };
                    let result = prompt::run(
                        store,
                        openrouter,
                        session_id.clone(),
                        user_message,
                        cancellation,
                        send_update,
                        prompt::ToolPermissions::Acp(task_connection.clone()),
                    )
                    .await;
                    reply(responder, result)
                })
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |cancel: CancelNotification, _connection| {
                cancel_state.operations.cancel(&cancel.session_id);
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(transport)
        .await
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use agent_client_protocol::schema::v1::{AuthCapabilities, ClientCapabilities, ErrorCode};

    use super::*;

    fn state() -> ServerState {
        let state = ServerState::new(SessionStore::in_memory());
        *state.openrouter.lock().unwrap() = Some(openrouter::Client::new("test-key".to_owned()));
        state
    }

    #[test]
    fn initialize_answers_with_the_supported_protocol_version() {
        let unsupported = ProtocolVersion::from(99);
        let response = initialize_response(&InitializeRequest::new(unsupported));

        assert_eq!(response.protocol_version, ProtocolVersion::V1);
    }

    #[test]
    fn terminal_login_is_advertised_only_to_supporting_clients() {
        let response = initialize_response(&InitializeRequest::new(ProtocolVersion::V1));
        assert!(response.auth_methods.is_empty());

        let initialize = InitializeRequest::new(ProtocolVersion::V1).client_capabilities(
            ClientCapabilities::new().auth(AuthCapabilities::new().terminal(true)),
        );
        let response = initialize_response(&initialize);

        assert!(response.agent_capabilities.auth.logout.is_some());
        assert!(matches!(
            &response.auth_methods[..],
            [AuthMethod::Terminal(method)]
                if method.id.to_string() == "openrouter"
                    && method.args == ["auth", "login"]
        ));
    }

    #[test]
    fn load_requires_a_matching_workspace_and_an_existing_session() {
        let state = state();
        let workspace_path = Path::new("/Users/kyle/projects/ox");
        let created = state
            .new_session(&NewSessionRequest::new(workspace_path))
            .unwrap();
        let mut updates = Vec::new();
        let mut send_update = |update| {
            updates.push(update);
            Ok(())
        };

        let missing = state
            .load_session(
                &LoadSessionRequest::new(SessionId::new("missing"), workspace_path),
                &mut send_update,
            )
            .unwrap_err();
        assert_eq!(missing.code, ErrorCode::ResourceNotFound);

        let elsewhere = state
            .load_session(
                &LoadSessionRequest::new(created.session_id.clone(), Path::new("/elsewhere")),
                &mut send_update,
            )
            .unwrap_err();
        assert_eq!(elsewhere.code, ErrorCode::InvalidParams);

        state
            .load_session(
                &LoadSessionRequest::new(created.session_id, workspace_path),
                &mut send_update,
            )
            .unwrap();
        assert!(updates.is_empty(), "a new session replays nothing");
    }

    #[test]
    fn load_and_list_use_the_same_exact_workspace_path() {
        let state = state();
        let created = state
            .new_session(&NewSessionRequest::new("/workspace/"))
            .unwrap();
        state
            .store
            .append_user(&created.session_id, "Hello")
            .unwrap();

        for workspace_path in ["/workspace/", "/workspace", "/workspace/./", "//workspace/"] {
            let mut updates = Vec::new();
            let loaded = state.load_session(
                &LoadSessionRequest::new(created.session_id.clone(), workspace_path),
                |update| {
                    updates.push(update);
                    Ok(())
                },
            );
            let listed = state
                .list_sessions(&ListSessionsRequest::new().cwd(workspace_path))
                .unwrap();
            if workspace_path == "/workspace/" {
                loaded.unwrap();
                assert_eq!(listed.sessions.len(), 1);
                assert_eq!(updates.len(), 1);
            } else {
                assert_eq!(loaded.unwrap_err().code, ErrorCode::InvalidParams);
                assert!(listed.sessions.is_empty());
                assert!(updates.is_empty(), "workspace validation precedes replay");
            }
        }
    }

    #[tokio::test]
    async fn clean_eof_cancels_the_prompt_and_drains_its_response() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        use crate::openrouter::fixture::{Reply, Server, delta};
        use crate::sessions::TranscriptEntry;

        let prefix = format!(
            "data: {}\n\n",
            delta(json!({ "content": "Provisional" }), None),
        );
        let server = Server::start(vec![Reply::Hang(prefix)]).await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let store = state.store.clone();
        let operations = state.operations.clone();
        let id = store
            .create(Path::new("/workspace"), openrouter::DEFAULT_MODEL)
            .unwrap()
            .id;
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);

        incoming_tx
            .unbounded_send(Ok(json!({
                "jsonrpc": "2.0", "id": 1, "method": "initialize",
                "params": { "protocolVersion": 1, "clientCapabilities": {} },
            })
            .to_string()))
            .unwrap();
        incoming_tx
            .unbounded_send(Ok(json!({
                "jsonrpc": "2.0", "id": 2, "method": "session/prompt",
                "params": { "sessionId": id, "prompt": [{ "type": "text", "text": "Hello" }] },
            })
            .to_string()))
            .unwrap();

        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut response = None;
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                if message["params"]["update"]["sessionUpdate"] == "agent_message_chunk" {
                    assert!(operations.try_load(&id).is_none());
                    drop(incoming_tx.take());
                }
                if message["id"] == 2 {
                    response = Some(message);
                }
            }
            assert!(incoming_tx.is_none(), "EOF was sent during inference");
            let response = response.expect("the final response was drained before shutdown");
            assert_eq!(response["result"]["stopReason"], "cancelled");
        };
        let (result, ()) = futures::join!(serve(state, transport), client);
        result.unwrap();
        assert!(
            operations.try_load(&id).is_some(),
            "the session became available"
        );
        assert_eq!(
            store.read(&id).unwrap().unwrap().transcript,
            vec![
                TranscriptEntry::Model(openrouter::DEFAULT_MODEL.to_owned()),
                TranscriptEntry::UserMessage("Hello".to_owned())
            ],
        );
    }

    async fn wait_for_file(path: &Path) {
        tokio::time::timeout(std::time::Duration::from_secs(5), async {
            while !path.exists() {
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn shell_permissions_control_execution_and_save_results() {
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        use crate::openrouter::fixture::{Server, shell_reply, text_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;

        for decision in [
            "approve",
            "deny",
            "mixed",
            "cancelled",
            "cancel",
            "eof",
            "unknown",
            "error",
        ] {
            let workspace = Workspace::new();
            let continues = matches!(decision, "approve" | "deny" | "mixed");
            let mut replies = vec![shell_reply(&[("touch first", 5), ("touch second", 5)])];
            if continues {
                replies.push(text_reply("Done"));
            }
            let server = Server::start(replies).await;
            let state = state();
            *state.openrouter.lock().unwrap() = Some(server.client());
            let store = state.store.clone();
            let operations = state.operations.clone();
            let id = store
                .create(&workspace.0, openrouter::DEFAULT_MODEL)
                .unwrap()
                .id;
            let (incoming_tx, incoming_rx) = mpsc::unbounded();
            let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
            let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
            for message in [
                json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
                json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Run commands"}]}}),
            ] {
                incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
            }
            let client = async {
                let mut incoming_tx = Some(incoming_tx);
                let mut requested = 0;
                let mut announced = Vec::new();
                let mut running = Vec::new();
                let mut response = None;
                while let Some(line) = outgoing_rx.next().await {
                    let message: Value = serde_json::from_str(&line).unwrap();
                    let update = &message["params"]["update"];
                    if update["sessionUpdate"] == "tool_call" {
                        assert!(
                            update["status"].is_null() || update["status"] == "pending",
                            "{decision}"
                        );
                        announced.push(update["toolCallId"].clone());
                    }
                    if update["status"] == "in_progress" {
                        running.push(update["toolCallId"].clone());
                    }
                    if message["method"] == "session/request_permission" {
                        let params = &message["params"];
                        assert_eq!(params["sessionId"], id.to_string(), "{decision}");
                        assert_eq!(
                            params["toolCall"]["toolCallId"],
                            format!("shell-{requested}"),
                            "{decision}"
                        );
                        assert!(
                            announced.contains(&params["toolCall"]["toolCallId"]),
                            "{decision}"
                        );
                        assert_eq!(params["toolCall"]["status"], "pending", "{decision}");
                        assert_eq!(params["toolCall"]["kind"], "execute", "{decision}");
                        let file = if requested == 0 { "first" } else { "second" };
                        assert_eq!(
                            params["toolCall"]["rawInput"]["command"],
                            format!("touch {file}"),
                            "{decision}"
                        );
                        assert_eq!(
                            params["toolCall"]["content"][0]["content"]["text"],
                            format!(
                                "Working directory: {}\n\nCommand:\n\n    touch {file}",
                                workspace.0.display()
                            ),
                            "{decision}"
                        );
                        assert!(
                            !workspace.0.join(file).exists(),
                            "{decision}: shell must await approval"
                        );
                        assert_eq!(
                            params["options"],
                            json!([
                                {"optionId":"approve","name":"Approve","kind":"allow_once"},
                                {"optionId":"deny","name":"Deny","kind":"reject_once"},
                            ]),
                            "{decision}"
                        );
                        assert!(operations.try_load(&id).is_none(), "{decision}");
                        assert!(operations.try_delete(&id).is_none(), "{decision}");
                        assert!(operations.try_prompt(&id).is_none(), "{decision}");
                        requested += 1;
                        if decision == "eof" {
                            drop(incoming_tx.take());
                            continue;
                        }
                        let reply = match decision {
                            "cancel" => {
                                json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":id}})
                            }
                            "error" => {
                                json!({"jsonrpc":"2.0","id":message["id"],"error":{"code":-32603,"message":"Permission UI failed"}})
                            }
                            _ => {
                                let outcome = if decision == "cancelled" {
                                    json!({"outcome":"cancelled"})
                                } else {
                                    let option = if decision == "mixed" {
                                        if requested == 1 { "deny" } else { "approve" }
                                    } else {
                                        decision
                                    };
                                    json!({"outcome":"selected","optionId":option})
                                };
                                json!({"jsonrpc":"2.0","id":message["id"],"result":{"outcome":outcome}})
                            }
                        };
                        incoming_tx
                            .as_ref()
                            .unwrap()
                            .unbounded_send(Ok(reply.to_string()))
                            .unwrap();
                    } else if message["id"] == 2 {
                        assert_eq!(
                            store
                                .read(&id)
                                .unwrap()
                                .unwrap()
                                .transcript
                                .iter()
                                .filter(|entry| matches!(entry, TranscriptEntry::ToolResult(_)))
                                .count(),
                            2,
                            "{decision}: batch is saved before responding"
                        );
                        response = Some(message);
                        drop(incoming_tx.take());
                    }
                }
                assert_eq!(requested, if continues { 2 } else { 1 }, "{decision}");
                assert_eq!(
                    running.len(),
                    match decision {
                        "approve" => 2,
                        "mixed" => 1,
                        _ => 0,
                    },
                    "{decision}"
                );
                let response =
                    response.unwrap_or_else(|| panic!("{decision}: missing prompt response"));
                if matches!(decision, "unknown" | "error") {
                    assert_eq!(response["error"]["code"], -32603, "{decision}");
                    if decision == "unknown" {
                        assert_eq!(
                            response["error"]["data"], "Unknown shell permission option: unknown",
                            "{decision}"
                        );
                    }
                } else {
                    assert_eq!(
                        response["result"]["stopReason"],
                        if continues { "end_turn" } else { "cancelled" },
                        "{decision}"
                    );
                }
            };
            let (result, ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
                tokio::join!(serve(state, transport), client)
            })
            .await
            .unwrap_or_else(|error| panic!("{decision}: timed out: {error}"));
            result.unwrap_or_else(|error| panic!("{decision}: ACP connection failed: {error}"));
            assert!(operations.try_load(&id).is_some(), "{decision}");
            assert_eq!(
                workspace.0.join("first").exists(),
                decision == "approve",
                "{decision}"
            );
            assert_eq!(
                workspace.0.join("second").exists(),
                matches!(decision, "approve" | "mixed"),
                "{decision}"
            );
            let transcript = store.read(&id).unwrap().unwrap().transcript;
            let results: Vec<_> = transcript
                .iter()
                .filter_map(|entry| match entry {
                    TranscriptEntry::ToolResult(result) => Some(result),
                    _ => None,
                })
                .collect();
            for (index, result) in results.iter().enumerate() {
                match decision {
                    "approve" => assert!(
                        matches!(result.outcome, ToolOutcome::Completed(_)),
                        "{decision}, result {index}"
                    ),
                    "deny" | "mixed" if decision == "deny" || index == 0 => assert_eq!(
                        result.outcome,
                        ToolOutcome::Failed(
                            "User denied permission to run this command.".to_owned()
                        ),
                        "{decision}, result {index}"
                    ),
                    "mixed" => assert!(
                        matches!(result.outcome, ToolOutcome::Completed(_)),
                        "{decision}, result {index}"
                    ),
                    "unknown" | "error" => {
                        assert!(
                            matches!(result.outcome, ToolOutcome::Failed(_)),
                            "{decision}, result {index}"
                        )
                    }
                    _ => assert!(
                        matches!(result.outcome, ToolOutcome::Cancelled(_)),
                        "{decision}, result {index}"
                    ),
                }
            }
            if continues {
                let requests = server.requests();
                assert_eq!(requests.len(), 2, "{decision}");
                for result in &results {
                    assert!(
                        requests[1]["messages"]
                            .as_array()
                            .unwrap()
                            .iter()
                            .any(|message| message["role"] == "tool"
                                && message["content"] == result.outcome.text()),
                        "{decision}"
                    );
                }
            }
            let mut replay = Vec::new();
            convert::replay_transcript(&transcript, |update| {
                replay.push(update);
                Ok(())
            })
            .unwrap();
            for result in results {
                assert!(
                    replay.iter().any(|update| matches!(update,
                    SessionUpdate::ToolCall(call) if call.tool_call_id.to_string() == result.call_id
                        && call.raw_output == Some(json!(result.outcome.text())))),
                    "{decision}"
                );
            }
        }
    }

    async fn assert_process_stopped(path: &Path, reaped: bool) {
        let pid = std::fs::read_to_string(path).unwrap();
        tokio::time::timeout(std::time::Duration::from_secs(3), async {
            loop {
                let output = tokio::process::Command::new("ps")
                    .args(["-o", "stat=", "-p", pid.trim()])
                    .output()
                    .await
                    .unwrap();
                let state = String::from_utf8(output.stdout).unwrap();
                if state.trim().is_empty() || (!reaped && state.trim().starts_with('Z')) {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
    }

    #[tokio::test]
    async fn headless_sigint_cleans_up_and_saves_even_with_repeated_signals() {
        use crate::openrouter::fixture::{Server, shell_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;
        use rustix::process::{Pid, Signal, kill_process, kill_process_group};

        const FLAG: &str = "OX_HEADLESS_SIGNAL_TEST";
        if let Some(path) = std::env::var_os(FLAG) {
            let path = Path::new(&path);
            let store = SessionStore::open(&path.join("ox.db")).unwrap();
            let session = store.create(path, openrouter::DEFAULT_MODEL).unwrap();
            let command = "echo $$ > shell; python3 -c 'import subprocess; p = subprocess.Popen([\"sleep\", \"30\"], start_new_session=True); open(\"detached\", \"w\").write(str(p.pid))'; sleep 30 & echo $! > child; printf started; touch ready; wait";
            let server =
                Server::start(vec![shell_reply(&[(command, 30), ("touch wrong", 5)])]).await;
            let response = run_headless_prompt(
                store.clone(),
                server.client(),
                session.id.clone(),
                "Run commands".into(),
            )
            .await;
            assert!(response.unwrap_err().to_string().contains("Cancelled"));
            let transcript = store.read(&session.id).unwrap().unwrap().transcript;
            assert_eq!(transcript.iter().filter(|entry| matches!(entry, TranscriptEntry::ToolResult(result) if matches!(result.outcome, ToolOutcome::Cancelled(_)))).count(), 2);
            std::fs::write(path.join("saved"), "yes").unwrap();
            return;
        }
        let workspace = Workspace::new();
        let mut child = tokio::process::Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "acp::tests::headless_sigint_cleans_up_and_saves_even_with_repeated_signals",
                "--nocapture",
            ])
            .env(FLAG, &workspace.0)
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        wait_for_file(&workspace.0.join("ready")).await;
        let pid = Pid::from_raw(child.id().unwrap() as i32).unwrap();
        kill_process(pid, Signal::INT).unwrap();
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        kill_process(pid, Signal::INT).unwrap();
        let status = tokio::time::timeout(std::time::Duration::from_secs(5), child.wait()).await;
        let detached = std::fs::read_to_string(workspace.0.join("detached"))
            .unwrap()
            .parse()
            .unwrap();
        kill_process_group(Pid::from_raw(detached).unwrap(), Signal::KILL).unwrap();
        assert!(status.unwrap().unwrap().success());
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert!(workspace.0.join("saved").exists());
        assert!(!workspace.0.join("wrong").exists());
        let store = SessionStore::open(&workspace.0.join("ox.db")).unwrap();
        let session = store.list(None).unwrap().pop().unwrap();
        assert!(store.read(&session.id).unwrap().unwrap().transcript.iter().any(|entry| matches!(entry,
            TranscriptEntry::ToolResult(result) if matches!(&result.outcome, ToolOutcome::Cancelled(text) if text.contains("started") && text.contains("partial changes")))));
    }

    #[tokio::test]
    async fn acp_shutdown_waits_for_shell_cleanup_saving_and_response() {
        use crate::openrouter::fixture::{Server, shell_reply};
        use crate::sessions::{ToolOutcome, TranscriptEntry};
        use crate::tools::fixture::Workspace;
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::{Value, json};

        let workspace = Workspace::new();
        let server = Server::start(vec![shell_reply(&[
            (
                "echo $$ > shell; sleep 30 & echo $! > child; printf started; touch ready; wait",
                30,
            ),
            ("touch wrong", 5),
        ])])
        .await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let store = state.store.clone();
        let operations = state.operations.clone();
        let id = store
            .create(&workspace.0, openrouter::DEFAULT_MODEL)
            .unwrap()
            .id;
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Run commands"}]}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        let client = async {
            let mut incoming_tx = Some(incoming_tx);
            let mut response = None;
            while let Some(line) = outgoing_rx.next().await {
                let message: Value = serde_json::from_str(&line).unwrap();
                if message["method"] == "session/request_permission" {
                    incoming_tx
                        .as_ref()
                        .unwrap()
                        .unbounded_send(Ok(json!({
                            "jsonrpc":"2.0", "id":message["id"],
                            "result":{"outcome":{"outcome":"selected","optionId":"approve"}}
                        })
                        .to_string()))
                        .unwrap();
                    wait_for_file(&workspace.0.join("ready")).await;
                    assert!(operations.try_load(&id).is_none());
                    drop(incoming_tx.take());
                } else if message["id"] == 2 {
                    assert!(
                        store
                            .read(&id)
                            .unwrap()
                            .unwrap()
                            .transcript
                            .iter()
                            .any(|entry| matches!(entry, TranscriptEntry::ToolResult(_))),
                        "saved before response"
                    );
                    response = Some(message);
                }
            }
            assert_eq!(response.unwrap()["result"]["stopReason"], "cancelled");
        };
        let (result, ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(serve(state, transport), client)
        })
        .await
        .unwrap();
        result.unwrap();
        assert!(operations.try_load(&id).is_some());
        assert_process_stopped(&workspace.0.join("shell"), true).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
        assert!(!workspace.0.join("wrong").exists());
        let transcript = store.read(&id).unwrap().unwrap().transcript;
        assert_eq!(transcript.iter().filter(|entry| matches!(entry, TranscriptEntry::ToolResult(result) if matches!(result.outcome, ToolOutcome::Cancelled(_)))).count(), 2);
    }

    #[tokio::test]
    async fn transport_error_stops_the_shell_process_group() {
        use crate::openrouter::fixture::{Server, shell_reply};
        use crate::tools::fixture::Workspace;
        use agent_client_protocol::Lines;
        use futures::{SinkExt, StreamExt, channel::mpsc};
        use serde_json::json;

        let workspace = Workspace::new();
        let server = Server::start(vec![shell_reply(&[(
            "echo $$ > shell; sleep 30 & echo $! > child; touch ready; wait",
            30,
        )])])
        .await;
        let state = state();
        *state.openrouter.lock().unwrap() = Some(server.client());
        let store = state.store.clone();
        let id = store
            .create(&workspace.0, openrouter::DEFAULT_MODEL)
            .unwrap()
            .id;
        let (incoming_tx, incoming_rx) = mpsc::unbounded();
        let (outgoing_tx, mut outgoing_rx) = mpsc::unbounded::<String>();
        let transport = Lines::new(outgoing_tx.sink_map_err(io::Error::other), incoming_rx);
        for message in [
            json!({"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":1,"clientCapabilities":{}}}),
            json!({"jsonrpc":"2.0", "id":2, "method":"session/prompt", "params":{"sessionId":id,"prompt":[{"type":"text","text":"Run commands"}]}}),
        ] {
            incoming_tx.unbounded_send(Ok(message.to_string())).unwrap();
        }
        let fail_transport = async {
            wait_for_file(&workspace.0.join("ready")).await;
            incoming_tx
                .unbounded_send(Err(io::Error::other("broken transport")))
                .unwrap();
        };
        let drain_output = async {
            while let Some(line) = outgoing_rx.next().await {
                let message: serde_json::Value = serde_json::from_str(&line).unwrap();
                if message["method"] == "session/request_permission" {
                    incoming_tx
                        .unbounded_send(Ok(json!({
                            "jsonrpc":"2.0", "id":message["id"],
                            "result":{"outcome":{"outcome":"selected","optionId":"approve"}}
                        })
                        .to_string()))
                        .unwrap();
                }
            }
        };
        let (result, (), ()) = tokio::time::timeout(std::time::Duration::from_secs(5), async {
            tokio::join!(serve(state, transport), fail_transport, drain_output)
        })
        .await
        .unwrap();
        assert!(
            result.is_err(),
            "the transport error reaches the ACP server"
        );
        assert_process_stopped(&workspace.0.join("shell"), false).await;
        assert_process_stopped(&workspace.0.join("child"), false).await;
    }

    #[test]
    fn listing_rejects_cursors_and_workspaces_must_be_absolute() {
        let state = state();
        let paged = ListSessionsRequest::new().cursor("next");
        assert_eq!(
            state.list_sessions(&paged).unwrap_err().code,
            ErrorCode::InvalidParams
        );
        assert_eq!(
            state
                .new_session(&NewSessionRequest::new("relative"))
                .unwrap_err()
                .code,
            ErrorCode::InvalidParams
        );
        state
            .delete_session(&DeleteSessionRequest::new(SessionId::new("missing")))
            .unwrap();
    }
}
