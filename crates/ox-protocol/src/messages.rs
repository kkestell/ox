use std::collections::BTreeMap;
use std::path::PathBuf;

use agent_client_protocol::v2::schema::{
    ContentBlock, PermissionOption, PromptCapabilities, RequestPermissionOutcome,
    RequestPermissionSubject, SessionConfigOption, SessionUpdate, StopReason,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// Version of the client–daemon protocol, exchanged in `hello`.
pub const PROTOCOL_VERSION: u32 = 1;

/// A client request: `{"id": n, "method": "...", "params": {...}}`.
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Request {
    pub id: u64,
    #[serde(flatten)]
    pub method: Method,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "method", content = "params")]
pub enum Method {
    #[serde(rename = "hello")]
    Hello { protocol_version: u32 },
    #[serde(rename = "session.list")]
    SessionList,
    /// Starts a watch: after the answer, the daemon sends the workspaces and
    /// sessions lists at once and again each time either differs from the
    /// last it sent. The request's `id` names the watch in every list.
    #[serde(rename = "watch")]
    Watch,
    /// Adds a workspace. `path` must be an absolute path to a directory that
    /// no workspace has.
    #[serde(rename = "workspace.add")]
    WorkspaceAdd { path: PathBuf },
    /// Removes a workspace. Refused while any session, exited or not,
    /// belongs to it.
    #[serde(rename = "workspace.remove")]
    WorkspaceRemove { workspace_id: String },
    #[serde(rename = "workspace.list")]
    WorkspaceList,
    /// The workspace's local and remote-tracking branches, for naming the
    /// branch of a new worktree. Refused outside a Git working tree. With
    /// `fetch`, the daemon first runs `git fetch --all`, shared with any
    /// fetch of the same repository already running and stopped after 60
    /// seconds; a fetch that fails sets `fetch_error` and the lists are
    /// answered as they stand. Git runs with `env` over the daemon's
    /// environment, as for `session.new`.
    #[serde(rename = "workspace.branches")]
    WorkspaceBranches {
        workspace_id: String,
        #[serde(default)]
        fetch: bool,
        #[serde(default)]
        env: BTreeMap<String, String>,
    },
    /// Starts an agent in the workspace's directory or in a new worktree of
    /// its Git repository, as `checkout` says. The result is the session in
    /// state `starting`; its `agent_started` or `agent_exited` event
    /// follows. A worktree's branch is resolved and created before the
    /// answer, and the worktree is added during the session's startup. Git
    /// runs with `env` over the daemon's environment, as the agent does.
    #[serde(rename = "session.new")]
    SessionNew {
        workspace_id: String,
        command: Vec<String>,
        #[serde(default)]
        env: BTreeMap<String, String>,
        #[serde(default)]
        checkout: Checkout,
    },
    /// Restarts the agent of an exited session that has an
    /// `agent_session_id`, from the session's command and directory, which
    /// must still exist. The
    /// result is the session in state `starting`. The agent replays the
    /// conversation before its `agent_started` event. A session with no
    /// stored events records the replay as `update` events; a session that has
    /// them, including one restored after an `oxd` restart, keeps them and
    /// drops the replay.
    #[serde(rename = "session.load")]
    SessionLoad {
        session_id: String,
        #[serde(default)]
        env: BTreeMap<String, String>,
    },
    #[serde(rename = "session.prompt")]
    SessionPrompt {
        session_id: String,
        /// ACP content blocks. Blocks other than text and resource links
        /// need the matching prompt capability of the agent.
        prompt: Vec<ContentBlock>,
    },
    #[serde(rename = "session.cancel")]
    SessionCancel { session_id: String },
    /// Starts a subscription: after the answer, the daemon sends the
    /// session's events with `seq > after_seq`, from the daemon's database
    /// for a session restored after an `oxd` restart, then each later event. The
    /// request's `id` names the subscription in every event it delivers. A
    /// subscription ends with the session's `session_closed` event. An
    /// `after_seq` that no event will follow is refused.
    #[serde(rename = "session.subscribe")]
    SessionSubscribe {
        session_id: String,
        #[serde(default)]
        after_seq: u64,
    },
    /// Ends the subscription or watch that request `subscription` started. Messages the daemon was sending it may arrive after the
    /// answer.
    #[serde(rename = "unsubscribe")]
    Unsubscribe { subscription: u64 },
    #[serde(rename = "permission.respond")]
    PermissionRespond {
        session_id: String,
        request_id: String,
        option_id: String,
    },
    #[serde(rename = "session.set_config_option")]
    SessionSetConfigOption {
        session_id: String,
        config_id: String,
        value: ConfigValue,
    },
    /// Stops the session's agent and deletes the session. A session in a
    /// worktree waits for its startup to finish, then has its worktree
    /// removed. The removal is refused with `UNSAVED_WORK`, leaving the
    /// session as it was, while the worktree has uncommitted changes,
    /// commits on no branch, or submodule commits on no remote branch,
    /// unless `discard` is set. A branch the daemon created is then deleted
    /// if all its commits are on another branch, tag or remote branch, and
    /// kept otherwise; a branch that existed before the session is always
    /// kept. Git runs with `env` over the daemon's environment, as for
    /// `session.new`. The result is [`Deleted`].
    #[serde(rename = "session.delete")]
    SessionDelete {
        session_id: String,
        #[serde(default)]
        discard: bool,
        #[serde(default)]
        env: BTreeMap<String, String>,
    },
}

