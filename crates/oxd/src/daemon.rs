use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use ox_protocol::{ErrorBody, Method, SessionInfo, code};
use serde_json::Value;
use tokio::time::Instant;

use crate::session::Session;

const IDLE_POLL_INTERVAL: Duration = Duration::from_millis(50);

pub struct Daemon {
    sessions: Mutex<HashMap<String, Arc<Session>>>,
    clients: AtomicUsize,
    next_session: AtomicU64,
    /// How long an agent has to answer `initialize` and `session/new`.
    startup_timeout: Duration,
}

/// Counts one connected client for as long as it lives.
pub struct ClientGuard(Arc<Daemon>);

impl Drop for ClientGuard {
    fn drop(&mut self) {
        self.0.clients.fetch_sub(1, Ordering::SeqCst);
    }
}

impl Daemon {
    pub fn new(startup_timeout: Duration) -> Self {
        Self {
            sessions: Mutex::default(),
            clients: AtomicUsize::new(0),
            next_session: AtomicU64::new(0),
            startup_timeout,
        }
    }

    pub fn client(self: &Arc<Self>) -> ClientGuard {
        self.clients.fetch_add(1, Ordering::SeqCst);
        ClientGuard(self.clone())
    }

    /// Resolves once the daemon has had no sessions and no clients for
    /// `idle_exit`.
    pub async fn idle(&self, idle_exit: Duration) {
        let mut idle_since = None;
        loop {
            let idle = self.clients.load(Ordering::SeqCst) == 0
                && self.sessions.lock().unwrap().is_empty();
            if idle {
                let since = *idle_since.get_or_insert_with(Instant::now);
                if since.elapsed() >= idle_exit {
                    return;
                }
            } else {
                idle_since = None;
            }
            tokio::time::sleep(IDLE_POLL_INTERVAL).await;
        }
    }

    /// Closes every session, killing the agents.
    pub fn close_all(&self) {
        let sessions: Vec<Arc<Session>> = self
            .sessions
            .lock()
            .unwrap()
            .drain()
            .map(|(_, s)| s)
            .collect();
        for session in sessions {
            session.close();
        }
    }

    pub fn session(&self, id: &str) -> Result<Arc<Session>, ErrorBody> {
        self.sessions
            .lock()
            .unwrap()
            .get(id)
            .cloned()
            .ok_or_else(|| ErrorBody {
                code: code::NOT_FOUND,
                message: format!("no session {id}"),
            })
    }

    /// Handles every method except `hello` and `session.subscribe`, which
    /// belong to the connection.
    pub async fn handle(&self, method: Method) -> Result<Value, ErrorBody> {
        match method {
            Method::SessionList => Ok(to_value(self.list())),
            Method::SessionNew { command, cwd, env } => {
                Ok(to_value(self.new_session(command, cwd, env).await?))
            }
            Method::SessionPrompt { session_id, text } => {
                self.session(&session_id)?.prompt(text)?;
                Ok(Value::Null)
            }
            Method::SessionCancel { session_id } => {
                self.session(&session_id)?.cancel();
                Ok(Value::Null)
            }
            Method::PermissionRespond {
                session_id,
                request_id,
                option_id,
            } => {
                self.session(&session_id)?.permit(&request_id, &option_id)?;
                Ok(Value::Null)
            }
            Method::SessionClose { session_id } => {
                let session = self.sessions.lock().unwrap().remove(&session_id);
                match session {
                    Some(session) => {
                        session.close();
                        Ok(Value::Null)
                    }
                    None => Err(ErrorBody {
                        code: code::NOT_FOUND,
                        message: format!("no session {session_id}"),
                    }),
                }
            }
            Method::Hello { .. } | Method::SessionSubscribe { .. } => Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: "unexpected method".into(),
            }),
        }
    }

    fn list(&self) -> Vec<SessionInfo> {
        let mut sessions: Vec<Arc<Session>> =
            self.sessions.lock().unwrap().values().cloned().collect();
        sessions.sort_by_key(|session| session.number);
        sessions.iter().map(|session| session.info()).collect()
    }

    async fn new_session(
        &self,
        command: Vec<String>,
        cwd: PathBuf,
        env: BTreeMap<String, String>,
    ) -> Result<SessionInfo, ErrorBody> {
        if command.is_empty() {
            return Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: "command is empty".into(),
            });
        }
        if !cwd.is_absolute() {
            return Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: format!("cwd {} is not absolute", cwd.display()),
            });
        }
        let number = self.next_session.fetch_add(1, Ordering::Relaxed);
        let (session, ready) = Session::spawn(number, command, cwd, env);
        self.sessions
            .lock()
            .unwrap()
            .insert(session.id.clone(), session.clone());
        let result = match tokio::time::timeout(self.startup_timeout, ready).await {
            Ok(Ok(result)) => result,
            Ok(Err(_)) => Err("session task ended".into()),
            Err(_) => Err(format!(
                "agent did not start within {} ms",
                self.startup_timeout.as_millis()
            )),
        };
        if let Err(message) = result {
            self.sessions.lock().unwrap().remove(&session.id);
            session.close();
            return Err(ErrorBody {
                code: code::AGENT,
                message,
            });
        }
        Ok(session.info())
    }
}

fn to_value(value: impl serde::Serialize) -> Value {
    serde_json::to_value(value).expect("protocol types serialize")
}
