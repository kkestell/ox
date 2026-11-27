# Review: copy `.worktreeinclude` files into new worktrees

## Scope and coverage

Base `origin/main` merge-base (HEAD `6f780b0`), uncommitted working tree, checked against `agents/plans/2026-11-27-worktreeinclude.md`. Reviewed `internal/server/worktree.go`, `internal/server/worktree_test.go`, and the **Worktrees** requirement diff, which matches the plan's New text word for word.

Gaps: no `/ox-work` final response was supplied, so Tests proved and Checks were taken from my own runs. `make e2e`, `make live`, and the headless Chrome check do not observe the change (server-side file copy only).

## Summary

| ID | Severity | Category      | Action | Status | Title                                                  |
| -- | -------- | ------------- | ------ | ------ | ------------------------------------------------------ |
| F1 | Low      | Testing       | Fix    | Resolved   | The destination-within-worktree guard has no test      |
| F2 | Low      | Documentation | Fix    | Resolved   | Architecture's creation steps omit the include copy    |

## F1. The destination-within-worktree guard has no test

Low · Testing · Fix · `internal/server/worktree.go:143`

`copyFile` fails when a destination parent resolves outside the worktree. Removing the `EvalSymlinks`/`within` check leaves every test green, so nothing protects this branch.

### Evidence

No test builds a worktree whose HEAD has a symlink where the main checkout has a directory. Reading the tests and running `go test ./internal/server -run Worktree` (passes) show none reaches the branch.

### Action

Add a row to `TestWorktreeCreationFails`: commit a symlink `data -> <temp dir>`, then replace it in the main checkout with a real directory holding an ignored file `data/x` listed in `.worktreeinclude`. Assert the error contains "outside the worktree", `nothingLeft`, and that the temp dir stays empty. Layer: server.

## F2. Architecture's creation steps omit the include copy

Low · Documentation · Fix · `docs/architecture.md:268`

Step 2 of the creation sequence reads "create the git worktree ... and verify the workspace's copy exists inside it". Creation now also copies the files `.worktreeinclude` lists, and a copy failure discards the worktree. The plan allowed this edit because the document describes what creation does.

### Action

Extend step 2 with "and copy the ignored files the repository's `.worktreeinclude` lists". Run `make docs`.

## Checks run

- `go test ./internal/server -run Worktree`: pass.
- Probe: `git ls-files -z --others --ignored --exclude-from=.worktreeinclude` lists files inside an ignored directory and a symlink inside it (`nm/l`); `copyFile` skips the symlink through `Lstat`.
- Not run: `make e2e`, `make live`, headless Chrome; none observes the change.

## Verification 1

Working tree after the fixes, uncommitted. Ran `go test ./internal/server` (pass), `make docs` (no further changes).

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | New row "copy through a symlink" fails with the `within` guard disabled and passes with it |
| F2 | Resolved | `docs/architecture.md` step 2 now names the `.worktreeinclude` copy |

No new findings.

## Verdict

`Ready to commit`.
