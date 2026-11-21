# Small deslop fixes

## Scope and coverage

Reviewed the uncommitted changes on base `0d2ee01` against [the plan](../plans/2026-11-21-004-small-deslop-fixes.md): 21 modified files covering deslop F9 (first item), F18, F19, F20, and F23. The working tree's only other change is the untracked plan `2026-11-21-005-ur-durable-conversations.md`, which is not in scope.

The implementer's deviations hold up:

- Dropping the `Settings` import follows from step 9, which says to drop imports that become unused.
- `TestInvalidLines` seeds the store directly. A forwarder connection needs an existing session, and seeding the store is the simplest way to get one. It is also the only test of invalid lines on the wire, since `answer` now parses before `handle`.
- The nameless-choice error, `every model needs a name, and "m" has none`, names the kind of choice and its id, as the plan's decision asks.

Gaps:

- `make e2e` was not rerun. The implementer ran it twice on this tree, and no finding depended on it.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                     |
| -- | -------- | ----------- | -------- | ------ | --------------------------------------------------------- |
| F1 | Low      | Correctness | Fix      | Resolved | `listen` creates and locks the lexical parent of `-socket` |
| F2 | Low      | Testing     | Rejected | —      | Draft release and the default permission mode are untested |

## F1. `listen` creates and locks the lexical parent of `-socket`

Low · Correctness · Fix · `cmd/oxd/main.go:181`

`absolute` now keeps `..`, so `net.Listen` binds the file `ox` dials. `listen` still takes `filepath.Dir(path)`, though, and `Dir` cleans its result. For `link/../sub/a.sock`, `listen` creates and flocks `base/sub` while it binds in `base/other/sub`. This is the same lexical cleaning F19 describes, so F19 is only partly fixed:

- If the socket's real parent does not exist, `MkdirAll` creates the wrong directory and the bind fails.
- Two starters that spell the same socket path differently lock different directories, so the stale-socket check and replacement are no longer serialized between them.

### Evidence

A probe test in `cmd/oxd` (since removed) set up `link -> other/inner` and called `listen(dir + "/link/../sub/c.sock")`. It returned `bind: no such file or directory`, and it created `dir/sub` but not `dir/other/sub`.

### Action

Take the directory without cleaning. Cut `path` after its last separator, for example `path[:strings.LastIndexByte(path, filepath.Separator)+1]`, so `/a.sock` gives `/`. `MkdirAll` and `os.Open` then resolve the link before `..`, as the bind does.

Add a case to `TestSocketPathResolvesLinksBeforeDotDot` (`cmd/oxd/main_test.go`) whose real parent does not exist yet, such as `link/../sub/c.sock`. Assert that `other/sub/c.sock` is bound and that `dir/sub` was not created.

## F2. Draft release and the default permission mode are untested

Low · Testing · Rejected · —

The implementer reported that removing `ox.release`, the draft-drop effect in `App.tsx`, or the `defaults.harness === name` branch of `defaultPermissionMode` fails no test.

### Action

Rejected. The plan settles that releasing a removed workspace's draft and control frees state the page cannot observe: `liveDrafts` already hid the draft. `defaultPermissionMode` is a refactor of `defaultSettings`'s permission-mode branch, and it changes no behavior. Neither change adds observable behavior that needs a new test.

## Checks run

- `make test`: passed.
- Removed conditions, one at a time, each reverted afterward:
  - Removing the empty-name check in `addChoice` fails `TestChoiceIDsAndNames`.
  - Classifying links with `filepath.Join(folder, name)` fails `TestWorkspaceFiles`.
  - Using `filepath.Abs` in place of `absolute` fails `TestSocketPathResolvesLinksBeforeDotDot`.
  - Dropping the appended startup reason fails `TestStartupFailureKeepsClaudesReason`.
- `cloc --help` lists `--skip-uniqueness`.
- Implementer's checks, accepted:
  - `make test` passed in a fresh worktree without `node_modules` or `web/dist`.
  - The metrics script passed with a duplicate file staged.
  - `make e2e` passed twice.
  - `make live` is not required: how Ox drives Claude Code is unchanged.
  - A Chrome check is not required: the relay is unchanged, and e2e drives the page through `oxd` and the new `serveConn` parse.

## Verification 1

Checked the uncommitted tree on base `0d2ee01` after the fix. Only `cmd/oxd/main.go` and `cmd/oxd/main_test.go` changed since the review.

- `make test`: passed.
- With `filepath.Dir` restored in `listen`, `TestSocketPathResolvesLinksBeforeDotDot` fails with `bind: no such file or directory` on `link/../sub/c.sock`. It passes with the fix.
- Implementer's `make e2e`: 40 passed, 3 skipped.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `listen` takes `path` up to its last separator, keeping `..`. The new `link/../sub/c.sock` case fails without the fix and passes with it. |

No new findings.

## Verdict

Ready to commit.
