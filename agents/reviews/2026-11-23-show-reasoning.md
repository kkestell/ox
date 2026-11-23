# Review: show Claude's and Ur's reasoning in the transcript

## Scope and coverage

Uncommitted changes on `show-reasoning` against base `e48899f` (`git merge-base origin/main HEAD`, also `HEAD`), with 21 modified files and the untracked plan. Checked against `agents/plans/2026-11-23-show-reasoning.md`, which resolves #49. The user approved the plan's requirement text and its three Check your assumptions entries as written. Implementation decision passed on by the top-level agent: `TestLiveUr`'s shell turn asks for the prime among 391, 403, and 409, and the reasoning in that turn is logged rather than asserted, because DeepSeek V4.1 Flash reasons only intermittently under Ur's request.

The **Reasoning** requirement in `docs/functional-requirements.md` matches the plan's Requirement changes word for word. No other requirement changed. Open `bug` issues #5, #6, #33, and #34 are untouched by the change.

Gaps:

- Whether any configured Ur model besides DeepSeek V4.1 Flash refuses a tool-loop request without its reasoning was not probed. F1 is rated on DeepSeek's behavior.

## Summary

| ID | Severity | Category    | Action | Status   | Title                                                      |
| -- | -------- | ----------- | ------ | -------- | ---------------------------------------------------------- |
| F1 | Low      | Correctness | Fix    | Resolved | Input taken mid-turn strips the turn's reasoning from Ur's requests |
| F2 | Low      | Testing     | Fix    | Resolved | No test protects the copy of saved reasoning in `conversation` |
| F3 | Low      | Testing     | Fix    | Resolved | `TestLiveUr` counts reasoning after the call as returned reasoning |

## F1. Input taken mid-turn strips the turn's reasoning from Ur's requests

Low · Correctness · Fix · `internal/ur/ur.go:418`

The plan's Goal is that Ur returns the current turn's reasoning in its requests. `body` sends `reasoning_details` only on messages after the conversation's last `user` message. An input sent during a tool loop is saved as a `user` message before the turn's next request (`request`), inside the same turn: its `prompt` arrives before the turn's `result`. That message becomes the last `user` message, so the assistant message that called the tools loses its reasoning in the follow-up request of the very turn it belongs to.

With DeepSeek V4.1 Flash the request still succeeds, as it did before the change, so the cost is the model losing its own reasoning mid-loop. A model that requires its reasoning back in a tool loop would refuse the request.

### Evidence

Traced from source. `TestShellInputBetweenSteps` shows the shape: the second request's roles are `user, assistant, tool, user`, with the mid-turn input last, and `prompt p2` precedes `result p2`. `body` sets `lastUser` to that final message and clears `ReasoningDetails` on every message before it, including the tool-calling assistant message at index 2 of the request.

### Action

Strip reasoning by where the turn began, not by the last `user` message. Record the history length when the turn begins (for example `turn.start`, set in `begin`) and clear `ReasoningDetails` only on history entries before it, before calling `conversation`. Note the new field in the commit body, per `AGENTS.md`. Keep the test in `internal/ur/ur_test.go`'s `TestShellInputBetweenSteps`: the first response streams a `reasoning.text` fragment before its call, and the second request's assistant message carries it. `TestTwoTurns` continues to guard that an earlier turn's reasoning stays out.

## F2. No test protects the copy of saved reasoning in `conversation`

Low · Testing · Fix · `internal/ur/ur.go:450`

`conversation` copies each entry's `ReasoningDetails` before merging, because `appendReasoning` writes into the last detail in place. Without the copy, the first `body` of a turn merges later fragments into the first saved entry's detail in `a.history`, and every later request of the turn sends the reasoning text repeated (`List both.both.`). No test reaches a third request in a turn whose response reasoned in more than one fragment.

### Evidence

Removed line 450 and ran `go test ./internal/ur/`: every test passed. Restored the line.

### Action

Extend `TestShellAuto` in `internal/ur/ur_test.go` so the second response makes another shell call, and assert that the third request's first assistant message still carries one detail with `List both.`. Confirm the test fails with line 450 removed.

## F3. `TestLiveUr` counts reasoning after the call as returned reasoning

Low · Testing · Fix · `internal/server/live_test.go:974`

The comment says that when the model reasoned before its call, the follow-up request returned that reasoning and the turn's success shows OpenRouter accepted it. The loop counts a `reasoning_delta` anywhere in the turn, including the reasoning of the response after the command ran, which no request returns. Its log line then says the follow-up request returned no reasoning only when the whole turn had none, and stays silent when the model reasoned only after the call, so a passing run does not say whether the case the comment describes happened.

### Evidence

Traced from source: `c.received(id)` holds every update of the turn, and the loop stops at nothing; the `tool_use` update that separates the two responses is ignored.

### Action

Count only `reasoning_delta` updates with text that arrive before the turn's `tool_use`, and log when there are none, so the log states whether the follow-up request carried reasoning. The decision to log rather than assert stands.

## Checks run

- Plan's requirement text compared with `docs/functional-requirements.md`: identical.
- Spot checks, each restored afterward:
  - Removed the strip loop in `body`: `TestTwoTurns` fails.
  - Removed the `reasoning_delta` join in `history.entries`: `TestHistoryMergesTextAndKeepsConversation` fails.
  - Removed the reasoning copy in `conversation`: all Ur tests pass (F2).
- OpenRouter probe, DeepSeek V4.1 Flash on `inference-net/us`: a request with a contentless assistant message, as an interrupt during reasoning now saves, succeeds.
- `go vet ./internal/...` and `go test ./internal/ur/ ./internal/server/ ./internal/claude/` on the restored tree: pass.
- `make test`, `make e2e`, and `make live` ran on the final tree and passed, per `/ox-work`'s report. A headless Chrome check is not required: the change does not alter how the page reaches `oxd`.

## Verification 1

Uncommitted tree on `e48899f` after `/ox-work fix`. Checked each fix at its cause, proved its test, and reviewed the changes since the review for regressions.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `turn.start` records the history length in `begin`; `body` clears reasoning only on a clone's entries before it. History only grows, so the index stays valid. `TestShellInputBetweenSteps` asserts the calling message keeps `Say hi.`, and fails when `body` strips every entry before the last. |
| F2 | Resolved | `TestShellAuto` makes a second call round and asserts the third request still carries one `List both.` detail; it fails with the copy in `conversation` removed. |
| F3 | Resolved | `TestLiveUr` counts only `reasoning_delta` before the turn's `tool_use`, and its log says the follow-up request returned no reasoning only then. |

No new findings. `go vet ./internal/...` and `go test ./internal/...` pass on the restored tree. `make test`, `make e2e`, and `make live` passed on the final tree, per `/ox-work fix`'s report.

## Verdict

Ready to commit. F1, F2, and F3 are resolved.
