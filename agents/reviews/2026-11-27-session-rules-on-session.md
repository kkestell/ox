# Parked-session rules on session, and less RPC boilerplate

## Scope and coverage

Reviewed the uncommitted changes in `internal/server/create.go`, `mcp.go`,
`methods.go`, `methods_test.go`, and `session.go` against base `566e832`
(`git merge-base origin/main HEAD`, also `HEAD`), checked against
`agents/plans/2026-11-27-session-rules-on-session.md` (#203). The change is a
refactor inside `internal/server` with no new state, so this session reviewed
it alone, with no concern reviewers.

Checked:

- **Intent.** `approve`, `interrupt`, and `configure` now live on `*session`
  beside `sendPrompt`. The handlers only parse params and reply. Outside
  `session.go` and `archive.go`, only `create.go:235` reads `sess.agent`,
  which the plan allows. `onSessionID`, `sessionIDParam`, and `replyEmpty`
  replace the repeated parsing and empty-success blocks. `fileRoot` and
  `removeWorkspaceLocked` return errors that `failure` maps. One
  `sessionInfo` type serves `workspace/list` and `session/created`.
  `docs/functional-requirements.md` is unchanged, and the plan makes no
  requirement changes.
- **Correctness.** Every error keeps its code and message:
  `errShuttingDown` maps to `operationFailed` "daemon is shutting down", as
  `shuttingDown` does. `errSessionNotFound` and `errWorkspaceNotFound` map to
  `notFound` with the same text. The model check's `*codedError` carries
  `jsonrpc.InvalidParams`. `configure` keeps today's sequence: it fills the
  effort into its local `patch` copy, which then reaches both
  `agent.Configure` and the `ErrExited` recovery, as `input.SettingsPatch` did
  before.
- **Requirements and Ownership.** Each operation still runs on the session
  goroutine through `onSession`. Each reply leaves from the same place as
  before: at once for approve and for a parked or archived configure, from the
  agent's `done` for interrupt and a running configure, and under `s.mu` after
  the broadcasts for unarchive and `workspace/remove`. Replies keep their
  order against updates.
- **Wire form.** `session/created` gains `archived` only when it is true.
  `create.go:268` refuses children of an archived caller, so a newly published
  session is never archived. Only the order of keys in the object changes.
  `docs/protocol.md` describes the fields, not their order.

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks, was not
  passed on. This session ran the checks and mutations below itself.

## Summary

No findings.

## Checks run

- `make test` (lint, Go with the race detector, Node suites): pass.
- `go vet ./internal/server/`: pass.
- Mutations of `session.go`, one at a time, each run against the named tests
  and restored afterwards:
  - Parked `interrupt` returns an error: `TestApprovalAnsweredOnce` fails.
    This is the new assertion.
  - Parked `approve` returns an error other than `ErrApprovalNotFound`:
    `TestApprovalAnsweredOnce` fails.
  - Archived-refusal branch removed from `configure`: the package's tests fail.
  - `ErrExited` recovery removed: `TestConfigurationOutlivesItsProcess` fails.
  - Model check failure returned without `InvalidParams`: `TestSettings*`
    fails.
  - Effort fill removed: the package's tests fail.
- `make e2e` and `make live` were skipped. The change alters neither what the
  page does nor how Ox drives Claude Code. Each call to the agent is the same
  call, made from a different function.

## Verdict

Ready to commit.
