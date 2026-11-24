# Review: session titles

## Scope and coverage

Uncommitted changes on `session-titles` over base `0ac289f` (origin/main, including #64), checked against `agents/plans/2026-11-23-session-titles.md`. The working tree held the 27 modified files, `internal/claude/ask.go`, `internal/claude/ask_test.go`, and the plan, untracked.

The plan's requirement changes appear in `docs/functional-requirements.md` word for word. The hand-resolved rebase in `internal/server/session.go` keeps #64's `update` switch unchanged: only a prompt with neither `from` nor `reports` on a top-level session reaches `labelFrom`, and `report` still runs for children.

The implementer's open choices:

- The `send_message` case in `TestSendMessage` (`methods_test.go`) is the test the plan meant: it is the one where one session messages another. Accepted.
- `titling.effort: ""` in the "model without efforts" fixture is required, since `CheckModel` refuses an effort on a model that has none. Accepted.
- `cleanTitle` shortening before it strips quotes is F1.
- Naming `New`'s local `titler` is fine.
- The rebase resolution is correct, as above.

Gaps:

- `/ox-work`'s final response was not passed on, so `make test` and `make e2e` were run here. The workflow lead reports that `/ox-work` ran `make live` and it passed.

## Summary

| ID | Severity | Category    | Action | Status   | Title                                                         |
| -- | -------- | ----------- | ------ | -------- | ------------------------------------------------------------- |
| F1 | Low      | Correctness | Fix    | Resolved | A quoted title near the limit keeps its opening quote         |
| F2 | Low      | Ownership   | Fix    | Resolved | A titling process can start after `oxd` stops waiting for it  |
| F3 | Low      | Testing     | Fix    | Resolved | No test protects the 4000-rune cut of the titling input       |

## F1. A quoted title near the limit keeps its opening quote

Low · Correctness · Fix · `internal/server/session.go:97`

`cleanTitle` calls `firstLine`, which shortens, before it strips enclosing quotes. A reply such as `"` + 47 runes + `"` is 49 runes, so `shorten` cuts it to 47 runes plus `…`. The last rune is then `…`, not the quote, and the label shows the opening `"`. A model told "at most 48 characters" that quotes its answer anyway lands exactly here.

### Evidence

Traced: `firstLine` → `shorten` returns `"xxxx…`, and `title[len(title)-1] == title[0]` is false.

### Action

Strip before shortening: take the reply's first trimmed line uncut, strip one pair of enclosing quotes, trim, then `shorten`. Add a case to `TestSessionLabel`: a reply of a quoted 48-rune title gives that title without quotes.

## F2. A titling process can start after `oxd` stops waiting for it

Low · Ownership · Fix · `internal/server/session.go:658`

`generateTitle` cancels its context from a watcher goroutine that selects on `sess.done`. `run` closes `done`, then `Close` returns, then `cmd/oxd` runs `children.WaitAll`. Between `done` closing and the watcher running `cancel`, the titling goroutine can still reach `children.Start` with a live context. If that happens after `WaitAll` has seen an empty set, `oxd` exits and leaves a `claude -p` process running. The architecture says `oxd` holds the socket until every process in the `children` set has exited. The cause is that the titling call's lifetime is not bounded by the session goroutine that owns the session.

### Evidence

Traced from source. `exec.Cmd.Start` refuses a context that is already cancelled. A process started before cancellation is registered under the set's lock and killed by the context. Only the window between `close(sess.done)` and the watcher's `cancel` lets a start succeed unseen.

### Action

Give the session a context that `run` cancels before it closes `done`: `defer close(sess.done)` then `defer sess.cancel()`, created in `newSession`. `generateTitle` derives `context.WithTimeout(sess.ctx, titleLimit)` and drops the watcher goroutine. A start then either precedes the cancel, so it is in the set before `Close` returns and is killed, or follows it and is refused. `TestSessionLabel`'s destroy case keeps covering cancellation.

## F3. No test protects the 4000-rune cut of the titling input

Low · Testing · Fix · `internal/server/session.go:654`

Removing the cut in `generateTitle` fails no test. Every titling input in the tests is short.

### Evidence

Read: `TestSessionLabel` asserts the asked text only for short prompts, and the long case uses a 60-rune line.

### Action

In `TestSessionLabel`'s long case (`methods_test.go:773`), make the line longer than `titleInputRunes`. Assert both the shortened label and that `Ask` receives exactly the first `titleInputRunes` runes.

## Checks run

- `make test`: passed.
- `make e2e`: passed, 156 passed and 4 skipped.
- Spot checks by reading: removing `case sess.title != ""`, `title: record.Title`, the quote strip, `shorten`, or the `done` watcher each fails `TestSessionLabel`. Removing the `titleInputRunes` cut fails nothing (F3).
- `make live`: passed in `/ox-work`, as the workflow lead reports, including `TestLive`'s and `TestLiveUr`'s title checks and the approval spec.

## Verification 1

Checked the uncommitted tree after `/ox-work fix`, with changes in `internal/server/session.go` and `internal/server/methods_test.go`. I reverted each fix in turn and ran `TestSessionLabel` under the race detector, then restored the file. `make test` passed on the fixed tree. The implementer reports that `make e2e` (156 passed, 4 skipped) and `make live` passed.

| ID | Status   | Evidence                                                                                                                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `cleanTitle` strips quotes from the uncut `leadingLine`, then shortens. The quoted 48-rune title case in `TestSessionLabel` fails when `firstLine` is restored in its place.           |
| F2 | Resolved | `newSession` creates `sess.ctx`, `run` cancels it before `close(sess.done)`, and `generateTitle` derives from it without a watcher. The destroy case fails without `defer sess.cancel()`. |
| F3 | Resolved | The long case uses `titleInputRunes`+10 runes and asserts that `Ask` receives the first `titleInputRunes`. It fails with the cut removed.                                          |

No new findings.

## Verification 2

Checked commit `1fc0170`, rebased onto `d079e3a` (#66), as `git diff d079e3a..1fc0170`.

In `session.go`, the hand-resolved conflict keeps #66's `state` field and `state: agent.StateParked` beside `title` and `title: record.Title`. `run` is the only writer of `state`, `firstLine`, and `title`, and it writes each one under `Server.mu` and broadcasts it in the same critical section. `update` publishes the state in one locked section, then calls `labelFrom`. `setTitle` runs on `run` and takes the lock. `workspace/list` and `session/created` read `labelLocked()` and `state` together under `Server.mu`, so a page receives each label and state either in the list or in a later broadcast, never both and never neither. In `docs/protocol.md`'s notifications table, only the `session/labeled` row differs from #66's table. The requirements diff is unchanged. Every server test except `TestTitlingHarnessNeedsAsk` builds through `newServer`. `make test` passed on this tree. The workflow lead reports that `make e2e` passed (160 passed, 4 skipped).

| ID | Status   | Evidence                                          |
| -- | -------- | ------------------------------------------------- |
| F1 | Resolved | Unchanged by the rebase; `cleanTitle` as verified |
| F2 | Resolved | Unchanged by the rebase; `run` cancels `sess.ctx` before closing `done` |
| F3 | Resolved | Unchanged by the rebase                           |

No new findings.

## Verdict

`Ready to commit`.
