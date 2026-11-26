# Review: chat request order and status behind file tabs

## Scope and coverage

Uncommitted changes in the worktree on `chat-request-order-and-status`, against `origin/main`, checked against `agents/plans/2026-11-26-chat-request-order-and-status.md` (issue #148, audit F4 and F5). The three requirement edits (Stop, Failed creation, Chat status behind other tabs) match the plan's text word for word. No finding survived. The change is web-client only, so no child sessions ran.

Gaps:

- `make live` and the Go suites were not run beyond `go test ./internal/claude -count=5`, since no Go code changed.

## Summary

| ID | Severity | Category     | Action   | Status | Title                                                     |
| -- | -------- | ------------ | -------- | ------ | --------------------------------------------------------- |
| F1 | Low      | Requirements | Rejected | —      | Lost-connection error outranks the recovery notice's dot  |
| F2 | Low      | Testing      | Rejected | —      | `internal/claude` `--permission-mode` failure             |

## F1. Lost-connection error outranks the recovery notice's dot

Low · Requirements · Rejected · `cmd/ox/web/src/ox.tsx:271`

After a lost creation reply and a relay restart, the control holds both the lost-connection error and the "Session recovered" notice. `attention()` returns the error first, so the dot on the hidden chat tab reads "Lost the connection to ox. Reconnecting."

### Evidence

A temporary probe in the recovered-creation spec printed the dot's label after 1.5 s: `Lost the connection to ox. Reconnecting.` The spec was restored. The chat shows both the error and the notice, as it did before this change; the change only exposes the pair through the dot.

### Action

Rejected. **Every request settles** says an error stays until that view's next request, so the error is correct while the chat shows it. The dot reports what the chat shows, with the error first, as the plan decided. The spec asserts only that the dot exists, which is what the behavior guarantees. If the user wants the error to clear on recovery, that is a separate change to `recover()` and the requirement, and a product question.

## F2. `internal/claude` `--permission-mode` failure

Low · Testing · Rejected · —

The implementer saw one `make test` failure in `internal/claude` that passed on rerun with no Go changes.

### Evidence

`go test ./internal/claude -count=5` passed. No open `bug` issue records it (#30, which covered permission-mode checks, is closed). It is unreproduced and unrelated to this web-client change.

### Action

Rejected as unconfirmed, per the reviewer's rule not to chase a failure it cannot reproduce. No change here.

## Checks run

- `npx playwright test workspace session`: 228 passed on the working tree.
- `npx tsc -p e2e`: clean. `npx biome check .`: warnings only.
- `go test ./internal/claude -count=5`: passed.
- Spot check: `free()` changed to let a `send` in flight not block Stop. "Stop waits for a steering send to be accepted, then drops its prompt" failed. The file was restored.
- `make live`: not run; the change does not alter how Ox drives Claude Code.

## Verdict

Ready to commit. No `Fix` or `Record` findings.
