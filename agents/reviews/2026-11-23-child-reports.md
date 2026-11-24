# Review: Report each child's turn to its parent

## Scope and coverage

Uncommitted changes in the `child-reports` worktree on base `360bf19` (the
merge base with `origin/main`; `HEAD` is the same commit), checked against
`agents/plans/2026-11-23-child-reports.md`. The tree holds 16 modified files
and the untracked plan. Reviewed: `internal/server/session.go`, `mcp.go`, and
their tests; `internal/agent/agent.go`; `internal/store/store.go`;
`internal/claude/claude.go`; the live section; the web client's `Prompt`
variant, `PromptBubble`, and the new spec; and the four documents. Open `bug`
issues #5, #6, #33, #34, and #55 are unaffected.

The requirements diff carries the plan's added **Child reports** and changed
**Only the user sends** text word for word, and nothing else.

Gaps:

- The told-parent race in F1 was reproduced with the fake harness and an
  injected delay, not with Claude Code.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                               |
| -- | -------- | ----------- | -------- | ------ | ------------------------------------------------------------------- |
| F1 | Medium   | Testing     | Fix      | Resolved | Fake echo time scales with the wrappers and slows every suite       |
| F2 | Low      | Ownership   | Fix      | Resolved | A `send_message` answered after its turn suppresses the next report |
| F3 | Low      | Testing     | Rejected | —      | The e2e spec does not show a pending reports input                  |
| F4 | Low      | Conventions | Rejected | —      | `send_message` answers from the MCP goroutine                       |
| F5 | Low      | Correctness | Rejected | —      | An empty successful reply sends an empty report                     |
| F6 | Low      | Settling    | Rejected | —      | Delivery stops once `oxd` is shutting down                          |
| F7 | Low      | Documentation | Fix    | Resolved | The e2e spec's header still describes word-at-a-time streaming      |

## F1. Fake echo time scales with the wrappers and slows every suite

Medium · Testing · Fix · `internal/fake/fake.go:160`

The fake streams its echo one word per 50 ms, and its echo is the whole
`Content`. Every child's first message now carries a ~70-word wrapper, and
every report delivery a ~60-word wrapper plus each report's up to 400
characters, so each child turn and each parent report turn costs 3 to 6
seconds of fixed sleep. `internal/server` went from about 28 s to 68 s even
after three tests were made parallel, and each e2e test built on `family()`
takes about 20 s and needs `test.slow()` and 15 to 30 s timeouts. Nothing in
these tests depends on how long the echo takes; they wait on it only because
the fake's turn length is proportional to its input. `make test` and `make e2e`
run several times per change, so this is a standing cost on every later change.

### Evidence

The implementer's timings in its final response; `fake.go` `begin` splits the
reply with `strings.SplitAfter(a.reply, " ")` and `tick` sends one word per
`step`. `family()` in `session.spec.ts` now waits for two reports from Child
before the family is idle.

### Action

Make a fake turn's length independent of its input: stream the reply in a fixed,
small number of steps (for example, words grouped so any reply takes at most a
handful of ticks), keeping word-wise streaming visible. Tests that need a turn
to stay running must then hold it explicitly rather than with a long prompt:
`TestChildReports`' `strings.Repeat("word ", 100)` and `("word ", 20)` turns,
and the e2e Stop spec's `words(200)`, can hold the turn with
`approve <tool> <input>`, which waits until answered or interrupted. Then drop
`test.slow()` and the extended timeouts from `family()`. The kept check is the
existing suites passing at their earlier duration; report the before and after
times for `internal/server` and a `family()` spec.

## F2. A `send_message` answered after its turn suppresses the next report

Low · Ownership · Fix · `internal/server/mcp.go:295`

`sendMessage` posts `told = toldSent` to the child once the parent accepts.
If the child's turn has already ended by then, because the user interrupted it
or its process exited while the parent was accepting, `result` has already reset
`told`, and the post sets it again with no step in progress. The child's next
turn, if it makes no tool call, then sends no report, though its last action was
not a message to its parent, contrary to **Child reports**. The design says
`told` lives only within the step that made the call; the post can outlive that
step.

### Evidence

A temporary test, with a 500 ms sleep injected before the caller post: child
`one` in `requires_action` (`approve Bash {}`) calls `send_message` to its
parent; the test interrupts `one` during the sleep; the parent takes
`Turn failed: Interrupted`; the test then sends `one` the prompt `short`.
`one` replied `You said: short`, and after 2 s the parent's history held no
report of it. Probe and fault reverted.

The same state arises deterministically without a delay when a child that is
idle calls `send_message` to its parent through its MCP attachment: the post
sets `toldSent` with no turn running.

### Action

The child's goroutine decides whether the fact still applies: in the caller
post, set `toldSent` only while the caller is not `free()`, since a free child
has no step in progress that the call could belong to. Keep a test in
`TestChildReports`: with child `one` idle, call `send_message` to the parent
from `one`'s attachment, then send `one` a plain prompt and require the parent
to take its report with `reportsTaken`. It fails today.

