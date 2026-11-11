#![cfg(unix)]

use std::collections::BTreeMap;
use std::io::Read;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::atomic::{AtomicU64, Ordering};

use ox_protocol::schema::{
    AudioContent, ContentBlock, EmbeddedResource, EmbeddedResourceResource, ImageContent,
    RequestPermissionOutcome, ResourceLink, SessionConfigKind, SessionConfigOption, SessionUpdate,
    StopReason, TextContent, TextResourceContents,
};
use ox_protocol::{
    Client, ConfigValue, End, Error, Event, EventKind, PROTOCOL_VERSION, SessionInfo, SessionState,
    SessionsWatch, Subscription, code,
};
use serde_json::{Value, json};
use tempfile::TempDir;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader, Lines};
use tokio::net::UnixStream;
use tokio::net::unix::{OwnedReadHalf, OwnedWriteHalf};
use tokio::process::{Child, ChildStderr, Command};
use tokio::sync::oneshot;
use tokio::task::JoinHandle;
use tokio::time::{Duration, timeout};

const TIMEOUT: Duration = Duration::from_secs(10);
const OXD: &str = env!("CARGO_BIN_EXE_oxd");
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_oxd-fake-agent");

struct Daemon {
    child: Child,
    stderr: Lines<BufReader<ChildStderr>>,
    dir: TempDir,
    socket: PathBuf,
}

impl Daemon {
    async fn start(idle_exit_ms: u64) -> Self {
        Self::start_with(idle_exit_ms, None).await
    }

    /// Starts oxd, optionally with a limit on open file descriptors, and
    /// waits for it to report that it is ready.
    async fn start_with(idle_exit_ms: u64, fd_limit: Option<u32>) -> Self {
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
        let mut child = command
            .arg("--socket")
            .arg(&socket)
            .arg("--idle-exit-ms")
            .arg(idle_exit_ms.to_string())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        let mut stdout = BufReader::new(child.stdout.take().unwrap()).lines();
        let ready = timeout(TIMEOUT, stdout.next_line()).await;
        assert_eq!(ready.unwrap().unwrap().as_deref(), Some("ready"));
        let stderr = BufReader::new(child.stderr.take().unwrap()).lines();
        Self {
            child,
            stderr,
            dir,
            socket,
        }
    }

    async fn client(&self) -> Client {
        timeout(TIMEOUT, Client::connect(&self.socket))
            .await
            .expect("timed out connecting")
            .unwrap()
    }

    async fn new_session(&self, client: &Client) -> (SessionInfo, u64) {
        self.new_session_with(client, &[]).await
    }

    /// Creates a session whose fake agent gets `args`, waits for the agent
    /// to start, and returns the session and the seq of its `agent_started`
    /// event.
    async fn new_session_with(&self, client: &Client, args: &[&str]) -> (SessionInfo, u64) {
        let command = std::iter::once(FAKE_AGENT)
            .chain(args.iter().copied())
            .map(String::from)
            .collect();
        let session = client
            .new_session(command, self.cwd(), BTreeMap::new())
            .await
            .unwrap();
        assert_eq!(session.state, SessionState::Starting);
        let mut events = client.subscribe(&session.id, 0).await.unwrap();
        let started = next(&mut events).await;
        assert!(
            matches!(started.kind, EventKind::AgentStarted { .. }),
            "{started:?}"
        );
        let session = info(client, &session.id).await;
        assert_eq!(session.state, SessionState::Idle);
        (session, started.seq)
    }

    fn cwd(&self) -> PathBuf {
        self.dir.path().canonicalize().unwrap()
    }

    async fn wait_for_exit(&mut self) -> std::process::ExitStatus {
        timeout(TIMEOUT, self.child.wait())
            .await
            .expect("oxd did not exit")
            .unwrap()
    }

    /// Waits for a line of oxd's stderr that contains `text`.
    async fn wait_for_log(&mut self, text: &str) {
        loop {
            let line = timeout(TIMEOUT, self.stderr.next_line())
                .await
                .unwrap_or_else(|_| panic!("oxd never logged {text:?}"))
                .unwrap()
                .expect("oxd closed stderr");
            if line.contains(text) {
                return;
            }
        }
    }
}

