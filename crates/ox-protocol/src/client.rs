use std::collections::{BTreeMap, HashMap};
use std::fmt;
use std::fs::OpenOptions;
use std::io;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::de::DeserializeOwned;
use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::net::unix::{OwnedReadHalf, OwnedWriteHalf};
use tokio::sync::{mpsc, oneshot};
use tokio::task::JoinHandle;
use tokio::time::Instant;

use crate::messages::{
    ErrorBody, Event, EventKind, HelloResult, Method, PROTOCOL_VERSION, Request, ServerMessage,
    SessionInfo,
};

const START_TIMEOUT: Duration = Duration::from_secs(5);
const START_POLL_INTERVAL: Duration = Duration::from_millis(20);
const RESPAWN_INTERVAL: Duration = Duration::from_millis(250);

#[derive(Debug)]
pub enum Error {
    Io(io::Error),
    Json(serde_json::Error),
    /// The daemon answered the request with an error.
    Remote(ErrorBody),
    /// The connection to the daemon closed.
    Closed,
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Io(error) => write!(f, "{error}"),
            Self::Json(error) => write!(f, "invalid message: {error}"),
            Self::Remote(error) => write!(f, "{}", error.message),
            Self::Closed => write!(f, "connection to oxd closed"),
        }
    }
}

impl std::error::Error for Error {}

impl From<io::Error> for Error {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

impl From<serde_json::Error> for Error {
    fn from(error: serde_json::Error) -> Self {
        Self::Json(error)
    }
}

/// The socket path: `$OX_SOCKET`, else `<data dir>/ox/oxd.sock`.
pub fn default_socket_path() -> PathBuf {
    if let Some(path) = std::env::var_os("OX_SOCKET") {
        return PathBuf::from(path);
    }
    dirs::data_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join("ox")
        .join("oxd.sock")
}

/// The daemon binary: `$OXD_BIN`, else `oxd` beside the current executable.
pub fn daemon_binary() -> io::Result<PathBuf> {
    if let Some(path) = std::env::var_os("OXD_BIN") {
        return Ok(PathBuf::from(path));
    }
    let exe = std::env::current_exe()?;
    Ok(exe.with_file_name("oxd"))
}

#[derive(Default)]
struct Shared {
    closed: bool,
    pending: HashMap<u64, oneshot::Sender<Result<Value, Error>>>,
    subscriptions: HashMap<String, SubscriptionSlot>,
    /// Session ids of `session.subscribe` requests awaiting their answer.
    subscribing: HashMap<u64, String>,
}

struct SubscriptionSlot {
    sender: mpsc::UnboundedSender<Event>,
    /// The `session.subscribe` request this slot waits on. Events that arrive
    /// before its answer belong to an earlier subscription and are dropped.
    request: Option<u64>,
}

/// A connection to oxd. Dropping it closes the connection.
pub struct Client {
    next_id: AtomicU64,
    outgoing: mpsc::UnboundedSender<String>,
    shared: Arc<Mutex<Shared>>,
    reader: JoinHandle<()>,
    writer: JoinHandle<()>,
    hello: HelloResult,
}

/// Events of one subscribed session, in `seq` order.
#[derive(Debug)]
pub struct Subscription {
    events: mpsc::UnboundedReceiver<Event>,
}

impl Subscription {
    /// The next event, or `None` once the connection or session is gone.
    pub async fn next(&mut self) -> Option<Event> {
        self.events.recv().await
    }
}

impl Client {
    /// Connects to the daemon at `socket` and performs `hello`.
    pub async fn connect(socket: &Path) -> Result<Self, Error> {
        let stream = UnixStream::connect(socket).await?;
        let (read, write) = stream.into_split();
        let shared = Arc::new(Mutex::new(Shared::default()));
        let (outgoing, outgoing_rx) = mpsc::unbounded_channel();
        let mut client = Self {
            next_id: AtomicU64::new(1),
            outgoing,
            shared: shared.clone(),
            reader: tokio::spawn(read_loop(read, shared)),
            writer: tokio::spawn(write_loop(write, outgoing_rx)),
            hello: HelloResult {
                protocol_version: PROTOCOL_VERSION,
                daemon_version: String::new(),
            },
        };
        client.hello = client
            .call(Method::Hello {
                protocol_version: PROTOCOL_VERSION,
            })
            .await?;
        Ok(client)
    }

    /// Connects to the daemon at `socket`, starting one if none is listening.
    pub async fn connect_or_start(socket: &Path) -> Result<Self, Error> {
        Self::connect_or_start_with(socket, &daemon_binary()?).await
    }

    /// Like [`connect_or_start`](Self::connect_or_start), with an explicit
    /// daemon binary.
    pub async fn connect_or_start_with(socket: &Path, daemon: &Path) -> Result<Self, Error> {
        let deadline = Instant::now() + START_TIMEOUT;
        let mut last_spawn: Option<Instant> = None;
        loop {
            match Self::connect(socket).await {
                // No daemon is listening, or one closed the connection while
                // exiting. Starting another is harmless: a daemon that cannot
                // take the lock exits at once.
                Err(Error::Io(_) | Error::Closed) if Instant::now() < deadline => {}
                result => return result,
            }
            if last_spawn.is_none_or(|at| at.elapsed() >= RESPAWN_INTERVAL) {
                spawn_daemon(socket, daemon)?;
                last_spawn = Some(Instant::now());
            }
            tokio::time::sleep(START_POLL_INTERVAL).await;
        }
    }

    pub fn daemon_version(&self) -> &str {
        &self.hello.daemon_version
    }

    pub async fn list(&self) -> Result<Vec<SessionInfo>, Error> {
        self.call(Method::SessionList).await
    }

