use std::collections::BTreeMap;
use std::path::PathBuf;

use agent_client_protocol::v1::schema::{
    PermissionOption, RequestPermissionOutcome, SessionUpdate, StopReason, ToolCallUpdate,
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
    #[serde(rename = "session.new")]
    SessionNew {
        command: Vec<String>,
        cwd: PathBuf,
        #[serde(default)]
        env: BTreeMap<String, String>,
    },
    #[serde(rename = "session.prompt")]
    SessionPrompt { session_id: String, text: String },
    #[serde(rename = "session.cancel")]
    SessionCancel { session_id: String },
    #[serde(rename = "session.subscribe")]
    SessionSubscribe {
        session_id: String,
        #[serde(default)]
        after_seq: u64,
    },
    #[serde(rename = "permission.respond")]
    PermissionRespond {
        session_id: String,
        request_id: String,
        option_id: String,
    },
    #[serde(rename = "session.close")]
    SessionClose { session_id: String },
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

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SessionInfo {
    pub id: String,
    pub command: Vec<String>,
    pub cwd: PathBuf,
    pub state: SessionState,
    pub last_seq: u64,
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
    PromptStarted {
        text: String,
    },
    /// An ACP `session/update` notification, unchanged.
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

/// A daemon message: a response to request `id`, or an event of a
/// subscribed session.
#[derive(Clone, Debug, Serialize)]
#[serde(untagged)]
pub enum ServerMessage {
    Result { id: u64, result: Value },
    Error { id: u64, error: ErrorBody },
    Event { event: Box<Event> },
}

impl ServerMessage {
    pub fn parse(line: &str) -> serde_json::Result<Self> {
        let mut value: Value = serde_json::from_str(line)?;
        let object = value.as_object_mut().ok_or_else(|| {
            <serde_json::Error as serde::de::Error>::custom("expected a JSON object")
        })?;
        if let Some(event) = object.remove("event") {
            return Ok(Self::Event {
                event: serde_json::from_value(event)?,
            });
        }
        let id = object
            .get("id")
            .and_then(Value::as_u64)
            .ok_or_else(|| <serde_json::Error as serde::de::Error>::missing_field("id"))?;
        if let Some(error) = object.remove("error") {
            return Ok(Self::Error {
                id,
                error: serde_json::from_value(error)?,
            });
        }
        Ok(Self::Result {
            id,
            result: object.remove("result").unwrap_or(Value::Null),
        })
    }
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
                event: Box::new(event),
            },
        ];
        for message in messages {
            let line = serde_json::to_string(&message).unwrap();
            let parsed = ServerMessage::parse(&line).unwrap();
            assert_eq!(serde_json::to_string(&parsed).unwrap(), line);
        }
    }
}
