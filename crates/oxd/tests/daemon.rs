#![cfg(unix)]

use std::collections::BTreeMap;
use std::io::{BufRead, BufReader as StdBufReader};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};

use ox_protocol::schema::{ContentBlock, RequestPermissionOutcome, SessionUpdate, StopReason};
use ox_protocol::{Client, Error, Event, EventKind, SessionInfo, SessionState, Subscription, code};
use serde_json::{Value, json};
use tempfile::TempDir;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::time::{Duration, Instant, sleep, timeout};

const TIMEOUT: Duration = Duration::from_secs(10);
const OXD: &str = env!("CARGO_BIN_EXE_oxd");
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_oxd-fake-agent");

struct Daemon {
    child: Child,
    dir: TempDir,
    socket: PathBuf,
}

impl Daemon {
    async fn start(idle_exit_ms: u64) -> Self {
        Self::start_with(idle_exit_ms, &[], None).await
    }

    /// Starts oxd with extra arguments and, optionally, a limit on open
    /// file descriptors.
    async fn start_with(idle_exit_ms: u64, extra: &[&str], fd_limit: Option<u32>) -> Self {
        let dir = tempfile::tempdir().unwrap();
        let socket = dir.path().join("oxd.sock");
        let mut command = match fd_limit {
            Some(limit) => {
                let mut command = Command::new("sh");
                command.args([
                    "-c",
                    &format!("ulimit -n {limit} && exec \"$0\" \"$@\""),
                    OXD,
                ]);
                command
            }
            None => Command::new(OXD),
        };
        let child = command
            .arg("--socket")
            .arg(&socket)
            .arg("--idle-exit-ms")
            .arg(idle_exit_ms.to_string())
            .args(extra)
            .spawn()
            .unwrap();
        let daemon = Self { child, dir, socket };
        let deadline = Instant::now() + TIMEOUT;
        while UnixStream::connect(&daemon.socket).await.is_err() {
            assert!(Instant::now() < deadline, "oxd did not start listening");
            sleep(Duration::from_millis(10)).await;
        }
        daemon
    }

    async fn client(&self) -> Client {
        timeout(TIMEOUT, Client::connect(&self.socket))
            .await
            .expect("timed out connecting")
            .unwrap()
    }

    async fn new_session(&self, client: &Client) -> SessionInfo {
        let session = client
            .new_session(
                vec![FAKE_AGENT.into()],
                self.dir.path().canonicalize().unwrap(),
                BTreeMap::new(),
            )
            .await
            .unwrap();
        assert_eq!(session.state, SessionState::Idle);
        session
    }

    async fn wait_for_exit(&mut self) -> std::process::ExitStatus {
        let deadline = Instant::now() + TIMEOUT;
        loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                return status;
            }
            assert!(Instant::now() < deadline, "oxd did not exit");
            sleep(Duration::from_millis(20)).await;
        }
    }
}

impl Drop for Daemon {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

async fn next(subscription: &mut Subscription) -> Event {
    timeout(TIMEOUT, subscription.next())
        .await
        .expect("timed out waiting for an event")
        .expect("subscription ended")
}

/// Events up to and including the first one matching `last`.
async fn until(subscription: &mut Subscription, last: impl Fn(&EventKind) -> bool) -> Vec<Event> {
    let mut events = Vec::new();
    loop {
        let event = next(subscription).await;
        let done = last(&event.kind);
        events.push(event);
        if done {
            return events;
        }
    }
}

fn turn_ended(kind: &EventKind) -> bool {
    matches!(kind, EventKind::TurnEnded { .. })
}

fn chunk(event: &Event) -> Option<&str> {
    match &event.kind {
        EventKind::Update(SessionUpdate::AgentMessageChunk(chunk)) => match &chunk.content {
            ContentBlock::Text(text) => Some(&text.text),
            _ => None,
        },
        _ => None,
    }
}

fn chunks(events: &[Event]) -> Vec<&str> {
    events.iter().filter_map(chunk).collect()
}

fn assert_contiguous(events: &[Event]) {
    let seqs: Vec<u64> = events.iter().map(|event| event.seq).collect();
    let expected: Vec<u64> = (1..=events.len() as u64).collect();
    assert_eq!(seqs, expected);
}

async fn wait_for_state(client: &Client, session_id: &str, state: SessionState) {
    let deadline = Instant::now() + TIMEOUT;
    loop {
        let sessions = client.list().await.unwrap();
        let session = sessions.iter().find(|s| s.id == session_id).unwrap();
        if session.state == state {
            return;
        }
        assert!(
            Instant::now() < deadline,
            "session stayed {:?}",
            session.state
        );
        sleep(Duration::from_millis(20)).await;
    }
}

fn remote_code(error: Error) -> i32 {
    match error {
        Error::Remote(body) => body.code,
        other => panic!("expected a daemon error, got {other}"),
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn reattach_mid_turn() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, "pid\nsay 20 50").await.unwrap();
    let mut seen = Vec::new();
    for _ in 0..5 {
        seen.push(next(&mut events).await);
    }
    drop(events);
    drop(client);

    let client = daemon.client().await;
    let mut events = client
        .subscribe(&session.id, seen.last().unwrap().seq)
        .await
        .unwrap();
    seen.extend(until(&mut events, turn_ended).await);
    assert_contiguous(&seen);
    assert_eq!(seen.len(), 23);
    let pid = chunk(&seen[1]).unwrap().to_owned();
    assert_eq!(
        chunks(&seen[2..]),
        (1..=20).map(|i| format!("chunk-{i}")).collect::<Vec<_>>()
    );

    client.prompt(&session.id, "pid").await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_eq!(chunks(&turn), [pid.as_str()]);
}

#[tokio::test(flavor = "multi_thread")]
async fn turn_completes_without_a_client() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    client.prompt(&session.id, "say 5 20").await.unwrap();
    drop(client);
    sleep(Duration::from_millis(500)).await;

