# The bridge reports Claude's background work and scheduled prompts on every state update

- Task: `2026-11-12-007-bridge-background-work`
- Kind: plan
- Status: ready
- Source HEAD: `ddb1b273b492bd880310fd7d3880727afee215df`
- Author: planner child of Hypercube session 945c2ca2, Claude Code, Opus 5.5
  (`claude-opus-5-5`); coordinator ID `planner`; native conversation ID
  `7eb96350-35c2-461c-9497-348302bd4237`
- Dependencies: none. `2026-11-12-008-park-idle-sessions` relies on the
  contract in **Contract for later units**.

## Outcome and boundaries

First of five units for the request: "I would like to add automatic session
parking, and explicit user archiving of sessions … similarly to how it does in
~/projects/hypercube. One thing we need to make sure we handle is not parking a
Claude session that appears to be idle, but actually has background tasks /
watchers running." Units: this one, then `008-park-idle-sessions`,
`009-persist-session-events`, `010-archive-sessions`, `011-app-archive`.

This unit makes `claude-code-acp` tell its client everything that keeps the
Claude session busy between turns, through ACP v2's standard `background` list
on state updates. Nothing in `oxd` or the app changes behaviour; `oxd` still
ignores the list until 008.

Done means:

- every `state_update` the bridge sends carries `background`, `[]` when
  nothing runs;
- the list holds every task in Claude's live background set and every
  scheduled prompt the bridge can infer, by the rules below;
- a listed tool call keeps a non-final status until its work ends;
- the turn Claude starts when background work reports back is reported as
  `running` … `idle`.

Not in this unit: any `oxd`, protocol, `oxctl` or app change, apart from
`docs/acp-v2.md`.

## Current system

- `crates/claude-code-acp/src/runner.rs`: `Claude::start` runs `claude -p`
  with stream-json and `CLAUDE_CODE_SDK_READS_SESSION_STATE=1`, lazily on the
  first prompt, with `--resume` when the session has a Claude ID.
  `Output::translate` maps `system/session_state_changed` to
  `Updates::working` / `idle_without_reason` and ends the turn on `result`
  (`end_turn` → `Updates::idle`), calling `fail_open_tools`. Messages with a
  `parent_tool_use_id` go to `subagent_event`. All other `system` subtypes are
  ignored.
- `crates/claude-code-acp/src/updates.rs`: `Work`/`Sent` dedupe state updates;
  `state_update()` builds them without `background`. `Tools.open` holds tool
  calls without a result; `event(ToolResult)` sends `completed`/`failed`.
- `bridge.rs` `resume_session` sends a state snapshot from `Claude::state()`,
  or `idle` when no claude runs. `new_session` sends none.
- Tests: `crates/claude-code-acp/tests/protocol.rs` drives the binary with fake
  `claude` shell scripts. `scripts/smoke-acp.py` `check_background_wake`
  expects the background Bash call `completed` in the first turn.

## What Claude Code reports (verified with Claude Code 2.1.322)

Experiments ran `claude -p --input-format stream-json --output-format
stream-json --verbose` with the bridge's environment.

- A `run_in_background` Bash command, a Monitor watch and a background subagent
  each appear in `system/background_tasks_changed`, whose `tasks` array is the
  full live set, and in `system/task_started` with `tool_use_id` and
  `is_backgrounded: true`. Both arrive before the call's `tool_result`. When a
  task ends: `task_updated`, `task_notification` (`task_id`, `tool_use_id`,
  `status`, `summary`), then `background_tasks_changed` without it, then a turn
  of Claude's own. Foreground tasks get `task_started` but never enter the
  live set. Claude's docs: the set is not emitted at startup; reset it when the
  process starts.
- After a background Bash or Monitor starts, Claude reports `idle` while the
  task runs.
- With a background subagent and the default
  `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING`, Claude holds `running` until it
  finishes and sends no new `running` for the follow-up turn, which the bridge
  then streams while reporting idle. With `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0`
  Claude reports `idle` at the turn's end and `running` when the follow-up
  turn starts.
- Scheduled prompts produce no list. Stream `user` messages carry the
  structured `tool_use_result`; transcripts carry it as `toolUseResult`, with a
  `timestamp`. Documented shapes (Agent SDK reference): `CronCreate` input
  `{cron, prompt, recurring?, durable?}`, output `{id, humanSchedule,
  recurring, durable?, nextFireAt?}` (epoch ms, jitter applied); `CronDelete`
  input `{id}`; `ScheduleWakeup` input `{delaySeconds?, reason?, prompt?,
  noop?, stop?}`, output `{scheduledFor, clampedDelaySeconds, wasClamped,
  stopped?, cancelledWakeups?}`. Recurring jobs expire 7 days after creation
  after one final fire. A self-paced `/loop` iteration that neither reschedules
  nor stops gets a fallback wakeup "about 20 minutes later", and the loop ends
  if that iteration does not reschedule either. `claude --resume` restores
  unexpired recurring jobs (verified with `CronList`) and one-shot jobs not yet
  due, but not wakeups.