async fn info(client: &Client, session_id: &str) -> SessionInfo {
    client
        .list()
        .await
        .unwrap()
        .into_iter()
        .find(|session| session.id == session_id)
        .unwrap()
}

/// A FIFO that a fake agent started with `--hold <path>` keeps open, which
/// tells when the agent has started and when it has exited.
struct ExitWatch {
    path: PathBuf,
    opened: Option<oneshot::Receiver<()>>,
    done: Option<JoinHandle<()>>,
}

impl ExitWatch {
    fn new(dir: &Path) -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let path = dir.join(format!("hold-{}", NEXT.fetch_add(1, Ordering::Relaxed)));
        let status = std::process::Command::new("mkfifo")
            .arg(&path)
            .status()
            .unwrap();
        assert!(status.success());
        let (opened, opened_rx) = oneshot::channel();
        let reader = path.clone();
        // Opening blocks until the agent opens the FIFO; reading ends once
        // every process holding it has exited.
        let done = tokio::task::spawn_blocking(move || {
            let mut fifo = std::fs::File::open(reader).unwrap();
            let _ = opened.send(());
            let mut rest = Vec::new();
            fifo.read_to_end(&mut rest).unwrap();
        });
        Self {
            path,
            opened: Some(opened_rx),
            done: Some(done),
        }
    }

    fn arg(&self) -> [&str; 2] {
        ["--hold", self.path.to_str().unwrap()]
    }

    /// Waits for the agent process to start.
    async fn started(&mut self) {
        let opened = self.opened.take().unwrap();
        timeout(TIMEOUT, opened)
            .await
            .expect("the agent did not start")
            .unwrap();
    }

    /// Waits for the agent process to exit.
    async fn exited(mut self) {
        let done = self.done.take().unwrap();
        timeout(TIMEOUT, done)
            .await
            .expect("the agent is still running")
            .unwrap();
    }
}

impl Drop for ExitWatch {
    /// Ends the blocking reader if no agent ever opened the FIFO, so a
    /// failing test does not hang.
    fn drop(&mut self) {
        let _ = std::fs::OpenOptions::new()
            .write(true)
            .custom_flags(libc::O_NONBLOCK)
            .open(&self.path);
    }
}

/// A connection that speaks the wire protocol itself and reads only when
/// asked to.
struct Raw {
    lines: Lines<BufReader<OwnedReadHalf>>,
    write: OwnedWriteHalf,
    next_id: u64,
}

impl Raw {
    async fn connect(daemon: &Daemon) -> Self {
        let stream = UnixStream::connect(&daemon.socket).await.unwrap();
        let (read, write) = stream.into_split();
        let mut raw = Self {
            lines: BufReader::new(read).lines(),
            write,
            next_id: 1,
        };
        raw.send("hello", json!({"protocol_version": PROTOCOL_VERSION}))
            .await;
        raw
    }

    /// Sends a request and returns its id.
    async fn send(&mut self, method: &str, params: Value) -> u64 {
        let id = self.next_id;
        self.next_id += 1;
        let request = json!({"id": id, "method": method, "params": params});
        self.write
            .write_all(format!("{request}\n").as_bytes())
            .await
            .unwrap();
        id
    }

    async fn read(&mut self) -> Value {
        let line = timeout(TIMEOUT, self.lines.next_line())
            .await
            .expect("timed out waiting for a message")
            .unwrap()
            .expect("the daemon closed the connection");
        serde_json::from_str(&line).unwrap()
    }

    /// Reads until the answer to request `id`.
    async fn answer(&mut self, id: u64) -> Value {
        loop {
            let message = self.read().await;
            if message["id"] == id {
                return message;
            }
        }
    }

    /// Reads until `last` matches a message, and returns every message read.
    async fn messages_until(&mut self, last: impl Fn(&Value) -> bool) -> Vec<Value> {
        let mut messages = Vec::new();
        loop {
            let message = self.read().await;
            let done = last(&message);
            messages.push(message);
            if done {
                return messages;
            }
        }
    }

    /// Reads until `last` matches an event message, and returns every event
    /// message read.
    async fn events_until(&mut self, last: impl Fn(&Value) -> bool) -> Vec<Value> {
        let mut events = Vec::new();
        loop {
            let message = self.read().await;
            if message["event"].is_object() {
                let done = last(&message);
                events.push(message);
                if done {
                    return events;
                }
            }
        }
    }
}