/// Where a new session runs.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum Checkout {
    /// The workspace's directory.
    #[default]
    Workspace,
    /// A new worktree of the workspace's repository, in the same
    /// subdirectory the workspace is of its checkout. With no `branch`, the
    /// worktree is on a new branch `ox/<first 8 digits of the session ID>`.
    /// A `branch` that exists locally is checked out; one that exists only
    /// on a remote, named `<branch>` or `<remote>/<branch>`, becomes a local
    /// branch tracking it; any other name is a new branch. A new branch
    /// starts at `start`, `HEAD` when unset, and has no upstream. `start` is
    /// refused for any other branch.
    Worktree {
        #[serde(default)]
        branch: Option<String>,
        #[serde(default)]
        start: Option<String>,
    },
}

/// A session's worktree.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct WorktreeInfo {
    /// The worktree's top level. The session's `cwd` is this directory or a
    /// subdirectory of it.
    pub path: PathBuf,
    pub branch: String,
    /// The commit the session started at.
    pub base: String,
    /// Whether the daemon created the branch for the session.
    pub created_branch: bool,
}

/// The result of `session.delete`.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Deleted {
    /// The branch the daemon created for the session's worktree, kept
    /// because it has commits on no other branch, tag or remote branch.
    pub kept_branch: Option<String>,
}

/// A value for a session config option: the value id of a select option, or
/// a boolean.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ConfigValue {
    Boolean(bool),
    Id(String),
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct HelloResult {
    pub protocol_version: u32,
    pub daemon_version: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SessionState {
    Starting,
    Idle,
    Running,
    AwaitingPermission,
    Exited,
}

impl SessionState {
    /// The state's name on the wire.
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Starting => "starting",
            Self::Idle => "idle",
            Self::Running => "running",
            Self::AwaitingPermission => "awaiting_permission",
            Self::Exited => "exited",
        }
    }
}

/// A directory the user added, which sessions run in.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct WorkspaceInfo {
    pub id: String,
    pub path: PathBuf,
    /// Whether the directory looks like it is in a Git working tree: it is
    /// not inside a `.git` directory, and it or a parent has a `.git`. Only
    /// then can a session run in a worktree of it. The daemon works this out
    /// each time it sends the workspace, without running git.
    pub git_working_tree: bool,
}

