# Driving Claude Code with `claude -p` and stream-json

Observed with Claude Code 2.1.312 through 2.1.331 (October–November 2026).

## 1. Launch

### Persistent session

Run one long-lived process per conversation and keep its stdin open across
turns:

```
claude -p \
  --input-format stream-json --output-format stream-json --verbose \
  --include-partial-messages \
  --thinking-display summarized \
  --replay-user-messages \
  --permission-mode <mode> --permission-prompt-tool stdio \
  --system-prompt-snapshot off \
  --model <model> --effort <level> \
  [--append-system-prompt <text>] \
  [--allowedTools=mcp__<server>__<tool>,...] \
  [--disallowedTools=<tool>,...] \
  [--settings <json>] \
  [--mcp-config '{"mcpServers":{...}}'] \
  (--session-id <uuid> | --resume <uuid> | --resume <parent> --fork-session --session-id <uuid>)
```

- `--verbose` is required with `--output-format stream-json`.
- `--include-partial-messages` adds `stream_event` lines carrying raw Anthropic
  SSE events (text and thinking deltas).
- `--thinking-display summarized` streams Claude's summary of its thinking in
  `thinking_delta`. It is accepted though absent from `--help` (2.1.330).
- `--replay-user-messages` echoes each prompt back as a `user` line with
  `isReplay: true` and the prompt's `uuid` once Claude takes it into a turn.
- `--permission-prompt-tool stdio` routes permission prompts to the host as
  `can_use_tool` control requests on stdout. The alternative is
  `--permission-prompt-tool mcp__server__tool`.
- `--system-prompt-snapshot off`: by default Claude records the fully composed
  system prompt on the first request and reuses it, including across `--resume`,
  until compaction. A host that passes `--append-system-prompt` on every launch
  needs the snapshot off for later launches to take effect.
- `--mcp-config` accepts inline JSON. Shape:
  `{"mcpServers":{"<name>":{"command":"/abs/path","args":[],"env":{}}}}`. MCP
  tools are named `mcp__<server>__<tool>`. The servers it names are added to the
  user's, plugin and claude.ai servers, which keep loading without
  `--strict-mcp-config`; an added server reports `source: "dynamic"`.
- `--allowedTools=mcp__<server>__<tool>` pre-approves one MCP tool; several go
  comma-separated in one flag. `--allowedTools` auto-approves without
  restricting the tool catalog; `--tools` restricts built-in tools and does not
  cover MCP tools.
- Every process, `-p` included, listens on `/tmp/cc-socks/<pid>.sock`
  (`$XDG_RUNTIME_DIR/cc-socks/` when that is set) for cross-session messages
  from the user's other local Claude Code sessions. The model gets `ListAgents`
  and `SendMessage`, which list and message those sessions. Other sessions list
  a `-p` process as `interactive`, named after its working directory (2.1.328).
- `--disallowedTools=SendMessage,ListAgents` removes both tools from the main
  thread and its subagents; the socket stays bound (2.1.328).
- The `init` line lists the subagent tool as `Task`. `--disallowedTools=Agent`
  or `=Task`, or a bare `Agent` in `permissions.deny`, removes it from the
  model's tool list. Skills with `context: fork` still run in their subagent
  (2.1.330).
- `--disallowedTools=EnterWorktree,ExitWorktree` drops both tools from the
  `init` line's tool list (2.1.331).
- The `crossSessionInbound` setting takes `accept`, `hold`, or `refuse`. Unset,
  a message is delivered only when the sender's permission-mode class matches
  the receiver's. `--settings '{"crossSessionInbound":"refuse"}'` outranks
  `accept` in project settings, and managed settings can only make it stricter.
  With `refuse`, the process stays listed and the sender's `SendMessage`
  succeeds. The sender then gets a `[Cross-session delivery notice]` that the
  message was refused, and the receiving process writes nothing and starts no
  turn (2.1.328).
- An accepted message to an idle process starts a turn Claude starts itself:
  `command_lifecycle` `started`, whose `command_uuid` is the uuid of an `isMeta`
  user entry, then `system` `init`, the reply, `result`, and `command_lifecycle`
  `completed`. Stdout carries no `user` line with the message (probed without
  `--replay-user-messages`). The transcript holds it as `<cross-session-message
  from="uds:<socket>" from-name="<name>"
  from-mode="prompting">…</cross-session-message>`, with `origin.kind` `"peer"`
  (2.1.328).
- `plan` mode ignores `--allowedTools` for an MCP tool and asks with
  `decision_reason_type: "mode"`, unless the tool's `tools/list` entry has
  `annotations: {"readOnlyHint": true}`. `readOnlyHint` alone does not skip the
  prompt in `default`, so a tool that runs without asking in every mode needs
  both.
- `auto` runs an MCP tool `--allowedTools` names, read-only or not, without
  asking and without a classifier refusal (2.1.328).
