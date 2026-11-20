# Review of the fixes for the full-repository review

## Scope and coverage

The change from `733ac2a` to `4c96a77` ("Keep session queues, settings, and process state in sync"). It was staged when this review was requested and was committed before the review began, so the commit was reviewed. The working tree adds one uncommitted edit, the **Setting commands** requirement in `docs/functional-requirements.md`. The plan is `agents/reviews/2026-11-19-004-full-repository-multi-lens.md`: its 37 findings and fixes, the user's F3 decision, and the user-approved text of **Setting commands**, which the working tree carries word for word.

The review traced the session's pending-input queue (`written`, `inbox`, `agentUpdate`, `startForInput`, `deliver`), `agent.Harness.CheckPrompt`, conversation-ID tracking, the `children` registry and `KillAll`, `read` and `deliverOutput`, the settings path through `OnConfigured` and `adopt`, `oxd`'s socket and signal handling, the client's `load` state, `reconcilePrompt`, the relay's ordering of responses and notifications, and the `column-reverse` transcript.

**Setting commands** is met. `checkPrompt` refuses a first word of `/model` or `/effort` before a parked session starts, and other slash commands pass (`TestClaudeRefusesSettingCommandsBeforeStarting`). The page keeps the text of any refused send in the composer. "an oversized prompt fails visibly and stays in the box" covers that client path for every refused send. An e2e test specific to `/model` would need the Claude harness, which the e2e stack does not run.

Gaps:

- `make e2e` and a headless Chrome check through `make dev` were not run. The caller is running `make live` and asked that no other Playwright run start alongside it. The e2e specs were read, not run.
- `npm run build` was not run because it rewrites `dist`, which the running live e2e may serve. `tsc --noEmit` and `biome check` ran in its place.
- The `column-reverse` transcript relies on the browser's scroll anchoring to hold the view while the reader is scrolled up. Only Chromium runs the following-output test. Other browsers were not checked.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                        |
| -- | -------- | ------------ | -------- | ------ | ------------------------------------------------------------ |
| F1 | Low      | Requirements | Fix      | Resolved | The sidebar labels a session by its ID until its snapshot loads |
| F2 | Low      | Construction | Fix      | Resolved | Pending-input rules are decided in several places             |
| F3 | Low      | Testing      | Rejected | —      | `KillAll`'s refusal of new spawns has no test                 |
| F4 | Low      | Documentation | Fix     | Resolved | Claude Code's empty `ExitPlanMode` input is recorded only as a known issue |

## F1. The sidebar labels a session by its ID until its snapshot loads

Low · Requirements · Fix · `cmd/ox/web/src/ox.tsx:56`, `cmd/ox/web/src/Sidebar.tsx:114`

The F16 fix lists sessions from `workspace/list` before their `session/get` returns. `sessionLabel` still derives the label from the loaded snapshot's entries. Until the snapshot arrives, it shows the first 8 characters of the ID, even for a session that already has a first prompt (**Session labels**).

`session/get` is answered on the session's goroutine. That goroutine can be busy for up to a minute while it starts Claude for a message (`startForInput`), or for up to the 10-second kill timer while it stops a process. On a page load or reconnect during either, the sidebar shows the ID in place of the label for that whole time. Before F16 the session was not shown at all, so the fix swapped a missing entry for a wrong label.

### Evidence

Traced from the source. `workspace/list` (`internal/server/methods.go`, `listWorkspaces`) returns only `session_id`, `harness`, and `created_at`. `sessionLabel` reads `session.load.snapshot?.entries` and falls back to `session.id.slice(0, 8)`. `getSession` posts to the session's goroutine through `onSession`.

### Action

Return the label with session membership. `workspace/list` includes each session's `label`, computed by `Server.label` as `list_sessions` already does, and documented in `docs/protocol.md`. The client's membership record keeps it. `sessionLabel` derives the label from the snapshot once one is loaded and uses the listed label before then. A session created on this page has no first prompt yet, so its ID-based label stays correct until its first prompt arrives.

Test to keep: an e2e test in `session.spec.ts` that holds one session's `session/get` with the existing `hold` helper, reloads, and expects the sidebar to show that session's prompt label while the hold is in place.

## F2. Pending-input rules are decided in several places

Low · Construction · Fix · `internal/server/session.go:103`, `:120`, `:130`, `:172`, `:264`; `internal/server/methods.go:420`

The session owns pending inputs, but several of its rules are decided by each caller or appear twice:

- `writeInput` sets `sess.written` without checking it, and clears it on any error. Each caller, `sendPrompt` and `deliver`, must check `written == nil` first. A caller that skips the check overwrites the written input. When Claude then takes the original, `agentUpdate` reads a nil `written` and panics, which brings down `oxd`.
- Two places drop the inbox when the process ends: `agentUpdate` on `UpdateExited` and `parked`. Each is enough without the other.
- The `len(sess.inbox) != 0` refusals in `sendPrompt` and `configureSession` cannot fire. `deliver` runs after every operation and event, so a non-empty inbox means either an input is written or a configuration is running, and the harness already refuses both as `ErrBusy`.

All current paths behave correctly. The cost is that the next caller of `writeInput`, or the next change to process-end handling, has to know rules the code does not enforce.

### Evidence

Each condition was removed in a detached worktree at `4c96a77` and `go test ./internal/server` was rerun:

- Removing `written != nil` from `sendPrompt` fails the tests. Only that caller-side check protects `written`.
- Removing `agentUpdate`'s `UpdateExited` case passes. Replacing `parked`'s `cancelInbox` with `sess.inbox = nil` passes. Removing both fails `TestExitDropsWaitingMessages`.
- Removing the inbox refusal from `configureSession` passes. Removing it from `sendPrompt` passes.

### Action

Give each rule one place:

- `writeInput` returns `agent.ErrBusy` while `written` is set, and `deliver` and `sendPrompt` take its answer instead of checking first.
- `sendPrompt` and `configureSession` ask one session predicate for "an input is pending" (`written != nil || len(inbox) != 0`), so the session's own queue decides the `-32002` refusal that `docs/protocol.md` documents. Alternatively, drop the unreachable inbox checks and leave the refusal to the harness. Do not keep both.
- Drop the inbox in exactly one place when the process ends. `parked` is the right place, because it covers exits that publish no `exited`, such as parking and deletion.

Tests to keep: `TestSendMessage` and `TestExitDropsWaitingMessages` already cover the behavior. Add a server test in `methods_test.go` in which a second `session/send` while a prompt is written returns `-32002`, and the first prompt's later `prompt` update still carries its text.

## F3. `KillAll`'s refusal of new spawns has no test

Low · Testing · Rejected · — · `internal/claude/claude.go:38`

Removing the `children.killed` check in `spawn` leaves `TestDaemonOwnsSocketUntilShutdown` passing.

### Action

Rejected. The check closes the window between `KillAll` releasing the lock and `os.Exit`, during which a session goroutine could start a Claude that outlives `oxd`. A test would have to start a spawn in that microsecond window. That costs more than the check it would protect, and the code makes the guarantee plain.

## F4. Claude Code's empty `ExitPlanMode` input is recorded only as a known issue

Low · Documentation · Fix · `docs/claude-code.md:429`, `docs/known-issues.md`

Added in verification 1. The new known-issues entry, "A plan written to a file shows no plan in its tool call", rests on an observed Claude Code behavior: Claude wrote its plan to `~/.claude/plans/`, called `ExitPlanMode` with `{}`, and the `can_use_tool` request still carried the plan. `docs/claude-code.md` owns observed Claude Code behavior, but its tool list still records only `ExitPlanMode{plan}`. That document now omits a variant Ox has seen, and the fact lives only in the known-issues entry.

The entry is a valid `Record`. It was seen once in three live runs, it needs a reproduction, and a fix depends on which Claude output reliably carries the plan. Its worst cost, how it was found, and what it waits on are stated. The `approval.spec.ts` test's final transcript assertion fails whenever the behavior recurs, which reproduces the recorded defect rather than a new one.

### Evidence

`docs/claude-code.md` line 429 lists `ExitPlanMode{plan}`. `planOf` in `cmd/ox/web/src/toolInput.ts` renders a plan only from `input.plan`. The live test "an ExitPlanMode plan renders as Markdown" checks the approval card's plan first and the transcript tool's plan last, which matches the entry's account that the card showed the plan and the tool card did not.

### Action

Record the observed variant in `docs/claude-code.md`, beside the `ExitPlanMode` input shape and with its version (2.1.329): `ExitPlanMode` can arrive with `{}` after Claude writes the plan to a plan file, and the `can_use_tool` request's input then carries `plan`. The known-issues entry links to that passage instead of restating the behavior. No test, since this is a documentation change.

## Checks run