    let client = daemon.client().await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_contiguous(&turn);
    assert!(matches!(turn[0].kind, EventKind::PromptStarted { ref text } if text == "say 5 20"));
    assert_eq!(
        chunks(&turn),
        ["chunk-1", "chunk-2", "chunk-3", "chunk-4", "chunk-5"]
    );
    assert!(matches!(
        turn.last().unwrap().kind,
        EventKind::TurnEnded {
            stop_reason: Some(StopReason::EndTurn),
            ..
        }
    ));
}

#[tokio::test(flavor = "multi_thread")]
async fn permission_across_disconnect() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    client.prompt(&session.id, "permission").await.unwrap();
    drop(client);

    let client = daemon.client().await;
    wait_for_state(&client, &session.id, SessionState::AwaitingPermission).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let requested = until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    let EventKind::PermissionRequested { request_id, .. } = &requested.last().unwrap().kind else {
        unreachable!()
    };
    client
        .permit(&session.id, request_id, "allow")
        .await
        .unwrap();

    let rest = until(&mut events, turn_ended).await;
    assert!(matches!(
        &rest[0].kind,
        EventKind::PermissionResolved {
            outcome: RequestPermissionOutcome::Selected(selected),
            ..
        } if &*selected.option_id.0 == "allow"
    ));
    assert_eq!(chunks(&rest), ["allowed"]);

    let error = client
        .permit(&session.id, request_id, "allow")
        .await
        .unwrap_err();
    assert_eq!(remote_code(error), code::INVALID_STATE);
}

#[tokio::test(flavor = "multi_thread")]
async fn cancel_with_pending_permission() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, "permission").await.unwrap();
    until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    client.cancel(&session.id).await.unwrap();

    let rest = until(&mut events, turn_ended).await;
    assert!(matches!(
        rest[0].kind,
        EventKind::PermissionResolved {
            outcome: RequestPermissionOutcome::Cancelled,
            ..
        }
    ));
    assert_eq!(chunks(&rest), ["cancelled"]);
    assert!(matches!(
        rest.last().unwrap().kind,
        EventKind::TurnEnded {
            stop_reason: Some(StopReason::Cancelled),
            ..
        }
    ));
}

