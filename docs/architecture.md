# Architecture

Ox runs as a tree of processes. The desktop app and `oxctl` are clients of
`oxd`, a per-user daemon that owns every session's agent process. Sessions
outlive the clients that started them, so closing and reopening the app
reconnects to the same sessions. `oxd` also saves its workspaces and sessions,
so they outlive the daemon.

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
`ox_protocol::Client` and streams the workspaces and sessions lists and the
open session's conversation back over Tauri channels. The sidebar groups
sessions under their workspaces; a workspace is added by choosing a directory,
and a session starts in a workspace without asking for one. Selecting a saved
session whose agent is not running loads it. A new Claude session runs
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
idle period with no clients and no running agents; a session whose agent has
exited is saved and does not keep `oxd` alive. Each session runs its agent
command in its own process group and speaks ACP to it over stdio; closing the
session kills that group. A session is running from a prompt until the agent
answers it. An agent that also starts turns by itself reports when it starts
and stops working with the `_ox/agent_state` ACP extension notification, and
the session is running during those turns too: `oxd` records `turn_started`
and `turn_ended` around them, with the stop reason the agent reports when it
goes idle, and takes no prompt until they end.

**Workspaces and saved sessions.** A workspace is a directory the user
added. A session belongs to a workspace by ID and runs in the workspace's
directory, which the session stores separately. `workspace.remove` is refused
while any session, exited ones included, belongs to the workspace. `oxd`
saves workspaces and sessions in one SQLite database beside the socket,
`<socket>.db`, and is the only process that writes it. A session's row holds
its workspace, command, directory, title, and the agent's session ID, and is
written when the session is created and again when its title or agent session
ID changes. The database holds no environment variables: a client passes its
environment each time it starts an agent, with `session.new` and
`session.load`. The schema has no migrations.

`session.close` deletes the session's row. Shutdown on SIGTERM, SIGINT, or idle
exit kills the agents and keeps every row. When `oxd` starts it registers each
saved session in state `exited`, with no agent and no events, and deletes
those that never started, which have no agent session ID to load.
`session.load` restarts the agent of an exited session that has an agent
session ID, from the session's stored command and directory, on the same
session: the session ID, subscriptions, and sidebar selection stay the same,
and the session goes through `starting` to `idle` as a new one does. The agent
replays the conversation before it answers `session/load`. A session whose
agent has not started yet records the replay as its history once the agent
starts, so a load that fails partway records none of it; one whose agent
has started, such as one that crashed, keeps its history and drops the
replay, and its history shows a second `agent_started` after the
`agent_exited`.

**claude-code-acp.** An ACP agent that bridges to the Claude Code CLI. A
session's first prompt starts one `claude -p`, in the bridge's process group,
and the session keeps it: the bridge holds its stdin open, writes each prompt
to it as a stream-json message, reads streamed output and permission requests
from its stdout, and answers permission requests with the client's decision.
A subagent's tool calls show as lines of the content of the tool call that
started it, and a tool call's result as its content. `session/cancel`
interrupts the running turn, and a model or mode change goes
to the running process as a control request. Claude keeps running between
prompts, and the turns it starts by itself, when a background task reports
back, stream to the client as a prompt's turn does. The bridge tells `oxd`
when Claude starts and stops working with the `_ox/agent_state` notification. When `claude` has exited,
the next prompt starts another with `--resume` and the Claude session ID. The
bridge records its sessions in `acp-sessions.json` and replays Claude's
transcripts on `session/load`.

**claude.** The Claude Code CLI, authenticated with the user's subscription.
