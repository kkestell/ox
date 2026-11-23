# Review: the dev stack sends every stop signal its services get

## Scope and coverage

The uncommitted changes to `cmd/ox/web/stack.mjs` and `cmd/ox/web/stack.test.mjs` on `dev-shutdown`, base `394daa4`, against `agents/plans/2026-11-22-dev-shutdown.md`. Also read `cmd/ox/web/dev.mjs`, the shutdown in `cmd/oxd/main.go`, and issue #29, which the change resolves.

Gaps:

- No manual `make dev` run in this review. The parent asked for a proportionate review, and the implementer's manual run covers it.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                  |
| -- | -------- | ------------- | -------- | ------ | ------------------------------------------------------ |
| F1 | Low      | Documentation | Fix      | Resolved | `stop`'s comment is left unwrapped                     |
| F2 | Low      | Documentation | Fix      | Resolved | `signalStack`'s comment reads "received received"      |
| F3 | Low      | Simplicity    | Rejected | —      | Second-SIGINT forwarding repeats `stopProcess`'s check |

## F1. `stop`'s comment is left unwrapped

Low · Documentation · Fix · `cmd/ox/web/stack.mjs:66`

Removing "unless send is false" joined the comment's first line with the next one. The result is about 130 columns long, then wraps after "any". Every other comment in the file wraps at 80.

### Evidence

`cmd/ox/web/stack.mjs:66-67` in the diff.

### Action

Rewrap the comment at 80 columns. It needs no test.

## F2. `signalStack`'s comment reads "received received"

Low · Documentation · Fix · `cmd/ox/web/stack.test.mjs:165`

"expects each service to have received received alone" uses the parameter name as a word in the sentence, so it reads like a typo.

### Evidence

`cmd/ox/web/stack.test.mjs:165-166`.

### Action

Reword it, for example: "sends signal to the stack and expects each service to have logged only received, the signal it got." It needs no test.

## F3. Second-SIGINT forwarding repeats `stopProcess`'s check

Low · Simplicity · Rejected · — · `cmd/ox/web/stack.mjs:156`

The second-SIGINT loop checks `exitCode` and `signalCode` before `proc.kill`, the same check `stopProcess` makes.

### Action

Rejected. A second SIGINT must signal without adding to `stops`, so it cannot call `retire`, and pulling a helper out of one line of shared logic costs more than repeating it. The behavior is correct, and it is the implementer's stated open choice.

## Checks run

- `node --test cmd/ox/web/stack.test.mjs`: 16 passed.
- Removing the second-SIGINT forwarding: "a second Ctrl-C forwards SIGINT" fails.
- Removing `detached: true`: "a terminal Ctrl-C reaches only the stack" fails.
- Removing the SIGHUP handler: "a closed terminal stops every service" fails.
- Every removal was reverted, and the diffstat matches the reviewed tree.
- I accepted the implementer's Tests proved results and their `make test`, `make e2e`, and manual `make dev` runs. Under `AGENTS.md`, `make live` is not required because the change does not alter how Ox drives Claude Code.

## Verification 1

Checked the uncommitted tree on `dev-shutdown` after the fixes. `node --test cmd/ox/web/stack.test.mjs` passes 16 of 16. Only the two comments changed.

| ID | Status   | Evidence                                                                                                                         |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `cmd/ox/web/stack.mjs:66-67` now wraps at 80 columns                                                                             |
| F2 | Open     | The wording is fixed, but `cmd/ox/web/stack.test.mjs:165` is now about 95 columns. Rewrap lines 164-167 at 80, as F1 requires. |

No new findings.

## Verification 2

Checked the uncommitted tree after the rewrap. `node --test cmd/ox/web/stack.test.mjs` passes 16 of 16. No comment in either file runs past 80 columns.

| ID | Status   | Evidence                                              |
| -- | -------- | ----------------------------------------------------- |
| F2 | Resolved | `cmd/ox/web/stack.test.mjs:164-167` wraps at 80 columns |

No new findings.

## Verdict

Ready to commit: F1 and F2 are resolved, and F3 is rejected. The change delivers the plan's Goal, keeps its Decisions, makes no requirement changes, and resolves #29.
