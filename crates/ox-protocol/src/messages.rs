use std::collections::BTreeMap;
use std::path::PathBuf;

use agent_client_protocol::v1::schema::{
    ContentBlock, PermissionOption, PromptCapabilities, RequestPermissionOutcome,
    SessionConfigOption, SessionUpdate, StopReason, ToolCallUpdate,
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
    /// Starts a sessions watch: after the answer, the daemon sends the
    /// sessions list at once and again each time it differs from the last
    /// list it sent. The request's `id` names the watch in every list.
    #[serde(rename = "sessions.watch")]
    SessionsWatch,
    /// Starts an agent. The result is the session in state `starting`; its
    /// `agent_started` or `agent_exited` event follows.
    #[serde(rename = "session.new")]
    SessionNew {
        command: Vec<String>,
        cwd: PathBuf,
        #[serde(default)]
        env: BTreeMap<String, String>,
    },
    /// Starts an agent on a session it has saved. The agent replays the
    /// conversation, which the session records as `update` events before its
    /// `agent_started` event.
    #[serde(rename = "session.load")]
    SessionLoad {
        command: Vec<String>,
        cwd: PathBuf,
        #[serde(default)]
        env: BTreeMap<String, String>,
        agent_session_id: String,
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
    /// session's events with `seq > after_seq`, then each later event. The
    /// request's `id` names the subscription in every event it delivers. A
    /// subscription ends with the session's `session_closed` event. An
    /// `after_seq` that no event will follow is refused.
    #[serde(rename = "session.subscribe")]
    SessionSubscribe {
        session_id: String,
        #[serde(default)]
        after_seq: u64,
    },
    /// Ends the subscription or sessions watch that request `subscription`
    /// started. Messages the daemon was sending it may arrive after the
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
    #[serde(rename = "session.close")]
    SessionClose { session_id: String },
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

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SessionInfo {
    pub id: String,
    pub command: Vec<String>,
    pub cwd: PathBuf,
    pub state: SessionState,
    /// The title the agent reported in a `session_info_update`.
    pub title: Option<String>,
    /// The agent's ID for the session, which `session.load` takes. Unset
    /// until the session has started.
    pub agent_session_id: Option<String>,
    /// The content the agent accepts in prompts. Empty until the session has
    /// started.
    pub prompt_capabilities: PromptCapabilities,
    /// The agent's current config options, such as model and mode.
    pub config_options: Vec<SessionConfigOption>,
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
    /// An ACP `session/update` notification, unchanged. When an agent answers
    /// `session.set_config_option` with options it has not reported in a
    /// notification, the daemon adds a `config_option_update` with them.
    Update(SessionUpdate),
    PermissionRequested {
        request_id: String,
        tool_call: ToolCallUpdate,
        options: Vec<PermissionOption>,
    },
    PermissionResolved {
        request_id: String,
        outcome: RequestPermissionOutcome,
    },
    /// The prompt turn finished. `stop_reason` is set when the agent answered
    /// the prompt and `error` when it answered with an error.
    TurnEnded {
        stop_reason: Option<StopReason>,
        error: Option<String>,
    },
    AgentExited {
        error: Option<String>,
    },
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
}

/// A daemon message: a response to request `id`, or an event or sessions
/// list delivered to the subscription or sessions watch that request
/// `subscription` started.
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
    Sessions {
        subscription: u64,
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
            method: Method::SessionsWatch,
        })
        .unwrap();
        assert_eq!(value, json!({"id": 9, "method": "sessions.watch"}));
        let request: Request = serde_json::from_value(value).unwrap();
        assert!(matches!(request.method, Method::SessionsWatch));

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
            ServerMessage::Sessions {
                subscription: 4,
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
            "sessions": [{
                "id": "s",
                "command": ["agent"],
                "cwd": "/tmp",
                "state": "idle",
                "title": "Fix the build",
                "agent_session_id": "a",
                "prompt_capabilities": {},
                "config_options": []
            }]
        }))
        .unwrap();
        assert!(matches!(
            message,
            ServerMessage::Sessions { subscription: 5, ref sessions }
                if sessions[0].title.as_deref() == Some("Fix the build")
        ));
    }
}