- `go vet ./...` and `go test -race -count=1 ./...` passed.
- `biome check .` and `tsc --noEmit` in `cmd/ox/web` passed.
- Against the base revision `733ac2a`, the new tests that compile there fail: `TestOpenUsesLiteralPath`, `TestChoiceIDs`, and `TestRPCRelaysOneMiBOfEscapedText`. The other new Go tests use interfaces this change adds.
- Removed conditions that a test catches: the `ErrExited` adopt in `configureSession` (`TestConfigurationOutlivesItsProcess`), the agent's `configured` call on an acknowledgement (`TestConfigureWaitsForAcknowledgements` and two others), the stderr tail in the exit reason (`TestUnexpectedExitIsReported`, `TestExitDropsWaitingMessages`), `FromLabel` on messages (`TestSendMessage`), the 1 MiB decoded limit (`TestSendPromptSizeCountsDecodedBytes`), `cancelInbox` and the notice in `startForInput` (`TestMessageToUnresumableConversationLeavesOneNotice`, `TestSendMessage`), `written != nil` in `deliver` and `sendPrompt`, the kill loop in `KillAll` (`TestDaemonOwnsSocketUntilShutdown`), the owner-side label read in `list_sessions` (`TestListSessionsCapturesLabelWithOwnerRead`), and conversation-ID tracking (`TestResumeTokenKeptWhenNotStored`).
- Removed conditions that only `TestLive` catches: the `/clear` conversation change, and publishing a result's text when the turn streamed none (`/cost`). Both are observed Claude Code behavior, and `TestLive` checks them as `AGENTS.md` prefers.
- Removed conditions that no test catches: see F2 and F3.
- Blocked: `make e2e` and the headless Chrome check through `make dev`. The caller's `make live` run was in progress and the caller asked that no other Playwright run start. `npm run build` was skipped for the same reason.

## Verification 1

Checked the uncommitted changes on top of `4c96a77`: `internal/server/session.go`, `internal/server/methods.go`, `cmd/ox/web/src/ox.tsx`, `cmd/ox/web/src/protocol.ts`, `cmd/ox/web/e2e/session.spec.ts`, `docs/protocol.md`, `docs/known-issues.md`, and the **Setting commands** requirement.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `workspace/list` returns each session's `label` from `Server.label`, and `docs/protocol.md` documents it. `sessionLabel` uses the listed label until a snapshot loads and derives it from the snapshot afterward. The e2e test "session list membership and ordering don't wait for transcripts" now holds a reload's `session/get` and expects the prompt label. With the client fallback removed in a detached worktree, that test fails ("Expected - 1, Received + 1"). With the fix, it passes. |
| F2 | Resolved | `writeInput` refuses with `ErrBusy` while `written` is set, so no caller can overwrite it. Removing that check fails `go test ./internal/server`. `sendPrompt` and `configureSession` share `inputPending`. Only `parked` drops the inbox: putting `agentUpdate`'s `UpdateExited` case back passes the tests, and before the fix, removing both drops failed `TestExitDropsWaitingMessages`, so `parked`'s is now the protected one. `configureSession`'s `inputPending` check is still unobserved because the harness also refuses an input that is pending. That overlap is the design F2 offered, so it is not a new finding. |

New findings: F4.

Checks run:

- `make test` passed: biome, `tsc` and the Vite build, and `go test -race ./...`.
- `make e2e` passed: 35 passed, 3 live-only tests skipped.
- Headless Chrome through an isolated stack: the built `oxd` and `ox` (race detector, embedded web build) on a temporary socket, database, and configuration, served on port 18492. A Playwright script added a workspace, ran a fake session's prompt to its reply, and sent `/model sonnet` as a new Claude session's first prompt. That send was refused with "change the model and effort in the session's settings", the text stayed in the composer, and no Claude process started. After a reload, the sidebar showed the listed labels. The stack was stopped afterwards. It used the relay's embedded build instead of `make dev`'s Vite server, because `make dev` binds fixed ports that the user's stack holds. The page reaches `oxd` through the same relay either way.
- `make live` was not rerun here. The caller ran it on this change before these fixes, and the fixes do not change how Ox drives Claude Code.

## Verification 2

Checked the uncommitted changes on top of `4c96a77`. Since verification 1, only `docs/claude-code.md` and `docs/known-issues.md` changed.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F4 | Resolved | Section 13 of `docs/claude-code.md` records that `ExitPlanMode` can arrive with `{}` after Claude writes its plan under `~/.claude/plans/`, while its `can_use_tool` request still carries `plan` (2.1.329, once in three Haiku runs). The known-issues entry links to that passage (`claude-code.md#13-built-in-tool-names-21319`, which matches the heading) and no longer restates the behavior. Its worst cost, how it was found, and what it waits on remain. |

New findings: none.

Checks run: none. The change is documentation only, and the checks in verification 1 cover all the code in the working tree.

## Verdict

Ready to commit. F1, F2, and F4 are resolved, and F3 is rejected. The rest of the plan is implemented as described, including the user's F3 decision and its **Setting commands** requirement. `make test`, `make e2e`, and the headless Chrome check pass.