- In `plan` mode Haiku declines to call an MCP tool that is not read-only,
  citing plan mode, even when the prompt asks for the call and says it asks for
  approval first (2.1.328).
- An MCP tool's text result of about 38,000 characters reaches the model whole,
  and the `tool_result` the stream reports carries all of it (2.1.329).
- MCP tools are deferred: the model loads one with `ToolSearch` `select:<name>`,
  which needs no approval, before calling it, unless the resumed conversation
  already loaded it.
- `--allowedTools` and `--disallowedTools` are variadic and consume every
  following argument that is not a flag. Put another `--flag` right after their
  values.
- Per-process flags (`--mcp-config`, `--settings`, `--plugin-dir`, `--add-dir`,
  `--fallback-model`, `--append-system-prompt`, model, effort, permission mode)
  must be passed again on every resume. A resumed process runs the
  `--permission-mode` it is given, or `default` without one, whatever mode the
  transcript was written in (2.1.327; the `system` `init` line's
  `permissionMode` and its approval requests both follow the flag).
- `--session-id` must be a UUID.
- Set the process's working directory to the session's directory.

### Environment

- `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` enables
  [`session_state_changed`](#system) events.
- `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0` makes Claude report `idle` at the end
  of a turn while background tasks keep running, then `running` when a turn they
  trigger begins. With the default, a background subagent keeps the state at
  `running` until it finishes, and the follow-up turn gets no fresh `running`.
  Background Bash and Monitor report `idle` either way.
- Remove `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` from the child
  environment to keep subscription auth; in `-p` mode an inherited API key is
  always used.
- `CLAUDE_CONFIG_DIR` (default `~/.claude`) isolates settings, history, plugins
  and most auth. `~/.claude.json` still holds global trust, account and MCP
  state.
- Claude sets `CLAUDE_CODE_SESSION_ID` in its tools' environment, so the agent
  can read its own conversation ID.
- Retry and timeout knobs: `API_TIMEOUT_MS` (default 600000),
  `CLAUDE_CODE_MAX_RETRIES` (default 10, max 15),
  `CLAUDE_ENABLE_STREAM_WATCHDOG=0`.
- To simulate a dead network: `HTTPS_PROXY=http://127.0.0.1:1
  ALL_PROXY=http://127.0.0.1:1 CLAUDE_CODE_MAX_RETRIES=0 API_TIMEOUT_MS=5000`.
- `claude` is typically installed in `~/.local/bin`. A GUI app or service has to
  take `PATH` from the login shell (`$SHELL -ilc`) to find it.

### Behavior of `-p`

- `-p` skips the trust dialog and silently drops invalid non-managed settings.
- Settings files reload while running; model and effort changes need control
  requests.
- `claude -p` with empty stdin exits 1.
- `--help` omits some documented flags, including `--permission-prompt-tool`,
  `--max-turns`, and `--system-prompt-file`.

## 2. The stdin protocol

Every message is one JSON object per line, terminated by `\n`.

### Initialize first

Send this immediately after spawning, before any prompt:

```json
{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}
```

The response carries the slash-command catalog and the model list. No model turn
runs and no conversation is created:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"initialize","response":{
  "commands":[{"name":"plugin:review","description":"Review","argumentHint":"file","aliases":["review"]},
              {"name":"clear","description":"Clear","argumentHint":"","builtin":true}],
  "models":[{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)",
             "supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
            {"value":"haiku","resolvedModel":"claude-haiku-5-5","displayName":"Haiku", "...":"..."}]}}}
