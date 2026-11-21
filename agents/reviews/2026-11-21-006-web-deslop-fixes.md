# Web client deslop fixes

## Scope and coverage

Reviewed the uncommitted changes on `83dd2e6` against
[the plan](../plans/2026-11-21-006-web-deslop-fixes.md): `cmd/ox/web/src`
(`App.tsx`, `ApprovalRequest.tsx`, `NewSessionView.tsx`, `Sidebar.tsx`,
`connection.ts`, `highlight.ts`, `ox.tsx`, `poll.ts`, `style.css`,
`toolInput.ts`), `cmd/ox/web/e2e/session.spec.ts`,
`cmd/ox/web/e2e/workspace.spec.ts`, and the seven entries removed from
`docs/todo.md`. The lines added to `docs/todo.md` (the workspace header entry
and the Sidebar section) come from another change and are out of scope. Also
read the relay's `/events` and `/rpc` handlers in `cmd/ox/main.go`, which decide
what a failed request returns.

Gaps:

- `/ox-work`'s final response was not passed on, so its Tests proved and Checks
  were not compared. Every condition the change adds was checked here instead
  by removing it.

## Summary

| ID | Severity | Category    | Action   | Status   | Title                                                          |
| -- | -------- | ----------- | -------- | -------- | -------------------------------------------------------------- |
| F1 | Low      | Correctness | Fix      | Resolved | A poll sent as oxd's connection closes shows the relay's error |
| F2 | Low      | Intent      | Rejected | —        | A tool call with empty input shows `{}`                        |

## F1. A poll sent as oxd's connection closes shows the relay's error

Low · Correctness · Fix · `cmd/ox/web/src/connection.ts:79`

When `oxd` exits or restarts, the relay's `/events` handler sees EOF on the
oxd socket and, on return, deletes the page's connection before the HTTP
response ends. The page learns of the loss only when the stream ends and
`onerror` fires. A file read or folder listing posted in that window gets
`404 unknown connection`; one posted after `oxd` closed its socket but before
the relay's read returns gets `502` with the broken pipe's text. Both reject as
a plain `Error`, so `createPoll` replaces the pane or folder with "unknown
connection" or "write unix …: broken pipe".

The plan's Decision says a failure to reach ox keeps the last answer. These two
relay replies mean exactly that, but the change classifies every non-OK HTTP
response as a relay error. The pane keeps the wrong error until the first poll
after the reload succeeds, and until the page is reloaded when `oxd` stays
down. The window is about a millisecond per `oxd` exit with two polls a second,
so it is rare.

### Evidence

Traced from the source. `cmd/ox/main.go:101-109` registers `conn.Close` and
then the deferred `delete(r.conns, id)`, which runs first when the read loop at
`:121-125` returns. `rpc` (`:133-155`) answers a missing connection with
`404 unknown connection` and a failed write with `502`. `connection.ts:79`
rejects both with `new Error(text)`, and `poll.ts:24` shows any error that is
not `Disconnected`. `ox.tsx:470-474` ticks the clock once a second while ready,
so the error stays until a poll after the reload.

### Action

Have the relay answer both cases that mean its oxd connection is gone, the
unknown connection and the failed write, with `503 Service Unavailable`, and
have `connection.ts` reject a `503` with `Disconnected` and the relay's text.
Every other non-OK status stays a plain `Error`, so the existing `502` fault
injections keep testing relay errors.

Tests: in `cmd/ox/main_test.go`, a relay test that `/rpc` with an unknown
connection answers `503`. In `workspace.spec.ts`, extend the "A request that
cannot reach the relay keeps the last listing" step with a route that fulfills
`list_folder` for `src` with `503` "unknown connection", polled at least twice,
and assert the listing is unchanged.

## F2. A tool call with empty input shows `{}`

Low · Intent · Rejected · `cmd/ox/web/src/toolInput.ts:10`

A tool called with no arguments, such as an MCP tool with no parameters, now
shows `{}` as its summary in the transcript and in the approval card, where the
card showed "Input" before.

### Evidence

`toolSubject` returns `stringify(plain)` when no string is found, which is
`{}` for an empty object.

### Action

Rejected: the plan decides that the summary is never blank and shows the input
as compact JSON when it holds no string, and `{}` states that the call had no
input.

## Checks run

- `make test`: passed.
- `make e2e`: 40 passed, 3 skipped.
- Each added condition removed in turn, with its e2e test run against the
  result. Every test failed:
  - `ox.tsx` clearing on `draft === prompt` instead of `edits === submitted`:
    "the first send hands its text and later edits to the new session".
  - `create` passing the edit count at the create reply instead of at submit:
    the same test, in its draft step.
  - `NewSessionView.tsx` at `83dd2e6`: "a rejected creation keeps the draft and
    its settings after switching views".
  - `toolSubject` at `83dd2e6`: "AskUserQuestion is answered from a form" and
    "Allow sends the exact numbers displayed in the tool input".
  - `poll.ts` keeping only `RpcError`: both file tree and file viewer tests.
  - The `fetch` rejection as a plain `Error`, and `onerror`'s error as a plain
    `Error`: "the file tree lists the workspace and keeps each session's
    expanded folders".
  - No `ResizeObserver` observation: "an archived session is read-only and
    hidden until shown".
  - No `hashchange` listener: "session list membership and ordering don't wait
    for transcripts".
  - No `&#13;` replacement, and no `.hljs-subst` rule: "the file viewer shows
    each session's open file".
- `make live` is not required: the change does not alter how Ox drives Claude
  Code. The headless Chrome check through `make dev` is not required: the page
  reaches `oxd` as before, and only the type of a failed request's error
  changes, which the e2e tests cover.

## Verification 1

Checked the uncommitted tree on `83dd2e6` with the F1 fix: `cmd/ox/main.go`,
`cmd/ox/main_test.go`, `cmd/ox/web/src/connection.ts`,
`cmd/ox/web/e2e/workspace.spec.ts`, and `docs/architecture.md`. `make test`
passed; `make e2e` passed, 40 passed and 3 skipped.

| ID | Status   | Evidence                                                                                                                                                                                                                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The relay answers an unknown connection and a failed write with `503`, and `connection.ts` rejects a `503` with `Disconnected`. With the relay's old statuses, `TestRPCAnswersGoneConnectionUnavailable` fails; with the `503` mapping removed, "the file tree lists the workspace and keeps each session's expanded folders" fails. |

No new findings. Nothing else reads the relay's old `404` or `502`, and
`docs/architecture.md` states the `503` where it describes the relay.

## Verdict

`Ready to commit`: F1 is resolved, and F2 is rejected.
