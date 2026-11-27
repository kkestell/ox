# Copy `.worktreeinclude` files into new worktrees

## Goal

A session worktree holds only committed files, so a repository's gitignored
local files (`.env`, local config) are missing and the session cannot run the
project. Claude Code's convention, `.worktreeinclude`, lists them. Ox honors the
same file so a repository's existing one works unchanged.

Claude Code's documented semantics
(https://code.claude.com/docs/en/worktrees.md, "Copy gitignored files into
worktrees"): the file sits at the project root, uses `.gitignore` syntax, and a
file is copied only when it matches a pattern and is also gitignored, so tracked
files are never duplicated. Ox copies whatever git's matching selects, including
files inside a wholly ignored directory.

## Related code

- `internal/server/worktree.go` `addWorktree` — makes the worktree and checks
  that the workspace folder exists in it; every failure after `worktree add`
  returns through `discardWorktree`, which removes the worktree and branch.
  This is the one place the copy belongs.
- `internal/server/worktree_test.go` — `gitRepo` helper (its `.gitignore`
  ignores `*.log`), `TestWorktreeSession`, `TestWorktreeCreationFails`,
  `nothingLeft`.
- `docs/functional-requirements.md` — **Worktrees**.

## Decisions

- **Source.** The main checkout is the workspace's repository top level
  (`git rev-parse --show-toplevel` from the workspace), not the workspace
  folder: `.worktreeinclude` and its patterns are relative to the repository
  root, as in Claude Code. The copy goes to the same relative paths under the
  worktree's root.
- **Matching is git's.** Two commands run in the top level.
  `git ls-files -z --others --ignored --exclude-from=.worktreeinclude` lists
  every untracked file the include patterns match. Its output goes to
  `git check-ignore -z --stdin` (the command's `Stdin` set; `gitCommand` takes
  an `*exec.Cmd`), which keeps the gitignored ones. Ox copies those. Listing
  every ignored file instead would print all of `node_modules` on each
  creation. `check-ignore` exits 1 when no path is ignored, which is an empty
  result, not a failure. This implements "matches a pattern and is gitignored";
  tracked files never appear in the first list. Checked by hand: with `.env`,
  `*.log`, `tracked.txt`, `!z.log`, `sub/` in `.worktreeinclude`, the include
  list is `.env` and `sub/y.log`; `nm/x` (ignored, not included) and `z.log`
  (negated) are excluded.
- **No file, no copy.** Without `.worktreeinclude` at the top level, nothing
  happens and no git command runs. Any other error reading it fails creation.
- **What is copied.** Regular files, with their permission bits, into
  directories made as needed. An entry git lists that is not a regular file (a
  symlink, or a nested repository's directory) is skipped.
- **Failure.** A failed copy fails creation with the reason and discards the
  worktree and branch: the copy runs after the existing in-worktree check and
  returns its error through the same `errors.Join(err, discardWorktree(...))`
  path, so **Worktrees** keeps its "creation fails with the reason and leaves no
  worktree or branch behind". Partial copies vanish with the worktree.
- **Trust boundaries.** The paths come from git inside the main checkout, so
  none leaves it. The worktree side can differ from the main checkout (a HEAD
  symlink where the main checkout has a directory), so each destination's
  parent is resolved with `filepath.EvalSymlinks` after it is made and must lie
  within the worktree root (`within`); otherwise creation fails. Whoever can
  write `.worktreeinclude` can already write the repository, and the files copied
  are ones the user's repository ignores.
- **Settling / Ownership / Claude Code.** No new state, timer, or wait; the copy
  is synchronous inside `addWorktree`, which already runs in the creation
  path. No Claude Code behavior involved.

## Check your assumptions

1. **Which files are copied.**
   - **Plan:** a new worktree gets the repository's gitignored files that
     `.worktreeinclude` lists, matching Claude Code, so an existing
     `.worktreeinclude` works the same. Nothing is copied when the file is
     absent.
   - **Alternative:** copy every ignored file (including `node_modules`) with no
     file needed. It would make large copies on every session and differ from
     Claude Code.
2. **A copy that fails.**
   - **Plan:** the session is not created and the error says which file failed,
     as for any other worktree failure.
   - **Alternative:** create the session anyway without that file. The user
     could find a session missing its `.env` only after it misbehaves.

## Test plan

- Server, in `internal/server/worktree_test.go`, extending `TestWorktreeSession`
  (its repo, workspace in `sub/`, and worktree session already exist): write
  `.env` and `.env.local` (both gitignored through the repo's `.gitignore`,
  extended with `.env*`), `build.log` (ignored, not listed), `sub/local.log`,
  and a tracked-and-listed `sub/file.txt` in `.worktreeinclude`
  (`.env*`, `sub/*.log`, `sub/file.txt`). Assert that the worktree has `.env`,
  `.env.local`, and `sub/local.log` with their contents and the executable bit
  kept for a `0o755` file, lacks `build.log`, and that `sub/file.txt` is
  unchanged from the committed text. It fails without the change because no file
  is copied.
- Server failure, in `TestWorktreeCreationFails` as a new row ("unreadable
  include file"): `.worktreeinclude` as a directory fails the read; assert the
  error and `nothingLeft`. A listed ignored file made unreadable (`0o000`) is
  the copy failure; assert the same. Skip the row's permission variant when
  the test runs as root.
- No `.worktreeinclude`: covered by the existing worktree tests, which still
  pass.

## Implementation plan

1. `internal/server/worktree.go`: add `copyWorktreeInclude(top, root string)
   error`: read `top/.worktreeinclude` (return nil when it does not exist), run
   `git ls-files` and `git check-ignore` as the Decisions say, and copy each
   regular file with `os.MkdirAll` for its parent, the `EvalSymlinks`/`within`
   check, and `os.WriteFile`-style copy preserving `Mode().Perm()`.
2. In `addWorktree`, get the top level with `rev-parse --show-toplevel` beside
   the existing `--show-prefix` call, and call `copyWorktreeInclude` after the
   workspace-folder check, setting `err` so the existing discard path handles
   failure. Update `addWorktree`'s comment.
3. Tests as above.
4. `docs/functional-requirements.md`: change **Worktrees** as below.

## Requirement changes

- **Changed: Worktrees.**
  - Old: "... Ox makes the worktree outside the repository, on a new branch from
    the workspace's current commit, holding only committed files, and runs the
    session in the worktree's copy of the workspace directory; if it cannot,
    creation fails with the reason and leaves no worktree or branch behind."
  - New: "... Ox makes the worktree outside the repository, on a new branch from
    the workspace's current commit, holding the committed files and the ignored
    files the repository's `.worktreeinclude` lists, and runs the session in the
    worktree's copy of the workspace directory; if it cannot make the worktree or
    copy a listed file, creation fails with the reason and leaves no worktree or
    branch behind."

## Documentation updates

- `docs/architecture.md`: only if it describes what a worktree holds; edit that
  sentence to match. `docs/protocol.md` and `docs/configuration.md` do not
  change.