```

- `models[].value` is the identifier accepted by `--model` and runtime model
  changes. `resolvedModel` is the model an alias selects.
  `supportedEffortLevels` lists the effort choices for models with
  `supportsEffort`.
- A refusal is `{"subtype":"error","request_id":"initialize","error":"..."}`.
- The response carries `session_state` (`idle` or `running`). Sent during a
  turn, `initialize` is answered at once with `running` (2.1.331).
- The response does not wait for MCP servers to connect. The first turn waits
  for them. It does wait for SessionStart hooks to finish (2.1.331).
- Claude accepts any model in `--model` or a runtime change and fails only on
  the next prompt. An unknown `--effort` value produces a stderr warning, and
  Claude runs at its default effort.
- Model discovery alone: run `claude -p --input-format stream-json
  --output-format stream-json --verbose` with no `--model` and no MCP config in
  the project directory, send `initialize`, read `models`, close stdin.

### User messages

```json
{"type":"user","uuid":"<your id>","session_id":"","message":{"role":"user","content":[...]},"parent_tool_use_id":null,"priority":"next"}
```

- `content` may be a string or content blocks:
  - `{"type":"text","text":"..."}`
  - `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}`
    (PNG, JPEG, GIF, WebP). A prompt of image blocks, with or without a text
    block, is taken as a text prompt is: `command_lifecycle` reports it the same
    way, and its replay echo carries the image blocks. Claude normalizes the
    images itself: a 6.75 MB PNG, a 9000×100 PNG, and JPEG data labeled
    `image/png` all succeed, and later turns and `--resume` still work
    (2.1.331).
  - `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"..."}}`
  - Embedded text can be sent as a text block wrapping it in `<context
    ref="uri">…</context>`; a file link as `[@name](uri)`, which Claude opens
    with its tools.
- `uuid` is your handle for the prompt. Claude reports it back when it takes the
  prompt into a turn (section 4).
- Set `"priority":"next"` explicitly; the default depends on Claude's feature
  flags (2.1.326). With `next`, a prompt written during a turn is taken in at
  the turn's next opportunity (steering); one not taken before the turn ends
  runs as its own turn (2.1.328):
  - Claude takes it (`command_lifecycle started`) right after the running tool
    call's `tool_result`, with no new `system init`, and the turn ends with one
    `result`.
  - While a `can_use_tool` request waits, the prompt stays untaken. Claude takes
    it after the answer's `tool_result`.
  - A turn with no further tool calls does not take it. After the turn's
    `result`, Claude starts the prompt as its own turn (`started`, a new `system
    init`, its own `result`) with no `idle` in between. `idle` follows only the
    last turn.
- A message cannot be withdrawn once written, short of an interrupt with
  `cancel_queued`.
- Slash commands and user-invocable skills are plain prompt text: `/review
  src/main.rs`. Local commands such as `/model` and `/clear` complete without a
  model call. `/model sonnet` and `/effort high` change the process's settings
  with confirmation in reply text, not a structured settings acknowledgement
  (2.1.328). An unknown command reaches the model as ordinary text.
  `--disable-slash-commands` disables both commands and skills; streamed
  `client_composed: true` bypasses slash dispatch.

### Control requests to Claude

Envelope: `{"type":"control_request","request_id":"<unique>","request":{...}}`.
Match responses by `request_id`; unrelated `control_response` lines arrive
interleaved.

| Request                                                                              | Effect                                                                                                                                         |
| ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `{"subtype":"initialize"}`                                                           | Handshake; commands and models.                                                                                                                |
| `{"subtype":"interrupt"}`                                                            | Stops the running turn and its tools. Response: `{"still_queued":[...]}`.                                                                      |
| `{"subtype":"interrupt","cancel_queued":true}`                                       | Also drops prompts Claude has not taken yet.                                                                                                   |
| `{"subtype":"set_model","model":"haiku"}`                                            | Changes the model. Does not change effort.                                                                                                     |
| `{"subtype":"set_permission_mode","mode":"acceptEdits"}`                             | Changes the permission mode. Response: `{"mode":<mode>}`, with `default` for `manual`.                                                         |
| `{"subtype":"apply_flag_settings","settings":{"model":"haiku","effortLevel":"max"}}` | In-process flag override (no settings file write). Can affect the next model call within the running turn. `"effortLevel":null` clears effort. |
| `{"subtype":"reload_skills"}`                                                        | Rescans skills; response `{"skills":[...]}`. Skills added after `initialize` are neither listed nor invocable until this runs.                 |
| `{"subtype":"reload_plugins"}`                                                       | Response has a refreshed `commands` list in the `initialize` shape.                                                                            |

Responses are
`{"type":"control_response","response":{"subtype":"success","request_id":"..."}}`
or `{"subtype":"error","request_id":"...","error":"bad model"}`.

Permission modes: `manual` (alias `default`), `auto`, `acceptEdits`, `plan`,
`dontAsk`, `bypassPermissions`. In `auto`, requests the classifier rejects never
reach the host.

### Stopping

Closing stdin lets Claude exit on its own once no foreground tool runs; while
one runs, Claude keeps running (2.1.331).

Each Bash tool call, foreground or background, runs in its own session
(`setsid`), outside Claude's process group. SIGTERM to Claude alone ends them
with Claude, which exits within about a second (2.1.331).

A new process can `--resume` a conversation at once after the process running it
receives SIGTERM, while that process is still exiting; the resumed conversation
keeps the earlier turns (2.1.329).

## 3. The stdout protocol

Stdout carries JSON lines and can carry plain text on
[failure](#startup-failures). `stream_event` lines carry an SSE event in
`.event`.

### `system`

- `init`: `session_id`, `model` (resolved ID), `tools`, `mcp_servers`,
  `slash_commands`, `skills`, `permissionMode` (the mode the turn runs in, with
  `manual` reported as `default`). Emitted at the start of every turn, including
  turns Claude starts itself. [`initialize`](#initialize-first) supplies startup
  data. Each `mcp_servers` entry is `{name, status, source}`, with `status`
  `connected`, `failed`, or `needs-auth`; a server whose command is missing or
  whose `initialize` fails is `failed`, and Claude runs without its tools.
- `status`: `status` and `permissionMode`, emitted after every permission mode
  change: a `set_permission_mode` (after its `control_response`),
  `EnterPlanMode` (which asks for no approval), and an approved `ExitPlanMode`
  (to `default` in a process started in `plan`).
- `session_state_changed`: `state` is `running`, `requires_action` (just before
  a `can_use_tool` request) or `idle`; carries `"sdk_host_only":true`. `idle`
  arrives after the turn's `result` is flushed.
- `commands_changed`: `commands` replaces the slash-command catalog (possibly
  with an empty list).
- `task_started`: `task_id`, `tool_use_id`, `is_backgrounded`. Foreground tasks
  get this too but never join the live set.
- `task_updated`: progress, safe to ignore.
- `task_notification`: `task_id`, `tool_use_id`, `status` (`completed` /
  `stopped` / `failed`), `summary`.
- `background_tasks_changed`: `tasks[]` is the complete live set, each item
  carrying `task_id`. Not emitted at startup; start from an empty set.
- `turn_preempted` (`preempted_by_uuid`) is sent only when `initialize` declares
  `rapidFollowupPreempt`. Without that declaration a running turn is never
  preempted by a new prompt.

### `stream_event`

```json
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The command"}},
 "session_id":"...","parent_tool_use_id":null,"api_message_id":"msg_..."}