## F3. The e2e spec does not show a pending reports input

Low · Testing · Rejected · `cmd/ox/web/e2e/session.spec.ts:1286`

Rejected: delivery runs only when the parent is free, so a reports input is
pending only while a parked parent's harness starts, which the fake does at
once. Showing it would need an injected delay inside `oxd`, not the page. The
pending input renders through the same queue path and `PromptBubble` branch as
the delivered one, and the delivered article and its reload are tested.

## F4. `send_message` answers from the MCP goroutine

Low · Conventions · Rejected · `internal/server/mcp.go:284`

Rejected: a message between unrelated sessions is now answered from the MCP
call's goroutine after the recipient's `acceptMessage` returns, not from the
recipient's goroutine. The answer goes to the calling Claude over the MCP
connection, which carries no `session/update`, so no update can land on the
wrong side of it, and the answer carries no state a later update could
contradict.

## F5. An empty successful reply sends an empty report

Low · Correctness · Rejected · `internal/server/session.go:839`

Rejected: the plan's Report text decision says every result produces a report.
The Claude harness already skips empty bookkeeping results, so an empty
`result` is a real turn that ended without text, and its report still tells the
parent that the child's turn ended.

## F6. Delivery stops once `oxd` is shutting down

Low · Settling · Rejected · `internal/server/session.go:896`

Rejected: **Child reports** says reports not yet delivered are dropped when
`oxd` stops, so the `closed` check matches the requirement and keeps a report
from starting a parked parent during shutdown. Every wait in `deliverReports`
ends: `ask` returns the zero value once a child's goroutine has ended.

## F7. The e2e spec's header still describes word-at-a-time streaming

Low · Documentation · Fix · `cmd/ox/web/e2e/session.spec.ts:1`

Found in verification 1. The header comment says the fake "echoes each prompt
back a word at a time" and lists its special prompts without the new `wait
<milliseconds>`. The fake now streams a reply in at most four runs of words,
and the Stop spec in the same file depends on `wait`. The comment restates a
fact that `internal/fake` and the `fake` row of `docs/architecture.md` own, so
it drifts with every change to the fake.

### Evidence

`session.spec.ts:1-6` against `fake.go` `split` and `begin`, and the
architecture row the fix updated.

### Action

Replace the restated list with a pointer: the specs run against the fake
harness, whose prompts `internal/fake` describes. No test; a comment.

## Checks run

- From `/ox-work`'s final response, on the final tree: `make test` passed;
  `make e2e` passed (156 passed, 4 live specs skipped); `make live` passed, and
  `TestLive/child_reports` passed three more times. Each new test failed on the
  base revision.
- F2 probe with an injected delay: failed as described; probe and fault
  reverted.
- Spot-check: `report` with the `told` guard removed: `TestChildReports` fails
  (`took a report from … beyond`).
- Spot-check: `deliverReports` with the running-child check removed:
  `TestChildReports` fails (the parent took the two reports in two prompts).
- Both removals reverted; the tree matches the reviewed diff.

## Verification 1

Checked the uncommitted tree on base `360bf19` after the fixes: 17 modified
files, now including `internal/fake/fake.go`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | The fake streams each reply and its reasoning in at most four pieces (`split`), so a turn's length no longer grows with its input. `go test -race -count=1 ./internal/server` took 24.1 s (68 s at review, about 28 s on base). `family()` no longer needs `test.slow()` or extended timeouts. In place of `approve`, long turns use a new `wait <milliseconds>` prompt, because a child in `requires_action` does not hold reports back and so cannot stand in for a running child. That deviation is sound, and the `fake` row in `docs/architecture.md` records it. |
| F2 | Resolved | The caller post sets `toldSent` only while the child is not `free()` (`mcp.go:316`). The new `TestChildReports` step "a message from an idle child leaves its next report alone" fails with the guard removed (`no matching message within 5s` at `mcp_test.go:1131`) and passes with it. |

New finding: F7.

Checks: `make test` and `make e2e` passed on the final tree, as `/ox-work fix`
reported. The implementer did not rerun `make live`. The F2 guard now depends
on Claude reporting a non-free state during an MCP call, so this review ran
`OX_LIVE=1 go test -run 'TestLive/child_reports'` on the final tree; it passed
(25.8 s), as did `TestLiveUr`. The rest of `make live` exercises nothing
changed since its last full pass. F2 guard removal reverted; the tree matches
the implementer's.

## Verification 2

Checked the uncommitted tree on base `360bf19` after the F7 fix. The only
change since verification 1 is the header comment of
`cmd/ox/web/e2e/session.spec.ts`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F7 | Resolved | The header now says the specs run against the fake harness in `internal/fake`, whose `Send` describes the prompts it answers, and restates none of them. Comment only; no test applies. |

No new findings. Checks: `npm run lint` passed. The change is a comment, so the
earlier `make test`, `make e2e`, and live results on this tree still stand.

## Verdict

Ready to commit. F1, F2, and F7 are resolved.
