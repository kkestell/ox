# Ox daemon protocol

This reference describes the interface served by `oxd`. See
[architecture.md](architecture.md) for how the daemon is built.

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

Clients must continuously read the socket: responses and `session/update`
notifications share the stream. Notifications can arrive before the response
to `session/send`. The daemon gives each socket write five seconds to complete
and buffers a limited backlog; a client that stops reading loses its
connection.

| Identifier | Meaning |
| --- | --- |
| `id` | Client-chosen JSON-RPC request identifier, echoed in its response. |
| `workspace_id` | Opaque identifier returned by `workspace/create`. |
| `session_id` | Claude's session UUID, returned by `session/create`. |
| `message_id` | Prompt identifier returned by `session/send` and attached to its updates. |
| `request_id` | Claude's identifier for an approval, used to answer or withdraw it. |

## Workspaces and sessions

A workspace is a directory. A session is a Claude Code conversation in a
workspace, and its ID is Claude's session ID. The daemon stores workspaces and
sessions in `ox/state.json` beneath `$XDG_STATE_HOME`, or beneath
`~/.local/state` when that is unset; `-state /absolute/path/to/state.json`
chooses a different file. They outlive the daemon.

A session runs a Claude process only while it is in use. A session without
one is parked. A new session starts parked; `session/send` to a parked session
starts a process that continues the conversation where it left off. A session
parks when its process exits for any reason, including daemon shutdown.

## Workspace methods

### `workspace/create`

Requires `path`, an absolute path to an existing directory. Registers the
directory and returns its workspace. A path that is already registered returns
the existing workspace.

```json
{"jsonrpc":"2.0","id":1,"method":"workspace/create","params":{"path":"/Users/kyle/src/ox"}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"workspace_id":"workspace-id"}}
```

### `workspace/list`

Returns every workspace with its sessions. `parked` is false while a session's
process is running.

```json
{"jsonrpc":"2.0","id":2,"method":"workspace/list","params":{}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"workspaces":[{"workspace_id":"workspace-id","path":"/Users/kyle/src/ox","sessions":[{"session_id":"session-id","model":"haiku","effort":"low","permission_mode":"","parked":true}]}]}}
```

## Session methods

### `session/create`

