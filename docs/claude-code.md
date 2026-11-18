# Driving Claude Code with `claude -p` and stream-json

Collected from the Rust implementation that preceded this repository, called Ox-rs here (`crates/claude-code-acp`, baseline `c0c3f0f`), and Hypercube (`plugins/claude`, Go, baseline `60b62ab`), including their git history, plans, reviews, work logs, research notes and recorded fixtures. Observations were made against Claude Code 2.1.312 through 2.1.326 (October–November 2026); fixtures used `claude-haiku-5-5`. Treat field names and behaviors as version-specific and re-probe when upgrading.

## 1. Launch

### Persistent session

Run one long-lived process per conversation and keep its stdin open across turns:

```
claude -p \
  --input-format stream-json --output-format stream-json --verbose \
  --include-partial-messages \
  --replay-user-messages \
  --permission-mode <mode> --permission-prompt-tool stdio \
  --system-prompt-snapshot off \
  --model <model> --effort <level> \
  [--append-system-prompt <text>] \
  [--allowedTools mcp__<server> ...] \
  [--mcp-config '{"mcpServers":{...}}'] \
  (--session-id <uuid> | --resume <uuid> | --resume <parent> --fork-session --session-id <uuid>)
```

- `--verbose` is required with `--output-format stream-json`.
- `--include-partial-messages` adds `stream_event` lines carrying raw Anthropic SSE events (text and thinking deltas).
- `--replay-user-messages` echoes each prompt back as a `user` line with `isReplay: true` and the prompt's `uuid` once Claude takes it into a turn.
- `--permission-prompt-tool stdio` routes permission prompts to the host as `can_use_tool` control requests on stdout. The alternative is `--permission-prompt-tool mcp__server__tool`.
- `--system-prompt-snapshot off`: by default Claude records the fully composed system prompt on the first request and reuses it, including across `--resume`, until compaction. A host that passes `--append-system-prompt` on every launch needs the snapshot off for later launches to take effect.
- `--mcp-config` accepts inline JSON; a temp file is unnecessary. Shape: `{"mcpServers":{"<name>":{"command":"/abs/path","args":[],"env":{}}}}`. MCP tools are named `mcp__<server>__<tool>`.
- `--allowedTools mcp__<server>` pre-approves every tool of that server so it never prompts. `--allowedTools` auto-approves; it does not restrict the tool catalog (`--tools` does that, and `--tools` does not cover MCP tools).
- `--allowedTools` and `--disallowedTools` are variadic and consume every following argument that is not a flag. Put another `--flag` right after their values.
- Per-process flags (`--mcp-config`, `--settings`, `--plugin-dir`, `--add-dir`, `--fallback-model`, `--append-system-prompt`, model, effort) must be passed again on every resume. The permission mode is restored from the transcript.
- `--session-id` must be a UUID. Hypercube derives fork destination IDs deterministically (first 32 hex chars of sha256 of its own ID, formatted `%s-%s-4%s-8%s-%s`) so a restart before the first turn reuses the same ID.
- Ox-rs makes its own session ID the Claude session ID, and chooses `--resume <id>` only when a transcript for that ID exists in that cwd; otherwise `--session-id <id>`.
- Set the process's working directory to the session's directory.

### Environment

- `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` enables `system/session_state_changed` (`running` / `requires_action` / `idle`). Without it those events are absent.
- `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0` makes Claude report `idle` at the end of a turn while background tasks keep running, then `running` when a turn they trigger begins. With the default, a background subagent keeps the state at `running` until it finishes, and the follow-up turn gets no fresh `running`. Background Bash and Monitor report `idle` either way.
- Remove `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` from the child environment to keep subscription auth; in `-p` mode an inherited API key is always used.
- `CLAUDE_CONFIG_DIR` (default `~/.claude`) isolates settings, history, plugins and most auth. `~/.claude.json` still holds global trust, account and MCP state.
- Claude sets `CLAUDE_CODE_SESSION_ID` in its tools' environment, so the agent can read its own conversation ID.
- Retry and timeout knobs: `API_TIMEOUT_MS` (default 600000), `CLAUDE_CODE_MAX_RETRIES` (default 10, max 15), `CLAUDE_ENABLE_STREAM_WATCHDOG=0`.
- To simulate a dead network: `HTTPS_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1 CLAUDE_CODE_MAX_RETRIES=0 API_TIMEOUT_MS=5000`.
- `claude` is typically installed in `~/.local/bin`. A GUI app or service has to take `PATH` from the login shell (`$SHELL -ilc`) to find it.

