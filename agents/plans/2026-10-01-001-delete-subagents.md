# Delete subagents

## Goal

Remove subagents from Ox. The model gets only the workspace and shell tools.
Each session has one agent, one transcript, and no child sessions. Code that
exists only to support subagents goes with them: the agent role, child sessions,
subagent messages, per-agent shell process scoping, the client's queue of
concurrent permission requests, and the TUI's rendering of nameless tool calls.

## Related code

- `crates/ox-server/src/subagents.rs` — runs subagents; deleted.
- `crates/ox-server/src/tools/subagent.rs` — `start_subagent`, `send_message`,
  `stop_subagent`, and `wait`; deleted.
- `crates/ox-server/src/prompts/subagent_prompt.md` and
  `crates/ox-server/src/system_prompt.rs` (`for_subagent`) — the subagent system
  prompt.
- `crates/ox-server/src/tools.rs` — `Role`, the coordination tool constants,
  schemas, titles, and dispatch, and `ToolContext.subagents`.
- `crates/ox-server/src/acp/prompt.rs` — `AcpIdentity`,
  `Presentation::subagent`, `role`, `connection`, the `Subagents` launch in
  `AgentTurn::open`, `deliver_subagent_messages`, `stop_subagents`, and
  `children_cost` in `send_usage`.
- `crates/ox-server/src/acp/convert.rs` — `usage_update` takes `children_cost`;
  `shell_permission_request` scopes by subagent ID; `subagent_message_updates`
  and its replay branch.
- `crates/ox-server/src/acp.rs` — `load_session` filters out child sessions and
  adds `children_cost`; the `ActiveSession.shell_processes` comment.
- `crates/ox-server/src/sessions.rs` — `parent_session_id` column, index, and
  summary field; `create_child`; `children_cost`; `append_subagent_messages`;
  `TranscriptEntry::SubagentMessages`, `SubagentMessage`, and
  `SubagentMessageContent`; the `subagent_messages` entry kind.
- `crates/ox-server/src/model.rs` — `ModelRequestParameters.role` and
  `subagent_message_text`.
- `crates/ox-server/src/openrouter.rs` and `crates/ox-server/src/openai.rs` —
  convert subagent messages into requests and pass `parameters.role` to
  `tools::schemas`; `openai.rs` names subagents in its namespace description.
  Both hold the `routed` test fixtures, whose routing only subagent tests use.
- `crates/ox-server/src/shell_processes.rs` and
  `crates/ox-server/src/tools/shell.rs` — every process records the session ID
  of the agent that started it, and `start`, `list`, `get`, `kill`, and `remove`
  filter by it.
- `crates/ox/src/acp.rs` and `crates/ox/src/tui.rs` —
  `Session.permission_requests` is a queue, filled only by concurrent subagent
  requests.
- `crates/ox/src/tui/transcript.rs` — `item_lines` and `content_rows` render
  nameless calls (subagent messages) in full as Markdown.
- `crates/ox/tests/tui.rs` — `render_replies` starts a subagent.
- `scripts/bench.py` — the `subagents` metric.
- `scripts/export_session.py` — exports child sessions.

## Decisions

- Old databases are not migrated. Users recreate `ox.db`, as `AGENTS.md`
  describes. Child sessions and `subagent_messages` entries in an old database
  fail to decode, which is acceptable.
- A `ShellProcesses` registry already belongs to one active session, so its
  processes need no per-agent session ID. `start`, `list`, and `get` drop the
  `session_id` parameter. `kill(session_id)` and `remove(session_id)` exist only
  for ending subagents and are deleted. `MAX_SHELL_PROCESSES` becomes a
  per-session limit. `ToolContext.session_id` exists only for this scoping and
  is removed.
- The server runs tool calls in order, so the client receives at most one
  permission request at a time. `Session.permission_requests` becomes
  `permission_request: Option<(RequestPermissionRequest, Responder<…>)>`. A
  second request while one is pending is a broken invariant: panic with a clear
  message.
- Every tool call the server sends now has a name, so the TUI's nameless-call
  branches in `item_lines` and `content_rows` are deleted rather than kept for a
  case that no longer occurs.
- `tools::schemas()` takes no argument and returns the seven workspace and shell
  tools.
- `convert::shell_permission_request` takes the session ID directly; with
  `AcpIdentity` gone, `Presentation` holds `session_id` and `target`.
  `Target::Acp` updates are always sent, so `sends_updates` and `send` lose
  their subagent checks.
- The `routed` fixtures in `openrouter.rs`, `openai.rs`, and the `prompt.rs`
  harnesses are folded into their single-script constructors (`start` / `new`),
  since every caller with more than one route is a deleted subagent test.

## Test plan

- `tools.rs`: the schema test asserts the seven tool names and no others. Delete
  `coordination_calls_from_a_subagent_fail_as_results`; a call to
  `start_subagent` now fails as an unknown tool, which the existing unknown-tool
  test covers.
