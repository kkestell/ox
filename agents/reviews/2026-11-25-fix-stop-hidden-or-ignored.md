# One Send/Stop button that stops reliably

## Scope and coverage

The uncommitted changes in the worktree on branch `fix-stop-hidden-or-ignored`,
base `b4b380d` (`git merge-base origin/main HEAD`, also `HEAD`), against
`agents/plans/2026-11-25-fix-stop-hidden-or-ignored.md` (issue #111). The tree
holds the 18 modified files `/ox-work` listed and the untracked plan. Traced:
the Claude agent's `Interrupt`, `settleStops` and its three callers
(`sessionState`, `commandStarted`, `end`), `controlResponse` during a
configuration, `interruptSession`, Ur's and the fake's `Interrupt`, the page's
`stopping`, `canSend`, `sendable`, and the composer button in both views, the
changed e2e specs, the live `interrupt` section, and the four documents.

Gaps:

- No live check of an interrupt written during a configuration. The unit case
  in `TestConfigurationKeepsReportedState` covers Ox's side; the plan relies on
  `controlResponse` ignoring the interrupt's `request_id`, which the code
  confirms.

## Summary

| ID | Severity | Category      | Action | Status | Title                                                    |
| -- | -------- | ------------- | ------ | ------ | -------------------------------------------------------- |
| F1 | Low      | Intent        | Fix    | Open   | Send is enabled with an empty composer while Stop is in flight |
| F2 | Low      | Documentation | Fix    | Open   | Docs misstate which taken input answers an interrupt     |

## F1. Send is enabled with an empty composer while Stop is in flight

Low · Intent · Fix · `cmd/ox/web/src/SessionView.tsx:144`

The plan's Requirements decision says that while the interrupt is in flight
"the button is Send, unavailable while the composer is empty", and its test
plan asks the first Stop spec to check that "Send shows disabled". The button's
`disabled` is `!control.canSend()`, and `canSend` does not read `sendable`, so
after Stop is pressed the composer shows an enabled Send that does nothing when
pressed. The spec was written to assert only `toBeVisible`, which passes either
way.

### Evidence

Changing the spec's assertion after `interrupt.arrived` to
`expect(send(page)).toBeDisabled()` fails: the locator resolves 21 times to
`<button … aria-label="Send">` with value `enabled`. The edit was reverted.

### Action

Disable the session composer's Send whenever there is nothing to send:
`disabled={!control.canSend() || !control.sendable()}`. This is ChatGPT's and
Claude Desktop's form, and it also covers an idle session with an empty
composer. In the e2e spec "Send becomes Stop once the page's send is accepted,
and stops it", assert `toBeDisabled()` on Send while the interrupt is held, in
place of `toBeVisible()`. Adjust any spec that expects an enabled Send with an
empty composer.

## F2. Docs misstate which taken input answers an interrupt

Low · Documentation · Fix · `docs/architecture.md:358`

`docs/architecture.md` says the reply waits until "Claude takes an input". The
code settles on any taken input except the one written and untaken when the
interrupt was sent (`settleStops(taken)` keeps a stop whose `target` is
`taken`), so the architecture doc states the rule wrongly.

`docs/harnesses/claude-code.md` ("Interrupt") says that with `cancel_queued`
written right after the prompt, "the prompt runs as without `cancel_queued`".
Read alone, "runs" says Claude carries the prompt out, while the preceding
bullet, and the live `interrupt` section's `Interrupted` result, show that
Claude takes it and stops it at once.

### Evidence

Traced `settleStops` in `internal/claude/prompt.go` against the two passages.
`TestStopDropsQueuedPrompt`'s last added case shows the target's take leaving
the stop waiting until `idle`.

### Action

In `docs/architecture.md`, "Interrupts": the reply waits until Claude reports
`idle`, Claude takes an input other than the one written when the interrupt
was sent, or the agent ends. In `docs/harnesses/claude-code.md`: "it can find
Claude already taking it, and Claude stops it at once as without
`cancel_queued`: `command_lifecycle started`, an `aborted_streaming` `result`,
and `idle`." No test.

## Checks run

- `go test -race ./internal/claude/ ./internal/ur/ ./internal/server/`: passed.
- `npm run e2e -- session -g "Send becomes Stop once"` with a temporary
  `toBeDisabled` assertion: failed as F1 states; the spec was restored.
- `/ox-work` reports `make test`, `make e2e` (203), and `make live` passed on
  the final tree, and the new unit and e2e cases failing with each old behavior
  restored. Those cover every behavior the plan adds: the reply after `idle`,
  after another taken input, and at the agent's end; the interrupt written
  during a configuration; the target's take not settling; the single button;
  typing turning Stop into Send; and Stop returning only on the reply.

## Verdict

`Fixes needed`: F1 and F2.
