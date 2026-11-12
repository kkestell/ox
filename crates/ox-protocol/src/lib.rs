//! Wire types and client for oxd, the daemon that owns Ox's ACP agent
//! sessions.
//!
//! Messages are newline-delimited JSON over a Unix socket. Clients send
//! [`Request`]s; the daemon answers each with a [`ServerMessage`] carrying the
//! same `id`. The daemon also pushes two kinds of stream, each named in every
//! message by the `id` of the request that started it: a subscription
//! (`session.subscribe`) delivers one session's [`Event`]s, and a watch
//! (`watch`) delivers the workspaces and sessions lists whenever either
//! changes. `unsubscribe` ends either kind.

#[cfg(unix)]
mod client;
mod messages;

#[cfg(unix)]
pub use client::{
    Client, End, Error, Subscription, Watch, daemon_binary, data_dir, default_socket_path,
};
#[cfg(unix)]
pub mod content;
pub use messages::*;

pub use agent_client_protocol::schema::MaybeUndefined;
pub use agent_client_protocol::v2::schema;