- `shell_processes.rs`: replace the per-agent retention case ("another agent's
  finished process was kept") with the per-session limit: once the session
  retains the limit, starting another drops the oldest finished process.
- `sessions.rs`: delete the child-session, `children_cost`, and subagent message
  tests. Listing returns every session in the workspace; deleting a session
  removes its transcript.
- `convert.rs`: `usage_update` reports the transcript's own cost. The permission
  request test covers only the main-session form, with no `_meta`.
- `acp.rs` (server): delete
  `child_sessions_stay_behind_their_main_session_and_count_toward_its_cost`,
  `subagent_permission_requests_use_the_main_session_and_scoped_tool_call_ids`,
  and `Calls::Subagent`.
- `prompt.rs`: delete the four subagent tests
  (`openai_subagent_turns_share_the_provider_and_save_unpriced_usage`,
  `subagents_run_concurrently_and_their_final_answers_reach_the_main_agent`,
  `published_subagent_messages_supersede_a_finished_answer`,
  `dropping_the_prompt_cancels_its_subagents_which_still_save_their_own_batches`)
  and their helpers (`started_subagent`, `final_answer`, and the start-call
  builder).
- `ox/src/acp.rs`: delete
  `simultaneous_permissions_keep_their_supplied_option_ids`. The existing
  single-request tests cover answering and cancelling.
- `transcript.rs`: delete the nameless-answer cases in the Markdown and
  item-rendering tests and the `answer` helper; named calls render as before.
- `tests/tui.rs`: `render_replies` makes no `start_subagent` call and needs no
  per-request routing; the expected screen drops the "Start subagent", "Wait up
  to 600 seconds", and "Final answer from subagent" lines.

## Implementation plan

1. Delete `crates/ox-server/src/subagents.rs`,
   `crates/ox-server/src/tools/subagent.rs`, and
   `crates/ox-server/src/prompts/subagent_prompt.md`. Remove `mod subagents`
   from `lib.rs` and `for_subagent` / `SUBAGENT_PROMPT` from `system_prompt.rs`.
2. `tools.rs`: delete `Role`, the four coordination constants, their schemas,
   titles, and dispatch branch, and `ToolContext.subagents` and
   `ToolContext.session_id`. `schemas()` takes no argument.
3. `shell_processes.rs` and `tools/shell.rs`: drop the per-process session ID
   and the `session_id` parameters; delete `kill` and `remove`. Update the
   comments that mention the agent or subagent that started a process, and the
   "(no shell process with this ID that this agent started)" text in
   `convert.rs`.
4. `model.rs`: remove `ModelRequestParameters.role` and its constructor
   parameter, and `subagent_message_text`. Update callers in `acp.rs`,
   `prompt.rs`, `convert.rs`, `openrouter.rs`, `openai.rs`, and `shell.rs`.
5. `openrouter.rs` and `openai.rs`: remove the `SubagentMessages` conversion
   branches; call `tools::schemas()`; the OpenAI namespace description becomes
   "Ox workspace and shell tools."
6. `sessions.rs`: drop `parent_session_id` from the schema, the
   `sessions_by_parent` index, `SessionSummary`, and the queries; merge `insert`
   into `create`; delete `create_child`, `children_cost`,
   `append_subagent_messages`, the subagent message types, the
   `SubagentMessages` variant, its validation, and its `subagent_messages`
   encoding. `delete` removes the session by ID alone.
7. `acp/convert.rs`: `usage_update` drops `children_cost`;
   `shell_permission_request` takes `&SessionId` and sends the call ID and title
   unchanged with no meta; delete `subagent_message_updates` and its replay
   branch.
8. `acp/prompt.rs`: remove `AcpIdentity`, `Presentation::subagent`, `role`,
   `connection`, the subagent launch, `deliver_subagent_messages` (both calls in
   `run_model_step`; `Stop::Finished` always breaks), and `stop_subagents`.
   `send_usage` reports the transcript cost alone. Update the module comment.
9. `acp.rs`: `load_session` no longer filters child sessions or adds a children
   cost; update the `ActiveSession.shell_processes` comment.
10. Fold the `routed` fixtures into single-script constructors and update the
    remaining server tests listed above.
11. Client: change `Session.permission_requests` to `permission_request` in
    `crates/ox/src/acp.rs` and its uses in `crates/ox/src/tui.rs`; remove the
    "names the subagent, if any" comment in `approval_lines`. Delete the
    nameless-call branches and their comments in `tui/transcript.rs`. Update
    `crates/ox/tests/tui.rs`.
12. `scripts/bench.py`: remove the `subagents` metric and its query.
    `scripts/export_session.py`: export one session with no `subagents` field,
    and update its header comment.

## Documentation updates

- `AGENTS.md`: `scripts/export_session.py` "exports a session to JSON."
