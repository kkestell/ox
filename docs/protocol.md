# Ox daemon protocol

This reference describes the interface served by `oxd`. See
[architecture.md](architecture.md) for how the daemon is built.

## Connection and messages

Run `go run ./cmd/oxd` and connect to its Unix socket. The default path is
`ox/oxd.sock` beneath `$XDG_RUNTIME_DIR`, or beside the default
[database](#workspaces-and-sessions) when that is unset.
Use `-socket /absolute/path/to/oxd.sock` to choose a different socket.

Each message is one JSON object terminated by a newline. Requests use
JSON-RPC `"2.0"`, a string, number, or null `id`, and a `method`. The envelope
accepts an object, an array, or an omitted `params`; methods with parameters
take an object. Responses echo the request `id` and contain either `result`
or `error`. A valid request without `id` is ignored. Send requests individually;
batch requests return `-32600`.

Clients must continuously read the socket: responses and notifications share
the stream. Every connection receives every notification, for every session,
from the moment it connects. Notifications can arrive before the response to
the request that caused them. Each session handles its requests one at a time,
independently of other sessions. Requests sent without waiting for earlier
responses can run in any order. The daemon gives each socket write five
seconds to complete and buffers a limited backlog; a client that stops reading
loses its connection.

| Identifier | Meaning |
| --- | --- |
| `id` | Client-chosen JSON-RPC request identifier, echoed in its response. |
| `workspace_id` | Opaque identifier returned by `workspace/create`. |
| `session_id` | Opaque identifier returned by `session/create`. |
| `message_id` | Prompt identifier returned by `session/send` and included in its [updates](#session-updates). |
| `request_id` | Claude's identifier for an approval, used to answer or withdraw it. |

## Workspaces and sessions

A workspace is a directory. A session is a conversation with a harness in a
workspace. Its harness is chosen at creation. The daemon recognizes `claude`
and `fake`; see [architecture](architecture.md#ox-architecture) for their
implementations. The daemon stores workspaces, sessions, and session history in
`ox/ox.db` beneath `$XDG_STATE_HOME`, or beneath `~/.local/state` when that
is unset;
`-db /absolute/path/to/ox.db` chooses a different file. They outlive the
daemon. Each workspace and session records when it was created, as
`created_at`, an RFC 3339 timestamp.

A new session starts parked. `session/send` starts its harness and resumes
its conversation when a resume token is available. The session parks when
the harness exits, including at daemon shutdown.

The daemon parks a session after five minutes of harness inactivity
(`-park-after` changes the period). For Claude, that means idle with no prompt,
configuration, approval, background task, or pending schedule. A scheduling
tool call also keeps the harness active until its next
[Stop hook schedule report](claude-code.md#8-background-work-and-schedules).

## Configuration

The daemon reads the harnesses it offers from `ox/config.json` beneath
`$XDG_CONFIG_HOME`, or beneath `~/.config` when that is unset; `-config
/absolute/path/to/config.json` chooses a different file. A missing file selects
the built-in Claude Code catalog. The daemon validates harness names at startup.

Each harness lists at least one model, each with the effort levels it takes,
and at least one permission mode. Every choice has a nonempty `id`, passed to
the harness, and a `name` to show. A model with no effort levels omits `efforts`
or lists none, and takes an empty `effort`. The
required `defaults` name the harness, model, effort, and permission mode a new
session starts with, each one of the configured choices; the daemon refuses to
start otherwise.

```json
{"harnesses":{"claude":{"models":[{"id":"claude-haiku-5-5","name":"Haiku 5.5","efforts":[{"id":"low","name":"Low"},{"id":"high","name":"High"}]}],"permission_modes":[{"id":"manual","name":"Manual"},{"id":"plan","name":"Plan"}]}},"defaults":{"harness":"claude","model":"claude-haiku-5-5","effort":"low","permission_mode":"plan"}}
```

Clients offer these choices, and the daemon accepts only these in
[`session/create`](#sessioncreate) and [`session/configure`](#sessionconfigure).
It passes them to the harness as they are, so a configured value the harness
does not accept surfaces as its error, usually on the next `session/send`.
Claude ignores an effort level it does not know, so the daemon refuses to
start with one and a session stored with one fails to start; see
[Claude's effort levels](claude-code.md#initialize-first).

### `harness/list`

Returns the configuration's harnesses and new-session defaults.

```json
{"jsonrpc":"2.0","id":1,"method":"harness/list","params":{}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"harnesses":{"claude":{"models":[{"id":"claude-haiku-5-5","name":"Haiku 5.5","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"manual","name":"Manual"}]}},"defaults":{"harness":"claude","model":"claude-haiku-5-5","effort":"low","permission_mode":"manual"}}}
```

## Workspace methods

### `workspace/create`

Requires `path`, an absolute path to an existing directory. Registers the
directory and returns its workspace ID. An already registered path returns
the existing ID.

```json
{"jsonrpc":"2.0","id":1,"method":"workspace/create","params":{"path":"/Users/kyle/src/ox"}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"workspace_id":"workspace-id"}}
```

### `workspace/list`

Returns every workspace with its sessions, each oldest first. A session's
settings and state come from `session/get`.

```json
{"jsonrpc":"2.0","id":2,"method":"workspace/list","params":{}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"workspaces":[{"workspace_id":"workspace-id","path":"/Users/kyle/src/ox","created_at":"2026-11-18T19:53:47.102Z","sessions":[{"session_id":"session-id","harness":"claude","created_at":"2026-11-18T19:53:48.416Z"}]}]}}
```

## Session methods

### `session/create`

Records a parked session in the workspace and returns its ID. The first
[`session/send`](#sessionsend) starts the harness, so startup errors surface
there. A setting the [configuration](#configuration) does not offer returns
`-32602`.

| Parameter | Required | Meaning |
| --- | --- | --- |
| `workspace_id` | Yes | The workspace whose directory the harness runs in. |
| `harness` | Yes | A harness in the configuration. |
| `model` | Yes | One of the harness's models; passed to Claude's `--model` flag. |
| `effort` | No | One of the model's effort levels; passed to Claude's `--effort` flag. A model without effort levels takes an empty or omitted value, which leaves the flag unset. |
| `permission_mode` | Yes | One of the harness's permission modes; passed to Claude's `--permission-mode` flag. |

Each process the session starts receives the stored settings.

```json
{"jsonrpc":"2.0","id":3,"method":"session/create","params":{"workspace_id":"workspace-id","harness":"claude","model":"claude-haiku-5-5","effort":"low","permission_mode":"manual"}}
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

Requires `session_id`. Returns the session's settings, its conversation so
far, and its current state. The connection already receives the session's updates: those
that arrive before the response are included in it, and those after it apply
on top of it. A client loading a session ignores its updates until the
response arrives.

```json
{"jsonrpc":"2.0","id":5,"method":"session/get","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":5,"result":{"model":"claude-haiku-5-5","effort":"low","permission_mode":"manual","state":"requires_action","prompt":{"message_id":"prompt-id"},"approvals":[{"request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"touch notes.txt"}}}],"history":[{"message_id":"prompt-id","type":"prompt","text":"Create notes.txt.","time":"2026-11-18T19:53:50Z"},{"message_id":"prompt-id","type":"text_delta","text":"I'll create the file.","time":"2026-11-18T19:53:51Z"}]}}
```

| Field | Meaning |
| --- | --- |
| `model`, `effort`, `permission_mode` | The session's stored settings. |
| `state` | `parked`, or the execution state: `running`, `requires_action`, or `idle`. A prompt is `running` from the moment `session/send` writes it. |
| `prompt` | The active prompt, or `null`. |
| `prompt.message_id` | The prompt's identifier, matching its updates. |
| `approvals` | Pending approvals in the order they arrived, each with `request_id` and Claude's `request`, whether or not a prompt is active. |
| `history` | The session's recorded updates, oldest first, in the form of `session/update` params without `session_id`. |

The daemon records every session's `prompt`, `text_delta`, `tool_use`,
`tool_result`, `result`, and `exited` updates, including those of turns Claude starts itself, and keeps
them across parking and daemon restarts. Consecutive `text_delta` fragments
are recorded as one. Applying the history the same way as live updates
reproduces the conversation. Deleting the session deletes its history.

### `session/configure`

Requires `session_id` and at least one of `model`, `effort`, and
`permission_mode`. Changes the settings given and keeps the others, preserving
the conversation, and stores them for the session's later processes. The
changed settings must be configured choices, as at creation: a changed model
or effort is checked as a pair, and a changed permission mode alone. A stored
setting the configuration no longer offers stays until it is changed.

```json
{"jsonrpc":"2.0","id":6,"method":"session/configure","params":{"session_id":"session-id","effort":"high"}}
```

```json
{"jsonrpc":"2.0","id":6,"result":{}}
```

A parked session stores the settings and applies them when its next process
starts. A running session must be idle with no pending prompt. Configuration
reserves it until completion; sends and other configuration requests during
that time return `-32002`.

For a running session, success means Claude acknowledged each setting that
differs from its process's, and only then are they stored. Model, effort, and permission
mode are applied in that order. If a change fails, earlier changes remain
applied to the process and later changes are skipped. Claude refusals and
write failures return an operation error. The session takes whatever settings
its process now uses, even when the request fails or they cannot be stored,
and a `session/configured` event reports them before the response. Settings
that could not be stored last until the daemon restarts. A change Claude has not acknowledged
after one minute stops the process. If the process exits first, including by
that stop, the settings are stored for the next process and the request
succeeds. See [Claude's model validation](claude-code.md#initialize-first).

### `session/approve`

Requires `session_id`, `request_id`, and a `response` object whose `behavior`
is `"allow"` or `"deny"`. The response payload is forwarded to Claude.

```json
{"jsonrpc":"2.0","id":7,"method":"session/approve","params":{"session_id":"session-id","request_id":"claude-request-id","response":{"behavior":"allow","updatedInput":{"command":"echo hello"},"toolUseID":"toolu_example"}}}
```

```json
{"jsonrpc":"2.0","id":7,"result":{}}
```

For rejection, use `{"behavior":"deny","message":"Reason for rejection"}`
as `response`.

The Claude harness checks the response shape and preserves its fields. When
supplied, `updatedInput` must be an object and `updatedPermissions` must be an
array; a running session returns `-32000` for an invalid shape. See
[Claude's permission responses](claude-code.md#5-permissions-and-questions)
for permission changes, interrupts, and `AskUserQuestion` answers.

A successful RPC consumes the pending request; a duplicate or late answer is
rejected. Success confirms forwarding to Claude, not tool execution. Continue
reading prompt updates for the outcome.

### `session/interrupt`

Requires `session_id`. Asks a running session to stop its turn and the tools
it runs, using Claude's [interrupt](claude-code.md#interrupt). Success means
the request was written; a session that is idle or parked has nothing to stop
and also succeeds. The stopped turn ends with an error `result` whose text is
`Interrupted`, and an `idle` state, which release a prompt as usual.

```json
{"jsonrpc":"2.0","id":8,"method":"session/interrupt","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":8,"result":{}}
```

### `session/destroy`

Requires `session_id`. Removes the session from the daemon. A running process
receives SIGTERM so Claude can clean up its tool processes, and the daemon
waits for it to exit. Pending approvals are withdrawn, and an active prompt
receives an `exited` update before the destroy response. Claude's transcript
stays on disk.

```json
{"jsonrpc":"2.0","id":9,"method":"session/destroy","params":{"session_id":"session-id"}}
```

```json
{"jsonrpc":"2.0","id":9,"result":{}}
```

## Session updates

Updates go to every connection. Every notification uses method
`session/update` and includes `session_id`, `type`, and `time` inside
`params`, and `message_id` when the update belongs to a client's prompt: the
prompt itself, the turn that takes it up to its `result`, and the `state` or
`exited` update that releases it.
`time` is when the daemon published the update, as an RFC 3339 timestamp; a
recorded reply's `text_delta` carries the time of its first fragment.
Turns Claude starts itself, such as a scheduled prompt firing or a background
task finishing, produce the same updates without `message_id`. Such a turn
can run after a prompt's `result` and before the `idle` that releases it:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"text_delta","text":"This repository ","time":"2026-11-18T19:53:50.674Z"}}
```

| `type` | Additional fields | Meaning |
| --- | --- | --- |
| `prompt` | `text`: string | A client sent this prompt. |
| `text_delta` | `text`: string | Append this fragment of top-level assistant text. |
| `tool_use` | `tool_use_id`: string; `tool_name`: string; `input`: object | Claude called a tool at the top level. |
| `tool_result` | `tool_use_id`: string; `result`: object with `text` and `is_error` | The outcome of the top-level tool call with this `tool_use_id`. `text` joins the result's text blocks. |
| `state` | `state`: `running`, `requires_action`, or `idle` | The session's execution state: Claude's report, or `running` for a prompt Claude has not taken yet. |
| `result` | `result`: object with `text` and `is_error` | Claude's final response or turn error. The text is a complete result, not another delta to append. |
| `approval` | `request_id`: string; `request`: object | A pending Claude request needing a client decision. |
| `approval_answered` | `request_id`: string | A client answered the approval. |
| `approval_cancelled` | `request_id`: string | Withdraw an unanswered approval. Discard any later answer. |
| `exited` | `error`: string | The process exited during an active prompt, which has ended, or exited without being stopped. |
| `parked` | — | The session's process has exited, for any reason. The last update from that process. |

`running` means Claude is working. `requires_action` means it is waiting for
user action; read the approval notifications to obtain the requests. `idle`
means the turn is over and, for a prompt, that another can be sent.

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"tool_use","tool_use_id":"toolu_example","tool_name":"Bash","input":{"command":"ls","description":"List files"},"time":"2026-11-18T19:53:51Z"}}
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"tool_result","tool_use_id":"toolu_example","result":{"text":"go.mod\ncmd","is_error":false},"time":"2026-11-18T19:53:52Z"}}
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"result","result":{"text":"The repository contains a Go daemon.","is_error":false},"time":"2026-11-18T19:53:53Z"}}
```

Claude's turn errors are delivered as `result.is_error: true`. JSON-RPC errors
describe failures of the client operation itself. A subagent appears in the
stream as its top-level `Agent` tool call and result.

### Approval lifecycle

Ox forwards the complete [`can_use_tool`](claude-code.md#can_use_tool) request, including any display
metadata, tool input, permission suggestions, or question fields:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"approval","request_id":"claude-request-id","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"echo hello"},"tool_use_id":"toolu_example","permission_suggestions":[]},"time":"2026-11-18T19:53:51Z"}}
```

The client chooses how to obtain the decision and replies using
`session/approve`. Reading output continues while approvals are pending.
Multiple approvals can be pending; identify each by its `request_id`.

An approval waits until a client answers it, however long that takes; while
it is pending, the session's process keeps running.

When Claude cancels a request, becomes idle, or exits, Ox withdraws unanswered
approvals. A `result` alone does not withdraw approvals: background subagents
can still be waiting for a decision. For idle or exit, the cancellation
notifications precede the corresponding `idle` or `exited` update:

```json
{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"session-id","message_id":"prompt-id","type":"approval_cancelled","request_id":"claude-request-id","time":"2026-11-18T19:53:52Z"}}
```

## Workspace and session events

Every connection is told when workspaces and sessions come and go:

| Method | `params` | Meaning |
| --- | --- | --- |
| `workspace/created` | `workspace_id`, `path`, `created_at` | A workspace was registered. |
| `session/created` | `session_id`, `workspace_id`, `harness`, `created_at` | A session was created, parked. |
| `session/configured` | `session_id`, `model`, `effort`, `permission_mode` | A session's settings changed; the fields are its stored settings. |
| `session/destroyed` | `session_id` | A session was destroyed, after its last update. |

```json
{"jsonrpc":"2.0","method":"session/created","params":{"session_id":"session-id","workspace_id":"workspace-id","harness":"claude","created_at":"2026-11-18T19:53:48.416Z"}}
```

## Connections

Any connection can send a session a prompt when it is idle, answer its
approvals, interrupt it, configure it, and destroy it.

Closing a connection leaves its sessions running. A prompt continues, and its
pending approvals stay pending until a client answers them. A client that
connects calls `workspace/list` and `session/get` to load the sessions, then
follows the notifications.

## Errors

Error responses echo the JSON-RPC request ID. Parse errors and invalid request
envelopes use `id: null`. Error messages for operation failures can include
underlying process or I/O errors.

```json
{"jsonrpc":"2.0","id":5,"error":{"code":-32002,"message":"session is busy"}}
```

| Code | Meaning |
| --- | --- |
| `-32700` | Parse error: malformed JSON. |
| `-32600` | Invalid request envelope, field types, JSON-RPC version, or batch request. |
| `-32601` | Unknown method. |
| `-32602` | Missing or invalid method parameters. |
| `-32000` | Operation failed, such as Claude startup, a process write failure, a database write failure, or an approval response Claude cannot take. |
| `-32001` | Workspace or session not found. |
| `-32002` | Session is busy; a prompt or configuration cannot start yet. |
| `-32003` | Approval request is no longer pending, including duplicate or cancelled answers. |
