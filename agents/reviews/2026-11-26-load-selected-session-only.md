# Review: load only the selected session

## Scope and coverage

Uncommitted changes in `cmd/ox/web/src/ox.tsx`, `cmd/ox/web/src/App.tsx`, and `cmd/ox/web/e2e/session.spec.ts`, against base `git merge-base origin/main HEAD` (`931fe48`), checked against `agents/plans/2026-11-26-load-selected-session-only.md`. The change touches no requirement text, and the plan makes no requirement changes.

Gaps:

- No spot-check removals of the new guards; the new e2e specs count `session/get` calls and fail on the old per-session loading by construction.
- `make live` does not observe a web-client-only change.

## Summary

| ID | Severity | Category | Action | Status | Title                                                       |
| -- | -------- | -------- | ------ | ------ | ----------------------------------------------------------- |
| F1 | Low      | Testing  | Fix    | Open   | The retry-finds-existing coverage and its helper went unused |

## F1. The retry-finds-existing coverage and its helper went unused

Low · Testing · Fix · `cmd/ox/web/e2e/session.spec.ts:1502`, `cmd/ox/web/e2e/ui.ts:255`

The spec "a retried creation recovers the session without sending" held the session's snapshots, clicked Send after reconnect, and waited for `cutoff.existing`. The change rewrote it as "a lost creation reply recovers the shown draft's session without sending" and dropped the retried send. Nothing awaits `cutoff.existing` now, so `replyExisting` and `existing` in `cutReply` are dead.

### Evidence

`grep -n "\.existing\|replyExisting" e2e/*.ts` finds only the definitions in `ui.ts`. The plan said to keep the existing specs and adjust only a `hold`.

### Action

Either restore the retried-send step so the `existing` reply is observed (a send before the recovery effect runs, for example with the `session/list` reply held), or remove `existing` and `replyExisting` from `cutReply` if the recovery effect leaves that path unreachable. Layer: e2e, same file.

## Checks run

- `make e2e`: 213 passed, 5 skipped.
- Traced `show`, `begin`, `load`, `reload`, `onError`, and `session/created` through switching A→B→A, a reconnect with the shown session loading, a session shown before it is listed, and a lost creation reply. Found no defect.

## Verdict

Fixes needed: F1.

## Verification 1

Checked the working tree after `existing` and `replyExisting` were removed from `cutReply` in `cmd/ox/web/e2e/ui.ts`.

| ID | Status | Evidence                                                                                                                                                                                                                                                  |
| -- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved by removal | The claim that the path is unreachable does not hold. `create` in `ox.tsx` still handles `result.existing`, and `oxd` still answers a repeated `session_id` that way (`existingAnswerLocked`, `internal/server/methods.go`). A retry is possible whenever the reply was lost and the session is not yet in the page's list, for example after a reconnect and before `workspace/list` returns. `recoverable` now needs the session listed, so the retry reaches the `existing` branch there. No test covers that branch. |

Fix: restore the helper and a spec that holds `workspace/list` after `restartRelay`, clicks Send, and waits for `existing`. Then the session lists, the page shows "Session recovered", and no `session/send` is sent. Alternatively, delete the `existing` branch in `create` if the user accepts that a retry in that window fails with an error; that is a code change the plan did not ask for.

Decision from the top-level agent: the implementing session showed the window does not exist. Send needs `connected()`, and `reload` sets the live stream in the same step that writes the `workspace/list` result, so Send stays disabled while `workspace/list` is held (`toBeEnabled` timed out). A listed session is recoverable at once, so no retry goes out. The `result.existing` branch in `create` stays as the page's answer to a protocol-defined response, since removing it would post the prompt to an existing session. The unused `existing` and `replyExisting` helpers in `e2e/ui.ts` are deleted. F1 is resolved by that removal.

## Verdict

Ready to commit.
