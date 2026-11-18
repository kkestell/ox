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

Clients must continuously read the socket: responses and notifications share
the stream. Every connection receives every notification, for every session,
from the moment it connects. Notifications can arrive before the response to
the request that caused them. The daemon gives each socket write five seconds to complete
and buffers a limited backlog; a client that stops reading loses its
connection.

| Identifier | Meaning |
| --- | --- |
| `id` | Client-chosen JSON-RPC request identifier, echoed in its response. |
| `workspace_id` | Opaque identifier returned by `workspace/create`. |
| `session_id` | Opaque identifier returned by `session/create`. |
| `message_id` | Prompt identifier returned by `session/send` and included in its updates. Updates from turns Claude starts itself have none. |
| `request_id` | Claude's identifier for an approval, used to answer or withdraw it. |

## Workspaces and sessions

A workspace is a directory. A session is a conversation with a harness in a
workspace. Its harness is chosen at creation and never changes. The daemon
knows two harnesses: `claude`, which runs Claude Code, and `fake`, which runs
no process and echoes each prompt back. The daemon stores workspaces,
sessions, and session history in the SQLite database `ox/ox.db` beneath
`$XDG_STATE_HOME`, or beneath `~/.local/state` when that is unset;
`-db /absolute/path/to/ox.db` chooses a different file. They outlive the
daemon. Each workspace and session records when it was created, as
`created_at`, an RFC 3339 timestamp.

A session runs a Claude process only while it is in use. A session without
one is parked. A new session starts parked; `session/send` to a parked session
starts a process that continues the conversation where it left off. A session
parks when its process exits for any reason, including daemon shutdown.

The daemon parks a session whose process has gone unused for five minutes
(`-park-after` changes the period): no prompt or configuration in progress,
Claude idle, no approval pending, no background task running, and no scheduled
prompt pending (`CronCreate`, `ScheduleWakeup`, or `/loop`), so scheduled
prompts keep firing. Connected clients do not keep a session running.

## Configuration

The daemon reads the harnesses it offers from `ox/config.json` beneath
`$XDG_CONFIG_HOME`, or beneath `~/.config` when that is unset; `-config
/absolute/path/to/config.json` chooses a different file. Without a file, it
uses a built-in configuration offering Claude's current models and no
`fake`. The daemon
reads the file at startup and refuses to start on a harness it does not know.

Each harness lists its models, each with the effort levels it takes, and its
permission modes. Every choice has an `id`, passed to the harness, and a
`name` to show. A model with no effort levels takes an empty `effort`. The
required `defaults` name the harness, model, effort, and mode a new session
starts with, each one of the configured choices; the daemon refuses to start
otherwise.

```json
{"harnesses":{"claude":{"models":[{"id":"claude-haiku-5-5","name":"Haiku 5.5","efforts":[{"id":"low","name":"Low"},{"id":"high","name":"High"}]}],"modes":[{"id":"manual","name":"Manual"},{"id":"plan","name":"Plan"}]}},"defaults":{"harness":"claude","model":"claude-haiku-5-5","effort":"low","mode":"plan"}}
```

Clients offer these choices. The daemon passes settings to the harness as they
are, so a value the harness does not accept surfaces as its error, usually on
the next `session/send`.

### `harness/list`

Returns the configuration's harnesses and new-session defaults.

```json
{"jsonrpc":"2.0","id":1,"method":"harness/list","params":{}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"harnesses":{"claude":{"models":[{"id":"claude-haiku-5-5","name":"Haiku 5.5","efforts":[{"id":"low","name":"Low"}]}],"modes":[{"id":"manual","name":"Manual"}]}},"defaults":{"harness":"claude","model":"claude-haiku-5-5","effort":"low","mode":"manual"}}}
```

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

Returns every workspace with its sessions, each oldest first. `parked` is
false while a session's process is running.

