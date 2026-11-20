# Review: show when a session is working in the background

## Scope and coverage

The uncommitted changes on base `847430b`, against
`agents/plans/2026-11-20-012-background-state.md`: the `background` state in
`internal/agent`, its derivation and publishing in `internal/claude`, the fake
harness's `background <milliseconds>` prompt, `hold` in
`internal/server/session.go`, the web client's `settled` helper and status
ring, the tests, and the doc changes. Traced every reader of the published
state: `Snapshot`, `hold`, `list_sessions`, `stateLine`, the page's
`canConfigure`, archive check, status dot, and `SessionView`'s `running()`.

Gaps:

- `make e2e` and `make live` were not rerun here; the implementer reports
  both passed on the final tree, including the new e2e and live cases.

## Summary

| ID | Severity | Category      | Action | Status | Title                                              |
| -- | -------- | ------------- | ------ | ------ | -------------------------------------------------- |
| F1 | Low      | Simplicity    | Fix    | Resolved | `Send` publishes `published()` where it is always `running` |
| F2 | Low      | Documentation | Fix    | Resolved | Archive rule formats `idle` and `background` differently |

## F1. `Send` publishes `published()` where it is always `running`

Low · Simplicity · Fix · Resolved · `internal/claude/prompt.go:47`

`Send` sets `a.state = agent.StateRunning` and then publishes
`a.published()`. `published()` returns `background` only when `a.state` is
`idle`, so here it always returns `running`. The call suggests that `Send`
could publish `background`, which it cannot.

### Evidence

`published()` (`internal/claude/prompt.go:192`) returns `a.state` unless
`a.state == agent.StateIdle`; the line above sets it to `running`.

### Action

Publish `a.state` in `Send`, as before the change. No test: the behavior is
unchanged.

## F2. Archive rule formats `idle` and `background` differently

Low · Documentation · Fix · Resolved · `docs/protocol.md:403`

The `session/archive` rule reads "not idle, `background`, or parked": one
state in code format, two in prose.

### Evidence

`docs/protocol.md:403-404`.

### Action

Write it as "that is not `idle`, `background`, or `parked`", and rewrap the
paragraph and the `session/interrupt` paragraph at line 368, whose edited
line runs past the file's wrap width.

## Checks run

- `make test`: passed.
- Mutation spot checks, each reverted:
  - `hold` without `StateBackground`: `TestArchive` fails with
    `session is busy`.
  - `Snapshot` returning `a.state`: `TestBackgroundTasksAfterTurn` fails with
    `snapshot state "idle", want background`.
- `make e2e` (40 passed) and `make live` (passed, with the background case):
  reported by the implementer on the final tree.
- Headless Chrome through `make dev`: not required, since the change adds no
  new path from the page to `oxd`.

The implementer's open choices are accepted: the fake's `step` constant and
`after(d, event)`, the extra cases in `TestSessionLifecycle` and
`TestBackgroundTasksAfterTurn`, `background 60000` in
`TestListSessionsCapturesLabelWithOwnerRead`, the `idle` or `background`
release wording in `docs/protocol.md`, and the 1.5px ring.

## Verification 1

Checked the working tree on base `847430b` after the fixes. Since the review,
only `internal/claude/prompt.go` and `docs/protocol.md` changed.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `Send` publishes `a.state` (`internal/claude/prompt.go:47`). No test needed: the published value is unchanged. |
| F2 | Resolved | The archive rule reads "not `idle`, `background`, or `parked`", and both paragraphs are rewrapped (`docs/protocol.md:364-371`, `400-409`). |

No new findings.

Checks: `make test` passed. The implementer reports `make e2e` passed (40)
on the final tree. `make live` was not rerun, and it is not required: F1
publishes the same value as before, and the earlier `make live` run covers the
rest of the change.

## Verdict

`Ready to commit`: F1 and F2 are resolved.
