# Nested child sessions

## Scope and coverage

Uncommitted changes on base `3a2b3d2`, reviewed against
`agents/plans/2026-11-21-008-nested-child-sessions.md`: the configuration's
`max_child_depth`, the depth check in `create_session`, the archive and
unarchive walks, the sidebar tree and archive gate, the tests, and the edits to
`docs/functional-requirements.md`, `docs/protocol.md`, and
`docs/architecture.md`.

The four requirement edits ("Session order", "Claude can start child sessions",
"Archiving a session", "Configuration checks") match the plan's Requirement
changes word for word, and no other requirement changed.

The implementer's departures hold up. Sending the grandchild's
`approve Bash {}` as a user prompt makes it busy the same way a first message
would. The `first` row class is needed: a later sibling directly below its
previous sibling also has `--rise` 1, so rise alone cannot place the 4px gap
below a parent's dot. The guide-geometry e2e test covers that class and the
curve. TestArchive restarts `k` and `g` after unarchiving so the later archive
has processes to stop and `parked` updates to wait for. TestCreateSession
deletes in reverse order because the grandchild has to go before its parent.

Headless Chrome through `make dev` is not required. The sidebar change renders
from the same `workspace/list` and `session/created` data and changes nothing
about how the page reaches `oxd`.

Gaps:

- `make e2e` and `make live` were taken from the implementer's report. Only the
  child-session e2e test was rerun here, as a mutation check.

## Summary

| ID | Severity | Category  | Action | Status   | Title                                         |
| -- | -------- | --------- | ------ | -------- | --------------------------------------------- |
| F1 | Low      | Ownership | Fix    | Resolved | Depth check walks a deleted caller's ancestry |

## F1. Depth check walks a deleted caller's ancestry

Low · Ownership · Fix · Resolved · `internal/server/mcp.go:340`

`createSession` runs the depth check on the MCP call goroutine, using the
`caller` that `handleMCP` resolved earlier. `depthLocked` relies on "every
ancestor is a member while its descendant is". That only holds while the
caller is itself a member, and nothing on the call goroutine guarantees that.
If the caller and then its parent are deleted between `handleMCP`'s lookup and
the depth check, `s.sessions[ParentID]` is nil and `depthLocked` dereferences
it. The nil dereference panics and takes down `oxd`.

The window is tiny. But the defect comes from who owns the read: the check
depends on the caller's membership, and only the caller's goroutine guarantees
membership. `addChild` already relies on exactly that guarantee ("The parent is
a member while its goroutine runs").

### Evidence

A temporary in-package probe created a parent and a child, captured the
child's `*session`, destroyed the child and then the parent through
`session/destroy`, and called `createSession` with the captured caller. It
panicked with `nil pointer dereference` in `(*Server).depthLocked`
(`server.go:193`), called from `mcp.go:341`. The probe was removed afterwards.

### Action

Move the depth check into `addChild`, under `s.mu` beside the shutdown and
archived checks. That runs on the caller's goroutine, so the caller and all its
ancestors are members. Then:

- drop the call-goroutine block and its "Ancestry is fixed at creation" comment
- move the depth refusal in `docs/protocol.md`'s `create_session` refusal list
  to where `addChild` now checks it
- add depth to `docs/architecture.md`'s list of what the parent's goroutine
  checks under `Server.mu`

A caller deleted before its goroutine runs the operation then gets
`session not found` from the failed `post`, which the protocol already lists.

Test, server layer, in `internal/server/mcp_test.go`: create a parent and a
child, capture the child's session, destroy the child and then the parent, and
call `createSession` with the captured caller. Expect the `session not found`
refusal. It fails today with the panic above. `createSession` is the tool's
handler, and the interleaving cannot be reached through the socket without
injected timing.

## Checks run

- `make test`: passed.
- Mutation: limiting `archive`'s walk to the first level failed `TestArchive`
  (`mcp_test.go:750`, no `-32002`).
- Mutation: limiting `unarchive`'s walk to direct children failed `TestArchive`
  (`mcp_test.go:857`, no `session/unarchived` for `g`).
- Mutation: `archiveBlocked` checking `childrenOf` instead of `descendantsOf`
  failed "a child session shows beneath its parent with its title"
  (`session.spec.ts:1023`, Archive not disabled).
- Probe for F1, described above, then removed.
- Every mutation was reverted, and the working tree matches the reviewed state.

## Verification 1

Checked the working tree on base `3a2b3d2`, which adds the fix for F1. The
uncommitted edits under `.claude/skills/` are outside this change and were not
reviewed.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The depth check is now in `addChild`, under `s.mu` on the caller's goroutine, and the call-goroutine check is gone. `TestCreateSessionByDeletedCaller` passes. With the old call-goroutine check put back, it panics in `depthLocked` while holding `s.mu` and then times out in cleanup. The order of refusals in `docs/protocol.md` and the list of checks in `docs/architecture.md` match `addChild`. |

No new findings. The fix moves the depth refusal after the name, shutdown, and
archived checks. The protocol's order of refusals states that order, and no
requirement depends on it.

Checks: `make test` passed on the final tree. `TestCreateSessionByDeletedCaller`
was run with and without the fix, as above. `make e2e` (41/41) is from the
implementer's report on the final tree.

## Verdict

`Ready to commit`: F1 is resolved.
