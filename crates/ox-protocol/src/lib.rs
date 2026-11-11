//! Wire types and client for oxd, the daemon that owns Ox's ACP agent
//! sessions.
//!
//! Messages are newline-delimited JSON over a Unix socket. Clients send
//! [`Request`]s; the daemon answers each with a [`ServerMessage`] carrying the
//! same `id`, and streams [`Event`]s for subscribed sessions.

#[cfg(unix)]
mod client;
mod messages;

#[cfg(unix)]
pub use client::{Client, Error, Subscription, daemon_binary, default_socket_path};
pub use messages::*;

pub use agent_client_protocol::v1::schema;
