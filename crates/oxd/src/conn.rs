use std::collections::HashMap;
use std::sync::Arc;

use ox_protocol::{
    ErrorBody, Event, HelloResult, Method, PROTOCOL_VERSION, Request, ServerMessage, code,
};
use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::net::unix::OwnedWriteHalf;
use tokio::sync::broadcast::error::RecvError;
use tokio::sync::{Notify, broadcast, mpsc};
use tokio::task::JoinHandle;
use tokio::time::{Duration, timeout};

use crate::daemon::{ClientGuard, Daemon};

/// Messages queued for a client before its subscriptions stop forwarding
/// and start to lag.
const OUTGOING_CAPACITY: usize = 256;

/// How long one write to a client may block before the client is
/// disconnected.
const WRITE_TIMEOUT: Duration = Duration::from_secs(30);

/// Serves one client until it disconnects, stops reading, or lags a
/// subscription.
pub async fn serve(daemon: Arc<Daemon>, stream: UnixStream, _client: ClientGuard) {
    let (read, write) = stream.into_split();
    let (outgoing, outgoing_rx) = mpsc::channel(OUTGOING_CAPACITY);
    let closing = Arc::new(Notify::new());
    let writer = tokio::spawn(write_loop(write, outgoing_rx, closing.clone()));
    let mut subscriptions: HashMap<String, JoinHandle<()>> = HashMap::new();
    let mut lines = BufReader::new(read).lines();
    let mut greeted = false;

    loop {
        let line = tokio::select! {
            line = lines.next_line() => line,
            () = closing.notified() => break,
        };
        let Ok(Some(line)) = line else { break };
        let request: Request = match serde_json::from_str(&line) {
            Ok(request) => request,
            Err(error) => {
                let Some(id) = serde_json::from_str::<Value>(&line)
                    .ok()
                    .and_then(|value| value.get("id").and_then(Value::as_u64))
                else {
                    break;
                };
                let error = ErrorBody {
                    code: code::INVALID_REQUEST,
                    message: error.to_string(),
                };
                if outgoing
                    .send(ServerMessage::Error { id, error })
                    .await
                    .is_err()
                {
                    break;
                }
                continue;
            }
        };
        let id = request.id;
        let result = match request.method {
            Method::Hello { protocol_version } if protocol_version == PROTOCOL_VERSION => {
                greeted = true;
                Ok(serde_json::to_value(HelloResult {
                    protocol_version: PROTOCOL_VERSION,
                    daemon_version: env!("CARGO_PKG_VERSION").into(),
                })
                .expect("protocol types serialize"))
            }
            Method::Hello { protocol_version } => Err(ErrorBody {
                code: code::PROTOCOL_MISMATCH,
                message: format!(
                    "protocol version {protocol_version} is not supported; oxd speaks {PROTOCOL_VERSION}"
                ),
            }),
            _ if !greeted => Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: "the first message must be hello".into(),
            }),
            Method::SessionSubscribe {
                session_id,
                after_seq,
            } => match daemon.session(&session_id) {
                Ok(session) => {
                    // The previous subscription must stop sending before the
                    // ack: the client drops events that precede it.
                    if let Some(previous) = subscriptions.remove(&session_id) {
                        previous.abort();
                        let _ = previous.await;
                    }
                    let (backlog, receiver) = session.subscribe(after_seq);
                    let ack = ServerMessage::Result {
                        id,
                        result: Value::Null,
                    };
                    if outgoing.send(ack).await.is_err() {
                        break;
                    }
                    let task = tokio::spawn(forward(
                        backlog,
                        receiver,
                        after_seq,
                        outgoing.clone(),
                        closing.clone(),
                    ));
                    subscriptions.insert(session_id, task);
                    continue;
                }
                Err(error) => Err(error),
            },
            // Agent startup can take a while; the connection keeps serving
            // other requests meanwhile.
            method @ Method::SessionNew { .. } => {
                let daemon = daemon.clone();
                let outgoing = outgoing.clone();
                tokio::spawn(async move {
                    let message = match daemon.handle(method).await {
                        Ok(result) => ServerMessage::Result { id, result },
                        Err(error) => ServerMessage::Error { id, error },
                    };
                    let _ = outgoing.send(message).await;
                });
                continue;
            }
            method => daemon.handle(method).await,
        };
        let message = match result {
            Ok(result) => ServerMessage::Result { id, result },
            Err(error) => ServerMessage::Error { id, error },
        };
        if outgoing.send(message).await.is_err() {
            break;
        }
    }

    for task in subscriptions.values() {
        task.abort();
    }
    writer.abort();
}

async fn forward(
    backlog: Vec<Event>,
    mut receiver: broadcast::Receiver<Event>,
    after_seq: u64,
    outgoing: mpsc::Sender<ServerMessage>,
    closing: Arc<Notify>,
) {
    for event in backlog {
        if outgoing
            .send(ServerMessage::Event {
                event: Box::new(event),
            })
            .await
            .is_err()
        {
            return;
        }
    }
    loop {
        match receiver.recv().await {
            Ok(event) if event.seq <= after_seq => {}
            Ok(event) => {
                if outgoing
                    .send(ServerMessage::Event {
                        event: Box::new(event),
                    })
                    .await
                    .is_err()
                {
                    return;
                }
            }
            Err(RecvError::Lagged(_)) => {
                closing.notify_one();
                return;
            }
            Err(RecvError::Closed) => return,
        }
    }
}

async fn write_loop(
    mut write: OwnedWriteHalf,
    mut outgoing: mpsc::Receiver<ServerMessage>,
    closing: Arc<Notify>,
) {
    while let Some(message) = outgoing.recv().await {
        let mut line = serde_json::to_string(&message).expect("protocol types serialize");
        line.push('\n');
        match timeout(WRITE_TIMEOUT, write.write_all(line.as_bytes())).await {
            Ok(Ok(())) => {}
            _ => break,
        }
    }
    closing.notify_one();
}