fn raw_turn_ended(message: &Value) -> bool {
    message["event"]["kind"]["type"] == "turn_ended"
}

async fn next(subscription: &mut Subscription) -> Event {
    timeout(TIMEOUT, subscription.next())
        .await
        .expect("timed out waiting for an event")
        .expect("subscription ended")
}

/// Why the subscription has no more events.
async fn end(subscription: &mut Subscription) -> End {
    timeout(TIMEOUT, subscription.next())
        .await
        .expect("subscription did not end")
        .expect_err("subscription did not end")
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

/// Lists from a sessions watch up to and including the first one matching
/// `last`.
async fn lists_until(
    watch: &mut SessionsWatch,
    is_last: impl Fn(&[SessionInfo]) -> bool,
) -> Vec<Vec<SessionInfo>> {
    let mut lists = Vec::new();
    loop {
        let list = timeout(TIMEOUT, watch.next())
            .await
            .expect("timed out waiting for a sessions list")
            .expect("the sessions watch ended");
        let done = is_last(&list);
        lists.push(list);
        if done {
            return lists;
        }
    }
}

/// The state of session `id` in a sessions list, if it is listed.
fn state_in(list: &[SessionInfo], id: &str) -> Option<SessionState> {
    list.iter()
        .find(|session| session.id == id)
        .map(|session| session.state)
}

/// A prompt of one text block.
fn text(text: &str) -> Vec<ContentBlock> {
    vec![ContentBlock::Text(TextContent::new(text))]
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

/// Asserts that the events' seqs increase by one from `first`.
fn assert_contiguous(events: &[Event], first: u64) {
    let seqs: Vec<u64> = events.iter().map(|event| event.seq).collect();
    let expected: Vec<u64> = (first..first + events.len() as u64).collect();
    assert_eq!(seqs, expected);
}

/// The current value of a select config option.
fn current(options: &[SessionConfigOption], id: &str) -> String {
    let option = options
        .iter()
        .find(|option| &*option.id.0 == id)
        .unwrap_or_else(|| panic!("no config option {id}"));
    match &option.kind {
        SessionConfigKind::Select(select) => select.current_value.to_string(),
        other => panic!("config option {id} is {other:?}"),
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
    let (session, started) = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client
        .prompt(&session.id, text("pid\nsay 20 50"))
        .await
        .unwrap();
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
    assert_contiguous(&seen, started + 1);
    assert_eq!(seen.len(), 23);
    let pid = chunk(&seen[1]).unwrap().to_owned();
    assert_eq!(
        chunks(&seen[2..]),
        (1..=20).map(|i| format!("chunk-{i}")).collect::<Vec<_>>()
    );

    client.prompt(&session.id, text("pid")).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_eq!(chunks(&turn), [pid.as_str()]);
}

#[tokio::test(flavor = "multi_thread")]
async fn turn_continues_after_its_client_disconnects() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    client.prompt(&session.id, text("say 5 20")).await.unwrap();
    drop(client);

    let client = daemon.client().await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_contiguous(&turn, started + 1);
    assert_eq!(
        turn[0].kind,
        EventKind::PromptStarted {
            prompt: text("say 5 20")
        }
    );
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
    let (session, _) = daemon.new_session(&client).await;
    client
        .prompt(&session.id, text("permission"))
        .await
        .unwrap();
    drop(client);

    let client = daemon.client().await;
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let requested = until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    assert_eq!(
        info(&client, &session.id).await.state,
        SessionState::AwaitingPermission
    );
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
    let (session, started) = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client
        .prompt(&session.id, text("permission"))
        .await
        .unwrap();
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

/// A client that vanishes mid-stream, leaving events unread in its socket,
/// does not disturb the daemon or the turn.
#[tokio::test(flavor = "multi_thread")]
async fn vanished_client() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;

    let mut raw = Raw::connect(&daemon).await;
    raw.send("session.subscribe", json!({"session_id": session.id}))
        .await;
    client.prompt(&session.id, text("say 20 50")).await.unwrap();
    raw.events_until(|message| message["event"]["seq"] == 3)
        .await;
    drop(raw);

    let mut events = client.subscribe(&session.id, started).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_contiguous(&turn, started + 1);
    assert_eq!(chunks(&turn).len(), 20);
}

#[tokio::test(flavor = "multi_thread")]
async fn agent_exit() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client.prompt(&session.id, text("exit 1")).await.unwrap();
    let history = until(&mut events, |kind| {
        matches!(kind, EventKind::AgentExited { .. })
    })
    .await;
    assert_eq!(info(&client, &session.id).await.state, SessionState::Exited);
    assert_contiguous(&history, started + 1);
    assert!(matches!(history[0].kind, EventKind::PromptStarted { .. }));
    assert!(turn_ended(&history[history.len() - 2].kind));
    let error = client.prompt(&session.id, text("pid")).await.unwrap_err();
    assert_eq!(remote_code(error), code::INVALID_STATE);

    client.close(&session.id).await.unwrap();
    assert!(client.list().await.unwrap().is_empty());
    let error = client.subscribe(&session.id, 0).await.unwrap_err();
    assert_eq!(remote_code(error), code::NOT_FOUND);
}

#[tokio::test(flavor = "multi_thread")]
async fn agent_that_cannot_start() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let session = client
        .new_session(
            vec![daemon.cwd().join("missing").to_string_lossy().into()],
            daemon.cwd(),
            BTreeMap::new(),
        )
        .await
        .unwrap();
    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    let exited = next(&mut events).await;
    assert!(
        matches!(exited.kind, EventKind::AgentExited { error: Some(_) }),
        "{exited:?}"
    );
    assert_eq!(info(&client, &session.id).await.state, SessionState::Exited);
}