```

- Use `content_block_delta` with `delta.type` `text_delta` (`.text`) or
  `thinking_delta` (`.thinking`).
- Without `--thinking-display summarized` every `thinking_delta` is empty (with
  `estimated_tokens`), and the final thinking block is
  `{"type":"thinking","thinking":""}`. With it, `thinking_delta` streams
  summarized text, and the final block carries that text with a `signature`.
  `showThinkingSummaries` in `--settings` changes neither (2.1.330). Drop empty
  thinking and whitespace-only text.
- `index` is the block's position in the whole API message. Key streamed items
  by `api_message_id:index`.
- Subagent lines arrive as complete messages, without `stream_event`s.

### `assistant`

`message.{id, model, content[], usage}` plus `parent_tool_use_id` and
`session_id`.

- One API message arrives as several `assistant` lines sharing `message.id`,
  each carrying only its newest block (for example a thinking line, then a text
  line). Count blocks per `message.id` to recover the index that matches
  `stream_event.index`; empty thinking blocks count, so text after them is
  index 1.
- Each split line repeats the same `usage`.
- `message.stop_reason` is always `null` here; it cannot detect a turn's end.
- The complete message repeats text already streamed as deltas. Skip text and
  thinking blocks once deltas were seen for them.
- `tool_use` blocks: `id`, `name`, `input`. Claude-generated synthetic messages
  use `"model":"<synthetic>"`.
- `isMeta: true` messages are notes Claude adds for the model; hide them.

### `user`

- Tool results: `content` blocks
  `{"type":"tool_result","tool_use_id":"...","is_error":false,"content":...}`.
  `content` is a string or an array of `text`, `image` (base64 or `url` source),
  `tool_reference` and other blocks.
- An MCP tool result with `isError: true` arrives as a `tool_result` with
  `is_error: true` and the result's text (2.1.328).
- An MCP call's `tool_result` line follows the MCP server's answer to it, and
  the next step's `tool_use` follows that line: a turn that calls
  `send_message`, then `list_sessions` in a later step, emits each call's
  `tool_use` and `tool_result` in that order (2.1.331, Haiku).
- Structured tool output is on the line as `tool_use_result` (in transcripts:
  `toolUseResult`). It belongs to the line, so it is ambiguous when one line
  carries several `tool_result` blocks.
- A `user` line whose `content` is a plain string, or one with `isReplay: true`,
  is the prompt echo.
- Interrupts produce synthetic text: `[Request interrupted by user]` or
  `[Request interrupted by user for tool use]`.

### `control_request` from Claude

Answer `can_use_tool`. Ox answers every `hook_callback` with success, and every
other subtype (MCP messages) with an error so Claude does not wait:

```json
{"type":"control_response","response":{"subtype":"error","request_id":"<id>","error":"Unsupported control request"}}
```

`control_cancel_request` `{"request_id":"..."}` withdraws a pending
`can_use_tool`. Close the dialog and discard a late answer.

### `result`

```json
{"type":"result","subtype":"success","is_error":false,"result":"<final text>","terminal_reason":"completed",
 "session_id":"...","total_cost_usd":0.0058,"num_turns":1,"duration_ms":0,
 "usage":{},"modelUsage":{"claude-haiku-5-5":{"contextWindow":1000000}}}
```

- `terminal_reason` values seen: `completed`, `aborted_tools`,
  `aborted_streaming`, `model_error`. A reason starting with `aborted` is a user
  interrupt, not an error.
- `subtype` values: `success`, `error_during_execution`, `error_max_turns`.
- `is_error: true` can occur with `subtype: "success"`, e.g. `"result":"API
  Error: 529 overloaded","terminal_reason":"model_error"`. Check both fields.
