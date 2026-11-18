# Ox architecture

`oxd` is a daemon that keeps workspaces and Claude Code sessions, runs a
Claude process for each session in use, and serves them to clients over a
Unix socket. `ox` (`cmd/ox`) serves the web UI, embedded from
`cmd/ox/web`. It gives each open page its own `oxd` connection: lines from
`oxd` reach the page as Server-Sent Events on `/events`, and the page posts
JSON-RPC requests to `/rpc`. The page speaks the `oxd` protocol directly.

Each layer of `oxd` depends only on the layers below it.

```
cmd/oxd            socket and state paths, signals
   │
internal/server    connections, sessions, JSON-RPC methods
   │         │    │
   │         │   internal/store     workspace and session records in a JSON file
   │         │
   │   internal/jsonrpc   message framing and envelopes
   │
internal/agent     one agent's state machine
   │
internal/claude    Claude Code subprocess and its stream-json wire format
```

| Package | Owns |
| --- | --- |
| `cmd/oxd` | Choosing the socket and state file, starting the server, and stopping it on a signal. |
| `server` | Client connections, the sessions and the process running each, and the mapping from each method to a session operation and its error codes. Each connection writes through its own queue, so agents never wait on a client. |
| `store` | Loading and saving the state file. |
| `jsonrpc` | Request validation and the response and notification types. |
| `agent` | Prompts, approvals, and configuration for one Claude process, and publishing progress to attached subscribers. |
| `claude` | Starting and initializing Claude, parsing its output into messages, and writing its input messages. |

## Sessions

The server loads the stored workspaces and sessions at startup, all parked,
and saves the state file whenever a record changes. A session's ID is the
Claude session ID, so each process it starts names the same conversation: the
first with `--session-id`, and later ones with `--resume` once the first
prompt's result shows Claude has written the transcript. Each process receives
the session's stored model, effort, and permission mode.

A session's lock is held while its process starts, so it runs at most one. A
prompt refused by a parking process waits for that process to exit and goes to
a new one.
The session keeps the connections attached to it and attaches them to each
new process before any prompt runs. A session parks when its process exits;
the server notices the exit the next time it looks at the session.

Each session's history is a subscriber to every process it starts. It appends
the conversation's updates to `sessions/<session_id>.jsonl` beside the state
file, holding the current reply's text in memory until the next update ends
it. `session/attach` reads the history from the agent's attach callback,
which runs on the agent's goroutine, so the history includes every update
published before the attachment and none after it. A parked session has no
process, so its lock holder reads the history.

## Agents

An agent is a state machine run by one goroutine, which owns all of its state.
Client operations (`Attach`, `Send`, `Approve`, `Configure`) and Claude's
output arrive as events and are handled one at a time, so updates go out in the
order their causes occurred.

The agent's phase records what owns its Claude process:

| Phase | Meaning | Leaves on |
| --- | --- | --- |
| `available` | Nothing owns the process. | A prompt or configuration starts. |
| `queued` | A prompt was written; Claude has not taken it yet. | `command_lifecycle started` for the prompt. |
| `prompting` | Claude is working on the prompt. | Claude reports `idle`. |
| `configuring` | Control requests are in flight, one at a time. | The last acknowledgement, a refusal, or a timeout. |
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
background task runs, and nothing is scheduled, and disarms it when any of
those stops holding. If the timer fires while still armed, the
agent enters `parking` and sends Claude SIGTERM.

Subscribers receive every update: those of a client's prompt carry its ID, and
those of turns Claude starts itself carry none. The agent publishes `parked`
as its last update when the process exits. The server attaches a connection
when it sends a prompt or calls `session/attach`, and a connection is detached
once it has closed.
