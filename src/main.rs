use agent_client_protocol::schema::v1::{
    AgentCapabilities, CancelNotification, ContentBlock, ContentChunk, DeleteSessionRequest,
    DeleteSessionResponse, InitializeRequest, InitializeResponse, ListSessionsRequest,
    ListSessionsResponse, LoadSessionRequest, LoadSessionResponse, NewSessionRequest,
    NewSessionResponse, PromptRequest, PromptResponse, SessionCapabilities,
    SessionDeleteCapabilities, SessionId, SessionListCapabilities, SessionNotification,
    SessionUpdate, StopReason, TextContent,
};
use agent_client_protocol::{Agent, Error, Result, Stdio};

mod sessions;

#[tokio::main]
async fn main() -> Result<()> {
    Agent
        .builder()
        .name("ox")
        .on_receive_request(
            async move |initialize: InitializeRequest, responder, _connection| {
                responder.respond(
                    InitializeResponse::new(initialize.protocol_version).agent_capabilities(
                        AgentCapabilities::new()
                            .load_session(true)
                            .session_capabilities(
                                SessionCapabilities::new()
                                    .list(SessionListCapabilities::new())
                                    .delete(SessionDeleteCapabilities::new()),
                            ),
                    ),
                )
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |new_session: NewSessionRequest, responder, _connection| {
                let session_id = SessionId::new(uuid::Uuid::new_v4().to_string());
                match sessions::create_session(&new_session.cwd, &session_id) {
                    Ok(_) => responder.respond(NewSessionResponse::new(session_id)),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |load_session: LoadSessionRequest, responder, _connection| {
                if sessions::session_exists(&load_session.cwd, &load_session.session_id) {
                    responder.respond(LoadSessionResponse::new())
                } else {
                    responder.respond_with_error(Error::resource_not_found(Some(
                        load_session.session_id.to_string(),
                    )))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |list_sessions: ListSessionsRequest, responder, _connection| {
                match sessions::list_sessions(list_sessions.cwd.as_deref()) {
                    Ok(sessions) => responder.respond(ListSessionsResponse::new(sessions)),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |delete_session: DeleteSessionRequest, responder, _connection| {
                match sessions::delete_session(&delete_session.session_id) {
                    Ok(true) => responder.respond(DeleteSessionResponse::new()),
                    Ok(false) => responder.respond_with_error(Error::resource_not_found(Some(
                        delete_session.session_id.to_string(),
                    ))),
                    Err(err) => responder.respond_with_error(Error::into_internal_error(err)),
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |prompt: PromptRequest, responder, connection| {
                let title = prompt.prompt.iter().find_map(|block| match block {
                    ContentBlock::Text(text) => sessions::title_from_prompt(&text.text),
                    _ => None,
                });
                const REPLY: &str = "Hello from ox! This is a hard-coded stub response.";
                let record = (|| {
                    sessions::record_activity(&prompt.session_id, title)?;
                    sessions::append_event(
                        &prompt.session_id,
                        "user_message",
                        &serde_json::json!({ "content": prompt.prompt }),
                    )?;
                    sessions::append_event(
                        &prompt.session_id,
                        "agent_message",
                        &serde_json::json!({ "text": REPLY }),
                    )
                })();
                if let Err(err) = record {
                    return responder.respond_with_error(Error::into_internal_error(err));
                }
                connection.send_notification(SessionNotification::new(
                    prompt.session_id,
                    SessionUpdate::AgentMessageChunk(ContentChunk::new(ContentBlock::Text(
                        TextContent::new(REPLY),
                    ))),
                ))?;
                responder.respond(PromptResponse::new(StopReason::EndTurn))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |_cancel: CancelNotification, _connection| Ok(()),
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}