ACP v2 (`agent-client-protocol-schema` 1.17.0, `StateUpdate::background`,
`BackgroundWork`): every state update may carry a complete `background` list;
absent means not reported, `[]` means nothing runs; once reported it is on
every state update; items are `tool_call {toolCallId}`, `terminal`, or custom
`_`-prefixed types, which still count as running work; a listed tool call
keeps `pending` or `in_progress` and gets its final status before the snapshot
that drops it; a tool call with a final status is not reopened.

## Design

### Environment

`Claude::start` sets `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0` (entry 3) and
replaces the undocumented `CLAUDE_CODE_SDK_READS_SESSION_STATE=1` with the
documented `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`. The smoke check confirms
`session_state_changed` still arrives.

### The background list

A `Background` value, owned by `Updates` under the same lock as `Work`, holds
two parts and renders the ACP list:

- **Tasks.** The task IDs of Claude's latest `background_tasks_changed`, each
  with its `tool_use_id` from `task_started`, keyed by task ID. Every task
  counts, `ambient` or not (entry 1).
- **Schedules.** Jobs inferred from scheduling tool calls (below).

Rendering: an item whose tool call the client has as a live main-thread tool
call (in `Tools.seen` and not given a final status) is `tool_call
{toolCallId}`. Anything else (a task without a known tool call, a schedule from
a subagent, from the transcript, or an implied fallback wakeup) is a custom
item `{"type": "_claude_task", "taskId"}` or `{"type": "_claude_schedule",
"id"}`. Each piece of work appears once.

`publish` puts the rendered list on every state update it sends, including
`Updates::state()` (the resume snapshot). When the list changes and the
foreground state does not, `Updates` re-sends the current state with the new
list. The state's dedupe compares state and list.

When claude starts or exits, both parts reset to empty, then a `--resume`
start seeds the schedules from the transcript.

### Tool calls of background work

- `event(ToolResult)` for a tool call whose task is listed sends the result's
  content with status `in_progress` and does not finish it.
- `task_notification` for a listed task's tool call sends its final status:
  `completed` for `completed` and `stopped`, `failed` for `failed`, with the
  `summary` appended to its content. The `background_tasks_changed` that drops
  the task follows, so the final status precedes the snapshot without it.
- A background subagent's tool call keeps receiving its activity lines
  (`subagent_event`) while `in_progress`.
- `fail_open_tools` at a turn's `result` skips tool calls that are listed. At
  claude's exit, it fails them all, and the list resets.
- Scheduling tool calls on the main thread stay `pending` while their job is
  listed, and become `completed` when it is dropped.

### Schedules (entry 2)

The rules err toward keeping the session awake:

