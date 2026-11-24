# Subscription usage under the composer

## Goal

A session shows how much of the user's subscription usage it has used, under
its composer, as issue [#2](https://github.com/kkestell/ox/issues/2) asks. A
harness reports any number of usage windows, each with its percent used, its
length, and when it resets. The Claude agent reports the windows of each
turn's `rate_limit_event`; Ur reports none. The page shows the window with the
shortest interval, and clicking it opens a popover listing every window. A
session that has not reported usage shows nothing.

## Related code

- `internal/agent/agent.go` — `Update`, the one type of session updates, and
  its update types. Gains the usage window type and the `usage` update.
- `internal/claude/messages.go`, `internal/claude/agent.go` (`handleOutput`) —
  parse Claude's lines and publish updates; `rate_limit_event` is ignored today.
- `internal/fake/fake.go` — the echo harness the server tests and e2e specs
  drive by prompt text (`think`, `wait`, `approve`).
- `internal/server/history.go` (`record`) — the update types recorded as
  history, which `session/get` returns and which survive parking and restarts.
- `internal/server/mcp.go` (`renderEntry`) — renders history for
  `read_session`; unknown types render nothing, so `usage` stays out of it with
  no change.
- `cmd/ox/web/src/protocol.ts` — the page's protocol types.
- `cmd/ox/web/src/ox.tsx` — `SessionSnapshot`, `apply` (history and live
  updates into the snapshot), `load` (merges a reloaded snapshot), and `clock`,
  the page's one-second clock.
- `cmd/ox/web/src/SessionView.tsx` — the composer and `SettingsForm`, whose
  `.settings` row sits under the prompt box.
- `cmd/ox/web/src/SettingsFields.tsx` (`ModelPicker`) — the page's popover
  pattern: a button with `popovertarget`, a `popover="auto"` panel, and CSS
  anchor positioning.
- `cmd/ox/web/src/style.css` — `.composer .settings`, `.model-picker`.
- `/Users/kyle/src/hypercube/internal/ui/web/agent/limits.tsx` — the look to
  copy: label, bar with elapsed marker, percent, time left; amber at 75%, red
  at 90%; a reset window dims.

## Decisions

- **Protocol shape.** A new update type `usage` carries `usage`, an array of
  one or more windows, shortest `interval_seconds` first. Each window is
  `interval_seconds` (integer, the window's length), `used_percent` (number,
  0–100), and `resets_at` (RFC 3339). Nothing names Claude's windows; the page
  labels a window from its interval (`5h`, `7d`).
- **The reading lives in the history.** The daemon records `usage` updates
  with the other history types, so the latest reading is the last `usage`
  update in the session's history. It survives parking and restarts with no
  new store field, session field, or snapshot field, and `session/get`
  returns it the same way it returns the transcript. A reading outlasting its
  window is shown dimmed, as Hypercube shows it; the history has no other
  staleness to manage. One row per turn is a negligible cost.
- **Requirements.** *Subscription usage* (added). *One transcript*: the
  transcript is unchanged, since `apply` keeps usage out of `entries`; every
  page applies the same history and live updates, so every page shows the
  same reading. *Every page in sync*: `usage` is broadcast like every update.
  *Lost connection*: a reconnect reloads `session/get`, whose history carries
  the latest reading, and `load` copies it onto the kept snapshot.
  *Surviving restarts*, *Parking*: the reading is history, so both keep it.
  *Archived sessions*: the composer is hidden, so usage is too. The usage
  display takes no action, so no request lifecycle applies.
- **Construction.**
  - Windows are ordered shortest first by `agent.UsageUpdate`, the one
    constructor every harness uses; the page shows `usage[0]` and lists the
    array in order, deciding nothing about order itself.
  - A `usage` update has at least one window: the Claude agent publishes one
    only when it mapped a window, and the fake always reports two. The page
    shows usage exactly when its snapshot has a reading.
  - The page's reading has one source, the updates it applies:
    `SessionSnapshot.usage` is set only by `apply` on a `usage` update, as
    `entries` are built.
  - Time left and elapsed are derived from `resets_at`, `interval_seconds`,
    and `Date.now()` read under `ox.clock()`, so they advance with the page's
    existing clock and no new timer.
- **Ownership.** No new state, timer, retry, lock, or stored field in `oxd`:
  the reading is history rows, owned by the session's history like every
  recorded update and deleted with the session. On the page,
  `SessionSnapshot.usage` is derived state rebuilt from history on every load.
- **Claude Code.** [`rate_limit_event`](../../docs/harnesses/claude-code.md#rate_limit_event):
  once per turn after the assistant message, none before the first turn,
  subscription auth only; `unifiedWindows` holds `five_hour` and `seven_day`,
  `utilization` is a fraction, `resetsAt` is Unix seconds, windows are 300 and
  10080 minutes. The agent maps those two keys to 18000 and 604800 seconds,
  `used_percent = utilization * 100`, and `resets_at = time.Unix(resetsAt, 0)`.
  A key the table does not name is skipped, since the docs record no interval
  for it; an event with no mapped window publishes nothing. The live case
  below confirms a real turn yields a recorded reading. The update is
  published in the turn and tagged like the turn's other updates.

## Check your assumptions

1. **Usage shows after the first turn and keeps the last reading.**
   - **Plan:** Under the composer, a Claude session shows its shortest usage
     window (5 hours) with a bar, percent, and time left; clicking it lists
     every window. The reading from the session's latest turn stays through
     parking, reconnects, and `oxd` restarts, dimmed once its window has
     reset. A new session, an Ur session, or one whose Claude has not finished
     a turn shows nothing. Proposed requirement: "**Subscription usage.**
     Under the composer, a Claude session shows how much of its shortest
     subscription usage limit is used and how long until it resets; clicking
     it lists every limit Claude reports. The session shows the reading from
     its latest turn, through parking and restarts, dimmed once its limit has
     reset, and shows nothing before Claude has reported one."
   - **Alternative:** Show nothing after parking or a restart until the next
     turn. The user would lose sight of their usage whenever an idle session
     parks, five minutes after its last turn.

## Test plan

- Claude harness (`internal/claude/prompt_test.go`, new
  `TestUsageIsPublished` beside `TestThinkingIsPublished`, using
  `promptingAgent`): a `rate_limit_event` with `five_hour` (0.01), `seven_day`
  (0.39), and an unnamed key publishes one `usage` update tagged with the
  prompt's ID, holding the 18000-second window then the 604800-second window,
  with percents 1 and 39 and the parsed reset times; a `rate_limit_event`
  without `unifiedWindows` publishes nothing. No existing test reads
  `rate_limit_event`.
- Server (`internal/server/history_test.go`,
  `TestHistoryMergesTextAndKeepsConversation`): record a `usage` update among
  the others; the fresh history read after the simulated restart includes it,
  in order, with its windows' fields intact.
- e2e (`cmd/ox/web/e2e/session.spec.ts`), driven by the fake's `usage`
  prompt:
  - A session shows no Subscription usage button after an ordinary turn, and
    shows it with `5h` after a `usage` turn.
  - Clicking Subscription usage opens a popover listing `5h` and `7d`, in
    that order.
  - The reading survives a page reload.
- Live (`internal/server/live_test.go`, section `resume after restart`): after
  the `cleared` session's first turn, its `session/get` history holds a
  `usage` update whose windows each have a positive `interval_seconds` and a
  `resets_at` in the future; after the restart, the history still holds it.

## Implementation plan

1. `internal/agent/agent.go`: add `UpdateUsage = "usage"` (comment: the
   harness's subscription usage, after a turn). Add
   `Usage []UsageWindow \`json:"usage,omitempty"\`` to `Update`. Add
   `UsageWindow{IntervalSeconds int64 \`json:"interval_seconds"\`; UsedPercent
   float64 \`json:"used_percent"\`; ResetsAt time.Time \`json:"resets_at"\`}`,
   documented as one subscription usage window. Add
   `func UsageUpdate(windows []UsageWindow) Update`, which sorts the windows
   by `IntervalSeconds` and returns the `usage` update; its comment says every
   harness builds usage updates with it and that `windows` is non-empty.
2. `internal/claude/messages.go`: add to `message`
   `RateLimitInfo struct { UnifiedWindows map[string]struct { Utilization
   float64 \`json:"utilization"\`; ResetsAt int64 \`json:"resetsAt"\` }
   \`json:"unifiedWindows"\` } \`json:"rate_limit_info"\``, and a method
   `usageWindows() []agent.UsageWindow` mapping the keys through a
   package-level table `windowSeconds = map[string]int64{"five_hour": 5 *
   3600, "seven_day": 7 * 24 * 3600}`, skipping unnamed keys.
3. `internal/claude/agent.go` (`handleOutput`): add `case "rate_limit_event"`
   that publishes `agent.UsageUpdate(windows)` through `a.publish` when
   `m.usageWindows()` is non-empty. Write `TestUsageIsPublished`.
4. `internal/server/history.go`: add `agent.UpdateUsage` to `record`'s list.
   Extend `TestHistoryMergesTextAndKeepsConversation`.
5. `internal/fake/fake.go`: an input `usage` publishes, before its result,
   `agent.UsageUpdate` with a 7-day window at 40% resetting in three days and
   a 5-hour window at 80% resetting in two hours, given in that order. Add it
   to `Send`'s doc comment.
6. `cmd/ox/web/src/protocol.ts`: add
   `export type UsageWindow = { interval_seconds: number; used_percent: number; resets_at: string }`
   and `| { type: "usage"; usage: UsageWindow[] }` to `OtherUpdate`.
7. `cmd/ox/web/src/ox.tsx`: add `usage?: UsageWindow[]` to `SessionSnapshot`
   (the windows of the session's latest `usage` update, shortest first);
   `apply` sets it on `case "usage"`; `load` copies `snapshot.usage` onto the
   kept snapshot beside `settings` and `queued`.
8. `cmd/ox/web/src/Usage.tsx` (new): `Usage(props: { harness: string; windows:
   UsageWindow[] })`, following `ModelPicker`'s popover pattern. A button with
   `popovertarget`, `aria-label="Subscription usage"`, and an anchor name,
   showing a `WindowRow` for `windows[0]`; a `popover="auto"` panel anchored
   above it (as `.model-picker`, `position-area: top span-left` so it opens
   toward the page) listing a `WindowRow` for each window. `WindowRow`
   adapts Hypercube's `LimitRow` into a four-column grid: label from
   `windowLabel(interval_seconds)` (`d` from 86400 s, `h` from 3600 s, else
   `m`), a bar (`role="meter"`, `aria-label="<label> usage"`,
   `aria-valuenow`, min 0, max 100) filled to `used_percent` with a marker at
   the elapsed share of the interval, the rounded percent, and the time left
   in Hypercube's `timeLeft` format. The fill is neutral, amber at 75%, red
   at 90%. A window whose `resets_at` has passed dims, shows `Waiting` in
   place of time left, and has Hypercube's title naming the harness with
   `harnessName`. Time is `Date.now()` read in a memo that tracks
   `ox.clock()`. On narrow screens the button omits the time left; the
   popover always shows it.
9. `cmd/ox/web/src/SessionView.tsx`: move the `.settings` div out of
   `SettingsForm` into `SessionView`, wrapping `SettingsForm` and
   `<Show when={props.snapshot.usage}>{(windows) => <Usage harness=…
   windows={windows()} />}</Show>`, the usage pushed to the row's end.
10. `cmd/ox/web/src/style.css`: styles for the usage button (as the picker
    button: no border, hover `var(--accent)`, `--muted-foreground` text,
    small tabular numerals), the row grid, bar, marker, threshold colors,
    the dimmed reset row, and the popover panel (as `.model-picker`'s
    surface).
11. Write the e2e specs and the live assertion, then update
    `docs/protocol.md` and `docs/functional-requirements.md`.

## Requirement changes

- **Added** under Claude Code, after Subscription billing:
  "**Subscription usage.** Under the composer, a Claude session shows how much
  of its shortest subscription usage limit is used and how long until it
  resets; clicking it lists every limit Claude reports. The session shows the
  reading from its latest turn, through parking and restarts, dimmed once its
  limit has reset, and shows nothing before Claude has reported one."

## Documentation updates

- `docs/protocol.md`: a `usage` row in the Session updates table (`usage`:
  windows, shortest `interval_seconds` first, each with `interval_seconds`,
  `used_percent`, `resets_at`; a harness's subscription usage, reported after
  a turn; Claude reports it each turn, Ur never), an example line, and
  `usage` in `session/get`'s list of recorded update types.
