# Review: two permission modes, Manual and Auto, for every harness

## Scope and coverage

Uncommitted changes in the `two-permission-modes` worktree on base `0b09f4b` (`git merge-base origin/main HEAD`), checked against `agents/plans/2026-11-27-two-permission-modes.md` and the user's later decisions: new sessions default to Manual; Manual asks only about what Claude asks about; stored sessions in a retired mode become Manual and Claude sessions in `auto` stay `auto`; Claude's plan mode is gone, with every piece of code that existed only for it, including `planOf` and plan rendering in old transcripts. Resolves #193.

Step 8 of `/ox-review`, which checks the Tests proved results and spot-checks removals, was skipped: the user dropped it from the workflow (69cefa2 on main). The tests were also checked against the Verification rule added on main in 594ec25: a test passes or fails on the behavior it checks alone, and each step waits for the event it needs.

The change touches harness driving, protocol, and persistence. The reviewer checked each concern itself, with no concern-reviewer children, because each async edge the change alters is small: the Claude adapter's `requires_action` report, the auto answer to `can_use_tool`, and a mode change that no longer waits on an acknowledgement.

Gaps:

- Unconfirmed: if Claude sends two `can_use_tool` requests in a row and Ox auto-answers the first, the `running` Claude reports after that answer could arrive after Ox has reported `requires_action` for the second. The session would then show running while an approval waits. Claude reported `running` after a user's answer in the same way before this change. No reproduction was attempted, and the approval card shows either way.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                            |
| -- | -------- | ------------- | -------- | ------ | ---------------------------------------------------------------- |
| F1 | Low      | Intent        | Fix      | Open: top-level agent | "Tool calls" requirement edited without approved text |
| F2 | Low      | Documentation | Fix      | Open: top-level agent | Source Map does not list `internal/agent/auto.go`     |
| F3 | Low      | Testing       | Fix      | Resolved              | Mode-plus-model test hangs on regression and has a dead check |
| F4 | Low      | Testing       | Rejected | —      | Live check of a start refused on stderr removed                  |
| F5 | Low      | Testing       | Rejected | —      | "A long permission card keeps Submit in view" removed            |

## F1. "Tool calls" requirement edited without approved text

Low · Intent · Fix · `docs/functional-requirements.md:123`

The change removes "and a plan renders as Markdown" from **Tool calls**. The plan's Requirement changes do not include this edit. It follows from the user's decision to delete plan rendering in old transcripts, which makes the old text untrue. Even so, `AGENTS.md` changes a requirement only with text the user approved.

### Evidence

The diff of `docs/functional-requirements.md` matches the plan's Requirement changes word for word: Permission modes added, Retired settings, Working together without approval, Ur's shell commands, and Built-in catalog changed, and Claude's own mode changes removed. It also edits Tool calls.

### Action

The top-level agent brings the user the new Tool calls text ("…expanding it shows the full input and the result's text. Expanding or collapsing a tool call keeps its summary where it was on screen.") with the requirements diff at step 7, and keeps it once the user approves. No code change.

## F2. Source Map does not list `internal/agent/auto.go`

Low · Documentation · Fix · `AGENTS.md:65`

The change adds `internal/agent/auto.go`, which holds `AutoRuns`, the one copy of Auto's rule. The Source Map in `AGENTS.md` lists every file under `internal/agent` but not this one.

### Evidence

`AGENTS.md` lines 65–68 list `agent.go`, `tracker_test.go`, and `tracker.go` under `internal/agent`. `git status` shows `internal/agent/auto.go` as untracked.

### Action

Add `│   │   ├── auto.go            # Auto's shell command rule` above `tracker_test.go` in the Source Map. `AGENTS.md` is meta work, so it goes straight to `main` as the Workflow section says, and its text goes to the other top-level agents with changes in flight.

## F3. Mode-plus-model test hangs on regression and has a dead check

Low · Testing · Fix · `internal/claude/configure_test.go:365`

The second half of `TestPermissionModeNeedsNoControl` checks that a combined model and mode change sends only `set_model`, in two ways. Neither works:

- `input.Len() != 0` runs after `json.NewDecoder(input).Decode`. The decoder's first read takes everything in the buffer, so `input.Len()` is always 0 at that point. The check is dead.
- If a regression brought back a `set_permission_mode` control, the agent would write it only after `set_model` is acknowledged, and would wait on it. The test's blocking `<-result` would then hang until `go test`'s timeout instead of failing with a message.