/// Run by `killed_client` in a child process: subscribes to a session and
/// prints one `event <seq>` line per event.
#[tokio::test(flavor = "multi_thread")]
#[ignore = "child process of killed_client"]
async fn attach_child() {
    let (Some(socket), Some(session)) = (
        std::env::var_os("OXD_TEST_SOCKET"),
        std::env::var("OXD_TEST_SESSION").ok(),
    ) else {
        return;
    };
    let client = Client::connect(Path::new(&socket)).await.unwrap();
    let mut events = client.subscribe(&session, 0).await.unwrap();
    while let Some(event) = events.next().await {
        println!("event {}", event.seq);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn killed_client() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    client.prompt(&session.id, "say 20 50").await.unwrap();

    let mut child = Command::new(std::env::current_exe().unwrap())
        .args(["attach_child", "--exact", "--ignored", "--nocapture"])
        .env("OXD_TEST_SOCKET", &daemon.socket)
        .env("OXD_TEST_SESSION", &session.id)
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let stdout = child.stdout.take().unwrap();
    let seen = tokio::task::spawn_blocking(move || {
        StdBufReader::new(stdout)
            .lines()
            .map_while(Result::ok)
            .filter(|line| line.starts_with("event "))
            .take(3)
            .count()
    });
    assert_eq!(timeout(TIMEOUT, seen).await.unwrap().unwrap(), 3);
    child.kill().unwrap();
    child.wait().unwrap();

    let client = daemon.client().await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_contiguous(&turn);
    assert_eq!(chunks(&turn).len(), 20);
}

#[tokio::test(flavor = "multi_thread")]
async fn agent_exit() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    client.prompt(&session.id, "exit 1").await.unwrap();
    wait_for_state(&client, &session.id, SessionState::Exited).await;

    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let history = until(&mut events, |kind| {
        matches!(kind, EventKind::AgentExited { .. })
    })
    .await;
    assert_contiguous(&history);
    assert!(matches!(history[0].kind, EventKind::PromptStarted { .. }));
    let error = client.prompt(&session.id, "pid").await.unwrap_err();
    assert_eq!(remote_code(error), code::INVALID_STATE);

    client.close(&session.id).await.unwrap();
    assert!(client.list().await.unwrap().is_empty());
    let error = client.subscribe(&session.id, 0).await.unwrap_err();
    assert_eq!(remote_code(error), code::NOT_FOUND);
}

#[tokio::test(flavor = "multi_thread")]
async fn lifetime() {
    let mut daemon = Daemon::start(300).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    drop(client);
    sleep(Duration::from_millis(1000)).await;
    assert!(
        daemon.child.try_wait().unwrap().is_none(),
        "oxd exited with a live session"
    );
    assert!(daemon.socket.exists());

    let client = daemon.client().await;
    client.close(&session.id).await.unwrap();
    drop(client);
    assert!(daemon.wait_for_exit().await.success());
    assert!(!daemon.socket.exists());
}

