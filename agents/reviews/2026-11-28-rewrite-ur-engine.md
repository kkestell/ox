# Review: rewrite Ur's engine around one loop, typed events, and turn steps that report errors

## Scope and coverage

Reviewed the uncommitted working tree on branch `rewrite-ur-engine` against base `b5e2cdc` (`git merge-base origin/main HEAD`): `internal/ur/engine/{engine,main,turn,conversation,stream,mcp}.go`, `engine_test.go`, `internal/ur/ur_test.go`, and the `AGENTS.md` Source Map. Plan: `agents/plans/2026-11-28-rewrite-ur-engine.md`. Its Goal and Requirement changes ("None") are the requirements. Its Decisions were checked as claims under Construction, following the rules on `main` at `ee8b8fd`.

Three concern reviewers (Lifecycle, Boundaries, Construction) read the change independently. I confirmed or rejected each finding they reported. The Boundaries reviewer built the base and changed `ur` binaries and ran them on the same inputs: stdin commands, flags, the lost-conversation path, two-turn request bodies with a configure in between, `-ask` bodies, and a crafted resume file. Request bodies, events, conversation files, exit codes, and stderr were identical, except in F5.

Owners derived from the events that change each state, including the oxd side in `internal/ur/agent.go`:

- **Turn:** only `begin` creates it, and only `startNext` calls `begin`. Only `finish` ends it, from four callers: the loop's error branch, `startNext`'s error branch, `onStreamed` with no calls, and `interrupt`. `stop` cancels the turn without publishing anything. oxd mirrors the turn from `taken` and `state idle`.
- **Running command:** `execute` starts it and emits `command_started`. `shell.wait` kills and reaps the group, and `onRan` always emits `command_ended`. A `ran` that arrives after stop is dropped, and `agent.go` kills the groups ur never reported ended. That is the base behavior, unchanged.
- **Conversation file:** `openConversation` reads it once. `conversation.append`, through `engine.save`, is the only writer, and `save` is the only place that emits `resumable` with a token.
- **Stop and exit status:** `loop`'s return value decides it. `serve`'s lost-conversation path and forwarder start failure return 1, as the plan's `serve` steps say.
- **Forwarder lifetime:** `read` reaps the forwarder, closes `exited`, then delivers `forwarderExited`. `stop` closes it after `shells.Wait`. The removed `f.err` field leaves the exit error in one place.

Past defects checked, none reopened:

- **#147, command lifetime:** interrupt, finish, and stop cancel the turn's context, and `shells.Wait` runs before the forwarder closes.
- **#156, outcome mapping:** one `finishError` serves both entry points through `client.post`.
- **Approved vs executed command:** `asking.command` is both the value the approval card shows and the value that runs.
- **#189, tool names:** the engine uses the bare names from `tools/list`.
- **First input:** a valid input is queued and then taken by `startNext`, and an input command without an input fails at the reader.

Event order matches the base: `result` and `idle` come before the next `taken`, `interrupted` comes after `idle`, and `command_ended` comes before a stale `ran` is dropped.

Gaps:

- The `approval` Playwright spec in `make live` was not run. It drives Claude, not Ur, so it cannot observe this change.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                         |
| -- | -------- | ------------- | -------- | ------ | ------------------------------------------------------------- |
| F1 | Low      | Testing       | Fix      | Resolved | TestShellAuto does not pin "first id stays, last signature wins" |
| F2 | Low      | Construction  | Fix      | Resolved | A step's error becomes the turn's result in two places         |
| F3 | Low      | Documentation | Fix      | Resolved | Two comments overstate what the code guarantees                |
| F4 | Low      | Construction  | Rejected | —      | `onRan` asserts the step is `*executing`                        |
| F5 | Low      | Correctness   | Rejected | —      | Lost-conversation path exits on an invalid command             |
| F6 | Low      | Construction  | Rejected | —      | `streamed` can carry calls and an error together               |
| F7 | Low      | Construction  | Rejected | —      | A turn exists briefly with a nil step                          |
| F8 | Low      | Settling      | Rejected | —      | The reader keeps reading stdin after the loop stops            |
| F9 | Low      | Testing       | Rejected | —      | TestStaleOutput is white-box                                   |

