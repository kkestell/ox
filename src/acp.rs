//! The ACP connection: request handlers over one shared process state.

pub(crate) mod convert;
mod operations;
mod prompt;

use std::{
    error::Error as StdError,
    io::{self, ErrorKind},
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
    },
};

use crate::{
    auth, openrouter,
    sessions::{self, SessionStore, SessionSummary},
};
use operations::SessionOperations;

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
            let response = response.expect("the settled response was drained before shutdown");
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
