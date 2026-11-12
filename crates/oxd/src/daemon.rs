use std::collections::hash_map::Entry;
use std::collections::{BTreeMap, HashMap};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

use ox_protocol::{
    Branches, Checkout, Deleted, ErrorBody, Listing, Method, SessionInfo, SessionState,
    WorkspaceInfo, code,
};
use serde_json::Value;
use tokio::sync::{OwnedMutexGuard, watch};

use crate::branches;
use crate::session::Session;
use crate::state::Change;
use crate::store::{SavedSession, SavedWorkspace, SavedWorktree, Store};
use crate::worktree::{self, Git, WorktreePlan};

/// The outcome of a fetch, once it has one.
type FetchOutcome = Option<Result<(), String>>;

struct Registry {
    workspaces: Vec<SavedWorkspace>,
    sessions: HashMap<String, Arc<Session>>,
    clients: usize,
    /// Whether the startup cleanup is still running.
    cleaning: bool,
}

pub struct Daemon {
    registry: Mutex<Registry>,
    store: Arc<Store>,
    /// The directory that holds sessions' worktrees.
    worktrees_dir: PathBuf,
    /// How long a session stays idle, with no background work, and unviewed
    /// before its agent is parked.
    park_after: Duration,
    /// Sent when a workspace or session is added or removed, a client
    /// connects or disconnects, or a session's `SessionInfo` changes.
    changed: watch::Sender<()>,
    /// The fetches running, by the common Git directory of the repository
    /// each fetches, so that requests for one repository share one fetch.
    fetches: Arc<Mutex<HashMap<PathBuf, watch::Receiver<FetchOutcome>>>>,
    /// The sessions that never started and have a worktree to remove, each
    /// with its lifecycle guard, until `clean_up` takes them.
    unstarted: Mutex<Vec<(Arc<Session>, OwnedMutexGuard<()>)>>,
}

/// Counts one connected client for as long as it lives.
pub struct ClientGuard(Arc<Daemon>);

impl Drop for ClientGuard {
    fn drop(&mut self) {
        self.0.update(|registry| registry.clients -= 1);
    }
}

impl Daemon {
    /// A daemon with the workspaces and sessions `store` holds, which adds
    /// sessions' worktrees in `worktrees_dir`, and parks the agent of a session
    /// that has been idle, with no background work, and unviewed for
    /// `park_after`. Each saved session is
    /// `exited`, with no agent, until it loads. A saved session that never
    /// started has nothing to load. One without a worktree is deleted; one
    /// with a worktree waits, `exited`, for `clean_up`.
    pub fn new(
        store: Store,
        worktrees_dir: PathBuf,
        park_after: Duration,
    ) -> rusqlite::Result<Self> {
        let store = Arc::new(store);
        let changed = watch::Sender::new(());
        let mut sessions = HashMap::new();
        let mut unstarted = Vec::new();
        let saved_sessions = store.sessions()?;
        for saved in saved_sessions {
            let started = saved.agent_session_id.is_some();
            if !started && saved.worktree.is_none() {
                store.remove_session(&saved.id)?;
                continue;
            }
            let session = Session::restore(store.clone(), changed.clone(), park_after, saved);
            if !started {
                let guard = session.hold_lifecycle();
                unstarted.push((session.clone(), guard));
            }
            sessions.insert(session.id.clone(), session);
        }
        let registry = Registry {
            workspaces: store.workspaces()?,
            sessions,
            clients: 0,
            cleaning: !unstarted.is_empty(),
        };
        Ok(Self {
            registry: Mutex::new(registry),
            store,
            worktrees_dir,
            park_after,
            changed,
            fetches: Arc::default(),
            unstarted: Mutex::new(unstarted),
        })
    }