Requires `workspace_id`. Records a parked session in the workspace and returns
its ID. No process starts, so startup errors such as an unknown model surface
on the first `session/send`.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workspace_id` | Yes | The workspace whose directory Claude runs in. |
| `model` | No | Passed to Claude's `--model` flag when nonempty. |
| `effort` | No | One of `low`, `medium`, `high`, `xhigh`, `max`, or `ultracode`; an empty value leaves Claude's default. |
| `permission_mode` | No | Passed to Claude's `--permission-mode` flag when nonempty. |

Omitted or empty optional settings leave Claude's defaults in effect. Ox
validates the effort values above; model names and permission modes are
interpreted by the installed Claude version. Each process the session starts
receives the stored settings.

```json
{"jsonrpc":"2.0","id":3,"method":"session/create","params":{"workspace_id":"workspace-id","model":"haiku","effort":"low","permission_mode":"default"}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"session_id":"session-id"}}
```

### `session/send`

Requires `session_id` and nonblank `text`. Sends a prompt to an idle session,
attaches the connection to the session, and returns the prompt's
`message_id`. A parked session first starts a process, which has a one-minute
initialization timeout; startup failure returns an operation error. Success
means the prompt was written to Claude; its output and eventual result arrive
through `session/update` notifications.

```json
{"jsonrpc":"2.0","id":4,"method":"session/send","params":{"session_id":"session-id","text":"Explain this repository."}}
```

```json
{"jsonrpc":"2.0","id":4,"result":{"message_id":"prompt-id"}}
```

Each session accepts one prompt at a time. A send while a prompt is pending or
the session is running or waiting for action returns `-32002`; prompts are not
queued. Wait for the prompt's `state: "idle"` update before sending another.
A `result` update alone does not release the session for another prompt.

### `session/attach`

Requires `session_id`. Subscribes the connection to the session's updates and
returns the session's current state. Use it to follow a session after
reconnecting. The response precedes every update delivered through the
attachment, so updates after it apply on top of the returned state. The
attachment lasts across parking: updates from the session's next process
reach the connection too.

```json
{"jsonrpc":"2.0","id":5,"method":"session/attach","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":5,"result":{"state":"requires_action","prompt":{"message_id":"prompt-id","approvals":[{"request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"touch notes.txt"}}}]}}}
```

| Field | Meaning |
| --- | --- |
| `state` | `parked`, or Claude's execution state: `running`, `requires_action`, or `idle`. |
| `prompt` | The active prompt, or `null`. |
| `prompt.message_id` | The prompt's identifier, matching its updates. |
| `prompt.approvals` | Pending approvals, each with `request_id` and Claude's `request`. |

Attaching again from the same connection has no further effect on delivery.

### `session/configure`

Requires `session_id` and at least one of `model`, `effort`, or
`permission_mode`. Changes the session's settings, preserving the
conversation, and stores them for its later processes. Omitted or null fields
are unchanged. `model` and `permission_mode` must be nonblank strings.
`effort` accepts the same levels as creation; `""` resets it to Claude's
default. Changing the model leaves effort unchanged.

```json
{"jsonrpc":"2.0","id":6,"method":"session/configure","params":{"session_id":"session-id","model":"haiku","effort":"low","permission_mode":"acceptEdits"}}
```

```json
{"jsonrpc":"2.0","id":6,"result":{}}
```

A parked session stores the settings and applies them when its next process
starts. A running session must be idle with no pending prompt. Configuration
reserves it until completion; sends and other configuration requests during
that time return `-32002`. Any connected client can configure an idle session.

For a running session, success means Claude acknowledged every requested
change, and only then are the settings stored. Model, effort, and permission
mode are applied in that order. If a change fails, earlier changes remain
applied to the process and later changes are skipped. Claude refusals, write
failures, process exit, and missing responses after one minute return an
operation error. As at creation, model identifiers are interpreted by Claude;
acknowledgement does not guarantee the model will work on a prompt.

### `session/approve`

Requires `session_id`, `request_id`, and a `response` object whose `behavior`
is `"allow"` or `"deny"`. Any connection can answer a pending approval. The
response payload is forwarded to Claude.

```json
{"jsonrpc":"2.0","id":7,"method":"session/approve","params":{"session_id":"session-id","request_id":"claude-request-id","response":{"behavior":"allow","updatedInput":{"command":"echo hello"},"toolUseID":"toolu_example"}}}
```

```json
{"jsonrpc":"2.0","id":7,"result":{}}
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

### `session/destroy`

Requires `session_id`. Removes the session from the daemon. A running process
receives SIGTERM so Claude can clean up its tool processes, and the daemon
waits for it to exit. Pending approvals are withdrawn, and an active prompt
receives an `exited` update before the destroy response. Claude's transcript
stays on disk. Any connected client can destroy a session.

```json
{"jsonrpc":"2.0","id":8,"method":"session/destroy","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":8,"result":{}}
```

## Prompt updates

Updates go to every connection attached to the session. Every notification
uses method `session/update` and includes `session_id`, `message_id`, and
`type` inside `params`:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"text_delta","text":"This repository "}}
```

| `type` | Additional fields | Meaning |
| --- | --- | --- |
| `text_delta` | `text`: string | Append this fragment of top-level assistant text. |
| `state` | `state`: `running`, `requires_action`, or `idle` | Claude's current execution state for the active prompt. |
| `result` | `result`: object with `text` and `is_error` | Claude's final response or turn error. The text is a complete result, not another delta to append. |
| `approval` | `request_id`: string; `request`: object | A pending Claude request needing a client decision. |
| `approval_cancelled` | `request_id`: string | Withdraw an unanswered approval. Discard any later answer. |
| `exited` | `error`: string | The process exited during an active prompt; the prompt has ended and the session is parked. |

`running` means Claude is working. `requires_action` means it is waiting for
user action; read the approval notifications to obtain the requests. `idle`
means the prompt is released and another can be sent. An idle process exit
has no active prompt to notify.

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"result","result":{"text":"The repository contains a Go daemon.","is_error":false}}}
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
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"approval","request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"echo hello"},"tool_use_id":"toolu_example","permission_suggestions":[]}}}
```

The client chooses how to obtain the decision and replies using
`session/approve`. Reading output continues while approvals are pending.
Multiple approvals can be pending; identify each by its `request_id`.

When Claude cancels a request, becomes idle, or exits, Ox withdraws unanswered
approvals. A `result` alone does not withdraw approvals: background subagents
can still be waiting for a decision. For idle or exit, the cancellation
notifications precede the corresponding `idle` or `exited` update:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"approval_cancelled","request_id":"claude-request-id"}}
```

## Connections

Clients have no identity: any connection with a session's ID can attach to
it, send it a prompt when it is idle, and answer its approvals.

Closing a connection detaches it and leaves its sessions running. A prompt
continues, and its pending approvals stay pending until a client answers them.
A client that reconnects calls `session/attach` to recover the prompt and its
approvals, then follows its updates. Updates published while no connection is
attached are not replayed.

## Errors

Error responses echo the JSON-RPC request ID. Parse errors and invalid request
envelopes use `id: null`. Error messages for operation failures can include
underlying process or I/O errors.

```json
{"jsonrpc":"2.0","id":5,"error":{"code":-32002,"message":"Session is busy"}}
```

| Code | Meaning |
| --- | --- |
| `-32700` | Parse error: malformed JSON. |
| `-32600` | Invalid request envelope, field types, JSON-RPC version, or batch request. |
| `-32601` | Unknown method. |
| `-32602` | Missing or invalid method parameters. |
| `-32000` | Operation failed, such as Claude startup, a process write failure, or a state file write failure. |
| `-32001` | Workspace or session not found. |
| `-32002` | Session is busy; a prompt or configuration cannot start yet. |
| `-32003` | Approval request is no longer pending, including duplicate or cancelled answers. |
