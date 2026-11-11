use std::collections::{BTreeMap, HashMap};
use std::fmt;
use std::fs::OpenOptions;
use std::io;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agent_client_protocol::v2::schema::{ContentBlock, SessionConfigOption};
use serde::de::DeserializeOwned;
use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::net::unix::{OwnedReadHalf, OwnedWriteHalf};
use tokio::sync::{mpsc, oneshot};
use tokio::task::JoinHandle;

use crate::messages::{
    ConfigValue, ErrorBody, Event, EventKind, HelloResult, Listing, Method, PROTOCOL_VERSION,
    Request, ServerMessage, SessionInfo, WorkspaceInfo,
};

/// How long a started daemon has to report that it is ready.
const START_TIMEOUT: Duration = Duration::from_secs(5);

/// Daemons `connect_or_start` starts before giving up. A second is needed
/// when the daemon that answered for the first was exiting.
const START_ATTEMPTS: usize = 3;

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

/// The daemon binary: `oxd` beside the current executable.
pub fn daemon_binary() -> io::Result<PathBuf> {
    Ok(std::env::current_exe()?.with_file_name("oxd"))
}

/// What a client shares with its reader and its subscriptions.
#[derive(Debug)]
struct Connection {
    next_id: AtomicU64,
    outgoing: mpsc::UnboundedSender<String>,
    routes: Mutex<Routes>,
}

/// Where the reader delivers the daemon's messages.
#[derive(Debug, Default)]
struct Routes {
    closed: bool,
    pending: HashMap<u64, oneshot::Sender<Result<Value, Error>>>,
    /// Each stream, by the id of the request that started it.
    streams: HashMap<u64, Route>,
}

/// A stream's messages: a subscription's events, or a watch's listings.
#[derive(Debug)]
enum Route {
    Events(mpsc::UnboundedSender<Event>),
    Listings(mpsc::UnboundedSender<Listing>),
}

impl Routes {
    /// Fails every pending request and disconnects every stream.
    fn close(&mut self) {
        self.closed = true;
        self.pending.clear();
        self.streams.clear();
    }
}

impl Connection {
    fn next_id(&self) -> u64 {
        self.next_id.fetch_add(1, Ordering::Relaxed)
    }

    fn send(&self, id: u64, method: Method) -> Result<(), Error> {
        let line = serde_json::to_string(&Request { id, method })?;
        self.outgoing.send(line).map_err(|_| Error::Closed)
    }
}

/// A connection to oxd. Dropping it closes the connection.
pub struct Client {
    connection: Arc<Connection>,
    reader: JoinHandle<()>,
    writer: JoinHandle<()>,
    hello: HelloResult,
}

/// Ends a stream when dropped: removes its route and sends `unsubscribe`.
#[derive(Debug)]
struct Unsubscribe {
    id: u64,
    connection: Arc<Connection>,
}

impl Unsubscribe {
    /// Routes the messages of stream `id` to `route` until the returned
    /// guard is dropped.
    fn route(connection: &Arc<Connection>, id: u64, route: Route) -> Self {
        connection.routes.lock().unwrap().streams.insert(id, route);
        Self {
            id,
            connection: connection.clone(),
        }
    }
}

impl Drop for Unsubscribe {
    fn drop(&mut self) {
        self.connection
            .routes
            .lock()
            .unwrap()
            .streams
            .remove(&self.id);
        // Sending fails only once the connection is gone, and the daemon's
        // side of the stream with it.
        let _ = self.connection.send(
            self.connection.next_id(),
            Method::Unsubscribe {
                subscription: self.id,
            },
        );
    }
}

/// Events of one session, in `seq` order. Dropping it unsubscribes.
#[derive(Debug)]
pub struct Subscription {
    events: mpsc::UnboundedReceiver<Event>,
    session_closed: bool,
    _unsubscribe: Unsubscribe,
}

/// Why a stream has no more messages.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum End {
    /// The session was closed. Its `SessionClosed` event came last.
    SessionClosed,
    /// The connection to the daemon closed.
    Disconnected,
}

impl Subscription {
    pub async fn next(&mut self) -> Result<Event, End> {
        if self.session_closed {
            return Err(End::SessionClosed);
        }
        let event = self.events.recv().await.ok_or(End::Disconnected)?;
        self.session_closed = matches!(event.kind, EventKind::SessionClosed);
        Ok(event)
    }
}