    pub async fn new_session(
        &self,
        command: Vec<String>,
        cwd: PathBuf,
        env: BTreeMap<String, String>,
    ) -> Result<SessionInfo, Error> {
        self.call(Method::SessionNew { command, cwd, env }).await
    }

    pub async fn prompt(&self, session_id: &str, text: &str) -> Result<(), Error> {
        self.ack(Method::SessionPrompt {
            session_id: session_id.into(),
            text: text.into(),
        })
        .await
    }

    pub async fn cancel(&self, session_id: &str) -> Result<(), Error> {
        self.ack(Method::SessionCancel {
            session_id: session_id.into(),
        })
        .await
    }

    /// Subscribes to a session's events with `seq > after_seq`, followed by
    /// live events. A second subscription to the same session replaces the
    /// first.
    pub async fn subscribe(&self, session_id: &str, after_seq: u64) -> Result<Subscription, Error> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        let (sender, events) = mpsc::unbounded_channel();
        {
            let mut shared = self.shared.lock().unwrap();
            shared.subscribing.insert(id, session_id.into());
            shared.subscriptions.insert(
                session_id.into(),
                SubscriptionSlot {
                    sender,
                    request: Some(id),
                },
            );
        }
        self.send::<Value>(
            id,
            Method::SessionSubscribe {
                session_id: session_id.into(),
                after_seq,
            },
        )
        .await?;
        Ok(Subscription { events })
    }

    pub async fn permit(
        &self,
        session_id: &str,
        request_id: &str,
        option_id: &str,
    ) -> Result<(), Error> {
        self.ack(Method::PermissionRespond {
            session_id: session_id.into(),
            request_id: request_id.into(),
            option_id: option_id.into(),
        })
        .await
    }

    pub async fn close(&self, session_id: &str) -> Result<(), Error> {
        self.ack(Method::SessionClose {
            session_id: session_id.into(),
        })
        .await
    }

    async fn ack(&self, method: Method) -> Result<(), Error> {
        self.call::<Value>(method).await.map(drop)
    }

    async fn call<T: DeserializeOwned>(&self, method: Method) -> Result<T, Error> {
        let id = self.next_id.fetch_add(1, Ordering::Relaxed);
        self.send(id, method).await
    }

    async fn send<T: DeserializeOwned>(&self, id: u64, method: Method) -> Result<T, Error> {
        let (sender, receiver) = oneshot::channel();
        {
            let mut shared = self.shared.lock().unwrap();
            if shared.closed {
                return Err(Error::Closed);
            }
            shared.pending.insert(id, sender);
        }
        let line = serde_json::to_string(&Request { id, method })?;
        self.outgoing.send(line).map_err(|_| Error::Closed)?;
        let value = receiver.await.map_err(|_| Error::Closed)??;
        Ok(serde_json::from_value(value)?)
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        self.reader.abort();
        self.writer.abort();
    }
}

async fn read_loop(read: OwnedReadHalf, shared: Arc<Mutex<Shared>>) {
    let mut lines = BufReader::new(read).lines();
    while let Ok(Some(line)) = lines.next_line().await {
        let Ok(message) = ServerMessage::parse(&line) else {
            break;
        };
        let mut shared = shared.lock().unwrap();
        match message {
            ServerMessage::Result { id, result } => {
                if let Some(session_id) = shared.subscribing.remove(&id)
                    && let Some(slot) = shared.subscriptions.get_mut(&session_id)
                    && slot.request == Some(id)
                {
                    slot.request = None;
                }
                if let Some(sender) = shared.pending.remove(&id) {
                    let _ = sender.send(Ok(result));
                }
            }
            ServerMessage::Error { id, error } => {
                if let Some(session_id) = shared.subscribing.remove(&id)
                    && shared
                        .subscriptions
                        .get(&session_id)
                        .is_some_and(|slot| slot.request == Some(id))
                {
                    shared.subscriptions.remove(&session_id);
                }
                if let Some(sender) = shared.pending.remove(&id) {
                    let _ = sender.send(Err(Error::Remote(error)));
                }
            }
            ServerMessage::Event { event } => {
                let session_id = event.session_id.clone();
                let closed = matches!(event.kind, EventKind::SessionClosed);
                if let Some(slot) = shared.subscriptions.get(&session_id)
                    && slot.request.is_none()
                {
                    let _ = slot.sender.send(*event);
                    if closed {
                        shared.subscriptions.remove(&session_id);
                    }
                }
            }
        }
    }
    let mut shared = shared.lock().unwrap();
    shared.closed = true;
    shared.pending.clear();
    shared.subscriptions.clear();
    shared.subscribing.clear();
}

async fn write_loop(mut write: OwnedWriteHalf, mut outgoing: mpsc::UnboundedReceiver<String>) {
    while let Some(mut line) = outgoing.recv().await {
        line.push('\n');
        if write.write_all(line.as_bytes()).await.is_err() {
            break;
        }
    }
}

/// Starts `daemon --socket <socket>` in a new session, logging to `oxd.log`
/// beside the socket.
fn spawn_daemon(socket: &Path, daemon: &Path) -> io::Result<()> {
    let dir = socket.parent().unwrap_or(Path::new("."));
    std::fs::create_dir_all(dir)?;
    let log = OpenOptions::new()
        .create(true)
        .append(true)
        .open(dir.join("oxd.log"))?;
    let mut command = std::process::Command::new(daemon);
    command
        .arg("--socket")
        .arg(socket)
        .stdin(Stdio::null())
        .stdout(log.try_clone()?)
        .stderr(log);
    // SAFETY: `setsid` is async-signal-safe and touches no Rust state.
    unsafe {
        command.pre_exec(|| {
            if libc::setsid() == -1 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let mut child = command.spawn()?;
    std::thread::spawn(move || child.wait());
    Ok(())
}
