# Review: bind a child's message to its parent to the turn that made it

## Scope and coverage

Reviewed the uncommitted changes in the `child-report-turn` worktree on base
`a38aced` (`git merge-base origin/main HEAD`, also `HEAD`):
`internal/server/session.go`, `internal/server/mcp.go`,
`internal/server/mcp_test.go`, `docs/architecture.md`, and `docs/protocol.md`,
against
[`agents/plans/2026-11-24-child-report-turn.md`](../plans/2026-11-24-child-report-turn.md).
Read `AGENTS.md`, the **Child reports** requirement, the open `bug` issues, the
Sessions passage of `docs/architecture.md`, and the Child reports section of
`docs/protocol.md`. Traced `sendMessage`, `ask`, `free`, `post`, `report`,
`newSession`, and the delivery path.

Gaps:

- `/ox-work`'s final response did not reach this review, so the checks below
  were run here instead of taken from it.

## Summary

No findings.

The change follows the plan's Decisions. `toParent.turn` is never nil: it is
built in `newSession` and replaced at `result` and `exited`. `sendMessage` takes
the caller's turn on the caller's goroutine before it waits on the parent, and
marks only that turn after acceptance. The turn is read and written only on the
child's goroutine. The added `ask` runs on the MCP call goroutine, so it adds no
wait between session goroutines. **Child reports** holds as written, and
`docs/functional-requirements.md` is unchanged, as the plan's Requirement changes
say. The document edits state the new fact in the documents that own it. The
change resolves #71.

The new case in `TestChildReports` is deterministic. The gate closes only after
`ask` has taken the first turn, and the late post completes before the approval
turn's `tool_result`, because the test waits for the `send_message` reply before
it approves. Counting the parent's history by reports instead of prompts keeps
the assertion independent of how the new case's two reports are batched.

## Checks run

- `go test -race -count=3 -run 'TestChildReports|TestChildStartHoldsUpNoOtherSession' ./internal/server/`:
  passed.
- New case with `mcp.go` and `session.go` restored to base: failed at
  `mcp_test.go:1212`, with the approval turn's report missing.
- Removing the `caller.free()` nil return in `sendMessage`: `TestChildReports`
  failed at `mcp_test.go:1155` (the idle child's next report).
- Removing the turn replacement at `result`: `TestChildReports` failed at
  `mcp_test.go:1212`.
- `make test`: passed.
- `make e2e`: 160 passed, 4 skipped.
- `dprint check`: clean.
- `make live`: not required. The change does not alter how Ox drives Claude
  Code.

## Verdict

Ready to commit.
