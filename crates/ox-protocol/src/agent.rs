//! The ACP extension an agent uses to tell the daemon when it is working.

use agent_client_protocol::JsonRpcNotification;
use agent_client_protocol::v1::schema::SessionId;
use serde::{Deserialize, Serialize};

/// Whether an agent is working on a session.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentState {
    Running,
    Idle,
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
    pub state: AgentState,
}