### Behavior of `-p`

- `-p` skips the trust dialog and silently drops invalid non-managed settings.
- Settings files reload while running; model and effort changes need control requests.
- `claude -p` with empty stdin exits 1.
- The CLI's `--help` is not exhaustive. Several documented flags (`--permission-prompt-tool`, `--max-turns`, `--system-prompt-file`) are missing from it.

## 2. The stdin protocol

Every message is one JSON object per line, terminated by `\n`. Serialize writes (one writer task or a mutex).

### Initialize first

Send this immediately after spawning, before any prompt:

```json
{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}
```

The response carries the slash-command catalog and the model list. No model turn runs and no conversation is created:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"initialize","response":{
  "commands":[{"name":"plugin:review","description":"Review","argumentHint":"file","aliases":["review"]},
              {"name":"clear","description":"Clear","argumentHint":"","builtin":true}],
  "models":[{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)",
             "supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"]},
            {"value":"haiku","resolvedModel":"claude-haiku-5-5","displayName":"Haiku", "...":"..."}]}}}
```

- `models[].value` is what `--model` and runtime model changes accept; `resolvedModel` is what an alias resolves to. Match a user's choice against either.
- An empty `models` list means the CLI is too old for model discovery.
- A refusal is `{"subtype":"error","request_id":"initialize","error":"..."}`.
- Allow up to 60 s for the response.
- Claude accepts any `--model` string and any model in a runtime change, and fails only on the next prompt. Unknown `--effort` values are ignored with a stderr warning (`Unknown --effort value 'minimal' — ignoring it and using the default effort. Valid values: low, medium, high, xhigh, max.`) and exit 0. Validate model and effort against `initialize` yourself: a model with `supportsEffort` needs one of its `supportedEffortLevels`, and a model without effort accepts none.
- Model discovery alone: run `claude -p --input-format stream-json --output-format stream-json --verbose` with no `--model` and no MCP config in the project directory, send `initialize`, read `models`, close stdin.

### User messages

```json
{"type":"user","uuid":"<your id>","session_id":"","message":{"role":"user","content":[...]},"parent_tool_use_id":null,"priority":"next"}
```

- `content` may be a string or content blocks:
  - `{"type":"text","text":"..."}`
  - `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}` (PNG, JPEG, GIF, WebP)
  - `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"..."}}`
  - Embedded text can be sent as a text block wrapping it in `<context ref="uri">…</context>`; a file link as `[@name](uri)`, which Claude opens with its tools.
- `uuid` is your handle for the prompt. Claude reports it back when it takes the prompt into a turn (section 4).
- Set `"priority":"next"` explicitly; the default depends on Claude's feature flags (2.1.326). With `next`, a prompt written during a turn is taken in at the turn's next opportunity (steering); one not taken before the turn ends runs as its own turn.
- A message cannot be withdrawn once written, short of an interrupt with `cancel_queued`.
- Slash commands and user-invocable skills are plain prompt text: `/review src/main.rs`. Local commands such as `/model` and `/clear` complete without a model call. An unknown command reaches the model as ordinary text. `--disable-slash-commands` disables both commands and skills; streamed `client_composed: true` bypasses slash dispatch.

### Control requests to Claude

Envelope: `{"type":"control_request","request_id":"<unique>","request":{...}}`. Match responses by `request_id`; unrelated `control_response` lines arrive interleaved.

| Request | Effect |
| --- | --- |
| `{"subtype":"initialize"}` | Handshake; commands and models. |
| `{"subtype":"interrupt"}` | Stops the running turn and its tools. Response: `{"still_queued":[...]}`. |
| `{"subtype":"interrupt","cancel_queued":true}` | Also drops prompts Claude has not taken yet. |
| `{"subtype":"set_model","model":"haiku"}` | Changes the model. Does not change effort. |
| `{"subtype":"set_permission_mode","mode":"acceptEdits"}` | Changes the permission mode. |
| `{"subtype":"apply_flag_settings","settings":{"model":"haiku","effortLevel":"max"}}` | In-process flag override (no settings file write). Can affect the next model call within the running turn. `"effortLevel":null` clears effort. |
| `{"subtype":"reload_skills"}` | Rescans skills; response `{"skills":[...]}`. Skills added after `initialize` are neither listed nor invocable until this runs. |
| `{"subtype":"reload_plugins"}` | Response has a refreshed `commands` list in the `initialize` shape. |

Responses are `{"type":"control_response","response":{"subtype":"success","request_id":"..."}}` or `{"subtype":"error","request_id":"...","error":"bad model"}`. Treat a closed process as "no answer" and apply the setting on the next launch instead.

Permission modes: `manual` (alias `default`), `auto`, `acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`. In `auto`, requests the classifier rejects never reach the host.

### Stopping

Close stdin; Claude exits on its own. Kill it if it is still alive after a few seconds (Hypercube waits 3 s). Spawn with kill-on-drop so a crashed host does not leak it.

## 3. The stdout protocol

Parse each non-empty line as JSON. Skip lines that fail to parse, but keep the last one: some failures are printed as plain text on stdout (section 6).

Lines of type `stream_event` wrap an SSE event in `.event`; unwrap before dispatching.

### `system`

- `init`: `session_id`, `model` (resolved ID), `tools`, `mcp_servers`, `slash_commands`, `skills`. Emitted at the start of every turn, including turns Claude starts itself. It is not emitted at process start, so use `initialize` for startup data.
- `session_state_changed`: `state` is `running`, `requires_action` (just before a `can_use_tool` request) or `idle`; carries `"sdk_host_only":true`. `idle` arrives after the turn's `result` is flushed.
- `commands_changed`: `commands` replaces the slash-command catalog (possibly with an empty list).
- `task_started`: `task_id`, `tool_use_id`, `is_backgrounded`. Foreground tasks get this too but never join the live set.
- `task_updated`: progress, safe to ignore.
- `task_notification`: `task_id`, `tool_use_id`, `status` (`completed` / `stopped` / `failed`), `summary`.
- `background_tasks_changed`: `tasks[]` is the complete live set, each item carrying `task_id`. Not emitted at startup; start from an empty set.
- `turn_preempted` (`preempted_by_uuid`) is sent only when `initialize` declares `rapidFollowupPreempt`. Without that declaration a running turn is never preempted by a new prompt.

### `stream_event`

```json
{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The command"}},
 "session_id":"...","parent_tool_use_id":null,"api_message_id":"msg_..."}