- Error text: `result` if non-empty, else `errors[]` joined, else `subtype`.
  `errors` can hold diagnostics such as `"[ede_diagnostic] result_type=user
  last_content_type=n/a stop_reason=tool_use"`.
- An empty bookkeeping result (`"result":""`, no `terminal_reason`) can follow
  an automatic turn.
- A local command can return text in a successful `result` without any
  `text_delta` lines, as `/cost` and `/effort` usage do (2.1.328).
- `total_cost_usd` is a running total for the process.
- `modelUsage` is cumulative, keyed by resolved model ID, and gains an entry per
  model used (including subagents' and after model switches).

### `rate_limit_event`

Once per turn, after the assistant message and before the result; nothing before
the first turn. Subscription auth only.

```json
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793767800,"rateLimitType":"five_hour",
 "overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,
 "unifiedWindows":{"five_hour":{"utilization":0.01,"resetsAt":1793767800,"status":"allowed"},
                   "seven_day":{"utilization":0.39,"resetsAt":1794193200,"status":"allowed"}}}}
```

`utilization` is a fraction; `resetsAt` is Unix seconds. Windows are 300 and
10080 minutes.

### `command_lifecycle`

`{"type":"command_lifecycle","command_uuid":"<your uuid>","state":"started"}`
marks a prompt taken into a turn. The same line with `queued` marks a prompt
received but not yet taken, which a prompt written during a turn gets at once;
with `cancelled` a prompt an interrupt dropped or stopped; and with `completed`
a prompt whose turn finished, around the turn's `result`. A prompt steered into
a turn completes before the turn's original prompt (2.1.328).

## 4. Turns

### When a prompt is taken

Either [`command_lifecycle`](#command_lifecycle) or the
[prompt echo](#persistent-session) signals prompt uptake. Output and completion
arrive separately.

An idle process reports `session_state_changed` `running` within milliseconds of
receiving a prompt, before taking it (2.1.328).

### When a turn ends

- `result` ends a model turn but does not mean Claude is idle. With a background
  subagent the sequence is: main `result` while the subagent runs → subagent
  lines continue → new `system/init` and an automatic main turn → its `result` →
  an empty bookkeeping `result` → `session_state_changed idle`.
- `idle` marks the end of the turn and can arrive directly after a cancellation.
  A background subagent can still be waiting for a permission answer after a
  main-thread `result`.
- A resumed conversation can start a turn during startup, for example to handle
  a background task notification.
- A resumed process starts its restored schedules once its SessionStart hooks
  finish, whether or not the host has sent `initialize`. Without `initialize` a
  due job runs a full turn, approval requests included, and the hooks
  `initialize` would declare are not called. `interrupt` before `initialize`
  stops such a turn exactly as after it. A host that writes `initialize` at
  spawn gets the response before any scheduled turn (2.1.331).
- A successful `result` confirms the newest main-thread assistant message as the
  final answer. A bookkeeping result preserves that answer.

### Interrupt

Send `interrupt` (with `cancel_queued: true` to drop untaken prompts). The
sequence with a running Bash tool:

1. `control_response` for the interrupt.
2. The running tool's `tool_result`, `is_error: true`, text starting "The user
   doesn't want to proceed with this tool use…".
3. `user` text `[Request interrupted by user for tool use]`.
4. `result` with `subtype:"error_during_execution"`, `is_error:true`,
   `terminal_reason:"aborted_tools"`.
5. `session_state_changed idle`.

The stopped turn stays in the transcript.

A prompt Claude has received but not taken (2.1.328):

- `interrupt` answers `{"still_queued":["<uuid>"]}`, then Claude takes the
  prompt and stops it at once: `command_lifecycle started`, `user` text
  `[Request interrupted by user]`, a `result` with
  `terminal_reason:"aborted_streaming"` and no API call, `command_lifecycle
  cancelled`, and `idle`.
- `interrupt` with `cancel_queued: true` answers
  `{"still_queued":[],"cancelled":["<uuid>"]}` after `command_lifecycle
  cancelled`, then `idle`, with no turn and no `result`.

A prompt written during a turn and not yet taken, with `cancel_queued: true`
(2.1.328): `command_lifecycle cancelled` for the queued prompt, then the
response `{"still_queued":[],"cancelled":["<uuid>"]}`, then steps 2 to 4 above
for the running turn, then `command_lifecycle cancelled` for the turn's prompt,
then `idle`.

### Session IDs

`--resume` needs a saved transcript. `command_lifecycle started` and
`system/init` come before the first user entry is written; `system/init` can
already have a transcript file with only metadata. In a new Haiku conversation
on 2.1.328, the prompt was on disk by the first model stream event, before an
approval and before any `result`. A fork also needs its first saved turn.

`/clear` emits `conversation_reset` under the old `session_id`. The following
`system/init` and empty successful `result` carry the new `session_id`, whose
transcript has user entries by that result (2.1.328). `--resume` can continue
that new conversation. The `new_conversation_id` in the reset event is not the
new `session_id`.

## 5. Permissions and questions

### `can_use_tool`

```json
{"type":"control_request","request_id":"1561f8fc-...","request":{"subtype":"can_use_tool",
 "tool_name":"Bash","display_name":"Bash","title":"...","description":"...",
 "input":{"command":"echo hi there","description":"..."},
 "permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"echo hi *"}],
                            "behavior":"allow","destination":"localSettings"}],
 "suppress_always_allow_rule":false,"decision_reason_type":"subcommandResults","tool_use_id":"toolu_..."}}
```

An MCP tool's request also carries `mcp_server` `{name, source}` and
`display_name`. A request the permission mode forces, such as an MCP tool in
`plan`, carries `decision_reason_type: "mode"`.

Order on stdout: the assistant `tool_use`, `session_state_changed
requires_action`, the `control_request`, then `running` again after the answer.

Answer asynchronously so output keeps flowing while the user decides; keep an
abort handle per `request_id` for `control_cancel_request` and for turn end.

Allow:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{
  "behavior":"allow","updatedInput":<the original input, byte-for-byte>,
  "toolUseID":"toolu_...","decisionClassification":"user_temporary"}}}
```

- Echo `input` unchanged. Hold it as raw JSON rather than decoding and
  re-encoding it. Send `{}` if it is not an object.
- "Allow always": `"decisionClassification":"user_permanent"` plus
  `"updatedPermissions": <permission_suggestions>`. Offer it only when
  suggestions exist and `suppress_always_allow_rule` is false. The suggestions'
  `destination` is typically `localSettings`, so this writes
  `.claude/settings.local.json`. To keep grants out of the project, store them
  in the host and answer `allow` per call.

Deny:

```json
{"behavior":"deny","message":"Permission denied by client","toolUseID":"toolu_...","decisionClassification":"user_reject"}
```

A deny becomes a tool error whose text is the deny's `message`, and the model
continues. Text typed by the user goes in `message` and reaches the model as
written; a deny with `"message":"Do not create files."` produced:

```json
{"type":"tool_result","content":"Do not create files.","is_error":true,"tool_use_id":"toolu_..."}
```

Setting `interrupt: true` on the deny stops the turn instead.

### `AskUserQuestion`

It arrives twice: as an assistant `tool_use` and as `can_use_tool` with
`tool_name:"AskUserQuestion"` and `"requires_user_interaction":true`.

Input: `questions[]` (1–4), each `{question, header (≤12 chars), options:
[{label, description}] (2–4), multiSelect}`.

Answer by allowing with the original input plus `answers`, keyed by question
text, all questions in one response:

```json
{"behavior":"allow","updatedInput":{"questions":[...],"answers":{"Which color?":"Red","Which sections?":"Introduction, Conclusion, Appendix"}}}
```

- Single choice: the label, or free text in its place.
- Multi-select: chosen labels in option order, then free text, joined with `",
  "` (matches Anthropic's `ask-user-question-previews` demo).
- Omit unanswered questions.
- Resulting tool result: `Your questions have been answered: "Which
  color?"="Red". You can now continue with these answers in mind.`
- Deny when the user declines or the input cannot be shown.
- `--disallowedTools AskUserQuestion` makes Claude ask in plain text instead,
  for hosts without a form UI.

## 6. Errors and process I/O

### Startup failures

- A bad `--resume` prints a `result` before, or instead of, the `initialize`
  response, then exits 1:
  ```json
  {"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"1111...","total_cost_usd":0,"modelUsage":{},
   "errors":["No conversation found with session ID: 1111..."]}
  ```
  No `session_state_changed` precedes it (2.1.331).
- Claude can exit before reading `initialize`; the write can then fail with
  EPIPE while the error result is still readable.
- Claude can emit an error `result` while a prompt is waiting to enter a turn.
- Failures can appear as plain text on stdout: `Not logged in · Please run
  /login`, `API Error: Connection refused`, or an unknown-model message (exit
  1).
- An invalid option prints only on stderr, with empty stdout, and exits 1:
  `error: option '--permission-mode <mode>' argument 'acceptEdit' is invalid.
  Allowed choices are acceptEdits, auto, bypassPermissions, manual, dontAsk,
  plan.`
- SIGINT can exit 0 with empty stdout; SIGTERM exits 143.

### Pipes

- A child that inherits an output descriptor can delay EOF after Claude exits.
- Claude's final lines can still be buffered when the process exits.
- Base64 images and large tool results can produce long lines. The final line
  can end at EOF.

## 7. Subagents

- Subagent lines carry `parent_tool_use_id` (the `Agent`/`Task` tool_use ID),
  and also `agent_id`, `subagent_type`, `task_description`. Group them under the
  parent tool call.
- They interleave with main-thread streaming and continue after the main
  `result`.
- The `Agent` tool can return immediately ("Async agent launched successfully…
  agentId… output_file…"); its findings arrive later in an automatic main turn.
- Exclude subagent messages from context-size calculations and from the
  session's current model.
- A subagent tool requiring permission arrives as an ordinary `can_use_tool`.
- `--forward-subagent-text` forwards subagent text, which is otherwise not
  streamed.

## 8. Background work and schedules

- Background Bash, Monitor and background subagents appear in
  `background_tasks_changed`. A task's tool call gets its `tool_result` when the
  task starts; the task ends at its `task_notification`, and a turn's `result`
  does not end it.
- On ending, the order is `task_updated`, `task_notification`,
  `background_tasks_changed` without the task, then a turn Claude starts itself
  with no user prompt.
- `background_tasks_changed` can arrive before the `task_started` that maps
  `task_id` to `tool_use_id`. Resolve the mapping lazily.
- The Stop hook reports pending schedules. Declare it in `initialize` with
  `"hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}`. At the end of each
  completed turn, before `result` and `idle`, Claude sends
  `{"type":"control_request","request_id":"<id>","request":{"subtype":"hook_callback","callback_id":"stop","input":{"hook_event_name":"Stop","background_tasks":[],"session_crons":[{"id":"4dc9d149","schedule":"*/30 * * *
  *","recurring":true,"prompt":"say hi"}]}}}` and waits for
  `{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{}}}`.
  - `session_crons` is the complete list of `CronCreate` jobs, `ScheduleWakeup`
    wakeups and `/loop` jobs, including those a foreground subagent created;
    `[]` means none. A wakeup appears as a one-shot entry whose cron string
    encodes its fire minute. Each `prompt` is capped at 1000 characters.
  - `SubagentStop` (same declaration shape) carries the same field when a
    subagent finishes.
  - A turn that ends by interrupt or error sends no Stop callback (an API error
    fires `StopFailure`, which has no `session_crons`), and a resumed process
    reports its restored jobs only when its first turn ends. No startup line,
    `initialize` response included, names them (2.1.331).
  - A fired one-shot job or wakeup is gone from the list at the end of its fire
    turn.
- The tool calls and their structured results describe each schedule:
  - `CronCreate` input `{cron, prompt, recurring?, durable?}`, result `{id,
    humanSchedule, recurring, durable?, nextFireAt, maxAgeMs?}` (epoch ms;
    `maxAgeMs` on recurring jobs).
  - A `/loop` with an interval schedules its job with a main-thread `CronCreate`
    call (2.1.331).
  - `CronDelete` input `{id}`.
  - `ScheduleWakeup` input `{delaySeconds?, reason?, prompt?, stop?}`, result
    `{scheduledFor, clampedDelaySeconds, wasClamped, stopped?,
    cancelledWakeups?}`.
  - Times may be floats.
- Rules: recurring jobs expire 30 days after creation (2.1.331, the result's
  `maxAgeMs`), deleted after their final fire; a one-shot job is done once a
  turn starting at or after its fire time ends; in `-p` mode a dynamic `/loop`
  ends when a wakeup turn schedules nothing (2.1.327);
  `ScheduleWakeup{stop:true}` clears wakeups only once its result is successful.
- A turn Claude starts for a schedule carries `wakeup_source` on its
  `command_lifecycle` `started` line: `schedule_wakeup` for a cron job, or
  `loop_wakeup` with `scheduled_for` (epoch ms) for a dynamic `/loop` wakeup.
- `CronCreate` with `durable:true` returns `durable:false` (2.1.327, 2.1.331);
  durable persistence is unavailable.
- `claude --resume` restores unexpired main-thread recurring jobs and future
  one-shot jobs, not wakeups or jobs created by subagents. Durable jobs of other
  sessions in `.claude/scheduled_tasks.json` are not visible. The transcript's
  only record of a job is its `CronCreate` call and result, and a restored job
  keeps its `id` across successive resumes. A missed fire is not caught up: a
  recurring job next fires at its next scheduled time (2.1.331).

## 9. Usage and cost

- Context in use = `input_tokens + cache_read_input_tokens +
  cache_creation_input_tokens` of the latest main-thread assistant message.
- Context window = `result.modelUsage[<current resolved model>].contextWindow`;
  take the current model from `system/init.model` and main-thread
  `message.model`.
- Cost comes from [`result.total_cost_usd`](#result).
- `--max-budget-usd` (print only) is a client estimate that counts subagents and
  can overshoot.

## 10. Transcripts on disk

- Path: `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/<uuid>.jsonl`. To find a
  session, scan every project directory for `<id>.jsonl` whose first `cwd` entry
  equals the directory; reading the first 1 MiB is enough.
- Subagents: `<uuid>/subagents/agent-<id>.jsonl` beside `agent-<id>.meta.json`
  holding `{"toolUseId": ...}`. In the main file, subagent entries have
  `isSidechain: true`.
- Entries carry ISO `timestamp` (the first few may lack one), `cwd`, `uuid`, and
  `entrypoint` on an early entry: `cli` (terminal), `claude-desktop`, or
  `sdk-cli` (`claude -p` and the Agent SDK). `sdk-cli` transcripts also carry
  `promptSource:"sdk"`.
- Replay keeps `user` and `assistant` entries and skips `isMeta`. The entry
  `uuid` of a user message is the prompt's `uuid`. Tool calls without a result
  belong to interrupted turns.
- Titles: `{"type":"custom-title","customTitle":...}` from `/rename` and
  `{"type":"ai-title","aiTitle":...}`, repeated through the file; the last of
  each wins, custom over generated.
- Slash commands appear in transcripts and replayed prompts as
  `<command-message>…</command-message><command-name>/…</command-name><command-args>…</command-args>`,
  without the rendered skill body.
- Transcripts are deleted after `cleanupPeriodDays` (default 30); after that the
  conversation cannot be resumed.

## 11. Skills and commands

- `--add-dir DIR` loads skills from `DIR/.claude/skills/`, not from
  `DIR/<name>/SKILL.md`. To add a raw skills directory, make a `--plugin-dir`
  plugin whose `skills` points (or symlinks) to it; its skills appear as
  `plugin:name` with a bare alias.
- `user-invocable: false` disables direct `/name` dispatch;
  `disable-model-invocation: true` disables only model invocation.
- `system/init.slash_commands` and `.skills` carry names only; descriptions and
  argument hints come from `initialize.commands`.
- Validate catalog entries: non-empty name without whitespace, description
  present, deduplicate by name.

## 12. One-shot utility calls

For titles and similar stateless prompts:

```
claude -p --input-format stream-json --output-format stream-json --verbose \
  --safe-mode --no-session-persistence --tools '' --disallowedTools 'mcp__*' \
  --strict-mcp-config --disable-slash-commands \
  --system-prompt 'Reply with only what the request asks for.' --model haiku --effort low
```

- Run it in a fresh empty temp directory and delete it afterwards.
- Still send `initialize` first to validate model and effort, then the prompt,
  read `result`, close stdin.
- Decide success from `result.subtype` and `is_error`, not the exit code.
- `--bare` rejects subscription login (bare accepts only `ANTHROPIC_API_KEY` or
  `apiKeyHelper`). `--safe-mode` keeps the normal login and skips CLAUDE.md,
  AGENTS.md and hooks.
- `--tools ''` leaves MCP tools; `--disallowedTools 'mcp__*'` removes them. With
  these flags `init` reports empty `tools`, `mcp_servers` and `skills`.
- `--no-session-persistence` does not stop global cache or telemetry writes.
- Structured output: `--output-format json --json-schema '<schema>'` returns
  `structured_output` in the result (with `num_turns: 2`, `stop_reason:
  "tool_use"`).
- `--output-format json` with the prompt on stdin, in place of stream-json,
  prints one `result` object. An unknown model gives a `result` with `is_error:
  true` and its reason in `result`, and exits 1 (2.1.331).
- A cold call takes about 2 s; a 100 KB prompt about 5 s; concurrent calls do
  not serialize.

## 13. Built-in tool names (2.1.319)

`Bash{command,description}`, `PowerShell`, `Monitor{command}`,
`Read{file_path,offset}`, `Write`, `Edit{file_path,old_string,new_string}`,
`NotebookEdit{notebook_path}`, `Glob{pattern,path}`, `Grep{pattern,path,glob}`,
`LSP{operation,filePath,line}`, `WebFetch{url,prompt}`, `WebSearch{query}`,
`Agent`/`Task{subagent_type,description,prompt}`, `Skill{skill,args}`,
`ToolSearch{query,max_results}`, `TodoWrite{todos}`, `TaskCreate`, `TaskGet`,
`TaskList`, `TaskUpdate`, `TaskStop`, `CronCreate`, `CronDelete`, `CronList`,
`ScheduleWakeup{delaySeconds,reason,prompt}`, `EnterPlanMode`,
`ExitPlanMode{plan}`, `EnterWorktree`, `ExitWorktree`, `ListMcpResourcesTool`,
`ReadMcpResourceTool`, `ReadMcpResourceDirTool`, `ListAgents{cursor}`,
`SendMessage{to,message,summary,notify_when_idle}`, `PushNotification{message}`,
`AskUserQuestion{questions}`.

Haiku writes its plan to a file in the plans directory and calls `ExitPlanMode`
with empty input. Claude fills in `{plan, planFilePath}` from the file in the
`assistant` `tool_use`, the transcript, and `can_use_tool` (2.1.331; on 2.1.329
the `tool_use` kept `{}`). An approved call's `tool_result` text ends with `##
Approved Plan:` and the plan, and its `tool_use_result` is `{plan, isAgent,
filePath}`. A denied call's result is the deny message.
