# Review: remove a workspace

## Scope and coverage

Uncommitted changes on `main` at `0cde03c` (12 modified files, nothing staged),
checked against `agents/plans/2026-11-19-003-remove-workspace.md` and the
user's decision that removal is refused while the workspace has any session.

The server side (`removeWorkspace`, `DeleteWorkspace`), the page's handling of
`workspace/removed`, the Sidebar button and dialog, the derived draft list in
`Layout`, and the doc edits match the plan. The two reported deviations hold
up: the draft-recovery effect reading `liveDrafts` changes nothing, because
recovery needs a session in the draft's workspace and removal needs none; the
reused example ID is F3.

Gaps:

- `make live` was not run: the change does not alter how Ox drives Claude Code.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                   |
| -- | -------- | ------------- | -------- | ------ | ------------------------------------------------------- |
| F1 | Medium   | Correctness   | Fix      | Resolved | Remove dialog shows the workspace path in upper case    |
| F2 | Low      | Documentation | Fix      | Resolved | `workspace/remove` doc reads as if it destroys sessions |
| F3 | Low      | Documentation | Rejected | —      | `workspace/remove` example reuses request ID 3          |

## F1. Remove dialog shows the workspace path in upper case

Medium · Correctness · Fix · `cmd/ox/web/src/style.css:126`

The dialogs live inside `<nav>`, so the workspace-row rule `nav p` (11px,
`text-transform: uppercase`, a fixed `height: 1.75rem`, flex, hover
background) also styles the dialog's `<p class="description">` and its
`<p role="alert">`. The confirmation that names what is being removed shows
`THIS REMOVES "/TMP/OXREV/WS" FROM OX.`, misstating a case-sensitive path, and
a long path or error overflows the fixed-height row. The delete-session
dialog has had the same styling since before this change; this change adds a
dialog whose whole job is to name a path.

The e2e assertion passes because `toContainText` reads `textContent`, which
ignores `text-transform`.

### Evidence

In headless Chrome through an isolated dev stack (oxd, relay, and Vite on
spare ports), the description's `innerText` was
`THIS REMOVES "/TMP/OXREV/WS" FROM OX. THE DIRECTORY STAYS ON DISK.` with a
computed height of 28px. The screenshot shows it in small upper case.

### Action

Scope the workspace-row rules in `style.css` to the row itself, such as
`nav > p` for the `nav p` rules (lines 126–130 and the `nav p` selectors on
140–141), so dialog paragraphs keep the `dialog .description` and
`dialog [role="alert"]` styling. This also fixes the delete-session dialog.

Test: in the e2e test "a workspace is removed once it has no sessions", pass
`{ useInnerText: true }` to the `toContainText` check on the dialog's
description, so it compares the rendered text and fails while the path is
upper-cased.

## F2. `workspace/remove` doc reads as if it destroys sessions

Low · Documentation · Fix · `docs/protocol.md:136`

"A workspace that has sessions is refused with `workspace has sessions`; its
sessions are destroyed first." The second clause reads as something `oxd`
does, which contradicts the refusal and the plan's decision. A client has to
destroy them.

### Action

Reword so the client is the actor, e.g. "A workspace that has sessions is
refused with `workspace has sessions`; destroy its sessions first." No test.

## F3. `workspace/remove` example reuses request ID 3

Low · Documentation · Rejected · `docs/protocol.md:140`

The new example uses `"id":3`, which `session/create`'s example also uses.

### Action

Rejected: each example is a self-contained request and reply, and the ID
only pairs them. Renumbering every later example costs more than the
duplicate does.

## Checks run

- `make test`: passed.
- `make e2e`: 16 passed, 3 skipped.
- New tests against the base revision `0cde03c`, in a removed detached
  worktree: `TestSessionLifecycle` fails (no `workspace/remove`); the e2e test
  fails at `expect(remove).toHaveCount(1)`.
- Each condition the change adds, removed one at a time, fails a test:
  the sessions check (`methods_test.go:201`), the store delete (`:239`),
  the `s.workspaces` delete (`:221`), the broadcast (`:216`), the not-found
  reply (`:224`), the empty-ID check (`:213`); in the web client, the
  `liveDrafts` filter in `draft()` (prompt still shown), the Show around the
  trash button (button shown with a session), and the `workspace/removed`
  handler (row stays).
- The `s.closed` check is untested, as is `createWorkspace`'s; the plan did
  not ask for it.
- Headless Chrome through an isolated `make dev`-equivalent stack (built with
  `-race`, own socket, database, and ports): a workspace added on one page
  appeared on a second, removal from the first dropped it from both, and a
  reload of the second did not list it. No race reports.

## Verification 1

Uncommitted changes on `0cde03c` after the implementer's fixes. Checks run:
`make test` passed; `make e2e` 16 passed, 3 skipped; headless Chrome through
an isolated dev stack (`-race` binaries, own socket, database, and ports).

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The row rules are `nav > p` (`style.css:126-130`, `140-141`), so dialog paragraphs fall back to `dialog .description`. In Chrome the description's `innerText` is `This removes "/tmp/oxver/ws" from Ox. …` in its original case, and the workspace row looks as before. The e2e check now passes `useInnerText: true`; with the selectors put back to `nav p` it fails at `workspace.spec.ts:56`. |
| F2 | Resolved | `docs/protocol.md` now reads "refused with `workspace has sessions`; destroy its sessions first." |

The fix also restyles the delete-session dialog's description and error,
which had the same defect. No new findings: no other `<p>` is a direct child
of `<nav>`, and adding and removing a workspace across two pages still works
with no race reports.

## Verdict

Ready to commit.