```

- Use `content_block_delta` with `delta.type` `text_delta` (`.text`) or `thinking_delta` (`.thinking`).
- `thinking_delta` strings are empty in these builds (with `estimated_tokens`), and the final thinking block is `{"type":"thinking","thinking":""}`. Drop empty thinking and whitespace-only text.
- `index` is the block's position in the whole API message. Key streamed items by `api_message_id:index`.
- Subagents' lines in the recordings had no `stream_event`s; only complete messages.

### `assistant`

`message.{id, model, content[], usage}` plus `parent_tool_use_id` and `session_id`.

- One API message arrives as several `assistant` lines sharing `message.id`, each carrying only its newest block (for example a thinking line, then a text line). Count blocks per `message.id` to recover the index that matches `stream_event.index`; empty thinking blocks count, so text after them is index 1.
- Each split line repeats the same `usage`.
- `message.stop_reason` is always `null` here; it cannot detect a turn's end.
- The complete message repeats text already streamed as deltas. Skip text and thinking blocks once deltas were seen for them.
- `tool_use` blocks: `id`, `name`, `input`. Claude-generated synthetic messages use `"model":"<synthetic>"`.
- `isMeta: true` messages are notes Claude adds for the model; hide them.

### `user`

- Tool results: `content` blocks `{"type":"tool_result","tool_use_id":"...","is_error":false,"content":...}`. `content` is a string or an array of `text`, `image` (base64 or `url` source), `tool_reference` and other blocks.
- Structured tool output is on the line as `tool_use_result` (in transcripts: `toolUseResult`). It belongs to the line, so it is ambiguous when one line carries several `tool_result` blocks.
- A `user` line whose `content` is a plain string, or one with `isReplay: true`, is the prompt echo.
- Interrupts produce synthetic text: `[Request interrupted by user]` or `[Request interrupted by user for tool use]`.

### `control_request` from Claude

Answer `can_use_tool`, and answer a `hook_callback` for a hook declared in `initialize` (such as the Stop hook in section 8) with success. Answer every other subtype (undeclared hook callbacks, MCP messages) with an error so Claude does not wait:

```json
{"type":"control_response","response":{"subtype":"error","request_id":"<id>","error":"Unsupported control request"}}
```

`control_cancel_request` `{"request_id":"..."}` withdraws a pending `can_use_tool`. Close the dialog and discard a late answer.

### `result`

```json
{"type":"result","subtype":"success","is_error":false,"result":"<final text>","terminal_reason":"completed",
 "session_id":"...","total_cost_usd":0.0058,"num_turns":1,"duration_ms":0,
 "usage":{},"modelUsage":{"claude-haiku-5-5":{"contextWindow":1000000}}}
