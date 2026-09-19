mod acp;
mod agent;
mod sessions;

#[tokio::main]
async fn main() -> agent_client_protocol::Result<()> {
    acp::run().await
}