#[tokio::test(flavor = "multi_thread")]
async fn lifetime() {
    let mut daemon = Daemon::start(300).await;
    let client = daemon.client().await;
    let (session, _) = daemon.new_session(&client).await;
    drop(client);
    assert!(
        timeout(Duration::from_secs(1), daemon.child.wait())
            .await
            .is_err(),
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

    let second = std::process::Command::new(OXD)
        .arg("--socket")
        .arg(&socket)
        .output()
        .unwrap();
    assert!(second.status.success());
    assert_eq!(second.stdout, b"ready\n");

    assert!(client.list().await.unwrap().is_empty());
    let other = Client::connect(&socket).await.unwrap();
    assert!(other.list().await.unwrap().is_empty());
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

#[tokio::test(flavor = "multi_thread")]
async fn subscriptions_to_one_session_are_independent() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut all = client.subscribe(&session.id, 0).await.unwrap();
    let mut live = client.subscribe(&session.id, started).await.unwrap();
    client.prompt(&session.id, text("say 3 0")).await.unwrap();
    let turn = until(&mut live, turn_ended).await;
    assert_contiguous(&turn, started + 1);
    let history = until(&mut all, turn_ended).await;
    assert_contiguous(&history, 1);
    assert_eq!(history[usize::try_from(started).unwrap()..], turn);

    let last_seq = turn.last().unwrap().seq;
    let error = client
        .subscribe(&session.id, last_seq + 1)
        .await
        .unwrap_err();
    assert_eq!(remote_code(error), code::INVALID_REQUEST);
}

#[tokio::test(flavor = "multi_thread")]
async fn unsubscribe_ends_a_subscription() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut raw = Raw::connect(&daemon).await;
    let first = raw
        .send("session.subscribe", json!({"session_id": session.id}))
        .await;
    raw.events_until(|message| message["event"]["seq"] == started)
        .await;
    let unsubscribe = raw
        .send("unsubscribe", json!({"subscription": first}))
        .await;
    raw.answer(unsubscribe).await;
    let second = raw
        .send(
            "session.subscribe",
            json!({"session_id": session.id, "after_seq": started}),
        )
        .await;
    client.prompt(&session.id, text("say 3 0")).await.unwrap();
    let turn = raw.events_until(raw_turn_ended).await;
    assert_eq!(turn.len(), 5);
    for message in turn {
        assert_eq!(message["subscription"], second);
    }
}

