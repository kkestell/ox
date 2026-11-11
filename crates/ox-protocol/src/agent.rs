//! The ACP extension an agent uses to tell the daemon when it is working.

use agent_client_protocol::JsonRpcNotification;
use agent_client_protocol::v1::schema::{SessionId, StopReason};
use serde::{Deserialize, Serialize};

/// Whether an agent is working on a session.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "state", rename_all = "snake_case")]
pub enum AgentState {
    Running,
    /// The agent stopped working. After a turn the agent started,
    /// `stop_reason` says how that turn ended.
    Idle {
        #[serde(
            default,
            rename = "stopReason",
            skip_serializing_if = "Option::is_none"
        )]
        stop_reason: Option<StopReason>,
    },
}

/// An agent's notice that it started or stopped working on a session. ACP
/// reports a turn only as the answer to a prompt, so an agent that also
/// starts turns by itself sends this each time its state changes. The
/// daemon takes `Running` outside a prompt's turn as the start of a turn
/// the agent started, and the next `Idle` as its end.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize, JsonRpcNotification)]
#[notification(method = "_ox/agent_state")]
#[serde(rename_all = "camelCase")]
pub struct AgentStateNotification {
    pub session_id: SessionId,
    #[serde(flatten)]
    pub state: AgentState,
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn idle_carries_the_stop_reason() {
        let notification = AgentStateNotification {
            session_id: SessionId::new("s"),
            state: AgentState::Idle {
                stop_reason: Some(StopReason::Cancelled),
            },
        };
        let value = serde_json::to_value(&notification).unwrap();
        assert_eq!(
            value,
            json!({"sessionId": "s", "state": "idle", "stopReason": "cancelled"})
        );
        assert_eq!(
            serde_json::from_value::<AgentStateNotification>(value).unwrap(),
            notification
        );
        let running: AgentStateNotification =
            serde_json::from_value(json!({"sessionId": "s", "state": "running"})).unwrap();
        assert_eq!(running.state, AgentState::Running);
    }
}
