# Mid-turn steering review

## Scope and coverage

Reviewed the uncommitted changes on `main` over base `30aed41`: the 18 modified files in `internal/agent`, `internal/claude`, `internal/fake`, `internal/server` tests, `cmd/ox/web`, and `docs`. Checked against `agents/plans/2026-11-19-002-mid-turn-steering.md`, whose requirement changes ("One prompt at a time", "Stop", with Stop beside Send) the user approved. Traced the callers the change depends on: `sendSession`, `session.update` and history recording, the relay's event stream (send replies and notifications share one ordered oxd connection, so a send's reply reaches the page before that prompt's `prompt_cancelled`), `load`, `receive`, and `createSessionControl`.

`docs/functional-requirements.md` carries the two approved texts word for word and nothing else.

The implementer's deviations from the plan:

- The live test runs `sleep 3 && touch steer.txt` instead of `sleep 3 && echo ok`. Accepted: Claude auto-allows a read-only `echo`, so no approval would arrive to hold the turn. `touch` needs approval in manual mode and writes only into the test's temporary workspace. Both live cases pass.
- Specs that waited for idle with `expect(send(page)).toBeVisible()` now wait with `expect(stop(page)).toHaveCount(0)`. Accepted: Send is always visible now, so the old assertion no longer observes idle, and Stop's absence observes it the way Send's presence did before.
- In the fake harness a steered prompt only echoes its text and never raises an approval. Accepted: the plan's fake-harness decision says it streams "You said: <text>", and no test needs an approval from a steered prompt.

Gaps:

- No `make dev` check in headless Chrome: the page reaches `oxd` through the same methods and transport as before. The e2e specs exercise the new behavior in headless Chrome.

## Summary

| ID | Severity | Category     | Action   | Status   | Title                                                       |
| -- | -------- | ------------ | -------- | -------- | ----------------------------------------------------------- |
| F1 | Medium   | Testing      | Fix      | Resolved | No test keeps dropped text off pages that did not send it   |
| F2 | Medium   | Testing      | Fix      | Resolved | No test clears a stale pending bubble on reconnect          |
| F3 | Low      | Construction | Fix      | Resolved | The queued-prompt check is spelled out at five call sites   |
| F4 | Low      | Requirements | Rejected | —        | An oxd shutdown with a prompt queued loses its text         |

## F1. No test keeps dropped text off pages that did not send it

Medium · Testing · Fix · Resolved · `cmd/ox/web/src/ox.tsx:211`

The approved Stop requirement says a dropped prompt's text returns to "the composer of the page that sent it". `restore` enforces that with `messageId === sent`. Every page with a control for the session receives the same `prompt_cancelled`, so without that condition every open page would get the text pasted into its composer. The only spec that reaches `restore` uses one page, which is both the sender and the page that stops.

### Evidence

Replaced the condition with one that is always true and ran `npm run e2e -- session`: all 19 specs passed.

### Action

Add an e2e spec in `session.spec.ts` with two pages on the same session (`context.newPage()`, as "on reconnect each session waits for its own settings" does). Page A starts `ask`, sends "steer", and page B presses Stop. Page A's composer then holds "steer", page B's composer stays empty, and both pages lose the pending bubble.

## F2. No test clears a stale pending bubble on reconnect

Medium · Testing · Fix · Resolved · `cmd/ox/web/src/ox.tsx:303`

After a reconnect, `load` brings a loaded session up to date in place, and `loaded.queued = session.queued` clears a pending prompt that Claude took or dropped while the page was disconnected. Without it, the page keeps the dimmed bubble and Send stays disabled until another update for that prompt arrives, which never happens. This breaks "Lost connection" and "One prompt at a time". The reload spec uses `page.reload()`, which builds a fresh session and never reaches the in-place path.

### Evidence

Removed the line and ran `npm run e2e -- session`: all 19 specs passed.

### Action

Add an e2e spec in `session.spec.ts`. Page A starts `ask` and sends "steer". Hold page A's `session/get` and call `services.restartRelay()`, as "a reconnect keeps an expanded tool call open" does. While page A is disconnected, a second page answers the approval with Allow, so the fake harness takes "steer". Release the hold. Page A then shows "steer" as a normal `.prompt`, no `.prompt.queued`, and an enabled Send.

