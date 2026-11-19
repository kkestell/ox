# Ox architecture

`oxd` keeps workspaces and agent sessions, runs their harnesses, and serves
clients over a Unix socket. `ox` serves the [web UI](#web-ui).

Each package of `oxd` depends only on the packages beneath it. The server and each
harness meet only at `internal/agent`; `cmd/oxd` gives the server each
harness's `Start` function by name.

```
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
| `cmd/oxd` | Choosing the socket, database, and configuration, checking the configured harnesses, starting the server, and stopping it on a signal. |
| `paths` | The XDG locations of the configuration, database, and socket, which `cmd/oxd` uses unless a flag overrides them and `cmd/ox` uses for the socket. |
| `config` | Reading the configuration file, or the built-in default, into each harness's models, efforts, and permission modes and the new-session defaults, and checking settings against them. |
| `server` | Client connections, each session's goroutine and the agent it runs, and the mapping from each method to a session operation and its error codes. Each connection writes through its own queue, so sessions never wait on a client. |
| `store` | The SQLite database of workspaces, sessions, and session events. |
| `jsonrpc` | Request validation and the response and notification types. |
| `agent` | The `Agent` interface a harness implements, its `Start` function and options, and the updates, snapshots, settings, and errors they share. |
| `fake` | A harness that runs no process and echoes each prompt back, a word at a time. |
| `claude` | The Claude Code harness: starting and initializing Claude, its stream-json wire format, and the state machine that runs it as an `Agent`. |

## Sessions

The server loads the stored workspaces and sessions at startup, all parked. A
session's ID is Ox's own. The session stores the harness's conversation token
when it receives `OnResumable` and supplies it at later starts. See the
[session lifecycle](protocol.md#workspaces-and-sessions) for client behavior.

Each session runs on its own goroutine, which owns its settings, resume token,
history, agent, and park timer. Every request for a session is an operation
posted to that goroutine, and the agent's events and the park timer are
handled there between operations. An operation queues its own response there:
at once, or, for a configuration Claude must acknowledge, from the event that
finishes it. Nothing else touches a session's state, and a response lands in a
connection's queue after every update it reflects and before every update it
does not. The server handles each connection's requests on separate goroutines.
Changes are written to the database before they are applied in memory,
except settings a running process has already taken, which the session
adopts even when storing them fails.

`Server.mu` guards workspace and session membership and the shutdown flag.
Membership changes and `workspace/list` queue their messages under the same
lock so every connection sees them in one order. Nothing posts to a session
while holding it.

The session owns the history buffer and writes conversation events to the
store. See [the snapshot and history contract](protocol.md#sessionget).

The session arms a park timer whenever its agent is `Parkable` and stops it
when the agent stops being parkable, so the full period restarts each time. If
the timer fires, the session stops the process and handles the agent's events
until it has exited.

## Agents

An `Agent` runs one harness instance. The session goroutine drives it by
calling its methods and passing each event from `Events` to `Handle`. The
agent publishes through `OnUpdate` during those calls, and reports failures no
call returns, such as a failed write to its process, through `OnError`, which
the session logs under its ID. See the
[update contract](protocol.md#session-updates) for the wire format and ordering.

### The Claude agent

Ox assigns a fresh UUID to a new Claude conversation and reports it through
`OnResumable` with the first published result. See Claude's
[launch flags](claude-code.md#persistent-session) and
[transcript timing](claude-code.md#session-ids).

The stdout reader and control-request timers deliver through `Events`. The
session publishes updates in the order it handles operations and events.

The phase identifies the current operation:

| Phase | Meaning | Leaves on |
| --- | --- | --- |
| `available` | Available for a prompt or configuration. | A prompt or configuration starts. |
| `queued` | A written prompt is waiting for Claude to take it. | `command_lifecycle started` for the prompt. |
| `prompting` | Claude is working on the prompt. | Claude reports `idle`. |
| `configuring` | Control requests are in flight, one at a time. | The last acknowledgement, a refusal, or a write failure. |
| `exited` | The Claude process has exited. | — |

Process exit moves any live phase to `exited`. Alongside the phase, the agent
keeps the execution state it publishes: Claude's reported state, except that a
prompt is `running` from the moment it is written until Claude reports `idle`
after taking it. Both `available` and `idle` are required to take a prompt or
configuration, and an interrupt is sent whenever the state is not `idle`.

The agent derives `Parkable` from its phase, its state, pending approvals,
and counts from [Claude's task and schedule reports](claude-code.md#8-background-work-and-schedules).
A scheduling tool call sets a flag that the next Stop report with a schedule
list clears. See the [parking rule](protocol.md#workspaces-and-sessions).

## Web UI

The web UI in `cmd/ox/web` is a Solid 2.0 app written in TypeScript. `make web`
builds it with Vite into `cmd/ox/web/dist`, which `ox` embeds. `make dev` builds
it before `ox`, runs `ox` on port 8081, and serves the UI from source with live
reload at http://localhost:8080 through Vite's dev server, which passes `/events`
and `/rpc` to `ox`.

Each page gets an `oxd` connection through `ox`: `/events` relays socket lines
as Server-Sent Events, and `/rpc` posts the page's JSON-RPC requests to the socket.

| File | Owns |
| --- | --- |
| `protocol.ts` | Types for the `oxd` methods, updates, and notifications. |
| `connection.ts` | Opening the event stream, sending requests, matching replies, and reporting stream errors. |
| `ox.tsx` | A provider that reloads the catalog, workspaces, and sessions on each connection, updating loaded sessions in place so their views keep their state, applies notifications, and exposes RPC calls; each session's control; `createAttempt`, which shows a request's error. |
| `App.tsx`, `Sidebar.tsx`, `SessionView.tsx`, `ApprovalRequest.tsx`, `SettingsFields.tsx`, `HarnessIcon.tsx` | The components. |

The page shows stream errors while reconnecting and asks for a reload if the
browser closes the stream after `ox` refuses it.

A session's control owns the page's requests for that session: its composer
text, sends, configuration, interrupts, approval answers, and their errors. It
outlives the session's view, so leaving the view or reconnecting keeps the
text and repeats no request. A send and a configuration exclude each other.
The settings form sends a patch to
[`session/configure`](protocol.md#sessionconfigure) and shows the requested
values, each changed one with a spinner, and Send disabled until the request
settles; then it shows the stored settings. The new-session view creates the session and hands its text
to the session's control, which sends the first prompt. The page then selects
the session unless the user has selected something else.