- `CronCreate` with a parsable result: a job `{id, recurring, fire_at:
  nextFireAt, created}` (`created` is when the result arrived, or the
  transcript entry's `timestamp`).
- A recurring job stays until `CronDelete` names its ID, or until a turn that
  started at or after `created` + 7 days has ended.
- A one-shot job stays until a turn that started at or after `fire_at` has
  ended, or `CronDelete` names it.
- `ScheduleWakeup` without `stop`: a wakeup `{fire_at: scheduledFor}`, kept
  until a turn that started at or after `fire_at` has ended. With `stop: true`,
  every wakeup and implied fallback is dropped.
- When the turn that ends a wakeup made no `ScheduleWakeup` call, the loop has
  an implied fallback wakeup with `fire_at` = that turn's end + 20 minutes. It
  is dropped like a wakeup, and a turn that ends it without a `ScheduleWakeup`
  call adds no further fallback (Claude ends the loop then).
- A `CronCreate`, `CronDelete` or `ScheduleWakeup` call whose input or result
  the bridge cannot read (missing `tool_use_result`, a result shared with
  other tool results in one message, missing `nextFireAt` or `scheduledFor`,
  a failed call) adds an unknown schedule kept until claude exits.
- Scheduling calls from subagents count the same way.
- Turn start and end times come from the bridge's own working/idle
  transitions; wall-clock times are `SystemTime` epoch milliseconds to compare
  with Claude's. Nothing in the bridge sleeps: jobs are dropped only at turn
  ends and tool calls.
- On a `--resume` start, the tracker runs over the transcript's main-thread
  and sidechain tool calls in order (`History` already reads the transcript),
  then keeps only what Claude restores: recurring jobs younger than 7 days and
  one-shot jobs whose `fire_at` is in the future. Wakeups and unknown entries
  from the transcript are dropped, since resume does not restore them.

Not detectable: durable jobs saved to `.claude/scheduled_tasks.json` by
another session in the same folder. They are named as a limitation in
`docs/acp-v2.md`.

### Snapshots for new sessions

After answering `session/new`, the bridge sends `idle` with `background: []`
so a session that is never prompted still reports the list (ACP: a new session
is idle until told otherwise; a repeated snapshot is not a transition).

### Docs

`docs/acp-v2.md`: `background` work lists are supported by `claude-code-acp`,
with what they contain and the schedule rules; the stop-reason and state rows
mention the follow-up turn. `oxd`'s column stays "Ignored" until 008.

## Contract for later units

`claude-code-acp` reports `background` on every state update, starting with
the snapshot after `session/new` or `session/resume`. `idle` with `[]` means
Claude has no foreground work, no live background task, and no scheduled
prompt the bridge knows of. Anything else, including an unknown schedule,
means work could start without a prompt.

## Check your assumptions

1. **Confirmed as proposed.** A session with any background work never parks,
   including a dev server or a persistent Monitor, until it ends or the user
   stops it. Every task in Claude's live set counts, `ambient` included. A
   session that never parks in practice will be fixed when seen.
2. **Confirmed, changed from the sketch.** Scheduled prompts keep a session
   awake. The bridge infers jobs from `CronCreate`, `CronDelete` and
   `ScheduleWakeup` and lists them, every guess erring toward staying up: a
   job counts from creation; a recurring job until deleted or expired; a
   one-shot job or wakeup until its fire time has passed and the turn it
   started has ended; an unreadable scheduling call until claude exits. A
   weekly job holding the session for days is accepted. Applying the same
   rule, the plan also counts Claude's implied fallback wakeup and the jobs a
   `--resume` restores.
3. **Confirmed as proposed.** A listed tool call keeps a non-final status
   until its work ends, and claude runs with
   `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING=0`, so the follow-up turn shows as a
   turn.

## Implementation sequence

1. `runner.rs` `Claude::start`: the two environment changes; on `--resume`,
   seed the schedule tracker from `History` before reading output.
2. `updates.rs`: `Background` (tasks, schedules, rendering), carried in
   `state_update`, `publish`, `state()`; re-send on list change; reset on
   start and exit; `ToolResult` and `fail_open_tools` exemptions; final status
   on notification; scheduling tool calls kept `pending`.
3. `runner.rs` `Output::translate`: handle `background_tasks_changed`,
   `task_started`, `task_updated` (`is_backgrounded`), `task_notification`;
   pass scheduling tool calls with their `tool_use_result` to the tracker,
   from main-thread and subagent messages; record turn start and end times.
4. `bridge.rs` / `main.rs`: the `idle` snapshot after `session/new`.
5. `docs/acp-v2.md`, and the bridge `README.md` if it lists environment
   variables.
6. `scripts/smoke-acp.py` `check_background_wake`: expect the Bash call
   `in_progress` in the first turn, a state update listing it, then
   `completed` and a state update with `background: []` before Claude's own
   turn.

## Verification

Tests in `crates/claude-code-acp/tests/protocol.rs` with fake claude scripts:

- background Bash: `task_started` + `background_tasks_changed` + tool result →
  the tool call stays `in_progress`, the turn's `idle` lists it; notification
  and empty set → `completed`, then a state update with `[]`;
- a turn's `result` does not fail a listed tool call; claude's exit fails it
  and the next state update has `[]`;
- a `task_started` for a foreground task lists nothing;
- `session/new` is followed by `idle` with `[]`; `session/resume` snapshot
  carries the list;
- `CronCreate` recurring: listed until `CronDelete`; one-shot: listed through
  a turn that started before `nextFireAt`, dropped after a turn that started
  after it; `ScheduleWakeup` then a turn without a reschedule leaves the
  implied fallback listed; `stop: true` empties schedules; an unreadable
  `CronCreate` stays until exit;
- `--resume` seeds a recurring job from a transcript fixture and skips a due
  one-shot and a wakeup.

Unit tests in `updates.rs` cover rendering (tool call vs custom item, once
each). Each fix's test fails with the fix undone (AGENTS.md).

Commands: `make check-rust`, `make smoke-claude` (required for
`claude-code-acp` changes).

## Handoff

Ready. Work size: one mechanism in the bridge (the background list), with its
two sources and its tool-call status rules, and fake-claude tests; a normal
assignment, at the upper end. Detail: the schedule rules are spelled out
because they are the user's settled policy, and the Claude Code section
records verified message shapes the implementer would otherwise have to
rediscover. `plan_metrics.py`: 2071 words, above the provisional 600–1500
band and at this repository's historical Q3 (2042, n=14); the excess is that
evidence, not extra scope.