/// A subscriber that reads nothing while a session records more events than
/// the daemon and the socket buffer for it receives every event once it
/// reads.
#[tokio::test(flavor = "multi_thread")]
async fn slow_subscriber_receives_every_event() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut raw = Raw::connect(&daemon).await;
    raw.send(
        "session.subscribe",
        json!({"session_id": session.id, "after_seq": started}),
    )
    .await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client
        .prompt(&session.id, text("say 5000 0"))
        .await
        .unwrap();
    let turn = until(&mut events, turn_ended).await;

    let seqs: Vec<u64> = raw
        .events_until(raw_turn_ended)
        .await
        .iter()
        .map(|message| message["event"]["seq"].as_u64().unwrap())
        .collect();
    assert_eq!(seqs, turn.iter().map(|event| event.seq).collect::<Vec<_>>());
}

#[tokio::test(flavor = "multi_thread")]
async fn close_while_starting() {
    let mut daemon = Daemon::start(300).await;
    let client = daemon.client().await;
    let mut agent = ExitWatch::new(daemon.dir.path());
    let mut command = vec![FAKE_AGENT.to_owned(), "--hang-initialize".to_owned()];
    command.extend(agent.arg().map(String::from));
    let session = client
        .new_session(command, daemon.cwd(), BTreeMap::new())
        .await
        .unwrap();
    assert_eq!(session.state, SessionState::Starting);
    // The agent runs and never answers `initialize`.
    agent.started().await;
    assert_eq!(
        info(&client, &session.id).await.state,
        SessionState::Starting
    );

    let mut events = client.subscribe(&session.id, 0).await.unwrap();
    client.close(&session.id).await.unwrap();
    assert!(matches!(
        next(&mut events).await.kind,
        EventKind::SessionClosed
    ));
    agent.exited().await;
    drop(client);
    assert!(daemon.wait_for_exit().await.success());
}

#[tokio::test(flavor = "multi_thread")]
async fn close_ends_other_clients_subscriptions() {
    let daemon = Daemon::start(1000).await;
    let watcher = daemon.client().await;
    let (session, started) = daemon.new_session(&watcher).await;
    let mut events = watcher.subscribe(&session.id, started).await.unwrap();
    let closer = daemon.client().await;
    closer.close(&session.id).await.unwrap();
    let last = next(&mut events).await;
    assert!(matches!(last.kind, EventKind::SessionClosed));
    assert_eq!(end(&mut events).await, End::SessionClosed);
}

#[tokio::test(flavor = "multi_thread")]
async fn close_ends_the_turn_and_its_permission_request() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client
        .prompt(&session.id, text("permission"))
        .await
        .unwrap();
    until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    client.close(&session.id).await.unwrap();
    let rest = until(&mut events, |kind| matches!(kind, EventKind::SessionClosed)).await;
    assert_eq!(rest.len(), 3, "{rest:?}");
    assert!(matches!(
        rest[0].kind,
        EventKind::PermissionResolved {
            outcome: RequestPermissionOutcome::Cancelled,
            ..
        }
    ));
    assert!(matches!(
        rest[1].kind,
        EventKind::TurnEnded {
            stop_reason: None,
            error: Some(_)
        }
    ));
    assert_eq!(end(&mut events).await, End::SessionClosed);
}

#[tokio::test(flavor = "multi_thread")]
async fn permission_after_turn_returns_session_to_idle() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client
        .prompt(&session.id, text("late-permission"))
        .await
        .unwrap();
    let history = until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    assert!(history.iter().any(|event| turn_ended(&event.kind)));
    assert_eq!(
        info(&client, &session.id).await.state,
        SessionState::AwaitingPermission
    );
    let EventKind::PermissionRequested { request_id, .. } = &history.last().unwrap().kind else {
        unreachable!()
    };
    client
        .permit(&session.id, request_id, "allow")
        .await
        .unwrap();
    until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionResolved { .. })
    })
    .await;
    assert_eq!(info(&client, &session.id).await.state, SessionState::Idle);
    client.prompt(&session.id, text("pid")).await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn exhausted_descriptors_do_not_stop_the_daemon() {
    let mut daemon = Daemon::start_with(1000, Some(64)).await;
    let mut streams = Vec::new();
    for _ in 0..100 {
        if let Ok(stream) = UnixStream::connect(&daemon.socket).await {
            streams.push(stream);
        }
    }
    daemon.wait_for_log("accept failed").await;
    drop(streams);
    let client = daemon.client().await;
    let (session, _) = daemon.new_session(&client).await;
    client.close(&session.id).await.unwrap();
    assert!(daemon.child.try_wait().unwrap().is_none(), "oxd exited");
}

