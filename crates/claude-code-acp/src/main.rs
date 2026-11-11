mod bridge;
mod config;
mod content;
mod history;
mod runner;
mod store;
mod updates;
mod validation;

use agent_client_protocol::v1::schema as acp;
use agent_client_protocol::v1::{ConnectionTo, Responder};
use agent_client_protocol::{Agent, Client, Result, Stdio};

#[tokio::main]
async fn main() -> Result<()> {
    let bridge = bridge::Bridge::new(config::Config::from_env());
    Agent
        .v1()
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
                            _connection: ConnectionTo<Client>| {
                    respond(responder, bridge.new_session(request))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::LoadSessionRequest,
                            responder: Responder<acp::LoadSessionResponse>,
                            connection: ConnectionTo<Client>| {
                    respond(responder, bridge.load_session(request, &connection))
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
                            _connection: ConnectionTo<Client>| {
                    respond(responder, bridge.resume_session(request))
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
                    respond(responder, bridge.set_config(&request, &connection))
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |_request: acp::AuthenticateRequest,
                        responder: Responder<acp::AuthenticateResponse>,
                        _connection: ConnectionTo<Client>| {
                responder.respond(acp::AuthenticateResponse::new())
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            {
                let bridge = bridge.clone();
                async move |request: acp::PromptRequest,
                            responder: Responder<acp::PromptResponse>,
                            connection: ConnectionTo<Client>| {
                    let turn = bridge.prompt(&request, connection.clone());
                    connection.spawn(async move { respond(responder, turn.await) })
                }
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |notification: acp::CancelNotification,
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
