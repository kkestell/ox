use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

use ox_protocol::{ErrorBody, Method, SessionInfo, code};
use serde_json::Value;
use tokio::sync::watch;

use crate::session::Session;

#[derive(Default)]
struct Registry {
    sessions: HashMap<String, Arc<Session>>,
    clients: usize,
}

pub struct Daemon {
    registry: Mutex<Registry>,
    /// Whether the daemon has no sessions and no clients. Updated under the
    /// registry lock.
    idle: watch::Sender<bool>,
    /// Sent when a session is added or removed, or a session's `SessionInfo`
    /// changes.
    sessions_changed: watch::Sender<()>,
    next_session: AtomicU64,
}

/// Counts one connected client for as long as it lives.
pub struct ClientGuard(Arc<Daemon>);

impl Drop for ClientGuard {
    fn drop(&mut self) {
        self.0.update(|registry| registry.clients -= 1);
    }
}

impl Daemon {
    pub fn new() -> Self {
        Self {
            registry: Mutex::default(),
            idle: watch::Sender::new(true),
            sessions_changed: watch::Sender::new(()),
            next_session: AtomicU64::new(0),
        }
    }

    pub fn client(self: &Arc<Self>) -> ClientGuard {
        self.update(|registry| registry.clients += 1);
        ClientGuard(self.clone())
    }

    /// Resolves once the daemon has had no sessions and no clients for
    /// `idle_exit`.
    pub async fn idle(&self, idle_exit: Duration) {
        let mut idle = self.idle.subscribe();
        loop {
            if *idle.borrow_and_update() {
                tokio::select! {
                    () = tokio::time::sleep(idle_exit) => return,
                    _ = idle.changed() => {}
                }
            } else {
                let _ = idle.changed().await;
            }
        }
    }

    /// Closes every session, killing the agents.
    pub fn close_all(&self) {
        let mut sessions = Vec::new();
        self.update(|registry| sessions.extend(registry.sessions.drain().map(|(_, s)| s)));
        for session in sessions {
            session.close();
        }
    }

    /// A receiver that sees a change each time the sessions list may have
    /// changed.
    pub fn sessions_changed(&self) -> watch::Receiver<()> {
        self.sessions_changed.subscribe()
    }

    pub fn session(&self, id: &str) -> Result<Arc<Session>, ErrorBody> {
        self.lock()
            .sessions
            .get(id)
            .cloned()
            .ok_or_else(|| not_found(id))
    }

    /// Handles every method except `hello`, `session.subscribe`,
    /// `sessions.watch`, and `unsubscribe`, which belong to the connection.
    pub async fn handle(&self, method: Method) -> Result<Value, ErrorBody> {
        match method {
            Method::SessionList => Ok(to_value(self.list())),
            Method::SessionNew { command, cwd, env } => {
                Ok(to_value(self.new_session(command, cwd, env, None)?))
            }
            Method::SessionLoad {
                command,
                cwd,
                env,
                agent_session_id,
            } => Ok(to_value(self.new_session(
                command,
                cwd,
                env,
                Some(agent_session_id),
            )?)),
            Method::SessionPrompt { session_id, prompt } => {
                self.session(&session_id)?.prompt(prompt)?;
                Ok(Value::Null)
            }
            Method::SessionSetConfigOption {
                session_id,
                config_id,
                value,
            } => {
                let session = self.session(&session_id)?;
                Ok(to_value(session.set_config_option(config_id, value).await?))
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
                let mut session = None;
                self.update(|registry| session = registry.sessions.remove(&session_id));
                session.ok_or_else(|| not_found(&session_id))?.close();
                Ok(Value::Null)
            }
            Method::Hello { .. }
            | Method::SessionSubscribe { .. }
            | Method::SessionsWatch
            | Method::Unsubscribe { .. } => Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: "unexpected method".into(),
            }),
        }
    }

    fn lock(&self) -> MutexGuard<'_, Registry> {
        self.registry.lock().unwrap()
    }

    /// Changes the registry, publishes whether the daemon is idle, and
    /// signals the sessions list if a session was added or removed.
    fn update(&self, change: impl FnOnce(&mut Registry)) {
        let mut registry = self.lock();
        let sessions = registry.sessions.len();
        change(&mut registry);
        if registry.sessions.len() != sessions {
            self.sessions_changed.send_replace(());
        }
        let idle = registry.clients == 0 && registry.sessions.is_empty();
        self.idle.send_if_modified(|current| {
            let modified = *current != idle;
            *current = idle;
            modified
        });
    }

    /// Every session, in the order they were created.
    pub fn list(&self) -> Vec<SessionInfo> {
        let mut sessions: Vec<Arc<Session>> = self.lock().sessions.values().cloned().collect();
        sessions.sort_by_key(|session| session.number);
        sessions.iter().map(|session| session.info()).collect()
    }

    fn new_session(
        &self,
        command: Vec<String>,
        cwd: PathBuf,
        env: BTreeMap<String, String>,
        load: Option<String>,
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
        let session = Session::spawn(
            number,
            command,
            cwd,
            env,
            load,
            self.sessions_changed.clone(),
        );
        let info = session.info();
        self.update(|registry| {
            registry.sessions.insert(session.id.clone(), session);
        });
        Ok(info)
    }
}

fn not_found(id: &str) -> ErrorBody {
    ErrorBody {
        code: code::NOT_FOUND,
        message: format!("no session {id}"),
    }
}

fn to_value(value: impl serde::Serialize) -> Value {
    serde_json::to_value(value).expect("protocol types serialize")
}