    /// Removes the worktrees and branches of the sessions that never
    /// started, one at a time, with the daemon's own environment, as a delete
    /// without `discard` does, and deletes those sessions. A session whose
    /// worktree would lose work, or cannot be removed, stays to be deleted by
    /// the user. The daemon is not idle until this ends.
    pub async fn clean_up(&self) {
        let unstarted = std::mem::take(&mut *self.unstarted.lock().unwrap());
        for (session, _lifecycle) in unstarted {
            let worktree = session
                .worktree()
                .expect("an unstarted session waiting for cleanup has a worktree");
            match remove_unstarted(&worktree).await {
                Ok(()) => {
                    if let Err(error) = self.forget(&session.id) {
                        eprintln!(
                            "oxd: deleting session {}, which never started: {}",
                            session.id, error.message
                        );
                        continue;
                    }
                    session.close();
                }
                Err(error) => eprintln!(
                    "oxd: keeping session {}, which never started: {}",
                    session.id, error.message
                ),
            }
        }
        self.update(|registry| registry.cleaning = false);
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
        !registry.cleaning
            && registry.clients == 0
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

    /// Returns once every recorded event is in the database.
    pub async fn flush(&self) {
        self.store.flush().await;
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
            Method::WorkspaceBranches {
                workspace_id,
                fetch,
                env,
            } => Ok(to_value(self.branches(&workspace_id, fetch, env).await?)),
            Method::SessionNew {
                workspace_id,
                command,
                env,
                checkout,
            } => Ok(to_value(
                self.new_session(&workspace_id, command, env, checkout)
                    .await?,
            )),
            Method::SessionLoad { session_id, env } => {
                let session = self.session(&session_id)?;
                let _lifecycle = self.lifecycle(&session).await?;
                session.history().await?;
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
            Method::SessionDelete {
                session_id,
                discard,
                env,
            } => Ok(to_value(
                self.delete_session(&session_id, discard, env).await?,
            )),
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
    /// order they were created. Whether each workspace is in a Git working
    /// tree is worked out now.
    pub fn list(&self) -> Listing {
        let registry = self.lock();
        let workspaces = registry.workspaces.clone();
        let mut sessions: Vec<Arc<Session>> = registry.sessions.values().cloned().collect();
        drop(registry);
        sessions.sort_by_key(|session| session.number);
        Listing {
            workspaces: workspaces.into_iter().map(workspace_info).collect(),
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
        let workspace = SavedWorkspace {
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
            Ok(workspace_info(workspace))
        })
    }

    fn remove_workspace(&self, id: &str) -> Result<(), ErrorBody> {
        self.update(|registry| {
            let index = registry
                .workspaces
                .iter()
                .position(|workspace| workspace.id == id)
                .ok_or_else(|| not_found("workspace", id))?;
            // The store has the rows of sessions still being created.
            if self
                .store
                .workspace_has_sessions(id)
                .map_err(|error| storage(&error))?
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

    /// Creates a session in the workspace's directory or in a new worktree.
    /// For a worktree, the branch is resolved and created here, and the
    /// session's startup adds the worktree.
    async fn new_session(
        &self,
        workspace_id: &str,
        command: Vec<String>,
        env: BTreeMap<String, String>,
        checkout: Checkout,
    ) -> Result<SessionInfo, ErrorBody> {
        if command.is_empty() {
            return Err(invalid_request("command is empty".into()));
        }
        let id = uuid::Uuid::new_v4().to_string();
        let Checkout::Worktree { branch, start } = checkout else {
            return self.add_session(id, workspace_id, command, env, &mut None);
        };
        let workspace = self.workspace_path(workspace_id)?;
        let git = Git::for_client(env.clone());
        let plan =
            worktree::plan(&git, &workspace, branch, start, &self.worktrees_dir, &id).await?;
        worktree::create_branch(&git, &plan).await?;
        let mut worktree = Some((git, plan));
        let added = self.add_session(id, workspace_id, command, env, &mut worktree);
        if let Some((git, plan)) = worktree
            && let Err(error) = worktree::roll_back(&git, &plan, false).await
        {
            eprintln!("oxd: undoing a session that was not saved: {error}");
        }
        added
    }

    /// Saves a session, with its worktree if it has one, and spawns it,
    /// taking the worktree to add. A session that cannot be saved leaves the
    /// worktree where it is.
    fn add_session(
        &self,
        id: String,
        workspace_id: &str,
        command: Vec<String>,
        env: BTreeMap<String, String>,
        worktree: &mut Option<(Git, WorktreePlan)>,
    ) -> Result<SessionInfo, ErrorBody> {
        self.update(|registry| {
            let workspace = registry
                .workspaces
                .iter()
                .find(|workspace| workspace.id == workspace_id)
                .ok_or_else(|| not_found("workspace", workspace_id))?;
            let (cwd, info) = match worktree {
                Some((_, plan)) => (plan.cwd.clone(), Some(plan.saved())),
                None => (workspace.path.clone(), None),
            };
            let number = self
                .store
                .add_session(&id, workspace_id, &command, &cwd, info.as_ref())
                .map_err(|error| storage(&error))?;
            let saved = SavedSession {
                number,
                id,
                workspace_id: workspace_id.into(),
                command,
                cwd,
                title: None,
                agent_session_id: None,
                worktree: info,
                last_seq: 0,
            };
            let session = Session::spawn(
                self.store.clone(),
                self.changed.clone(),
                self.park_after,
                saved,
                env,
                worktree.take(),
            );
            let info = session.info();
            registry.sessions.insert(session.id.clone(), session);
            Ok(info)
        })
    }

    fn workspace_path(&self, id: &str) -> Result<PathBuf, ErrorBody> {
        self.lock()
            .workspaces
            .iter()
            .find(|workspace| workspace.id == id)
            .map(|workspace| workspace.path.clone())
            .ok_or_else(|| not_found("workspace", id))
    }

    /// The branches of the workspace's repository, listed after a fetch
    /// when `fetch` is set.
    async fn branches(
        &self,
        workspace_id: &str,
        fetch: bool,
        env: BTreeMap<String, String>,
    ) -> Result<Branches, ErrorBody> {
        let workspace = self.workspace_path(workspace_id)?;
        let git = Git::for_client(env);
        let common_dir = branches::common_dir(&git, &workspace).await?;
        let fetch_error = if fetch {
            self.fetch(&git, common_dir, &workspace).await.err()
        } else {
            None
        };
        let (local, remote) = branches::list(&git, &workspace).await?;
        Ok(Branches {
            local,
            remote,
            fetch_error,
        })
    }

    /// Fetches the repository whose common Git directory is `common_dir`,
    /// from `workspace` with `git`, or waits for the fetch of it already
    /// running. The fetch runs in its own task, so it finishes even when
    /// every request waiting for it is dropped.
    async fn fetch(&self, git: &Git, common_dir: PathBuf, workspace: &Path) -> Result<(), String> {
        let mut outcome = match self.fetches.lock().unwrap().entry(common_dir) {
            Entry::Occupied(running) => running.get().clone(),
            Entry::Vacant(entry) => {
                let (sender, outcome) = watch::channel(None);
                let common_dir = entry.key().clone();
                entry.insert(outcome.clone());
                let fetches = self.fetches.clone();
                let git = git.clone();
                let workspace = workspace.to_owned();
                tokio::spawn(async move {
                    sender.send_replace(Some(branches::fetch(&git, &workspace).await));
                    fetches.lock().unwrap().remove(&common_dir);
                });
                outcome
            }
        };
        match outcome.wait_for(Option::is_some).await {
            Ok(outcome) => outcome.clone().expect("waited for an outcome"),
            Err(_) => Err("the fetch stopped without an outcome".into()),
        }
    }

    /// Waits until the session is not starting, loading or being deleted,
    /// and returns the guard that keeps it so. A session deleted meanwhile
    /// is not found.
    async fn lifecycle<'a>(
        &self,
        session: &'a Session,
    ) -> Result<tokio::sync::MutexGuard<'a, ()>, ErrorBody> {
        let guard = session.lifecycle().await;
        if !self.lock().sessions.contains_key(&session.id) {
            return Err(not_found("session", &session.id));
        }
        Ok(guard)
    }

    /// Deletes a session once its startup has finished. A session in a
    /// worktree has its agent stopped, then its worktree removed and the
    /// branch rule applied, with git run with `env`. A worktree that would
    /// lose work refuses the delete unless `discard` is set, leaving the
    /// session as it was, or `exited` when the work appeared after its agent
    /// stopped.
    async fn delete_session(
        &self,
        id: &str,
        discard: bool,
        env: BTreeMap<String, String>,
    ) -> Result<Deleted, ErrorBody> {
        let session = self.session(id)?;
        let _lifecycle = self.lifecycle(&session).await?;
        let mut kept_branch = None;
        if let Some(worktree) = session.worktree() {
            let git = Git::for_client(env);
            if !discard {
                let unsaved = worktree::unsaved(&git, &worktree).await?;
                if !unsaved.is_empty() {
                    return Err(worktree::unsaved_work(&unsaved));
                }
            }
            session.stop(Change::Exited(None)).await;
            worktree::remove_worktree(&git, &worktree, discard).await?;
            kept_branch = worktree::apply_branch_rule(&git, &worktree).await?;
        }
        self.forget(id)?;
        session.close();
        Ok(Deleted { kept_branch })
    }

    /// Deletes the session's rows and removes it from the registry.
    fn forget(&self, id: &str) -> Result<(), ErrorBody> {
        self.update(|registry| {
            self.store
                .remove_session(id)
                .map_err(|error| storage(&error))?;
            registry.sessions.remove(id);
            Ok(())
        })
    }
}

/// Removes the worktree and branch of a session that never started, with
/// the daemon's own environment, as a delete without `discard` would.
async fn remove_unstarted(worktree: &SavedWorktree) -> Result<(), ErrorBody> {
    let git = Git::own();
    worktree::remove_worktree(&git, worktree, false).await?;
    worktree::apply_branch_rule(&git, worktree).await?;
    Ok(())
}

/// The workspace as clients see it.
fn workspace_info(workspace: SavedWorkspace) -> WorkspaceInfo {
    WorkspaceInfo {
        git_working_tree: branches::git_working_tree(&workspace.path),
        id: workspace.id,
        path: workspace.path,
    }
}

pub(crate) fn invalid_request(message: String) -> ErrorBody {
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
