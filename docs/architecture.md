# Ox architecture

`oxd` is a daemon that runs Claude Code processes as agents and serves them to
clients over a Unix socket. `ox` (`cmd/ox`) serves the web UI, embedded from
`cmd/ox/web`. It gives each open page its own `oxd` connection: lines from
`oxd` reach the page as Server-Sent Events on `/events`, and the page posts
JSON-RPC requests to `/rpc`. The page speaks the `oxd` protocol directly.

Each layer of `oxd` depends only on the layers below it.

```
cmd/oxd            socket path, signals
   │
internal/server    connections, agent table, JSON-RPC methods
   │         │
   │   internal/jsonrpc   message framing and envelopes
   │
internal/agent     one agent's state machine
   │
internal/claude    Claude Code subprocess and its stream-json wire format
```

| Package | Owns |
| --- | --- |
| `cmd/oxd` | Choosing the socket, starting the server, and stopping it on a signal. |
| `server` | Client connections, the table of agents by ID, and the mapping from each method to an agent operation and its error codes. Each connection writes through its own queue, so agents never wait on a client. |
| `jsonrpc` | Request validation and the response and notification types. |
| `agent` | Prompts, approvals, and configuration for one Claude process, and publishing progress to attached subscribers. |
| `claude` | Starting and initializing Claude, parsing its output into messages, and writing its input messages. |

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
| `exited` | The Claude process has exited. | — |

Claude's reported session state (`running`, `requires_action`, `idle`) is kept
alongside the phase. The agent accepts a prompt or configuration only when it
is `available` and Claude is `idle`.

Subscribers receive updates about the current prompt. The server attaches a
connection when it sends a prompt or calls `agent/attach`, and a connection is
detached once it has closed.
