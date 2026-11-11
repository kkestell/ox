# Architecture

Ox runs as a tree of processes. The desktop app and `oxctl` are clients of
`oxd`, a per-user daemon that owns every session's agent process. Sessions
outlive the clients that started them, so closing and reopening the app
reconnects to the same sessions.

```mermaid
flowchart TB
    ox["ox"]
    oxctl["oxctl"]
    oxd["oxd"]
    bridge["claude-code-acp<br/>(one per session)"]
    opencode["opencode acp<br/>(one per session)"]
    claude["claude<br/>(one per session)"]

    ox -.-> oxd
    oxctl -.-> oxd
    oxd --> bridge
    oxd --> opencode
    bridge --> claude
```

Solid arrows point from a parent to the processes it owns. Dotted arrows
start `oxd` detached, so it outlives the client that started it.

## Processes

**ox.** The Tauri 2 desktop app. The React UI in the webview calls Tauri commands
in the Rust core, which forwards them to `oxd` through one
`ox_protocol::Client` and streams the sessions list and the open session's
conversation back over Tauri channels. A new Claude session runs
`claude-code-acp` from beside the app's executable; a new OpenCode session
runs `opencode acp`. The app passes its environment to the session.

**oxctl.** A command-line client of `oxd` that speaks the same protocol as
the app.

**oxd.** A per-user daemon on `$OX_SOCKET`, or `<data dir>/ox/oxd.sock`.
`Client::connect_or_start` starts `oxd` from beside the client's executable
when no daemon answers, in a new session (`setsid`) with stderr appended to
`oxd.log` beside the socket, and reads the `ready` line it prints once a
daemon is listening. A startup lock and an instance lock keep one daemon per
socket. `oxd` accepts connections only from its own user, and exits after an
idle period with no clients and no sessions. Each session runs its agent
command in its own process group and speaks ACP to it over stdio; closing the
session kills that group. A session is running from a prompt until the agent
answers it. An agent that also starts turns by itself reports when it starts
and stops working with the `_ox/agent_state` ACP extension notification, and
the session is running during those turns too: `oxd` records `turn_started`
and `turn_ended` around them and takes no prompt until they end.

**claude-code-acp.** An ACP agent that bridges to the Claude Code CLI. A
session's first prompt starts one `claude -p`, in the bridge's process group,
and the session keeps it: the bridge holds its stdin open, writes each prompt
to it as a stream-json message, reads streamed output and permission requests
from its stdout, and answers permission requests with the client's decision.
`session/cancel` interrupts the running turn, and a model or mode change goes
to the running process as a control request. Claude keeps running between
prompts, and the turns it starts by itself, when a background task reports
back, stream to the client as a prompt's turn does. The bridge tells `oxd`
when Claude starts and stops working with the `_ox/agent_state` notification. When `claude` has exited,
the next prompt starts another with `--resume` and the Claude session ID. The
bridge records its sessions in `acp-sessions.json` and replays Claude's
transcripts on `session/load`.

**claude.** The Claude Code CLI, authenticated with the user's subscription.
