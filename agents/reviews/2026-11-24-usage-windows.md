# Review: subscription usage under the composer

## Scope and coverage

Uncommitted changes in the `usage-windows` worktree on base `5d72dd5` (`git merge-base origin/main HEAD`, also `HEAD`), checked against `agents/plans/2026-11-24-usage-windows.md`, which the user approved as written. The tree holds the modified files under `cmd/ox/web`, `docs`, and `internal`, plus the untracked plan and `cmd/ox/web/src/Usage.tsx`.

The UI was compared with Hypercube's `internal/ui/web/agent/limits.tsx` and checked in headless Chrome through `make dev` against a real Claude Haiku session. Views checked: the collapsed reading and the open popover at 1280×800, at a 390×844 viewport, and in an emulated iPhone 15 (touch, coarse pointer).

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks lists, was not passed to this review. The checks below were run here instead.
- The open `bug` issues (#5, #6, #33, #34) do not touch usage, and this change resolves none of them.

## Summary

| ID | Severity | Category    | Action | Status   | Title                                                        |
| -- | -------- | ----------- | ------ | -------- | ------------------------------------------------------------ |
| F1 | Low      | Testing     | Fix    | Resolved | Percent, time left, reset dimming, and reconnect are untested |
| F2 | Low      | Conventions | Fix    | Resolved | Touch screens get the reading at desktop size                 |
| F3 | Low      | Conventions | Fix    | Resolved | The usage button's name drops its visible text                |

## F1. Percent, time left, reset dimming, and reconnect are untested

Low · Testing · Fix · `cmd/ox/web/e2e/session.spec.ts:385`

The requirement *Subscription usage* promises how much of the limit is used, how long until it resets, and a dimmed reading once the limit has reset. The three new specs check only the window labels (`5h`, `7d`) and that a reload keeps the reading. No test reads the percent or the time left, no test reaches a reading whose `resets_at` has passed, and no test covers the reconnect path in `load` (`cmd/ox/web/src/ox.tsx:607`, `previous.usage = snapshot.usage`), which *Lost connection* relies on when a turn finishes while the page is disconnected. A reload does not reach that line: a fresh page has no previous snapshot.

### Evidence

With `previous.usage = snapshot.usage;` removed, `npx playwright test -g "usage|reconnect"` passed all 15 tests. The fake's `usage` input reports only windows that reset in the future (`internal/fake/fake.go:183`), so no spec can reach the `reset()` branch in `WindowRow`.

### Action

Add e2e coverage in `cmd/ox/web/e2e/session.spec.ts`:

- In "subscription usage shows under the composer once the harness reports it", assert that the meter named `5h usage` has `aria-valuenow` `80`, and that the button shows `80%` and the time left (`/1h 59m|2h 0m/`).
- A spec for a reset window. Give the fake a second input, such as `usage reset`, that reports a 5-hour window whose `resets_at` has already passed. Assert that the `5h usage` meter's `aria-valuetext` names the wait for a new reading, and that the button shows `Waiting`.
- A spec for a reading that arrives while the page is disconnected. Hold the event stream, as "a reconnecting stream shows as a status, not an error" does, while a `usage` turn finishes, then release it. Assert that the reading appears once the page has reconnected. This spec must fail with the `load` line removed.

## F2. Touch screens get the reading at desktop size

Low · Conventions · Fix · `cmd/ox/web/src/style.css:394`

On a coarse pointer, the stylesheet's touch rule (`style.css:538`) gives the composer's settings controls 15px text and a 2rem height, so they are large enough to tap. The new usage button sits in the same row but keeps 12px text and a 1.5rem (24px) target. In an emulated iPhone 15 the model picker and permission select render at 15px and 32px tall, and the usage reading renders at 12px and 24px tall beside them.

The settings row also wraps on a phone. The picker (158px), the select (124px), and the reading (117px), plus their gaps, need 407px against the row's 374px, so the reading drops to a line of its own at the row's right end. At a 390px viewport with a fine pointer, the row overflows by 1px and wraps the same way.

### Evidence

The iPhone 15 probe measured `picker 158px / 15px` and `select 124px / 15px` at `top 583`, and `usage 117px / 12px` at `top 619`. At 390×844 the row's children measured 141.7 + 108.8 + 116.7px plus two 4px gaps, against a 374px row.

### Action

Add `.composer .settings > button.usage` to the `@media (pointer: coarse)` rule, at the pickers' `height: 2rem` and `font-size: 15px`. On a phone the reading then takes its own line under the pickers. That is the layout the user will judge from the pull request's phone screenshots. No test: specs do not assert styling.

## F3. The usage button's name drops its visible text

Low · Conventions · Fix · `cmd/ox/web/src/Usage.tsx:578`

The button's `aria-label="Subscription usage"` replaces its content as its accessible name. A screen reader announces "Subscription usage, button" without the `5h 12% 1h 19m` it shows, and voice control cannot target the button by what it displays. `ModelPicker` handles the same case with `aria-labelledby`, naming its button "Model" followed by its content (`SettingsFields.tsx:63`), so that the visible text stays part of the name.

### Evidence

The accessibility snapshot in headless Chrome showed `button "Subscription usage"` with children `5h`, `meter "5h usage"`, `12%`, and `1h 19m`. The model picker beside it is named `"Model Claude Haiku 5.5 Low"`.

### Action

Name the button the way `ModelPicker` does: a hidden "Subscription usage" label span, plus `aria-labelledby` pointing at the label and at the button. The existing specs find the button with `getByRole("button", { name: "Subscription usage" })`, which matches a substring, so they continue to pass. Add an assertion in the first usage spec that the button's accessible name contains `80%`.

## Checks run

- `make test`: Go tests, golangci-lint, Biome, and the Node suites passed. Biome's four `noDescendingSpecificity` notes are on lines this change does not touch.
- `make e2e`: 168 passed, 4 skipped.
- `dprint check`: passed. The requirement added to `docs/functional-requirements.md` matches the plan's text word for word and sits after *Subscription billing*.
- Live Go suite (`OX_LIVE=1 go test -race -count=1 -run TestLive -v ./internal/server`): all 23 subtests and `TestLiveUr` passed, including the new usage assertions in `resume after restart`.
- `make dev` in headless Chrome with a real Haiku turn: the reading appeared under the composer (`5h`, 12%, `1h 19m`). The popover listed `5h` and then `7d`, with an elapsed marker matching each window's time left.
- Spot check: with the sort in `agent.UsageUpdate` removed, all three usage specs fail.
- Spot check: with `previous.usage = snapshot.usage` removed, every usage and reconnect spec passes (F1).
- `make live`'s web half (`approval.spec.ts`) was not run. It does not exercise usage.

## Verification 1

Checked the uncommitted tree after `/ox-work fix`, on the same base `5d72dd5`. The changes since the review are in `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/src/Usage.tsx`, `cmd/ox/web/src/style.css`, and `internal/fake/fake.go`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The first usage spec now asserts `80%`, the time left, and the meter's `aria-valuenow`. A new spec, "a usage window that has reset shows it is waiting for a new reading", drives the fake's new `usage reset` input. A second new spec, "a usage reading reported while the page is disconnected shows once it reconnects", sends the turn from a second page while the first page's stream is held. With `previous.usage = snapshot.usage` removed, the reconnect spec fails. With `reset()` forced false, the reset spec fails (received `5h80%0m`). The reconnect spec passed 8 of 8 runs with `--repeat-each=8`. |
| F2 | Resolved | The touch rule now includes `button.usage`. In an emulated iPhone 15, the reading renders at 15px and 32px tall, matching the pickers, on its own line under them. The popover's rows stay at 12px, like the desktop popover. |
| F3 | Resolved | The button is named through `aria-labelledby` (hidden label plus the button), as `ModelPicker` is. With `aria-label` restored, the first usage spec fails: expected `/80%/`, received "Subscription usage". |

No new findings.

Checks run:

- `make test`: passed (exit 0).
- `make e2e`: 170 passed, 4 skipped.
- `make dev` in headless Chrome, using the Haiku session from the review: the collapsed reading and the popover at 1280×800 and in an emulated iPhone 15.
- The live Go suite was not rerun. Since the review's live run, the Go code changed only in the fake harness, which the live suite does not use.

## Verification 2

Checked commit `af99c32`, the change rebased onto `origin/main` at `4286e97` (#81, phone text size). The rebase conflicted in the `@media (pointer: coarse)` block of `cmd/ox/web/src/style.css`. The resolution keeps all of #81's lines and changes only the composer selector to `.composer .settings > :is(button.picker, button.usage)` at 15px. That matches #81's intent that the composer's pickers stay at 15px. No other file in the commit differs from the tree Verification 1 checked.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | Unchanged by the rebase. All five usage specs pass. |
| F2 | Resolved | In an emulated iPhone 15 through `make dev`, the body text is 17px. The picker, the select, and the usage reading are all 15px and 32px tall, with the reading on its own line under the pickers. The popover's rows stay at 12px. |
| F3 | Resolved | Unchanged by the rebase. The first usage spec asserts that the button's name contains `80%`. |

No new findings.

Checks run:

- `make test`: passed (exit 0).
- `dprint check`: passed.
- `make e2e`: the first run failed some tests (163 passed). The next three runs each passed 170, with 4 skipped.
- The usage specs with `--repeat-each=10`, three runs: 1 to 5 failures per run. Every captured failure was `browserType.launch` aborting Chrome (SIGABRT) before the test body ran, a failure of the machine's Chrome rather than of the specs. No assertion failed.
- `make dev` in headless Chrome, emulated iPhone 15: the collapsed reading and the open popover.

## Verdict

Ready to commit. F1, F2, and F3 are resolved on the rebased commit `af99c32`.