```

- `terminal_reason` values seen: `completed`, `aborted_tools`, `aborted_streaming`, `model_error`. A reason starting with `aborted` is a user interrupt, not an error.
- `subtype` values: `success`, `error_during_execution`, `error_max_turns`.
- `is_error: true` can occur with `subtype: "success"`, e.g. `"result":"API Error: 529 overloaded","terminal_reason":"model_error"`. Check both fields.
- Error text: `result` if non-empty, else `errors[]` joined, else `subtype`. `errors` can hold diagnostics such as `"[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=tool_use"`.
- An empty bookkeeping result (`"result":""`, no `terminal_reason`) can follow an automatic turn.
- `total_cost_usd` is a running total for the process.
- `modelUsage` is cumulative, keyed by resolved model ID, and gains an entry per model used (including subagents' and after model switches).

### `rate_limit_event`

Once per turn, after the assistant message and before the result; nothing before the first turn. Subscription auth only.

```json
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793767800,"rateLimitType":"five_hour",
 "overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,
 "unifiedWindows":{"five_hour":{"utilization":0.01,"resetsAt":1793767800,"status":"allowed"},
                   "seven_day":{"utilization":0.39,"resetsAt":1794193200,"status":"allowed"}}}}
```

`utilization` is a fraction; `resetsAt` is Unix seconds. Windows are 300 and 10080 minutes.

### `command_lifecycle`

`{"type":"command_lifecycle","command_uuid":"<your uuid>","state":"started"}` marks a prompt taken into a turn.

## 4. Turns

### When a prompt is taken

Two signals mark a prompt entering the conversation: `command_lifecycle` `started` with `command_uuid`, and the `isReplay: true` echo with `uuid`. Use whichever arrives first. Acknowledge a prompt at that point, not at the turn's end; output and completion arrive separately.

### When a turn ends

- `result` ends a model turn but does not mean Claude is idle. With a background subagent the sequence is: main `result` while the subagent runs → subagent lines continue → new `system/init` and an automatic main turn → its `result` → an empty bookkeeping `result` → `session_state_changed idle`.
- Use `idle` (requires `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`) as the authoritative end. Keep pending permission requests across `result`s: background subagents can still be waiting for their answers. Withdraw them when Claude cancels the request, becomes idle, or exits.
- `idle` can arrive without a `result` after a cancel. Clear any "cancelling" flag on `idle` so the next turn's error is not misread as a cancel.
- A resumed conversation can start a turn on its own during startup (for a background task notification). Report idle after start only if Claude has not reported a state.
- The final answer is the text of the newest main-thread assistant message confirmed by a successful `result`. A bookkeeping result with no newer message leaves it alone; subagent text is never the final answer.
- Keep per-process state (block counters, open tool maps) across `result`s. Subagent output straddles main-thread results, and clearing at `result` merges items and loses late tool results.

### Interrupt

Send `interrupt` (with `cancel_queued: true` to drop untaken prompts). The sequence with a running Bash tool:

1. `control_response` for the interrupt.
2. The running tool's `tool_result`, `is_error: true`, text starting "The user doesn't want to proceed with this tool use…".
3. `user` text `[Request interrupted by user for tool use]`.
4. `result` with `subtype:"error_during_execution"`, `is_error:true`, `terminal_reason:"aborted_tools"`.
5. `session_state_changed idle`.

On the aborted result, fail the prompts Claude never took as cancelled. A failed result, or a process exit, while you are cancelling counts as a cancel. The stopped turn stays in the transcript.

### Session IDs

Claude names the session at startup but writes the transcript only once a turn runs. Record the conversation as resumable only after a `result` (or once the transcript exists). A cancelled or failed first turn may leave nothing to resume. A fork reports no conversation until its first result.

### Exit and restart

If Claude exits mid-turn, end the turn as an error with `claude exited with <status>: <stderr tail>`. Start a new process with `--resume` on the next prompt, or `--session-id` if no transcript was saved.

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

Order on stdout: the assistant `tool_use`, `session_state_changed requires_action`, the `control_request`, then `running` again after the answer.

Answer asynchronously so output keeps flowing while the user decides; keep an abort handle per `request_id` for `control_cancel_request` and for turn end.

Allow:

```json
{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{
  "behavior":"allow","updatedInput":<the original input, byte-for-byte>,
  "toolUseID":"toolu_...","decisionClassification":"user_temporary"}}}
