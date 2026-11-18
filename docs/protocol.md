# Ox daemon protocol

This reference describes the interface implemented in [internal/protocol](internal/protocol)
and [internal/agents](internal/agents).

## Connection and messages

Run `go run ./cmd/oxd` and connect to its Unix socket. The default path is
`ox/oxd.sock` beneath Go's user cache directory (`~/Library/Caches` on macOS).
Use `-socket /absolute/path/to/oxd.sock` to choose a different socket.

Each message is one JSON object terminated by a newline. The examples below
each occupy one line on the wire. Requests use JSON-RPC `"2.0"`, a string or
numeric `id`, a `method`, and an object in `params`. Responses echo the request
`id` and contain either `result` or `error`. A null `id` is also accepted;
omitting `id` does not execute an operation or produce a response for a valid
message. Batch requests are unsupported. No connection handshake is required.

Clients must continuously read the socket: responses and `agent/update`
notifications share the stream. Notifications can arrive before the response
to `agent/send`. The daemon gives each socket write five seconds to complete;
a client that stops reading can lose its connection.

| Identifier | Meaning |
| --- | --- |
| `id` | Client-chosen JSON-RPC request identifier, echoed in its response. |
| `agent_id` | Opaque identifier returned by `agent/create`. |
| `message_id` | Prompt identifier returned by `agent/send` and attached to its updates. |
| `request_id` | Claude's identifier for an approval, used to answer or withdraw it. |

## Agent methods

### `agent/create`

