mod background;
mod bridge;
mod config;
mod content;
mod history;
mod runner;
mod store;
mod updates;
mod validation;

use agent_client_protocol::v2::schema as acp;
use agent_client_protocol::v2::{ConnectionTo, Responder};
use agent_client_protocol::{Agent, Client, Result, Stdio};

#[tokio::main]
async fn main() -> Result<()> {
    let bridge = bridge::Bridge::new(config::Config::from_env());
    Agent
        .v2()
        .name("claude-code-acp")
        .on_receive_request(
            async move |request: acp::InitializeRequest,
                        responder: Responder<acp::InitializeResponse>,
                        _connection: ConnectionTo<Client>| {
                responder.respond(bridge::Bridge::initialize(&request))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::NewSessionRequest,
                            responder: Responder<acp::NewSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    let response = bridge.new_session(request);
                    // A new session is idle with nothing running, which it
                    // reports once the client knows its ID.
                    let id = response.as_ref().ok().map(|new| new.session_id.to_string());
                    respond(responder, response)?;
                    if let Some(id) = id {
                        updates::notify(
                            &connection,
                            &id,
                            acp::SessionUpdate::StateUpdate(updates::idle_state()),
                        );
                    }
                    Ok(())
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::ListSessionsRequest,
                            responder: Responder<acp::ListSessionsResponse>,
                            _connection: ConnectionTo<Client>| {
                    respond(responder, bridge.list_sessions(request))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::ResumeSessionRequest,
                            responder: Responder<acp::ResumeSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    respond(responder, bridge.resume_session(request, &connection))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::CloseSessionRequest,
                            responder: Responder<acp::CloseSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    let closed = bridge.close_session(&request.session_id.to_string());
                    connection.spawn(async move {
                        closed.await;
                        responder.respond(acp::CloseSessionResponse::new())
                    })
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::SetSessionConfigOptionRequest,
                            responder: Responder<acp::SetSessionConfigOptionResponse>,
                            connection: ConnectionTo<Client>| {
                    let change = bridge.set_config(&request, connection.clone());
                    connection.spawn(async move { respond(responder, change.await) })
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::PromptRequest,
                            responder: Responder<acp::PromptResponse>,
                            connection: ConnectionTo<Client>| {
                    bridge.prompt(&request, responder, &connection)
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |notification: acp::CancelSessionNotification,
                        _connection: ConnectionTo<Client>| {
                bridge.cancel(&notification.session_id.to_string());
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}

fn respond<T: agent_client_protocol::JsonRpcResponse>(
    responder: Responder<T>,
    result: std::result::Result<T, agent_client_protocol::Error>,
) -> Result<()> {
    match result {
        Ok(response) => responder.respond(response),
        Err(error) => responder.respond_with_error(error),
    }
}