/// A workspace's branches, each list most recently committed first.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Branches {
    /// Local branch names, such as `main`.
    pub local: Vec<String>,
    /// Remote-tracking branch names with their remote, such as
    /// `origin/main`, without symbolic refs such as `origin/HEAD`.
    pub remote: Vec<String>,
    /// Why the fetch the request asked for failed, if it did.
    pub fetch_error: Option<String>,
}

/// What a watch delivers: the daemon's workspaces, in the order they were
/// added, and its sessions, in the order they were created.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Listing {
    pub workspaces: Vec<WorkspaceInfo>,
    pub sessions: Vec<SessionInfo>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SessionInfo {
    pub id: String,
    pub workspace_id: String,
    pub command: Vec<String>,
    pub cwd: PathBuf,
    pub state: SessionState,
    /// The title the agent reported in a `session_info_update`.
    pub title: Option<String>,
    /// The agent's ID for the session, which `session.load` resumes. Unset
    /// until the session has started.
    pub agent_session_id: Option<String>,
    /// The content the agent accepts in prompts. Empty until the session has
    /// started.
    pub prompt_capabilities: PromptCapabilities,
    /// The agent's current config options, such as model and mode.
    pub config_options: Vec<SessionConfigOption>,
    /// The worktree the session runs in, if it has one. Unset again when
    /// the worktree could not be added.
    pub worktree: Option<WorktreeInfo>,
}

/// One entry in a session's history. `seq` starts at 1 and increases by one
/// per event within a session.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Event {
    pub session_id: String,
    pub seq: u64,
    pub kind: EventKind,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", content = "data", rename_all = "snake_case")]
