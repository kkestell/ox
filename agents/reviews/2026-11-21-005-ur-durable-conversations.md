# Review: durable Ur conversations

## Scope and coverage

Reviewed the uncommitted changes on base `cb452a1` against
`agents/plans/2026-11-21-005-ur-durable-conversations.md`, with the user's
answers: deleting a session leaves the conversation file, and a lost
conversation refuses the send. Working tree: `cmd/oxd/main.go`,
`cmd/oxd/main_test.go`, `docs/architecture.md`,
`docs/functional-requirements.md`, `internal/paths/paths.go`,
`internal/server/live_test.go`, `internal/ur/stream.go`, `internal/ur/ur.go`,
`internal/ur/ur_test.go`, all modified; the plan untracked.

Traced `Start`, `save`, `load`, `begin`, `finish`, `keepReply`, `Stop`, and
`Parkable` through the server's `start`, `startForInput`, `markResumable`,
`armPark`, `stop`, and `destroy`. The requirement edits match the plan's
Requirement changes word for word. Requirements checked: **Ur sessions**,
**Parking**, **Resuming the conversation**, **Surviving restarts**,
**Deleting a session**, **Ox's own files**, **File locations**, **Stop**,
**Setting changes keep the rest**.

Gaps:

- No headless Chrome check. None is required: the change touches no web
  client code, protocol message, or relay path, and a start error reaches the
  page by the existing path the missing-key refusal uses.

## Summary

| ID | Severity | Category | Action   | Status | Title                                            |
| -- | -------- | -------- | -------- | ------ | ------------------------------------------------ |
| F1 | Low      | Testing  | Rejected | —      | Write and Close errors in `save` have no test    |
| F2 | Low      | Correctness | Rejected | —   | A partial write leaves a fragment in the file    |

## F1. Write and Close errors in `save` have no test

Low · Testing · Rejected · `internal/ur/ur.go:142`

Ignoring the error from `f.Write` or `f.Close` in `save` fails no test, so
`history` could gain an entry the file lacks without a test noticing.

### Evidence

The implementer reports the removal survives. A write to a file opened for
writing fails only on a full or failing disk; macOS has no `/dev/full`, and
`RLIMIT_FSIZE` raises `SIGXFSZ` in the test process.

### Action

Rejected: the fault costs more to inject than the check costs to keep. The
`OpenFile` failure, which shares the same return path, is tested by
`TestSaveFailure`, and the `MkdirAll` error is covered by the `OpenFile` right
after it.

## F2. A partial write leaves a fragment in the file

Low · Correctness · Rejected · `internal/ur/ur.go:142`

If `Write` writes part of an entry and fails, the entry stays out of
`history`, but its fragment stays in the file. The next successful save
appends after the fragment, so a later resume fails to decode that line and
the conversation is lost.

### Evidence

From the source: `save` opens with `O_APPEND` and does not truncate back on a
failed write.

### Action

Rejected: it needs a full or failing disk, an unusual failure `AGENTS.md`
says not to build recovery for. The outcome is the lost-conversation path,
which refuses the send with a clear error and starts fresh.

## Checks run

- `make test` on the final tree: passed.
- Removed conditions, one at a time, with `go test ./internal/ur`; each
  failed a test:
  - announcing the token on every save, not only the first: `TestResume`.
  - `Parkable` returning false: `TestInputDuringTurnRunsNext`.
  - `Stop` dropping the partial reply: `TestStopDuringTurn`,
    `TestSaveFailure/stop`.
  - dropping the turn's own error text before the save failure:
    `TestSaveFailure/failed_reply`.
  - not clearing the token on a lost conversation: `TestResumeLost`.
  - `save` not appending to `history`: `TestTwoTurns`, `TestStreamErrors`.
  - posting the request after a failed prompt save: `TestSaveFailure/prompt`.
  - sending messages without their content: `TestInterrupt`,
    `TestStopDuringTurn`, `TestResume`.
- From the implementer's report, on the final tree: `make e2e` (40 passed, 3
  skipped) and `make live` (`TestLive`, `TestLiveUr` parking between turns,
  and 3 approval specs) passed.
- The implementer's open choices hold: `new(expr)` compiles under Go 1.27,
  promoted entry fields are what `go fix` produces, the `run` helper's
  recorded settings mirror the server's `adopt`, and a 1s `parkAfter` makes
  `TestLiveUr` wait for a real `parked` update after the first turn.

## Verdict

Ready to commit. F1 and F2 are rejected.
