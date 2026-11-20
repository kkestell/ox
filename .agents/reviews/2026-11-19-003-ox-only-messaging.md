# Review: Ox-only messaging and Ox's instructions for Claude

## Scope and coverage

Uncommitted changes on `main` over base `00c56b6`: `internal/claude/claude.go`, `internal/claude/claude_test.go`, `internal/server/live_test.go`, `internal/server/methods_test.go`, `docs/claude-code.md`, `docs/functional-requirements.md`. Checked against `.agents/plans/2026-11-19-003-ox-only-messaging.md`, with the user's approval of both requirement changes word for word and their choice of Refuse for inbound cross-session messages. The change to the stand-in `claude` scripts in `internal/server/methods_test.go`, which the plan does not name, is reviewed as part of the change.

The code matches the plan: `systemPrompt` reproduces the approved Ox text with the session ID and workspace filled in, `startProcess` passes `--disallowedTools=SendMessage,ListAgents` and `--settings={"crossSessionInbound":"refuse"}` on every start and resume, the parameter rename is done, and both requirement changes are applied word for word. The heading the instructions name matches `messageInput`. A probe on Claude Code 2.1.328 confirmed that the `--settings=<value>` form passes its value through (an invalid value is reported as `Settings file not found: …/{bad`), and that with both flags Haiku lists no `SendMessage` or `ListAgents` among its tools.

Gaps:

- The inbound refusal is not reproduced here; it rests on the plan's probe and the stand-in assertion on `--settings`, as the plan's Test plan states.

## Summary

| ID | Severity | Category | Action | Status | Title                                                            |
| -- | -------- | -------- | ------ | ------ | ---------------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix    | Resolved | The live step reports the defect it reproduces as a flake        |
| F2 | Low      | Testing  | Fix    | Resolved | Each stand-in `claude` script writes the start log its own way   |
| F3 | Low      | Documentation | Fix | Resolved | `starts` lost its doc comment to `logStarts`                     |

## F1. The live step reports the defect it reproduces as a flake

Medium · Testing · Fix · `internal/server/live_test.go:376`

The step "asked in plain words, Claude messages another session through Ox" reproduces the reported incident: asked in plain words, Haiku used Claude Code's `ListAgents` and `SendMessage` instead of Ox's tools. When that regression returns, Haiku calls `SendMessage` and not `mcp__ox__send_message`, so the step stops at its first check with "Haiku did not call send_message (…); run again". The check for Claude's own messaging tools, which names the actual defect, comes after that `t.Fatalf` and never runs.

The plan reserves "run again" for a turn in which Haiku calls no messaging tool at all. As written, the one failure the step exists to catch reads as model flakiness, and a rerun in which Haiku happens to pick Ox's tool passes while the flags are gone.

### Evidence

Traced `live_test.go:374-382`: `slices.Contains(tools, "mcp__ox__send_message")` is checked first and calls `t.Fatalf` with "run again"; `SendMessage`/`ListAgents` are checked only after it.

### Action

Check for `SendMessage` and `ListAgents` in the turn's tools first, and fail with a message that says Claude used its own messaging. Then `t.Fatalf` with "run again" only when the turn called no messaging tool at all, as the plan states. The step in `TestLive` stays the test that carries it.

## F2. Each stand-in `claude` script writes the start log its own way

Low · Testing · Fix · `internal/server/methods_test.go:388`

Three stand-in scripts (`unresumableSession`, `TestResumeTokenKeptWhenNotStored`, `TestExitDropsWaitingMessages`) each spell out the same shell line that logs a start's argv on one line, and two places parse that log: the `starts` helper and an inline copy in `TestResumeTokenKeptWhenNotStored` (`methods_test.go:448-452`). The format is one rule kept in five places. This change shows the cost: the multi-line system prompt broke the one-line-per-start log, and the fix had to be made three times. The new line itself is correct: `echo "$@" | tr '\n' ' '` folds every newline in the arguments into a space, and `printf` ends the start with one newline.

### Evidence

`git diff internal/server/methods_test.go` changes the same line identically at lines 388, 419, and 889. `TestResumeTokenKeptWhenNotStored` reads the file with `os.ReadFile` and splits it itself instead of calling `starts`.

### Action

Write the logging line once, as a function that returns a stand-in script's first lines for a given log path, and build the three scripts from it. Replace the inline parse in `TestResumeTokenKeptWhenNotStored` with `starts(t, args)`. The existing tests that read the log (`TestUnresumableConversationStartsAnew`, `TestUnresumableConversationForgottenWhenNotStored`, `TestResumeTokenKeptWhenNotStored`, `TestExitDropsWaitingMessages`) carry it; no new test is needed.

## F3. `starts` lost its doc comment to `logStarts`

Low · Documentation · Fix · `internal/server/methods_test.go:370`

The fix for F2 put `logStarts` between the comment `// starts returns the arguments of each start the stand-in Claude recorded.` and `func starts`. That line now opens `logStarts`'s doc comment, so `logStarts` is described as returning recorded arguments, and `starts` has no comment.

### Evidence

`methods_test.go:370-377`: the `starts` comment line sits directly above `logStarts`'s two comment lines, and `func starts` follows `logStarts` after a blank line.

### Action

Move the `starts` comment line back above `func starts`, so each function has only its own comment. No test is needed.

## Checks run

- `make test`: passed.
- `make e2e`: 25 passed, 3 skipped.
- `make live`: passed, including the new step "asked in plain words, Claude messages another session through Ox", and the 3 `approval.spec.ts` specs.
- `TestClaudeRunsAsAnOxSession` against base `00c56b6` in a detached worktree (removed afterwards): fails on the missing `--disallowedTools` and `--settings`, so it checks what the change adds. The `--append-system-prompt` assertion requires `You are Ox session` with the ID and workspace, which the base's tone-only text lacks, and removing any of the three added arguments fails one assertion.
- Probe, `claude` 2.1.328 with Haiku: `--settings={bad` fails with `Settings file not found: /private/tmp/{bad`, so the `=` form passes its value; with `--settings={"crossSessionInbound":"refuse"}` and `--disallowedTools=SendMessage,ListAgents`, Haiku's tools whose names contain "Agent" or "Message" are only `Agent`.
- Headless Chrome through `make dev`: not needed; the change does not alter how the page reaches `oxd`.

## Verification 1

Checked the working tree over base `00c56b6` after the fixes for F1 and F2. `make test` passed, `make e2e` passed (25 passed, 3 skipped), and `make live` passed, including the step "asked in plain words, Claude messages another session through Ox" and the 3 `approval.spec.ts` specs.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `live_test.go:377-382` checks for `SendMessage`/`ListAgents` first with a `t.Fatalf` that names Claude's own messaging, and says "run again" only when `mcp__ox__send_message` is also absent. With the change's flags removed, a turn that uses `SendMessage` now fails as a regression. |
| F2 | Resolved | `logStarts` writes the logging line once, and all three stand-in scripts start with it. `TestResumeTokenKeptWhenNotStored` reads the log through `starts`. The tests that read the log pass. |

New findings: F3.

## Verification 2

Checked the working tree over base `00c56b6` after the fix for F3. Since Verification 1, only a doc comment in `internal/server/methods_test.go` has moved. `make test` and `go vet ./internal/server` passed. `make e2e` and `make live` were not rerun: no code changed since they passed in Verification 1.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F3 | Resolved | `methods_test.go:370-377`: `logStarts` has only its own comment, and `starts` has `// starts returns the arguments of each start the stand-in Claude recorded.` again. |

No new findings.

## Verdict

Ready to commit.
