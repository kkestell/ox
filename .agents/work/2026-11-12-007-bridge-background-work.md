# The bridge reports Claude's background work and scheduled prompts on every state update

- Task: `2026-11-12-007-bridge-background-work`
- Status: complete
- Plan: `.agents/plans/2026-11-12-007-bridge-background-work.md`

## Changes

Design as in the plan; its "Contract for later units" holds.

- `crates/claude-code-acp/src/background.rs` (new): `Background` tracks
  Claude's live task IDs, the tool call of each started task, and the
  inferred schedules (`CronCreate`/`CronDelete`/`ScheduleWakeup`, per the
  plan's rules, including the implied fallback wakeup and the `--resume`
  seed). Renders the ACP list. Unit tests cover rendering and every schedule
  rule with explicit times.
- `crates/claude-code-acp/src/updates.rs`: `Work` carries the `Background`,
  the last list sent, and the turn's start time. Every state update carries
  `background`; a list change re-sends the state. Held tool calls (listed
  tasks and schedules) keep `in_progress`/`pending` after their result, get
  their final status from `task_notification` or when their schedule ends,
  and survive `fail_open_tools`; `exited()` fails everything and clears the
  list.
- `crates/claude-code-acp/src/runner.rs`: environment
  `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` (replacing
  `CLAUDE_CODE_SDK_READS_SESSION_STATE`) and
  `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0`; handles `task_started`,
  `task_notification`, `background_tasks_changed`; passes scheduling calls
  from main-thread and subagent messages to the tracker; seeds schedules from
  `StartRequest::transcript` on `--resume`.
- `crates/claude-code-acp/src/history.rs`, `bridge.rs`, `main.rs`:
  `History::entries` (all transcript entries, sidechain included); the
  bridge passes them on a resumed start; `idle` with `[]` follows the
  `session/new` response, and the resume snapshot with no running claude
  carries `[]`.
- `crates/claude-code-acp/tests/protocol.rs`: `new_session` checks the idle
  snapshot; new tests for a background task's tool call, claude exiting,
  foreground and unknown-tool tasks plus the resume snapshot, schedules
  through a six-turn script, and a resume seeding schedules from a
  transcript.
- `scripts/smoke-acp.py` `check_background_wake`: the Bash call is
  `in_progress` (with output) in the first turn, then `completed` before
  Claude's own `turn_started`.
- `docs/acp-v2.md`, `crates/claude-code-acp/README.md`: the list and schedule
  rules.

Changed paths:

- `crates/claude-code-acp/src/background.rs` (new)
- `crates/claude-code-acp/src/updates.rs`, `runner.rs`, `history.rs`, `bridge.rs`, `main.rs`
- `crates/claude-code-acp/tests/protocol.rs`
- `crates/claude-code-acp/README.md`, `docs/acp-v2.md`
- `scripts/smoke-acp.py`

## Deviations

- Real Claude 2.1.x sends `background_tasks_changed` (tasks carry only
  `task_id`) before `task_started`, which holds the `tool_use_id`. A task is
  therefore listed first as `_claude_task` and, once `task_started` arrives
  (the next line, before the tool result), re-listed as its `tool_call`.
  Found when the first smoke run failed; the bridge resolves the tool call
  lazily.
- The smoke check cannot see the list or the `background: []` snapshot: `oxd`
  ignores it until 008 and `oxctl` shows no state updates. It checks the tool
  call statuses instead; the list is covered by the protocol tests.
- `task_updated` is ignored: the live set comes from
  `background_tasks_changed`.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | passed (fmt, clippy `-D warnings`, workspace tests; 49 protocol + 13 unit tests in `claude-code-acp`) | `/tmp/check-rust.log` |
| `make check-scripts` | passed | `/tmp/check-scripts.log` |
| `make smoke-claude` | passed (Haiku; `background_task_wakes_claude` with the new statuses). Ran before the docs-only edits and `make fix`, which left the code unchanged. | `/tmp/smoke-claude.log` |

`make e2e` and `make check-ui` are not required: no `crates/ox` or UI change.

## Limitations

- Schedule rules assume `scheduledFor` and `nextFireAt` are epoch
  milliseconds, and that `CronCreate` always returns `nextFireAt` (a result
  without it counts as an unreadable call, lasting until exit). Not verified
  against a real `CronCreate`/`ScheduleWakeup`.
- The resume seed reads the session's main transcript only, not the
  `subagents/` files, so scheduling calls made only inside a subagent are not
  restored.
- Durable jobs saved by another session in the same folder are not
  detected (named in the docs).
- `claude` exit publishes a state update only when the list changed or a
  turn ended; scheduled-wakeup behaviour was not exercised against a real
  Claude.

## Repair round 1

- Findings addressed: R1.1. `ScheduleWakeup {stop: true}` no longer clears
  wakeups when the call is made; `Background::finish` clears wakeups and
  fallbacks only when the stop call's result is readable and successful. A
  failed or unreadable stop call falls through to the unknown-schedule rule.
  R1.2 (subagent transcript files not seeded on resume) is dropped by the
  user and stays in Limitations.
- Changed paths: `crates/claude-code-acp/src/background.rs` (fix, and the
  unit test now covers a failed stop and a successful one),
  `crates/claude-code-acp/tests/protocol.rs` (new
  `a_failed_stop_leaves_the_wakeup_listed`; in
  `scheduled_prompts_keep_the_session_listed` the stop call's tool call now
  appears before the wakeup's release, as the stop takes effect at its
  result).
- Verification: with the fix undone (wakeups cleared at `tool_use` time)
  `a_failed_stop_leaves_the_wakeup_listed` fails (list is
  `["_claude_schedule:wake-2"]`, not `["wake-1", "_claude_schedule:wake-2"]`)
  and the unit test fails; restored, both pass.

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-rust` | passed (50 protocol + 13 unit tests in `claude-code-acp`) | `/tmp/check-rust-r1.log` |

`make smoke-claude` not rerun: `runner.rs` and `updates.rs` did not change.