#[tokio::test(flavor = "multi_thread")]
async fn sigterm_stops_agents_and_removes_the_socket() {
    let mut daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let agent = ExitWatch::new(daemon.dir.path());
    daemon.new_session_with(&client, &agent.arg()).await;
    drop(client);
    // SAFETY: sends SIGTERM to the daemon this test started.
    unsafe { libc::kill(daemon.child.id().unwrap().cast_signed(), libc::SIGTERM) };
    assert!(daemon.wait_for_exit().await.success());
    assert!(!daemon.socket.exists());
    agent.exited().await;
}

#[tokio::test(flavor = "multi_thread")]
async fn close_stops_the_agent() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let agent = ExitWatch::new(daemon.dir.path());
    let (session, _) = daemon.new_session_with(&client, &agent.arg()).await;
    client.close(&session.id).await.unwrap();
    agent.exited().await;
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

#[tokio::test(flavor = "multi_thread")]
async fn config_options_are_listed_and_set() {
    // An agent that reports the change before its response, or not at all,
    // yields one update. One that reports it after its response yields its
    // own notification as well.
    for (flags, updates) in [
        (&[][..], 1),
        (&["--silent-config"][..], 1),
        (&["--late-config-update"][..], 2),
    ] {
        let daemon = Daemon::start(1000).await;
        let client = daemon.client().await;
        let (session, started) = daemon.new_session_with(&client, flags).await;
        assert_eq!(current(&session.config_options, "mode"), "ask");
        let mut events = client.subscribe(&session.id, started).await.unwrap();

        let other = daemon.client().await;
        let options = other
            .set_config_option(&session.id, "mode", ConfigValue::Id("code".into()))
            .await
            .unwrap();
        assert_eq!(current(&options, "mode"), "code");
        let listed = other.list().await.unwrap();
        assert_eq!(current(&listed[0].config_options, "mode"), "code");

        let error = other
            .set_config_option(&session.id, "mode", ConfigValue::Id("fly".into()))
            .await
            .unwrap_err();
        assert_eq!(remote_code(error), code::AGENT);
        let listed = other.list().await.unwrap();
        assert_eq!(current(&listed[0].config_options, "mode"), "code");

        client.prompt(&session.id, text("mode")).await.unwrap();
        let turn = until(&mut events, turn_ended).await;
        let reported: Vec<String> = turn
            .iter()
            .filter_map(|event| match &event.kind {
                EventKind::Update(SessionUpdate::ConfigOptionUpdate(update)) => {
                    Some(current(&update.config_options, "mode"))
                }
                _ => None,
            })
            .collect();
        assert_eq!(
            reported,
            vec!["code"; updates],
            "fake agent flags {flags:?}"
        );
        assert_eq!(chunks(&turn), ["code"]);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn prompt_content_reaches_the_agent() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    assert!(session.prompt_capabilities.image);
    assert!(session.prompt_capabilities.embedded_context);
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    let prompt = vec![
        ContentBlock::Text(TextContent::new("describe")),
        ContentBlock::Image(ImageContent::new("iVBORw0KGgo=", "image/png")),
        ContentBlock::ResourceLink(ResourceLink::new("a.rs", "file:///a.rs")),
        ContentBlock::Resource(EmbeddedResource::new(
            EmbeddedResourceResource::TextResourceContents(TextResourceContents::new(
                "notes",
                "file:///b.md",
            )),
        )),
    ];
    client.prompt(&session.id, prompt.clone()).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_eq!(turn[0].kind, EventKind::PromptStarted { prompt });
    assert_eq!(
        chunks(&turn),
        ["text image:image/png link:file:///a.rs resource:file:///b.md"]
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn prompt_content_the_agent_does_not_accept_is_rejected() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session_with(&client, &["--text-only"]).await;
    let text = ContentBlock::Text(TextContent::new("describe"));
    let rejected = [
        vec![],
        vec![
            text.clone(),
            ContentBlock::Image(ImageContent::new("iVBORw0KGgo=", "image/png")),
        ],
        vec![
            text.clone(),
            ContentBlock::Audio(AudioContent::new("UklGRg==", "audio/wav")),
        ],
        vec![ContentBlock::Resource(EmbeddedResource::new(
            EmbeddedResourceResource::TextResourceContents(TextResourceContents::new(
                "notes",
                "file:///b.md",
            )),
        ))],
    ];
    for prompt in rejected {
        let error = client.prompt(&session.id, prompt).await.unwrap_err();
        assert_eq!(remote_code(error), code::INVALID_REQUEST);
    }
    let listed = client.list().await.unwrap();
    assert_eq!(listed[0].state, SessionState::Idle);

    let mut events = client.subscribe(&session.id, started).await.unwrap();
    let link = ContentBlock::ResourceLink(ResourceLink::new("a.rs", "file:///a.rs"));
    let prompt = vec![text, link];
    client.prompt(&session.id, prompt.clone()).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_eq!(turn[0].seq, started + 1);
    assert_eq!(turn[0].kind, EventKind::PromptStarted { prompt });
    assert_eq!(chunks(&turn), ["text link:file:///a.rs"]);
}

#[tokio::test(flavor = "multi_thread")]
async fn load_replays_a_session_after_the_daemon_restarts() {
    let mut first = Daemon::start(1000).await;
    let client = first.client().await;
    let (session, started) = first.new_session(&client).await;
    let agent_session_id = session.agent_session_id.clone().unwrap();
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client.prompt(&session.id, text("say 2 0")).await.unwrap();
    until(&mut events, turn_ended).await;
    drop(client);
    // SAFETY: sends SIGTERM to the daemon this test started.
    unsafe { libc::kill(first.child.id().unwrap().cast_signed(), libc::SIGTERM) };
    assert!(first.wait_for_exit().await.success());

    let second = Daemon::start(1000).await;
    let client = second.client().await;
    let loaded = client
        .load_session(
            vec![FAKE_AGENT.into()],
            first.cwd(),
            BTreeMap::new(),
            &agent_session_id,
        )
        .await
        .unwrap();
    assert_eq!(loaded.state, SessionState::Starting);
    let mut events = client.subscribe(&loaded.id, 0).await.unwrap();
    let history = until(&mut events, |kind| {
        matches!(kind, EventKind::AgentStarted { .. })
    })
    .await;
    assert_contiguous(&history, 1);
    let replayed: Vec<String> = history
        .iter()
        .filter_map(|event| match &event.kind {
            EventKind::Update(SessionUpdate::UserMessageChunk(chunk)) => match &chunk.content {
                ContentBlock::Text(text) => Some(format!("user: {}", text.text)),
                _ => None,
            },
            _ => chunk(event).map(|text| format!("agent: {text}")),
        })
        .collect();
    assert_eq!(
        replayed,
        ["user: say 2 0", "agent: chunk-1", "agent: chunk-2"]
    );
    assert!(matches!(
        &history.last().unwrap().kind,
        EventKind::AgentStarted { agent_session_id: id, .. } if *id == agent_session_id
    ));

    client.prompt(&loaded.id, text("say 1 0")).await.unwrap();
    let turn = until(&mut events, turn_ended).await;
    assert_eq!(chunks(&turn), ["chunk-1"]);
}

#[tokio::test(flavor = "multi_thread")]
async fn load_needs_an_agent_that_loads_sessions() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let loaded = client
        .load_session(
            vec![FAKE_AGENT.into(), "--no-load".into()],
            daemon.cwd(),
            BTreeMap::new(),
            "fake-unknown",
        )
        .await
        .unwrap();
    let mut events = client.subscribe(&loaded.id, 0).await.unwrap();
    let exited = next(&mut events).await;
    assert!(
        matches!(&exited.kind, EventKind::AgentExited { error: Some(error) } if error.contains("cannot load")),
        "{exited:?}"
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn sessions_watch_starts_with_the_sessions_list() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, _) = daemon.new_session(&client).await;
    let mut watch = client.watch_sessions().await.unwrap();
    let first = lists_until(&mut watch, |_| true).await.remove(0);
    assert_eq!(first, [session]);
}

#[tokio::test(flavor = "multi_thread")]
async fn sessions_watch_follows_a_session() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let mut watch = client.watch_sessions().await.unwrap();
    lists_until(&mut watch, <[SessionInfo]>::is_empty).await;

    let session = client
        .new_session(vec![FAKE_AGENT.into()], daemon.cwd(), BTreeMap::new())
        .await
        .unwrap();
    let id = session.id.as_str();
    let mut events = client.subscribe(id, 0).await.unwrap();
    let mut reach = async |state: SessionState| {
        lists_until(&mut watch, |list| state_in(list, id) == Some(state)).await;
    };
    reach(SessionState::Idle).await;

    client.prompt(id, text("wait")).await.unwrap();
    reach(SessionState::Running).await;
    client.cancel(id).await.unwrap();
    reach(SessionState::Idle).await;

    client.prompt(id, text("permission")).await.unwrap();
    reach(SessionState::AwaitingPermission).await;
    let requested = until(&mut events, |kind| {
        matches!(kind, EventKind::PermissionRequested { .. })
    })
    .await;
    let EventKind::PermissionRequested { request_id, .. } = &requested.last().unwrap().kind else {
        unreachable!()
    };
    client.permit(id, request_id, "allow").await.unwrap();
    reach(SessionState::Idle).await;

    client.close(id).await.unwrap();
    lists_until(&mut watch, |list| state_in(list, id).is_none()).await;
}

