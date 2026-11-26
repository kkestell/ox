# Review: Claude thinking summaries setting

## Scope and coverage

Reviewed the uncommitted changes on `claude-thinking-summaries-setting` against base `37db075` (`git merge-base origin/main HEAD`), with no plan. The request: add a setting to the Ox configuration file that turns Claude's thinking summaries on and off, off by default, because Opus 5.5 ends more turns with thinking and no reply text when `--thinking-display=summarized` is passed.

The change touches harness driving through one flag and adds no state, timer, or asynchronous edge, so the review ran in this session with no concern reviewers.

Gaps:

- `make live` and `make e2e` were not run, at the parent session's request; it runs them after the rebase.

## Summary

| ID | Severity | Category | Action | Status   | Title                                                         |
| -- | -------- | -------- | ------ | -------- | ------------------------------------------------------------- |
| F1 | Medium   | Intent   | Fix    | Resolved | Reasoning requirement no longer holds for Claude by default   |
| F2 | Low      | Testing  | Fix    | Resolved | No test sees `thinking_summaries` reach the Claude harness    |

## F1. Reasoning requirement no longer holds for Claude by default

Medium · Intent · Fix · Resolved · `docs/functional-requirements.md:123`

The **Reasoning** requirement says that when Claude or Ur reasons, the transcript shows a collapsed Thinking row where it happened. With `thinking_summaries` off, which is now the default, Claude's `thinking_delta` events are all empty, and `internal/claude/agent.go:247` drops empty deltas, so a Claude session shows no Thinking row at all. The user asked for exactly this, so the behavior is correct. The requirement now misstates it, and a sweep will report it as a defect.

### Evidence

`docs/harnesses/claude-code.md:324` records that without `--thinking-display summarized` every `thinking_delta` is empty and the final thinking block is `{"type":"thinking","thinking":""}`. `internal/claude/agent.go:247` publishes `reasoning_delta` only when `delta.Thinking != ""`, and no other path in `internal/claude` publishes reasoning. `internal/config/default.json` sets `thinking_summaries` to `false`, and a user's existing configuration file lacks the key, so both default to off.

### Action

The top-level agent asks the user to approve requirement text and applies it word for word. Proposed text:

> - **Reasoning.** When Ur reasons, or Claude reasons with thinking summaries
>   turned on in the configuration, the transcript shows a collapsed Thinking
>   row where it happened; expanding it shows the reasoning in full, streaming
>   in as Markdown under the same rules as replies, and keeps its summary where
>   it was on screen.

No test changes: `TestThinkingSummaries` already guards the flag's absence when the setting is off.

## F2. No test sees `thinking_summaries` reach the Claude harness

Low · Testing · Fix · Resolved · `cmd/oxd/main.go:67`

Setting `thinking_summaries` to `true` in the configuration file is the whole user-facing behavior, and it reaches Claude through one line in `main()`: `claude.Harness(cfg.Harnesses["claude"].ThinkingSummaries)`. No test covers that line. `TestThinkingSummaries` starts the process directly with the flag, and `TestLive` builds `claude.Harness(true)` itself, so either keeps passing if `main()` passes `false`.

### Evidence

Replacing the argument with `false` in `cmd/oxd/main.go` left `go test ./cmd/oxd` passing. The edit was reverted.

### Action

Move the harness map out of `main()` into a function in `cmd/oxd` that takes the configuration and Ur's paths, and have `main()` call it. Add a test in `cmd/oxd/main_test.go` that builds the map from a configuration with `thinking_summaries: true` for `claude`, starts the `claude` harness against a stand-in `claude` on `PATH` that records its arguments, as `internal/claude/claude_test.go`'s `standIn` does, and checks for `--thinking-display=summarized`. One configuration with the setting on is enough. The default-off case is already covered.

## Checks run

- Spot-check: making `startProcess` always pass `--thinking-display=summarized` fails `TestThinkingSummaries`. Reverted.
- Spot-check: removing the `thinking_summaries` check from `checkConfig` fails `TestConfigOffersOnlyWhatHarnessesTake`. Reverted.
- Spot-check: passing `false` to `claude.Harness` in `main()` leaves `go test ./cmd/oxd` passing (F2). Reverted.
- The implementer reports that `make test` passes, that the new tests fail with injected faults, and that the Go sections of `TestLive` and `TestLiveUr` pass. Not re-run here.
- `make live` (including `approval.spec.ts`) and `make e2e` have not run on the final tree. The parent session will run them after the rebase.

## Verification 1

Checked the working tree on base `37db075` after the fixes. Ran `go test -race -count=1 ./cmd/oxd` (passes), and fault-injected F2's fix. `make live` and `make e2e` are still to run after the rebase, as the parent session arranged.

| ID | Status   | Evidence                                                                                                                                                                                                                                                       |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `docs/functional-requirements.md` carries the proposed Reasoning text word for word, reflowed by dprint. The parent session reports that the user approved it.                                                                                                |
| F2 | Resolved | `main()` builds its harnesses with `newHarnesses`. `TestConfiguredThinkingSummariesReachClaude` starts the configured `claude` harness against a stand-in and finds `--thinking-display=summarized`. It fails when `newHarnesses` passes `false`, and passes with the fix. |

No new findings.
