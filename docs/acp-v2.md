# ACP v2 support

This chart lists the draft [Agent Client Protocol](https://agentclientprotocol.com/)
v2 and what Ox supports of it. It is built from `agent-client-protocol-schema`
1.17.0, the version `agent-client-protocol` 5.2.0 pins, with the
`unstable_protocol_v2` feature. Rows marked *unstable* sit behind an
`unstable_*` feature of the schema and may change in any release. Bumping
`agent-client-protocol` updates these rows in the same change.

The columns are the feature, `oxd` as the client, `claude-code-acp` as the
agent, and caveats. Each cell is *supported*, *partial*, or *unsupported*; a
partial cell says in the caveats what works and what does not.
`oxd-fake-agent` is a test fixture and has no column.

`oxd` records every session update it receives, in the order received, and
relays it unchanged to its clients. "Supported" in the `oxd` column means
`oxd` records and relays the update, or acts on it as the protocol requires of
a client; a caveat says where `oxd` only records it.

## Methods the client sends

| Feature | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `initialize` | supported | supported | claude-code-acp: The agent answers with the protocol version the client sent, and notes whether the client advertises `elicitation.form`. oxd: Sends protocol version 2 and the client capability `elicitation.form`. A session whose agent selects a lower version, or advertises no `session` capability, exits with an error. |
| `auth/login` | unsupported | unsupported | The agent advertises no auth methods. |
| `auth/logout` | unsupported | unsupported | |
| `auth/status` (unstable) | unsupported | unsupported | |
| `providers/list` (unstable) | unsupported | unsupported | |
| `providers/set` (unstable) | unsupported | unsupported | |
| `providers/disable` (unstable) | unsupported | unsupported | |
| `session/new` | supported | supported | claude-code-acp: Answers with the session's config options, reports the session idle, then starts its Claude process, whose native command catalog follows as `available_commands_update`. A Claude process that fails to start fails the next prompt with its error. MCP servers are stdio only; see `session.mcp`. oxd: Sends the session's directory and no MCP servers. |
| `session/list` | unsupported | supported | claude-code-acp: Lists the bridge's sessions and Claude's native transcripts, 50 per page. oxd: `oxd` keeps its own sessions. |
| `session/resume` | supported | supported | claude-code-acp: `replayFrom: start` replays the conversation from Claude's transcript, then the session's state is sent, then the response, with the native command catalog so far. Opens a Claude process if needed, without waiting for it; its catalog follows as `available_commands_update`, and a process that fails to start fails the next prompt with its error. Without `replayFrom` nothing replays. Any other cursor is rejected with `-32602`. oxd: Sends `replayFrom: start` for a saved session. The replay is recorded as the session's history unless the session already has one. |
| `session/close` | unsupported | supported | claude-code-acp: Interrupts and stops the session's `claude`, answers once it has exited, and leaves the session resumable. Closing a session that is not open succeeds. oxd: Deleting a session kills the agent's process group. |
| `session/prompt` | supported | supported | claude-code-acp: Answers with the `messageId` of the user message the prompt becomes, once `claude` takes the prompt into a turn. A prompt sent during a turn waits for it. A prompt dropped by `session/cancel` before that is answered with `-32800`. oxd: Checks the prompt against the agent's prompt capabilities first. The answer matters only when it is an error: the prompt was not inserted, and the turn ends with that error unless the agent already reported work for it. |
| `session/set_config_option` | supported | supported | claude-code-acp: Options `model`, `effort`, and `mode`, all selects. oxd: Selects and booleans. |
| `session/delete` | unsupported | unsupported | |
| `session/fork` (unstable) | unsupported | unsupported | |
| `session/archive` (unstable) | unsupported | unsupported | |
| `session/unarchive` (unstable) | unsupported | unsupported | |
| `session/status` (unstable) | unsupported | unsupported | |
| `session/options` (unstable) | unsupported | unsupported | |
| `session/set_title` (unstable) | unsupported | unsupported | The bridge titles a session from its first prompt. |
| `tool_call/cancel` | unsupported | unsupported | |
| `terminal/cancel` | unsupported | unsupported | |
| `mcp/message` (unstable) | unsupported | unsupported | |
| Extension requests (`_`-prefixed) | unsupported | unsupported | |

## Notifications the client sends

| Feature | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `session/cancel` | supported | supported | claude-code-acp: Interrupts the turn and drops prompts `claude` has not taken. Open permission requests and elicitations are withdrawn. oxd: Withdraws every open permission request as cancelled. Open elicitations stay open until the agent withdraws them with `$/cancel_request`. |
| `$/cancel_request` | unsupported | unsupported | |
| `mcp/message` (unstable) | unsupported | unsupported | |
| Extension notifications (`_`-prefixed) | unsupported | unsupported | |

## Methods the agent sends

| Feature | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `session/request_permission` | supported | supported | claude-code-acp: Sent for each tool `claude` asks to use, except AskUserQuestion; see `elicitation/create`. The subject is the tool call. Options are allow once, allow always when `claude` supplies a rule, and reject once. oxd: Recorded with its title, description, subject, and options, and answered when a client picks an option or the turn is cancelled. The session is awaiting permission meanwhile. |
| `elicitation/create` | supported | supported | claude-code-acp: Asks Claude's AskUserQuestion questions as a form, scoped to the session and the tool call. Question *i* becomes `q`*i*, a `oneOf` choice of its options or an `anyOf` multi-select when `multiSelect` is set, titled with its header and described by its text, and `q`*i*`-other`, a text field titled `Other`. No field is required. The message is `Claude has a question` or `Claude has questions`. Accepting allows the tool with the answers: a single-select question's `Other` text, or else its choice, and a multi-select question's choices in option order followed by its `Other` text, joined with `, `. Declining or cancelling denies the tool. A client without `elicitation.form` gets no elicitations: `claude` starts with AskUserQuestion disallowed and asks in plain text. oxd: Form mode, scoped to the session. Recorded with its message, tool call, and schema, and answered when a client accepts, declines, or cancels it. Accepted content is checked against the schema, except `pattern` and `format`. The session is awaiting input meanwhile, or awaiting permission while a permission request is also open. The elicitation ends as `cancel` when the agent withdraws it, exits, or the session is parked or closed. URL mode, request scope, a required property of unknown type or with no schema, and a property with both `enum` and `oneOf` are refused with `-32602`. |
| `mcp/message` (unstable) | unsupported | unsupported | |
| Extension requests (`_`-prefixed) | unsupported | unsupported | |

## Notifications the agent sends

| Feature | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `session/update` | supported | supported | claude-code-acp: See the session updates below. oxd: See the session updates below. |
| `elicitation/complete` | unsupported | unsupported | |
| `auth/status_update` (unstable) | unsupported | unsupported | |
| `$/cancel_request` | partial | supported | claude-code-acp: Withdraws an open permission request or elicitation when Claude withdraws it, the turn ends, or `claude` exits. oxd: Withdraws an open elicitation, which ends as `cancel`. A withdrawn permission request stays pending until a client answers it or the turn is cancelled. |
| Extension notifications (`_`-prefixed) | unsupported | unsupported | |

## Session updates

| `sessionUpdate` | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `user_message` | supported | supported | claude-code-acp: Sent when a prompt enters the conversation, with the prompt's `messageId`, and on replay, one per transcript entry. oxd: Recorded and relayed. |
| `user_message_chunk` | supported | unsupported | Recorded and relayed. |
| `agent_message` | supported | supported | claude-code-acp: Sent for a message that streamed no text, and on replay. oxd: Recorded and relayed. |
| `agent_message_chunk` | supported | supported | claude-code-acp: Sent while `claude` streams text. The message is not repeated as a whole. oxd: Recorded and relayed. |
| `agent_thought` | supported | supported | claude-code-acp: Same as `agent_message`. oxd: Recorded and relayed. |
| `agent_thought_chunk` | supported | supported | claude-code-acp: Same as `agent_message_chunk`. oxd: Recorded and relayed. |
| `state_update` | supported | supported | claude-code-acp: See the state updates below. oxd: Drives the turn; see the state updates below. The update is also recorded. |
| `tool_call_update` | supported | supported | claude-code-acp: The first update for a tool call ID carries its name, title, kind, `pending` status, and input. A result sets the whole `content` list: the result's text and images, preceded by the activity lines of a subagent the tool call started. Tool calls that end without a result are failed. oxd: Recorded and relayed. |
| `tool_call_content_chunk` | supported | unsupported | claude-code-acp: Tool call content is sent whole. oxd: Recorded and relayed. |
| `terminal_update` | supported | unsupported | Recorded and relayed. |
| `terminal_output_chunk` | supported | unsupported | Recorded and relayed. |
| `plan_update` | supported | unsupported | Recorded and relayed. |
| `plan_removed` (unstable) | unsupported | unsupported | |
| `available_commands_update` | supported | supported | claude-code-acp: Native command names, descriptions, and argument hints are sent once Claude initializes, included in resume responses, and replaced on Claude's `commands_changed` events. oxd: Setup responses and notifications replace the current catalog, including an empty list, which is included in session listings and watches. |
| `config_option_update` | supported | supported | claude-code-acp: Sent after a config change is accepted. oxd: Replaces the session's config options. |
| `session_info_update` | supported | supported | claude-code-acp: `title` from the first prompt, `updatedAt` after a turn finishes. oxd: A `title` sets or clears the session's title and is saved. `updatedAt` is only recorded. |
| `usage_update` | supported | unsupported | Recorded and relayed. |
| `notice` | supported | unsupported | Recorded and relayed. |
| `compaction_update` | supported | unsupported | Recorded and relayed. |
| `compaction_summary_chunk` | supported | unsupported | Recorded and relayed. |
| `subagent_update` (unstable) | unsupported | unsupported | A subagent shows as activity lines of its tool call. |
| `session_message` (unstable) | unsupported | unsupported | |
| `session_message_chunk` (unstable) | unsupported | unsupported | |
| Extension updates (`_`-prefixed) | supported | unsupported | Recorded and relayed. |

The app builds its conversation from the recorded updates. A prompt, an
agent message, and an agent thought are items keyed by `messageId`: chunks
append to the item, and `user_message`, `agent_message`, and `agent_thought`
replace its content with their text. A `user_message` or `user_message_chunk`
during a turn is skipped, because the turn's prompt is its item. The
`tool_call_update` creates a tool call for a new ID and patches an existing one,
and `tool_call_content_chunk` appends to its content. A tool call shows its
text and images; diffs and terminals are not shown. A permission request whose
subject is a tool call applies the subject as a `tool_call_update`. The app
shows slash-command completion in the composer from the current catalog: typing `/` filters names by prefix; arrows select, Enter or Tab completes the draft, and a later submission runs it through `session/prompt`. Escape dismisses completion. Descriptions and text argument hints are shown. The app shows nothing for the other updates, which `oxd` still records.

## State updates

| `state` | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `running` | supported | supported | claude-code-acp: Sent when `claude` takes a prompt or starts a turn of its own, including the turn a finished background task starts, and when a permission request is answered or withdrawn. oxd: Starts a turn when none is running, recorded as `turn_started`. |
| `idle` | supported | supported | claude-code-acp: Sent when a turn ends, with its `stopReason`. A `claude` that exits during a turn ends it with `error`, or `cancelled` when the session is closing. oxd: Ends the running turn, recorded as `turn_ended` with the stop reason. After a prompt, an `idle` before the agent reports work for it is a repeated snapshot and ends nothing. In a resume snapshot it sets the session idle. |
| `requires_action` | supported | supported | claude-code-acp: Sent while a permission request is open. oxd: Counts as work, like `running`. |
| `unknown` (unstable) | unsupported | unsupported | |
| `background` work lists | supported | supported | claude-code-acp: Every state update carries the list, `[]` when nothing runs, including the `idle` it sends after `session/new` and in a resume snapshot. It holds `claude`'s live background tasks and the scheduled prompts it infers from `CronCreate`, `CronDelete` and `ScheduleWakeup`; each is a `tool_call` item when the client has the tool call, otherwise a custom `_claude_task` or `_claude_schedule` item. A listed tool call stays `in_progress`, or `pending` for a schedule, until its work ends. Schedules last until their fire time has passed and a turn that started after it has ended, until `CronDelete`, or after seven days for a recurring job; a call the bridge cannot read lasts until `claude` exits. Durable jobs saved by another session in the same folder are not listed. oxd: Kept per session; a session whose agent last reported `[]`, with no turn and no pending request, can be parked. An agent that reports no list is never parked. |
| Other states | supported | unsupported | Count as work, never as idle. |

## Stop reasons

| `stopReason` | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `end_turn` | supported | supported | Recorded in `turn_ended`. |
| `max_tokens` | supported | unsupported | Recorded in `turn_ended`. |
| `max_turn_requests` | supported | supported | claude-code-acp: From a `claude` result of `error_max_turns`. oxd: Recorded in `turn_ended`. |
| `refusal` | supported | unsupported | Recorded in `turn_ended`. |
| `cancelled` | supported | supported | Recorded in `turn_ended`. |
| `error` | supported | supported | claude-code-acp: The error's message is the failure `claude` reported, or why `claude` exited, with its stderr. oxd: Recorded in `turn_ended` with the failure's details. The app shows the message as a turn error. |
| Other stop reasons | supported | unsupported | Recorded in `turn_ended`. |

## Content blocks in prompts

| Block | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `text` | supported | supported | |
| `resource_link` | supported | supported | Sent to `claude` as a Markdown link, `[@name](uri)`. |
| `image` | supported | supported | claude-code-acp: PNG, JPEG, GIF, and WebP. oxd: Sent when the agent advertises `session.prompt.image`. |
| `resource` | supported | supported | claude-code-acp: Embedded text goes to `claude` in a `<context ref="uri">` block, and an embedded PDF or image as a document or image. Other blobs are rejected. oxd: Sent when the agent advertises `session.prompt.embeddedContext`. |
| `audio` | supported | unsupported | claude-code-acp: Rejected with `-32602`. oxd: Sent when the agent advertises `session.prompt.audio`. |
| Other blocks | unsupported | unsupported | Rejected with `-32602`. |

## Agent capabilities

| Capability | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `session` | supported | supported | claude-code-acp: Advertised, so the baseline session methods apply. oxd: Required. Without it the session exits with an error that the agent does not support sessions. |
| `session.prompt.image` | supported | supported | Gates image prompts. |
| `session.prompt.embeddedContext` | supported | supported | Gates resource prompts. |
| `session.prompt.audio` | supported | unsupported | Gates audio prompts. |
| `session.mcp.stdio` | unsupported | supported | The command is an absolute path. |
| `session.mcp.http` | unsupported | unsupported | A request with an HTTP server is rejected with invalid params. |
| `session.mcp.acp` (unstable) | unsupported | unsupported | |
| `session.delete` | unsupported | unsupported | |
| `session.additionalDirectories` | unsupported | unsupported | |
| `session.systemPrompt` | unsupported | unsupported | |
| `session.fork` (unstable) | unsupported | unsupported | |
| `session.archive` (unstable) | unsupported | unsupported | |
| `session.status` (unstable) | unsupported | unsupported | |
| `session.options` (unstable) | unsupported | unsupported | |
| `session.title` (unstable) | unsupported | unsupported | |
| `auth` | unsupported | unsupported | |
| `providers` (unstable) | unsupported | unsupported | |

## Client capabilities

| Capability | oxd | claude-code-acp | Caveats |
| --- | --- | --- | --- |
| `auth.terminal` | unsupported | unsupported | |
| `elicitation.form` | supported | supported | claude-code-acp: Decides whether Claude can call AskUserQuestion; see `elicitation/create`. oxd: Advertised in `initialize`; see `elicitation/create`. |
| `elicitation.url` | unsupported | unsupported | |