```json
{"jsonrpc":"2.0","id":2,"method":"workspace/list","params":{}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"workspaces":[{"workspace_id":"workspace-id","path":"/Users/kyle/src/ox","created_at":"2026-11-18T19:53:47.102Z","sessions":[{"session_id":"session-id","harness":"claude","model":"haiku","effort":"low","permission_mode":"manual","created_at":"2026-11-18T19:53:48.416Z","parked":true}]}]}}
```

## Session methods

### `session/create`

Requires `workspace_id`, `harness`, `model`, `effort`, and `permission_mode`. Records a
parked session in the workspace and returns its ID. No process starts, so startup errors such as an unknown model surface
on the first `session/send`.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workspace_id` | Yes | The workspace whose directory the harness runs in. |
| `harness` | Yes | A harness in the configuration. |
| `model` | Yes | Nonblank; passed to Claude's `--model` flag. |
| `effort` | Yes | Passed to Claude's `--effort` flag; empty passes none. |
| `permission_mode` | Yes | Nonblank; passed to Claude's `--permission-mode` flag. |

Model names, effort levels, and permission modes are interpreted by the
installed Claude version. Each process the session starts receives the stored
settings.

```json
{"jsonrpc":"2.0","id":3,"method":"session/create","params":{"workspace_id":"workspace-id","harness":"claude","model":"haiku","effort":"low","permission_mode":"manual"}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"session_id":"session-id"}}
```

### `session/send`

Requires `session_id` and nonblank `text`. Sends a prompt to an idle session
and returns the prompt's `message_id`. A parked session first starts a process, which has a one-minute
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

### `session/get`

Requires `session_id`. Returns the session's conversation so far and its
current state. The connection already receives the session's updates: those
that arrive before the response are included in it, and those after it apply
on top of it. A client loading a session ignores its updates until the
response arrives.

```json
{"jsonrpc":"2.0","id":5,"method":"session/get","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":5,"result":{"state":"requires_action","prompt":{"message_id":"prompt-id"},"approvals":[{"request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"touch notes.txt"}}}],"history":[{"message_id":"prompt-id","type":"prompt","text":"Create notes.txt."},{"message_id":"prompt-id","type":"text_delta","text":"I'll create the file."}]}}
```

| Field | Meaning |
| --- | --- |
| `state` | `parked`, or Claude's execution state: `running`, `requires_action`, or `idle`. |
| `prompt` | The active prompt, or `null`. |
| `prompt.message_id` | The prompt's identifier, matching its updates. |
| `approvals` | Pending approvals, each with `request_id` and Claude's `request`, whether or not a prompt is active. |
| `history` | The session's recorded updates, oldest first, in the form of `session/update` params without `session_id`. |

The daemon records every session's `prompt`, `text_delta`, `tool_use`,
`tool_result`, `result`, and `exited` updates, including those of turns Claude starts itself, and keeps
them across parking and daemon restarts. Consecutive `text_delta` fragments
are recorded as one. Applying the history the same way as live updates
reproduces the conversation. Deleting the session deletes its history.

### `session/configure`

Requires `session_id`, `model`, `effort`, and `permission_mode`, validated as
at creation. Replaces the session's settings, preserving the conversation, and
stores them for its later processes.

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

For a running session, success means Claude acknowledged all three settings,
and only then are they stored. Model, effort, and permission
mode are applied in that order. If a change fails, earlier changes remain
applied to the process and later changes are skipped. Claude refusals, write
failures, and missing responses after one minute return an operation error.
If the process exits first, the settings are stored for the next process and
the request succeeds. As at creation, model identifiers are interpreted by
Claude; acknowledgement does not guarantee the model will work on a prompt.

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

## Session updates

Updates go to every connection. Every notification uses method
`session/update` and includes `session_id`, `type`, and `time` inside
`params`, and `message_id` when the update belongs to a client's prompt.
`time` is when the daemon published the update, as an RFC 3339 timestamp; a
recorded reply's `text_delta` carries the time of its first fragment.
Turns Claude starts itself, such as a scheduled prompt firing or a background
task finishing, produce the same updates without `message_id`:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"text_delta","text":"This repository ","time":"2026-11-18T19:53:50.674Z"}}
```

| `type` | Additional fields | Meaning |
| --- | --- | --- |
| `prompt` | `text`: string | A client sent this prompt. |
| `text_delta` | `text`: string | Append this fragment of top-level assistant text. |
| `tool_use` | `tool_use_id`: string; `tool_name`: string; `input`: object | Claude called a tool at the top level. |
| `tool_result` | `tool_use_id`: string; `result`: object with `text` and `is_error` | The outcome of the top-level tool call with this `tool_use_id`. `text` joins the result's text blocks. |
| `state` | `state`: `running`, `requires_action`, or `idle` | Claude's current execution state. |
| `result` | `result`: object with `text` and `is_error` | Claude's final response or turn error. The text is a complete result, not another delta to append. |
| `approval` | `request_id`: string; `request`: object | A pending Claude request needing a client decision. |
| `approval_answered` | `request_id`: string | A client answered the approval. |
| `approval_cancelled` | `request_id`: string | Withdraw an unanswered approval. Discard any later answer. |
| `exited` | `error`: string | The process exited during an active prompt; the prompt has ended. |
| `parked` | — | The session's process has exited, for any reason. The last update from that process. |

`running` means Claude is working. `requires_action` means it is waiting for
user action; read the approval notifications to obtain the requests. `idle`
means the turn is over and, for a prompt, that another can be sent.

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"tool_use","tool_use_id":"toolu_example","tool_name":"Bash","input":{"command":"ls","description":"List files"}}}
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"tool_result","tool_use_id":"toolu_example","result":{"text":"go.mod\ncmd","is_error":false}}}
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"result","result":{"text":"The repository contains a Go daemon.","is_error":false}}}
```

Claude's errors are delivered as `result.is_error: true`; an unexpected process
exit is delivered as `exited`. JSON-RPC errors describe failures of the client
operation itself. The stream exposes the top-level assistant text, tool calls
and their results, states, results, and approval lifecycle events. Subagents'
text and tool calls are not forwarded; a subagent appears as its top-level
`Agent` tool call and result.

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

Turns Claude starts itself request approval the same way, without
`message_id`. An approval waits until a client answers it, however long that
takes; while it is pending, the session's process keeps running.

When Claude cancels a request, becomes idle, or exits, Ox withdraws unanswered
approvals. A `result` alone does not withdraw approvals: background subagents
can still be waiting for a decision. For idle or exit, the cancellation
notifications precede the corresponding `idle` or `exited` update:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"approval_cancelled","request_id":"claude-request-id"}}
```

