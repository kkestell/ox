# Changes counts, paths, and file selection

## Scope and coverage

Uncommitted changes in the `changes-counts-and-paths` worktree on base
`f0be2569` (`git merge-base origin/main HEAD`; `HEAD` is `c776fff`), against
`agents/plans/2026-11-26-changes-counts-and-paths.md`, which resolves #149
(audit findings F6, F14, F15, F16, F21, F28 in `agents/audits/2026-11-26-main.md`).
Reviewed in this session without concern reviewers: the change touches no state
owner in `oxd`, no persistence or harness driving, and adds one optional
protocol field to a read-only poll.

The implementer's three notes:

- **The poll-scroll e2e test passes without the change.** Confirmed, and it
  also passes with the `applied` guard disabled. See F1.
- **The go.mod/go.sum diff also bumps x/sync.** Correct. `golang.org/x/text`
  v0.44.0 requires `golang.org/x/sync` v0.25.0, which minimum version selection
  picks over libc's v0.24.0. `go mod tidy` on the tree reproduces the exact
  go.mod and go.sum.
- **`readFile` was split into `openRegular`, `text`, and `isBinary`.** Sound.
  `untrackedText` needs to open the file once, keep the first `maxFileSize`+1
  bytes, and read on to count lines, so it reuses `openRegular` and `text`. It
  checks `isBinary` before `text` so a binary file over 1 MiB gets no counts, as
  Git's `-` would give. `readFile`'s behavior is unchanged. For an untracked
  binary file over 1 MiB the reason changes from `file is larger than 1 MiB` to
  `file is binary`, which matches Git and the **Change diffs** requirement.

Gaps:

- F21 was checked on darwin only. The non-darwin branch of
  `TestChangedPathsMatchFolderNames` did not run.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                       |
| -- | -------- | -------- | ------ | -------- | ----------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix    | Resolved | No test protects the reveal's `applied` guard               |
| F2 | Low      | Testing  | Fix    | Resolved | No test merges an untracked decomposed spelling on macOS    |
| F3 | Low      | Testing  | Fix    | Resolved | `TestListChanges` failures print counts as pointer addresses |

## F1. No test protects the reveal's `applied` guard

Medium · Testing · Fix · `cmd/ox/web/src/Changes.tsx:135`, `cmd/ox/web/e2e/workspace.spec.ts:708`

The new clocked spec "a poll keeps the Changes tab's scroll after a reveal"
observes nothing this change adds. A Solid 2.0 effect runs its apply only when
its compute returns a different value. A poll that relists the revealed file
makes the compute return the same reveal object, so the apply does not run
again, whether `applied` exists or not.

What `applied` guards is the reveal coming back after the compute returned
`undefined`: a poll whose listing lacks the file, or that fails, followed by one
that holds it again. Without the guard, the view jumps back to the revealed card
after the user has scrolled away. The behavior is right. No test keeps it.

### Evidence

- With the pre-change effect (`() => props.reveal`), the spec passes. "a file
  in the Changes list opens the Changes tab at its card" fails, as it should.
- With the guard disabled (`reveal === applied && Math.random() > 2`), both
  specs pass.
- A probe version of the spec that deletes `file-19.txt` and runs the clock one
  second, then writes the file back and runs it again before the final
  assertion, passes on the tree and fails with the guard disabled
  (`expect(locator).toBeInViewport() failed` for `file-00.txt`).

### Action

Extend the spec at `workspace.spec.ts:708`. After scrolling to `file-00.txt`:
`rmSync` the revealed `file-19.txt`, `page.clock.runFor(1000)`, and expect its
card to have count 0. Then write it back, run the clock again, and expect its
card to have count 1 and `file-00.txt` to be in view. Rename the spec to say
what it guards, for example "a revealed file that leaves and returns keeps the
Changes tab's scroll". Prove that it fails with the guard removed.

## F2. No test merges an untracked decomposed spelling on macOS

Low · Testing · Fix · `internal/server/changes.go:141`

The plan's F21 decision normalizes the untracked path before the F14 merge.
With `core.precomposeUnicode=false`, Git on macOS reports an edited tracked file
as both a composed numstat path and a decomposed untracked path. The merge
lists the file once only because it keys on `localName(path)`. No test runs
that case. Keying on the raw `path` passes every test.