#[tokio::test(flavor = "multi_thread")]
async fn sessions_watch_sends_only_changed_lists() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut watch = client.watch_sessions().await.unwrap();
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    client.prompt(&session.id, text("say 200 0")).await.unwrap();
    until(&mut events, turn_ended).await;
    client.close(&session.id).await.unwrap();
    let lists = lists_until(&mut watch, <[SessionInfo]>::is_empty).await;
    for pair in lists.windows(2) {
        assert_ne!(pair[0], pair[1]);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn session_info_updates_set_the_title() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    assert_eq!(session.title, None);
    let mut watch = client.watch_sessions().await.unwrap();
    let mut events = client.subscribe(&session.id, started).await.unwrap();
    for (script, title) in [
        ("title Fix the build", Some("Fix the build")),
        ("touch", Some("Fix the build")),
        ("title-null", None),
    ] {
        client.prompt(&session.id, text(script)).await.unwrap();
        until(&mut events, turn_ended).await;
        assert_eq!(
            info(&client, &session.id).await.title.as_deref(),
            title,
            "{script}"
        );
        lists_until(&mut watch, |list| {
            list[0].title.as_deref() == title && list[0].state == SessionState::Idle
        })
        .await;
    }
}

/// After a sessions watch's `unsubscribe` is answered, the connection
/// receives no more lists.
#[tokio::test(flavor = "multi_thread")]
async fn unsubscribe_ends_a_sessions_watch() {
    let daemon = Daemon::start(1000).await;
    let client = daemon.client().await;
    let (session, started) = daemon.new_session(&client).await;
    let mut raw = Raw::connect(&daemon).await;
    let watch = raw.send("sessions.watch", Value::Null).await;
    raw.answer(watch).await;
    let first = raw.read().await;
    assert_eq!(first["subscription"], watch);
    assert_eq!(first["sessions"][0]["id"], session.id.as_str());
    let unsubscribe = raw
        .send("unsubscribe", json!({"subscription": watch}))
        .await;
    raw.answer(unsubscribe).await;
    raw.send(
        "session.subscribe",
        json!({"session_id": session.id, "after_seq": started}),
    )
    .await;

    // A turn held open changes the session's state long enough for a watch
    // to send the list.
    let mut running = client.watch_sessions().await.unwrap();
    client.prompt(&session.id, text("wait")).await.unwrap();
    lists_until(&mut running, |list| list[0].state == SessionState::Running).await;
    client.cancel(&session.id).await.unwrap();
    let messages = raw.messages_until(raw_turn_ended).await;
    assert!(
        messages
            .iter()
            .all(|message| message.get("sessions").is_none()),
        "{messages:?}"
    );
}
