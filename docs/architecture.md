# Ox architecture

`oxd` is a daemon that keeps workspaces and agent sessions, runs a harness
process for each session in use, and serves them to clients over a
Unix socket. `ox` (`cmd/ox`) serves the web UI, embedded from its build in
`cmd/ox/web/dist`. It gives each open page its own `oxd` connection: lines from
`oxd` reach the page as Server-Sent Events on `/events`, and the page posts
JSON-RPC requests to `/rpc`. The page speaks the `oxd` protocol directly.

Each package of `oxd` depends only on the packages beneath it. The server and each
harness meet only at `internal/agent`; `cmd/oxd` gives the server each
harness's `Start` function by name.

```
cmd/oxd            socket, database, and configuration paths, signals, the harness
   ├── internal/socket    the default socket path, shared with cmd/ox
   ├── internal/config    the harnesses offered and their choices
   ├── internal/claude    the Claude Code harness
   │      └── internal/agent
   ├── internal/fake      a harness that echoes prompts
   │      └── internal/agent
   └── internal/server    connections, sessions, JSON-RPC methods
          ├── internal/store     workspaces, sessions, and events in SQLite
          ├── internal/jsonrpc   message framing and envelopes
          └── internal/agent     the contract between the server and a harness
```

| Package | Owns |
| --- | --- |
| `cmd/oxd` | Choosing the socket, database, and configuration, checking the configured harnesses, starting the server, and stopping it on a signal. |
| `socket` | The default socket path, which `cmd/oxd` serves and `cmd/ox` connects to. |
| `config` | Reading the configuration file, or the built-in default, into each harness's models, efforts, and modes and the new-session defaults, and checking the defaults against them. |
| `server` | Client connections, the sessions and the agent running each, and the mapping from each method to a session operation and its error codes. Each connection writes through its own queue, so agents never wait on a client. |
| `store` | The SQLite database of workspaces, sessions, and session events. |
| `jsonrpc` | Request validation and the response and notification types. |
| `agent` | The `Agent` interface a harness implements, its `Start` function and options, and the updates, snapshots, settings, and errors they share. |
| `fake` | A harness that runs no process and echoes each prompt back, a word at a time. |
| `claude` | The Claude Code harness: starting and initializing Claude, its stream-json wire format, and the state machine that runs it as an `Agent`. |

## Sessions

The server loads the stored workspaces and sessions at startup, all parked,
and writes each change to the database before applying it in memory. A
session's ID is Ox's own, and its harness is fixed at creation. Its resume token is the harness's name for the
conversation: empty until the harness reports it through `OnResumable`, and
then passed to every later process so it continues the same conversation. Each
process receives the session's stored model, effort, and permission mode.

A session's lock is held while its process starts, so it runs at most one. A
prompt refused by a parking process waits for that process to exit and goes to
a new one. A session parks when its process exits; the server notices the exit
the next time it looks at the session.

Each process a session starts has two subscribers: the session's history and
a broadcaster that sends each update to every open connection. The history stores the
conversation's updates as events in the database, holding the current reply's
text in memory until the next update ends it. `session/get` reads the
history and the agent's state through `Inspect`, whose callback runs on the
agent's goroutine between updates and queues the response there. The response
therefore lands in the connection's queue after every update it reflects and
before every update it does not. A parked session has no process, so its lock
holder reads the history.

## Agents

An `Agent` runs one harness process. Every agent publishes its updates to its
subscribers on one goroutine, runs `Inspect` callbacks on that goroutine between
updates, refuses a prompt or configuration with `ErrBusy` unless it is idle, and
publishes `parked` as its last update.

### The Claude agent

The Claude agent names a new conversation with a fresh UUID passed to
`--session-id`, reports it as the resume token at the first result, once Claude
has written the transcript, and resumes a conversation with `--resume`.

The Claude agent is a state machine run by one goroutine, which owns all of its state.
Client operations (`Inspect`, `Send`, `Approve`, `Configure`) and Claude's
output arrive as events and are handled one at a time, so updates go out in the
order their causes occurred.

The agent's phase records what owns its Claude process:

| Phase | Meaning | Leaves on |
| --- | --- | --- |
| `available` | Nothing owns the process. | A prompt or configuration starts. |
| `queued` | A prompt was written; Claude has not taken it yet. | `command_lifecycle started` for the prompt. |
| `prompting` | Claude is working on the prompt. | Claude reports `idle`. |
| `configuring` | Control requests are in flight, one at a time. | The last acknowledgement, a refusal, a write failure, a timeout, or the process exiting. |
| `parking` | The agent has terminated an unused process; prompts and configuration are refused as if it had exited. | The process exits. |
| `exited` | The Claude process has exited. | — |

Claude's reported session state (`running`, `requires_action`, `idle`) is kept
alongside the phase. The agent accepts a prompt or configuration only when it
is `available` and Claude is `idle`.

The agent also tracks the size of Claude's background-task set from
`background_tasks_changed`, and the number of pending schedules from the Stop
hook Claude calls at the end of each completed turn. A turn that ends by
interrupt or error calls no Stop hook, so a `CronCreate`, `CronDelete`, or
`ScheduleWakeup` call counts as a pending schedule until the next Stop hook.
The agent arms a park timer whenever it is `available`, Claude is `idle`, no
approval is pending, no background task runs, and nothing is scheduled, and
disarms it when any of those stops holding. If the timer fires while still armed, the
agent enters `parking` and sends Claude SIGTERM.

Subscribers receive every update: those of a client's prompt carry its ID, and
those of turns Claude starts itself carry none. The agent publishes `parked`
as its last update when the process exits.

## Web UI

The web UI in `cmd/ox/web` is a Solid 2.0 app written in TypeScript and built
by Vite into `cmd/ox/web/dist`, which `ox` embeds; `make web` builds it, and
`make dev` builds it before `ox`. `ox` serves the built `index.html` as a Go
template that fills in the directory `ox` runs in as the default workspace.

| File | Owns |
| --- | --- |
| `protocol.ts` | Types for the `oxd` methods, updates, and notifications. |
| `connection.ts` | The event stream and the requests posted on it, with no Solid state. |
| `ox.tsx` | The page's view of every workspace and session: a store created in a provider, loaded when the stream connects and kept current by notifications, and the requests that change it. |
| `App.tsx`, `Sidebar.tsx`, `SessionView.tsx`, `SettingsFields.tsx` | The components. |

The store changes only through notifications and `session/get` responses, so
every page shows the same state. Requests write nothing to it: `oxd` sends the
resulting notification to every connection, including the one that asked.
Each new connection clears the store and loads it again, since it has missed
the notifications sent while the page was disconnected. The settings form
shows a change while `session/configure` is in flight with an optimistic
value, which reverts to the stored settings if the request fails.