#[tokio::test(flavor = "multi_thread")]
async fn single_instance_and_on_demand_start() {
    let dir = tempfile::tempdir().unwrap();
    let socket = dir.path().join("oxd.sock");
    let client = timeout(
        TIMEOUT,
        Client::connect_or_start_with(&socket, Path::new(OXD)),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(client.list().await.unwrap().is_empty());

    let status = Command::new(OXD)
        .arg("--socket")
        .arg(&socket)
        .status()
        .unwrap();
    assert!(status.success());

    assert!(client.list().await.unwrap().is_empty());
    let second = Client::connect(&socket).await.unwrap();
    assert!(second.list().await.unwrap().is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn version_mismatch() {
    let daemon = Daemon::start(1000).await;
    let stream = UnixStream::connect(&daemon.socket).await.unwrap();
    let (read, mut write) = stream.into_split();
    let mut lines = BufReader::new(read).lines();
    let mut send = async |message: Value| {
        write
            .write_all(format!("{message}\n").as_bytes())
            .await
            .unwrap();
        let line = timeout(TIMEOUT, lines.next_line())
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        serde_json::from_str::<Value>(&line).unwrap()
    };

    let response = send(json!({"id": 1, "method": "session.list"})).await;
    assert_eq!(response["error"]["code"], code::INVALID_REQUEST);
    let response =
        send(json!({"id": 2, "method": "hello", "params": {"protocol_version": 999}})).await;
    assert_eq!(response["id"], 2);
    assert_eq!(response["error"]["code"], code::PROTOCOL_MISMATCH);
}

fn alive(pid: i32) -> bool {
    // SAFETY: signal 0 only checks that the process exists.
    unsafe { libc::kill(pid, 0) == 0 }
}

async fn wait_until_dead(pid: i32) {
    let deadline = Instant::now() + TIMEOUT;
    while alive(pid) {
        assert!(Instant::now() < deadline, "process {pid} is still alive");
        sleep(Duration::from_millis(20)).await;
    }
}

async fn agent_pid(client: &Client, session_id: &str) -> i32 {
    let mut events = client.subscribe(session_id, 0).await.unwrap();
    client.prompt(session_id, "pid").await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    chunks(&turn)[0].parse().unwrap()
}

#[tokio::test(flavor = "multi_thread")]
async fn resubscribe_on_one_connection_delivers_each_event_once() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, "say 3000 0").await.unwrap();
    let total = until(&mut events, turn_ended).await.len();
    for _ in 0..5 {
        let mut first = client.subscribe(&session.id, 0).await.unwrap();
        let mut second = client.subscribe(&session.id, 0).await.unwrap();
        let replay = until(&mut second, turn_ended).await;
        assert_eq!(replay.len(), total);
        assert_contiguous(&replay);
        // The replaced subscription ends.
        timeout(TIMEOUT, async { while first.next().await.is_some() {} })
            .await
            .expect("replaced subscription did not end");
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn close_during_startup_fails_new_session() {
    let mut daemon = Daemon::start(300).await;
    let client = std::sync::Arc::new(daemon.client().await);
    let starting = tokio::spawn({
        let client = client.clone();
        let cwd = daemon.dir.path().canonicalize().unwrap();
        async move {
            client
                .new_session(
                    vec![FAKE_AGENT.into(), "--hang-initialize".into()],
                    cwd,
                    BTreeMap::new(),
                )
                .await
        }
    });
    let deadline = Instant::now() + TIMEOUT;
    let session_id = loop {
        if let Some(session) = client.list().await.unwrap().pop() {
            assert_eq!(session.state, SessionState::Starting);
            break session.id;
        }
        assert!(Instant::now() < deadline, "session never appeared");
        sleep(Duration::from_millis(20)).await;
    };
    client.close(&session_id).await.unwrap();
    let error = timeout(TIMEOUT, starting)
        .await
        .unwrap()
        .unwrap()
        .unwrap_err();
    assert_eq!(remote_code(error), code::AGENT);
    drop(client);
    assert!(daemon.wait_for_exit().await.success());
}

#[tokio::test(flavor = "multi_thread")]
async fn agent_that_never_starts_times_out() {
    let daemon = Daemon::start_with(1000, &["--startup-timeout-ms", "300"], None).await;
    let client = daemon.client().await;
    let error = timeout(
        TIMEOUT,
        client.new_session(
            vec![FAKE_AGENT.into(), "--hang-initialize".into()],
            daemon.dir.path().canonicalize().unwrap(),
            BTreeMap::new(),
        ),
    )
    .await
    .unwrap()
    .unwrap_err();
    assert_eq!(remote_code(error), code::AGENT);
    assert!(client.list().await.unwrap().is_empty());
    // The connection kept serving while the agent was starting.
    assert!(client.list().await.is_ok());
}

#[tokio::test(flavor = "multi_thread")]
async fn close_ends_other_clients_subscriptions() {
    let daemon = Daemon::start(1000).await;
    let watcher = daemon.client().await;
    let session = daemon.new_session(&watcher).await;
    let mut events = watcher.subscribe(&session.id, 0).await.unwrap();
    let closer = daemon.client().await;
    closer.close(&session.id).await.unwrap();
    let last = next(&mut events).await;
    assert!(matches!(last.kind, EventKind::SessionClosed));
    assert!(
        timeout(TIMEOUT, events.next())
            .await
            .expect("subscription did not end")
            .is_none()
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn permission_after_turn_returns_session_to_idle() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.prompt(&session.id, "late-permission").await.unwrap();
    let history = until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    assert!(history.iter().any(|event| turn_ended(&event.kind)));
    wait_for_state(&client, &session.id, SessionState::AwaitingPermission).await;
    let EventKind::PermissionRequested { request_id, .. } = &history.last().unwrap().kind else {
        unreachable!()
    };
    client
        .permit(&session.id, request_id, "allow")
        .await
        .unwrap();
    wait_for_state(&client, &session.id, SessionState::Idle).await;
    client.prompt(&session.id, "pid").await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn exhausted_descriptors_do_not_stop_the_daemon() {
    let mut daemon = Daemon::start_with(1000, &[], Some(64)).await;
    let mut streams = Vec::new();
    for _ in 0..100 {
        if let Ok(stream) = UnixStream::connect(&daemon.socket).await {
            streams.push(stream);
        }
    }
    sleep(Duration::from_millis(300)).await;
    drop(streams);
    sleep(Duration::from_millis(300)).await;
    assert!(daemon.child.try_wait().unwrap().is_none(), "oxd exited");
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    client.close(&session.id).await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn sigterm_stops_agents_and_removes_the_socket() {
    let mut daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let pid = agent_pid(&client, &session.id).await;
    drop(client);
    // SAFETY: sends SIGTERM to the daemon this test started.
    unsafe { libc::kill(daemon.child.id() as i32, libc::SIGTERM) };
    assert!(daemon.wait_for_exit().await.success());
    assert!(!daemon.socket.exists());
    wait_until_dead(pid).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn close_stops_the_agent() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = daemon.new_session(&client).await;
    let pid = agent_pid(&client, &session.id).await;
    client.close(&session.id).await.unwrap();
    wait_until_dead(pid).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn socket_is_private() {
    use std::os::unix::fs::PermissionsExt;
    let daemon = Daemon::start(1000).await;
    let mode = std::fs::metadata(&daemon.socket)
        .unwrap()
        .permissions()
        .mode();
    assert_eq!(mode & 0o777, 0o600);
}