## F3. The queued-prompt check is spelled out at five call sites

Low · Construction · Fix · Resolved · `internal/claude/prompt.go:20`

"Is a prompt queued" is decided separately in `Send` (`a.phase == prompting && a.prompt.queued != nil`), `Interrupt` (the same), `isQueued`, `sessionState` (the same), `exit` (`a.prompt.queued != nil`, relying on the enclosing `case prompting`), and `Snapshot` (`a.prompt != nil && a.prompt.queued != nil`, which tests the pointer where the others test the phase). These are two spellings of the invariant that `a.prompt` exists only in `prompting`, so each caller restates it. A future caller that tests `a.prompt.queued` outside `prompting` dereferences nil.

### Evidence

Traced from the source (`grep -n "prompt.queued" internal/claude/*.go`). No current call site is wrong.

### Action

Add one accessor, such as `func (a *Agent) queued() *agent.Prompt`, which returns `a.prompt.queued` in `prompting` and nil otherwise. Have `Send`, `Interrupt`, `isQueued`, `sessionState`, `exit`, and `Snapshot` read it. `TestPromptSentWhileClaudeWorks` and `TestStopDropsQueuedPrompt` already cover every site and carry the change. No new test is needed.

## F4. An oxd shutdown with a prompt queued loses its text

Low · Requirements · Rejected · `internal/claude/agent.go:208`

Before this change a written prompt was recorded in history at `Send`, so it survived an `oxd` restart. Now an untaken prompt exists only in memory. A shutdown drops it with `prompt_cancelled` as the connections close, and a page that misses the update gets nothing back.

### Action

Rejected: the plan decides that a prompt enters the history only when Claude takes it, and its Lost connection concern accepts that a page disconnected when the drop happens does not restore the text, at the cost of retyping it.

## Checks run

- `make test`: passed.
- `make e2e`: 21 passed, 3 skipped (the live `approval.spec.ts`).
- `make live`: `TestLive` passed, including "a prompt sent while Claude works joins the turn" and "Stop drops a prompt Claude has not taken"; `approval.spec.ts` passed 3 of 3.
- New Go tests against the base revision, in a detached worktree since removed: `TestPromptSentWhileClaudeWorks`, `TestStopDropsQueuedPrompt`, the changed `TestPromptReleasedAfterClaudeTakesIt`, and `TestPromptQueuedWhileClaudeWorks` fail to build there (`agent.Prompt`, `Snapshot.Queued` undefined). The new e2e specs need Send beside Stop and `.prompt.queued`, which the base page lacks.
- Removed conditions, each restored afterwards:
  - `Interrupt` sending `cancel_queued` only while a prompt is queued: `go test ./internal/claude` fails.
  - `sessionState` waiting on a stale idle while a prompt is queued: fails.
  - `commandCancelled` matching only the queued prompt's ID: fails.
  - `restore` matching the page's sent ID: survives (F1).
  - The in-place `load` setting `queued`: survives (F2).

## Verification 1

Checked the working tree over base `30aed41` after the fixes: a `queued()` accessor in `internal/claude/prompt.go` and two new specs in `cmd/ox/web/e2e/session.spec.ts`. `cmd/ox/web/src/ox.tsx` is unchanged since the review.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | "a dropped prompt returns only to the page that sent it" stops from a second page and checks that the text returns only to the sender's composer. It fails with `restore`'s sent-ID condition removed and passes with it. |
| F2 | Resolved | "a reconnect clears a pending prompt Claude took while the page was away" keeps the page disconnected while a second page lets the harness take the prompt, then reconnects. It fails with the in-place `loaded.queued` assignment removed and passes with it. |
| F3 | Resolved | `queued()` returns the queued prompt only in `prompting`. `Send`, `Interrupt`, `isQueued`, `sessionState`, `exit`, and `Snapshot` read it. The remaining direct uses of `a.prompt.queued` are writes on paths already in `prompting`. `go test ./internal/claude` fails when `queued()` always returns nil. |

No new findings.

Checks run: `make test` passed. `make e2e` passed (23 passed, 3 skipped). `make live` passed (`TestLive`, plus `approval.spec.ts` 3 of 3). Every mutation was reverted.

## Verdict

Ready to commit. F1, F2, and F3 are resolved, and F4 is rejected.
