# Review of Ur saving streamed replies

## Scope and coverage

Reviewed the uncommitted Ur implementation and tests, the Ur row in the
architecture, and the removal of the F1 to-do item over
`784d484812b33b808d64817dc9f5b75ea163bca3`. The other working-tree changes were
outside this review. The [plan](../plans/2026-11-22-001-ur-saves-replies-as-they-stream.md)
implements **Ur sessions** without changing the functional requirements.

The session goroutine saves each delta to Ur's file before publishing it.
The server then records the published update before broadcasting it. If the
first write succeeds and the daemon dies before the second, the model can
retain one delta the page never showed.

A user entry separates successive replies, so both existing one-entry replies
and new multi-entry replies reconstruct as single assistant messages. A
successful result reads its text from the same saved entries. A denied save
ends the turn with an error, keeps the earlier saved text, and cancels the
request. Its later events cannot affect the next turn. An unreadable
conversation still follows the existing refusal and next-send behavior.

## Summary

No findings.

## Checks run

- Independently ran the new tests against the base implementation in a
  temporary copy. `TestResumeDuringTurn` omitted the partial reply,
  `TestResume` found one entry for the whole reply, and `TestSaveFailure/reply`
  saw the unsaved delta and lost the earlier fragment from model context.
- Independently confirmed in a temporary copy that `TestSaveFailure/reply`
  fails when publish precedes save and that `TestTwoTurns` fails when the
  completed result drops the reconstructed reply.
- The implementer's `make test` passed. In two independent review runs, Go
  tests and linters passed, and the unchanged Node stack test
  `one terminal Ctrl-C signals each service once` timed out waiting for a
  synthetic shell service to exit. The test passed in isolation and the full
  `make test` rerun passed after the browser suite finished.
- The implementer's final-tree `make e2e` passed with 43 passing tests and
  three expected live approval skips.
- The implementer's focused `TestLiveUr` passed against OpenRouter.
- `git diff --check` passed for the scoped files.

## Verdict

Ready to commit.
