# Ox architecture

`oxd` keeps workspaces and agent sessions, runs their harnesses, and serves
clients over a Unix socket. `ox` serves the [web UI](#web-ui).

Each package of `oxd` depends only on the packages beneath it. The server and each
harness meet only at `internal/agent`; `cmd/oxd` gives the server each
harness by name, with its `Start` function and any prompt validation.

```
cmd/ox-mcp         stdio forwarder from a harness's MCP client to the socket

cmd/oxd            socket, database, and configuration paths, signals, the harnesses by name
   ├── internal/paths     the default file locations, shared with cmd/ox
   ├── internal/config    the harnesses offered and their choices
   ├── internal/store     the database it opens for the server
   ├── internal/claude    the Claude Code harness
   │      └── internal/agent
   ├── internal/fake      a harness that echoes prompts
   │      └── internal/agent
   └── internal/server    connections, sessions, JSON-RPC methods
          ├── internal/config    the catalog that harness/list serves
          ├── internal/store     workspaces, sessions, and events in SQLite
          ├── internal/jsonrpc   message framing and envelopes
          └── internal/agent     the contract between the server and a harness
```

| Package | Owns |
| --- | --- |
| `cmd/ox-mcp` | Connecting a harness's MCP client, over stdio, to `oxd`'s socket for one session: attaching, then copying each line between stdin and the socket and the socket and stdout. |
| `cmd/oxd` | Choosing the socket, database, and configuration, checking the configured harnesses, and holding the socket from before database startup until sessions have stopped and every Claude process has exited. A second stop signal kills all registered Claude processes, including those still initializing, and ends the daemon. |
| `paths` | The XDG locations of the configuration, database, and socket, which `cmd/oxd` uses unless a flag overrides them and `cmd/ox` uses for the socket. |
| `config` | Reading the configuration file, or the built-in default, into each harness's models, efforts, and permission modes and the new-session defaults, and checking settings against them. |
| `server` | Client and MCP connections, each session's goroutine and the agent it runs, and the mapping from each method to a session operation and its error codes. Each connection writes through its own queue, so sessions never wait on a client. |
| `store` | The SQLite database of workspaces, sessions, and session events. |
| `jsonrpc` | Request validation and the response and notification types. |
| `agent` | The `Agent` interface a harness implements, its `Start` function and options, and the updates, snapshots, settings, and errors they share. |
| `fake` | A harness that runs no process and echoes each prompt back, a word at a time. A prompt `approve <tool> <input JSON>` first asks to approve that tool call, and the call's result is the answer. |
| `claude` | The Claude Code harness: starting and initializing Claude, its stream-json wire format, and the state machine that runs it as an `Agent`. |

## Sessions

The server loads the stored workspaces and sessions at startup, all parked. A
session's ID is Ox's own. The session stores the harness's conversation token
when it receives `OnResumable` and supplies it at later starts. An empty
token from `OnResumable` clears it, so the next start begins a new
conversation. See the
[session lifecycle](protocol.md#workspaces-and-sessions) for client behavior.

Each session runs on its own goroutine, which owns its settings, resume token,
history, agent, inbox, and park timer. Every request for a session is an operation
posted to that goroutine, and the agent's events and the park timer are
handled there between operations. An operation queues its own response there:
at once, or, for a configuration Claude must acknowledge, from the event that
finishes it. Nothing else touches a session's state, and a response lands in a
connection's queue after every update it reflects and before every update it
does not. The server handles each connection's requests on separate goroutines.
Workspace and session membership changes are stored before they are applied
in memory. The session keeps the settings it adopts and the resume token it
receives or clears even when storing them fails.

`Server.mu` guards workspace and session membership and the shutdown flag.
Membership changes and `workspace/list` queue their messages under the same
lock so every connection sees them in one order. Nothing posts to a session
while holding it.

The session owns the history buffer and writes conversation events to the
store. See [the snapshot and history contract](protocol.md#sessionget).

The session owns the inbox: messages, with their IDs and attribution, wait
there until sent to the agent. The agent owns an input it has accepted, with
its text and attribution, until it is taken or dropped. One operation starts a
parked session for either input source. After each operation and event,
`deliver` tries the oldest waiting message; a busy agent leaves it for the
next try. `send_message` posts to the recipient's goroutine and
`list_sessions` captures each entry on its owning goroutine. `read_session`
reads the target's snapshot, inbox, and history on the target's goroutine and
starts nothing. No session goroutine posts to another.

The session arms a park timer whenever its agent is `Parkable` and stops it
when the agent stops being parkable, so the full period restarts each time. If
the timer fires, the session stops the agent, which ends at once while its
process exits in the background.

## Agents

An `Agent` runs one harness instance. `Start` returns at once with the agent
starting, and fails only when the harness cannot be launched; a start that
fails later ends the agent with its reason. `Stop` ends the agent at once. The
session goroutine drives it by calling its methods and passing each event from
`Events` to `Handle`. The agent publishes through `OnUpdate` during those
calls, and reports failures no call returns, such as a failed write to its
process, through `OnError`, which the session logs under its ID. `Send`
receives the input with its session-assigned ID, text, and attribution. The
agent publishes the lifecycle updates for when it is accepted, taken, or
dropped with that text and attribution, and reports the untaken input in
`Snapshot`. The agent reports settings changes through `OnConfigured`, as they
happen, separately from the outcome of a `Configure` request. See the [update
contract](protocol.md#session-updates).

Each harness receives Ox's MCP server for its session: the `ox-mcp` beside
`oxd`, its environment naming the socket and the session, and the tools that
run without an approval prompt where the permission mode allows.

### The Claude agent

Ox assigns a fresh UUID when launching a new Claude conversation. The agent
tracks the conversation ID Claude reports and calls `OnResumable` once a
changed ID is known to be resumable. See Claude's [transcript timing](claude-code.md#session-ids).
On every launch, resumes included, it registers Ox's MCP server as `ox` and
pre-approves its tools; see Claude's [launch flags](claude-code.md#persistent-session).

One stdout reader, from spawn to exit, drains the pipe into a delivery queue
independently of the session, delivering each JSON line and each other
nonblank line as plain text. The reader's inherited-descriptor cutoff does
not limit delivery; all captured output arrives before the exit event, which
includes the stderr tail. Once the agent has ended, delivery discards the
rest. The output queue, the startup limit, and control-request timers
deliver through `Events`. The session publishes updates in the order it
handles operations and events.

The phase identifies the current operation and holds the fields valid
only during it:

| Phase | Meaning | Leaves on |
| --- | --- | --- |
| `starting` | Claude has not answered `initialize`. It holds at most one input, written once Claude answers, and Claude's last failure output. | The `initialize` response, a refusal, the startup limit, the process's exit, or `Stop`. |
| `available` | Available for a prompt or configuration. | A prompt or configuration starts. |
| `prompting` | Client prompts hold the agent. It holds at most one prompt Claude has not taken yet, which `command_lifecycle started` or `cancelled` ends. | Claude reports `idle` with no prompt queued. |
| `configuring` | Control requests are in flight, one at a time. | The last acknowledgement, a refusal, a write failure, or a timeout. |
| `timedOut` | A control request went unanswered and Ox asked the process to exit. | The process exits. |
| `exited` | The agent has ended. | — |

The process's exit, `Stop`, or a failed start moves any live phase to
`exited`. Alongside the phase, the agent keeps the execution state it
publishes: Claude's reported state, except that a prompt is `running` from
the moment it is accepted until Claude reports `idle` with no prompt queued.
A prompt is held in `starting`, and written in `available`, or in
`prompting` while no prompt is queued. Both `available` and `idle` are
required to take a configuration. An interrupt is sent whenever the state is
not `idle`, and drops a queued prompt; in `starting` it drops the held input
and sends nothing.

The agent derives `Parkable` from its phase, its state, pending approvals,
and [Claude's reports of background work](claude-code.md#8-background-work-and-schedules).
On resume or after a scheduling tool call, only a Stop report with an empty
schedule list clears the possible pending work. See the [parking rule](protocol.md#workspaces-and-sessions).

## Web UI

The web UI in `cmd/ox/web` is a Solid 2.0 app written in TypeScript. `make web`
builds it with Vite into `cmd/ox/web/dist`, which `ox` embeds. `make dev` builds
it before `ox`, runs `ox` on port 8081, and serves the UI from source with live
reload at http://localhost:8080 through Vite's dev server, which passes `/events`
and `/rpc` to `ox`.

Each page gets an `oxd` connection through `ox`: `/events` relays socket lines
as Server-Sent Events, and `/rpc` posts the page's JSON-RPC requests to the socket.
Its 8 MiB request body cap leaves space for JSON escaping under the
[decoded prompt limit](protocol.md#sessionsend). `ox` answers both only when the request's `Host` names `localhost`, `127.0.0.1`,
`[::1]`, or the host given to `-addr`, and refuses an `/rpc` post from another
origin, so a page whose domain rebinds to the relay's address cannot reach `oxd`.

| File | Owns |
| --- | --- |
| `protocol.ts` | Types for the `oxd` methods, updates, and notifications. |
| `connection.ts` | Opening the event stream, sending requests, matching replies, and reporting stream errors. |
| `ox.tsx` | A provider that reloads the catalog, workspaces, and sessions on each connection, updating loaded sessions in place so their views keep their state, applies notifications, and exposes RPC calls; each session's control; `createAttempt`, which shows a request's error. |
| `toolInput.ts` | Readers of Claude's tool calls: a call's subject, an `ExitPlanMode` plan, `AskUserQuestion` questions, and a call's input as JSON. |
| `App.tsx`, `Sidebar.tsx`, `SessionView.tsx`, `NewSessionView.tsx`, `ApprovalRequest.tsx`, `SettingsFields.tsx`, `HarnessIcon.tsx`, `Markdown.tsx` | The components. |

The page shows stream errors while reconnecting and asks for a reload if the
browser closes the stream after `ox` refuses it.

A session's control owns the page's requests for that session: its composer
text, creation, sends, configuration, interrupts, approval answers, and their
errors. It outlives the session's view, so leaving the view or reconnecting
keeps the text and repeats no request. A send and a configuration exclude each other.
The control remembers the prompt it sent last, so a prompt dropped before
Claude took it returns to the composer of the page that sent it.
The settings form sends a patch to
[`session/configure`](protocol.md#sessionconfigure) and shows the requested
values, each changed one with a spinner, and Send disabled until the request
settles. After a reconnect each session waits for its own snapshot before
allowing another send or settings change.

The page keeps one draft per workspace. Its ID and composer carry over to
the created session. A successful create sends the submitted text once.
If the reply is lost, the page can recognize the session after reconnect,
restore its composer and let the user decide when to send. A failed creation
in another view links back to the draft.
