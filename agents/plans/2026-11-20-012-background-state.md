# Show when a session is working in the background

## Goal

When Claude ends a turn while background work it started still runs
(background Bash, Monitor), the session looks idle: the user cannot tell
"waiting on its own job, will resume by itself" from "done, waiting for me".
After this change, such a session publishes the execution state
`background`, which the sidebar shows with its own indicator, and which
`list_sessions` and `read_session` report to other sessions' Claude. In
every rule that turns on idleness (settings changes, archiving, Send,
parking), `background` behaves as `idle`.

## Related code

- `internal/claude/agent.go` — `Agent.state` (the published execution state)
  and `Agent.background` (the count from `background_tasks_changed`);
  `handleOutput` updates the count; `Snapshot` reports the state.
- `internal/claude/prompt.go` — `Send`, `setState`, and `endPrompt` publish
  `state` updates; `idle()` (in `agent.go`) gates `Configure`, and
  `Interrupt` checks `a.state`.
- `internal/claude/park.go` — `Parkable` already refuses while
  `a.background > 0`.
- `internal/agent/agent.go` — the `State*` constants.
- `internal/server/session.go` — `hold` decides whether a session is free to
  archive from `Snapshot().State`.
- `internal/server/mcp.go` — `list_sessions` reports `State`; `stateLine`
  renders `read_session`'s last line.
- `internal/fake/fake.go` — the echo harness, which needs a way to reach the
  new state for server and e2e tests.
- `cmd/ox/web/src/protocol.ts` — `SessionState`.
- `cmd/ox/web/src/ox.tsx` — `canConfigure` gates settings on
  `idle`/`parked`.
- `cmd/ox/web/src/Sidebar.tsx` — `SessionRow` renders the status dot;
  the archive menu item gates on `idle`/`parked`.
- `cmd/ox/web/src/style.css` — the `.status.*` dots.

## Decisions

