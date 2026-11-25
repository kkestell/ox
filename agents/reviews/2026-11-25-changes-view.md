# Changes view review

## Scope and coverage

Uncommitted changes on `changes-view` against base `fe8ee7f` (the merge base with `origin/main`, also `HEAD`), checked against `agents/plans/2026-11-25-changes-view.md`. The tree reviewed holds the 15 modified files and the untracked `changes.go`, `changes_test.go`, `Changes.tsx`, and the plan.

The plan deviates in one place: the diff also runs with `-c diff.autoRefreshIndex=false`. This is confirmed as needed. With the flag removed, `TestListChanges` fails with `listing changes rewrote the index`, so `--no-optional-locks` alone does not keep *Ox's own files*. With the flag, a stat-dirty file is still compared by content and left out of the listing.

The requirements diff matches the plan's Requirement changes word for word. Open `bug` issue #33 does not touch this change.

Gaps:

- `/ox-work`'s Tests proved and Checks were not passed on. The checks below were run here instead.

## Summary

| ID | Severity | Category     | Action   | Status   | Title                                                         |
| -- | -------- | ------------ | -------- | -------- | ------------------------------------------------------------- |
| F1 | Medium   | Requirements | Fix      | Resolved | A repository with no commits shows a Git error                |
| F2 | Low      | Testing      | Fix      | Resolved | Two parser branches have no test                              |
| F3 | Low      | Simplicity   | Rejected | —        | Stat-dirty files are hashed again on every poll               |

## F1. A repository with no commits shows a Git error

Medium · Requirements · Fix · `internal/server/changes.go:107`

In a repository that has no commits yet, such as a project just made with `git init`, the Changes tab shows `list changes: fatal: Needed a single revision` instead of the files. *Changes view* says the tab lists untracked files and says why only when the directory is not in a Git repository. In this state every file is new and should be listed as added.

`git rev-parse --verify HEAD` in the workspace exits 128 when `HEAD` is unborn, and the error goes straight to the page.

### Evidence

In `make dev`, a workspace at a fresh `git init` directory holding `notes.txt` showed only the paragraph `list changes: fatal: Needed a single revision` in its Changes tab. A probe in such a repository confirmed that `git rev-parse --verify -q HEAD` exits 1 with no output. The same probe confirmed that `git diff --numstat -z -p … 4b825dc… --` against the empty tree lists a staged file as a new file.

### Action

When the workspace's `HEAD` is unborn, use the empty tree as the base: run `rev-parse --verify -q HEAD` and treat exit 1 with no output as unborn. Take the empty tree from `git hash-object -t tree /dev/null` so SHA-256 repositories work too. Skip `merge-base` in that case. A worktree session cannot exist without a commit, so only the workspace directory and the sessions in it reach this path. Add a case to `TestListChanges`: a workspace in a fresh `git init` directory with one staged file and one untracked file lists both as `added` with one hunk from line 1.

## F2. Two parser branches have no test

Low · Testing · Fix · `internal/server/changes.go:116`, `internal/server/changes.go:218`

Two branches the change adds have no test:

- Dropping hunk lines that begin with `\` ("No newline at end of file"). Without that branch, the line would show as a context row and shift the line numbers after it. Every tracked file in `TestListChanges` and in the e2e `gitRepo` ends with a newline.
- The `no commit in common with the workspace's branch` error on `merge-base` exit 1.

### Evidence

With the `line[0] == '\\'` case removed, `go test ./internal/server -run 'TestListChanges|TestWorkspaceFiles'` passes. No test anywhere contains `no commit in common`.

### Action

In `TestListChanges`, commit a file without a final newline in the worktree, then edit its last line. Assert its hunk lines hold no `\` entry and end `-old`, `+new`. Add a worktree session that checks out an orphan branch with one commit (`git checkout -q --orphan other`, then commit), and assert `-32000` `no commit in common with the workspace's branch`.

## F3. Stat-dirty files are hashed again on every poll

Low · Simplicity · Rejected · `internal/server/changes.go:85`

With `diff.autoRefreshIndex=false`, a file whose stat changed but whose content did not is hashed again on each one-second poll until something else refreshes the index.

### Action

Rejected: an unmeasured scaling cost. The flag is what keeps *Ox's own files*, and any `git status` that Claude or the user runs refreshes the index.

## Checks run

- `make test`: passed.
- `make e2e`: 185 passed.
- `go test ./internal/server -run TestListChanges` with `-c diff.autoRefreshIndex=false` removed: failed with `listing changes rewrote the index`. Passed with it restored.
- Spot check: with the `\` line case removed, `TestListChanges` passed (F2).
- `make dev` in this worktree, headless Chrome at 1400×900. A draft on this worktree's directory showed the Changes tab with branch `changes-view`, totals, cards, collapsible sections, numbered and highlighted `ins`/`del` rows, and new files that appeared on the next poll. A draft on a fresh `git init` directory reproduced F1.
- `make live`: not run, since the change does not alter how Ox drives Claude Code.

## Verification 1

Checked the uncommitted tree on base `fe8ee7f` after the fixes. Since the review, only `internal/server/changes.go`, `internal/server/changes_test.go`, and `docs/protocol.md` changed. `forkPoint` now takes the base: the empty tree from `git hash-object -t tree` when the workspace's `HEAD` has no commit (`rev-parse --verify -q` exits 1 with no output), the `no commit in common` error when `merge-base` answers no, and the merge base otherwise. `docs/protocol.md` states the empty-tree base.

| ID | Status   | Evidence                                                                                                                                                                                |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `TestListChanges` lists a fresh repository's staged and untracked files as `added`. With the unborn branch in `forkPoint` disabled it fails with `list changes:`.                     |
| F2 | Resolved | `TestListChanges` covers `tail.txt` without a final newline and an orphan-branch session. Removing the `\` case fails on `tail.txt`, and disabling the no-common-base branch fails on the orphan. |

No new findings.

Checks run:

- `make test`: passed.
- `dprint check`: passed.
- The three removals above, each restored afterwards. `TestListChanges` passes on the final tree.
- `make e2e` and the `make dev` check: not re-run, since the fixes change only Go server code and the protocol doc, which `make test` observes. Both passed on the reviewed tree.
- `make live`: not run, since the change does not alter how Ox drives Claude Code.

## Verdict

Ready to commit. F1 and F2 are resolved.