```

- Echo `input` unchanged. Hold it as raw JSON rather than decoding and re-encoding it. Send `{}` if it is not an object.
- "Allow always": `"decisionClassification":"user_permanent"` plus `"updatedPermissions": <permission_suggestions>`. Offer it only when suggestions exist and `suppress_always_allow_rule` is false. The suggestions' `destination` is typically `localSettings`, so this writes `.claude/settings.local.json`. To keep grants out of the project, store them in the host and answer `allow` per call.

Deny:

```json
{"behavior":"deny","message":"Permission denied by client","toolUseID":"toolu_...","decisionClassification":"user_reject"}
```

A deny becomes a tool error (`"User declined"`, `is_error: true`) and the model continues; text typed by the user can go in `message`. Setting `interrupt: true` on the deny stops the turn instead.

### `AskUserQuestion`

It arrives twice: as an assistant `tool_use` and as `can_use_tool` with `tool_name:"AskUserQuestion"` and `"requires_user_interaction":true`.

Input: `questions[]` (1–4), each `{question, header (≤12 chars), options: [{label, description}] (2–4), multiSelect}`.

Answer by allowing with the original input plus `answers`, keyed by question text, all questions in one response:

```json
{"behavior":"allow","updatedInput":{"questions":[...],"answers":{"Which color?":"Red","Which sections?":"Introduction, Conclusion, Appendix"}}}
```

- Single choice: the label, or free text in its place.
- Multi-select: chosen labels in option order, then free text, joined with `", "` (matches Anthropic's `ask-user-question-previews` demo).
- Omit unanswered questions.
- Resulting tool result: `Your questions have been answered: "Which color?"="Red". You can now continue with these answers in mind.`
- Deny when the user declines or the input cannot be shown.
- `--disallowedTools AskUserQuestion` makes Claude ask in plain text instead, for hosts without a form UI.

## 6. Errors and process I/O

### Startup failures

- A bad `--resume` prints a `result` before, or instead of, the `initialize` response, then exits 1:
  ```json
  {"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"1111...","total_cost_usd":0,"modelUsage":{},
   "errors":["No conversation found with session ID: 1111..."]}
  ```
  Treat any `result` before `initialize` answers as the startup failure reason, keep the first one, and report it on the waiting prompt. Do not fall back to a fresh conversation.
- Writing `initialize` can fail with EPIPE because Claude already exited; the real reason is the unread `result`. Ignore write errors during startup and let the reader report the exit reason.
- An error `result` while a prompt waits and no turn is running belongs to no turn. Hold it as the reason the waiting prompt fails if the process then exits; clear it when a prompt is taken.
- Failures can appear as plain text on stdout with empty stderr: `Not logged in · Please run /login`, `API Error: Connection refused`, unknown-model messages (exit 1). Keep the last undecodable stdout line and include it in the exit reason when no `result` arrives.
- Exit status alone is insufficient: SIGINT can exit 0 with empty stdout; SIGTERM exits 143.

### Pipes

- Read stdout until the process exits, not until EOF. Claude's children (background Bash, MCP servers) can hold stdout and stderr open after Claude exits.
- Do not lose Claude's last lines (often the final `result`) at exit. After `wait` returns, drain the reader's buffer and then read the descriptor non-blockingly until `EAGAIN`; the async runtime may not yet have seen the pipe become readable. In Go, use an `os.Pipe` you own rather than `cmd.StdoutPipe` (which `Wait` closes), set `cmd.WaitDelay`, and set a short read deadline after exit.
- Read stderr concurrently so a full pipe never blocks Claude, and keep the last ~2 KiB for exit messages.
- Lines can be large (base64 images, big tool results). Size line buffers generously (Hypercube: 1 MiB reader, up to 64 MiB scanner) and handle a final line without a newline.

## 7. Subagents

- Subagent lines carry `parent_tool_use_id` (the `Agent`/`Task` tool_use ID), and also `agent_id`, `subagent_type`, `task_description`. Group them under the parent tool call.
- They interleave with main-thread streaming and continue after the main `result`.
- The `Agent` tool can return immediately ("Async agent launched successfully… agentId… output_file…"); its findings arrive later in an automatic main turn.
- Exclude subagent messages from context-size calculations and from the session's current model.
- A subagent tool requiring permission arrives as an ordinary `can_use_tool`.
- `--forward-subagent-text` forwards subagent text, which is otherwise not streamed.

## 8. Background work and schedules

- Background Bash, Monitor and background subagents appear in `background_tasks_changed`. A task's tool call stays running after its `tool_result` until its `task_notification`; a turn's `result` should not mark it failed.
- On ending, the order is `task_updated`, `task_notification`, `background_tasks_changed` without the task, then a turn Claude starts itself with no user prompt.
- `background_tasks_changed` can arrive before the `task_started` that maps `task_id` to `tool_use_id`. Resolve the mapping lazily.
- The Stop hook reports pending schedules. Declare it in `initialize` with `"hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}`. At the end of each completed turn, before `result` and `idle`, Claude sends `{"type":"control_request","request_id":"<id>","request":{"subtype":"hook_callback","callback_id":"stop","input":{"hook_event_name":"Stop","background_tasks":[],"session_crons":[{"id":"4dc9d149","schedule":"*/30 * * * *","recurring":true,"prompt":"say hi"}]}}}` and waits for `{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{}}}`.
  - `session_crons` is the complete list of `CronCreate` jobs, `ScheduleWakeup` wakeups and `/loop` jobs, including those a foreground subagent created; `[]` means none. A wakeup appears as a one-shot entry whose cron string encodes its fire minute. Each `prompt` is capped at 1000 characters.
  - `SubagentStop` (same declaration shape) carries the same field when a subagent finishes.
  - A turn that ends by interrupt or error sends no Stop callback (an API error fires `StopFailure`, which has no `session_crons`), and a resumed process reports its restored jobs only when its first turn ends.
  - A fired one-shot job or wakeup is gone from the list at the end of its fire turn.
