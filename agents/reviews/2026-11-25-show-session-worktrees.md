# Review: list_sessions shows worktrees

## Scope and coverage

Uncommitted changes in the `show-session-worktrees` worktree against base `6a94744` (`git merge-base origin/main HEAD`), checked against `agents/plans/2026-11-25-show-session-worktrees.md`. Files: `internal/server/mcp.go`, `internal/server/methods_test.go`, `internal/server/worktree_test.go`, `docs/protocol.md`, `docs/functional-requirements.md`. Open `bug` issues (#33, #104, #108, #109, #110, #111, #118) do not touch this code.

Gaps:

- `make live` and `make e2e` were not run: the change alters MCP tool output text only, not how Ox drives Claude Code or what the page does.

## Summary

No findings.

## Checks run

- `go test ./internal/server -run 'TestSendMessage|TestWorktreeSession'` passes.
- With `internal/server/mcp.go` restored to base (restored afterward), both tests fail: `TestSendMessage` sees no `directory`, and `TestWorktreeSession` sees a listing without `directory` or `worktree`.
- The requirements diff matches the plan's approved text word for word.
- `directory` and `worktree` are read on the session goroutine from fields fixed at creation, and no other caller of the sender's workspace in the header remains. The protocol's "Workspaces and sessions" section defines both fields and the no-worktree rule that the new link points to.

## Verdict

Ready to commit.