## F1. TestShellAuto does not pin "first id stays, last signature wins"

Low · Testing · Fix · `internal/ur/ur_test.go:1391`

The plan's pin, and the test's comment, say that fragments of one reasoning detail keep the first `id` and the last `signature`. In the fixture, only the first fragment has an `id` and only the last has a `signature`, so each value appears once, and the test passes whichever fragment wins.

### Evidence

In `appendReasoning` (`stream.go:121,123`), I swapped the operands of `cmp.Or` for both `ID` and `Signature`, so the last id and the first signature win. `go test ./internal/ur -run 'TestShellAuto$'` still passes. Then I reverted the swap.

### Action

Give both fragments a value for each field: for example, `id` `rs_1` on the first and `rs_2` on the last, and `signature` `sig_0` on the first and `sig` on the last. Keep the expected `listBoth`, with `rs_1` and `sig`. TestShellAuto then fails if either rule is reversed.

## F2. A step's error becomes the turn's result in two places

Low · Construction · Fix · `internal/ur/engine/engine.go:144`, `internal/ur/engine/engine.go:182`

The Goal says "One place turns a step's error into the turn's result". Yet `loop` and `startNext` each build `e.finish(agent.Result{Text: err.Error(), IsError: true})` themselves. Today the two agree, but a change to how a failed turn is reported would have to find both.

### Evidence

Both lines read the same in the source. The plan's sketch used a `failed(err)` helper for this.

### Action

Add one method, for example `func (e *engine) fail(err error)`, which calls `finish` with the error result. Call it from both places. No new test: the existing save-failure and stream-error tests in `ur_test.go` cover both callers.

## F3. Two comments overstate what the code guarantees

Low · Documentation · Fix · `internal/ur/engine/conversation.go:83`, `internal/ur/engine/engine.go:17`

- `conversation.messages` says "The messages share no slice with entries". `Content.Images` is shared, because `m` is a struct copy and `Images` is not cloned. Nothing writes to it, so behavior is correct, but the sentence is false.
- The `engine` comment says its fields "are set before the loop starts, and request and command goroutines read them". `shells` is written by the loop (`Add`) and by command goroutines (`Done`), and `stop` closes `stopped`. Each of those fields has its own accurate comment, so only the struct-level sentence is wrong.

### Evidence

Read in the source.

### Action

Narrow the `messages` sentence to what holds: the reasoning details and tool calls are fresh slices, so the merge never writes into entries. Reword the `engine` sentence so that it covers the fields set at start, and leave `shells` and `stopped` to their own comments.

## F4. `onRan` asserts the step is `*executing`

Low · Construction · Rejected · `internal/ur/engine/turn.go:183`

`onRan` checks only that the event's turn is the running turn, then asserts `e.turn.step.(*executing)`. The assertion would panic if a current turn's `ran` arrived during another step. The base compared step pointers instead.

### Evidence

All three reviewers derived the invariant independently, and it holds. A step leaves `executing` only through its own `ran` or the end of the turn. `approve` acts only on `*asking`, and `onAnswered` only on `*calling`. A current `delta` or `streamed` cannot arrive, because a turn runs one goroutine at a time.

### Action

Rejected. The plan chose turn identity as its one staleness rule. A panic is how AGENTS.md says code fails when something unsupported happens, and comparing step pointers would bring back a second staleness rule.

## F5. Lost-conversation path exits on an invalid command

Low · Correctness · Rejected · `internal/ur/engine/engine.go:95`

Commands are now checked in the reader, which also runs during the lost-conversation path. With `-resume missing` and `{"type":"bogus"}` on an open FIFO, the base kept waiting, and the change exits with status 1 within a second.