- The tool calls and their structured results describe each schedule:
  - `CronCreate` input `{cron, prompt, recurring?, durable?}`, result `{id, humanSchedule, recurring, durable?, nextFireAt}` (epoch ms).
  - `CronDelete` input `{id}`.
  - `ScheduleWakeup` input `{delaySeconds?, reason?, prompt?, stop?}`, result `{scheduledFor, clampedDelaySeconds, wasClamped, stopped?, cancelledWakeups?}`.
  - Times may be floats.
- Rules: recurring jobs expire 7 days after creation, deleted after their final fire; a one-shot job is done once a turn starting at or after its fire time ends; in `-p` mode a dynamic `/loop` ends when a wakeup turn schedules nothing (2.1.327); `ScheduleWakeup{stop:true}` clears wakeups only once its result is successful.
- A turn Claude starts for a schedule carries `wakeup_source` on its `command_lifecycle` `started` line: `schedule_wakeup` for a cron job, or `loop_wakeup` with `scheduled_for` (epoch ms) for a dynamic `/loop` wakeup.
- `CronCreate` with `durable:true` returns `durable:false` in 2.1.327; durable persistence is unavailable.
- `claude --resume` restores unexpired main-thread recurring jobs and future one-shot jobs, not wakeups or jobs created by subagents. Durable jobs of other sessions in `.claude/scheduled_tasks.json` are not visible.

