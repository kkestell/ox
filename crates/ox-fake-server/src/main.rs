use agent_client_protocol::{ConnectTo, Stdio};
use ox_fake_server::{Hold, SavedSessions, fake_server};

/// The fake server over stdin and stdout, for the ACP client to launch from the
/// config file. Nothing releases its `hold` script. Its saved sessions last as
/// long as the process, or, given a file as its argument, is kept in that file.
#[tokio::main]
async fn main() -> agent_client_protocol::Result<()> {
    let saved_sessions = match std::env::args_os().nth(1) {
        Some(file) => SavedSessions::file(file.into()),
        None => SavedSessions::default(),
    };
    let flags: Vec<_> = std::env::args().skip(2).collect();
    saved_sessions.capabilities(
        !flags.iter().any(|flag| flag == "--no-image"),
        !flags.iter().any(|flag| flag == "--no-delete"),
    );
    fake_server(Hold::default(), saved_sessions)
        .connect_to(Stdio::new())
        .await
}
