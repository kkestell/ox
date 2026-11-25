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

## Copilot

Copilot's review of PR #100 raised four defects in `internal/server/changes.go`. Each is fixed in the uncommitted tree on `fcb3b50`, with a case in `TestListChanges`'s workspace listing that fails with its fix reverted.

| ID | Status | Title                                                        |
| -- | ------ | ------------------------------------------------------------ |
| C1 | Fixed  | Repository diff configuration changes the parsed hunks       |
| C2 | Fixed  | An untracked symlink shows its target's contents             |
| C3 | Fixed  | A file removed from the index but kept on disk shows twice   |
| C4 | Fixed  | An untracked file's added lines can exceed the 1 MiB limit   |

### C1. Repository diff configuration changes the parsed hunks

With `diff.suppressBlankEmpty=true`, git writes a blank context line without its space and the parser drops it, so context and line numbers go wrong. `diff.submodule=log` or `diff` changes a submodule's section in the same way: `log` replaces the `diff --git` section with a `Submodule` summary, so the numstat records and patch sections no longer pair up. The diff now runs with `-c diff.suppressBlankEmpty=false` and `--submodule=short`. Every other diff setting is overridden by a flag the command already passes (`--no-color`, `--no-ext-diff`, `--no-textconv`, `--no-renames`, `--unified=3`, `--relative`, the prefixes), affects only paths the parser reads from numstat (`core.quotePath`), or still produces well-formed hunks (`diff.algorithm`, `diff.indentHeuristic`, `diff.interHunkContext`). `diff.orderFile` orders numstat and patch alike, which a probe confirmed.

Test: the workspace listing sets both in the repository's configuration. `blank.txt` must read `" a"`, `" "`, `"-b"`, `"+B"`, and a staged submodule `inner` must read `+Subproject commit <sha>`. Without `diff.suppressBlankEmpty=false`, `blank.txt` loses its `" "` line. Without `--submodule=short`, the listing fails with `6 numstat records but 5 patch sections`.

### C2. An untracked symlink shows its target's contents

`untrackedText` reads an untracked file with `Lstat` through the directory's `os.Root`, and returns a symlink's target with `Root.Readlink`, as Git records it. Other files still go through `readFile`, which the file viewer uses unchanged.

Test: an untracked `link` to `file.txt` must be one hunk `+file.txt`. Without the fix it lists `file.txt`'s 20 lines.

### C3. A file removed from the index but kept on disk shows twice

After `git rm --cached`, the path was both deleted in the diff and untracked. The listing now finds such paths before the diff: untracked paths that `git diff --cached --diff-filter=D <base>` reports as removed. When there are any, it copies the index into a new temporary directory and runs `git --literal-pathspecs reset -q --pathspec-from-file=- --pathspec-file-nul <base>` on the copy, which sets those paths to the base's version. The diff then runs with `GIT_INDEX_FILE` set to the copy, and those paths are left out of the untracked files. The copy is removed afterwards. The base's blobs already exist, so nothing is written in `.git`. `git add -N` on a copy was rejected because it writes the empty blob into `.git/objects` and fails the whole diff on an unreadable file. `git`'s body moved to `gitCommand` in `worktree.go` so the listing can pass an environment and stdin.

Test: `file.txt` is removed from the index and edited on line 5. It must show once, as `modified`, with one hunk from line 2. Every file beneath `.git` must keep its modification time across the listing. Without the fix, `file.txt` lists as deleted, and again as added.

### C4. An untracked file's added lines can exceed the 1 MiB limit

`added` counts the synthesized diff's size, a `+` and newline per line, and returns `diff is larger than 1 MiB` over `maxFileSize`.

Test: an untracked `many.txt` of 400,000 `a` lines must carry that reason. Without the fix it lists all its lines.

### Checks run

- `make test`: passed.
- Each fix reverted on its own (C1's two overrides separately): `TestListChanges` failed on the case named above, and passes on the final tree.
- `make e2e`: not re-run, since the fixes change only server code and the protocol doc.

## Verification 2

Checked the uncommitted fixes for C1–C4 on `fcb3b50`, in `internal/server/changes.go`, `changes_test.go`, `worktree.go`, and `docs/protocol.md`.

| ID | Status   | Evidence                                                                                                                                                                                                 |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| C1 | Resolved | Removing `-c diff.suppressBlankEmpty=false` fails on `blank.txt`, which loses its `" "` line. Removing `--submodule=short` fails with `6 numstat records but 5 patch sections`.                              |
| C2 | Resolved | Disabling the symlink branch in `untrackedText` fails `TestListChanges` on the workspace listing.                                                                                                       |
| C3 | Resolved | Skipping the restored index fails with `file.txt` listed as `deleted`. The `.git` modification-time check covers the `git reset` on the copied index.                                                      |
| C4 | Resolved | Disabling the size check in `added` fails on `many.txt`.                                                                                                                                                 |

The fixes bring no regressions:

- `git` keeps its behavior through `gitCommand`, with stdin still closed.
- The temporary index is removed on every path after `restoredIndex` creates it.
- `git reset <base> -- <paths>` on `GIT_INDEX_FILE` writes only the copy and its lock in the temporary directory.
- The worktree session's index check still passes.

No new findings.

Checks run:

- `make test`: passed.
- `dprint check`: passed.
- The five removals above, each restored afterwards.
- `make e2e`: not re-run, since the fixes change only server code and the protocol doc, which `make test` observes.
- `make live`: not run, since the change does not alter how Ox drives Claude Code.

## Verdict

Ready to commit. F1, F2, and C1–C4 are resolved.