## Workspace and session events

Every connection is told when workspaces and sessions come and go:

| Method | `params` | Meaning |
| --- | --- | --- |
| `workspace/created` | `workspace_id`, `path`, `created_at` | A workspace was registered. |
| `session/created` | `session_id`, `workspace_id`, `harness`, `model`, `effort`, `permission_mode`, `created_at` | A session was created, parked. |
| `session/configured` | `session_id`, `model`, `effort`, `permission_mode` | A session's settings changed; the fields are its stored settings. |
| `session/destroyed` | `session_id` | A session was destroyed, after its last update. |

```json
{"jsonrpc":"2.0","method":"session/created","params":{"session_id":"session-id","workspace_id":"workspace-id","harness":"claude","model":"haiku","effort":"low","permission_mode":"manual","created_at":"2026-11-18T19:53:48.416Z"}}
```

## Connections

Clients have no identity: any connection can send a session a prompt when it
is idle, answer its approvals, configure it, and destroy it.

Closing a connection leaves its sessions running. A prompt continues, and its
pending approvals stay pending until a client answers them. A client that
connects calls `workspace/list` and `session/get` to load the sessions, then
follows the notifications.

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
| `-32000` | Operation failed, such as Claude startup, a process write failure, or a database write failure. |
| `-32001` | Workspace or session not found. |
| `-32002` | Session is busy; a prompt or configuration cannot start yet. |
| `-32003` | Approval request is no longer pending, including duplicate or cancelled answers. |
