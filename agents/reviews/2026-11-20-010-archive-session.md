# Review: Archive a session

## Scope and coverage

Reviewed the uncommitted changes on `main` at base `a42b498`, against the plan
`agents/plans/2026-11-20-010-archive-session.md`, with the user's approval of
its two Check your assumptions entries in their planned form and its
Requirement changes text. The working tree held the 17 modified files of this
change and the untracked plan; the child-row guide lines in `style.css` were
committed in `a42b498` and are outside this review.

Covered: the store's `archived` table and `SetArchived`; the session's
`archived` flag, `hold`, `Server.archive`, `Server.unarchive`, and
`setArchivedLocked`; the `startForInput`, `configureSession`, `list_sessions`,
and `create_session` refusals; `workspace/list`; the page's store, session
control, `SessionView`, `Sidebar` menu and filtering, and styles; the e2e
specs; `docs/functional-requirements.md`, `docs/protocol.md`, and
`docs/architecture.md`.

Gaps:

- How the menu and archived rows look is left to the user.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                               |
| -- | -------- | ------------ | -------- | ------ | --------------------------------------------------- |
| F1 | Low      | Testing      | Fix      | Resolved | The menu's viewport clamp has no test               |
| F2 | Low      | Testing      | Rejected | —      | The `closed` check in `setArchivedLocked` has no test |
| F3 | Low      | Requirements | Rejected | —      | An open workspace menu covers the next row's ⋯ button |
| F4 | Low      | Intent       | Rejected | —      | The implementer wrote three disabled-item reasons   |

## F1. The menu's viewport clamp has no test

Low · Testing · Fix · `cmd/ox/web/src/Sidebar.tsx:120`

`Menu` moves itself so the whole menu stays inside the window. The plan places
the menu at the ⋯ button's corner or the pointer and does not mention the
clamp, but without it a menu opened on a row near the bottom of the window,
which a long session list makes ordinary, runs off-screen and its last item
cannot be reached. The clamp earns its place. No test fails if it is removed.

### Evidence

No spec in `cmd/ox/web/e2e/` sizes the viewport or measures the menu's box
(`grep` for `setViewportSize`, `boundingBox`, and `innerHeight` finds
nothing).

### Action

Keep the clamp and add an e2e check in `e2e/session.spec.ts`, in "an archived
session is read-only and hidden until shown": shrink the viewport with
`page.setViewportSize` so a session row sits near its bottom edge, open the
row's menu by right-clicking near the row's bottom, and check that the menu's
bounding box ends inside the viewport. Confirm it fails with the two
`menu.style` lines removed.

## F2. The `closed` check in `setArchivedLocked` has no test

Low · Testing · Rejected · `internal/server/session.go:266`

`archiveSession` already refuses once the daemon is shutting down, so the
check in `setArchivedLocked` is reached only when `Close` begins between that
check and the flag write.

### Action

Rejected. The plan calls for the check, beside the same check in
`create_session`. Every interleaving it covers ends validly: an archive that
holds a goroutine completes before `Close` can return, and without the check
an unarchive racing a finished `Close` fails on the closed store. The check
only gives that refusal its usual message. Forcing the interleaving needs a
held goroutine and an injected `Close`, which costs more than the message it
protects.

## F3. An open workspace menu covers the next row's ⋯ button

Low · Requirements · Rejected · `cmd/ox/web/src/Sidebar.tsx:106`

The workspace menu opens below its ⋯ button and overlaps the first session
row, including that row's ⋯ button.

### Action

Rejected. A menu covers what lies under it while it is open. Light dismiss
and Escape close it, and the plan places it at the button's bottom-right
corner.

## F4. The implementer wrote three disabled-item reasons

Low · Intent · Rejected · `cmd/ox/web/src/Sidebar.tsx:168`

Archive shows "Wait until it is idle" or "Wait until it and its child
sessions are idle" while a member is busy. Archive and Unarchive show "Not
connected" while the page is not ready. The plan gave no text for these.

### Action

Rejected. The approved first Check your assumptions entry requires a one-line
reason for an Archive refused as busy, and *Lost connection* in the plan
requires these items disabled while the page is not ready. The text follows
that decision and the other reasons' style, and leaves nothing for the user
to decide.

## Checks run

- `make test`: passed on the reviewed tree.
- Conditions removed one at a time, each failing `TestArchive`, then
  restored: the `list_sessions` filter, the archived-caller refusal in
  `create_session`, the parent check in `unarchive`, the archived refusal in
  `configureSession`, and the busy check in `hold`.
- `docs/functional-requirements.md` compared with the plan's Requirement
  changes: identical, word for word.
- From `/ox-work`: `make e2e` 38 passed, 3 skipped; headless Chrome on a
  separate dev stack passed. `make live` was not run; the change drives Claude
  only through the existing stop path, so it is not required.

## Verification 1

Checked the uncommitted tree on base `a42b498` after the fix for F1. The only
change since the review is a check in `e2e/session.spec.ts` ("an archived
session is read-only and hidden until shown") that shrinks the viewport so the
archived row ends near its bottom, right-clicks near the row's bottom, and
expects the menu to end at the viewport's bottom edge. The source files are
unchanged since the review.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | With the clamp's `menu.style.top` line removed, the test fails at `session.spec.ts:1037` (menu ends 32px below the viewport's edge); restored, it passes. |

No new findings.

Checks run: `make test` passed; the single e2e test run with and without the
clamp, as above. From `/ox-work`'s fix: `make test` and `make e2e` passed.

## Verdict

`Ready to commit`. F1 is resolved; F2, F3, and F4 are rejected.