### Evidence

The Boundaries reviewer reproduced this with the base and changed binaries side by side.

### Action

Rejected: normal use does not reach it. oxd sends only valid commands (`agent.go:272,293,308,334`), and it terminates ur once it sees a `resumable` event with an empty token (`agent.go:201-204`). The exit status is 1 either way.

## F6. `streamed` can carry calls and an error together

Low · Construction · Rejected · `internal/ur/engine/turn.go:70`

`streamed{calls, err}` allows both fields to be set at once.

### Action

Rejected. `post` follows Go's `(value, error)` convention and never returns both, and `onStreamed` checks the error first. A separate type for each outcome costs more than the convention it would replace.

## F7. A turn exists briefly with a nil step

Low · Construction · Rejected · `internal/ur/engine/turn.go:97`

`begin` creates the turn without a step, and `request` sets `streaming{}` only once the prompt is saved and AGENTS.md is read.

### Action

Rejected. `begin`, `request`, and the `finish` that follows a failure all run within one loop iteration, so no event ever sees the nil step.

## F8. The reader keeps reading stdin after the loop stops

Low · Settling · Rejected · `internal/ur/engine/engine.go:190`

At the base, the reader returned once `stopped` closed. Now `deliver` returns and the reader goes back to decoding until EOF.

### Action

Rejected. It touches no state, and the process exits as soon as `stop` returns.

## F9. TestStaleOutput is white-box

Low · Testing · Rejected · `internal/ur/engine/engine_test.go:119`

The test builds engine internals and calls `handle` directly.

### Action

Rejected. The plan sanctions this test, the base test had the same form, and a black-box version would need control over the timing between cancel and delivery. Mutations confirm it guards the change: removing the `current` check in `onDelta`, `onStreamed`, or `onRan` fails its subtest.

## Checks run

- Plan pins on `b5e2cdc`, in a temporary worktree carrying the new `ur_test.go` and `TestBadCommand`: `TestBadCommand`, `TestShellAuto`, and `TestLastLineWithoutNewline` (with the CRLF case) pass. The full `go test -race ./internal/ur/...` also passes there.
- `make test` on the change (lint, gochecksumtype included, `go test -race ./...`, and the Node suites): pass.
- `OX_LIVE=1 go test -race -run TestLiveUr ./internal/server`, with the key from `.env`: pass.
- Spot-checked removals, each reverted:
  - CRLF trim in `sseData` fails the CRLF case.
  - Showing encrypted reasoning fails TestShellAuto.
  - Treating an unknown command as an interrupt fails TestBadCommand.
  - Dropping the configure nil check fails TestBadCommand.
  - Removing each `current` check fails TestStaleOutput.
  - Swapping the id and signature merge rules passes (F1).
- Old and new `ur` binaries run side by side on stdin commands, flags, the lost path, request bodies, `-ask`, and resume (Boundaries reviewer): identical except F5.

## Verification 1

Checked the uncommitted working tree on `rewrite-ur-engine` after `/ox-work fix`. Checks run: `go test -race ./internal/ur/...` and `make lint` (0 issues) both pass. The implementer reports that `make test` and `TestLiveUr` pass.

| ID | Status   | Evidence                                                                                                                                           |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | Both fragments now carry an id and a signature (`rs_1`/`sig_0`, `rs_2`/`sig`). Reversing either the id rule or the signature rule in `appendReasoning`, one at a time, fails TestShellAuto. |
| F2 | Resolved | `engine.fail` (`turn.go:324`) is the one place that builds the error result. `loop` and `startNext` call it (`engine.go:145,183`).                   |
| F3 | Resolved | The `messages` comment now claims fresh slices only for reasoning details and tool calls. The `engine` comment now covers only the client, workspace, forwarder, and output. |

No new findings.

## Verdict

Ready to commit.