## 9. Usage and cost

- Context in use = `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` of the latest main-thread assistant message.
- Context window = `result.modelUsage[<current resolved model>].contextWindow`; take the current model from `system/init.model` and main-thread `message.model`.
- Cost = `result.total_cost_usd` (process running total).
- `--max-budget-usd` (print only) is a client estimate that counts subagents and can overshoot.

## 10. Transcripts on disk

- Path: `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/<uuid>.jsonl`. To find a session, scan every project directory for `<id>.jsonl` whose first `cwd` entry equals the directory; reading the first 1 MiB is enough.
- Subagents: `<uuid>/subagents/agent-<id>.jsonl` beside `agent-<id>.meta.json` holding `{"toolUseId": ...}`. In the main file, subagent entries have `isSidechain: true`.
- Entries carry ISO `timestamp` (the first few may lack one), `cwd`, `uuid`, and `entrypoint` on an early entry: `cli` (terminal), `claude-desktop`, or `sdk-cli` (`claude -p` and the Agent SDK). `sdk-cli` transcripts also carry `promptSource:"sdk"`.
- Replay keeps `user` and `assistant` entries and skips `isMeta`. The entry `uuid` of a user message is the prompt's `uuid`. Tool calls without a result belong to interrupted turns.
- Titles: `{"type":"custom-title","customTitle":...}` from `/rename` and `{"type":"ai-title","aiTitle":...}`, repeated through the file; the last of each wins, custom over generated.
- Slash commands appear in transcripts and replayed prompts as `<command-message>…</command-message><command-name>/…</command-name><command-args>…</command-args>`, without the rendered skill body.
- Transcripts are deleted after `cleanupPeriodDays` (default 30); after that the conversation cannot be resumed.

## 11. Skills and commands

- `--add-dir DIR` loads skills from `DIR/.claude/skills/`, not from `DIR/<name>/SKILL.md`. To add a raw skills directory, make a `--plugin-dir` plugin whose `skills` points (or symlinks) to it; its skills appear as `plugin:name` with a bare alias.
- `user-invocable: false` disables direct `/name` dispatch; `disable-model-invocation: true` disables only model invocation.
- `system/init.slash_commands` and `.skills` carry names only; descriptions and argument hints come from `initialize.commands`.
- Validate catalog entries: non-empty name without whitespace, description present, deduplicate by name.

## 12. One-shot utility calls

For titles and similar stateless prompts:

```
claude -p --input-format stream-json --output-format stream-json --verbose \
  --safe-mode --no-session-persistence --tools '' --disallowedTools 'mcp__*' \
  --strict-mcp-config --disable-slash-commands \
  --system-prompt 'Reply with only what the request asks for.' --model haiku --effort low
```

- Run it in a fresh empty temp directory and delete it afterwards.
- Still send `initialize` first to validate model and effort, then the prompt, read `result`, close stdin.
- Decide success from `result.subtype` and `is_error`, not the exit code.
- `--bare` rejects subscription login (bare accepts only `ANTHROPIC_API_KEY` or `apiKeyHelper`). `--safe-mode` keeps the normal login and skips CLAUDE.md, AGENTS.md and hooks.
- `--tools ''` leaves MCP tools; `--disallowedTools 'mcp__*'` removes them. With these flags `init` reports empty `tools`, `mcp_servers` and `skills`.
- `--no-session-persistence` does not stop global cache or telemetry writes.
- Structured output: `--output-format json --json-schema '<schema>'` returns `structured_output` in the result (with `num_turns: 2`, `stop_reason: "tool_use"`).
- A cold call takes about 2 s; a 100 KB prompt about 5 s; concurrent calls do not serialize.

