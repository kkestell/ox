use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

use ox_protocol::{ErrorBody, Listing, Method, SessionInfo, SessionState, WorkspaceInfo, code};
use serde_json::Value;
use tokio::sync::watch;

use crate::session::Session;
use crate::store::{SavedSession, Store};

struct Registry {
    workspaces: Vec<WorkspaceInfo>,
    sessions: HashMap<String, Arc<Session>>,
    clients: usize,
}

pub struct Daemon {
    registry: Mutex<Registry>,
    store: Arc<Store>,
    /// Sent when a workspace or session is added or removed, a client
    /// connects or disconnects, or a session's `SessionInfo` changes.
    changed: watch::Sender<()>,
}

/// Counts one connected client for as long as it lives.
pub struct ClientGuard(Arc<Daemon>);

impl Drop for ClientGuard {
    fn drop(&mut self) {
        self.0.update(|registry| registry.clients -= 1);
    }
}

impl Daemon {
    /// A daemon with the workspaces and sessions `store` holds. Each saved
    /// session is `exited`, with no agent, until it loads. A saved session
    /// that never started has nothing to load, and is deleted.
    pub fn new(store: Store) -> rusqlite::Result<Self> {
        let store = Arc::new(store);
        store.remove_unstarted_sessions()?;
        let changed = watch::Sender::new(());
        let sessions = store
            .sessions()?
            .into_iter()
            .filter_map(|saved| Session::restore(store.clone(), changed.clone(), saved))
            .map(|session| (session.id.clone(), session))
            .collect();
        let registry = Registry {
            workspaces: store.workspaces()?,
            sessions,
            clients: 0,
        };
        Ok(Self {
            registry: Mutex::new(registry),
            store,
            changed,
        })
    }

    pub fn client(self: &Arc<Self>) -> ClientGuard {
        self.update(|registry| registry.clients += 1);
        ClientGuard(self.clone())
    }

    /// Resolves once the daemon has had no clients and no running agents for
    /// `idle_exit`. A session whose agent is not running, `exited`, is saved
    /// and does not keep the daemon alive.
    pub async fn idle(&self, idle_exit: Duration) {
        let mut changed = self.changed.subscribe();
        loop {
            // Marking the change seen before reading the registry means a
            // change made while it is read wakes this again.
            changed.borrow_and_update();
            if self.is_idle() {
                tokio::select! {
                    () = tokio::time::sleep(idle_exit) => return,
                    _ = changed.changed() => {}
                }
            } else {
                let _ = changed.changed().await;
            }
        }
    }

    fn is_idle(&self) -> bool {
        let registry = self.lock();
        registry.clients == 0
            && registry
                .sessions
                .values()
                .all(|session| session.state() == SessionState::Exited)
    }

    /// Stops every agent. The sessions stay saved.
    pub fn close_all(&self) {
        let mut sessions = Vec::new();
        self.update(|registry| sessions.extend(registry.sessions.drain().map(|(_, s)| s)));
        for session in sessions {
            session.close();
        }
    }

    /// A receiver that sees a change each time the lists may have changed.
    pub fn changed(&self) -> watch::Receiver<()> {
        self.changed.subscribe()
    }

    pub fn session(&self, id: &str) -> Result<Arc<Session>, ErrorBody> {
        self.lock()
            .sessions
            .get(id)
            .cloned()
            .ok_or_else(|| not_found("session", id))
    }

