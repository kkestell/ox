# Review: split internal/server files by concern

## Scope and coverage

Reviewed the uncommitted changes in the `split-server-files` worktree against base `9abac76` (`git merge-base origin/main HEAD`, also `HEAD`), checked against `agents/plans/2026-11-27-split-server-files.md` and issue #204. The working tree adds `archive.go`, `create.go`, `report.go`, `title.go`, and `transcript.go` under `internal/server`, removes the moved code from `mcp.go`, `methods.go`, and `session.go`, and adds five Source Map entries to `AGENTS.md`.

The change touches no state owner's behavior, protocol, persistence, or harness driving, so this session reviewed it alone, with no concern reviewers.

Gaps: none material.

## Summary

No findings.

The moved code is verbatim. A `go/ast` comparison of every top-level declaration in the package's non-test files, base against working tree, with doc comments included, found every declaration present in both, and byte-identical except for the seven functions the plan names: `readyLocked`, `familyChangedLocked`, `takeReports`, `report`, `shorten`, `generateTitle`, and `cut`, plus the new `firstRunes`. Every line the diff removes from `mcp.go`, `methods.go`, and `session.go` appears in the new files, apart from the tidied lines in those functions. Each declaration landed in the file the plan's implementation steps name. `childName` and `errNotWorkspace` stay in `mcp.go`, `codedError` and `errWorkspaceNotFound` in `methods.go`, and the session error block and `newSession` in `session.go`.

The tidies preserve behavior:

- `firstRunes` uses the same `[]rune` conversion as the code it replaces. Its count matches `utf8.RuneCountInString` for invalid UTF-8 as well, because both count one rune per invalid byte. `cut` returns the same text on both branches.
- `report` reaches the cut only when `n > reportRunes`, so `cut(text, reportRunes)` always adds the ellipsis. The format string drops its literal `…`, so the output is unchanged.
- `shorten` tests `titleRunes` and cuts to `titleRunes-1`, as before. `generateTitle` cuts to `titleInputRunes` only when the prompt is longer.
- `readyLocked` returns `nil, false` on both early exits. Its final expression is unchanged, and it now also returns the reporters it already computed. `takeReports` holds `Server.mu` across the call and the loop, so the reporters it ranges over are the list `reportersLocked` would return again. `familyChangedLocked`'s split nil check runs nothing after the `if`, so the early return matches the old `&&`.
- `docs/architecture.md` ("Delivery") still names `readyLocked`, `reportersLocked`, and `familyChangedLocked` correctly.

The `AGENTS.md` Source Map entries match the plan's text, sit in alphabetical order, and keep the comment column aligned. No other document names the files' old contents.

The issue's suggestion to group `Server.mu`-guarded fields is declined in the plan's Decisions, with its reason. The rest of #204 is delivered.

## Checks run

- Declaration-by-declaration comparison, base against working tree: identical apart from the named tidies.
- Removed-line accounting: every removed line is present in the new files, except the tidied lines.
- `gofmt -l`, `go vet ./internal/server/`, `golangci-lint run ./internal/server/...`: clean.
- `make test` (lint, `go test -race ./...`, Node suites): pass.
- Mutation: `firstRunes` cutting one rune short fails `TestReadSession`, `TestChildReports`, and `TestSessionLabel`. `takeReports` ranging over no reporters fails `TestChildReports` and `TestRefusedDeleteKeepsReports`. Both mutations were reverted.

## Verdict

Ready to commit.