#[allow(
    clippy::large_enum_variant,
    reason = "most events are updates, so boxing them adds an allocation to nearly every event"
)]
pub enum EventKind {
    /// The agent answered `initialize` and `session/new`. It is the first
    /// event of a session that starts.
    AgentStarted {
        agent_session_id: String,
        prompt_capabilities: PromptCapabilities,
        config_options: Vec<SessionConfigOption>,
    },
    PromptStarted {
        prompt: Vec<ContentBlock>,
    },
    /// The agent reported work no prompt asked for, as Claude does when a
    /// background task it started reports back.
    TurnStarted,
    /// An ACP `session/update` notification, unchanged. When an agent answers
    /// `session.set_config_option` with options it has not reported in a
    /// notification, the daemon adds a `config_option_update` with them.
    Update(SessionUpdate),
    /// A `session/request_permission` request, unchanged. A tool call
    /// subject carries the tool call's details, which a client applies as it
    /// does a `tool_call_update`.
    PermissionRequested {
        request_id: String,
        title: String,
        description: Option<String>,
        subject: Option<RequestPermissionSubject>,
        options: Vec<PermissionOption>,
    },
    PermissionResolved {
        request_id: String,
        outcome: RequestPermissionOutcome,
    },
    /// A turn finished. `stop_reason` is the one the agent reported with its
    /// `idle` state, if any. `error` is set when the agent rejected the
    /// prompt, exited, or the session was closed during the turn.
    TurnEnded {
        stop_reason: Option<StopReason>,
        error: Option<String>,
    },
    AgentExited {
        error: Option<String>,
    },
    /// The daemon stopped the agent because the session was idle, with no
    /// background work, and unviewed. The session is `exited`;
    /// `session.load` resumes it.
    AgentParked,
    /// The session was closed. It is the last event of a session.
    SessionClosed,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct ErrorBody {
    pub code: i32,
    pub message: String,
}

/// Error codes carried in [`ErrorBody::code`].
pub mod code {
    pub const INVALID_REQUEST: i32 = 1;
    pub const PROTOCOL_MISMATCH: i32 = 2;
    pub const NOT_FOUND: i32 = 3;
    pub const INVALID_STATE: i32 = 4;
    pub const AGENT: i32 = 5;
    /// The daemon could not read or write its database.
    pub const STORAGE: i32 = 6;
    /// Deleting the session would lose work in its worktree, which the
    /// message lists. Deleting it with `discard` set loses that work.
    pub const UNSAVED_WORK: i32 = 7;
    /// A git command failed. The message carries git's error output.
    pub const GIT: i32 = 8;
}

/// A daemon message: a response to request `id`, or an event or listing
/// delivered to the subscription or watch that request `subscription`
/// started.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ServerMessage {
    Error {
        id: u64,
        error: ErrorBody,
    },
    Result {
        id: u64,
        result: Value,
    },
    Event {
        subscription: u64,
        event: Box<Event>,
    },
    List {
        subscription: u64,
        workspaces: Vec<WorkspaceInfo>,
        sessions: Vec<SessionInfo>,
    },
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn request_round_trip() {
        let request: Request =
            serde_json::from_value(json!({"id": 3, "method": "session.list"})).unwrap();
        assert!(matches!(request.method, Method::SessionList));

        let request: Request = serde_json::from_value(json!({
            "id": 4,
            "method": "session.subscribe",
            "params": {"session_id": "s", "after_seq": 7}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionSubscribe { ref session_id, after_seq: 7 } if session_id == "s"
        ));

        let request: Request = serde_json::from_value(json!({
            "id": 8,
            "method": "unsubscribe",
            "params": {"subscription": 4}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::Unsubscribe { subscription: 4 }
        ));
        let value = serde_json::to_value(Request {
            id: 9,
            method: Method::Watch,
        })
        .unwrap();
        assert_eq!(value, json!({"id": 9, "method": "watch"}));
        let request: Request = serde_json::from_value(value).unwrap();
        assert!(matches!(request.method, Method::Watch));

        let request: Request = serde_json::from_value(json!({
            "id": 10,
            "method": "workspace.add",
            "params": {"path": "/work"}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::WorkspaceAdd { ref path } if path == std::path::Path::new("/work")
        ));
        let request: Request = serde_json::from_value(json!({
            "id": 11,
            "method": "session.new",
            "params": {"workspace_id": "w", "command": ["agent"]}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionNew { ref workspace_id, ref env, checkout: Checkout::Workspace, .. }
                if workspace_id == "w" && env.is_empty()
        ));
        let method = Method::SessionNew {
            workspace_id: "w".into(),
            command: vec!["agent".into()],
            env: BTreeMap::new(),
            checkout: Checkout::Worktree {
                branch: Some("feature".into()),
                start: None,
            },
        };
        let value = serde_json::to_value(Request { id: 13, method }).unwrap();
        assert_eq!(
            value["params"]["checkout"],
            json!({"type": "worktree", "branch": "feature", "start": null})
        );
        let request: Request = serde_json::from_value(value).unwrap();
        assert!(matches!(
            request.method,
            Method::SessionNew {
                checkout: Checkout::Worktree { branch: Some(ref branch), start: None },
                ..
            } if branch == "feature"
        ));
        let request: Request = serde_json::from_value(json!({
            "id": 14,
            "method": "session.new",
            "params": {"workspace_id": "w", "command": ["agent"], "checkout": {"type": "worktree"}}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionNew {
                checkout: Checkout::Worktree {
                    branch: None,
                    start: None
                },
                ..
            }
        ));
        let request: Request = serde_json::from_value(json!({
            "id": 16,
            "method": "session.delete",
            "params": {"session_id": "s"}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionDelete { ref session_id, discard: false, ref env }
                if session_id == "s" && env.is_empty()
        ));
        let request: Request = serde_json::from_value(json!({
            "id": 15,
            "method": "workspace.branches",
            "params": {"workspace_id": "w"}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::WorkspaceBranches { ref workspace_id, fetch: false, ref env }
                if workspace_id == "w" && env.is_empty()
        ));
        let method = Method::WorkspaceBranches {
            workspace_id: "w".into(),
            fetch: true,
            env: BTreeMap::from([("PATH".into(), "/bin".into())]),
        };
        let value = serde_json::to_value(Request { id: 16, method }).unwrap();
        assert_eq!(
            value,
            json!({
                "id": 16,
                "method": "workspace.branches",
                "params": {"workspace_id": "w", "fetch": true, "env": {"PATH": "/bin"}}
            })
        );
        let branches = Branches {
            local: vec!["main".into()],
            remote: vec!["origin/main".into()],
            fetch_error: Some("no network".into()),
        };
        let value = serde_json::to_value(&branches).unwrap();
        assert_eq!(
            value,
            json!({"local": ["main"], "remote": ["origin/main"], "fetch_error": "no network"})
        );
        assert_eq!(serde_json::from_value::<Branches>(value).unwrap(), branches);
        let request: Request = serde_json::from_value(json!({
            "id": 12,
            "method": "session.load",
            "params": {"session_id": "s"}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionLoad { ref session_id, .. } if session_id == "s"
        ));

        let request: Request = serde_json::from_value(json!({
            "id": 5,
            "method": "session.set_config_option",
            "params": {"session_id": "s", "config_id": "mode", "value": "plan"}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionSetConfigOption { value: ConfigValue::Id(ref id), .. } if id == "plan"
        ));
        let request: Request = serde_json::from_value(json!({
            "id": 6,
            "method": "session.set_config_option",
            "params": {"session_id": "s", "config_id": "fast", "value": true}
        }))
        .unwrap();
        assert!(matches!(
            request.method,
            Method::SessionSetConfigOption {
                value: ConfigValue::Boolean(true),
                ..
            }
        ));

        let value = serde_json::to_value(Request {
            id: 1,
            method: Method::Hello {
                protocol_version: 1,
            },
        })
        .unwrap();
        assert_eq!(
            value,
            json!({"id": 1, "method": "hello", "params": {"protocol_version": 1}})
        );
    }

    #[test]
    fn server_message_round_trip() {
        let event = Event {
            session_id: "s".into(),
            seq: 2,
            kind: EventKind::TurnEnded {
                stop_reason: Some(StopReason::EndTurn),
                error: None,
            },
        };
        let messages = [
            ServerMessage::Result {
                id: 1,
                result: Value::Null,
            },
            ServerMessage::Error {
                id: 2,
                error: ErrorBody {
                    code: code::NOT_FOUND,
                    message: "no".into(),
                },
            },
            ServerMessage::Event {
                subscription: 3,
                event: Box::new(event),
            },
            ServerMessage::List {
                subscription: 4,
                workspaces: Vec::new(),
                sessions: Vec::new(),
            },
        ];
        for message in messages {
            let line = serde_json::to_string(&message).unwrap();
            let parsed: ServerMessage = serde_json::from_str(&line).unwrap();
            assert_eq!(serde_json::to_string(&parsed).unwrap(), line);
        }

        let message: ServerMessage = serde_json::from_value(json!({
            "subscription": 5,
            "workspaces": [{"id": "w", "path": "/tmp", "git_working_tree": true}],
            "sessions": [{
                "id": "s",
                "workspace_id": "w",
                "command": ["agent"],
                "cwd": "/tmp",
                "state": "idle",
                "title": "Fix the build",
                "agent_session_id": "a",
                "prompt_capabilities": {},
                "config_options": [],
                "worktree": {
                    "path": "/worktrees/tmp-01234567",
                    "branch": "ox/01234567",
                    "base": "abc",
                    "created_branch": true
                }
            }]
        }))
        .unwrap();
        assert!(matches!(
            message,
            ServerMessage::List { subscription: 5, ref workspaces, ref sessions }
                if workspaces[0].id == "w"
                    && workspaces[0].git_working_tree
                    && sessions[0].workspace_id == "w"
                    && sessions[0].title.as_deref() == Some("Fix the build")
                    && sessions[0].worktree.as_ref().is_some_and(|worktree| {
                        worktree.branch == "ox/01234567" && worktree.created_branch
                    })
        ));
    }
}