    /// Handles every method except `hello`, `session.subscribe`, `watch`, and
    /// `unsubscribe`, which belong to the connection.
    pub async fn handle(&self, method: Method) -> Result<Value, ErrorBody> {
        match method {
            Method::SessionList => Ok(to_value(self.list().sessions)),
            Method::WorkspaceAdd { path } => Ok(to_value(self.add_workspace(path)?)),
            Method::WorkspaceRemove { workspace_id } => {
                self.remove_workspace(&workspace_id)?;
                Ok(Value::Null)
            }
            Method::WorkspaceList => Ok(to_value(self.list().workspaces)),
            Method::SessionNew {
                workspace_id,
                command,
                env,
            } => Ok(to_value(self.new_session(&workspace_id, command, env)?)),
            Method::SessionLoad { session_id, env } => {
                let session = self.session(&session_id)?;
                session.load(env)?;
                Ok(to_value(session.info()))
            }
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
                self.close_session(&session_id)?;
                Ok(Value::Null)
            }
            Method::Hello { .. }
            | Method::SessionSubscribe { .. }
            | Method::Watch
            | Method::Unsubscribe { .. } => Err(ErrorBody {
                code: code::INVALID_REQUEST,
                message: "unexpected method".into(),
            }),
        }
    }

    fn lock(&self) -> MutexGuard<'_, Registry> {
        self.registry.lock().unwrap()
    }

    /// Changes the registry and signals the change.
    fn update<R>(&self, change: impl FnOnce(&mut Registry) -> R) -> R {
        let mut registry = self.lock();
        let result = change(&mut registry);
        self.changed.send_replace(());
        result
    }

    /// The workspaces in the order they were added and the sessions in the
    /// order they were created.
    pub fn list(&self) -> Listing {
        let registry = self.lock();
        let workspaces = registry.workspaces.clone();
        let mut sessions: Vec<Arc<Session>> = registry.sessions.values().cloned().collect();
        drop(registry);
        sessions.sort_by_key(|session| session.number);
        Listing {
            workspaces,
            sessions: sessions.iter().map(|session| session.info()).collect(),
        }
    }

    fn add_workspace(&self, path: PathBuf) -> Result<WorkspaceInfo, ErrorBody> {
        if !path.is_absolute() {
            return Err(invalid_request(format!(
                "{} is not an absolute path",
                path.display()
            )));
        }
        if !path.is_dir() {
            return Err(invalid_request(format!(
                "{} is not a directory",
                path.display()
            )));
        }
        let workspace = WorkspaceInfo {
            id: uuid::Uuid::new_v4().to_string(),
            path,
        };
        self.update(|registry| {
            if !self
                .store
                .add_workspace(&workspace)
                .map_err(|error| storage(&error))?
            {
                return Err(invalid_request(format!(
                    "{} is already a workspace",
                    workspace.path.display()
                )));
            }
            registry.workspaces.push(workspace.clone());
            Ok(workspace)
        })
    }

    fn remove_workspace(&self, id: &str) -> Result<(), ErrorBody> {
        self.update(|registry| {
            let index = registry
                .workspaces
                .iter()
                .position(|workspace| workspace.id == id)
                .ok_or_else(|| not_found("workspace", id))?;
            if registry
                .sessions
                .values()
                .any(|session| session.workspace_id() == id)
            {
                return Err(ErrorBody {
                    code: code::INVALID_STATE,
                    message: "the workspace has sessions".into(),
                });
            }
            self.store
                .remove_workspace(id)
                .map_err(|error| storage(&error))?;
            registry.workspaces.remove(index);
            Ok(())
        })
    }

    fn new_session(
        &self,
        workspace_id: &str,
        command: Vec<String>,
        env: BTreeMap<String, String>,
    ) -> Result<SessionInfo, ErrorBody> {
        if command.is_empty() {
            return Err(invalid_request("command is empty".into()));
        }
        self.update(|registry| {
            let workspace = registry
                .workspaces
                .iter()
                .find(|workspace| workspace.id == workspace_id)
                .ok_or_else(|| not_found("workspace", workspace_id))?;
            let id = uuid::Uuid::new_v4().to_string();
            let number = self
                .store
                .add_session(&id, workspace_id, &command, &workspace.path)
                .map_err(|error| storage(&error))?;
            let saved = SavedSession {
                number,
                id,
                workspace_id: workspace_id.into(),
                command,
                cwd: workspace.path.clone(),
                title: None,
                agent_session_id: None,
            };
            let session = Session::spawn(self.store.clone(), self.changed.clone(), saved, env);
            let info = session.info();
            registry.sessions.insert(session.id.clone(), session);
            Ok(info)
        })
    }

    /// Deletes the session's row, then stops its agent and forgets it.
    fn close_session(&self, id: &str) -> Result<(), ErrorBody> {
        let session = self.update(|registry| {
            if !registry.sessions.contains_key(id) {
                return Err(not_found("session", id));
            }
            self.store
                .remove_session(id)
                .map_err(|error| storage(&error))?;
            Ok(registry
                .sessions
                .remove(id)
                .expect("the session was just found"))
        })?;
        session.close();
        Ok(())
    }
}

fn invalid_request(message: String) -> ErrorBody {
    ErrorBody {
        code: code::INVALID_REQUEST,
        message,
    }
}

fn not_found(kind: &str, id: &str) -> ErrorBody {
    ErrorBody {
        code: code::NOT_FOUND,
        message: format!("no {kind} {id}"),
    }
}

fn storage(error: &rusqlite::Error) -> ErrorBody {
    ErrorBody {
        code: code::STORAGE,
        message: format!("the database failed: {error}"),
    }
}

fn to_value(value: impl serde::Serialize) -> Value {
    serde_json::to_value(value).expect("protocol types serialize")
}
