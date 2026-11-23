# The page shows and recovers from its own failed requests

## Scope and coverage

Uncommitted changes on `page-errors` against base `394daa4`, reviewed against `agents/plans/2026-11-22-page-errors.md` (issue #28, audit findings F2, F3, F7). Working tree: changes to `connection.ts`, `poll.ts`, `ox.tsx`, `App.tsx`, `style.css`, `session.spec.ts`, `workspace.spec.ts`, and `docs/architecture.md`, plus the untracked plan.

Gaps:

- The review is proportionate by request: no child sessions, and the full suites were not rerun. The implementer's `make test`, `make e2e`, and headless Chrome check through `make dev` are taken from their report.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                        |
| -- | -------- | ----------- | -------- | ------ | ------------------------------------------------------------ |
| F1 | Low      | Correctness | Rejected | —      | One retry's success clears another retry's identical error   |
| F2 | Low      | Ownership   | Rejected | —      | A retry from the previous connection continues after reconnect |

## F1. One retry's success clears another retry's identical error

Low · Correctness · Rejected · `cmd/ox/web/src/ox.tsx:367`

`reload` runs `load` for every session at once. When two snapshots fail with the same message, such as `Could not reach ox`, the first to succeed sees `error() === shown` and clears the page error while the other is still retrying.

### Evidence

Traced from `retry`: the clear compares message text, not which call set it.

### Action

Rejected. The other retry sets the error again on its next failure, within one second, so the alert flickers at worst. Tracking ownership of the error per call costs more than the flicker.

## F2. A retry from the previous connection continues after reconnect

Low · Ownership · Rejected · `cmd/ox/web/src/ox.tsx:363`

When the stream drops and returns during a retry's one-second wait, the old `retry` wakes on the new connection and repeats its request there, alongside the new `reload`.

### Evidence

Traced: `connection.rpc` uses whichever stream is current, and a wait is not cancelled by `onError`.

### Action

Rejected. The plan's Decisions settle this overlap: each answer is consistent with the stream at its reply, so both orders are valid, and AGENTS.md treats a race in which every outcome is valid as no defect.

## Checks run

- Traced every remaining `Disconnected` consumer (`poll.ts`, `retry`); no other caller relied on a rejected `fetch` being `Disconnected`.
- Checked that removing `main > [role="alert"]` leaves the views' own alerts styled: `.log ~ [role="alert"]` already won on specificity.
- Checked that `.narrow main:has(~ .viewer)` and the viewer sash still see `<main>`, the sash, and `.viewer` as siblings inside `.panes`.
- `npm run lint`: 2 CSS specificity warnings, both in `nav` selectors the change does not touch.
- Tests proved: the implementer reports each new or extended test failing on the base, and the renamed 503 test passing on the base as the old case. Each test reaches the branch it names: the empty-body fallback (`502` text), the retry loop and its error clear (alert count 0 after catch-up), and the alert placement (phone viewer test).
- `docs/functional-requirements.md` unchanged, as the plan's Requirement changes (none) require.

## Verdict

Ready to commit.