/// The daemon's workspaces and sessions, each time either changes. Dropping
/// it unsubscribes.
#[derive(Debug)]
pub struct Watch {
    listings: mpsc::UnboundedReceiver<Listing>,
    _unsubscribe: Unsubscribe,
}

impl Watch {
    /// The next listing, which differs from the one before it. The first is
    /// the listing when the watch started. A watch ends only with
    /// [`End::Disconnected`].
    pub async fn next(&mut self) -> Result<Listing, End> {
        self.listings.recv().await.ok_or(End::Disconnected)
    }
}

impl Client {
    /// Connects to the daemon at `socket` and performs `hello`.
    pub async fn connect(socket: &Path) -> Result<Self, Error> {
        let stream = UnixStream::connect(socket).await?;
        let (read, write) = stream.into_split();
        let (outgoing, outgoing_rx) = mpsc::unbounded_channel();
        let connection = Arc::new(Connection {
            next_id: AtomicU64::new(1),
            outgoing,
            routes: Mutex::default(),
        });
        let mut client = Self {
            reader: tokio::spawn(read_loop(read, connection.clone())),
            writer: tokio::spawn(write_loop(write, outgoing_rx)),
            connection,
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
    ///
    /// A started daemon prints `ready` once a daemon is listening on the
    /// socket, whether itself or one that was already running. A daemon
    /// that is exiting also closes connections, so a failed connection
    /// after `ready` starts another daemon, which waits for the exiting one
    /// to finish.
    pub async fn connect_or_start_with(socket: &Path, daemon: &Path) -> Result<Self, Error> {
        for _ in 0..START_ATTEMPTS {
            match Self::connect(socket).await {
                Err(Error::Io(_) | Error::Closed) => start_daemon(socket, daemon).await?,
                result => return result,
            }
        }
        Self::connect(socket).await
    }

    pub fn daemon_version(&self) -> &str {
        &self.hello.daemon_version
    }

    /// Whether the connection to the daemon has closed. Every request on a
    /// closed client fails with [`Error::Closed`].
    pub fn is_closed(&self) -> bool {
        self.connection.routes.lock().unwrap().closed
    }

    pub async fn list(&self) -> Result<Vec<SessionInfo>, Error> {
        self.call(Method::SessionList).await
    }

    pub async fn add_workspace(&self, path: PathBuf) -> Result<WorkspaceInfo, Error> {
        self.call(Method::WorkspaceAdd { path }).await
    }

    pub async fn remove_workspace(&self, workspace_id: &str) -> Result<(), Error> {
        self.ack(Method::WorkspaceRemove {
            workspace_id: workspace_id.into(),
        })
        .await
    }

    pub async fn list_workspaces(&self) -> Result<Vec<WorkspaceInfo>, Error> {
        self.call(Method::WorkspaceList).await
    }

    pub async fn new_session(
        &self,
        workspace_id: &str,
        command: Vec<String>,
        env: BTreeMap<String, String>,
    ) -> Result<SessionInfo, Error> {
        self.call(Method::SessionNew {
            workspace_id: workspace_id.into(),
            command,
            env,
        })
        .await
    }

    pub async fn load_session(
        &self,
        session_id: &str,
        env: BTreeMap<String, String>,
    ) -> Result<SessionInfo, Error> {
        self.call(Method::SessionLoad {
            session_id: session_id.into(),
            env,
        })
        .await
    }

    pub async fn prompt(&self, session_id: &str, prompt: Vec<ContentBlock>) -> Result<(), Error> {
        self.ack(Method::SessionPrompt {
            session_id: session_id.into(),
            prompt,
        })
        .await
    }

    /// Sets a config option and returns the agent's resulting options.
    pub async fn set_config_option(
        &self,
        session_id: &str,
        config_id: &str,
        value: ConfigValue,
    ) -> Result<Vec<SessionConfigOption>, Error> {
        self.call(Method::SessionSetConfigOption {
            session_id: session_id.into(),
            config_id: config_id.into(),
            value,
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
    /// live events.
    pub async fn subscribe(&self, session_id: &str, after_seq: u64) -> Result<Subscription, Error> {
        let id = self.connection.next_id();
        let (sender, events) = mpsc::unbounded_channel();
        let subscription = Subscription {
            events,
            session_closed: false,
            _unsubscribe: Unsubscribe::route(&self.connection, id, Route::Events(sender)),
        };
        self.send::<Value>(
            id,
            Method::SessionSubscribe {
                session_id: session_id.into(),
                after_seq,
            },
        )
        .await?;
        Ok(subscription)
    }

    /// Watches the daemon's workspaces and sessions.
    pub async fn watch(&self) -> Result<Watch, Error> {
        let id = self.connection.next_id();
        let (sender, listings) = mpsc::unbounded_channel();
        let watch = Watch {
            listings,
            _unsubscribe: Unsubscribe::route(&self.connection, id, Route::Listings(sender)),
        };
        self.send::<Value>(id, Method::Watch).await?;
        Ok(watch)
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
        self.send(self.connection.next_id(), method).await
    }

    async fn send<T: DeserializeOwned>(&self, id: u64, method: Method) -> Result<T, Error> {
        let (sender, receiver) = oneshot::channel();
        {
            let mut routes = self.connection.routes.lock().unwrap();
            if routes.closed {
                return Err(Error::Closed);
            }
            routes.pending.insert(id, sender);
        }
        self.connection.send(id, method)?;
        let value = receiver.await.map_err(|_| Error::Closed)??;
        Ok(serde_json::from_value(value)?)
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        self.reader.abort();
        self.writer.abort();
        self.connection.routes.lock().unwrap().close();
    }
}

async fn read_loop(read: OwnedReadHalf, connection: Arc<Connection>) {
    let mut lines = BufReader::new(read).lines();
    while let Ok(Some(line)) = lines.next_line().await {
        let Ok(message) = serde_json::from_str::<ServerMessage>(&line) else {
            break;
        };
        let mut routes = connection.routes.lock().unwrap();
        match message {
            ServerMessage::Result { id, result } => {
                if let Some(sender) = routes.pending.remove(&id) {
                    let _ = sender.send(Ok(result));
                }
            }
            ServerMessage::Error { id, error } => {
                if let Some(sender) = routes.pending.remove(&id) {
                    let _ = sender.send(Err(Error::Remote(error)));
                }
            }
            ServerMessage::Event {
                subscription,
                event,
            } => {
                if let Some(Route::Events(events)) = routes.streams.get(&subscription) {
                    let _ = events.send(*event);
                }
            }
            ServerMessage::List {
                subscription,
                workspaces,
                sessions,
            } => {
                if let Some(Route::Listings(listings)) = routes.streams.get(&subscription) {
                    let _ = listings.send(Listing {
                        workspaces,
                        sessions,
                    });
                }
            }
        }
    }
    connection.routes.lock().unwrap().close();
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
/// beside the socket, and waits for it to print `ready`.
async fn start_daemon(socket: &Path, daemon: &Path) -> Result<(), Error> {
    let dir = socket.parent().unwrap_or(Path::new("."));
    std::fs::create_dir_all(dir)?;
    let log_path = dir.join("oxd.log");
    let log = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)?;
    let mut command = tokio::process::Command::new(daemon);
    command
        .arg("--socket")
        .arg(socket)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
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
    let mut stdout = BufReader::new(child.stdout.take().expect("piped stdout"));
    let mut line = String::new();
    let read = stdout.read_line(&mut line);
    match tokio::time::timeout(START_TIMEOUT, read).await {
        Ok(Ok(_)) if line.trim_end() == "ready" => Ok(()),
        Ok(Err(error)) => Err(error.into()),
        Ok(Ok(_)) => Err(io::Error::other(format!(
            "oxd exited during startup; see {}",
            log_path.display()
        ))
        .into()),
        Err(_) => Err(io::Error::other(format!(
            "oxd did not become ready within {} s; see {}",
            START_TIMEOUT.as_secs(),
            log_path.display()
        ))
        .into()),
    }
}

#[cfg(test)]
mod tests {
    use tokio::io::Lines;
    use tokio::net::UnixListener;
    use tokio::time::timeout;

    use super::*;

    const TIMEOUT: Duration = Duration::from_secs(10);

    /// The daemon's end of a client's connection.
    struct Daemon {
        lines: Lines<BufReader<OwnedReadHalf>>,
        write: OwnedWriteHalf,
        _dir: tempfile::TempDir,
    }

    impl Daemon {
        /// Reads the client's next request and answers it with `result`.
        async fn answer(&mut self, result: Value) -> Request {
            let line = timeout(TIMEOUT, self.lines.next_line())
                .await
                .expect("timed out waiting for a request")
                .unwrap()
                .unwrap();
            let request: Request = serde_json::from_str(&line).unwrap();
            let id = request.id;
            self.send(ServerMessage::Result { id, result }).await;
            request
        }

        async fn send(&mut self, message: ServerMessage) {
            let line = serde_json::to_string(&message).unwrap() + "\n";
            self.write.write_all(line.as_bytes()).await.unwrap();
        }

        async fn close_session(&mut self, subscription: u64) {
            let event = Box::new(Event {
                session_id: "s".into(),
                seq: 1,
                kind: EventKind::SessionClosed,
            });
            self.send(ServerMessage::Event {
                subscription,
                event,
            })
            .await;
        }
    }

    async fn connect() -> (Client, Daemon) {
        let dir = tempfile::tempdir().unwrap();
        let socket = dir.path().join("oxd.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let accept = async {
            let (read, write) = listener.accept().await.unwrap().0.into_split();
            let mut daemon = Daemon {
                lines: BufReader::new(read).lines(),
                write,
                _dir: dir,
            };
            let hello = HelloResult {
                protocol_version: PROTOCOL_VERSION,
                daemon_version: "test".into(),
            };
            daemon.answer(serde_json::to_value(hello).unwrap()).await;
            daemon
        };
        let (client, daemon) = tokio::join!(Client::connect(&socket), accept);
        (client.unwrap(), daemon)
    }

    async fn subscribe(client: &Client, daemon: &mut Daemon) -> (Subscription, u64) {
        let (subscription, request) =
            tokio::join!(client.subscribe("s", 0), daemon.answer(Value::Null));
        (subscription.unwrap(), request.id)
    }

    async fn next(subscription: &mut Subscription) -> Result<Event, End> {
        timeout(TIMEOUT, subscription.next())
            .await
            .expect("timed out waiting for the subscription")
    }

    #[tokio::test]
    async fn dropping_a_subscription_unsubscribes() {
        let (client, mut daemon) = connect().await;
        let (subscription, id) = subscribe(&client, &mut daemon).await;
        drop(subscription);
        let request = daemon.answer(Value::Null).await;
        assert!(matches!(
            request.method,
            Method::Unsubscribe { subscription } if subscription == id
        ));
    }

    #[tokio::test]
    async fn dropping_a_watch_unsubscribes() {
        let (client, mut daemon) = connect().await;
        let (watch, request) = tokio::join!(client.watch(), daemon.answer(Value::Null));
        assert!(matches!(request.method, Method::Watch));
        drop(watch.unwrap());
        let unsubscribe = daemon.answer(Value::Null).await;
        assert!(matches!(
            unsubscribe.method,
            Method::Unsubscribe { subscription } if subscription == request.id
        ));
    }

    #[tokio::test]
    async fn a_watch_ends_with_its_connection() {
        let (client, mut daemon) = connect().await;
        let (watch, request) = tokio::join!(client.watch(), daemon.answer(Value::Null));
        let mut watch = watch.unwrap();
        daemon
            .send(ServerMessage::List {
                subscription: request.id,
                workspaces: Vec::new(),
                sessions: Vec::new(),
            })
            .await;
        let listing = timeout(TIMEOUT, watch.next()).await.unwrap().unwrap();
        assert_eq!(
            listing,
            Listing {
                workspaces: Vec::new(),
                sessions: Vec::new()
            }
        );
        assert!(!client.is_closed());
        drop(daemon);
        let end = timeout(TIMEOUT, watch.next()).await.unwrap();
        assert_eq!(end, Err(End::Disconnected));
        assert!(client.is_closed());
        assert!(matches!(client.list().await, Err(Error::Closed)));
    }

    #[tokio::test]
    async fn a_subscription_ends_with_its_session_or_its_connection() {
        let (client, mut daemon) = connect().await;
        let (mut first, _) = subscribe(&client, &mut daemon).await;
        let (mut second, id) = subscribe(&client, &mut daemon).await;
        daemon.close_session(id).await;
        let closed = next(&mut second).await.unwrap();
        assert_eq!(closed.kind, EventKind::SessionClosed);
        assert_eq!(next(&mut second).await, Err(End::SessionClosed));
        drop(daemon);
        assert_eq!(next(&mut first).await, Err(End::Disconnected));

        let (client, mut daemon) = connect().await;
        let (mut subscription, _) = subscribe(&client, &mut daemon).await;
        drop(client);
        assert_eq!(next(&mut subscription).await, Err(End::Disconnected));
    }
}