Starts one persistent Claude Code process and returns after initialization.
Initialization has a one-minute timeout; startup or initialization failure
returns an operation error.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workspace` | Yes | Absolute path to the process's working directory. Startup fails if it cannot be used. |
| `model` | No | Passed to Claude's `--model` flag when nonempty. |
| `effort` | No | One of `low`, `medium`, `high`, `xhigh`, `max`, or `ultracode`; an empty value leaves Claude's default. |
| `permission_mode` | No | Passed to Claude's `--permission-mode` flag when nonempty. |

Omitted or empty optional settings leave Claude's defaults in effect. Ox
validates the effort values above; model names and permission modes are
interpreted by the installed Claude version.

```json
{"jsonrpc":"2.0","id":1,"method":"agent/create","params":{"workspace":"/Users/kyle/src/ox","model":"haiku","effort":"low","permission_mode":"default"}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"agent_id":"agent-id"}}
```

### `agent/configure`

Requires `agent_id` and at least one of `model`, `effort`, or `permission_mode`.
Changes settings in the existing Claude process, preserving the conversation.
Omitted or null fields are unchanged. `model` and `permission_mode` must be
nonblank strings. `effort` accepts the same levels as creation; `""` resets it
to Claude's default. Changing the model leaves effort unchanged.

```json
{"jsonrpc":"2.0","id":5,"method":"agent/configure","params":{"agent_id":"agent-id","model":"haiku","effort":"low","permission_mode":"acceptEdits"}}
```

```json
{"jsonrpc":"2.0","id":5,"result":{}}
```

The agent must be idle with no pending prompt. Configuration reserves the
agent until completion; sends and other configuration requests during that
time return `-32002`. Any connected client can configure an idle agent.

Success means Claude acknowledged every requested change. Model, effort, and
permission mode are applied in that order. If a change fails, earlier changes
remain applied and later changes are skipped. Claude refusals, write failures,
and missing responses after one minute return an operation error. A process
exit returns `-32001`. As at creation, model identifiers are interpreted by
Claude; acknowledgement does not guarantee the model will work on a prompt.

### `agent/send`

Requires `agent_id` and nonblank `text`. Sends a prompt to an idle agent and
returns its `message_id`. Success means the prompt was written to Claude;
its output and eventual result arrive through `agent/update` notifications.

```json
{"jsonrpc":"2.0","id":2,"method":"agent/send","params":{"agent_id":"agent-id","text":"Explain this repository."}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"message_id":"prompt-id"}}
```

Each agent accepts one prompt at a time. A send while a prompt is pending or
the agent is running or waiting for action returns `-32002`; prompts are not
queued. Wait for the prompt's `state: "idle"` update before sending another.
A `result` update alone does not release the agent for another prompt.

### `agent/approve`

Requires `agent_id`, `request_id`, and a `response` object whose `behavior` is
`"allow"` or `"deny"`. Only the connection that sent the prompt can answer its
pending approvals. The response payload is forwarded to Claude.

```json
{"jsonrpc":"2.0","id":3,"method":"agent/approve","params":{"agent_id":"agent-id","request_id":"claude-request-id","response":{"behavior":"allow","updatedInput":{"command":"echo hello"},"toolUseID":"toolu_example"}}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{}}
```

For rejection, use `{"behavior":"deny","message":"Reason for rejection"}`
as `response`. Set `interrupt: true` in that response to stop the turn as well.

Ox checks the response shape and preserves its fields. If supplied,
`updatedInput` must be an object and `updatedPermissions` must be an array.
Clients can echo the original input, supply changed input (including
`AskUserQuestion` answers), or return `updatedPermissions` chosen from
Claude's suggestions. Claude applies those permission changes; Ox stores no
grants or policy. See [Claude's response documentation](https://code.claude.com/docs/en/agent-sdk/user-input#respond-to-tool-requests).

A successful RPC consumes the pending request; a duplicate or late answer is
rejected. Success confirms forwarding to Claude, not tool execution. Continue
reading prompt updates for the outcome.

### `agent/destroy`

Requires `agent_id`. Sends SIGTERM so Claude can clean up its tool processes,
and waits for it to exit.
Pending approvals are withdrawn, and an active prompt receives an `exited`
update before the destroy response. Any connected client can destroy an agent.

```json
{"jsonrpc":"2.0","id":4,"method":"agent/destroy","params":{"agent_id":"agent-id"}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{}}
```

## Prompt updates

Updates go only to the connection that called `agent/send`. Every notification
uses method `agent/update` and includes `agent_id`, `message_id`, and `type`
inside `params`:

```json
{"jsonrpc":"2.0","method":"agent/update","params":{"agent_id":"agent-id","message_id":"prompt-id","type":"text_delta","text":"This repository "}}
```

| `type` | Additional fields | Meaning |
| --- | --- | --- |
| `text_delta` | `text`: string | Append this fragment of top-level assistant text. |
| `state` | `state`: `running`, `requires_action`, or `idle` | Claude's current execution state for the active prompt. |
| `result` | `result`: object with `text` and `is_error` | Claude's final response or turn error. The text is a complete result, not another delta to append. |
| `approval` | `request_id`: string; `request`: object | A pending Claude request needing a client decision. |
| `approval_cancelled` | `request_id`: string | Withdraw an unanswered approval. Discard any later answer. |
| `exited` | `error`: string | The process exited during an active prompt; the prompt has ended and the agent is unavailable. |

`running` means Claude is working. `requires_action` means it is waiting for
user action; read the approval notifications to obtain the requests. `idle`
means the prompt is released and another can be sent. An idle process exit
has no active prompt to notify.

```json
{"jsonrpc":"2.0","method":"agent/update","params":{"agent_id":"agent-id","message_id":"prompt-id","type":"result","result":{"text":"The repository contains a Go daemon.","is_error":false}}}
```

Claude's errors are delivered as `result.is_error: true`; an unexpected process
exit is delivered as `exited`. JSON-RPC errors describe failures of the client
operation itself. The stream exposes assistant text, states, results, and
approval lifecycle events; it does not forward the full Claude tool transcript.

### Approval lifecycle

Claude decides when a tool needs approval using its permission mode and rules.
Ox forwards Claude's complete `can_use_tool` request, including any display
metadata, tool input, permission suggestions, or question fields:

```json
{"jsonrpc":"2.0","method":"agent/update","params":{"agent_id":"agent-id","message_id":"prompt-id","type":"approval","request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"echo hello"},"tool_use_id":"toolu_example","permission_suggestions":[]}}}
```

The client chooses how to obtain the decision and replies using `agent/approve`.
Reading output continues while approvals are pending. Multiple approvals can
be pending; identify each by its `request_id`.

When Claude cancels a request, becomes idle, or exits, Ox withdraws unanswered
approvals. A `result` alone does not withdraw approvals: background subagents
can still be waiting for a decision. For idle or exit, the cancellation
notifications precede the corresponding `idle` or `exited` update:

```json
{"jsonrpc":"2.0","method":"agent/update","params":{"agent_id":"agent-id","message_id":"prompt-id","type":"approval_cancelled","request_id":"claude-request-id"}}
```

## Connection and agent lifetimes

Agents belong to the daemon and remain available across prompts. Any connected
client with an agent's ID can send a prompt when it is idle. Approval ownership
belongs to the connection that sent the current prompt.

Closing a client connection does not destroy its agents. Running work can
continue, but if an approval is pending, Ox denies it with `interrupt: true`
and the message `Approval client disconnected`. Further approval requests for
that disconnected prompt receive the same answer. Once the agent is idle,
another connection can send it a new prompt.

Updates have no replay or reconnect subscription, and pending approvals cannot
be transferred between connections. Agent IDs and processes last only for the
current daemon lifetime. Destruction, process exit, or daemon shutdown removes
the agent; daemon shutdown stops all its Claude processes.

## Errors

Error responses echo the JSON-RPC request ID. Parse errors and invalid request
envelopes use `id: null`. Error messages for operation failures can include
underlying process or I/O errors.

```json
{"jsonrpc":"2.0","id":5,"error":{"code":-32002,"message":"Agent is busy"}}
```

| Code | Meaning |
| --- | --- |
| `-32700` | Parse error: malformed JSON. |
| `-32600` | Invalid request envelope, field types, JSON-RPC version, or batch request. |
| `-32601` | Unknown method. |
| `-32602` | Missing or invalid method parameters. |
| `-32000` | Operation failed, such as Claude startup or a process write failure. |
| `-32001` | Agent not found or no longer available. |
| `-32002` | Agent is busy; a prompt or configuration cannot start yet. |
| `-32003` | Approval request is no longer pending, including duplicate or cancelled answers. |
| `-32004` | Pending approval belongs to another client connection. |