## 13. Built-in tool names (2.1.319)

`Bash{command,description}`, `PowerShell`, `Monitor{command}`, `Read{file_path,offset}`, `Write`, `Edit{file_path,old_string,new_string}`, `NotebookEdit{notebook_path}`, `Glob{pattern,path}`, `Grep{pattern,path,glob}`, `LSP{operation,filePath,line}`, `WebFetch{url,prompt}`, `WebSearch{query}`, `Agent`/`Task{subagent_type,description,prompt}`, `Skill{skill,args}`, `ToolSearch{query,max_results}`, `TodoWrite{todos}`, `TaskCreate`, `TaskGet`, `TaskList`, `TaskUpdate`, `TaskStop`, `CronCreate`, `CronDelete`, `CronList`, `ScheduleWakeup{delaySeconds,reason,prompt}`, `EnterPlanMode`, `ExitPlanMode{plan}`, `EnterWorktree`, `ExitWorktree`, `ListMcpResourcesTool`, `ReadMcpResourceTool`, `ReadMcpResourceDirTool`, `SendMessage{to,message}`, `PushNotification{message}`, `AskUserQuestion{questions}`.

## 14. Stream-json compared with the interactive TUI and hooks

Hypercube first drove interactive `claude` in tmux with `--settings` hooks (`SessionStart`, `UserPromptSubmit`, `PostToolUse`, `Stop`, `StopFailure`, `PermissionRequest`) and a status-line script. Facts from that approach that matter when choosing:

- An idle foreground session can only be woken by an `asyncRewake: true` hook exiting 2, whose stderr becomes a system reminder.
- A user interrupt fires no `Stop` hook, so hook-based busy/idle tracking stays "busy".
- Hooks also run inside native subagents (input has `agent_id`); `agent_type` alone does not identify a subagent, because `--agent` main sessions have one.
- `UserPromptSubmit.prompt` wraps pastes in `<pasted_content>` tags.
- The status-line JSON (`context_window`, `cost`, `rate_limits`, `model`, `effort`) is debounced and can have null fields.

The persistent stream-json process delivers message injection at any time, explicit `running`/`idle` states, interrupts and structured permission requests, without terminal scraping.

## 15. Testing

Put a shell script named `claude` first on `PATH`. It reads stdin line by line (exiting when stdin closes), records argv and control requests to files, answers `initialize` with a canned `control_response`, and plays a recorded JSONL fixture per user message: `command_lifecycle started`, the `isReplay` echo, `session_state_changed`, a `can_use_tool` request, the interrupt sequence, or a failed resume (print the error `result`, write to stderr, exit 1). Set `CLAUDE_CONFIG_DIR` to an empty directory and set a dummy `ANTHROPIC_API_KEY` to check that it is stripped. Record fixtures from a real run and trim them to the fields the host reads.

Live smoke tests against a real model (Haiku) are flaky when the test depends on the model following instructions; keep their assertions to protocol behavior.

## Sources

- Ox-rs: `crates/claude-code-acp/src/{runner,updates,background,questions,content,commands,history,sessions}.rs`, `crates/acp-bridge/src/pipes.rs`, `tests/protocol.rs`, `spec/claude.md`, `docs/research/harnesses/claude-code.md`, `docs/research/utility-model.md`; commits `03bfdcc` (persistent process), `fc13def` (background tasks), `d00463b` (`priority: next`), `1647c4f` (turn ends, preemption), `d421e4b` (output lost at exit), `c977427` (initialize at spawn), `a542b25` (AskUserQuestion).
- Hypercube: `plugins/claude/{main,events,requests,configure,discovery,generate}.go`, `plugins/claude/testdata/*.jsonl`, `plugins/internal/plugin/process.go`, `.agents/research/harnesses/claude-code.md`, `.agents/research/utility-model.md`, `docs/harness-support.md`; commits `d535ef4` (stream-json switch), `444f60a` (model discovery), `11a3bd3` (`apply_flag_settings`), `4dd12a0` (state across results), `4d85b1d` (EPIPE on resume), `c06825e` (self-started turns), `ff4f5c0` (rate limits), `08f0d1a` (TUI and hooks era).
