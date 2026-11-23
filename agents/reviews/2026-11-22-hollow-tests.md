# Review: make thirteen tests fail when their behavior breaks

## Scope and coverage

Uncommitted changes on `hollow-tests` against base `394daa4`, checked against `agents/plans/2026-11-22-hollow-tests.md`. The working tree held the seven test files the plan names and the plan itself; no production code changed.

Gaps:

- Proportionate review at the parent session's request: four of the twelve claimed mutations were spot-checked, and the full suites were not rerun.

## Summary

| ID | Severity | Category | Action | Status | Title                                                 |
| -- | -------- | -------- | ------ | ------ | ----------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Resolved | `TestInvalidLines` matches a three-digit path segment |

## F1. `TestInvalidLines` matches a three-digit path segment

Low · Testing · Fix · `internal/server/methods_test.go:90`

The new check fails the test when the `workspace/list` result contains `filepath.Base(unadded)`. `t.TempDir` names its directories `001`, `002`, `003`, so the check searches the whole result for a three-digit string. The result also holds the `w` workspace's temporary path, whose parent carries a random decimal suffix, and nanosecond `created_at` timestamps. Either can contain the digits, and the test then fails with no workspace created.

### Evidence

`go test ./internal/server -run 'TestInvalidLines$' -count=400` on the unmodified tree failed once:

```
methods_test.go:91: a request without an ID created a workspace: {"workspaces":[{"workspace_id":"w","path":".../TestInvalidLines3647476393/002",...
```

### Action

Decode the `workspace/list` result and assert it holds exactly the one workspace the test stored (`w`), or that no workspace's path equals `unadded` after `filepath.EvalSymlinks`. Keep it in `TestInvalidLines`, and confirm the mutation the implementer used (the ID-less drop removed) still fails it.

## Checks run

- `TestConfigureTimeoutStopsProcess` with the request-ID check in `controlTimedOut` removed: fails.
- `TestSameDirectoryIsOneWorkspace` with a `workspace/created` broadcast added for an existing workspace: fails.
- The pending-setting spec, the one the implementer left unproven, with `session/configured` no longer applied to the page's settings: the new order fails at the value check after `aria-busy` clears; the old order passes. The reorder is proven.
- `TestInvalidLines` repeated 400 times on the unmodified tree: one false failure (F1).
- Every mutation was reverted; the diff matches the reviewed tree.
- From the implementer: `make test` passed; `make e2e` passed (124 passed, 4 skipped) on a rerun after unrelated browser-closed failures under concurrent load. `make live` is not required for a test-only change.

Issue [#31](https://github.com/kkestell/ox/issues/31) counts fourteen tests; this change fixes thirteen and leaves `TestLiveUr` to [#27](https://github.com/kkestell/ox/issues/27), as the plan decides. The pull request that closes #31 should say so, or #27 should name `TestLiveUr`.

## Verification 1

Checked the uncommitted tree after the fix for F1. Only `TestInvalidLines` changed since the review.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The test decodes `workspace/list` and compares each path with `unadded`. 200 repeated runs pass. With the ID-less drop removed from `serve`, the path check fires in 9 of 10 runs and the unanswered-request check fails every run. The server stores the path as written, so the exact comparison holds on macOS. |

No new findings. The pull request body will say that `TestLiveUr` is fixed in #27.

## Verdict

`Ready to commit`.