### Evidence

- A probe repository on macOS: a file written decomposed, committed with
  `core.precomposeUnicode=true`, edited, then `core.precomposeUnicode=false`.
  `git ls-files --others` gives `café.txt`, and
  `git diff --numstat -z HEAD` gives `café.txt`.
- With `!listed[localName(path)]` changed to `!listed[path]`,
  `TestListChanges`, `TestListChangesStagedDuringListing`, and
  `TestChangedPathsMatchFolderNames` all pass. The same file would then be
  listed twice, as `modified` and `added`, under one path.

### Action

In `TestChangedPathsMatchFolderNames`, on darwin, set
`core.precomposeUnicode=false` in the repository after the commit, before
listing. Expect `list_changes` to list the edited file once, composed and
`modified`, with the folder names still matching. Prove that it fails with the
key changed to `path`.

## F3. `TestListChanges` failures print counts as pointer addresses

Low · Testing · Fix · `internal/server/changes_test.go:248`

`changedFile.Counts` is a `*lineCounts`, so the `%+v` failure messages in
`TestListChanges` and the new tests print `Counts:0x10a85fae71c0`. A wrong count
shows as two lists that differ only in addresses, and the message hides which
file's counts are wrong and what they were.

### Evidence

The mutation that stops `untrackedText` counting past the first 1 MiB failed
`changes_test.go:248` with two identical-looking lists whose only differences
were the `Counts` addresses.

### Action

Report the mismatches in `changes_test.go` as JSON, the form the page reads, with
a small helper used by each `%+v` that prints `changedFile` values. Lines 165,
169, 174, 179, 248, 293, and 330 do this today. Comparisons stay
`reflect.DeepEqual`. No new test is needed.

## Checks run

- `make test`: passed (golangci-lint 0 issues, Biome and tsc clean,
  `go test -race ./...`, Node suites 48/48).
- `go mod tidy`: no change to go.mod or go.sum.
- `make docs`: no change.
- Focused e2e, `workspace.spec.ts -g "Changes|reveal|poll keeps"`: 14 passed.
- `make e2e`: 219 passed.
- Mutations, each restored from a copy:
  - Removed the F14 merge (`!listed[...]`): `TestListChangesStagedDuringListing` fails.
  - `localName` returns the name unchanged: `TestChangedPathsMatchFolderNames` fails (folder lists NFD, changes list NFC).
  - Count only the first 1 MiB of an untracked file: `TestListChanges` fails.
  - Removed `isBinary(head)` from `untrackedText`: `TestListChanges` fails.
  - Last line without a newline not counted: `TestListChanges` fails.
  - Untracked symlink without counts: `TestListChanges` fails.
  - Merge keyed on the raw untracked path: no test fails (F2).
  - Pre-change reveal effect: the session-switch reveal spec fails, and the poll-scroll spec passes (F1).
  - `applied` guard disabled: no test fails (F1).
- `make live` was skipped: the change does not alter how Ox drives Claude Code.
- A `make dev` check in headless Chrome was skipped: the page reaches `oxd` the same way.

## Verification 1

The working tree after the fixes, which changed only `internal/server/changes_test.go`
and `cmd/ox/web/e2e/workspace.spec.ts`. Checks: `make test` passed; `make e2e`
219 passed.

| ID | Status   | Evidence                                                                                                                                                                                                                          |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | "a revealed file that leaves and returns keeps the Changes tab's scroll" deletes and restores the revealed file between ticks; it passes on the tree and fails with the guard disabled (`toBeInViewport` for `file-00.txt`).      |
| F2 | Resolved | `TestChangedPathsMatchFolderNames` sets `core.precomposeUnicode=false` on darwin and expects the edited file once, `modified`; with the merge keyed on the raw path it fails, listing `café.txt` twice.                            |
| F3 | Resolved | `asJSON` reports each `changedFile` mismatch; the count-only-1-MiB mutation now prints `huge.txt` with `"counts":{"added":524289,...}` against `600000`. The remaining `%+v` uses print errors, not files.                   |

No new findings.

## Verdict

Ready to commit.