- **One state value, derived.** `background` is a new value of the
  execution state, not a field beside it. The agent keeps its two facts as
  now, `state` (Claude's report, or `running` for an accepted prompt) and
  `background` (the live task count), and derives what it publishes:
  `published() = background` when `state == idle && background > 0`, else
  `state`. `state` itself never holds `background`, so `idle()`, `Configure`,
  `Interrupt`, and `Parkable` are unchanged. A field beside `state` would let
  a page show background work on a parked or exited session unless it
  cleared the field itself; one value rules that out.
- **Pending schedules do not count.** Only live background tasks make a
  session `background`. Ox's schedule knowledge is a conservative guess for
  parking (`mayHaveSchedules` stays true after a resume until a Stop report,
  see `docs/known-issues.md`), and a recurring job would mark a session for
  up to seven days.
- **Background subagents stay `running`.** Ox leaves
  `CLAUDE_CODE_BG_TASKS_REPORT_RUNNING` at Claude's default, under which a
  background subagent keeps Claude's report at `running` until it finishes
  ([Claude Code §1](../../docs/claude-code.md#environment)). The new state
  covers background Bash and Monitor, which report `idle` either way.
- **Idle rules.** `background` is idle for every rule. `oxd`: `hold` treats
  `idle`, `background`, and `parked` as free; `Configure` and parking already
  read the agent's own `state`. The page: one helper in `protocol.ts`,
  `settled(state)`, true for `idle`, `background`, and `parked`, used by
  `canConfigure` and the Sidebar archive check, replacing their inline
  comparisons. Stop stays hidden in `background`, as in `idle`: `SessionView`'s
  `running()` is unchanged.
- **Indicator.** The status dot for `background` is a hollow ring, the same
  0.5rem as the other dots, in `running`'s green (`#16a34a`) with no pulse:
  Slack's presence convention, where a filled dot is active and a hollow one
  present but not active. Title and `aria-label`: `background`, as each
  dot's are its state.
- **Requirements.** *Session status* changes (below). *When settings can
  change*, *Archiving a session*, *Parking*, and *Claude can message other
  sessions* hold unchanged, since a session in the background is idle. The
  state arrives as `state` updates and in `session/get`, like every other
  state, so reconnects, reloads, a second page, and parking (which publishes
  `parked`, and cannot happen with a live task) need nothing new.
- **Construction.** The page never sees `background` on a session with no
  process, and never sees `idle` while Claude reports a live task, because
  the agent derives the one published value from its two facts at each
  publish.
- **Ownership.** No new state, timer, or stored field. `Agent.background`
  gains a second reader. The agent publishes a `state` update whenever the
  derived value changes: in `Send`, `setState`, and `endPrompt` (which
  publish `published()` in place of `a.state`), and on
  `background_tasks_changed` when the count crosses zero while `state` is
  `idle`. The update carries no `message_id` in that last case.
- **Claude Code.** Relies on
  [§8](../../docs/claude-code.md#8-background-work-and-schedules):
  background Bash, Monitor, and background subagents appear in
  `background_tasks_changed`, which carries the complete live set, and a
  task's end sends one without it, then a turn Claude starts itself. A
  finished task therefore shows `background`, then `idle`, then `running`
  for that turn. Claude's process exit ends the count with the agent. The
  order of `background_tasks_changed` against the end-of-turn `idle` does not
  matter, since the published value is derived either way. The live case
  below confirms the end-to-end sequence.

## Check your assumptions

1. **Pending schedules do not show as background work.**
   - **Plan:** A session with only a scheduled or looping job pending shows
     as idle; the background indicator means a task is running right now.
   - **Alternative:** Count schedules too. A session with a recurring job
     would show background work for up to seven days, and a resumed session
     would show it until its first completed turn, whether or not it has any
     schedule.
2. **A background subagent keeps showing as running.**
   - **Plan:** While a background subagent works after Claude's turn ends,
     the session keeps showing running with Stop available, as today. The
     new indicator appears for background shell commands and monitors.
   - **Alternative:** Show background subagents with the new indicator too.
     That means telling Claude to report idle while they run, which changes
     when settings can change and when Stop shows during a subagent, and
     needs its own live checks.
3. **The indicator is a hollow green ring.** *Changed from a slow grey
   spinner.*
   - **Plan:** The status shows a hollow ring the size of the other dots, in
     running's green without the pulse, where idle shows a grey dot and
     running a pulsing green one.
   - **Alternative:** The app's spinner icon in grey, which would be the
     only status that is not a dot.

Questions 1 and 2 kept.

## Test plan

- Harness: a background task live when Claude reports idle publishes
  `background`, with the prompt's `message_id`, and `Snapshot().State` is
  `background`; `background_tasks_changed` with `[]` publishes `idle`; a
  task starting and ending while `running` publishes no `state` update;
  `Configure` succeeds while `background`. New test
  `TestBackgroundTasksAfterTurn` in `internal/claude/prompt_test.go`, beside
  the other state tests; no existing test's setup ends a turn with a live
  task.
- Server: archiving a session in `background` succeeds and stops it. Add the
  case to `TestArchive` in `internal/server/mcp_test.go`, using the fake
  harness's `background` prompt.
- Server: `list_sessions` reports `background` and `read_session` ends with
  `(background)`. Add to `TestListSessionsCapturesLabelWithOwnerRead` and
  `TestReadSession`, which already check the state.
- E2e: a session sent `background 2000` shows the `background` status in the
  sidebar, with settings enabled and no Stop button, then the `idle` status.
  New test in `cmd/ox/web/e2e/session.spec.ts`.
- Live: a `TestLive` case where Haiku runs `sleep 8` as background Bash and
  ends its turn: the session publishes `background`, then `idle`, then
  `running` for Claude's own follow-up turn, and settles idle.

## Implementation plan

1. `internal/agent/agent.go`: add `StateBackground = "background"`,
   commented as idle with background tasks running.
2. `internal/claude`: add `published()`; publish it in `Send`, `setState`,
   and `endPrompt`; return it from `Snapshot`; in the
   `background_tasks_changed` case, publish a `state` update when the
   derived value changes. Keep `idle()`, `Interrupt`, and `Parkable` on
   `a.state`.
3. `internal/fake/fake.go`: a prompt `background <milliseconds>` replies as
   usual, then holds a background task for that long, publishing
   `background` at the turn's end and `idle` when it ends. The fake derives
   its published state the same way; `Configure` stays allowed and
   `Parkable` false while the task runs; `Stop` ends the task.
4. `internal/server/session.go`: `hold` treats `background` as free.
5. `internal/server/mcp.go`: no code change for `list_sessions`;
   `stateLine` prints `background` as is.
6. Web client, once the file viewer change is committed:
   `protocol.ts` adds `"background"` to `SessionState` and `settled`;
   `ox.tsx` `canConfigure` and `Sidebar.tsx`'s archive check use `settled`;
   `style.css` adds `.status.background` in `#16a34a`, whose `::before`
   dot has a transparent background and a `currentColor` border inside its
   0.5rem box (`box-sizing: border-box`).
7. Tests above; `docs/` updates below.

## Requirement changes

- **Changed:** **Session status.**
  - Old: Each session shows whether it is parked, idle, running, waiting on
    the user, or archived.
  - New: Each session shows whether it is parked, idle, idle while
    background work Claude started still runs, running, waiting on the
    user, or archived.

## Documentation updates

- `docs/protocol.md`: `background` in the `state` value lists
  (`session/get`, the `state` update row, `list_sessions`), its meaning
  beside `running`/`requires_action`/`idle`, and `(background)` in
  `read_session`'s state line.
- `docs/architecture.md`: the published execution state is `background`
  while Claude's state is `idle` and it reports live background tasks.