Both waits in the first half of the test, and the one in `TestConfigureRequiresIdle`, are deterministic: they do not depend on timing. `Configure` calls `done` synchronously, on the caller's goroutine, both for `ErrBusy` and when no control is needed. `replyTo` writes to a channel with a buffer of one, and nothing else runs on another goroutine. So the `select` with `default` fails only when `Configure` did not answer before it returned, which is the behavior under test. Fifty runs with `-race` passed.

### Evidence

`encoding/json`'s `Decoder.refill` reads into a buffer of at least 512 bytes. The control line is under 100 bytes, so one `Read` empties the `bytes.Buffer`. In `controlResponse`, the agent sends the next queued control after an acknowledgement and calls `done` only when none remain.

### Action

After `acknowledge(t, a, wire.RequestID, "success")`, read `result` with a `select` and a `default` that fails with "a model and mode change sent more than set_model", along with `input`. With the fix, `done` runs synchronously inside `acknowledge`. Check `input.Len() == 0` there, after the acknowledgement, where it can see a second control, and drop the dead check after `Decode`. The test stays in `TestPermissionModeNeedsNoControl`.

## F4. Live check of a start refused on stderr removed

Low · Testing · Rejected · `internal/server/live_test.go:390`

`TestLive` no longer configures the misspelled mode `acceptEdit` to check that a Claude start refused on stderr fails with its reason.

### Evidence

`--permission-mode` is now always `manual`, so Ox no longer passes Claude a mode taken from the configuration, and the configuration cannot produce this refusal. `TestStartupFailureOnStderr` (`internal/claude/claude_test.go:314`) still covers the code path that brings a stderr reason to the failed start.

### Action

Rejected: the failure the live check covered can no longer happen through Ox, and the code path keeps its test.

## F5. "A long permission card keeps Submit in view" removed

Low · Testing · Rejected · `cmd/ox/web/e2e/session.spec.ts`

The e2e test that a long approval card keeps Submit in view on a phone is removed.

### Evidence

The test made a card long with an `ExitPlanMode` plan, the only part of an approval card that rendered long content outside the collapsed input `<details>`. Plan rendering is gone by the user's decision, and no requirement states this layout.

### Action

Rejected: no card can be long in the way the test exercised.

## Checks run

- `go test -race ./internal/claude/ -run 'TestPermissionModeNeedsNoControl|TestConfigureRequiresIdle|TestAutoApproval|TestApprovalRouting' -count=50`: passed.
- From `/ox-work`'s final response, on the final tree: `make test` passed; `make e2e` passed (240 passed, 4 skipped); `make live` passed, including the new "auto mode" section, `TestLiveUr`, and `approval.spec.ts`.
- `docs/images/hero-light.png` shows the session in Auto, unchanged by the `hero.json` edit, so `make screenshots` is not needed.

## Verification 1

Checked the same uncommitted tree on base `0b09f4b`. Since the review, only `internal/claude/configure_test.go` changed. The parent's brief was to check that each fix's test exists and passes without removing the fix.

| ID | Status                    | Evidence                                                                                                                                                                                                                                                                                                         |
| -- | ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Open: top-level agent     | Needs the user's approval of the Tool calls text when the top-level agent commits. No implementer work.                                                                                                                                                                                                         |
| F2 | Open: top-level agent     | Lands on `main` after the merge, once `internal/agent/auto.go` exists there. No implementer work.                                                                                                                                                                                                                |
| F3 | Resolved                  | `TestPermissionModeNeedsNoControl` (`configure_test.go:368`) now reads `result` with a `select` and a `default` after acknowledging `set_model`, and checks `input.Len() == 0` there. The dead check after `Decode` is gone. A second control now fails the test at once instead of hanging. |

No new findings. The test-only change touches no other behavior.

Checks: `go test -race ./internal/claude/ -count=1` passed, and `TestPermissionModeNeedsNoControl` and `TestConfigureRequiresIdle` passed 50 runs with `-race`. Nothing else changed since the earlier `make test`, `make e2e`, and `make live`.

## Verdict

Ready to commit. F3 is resolved. F1 and F2 are the top-level agent's: F1 needs the user's approval of the Tool calls text at commit, and F2 lands on `main` after the merge.
