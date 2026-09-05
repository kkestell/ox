# Recover Interrupted Tool Effects Without Redispatch

## Sources

- `docs/spec.md#session-lifecycle-and-recovery` — owns the unknown-outcome,
  no-redispatch, and persistence-failure behavior.
- `eng/roadmap.md#interrupted-effect-recovery` — owns this slice and its fault
  injection gates.
- `eng/architecture.md#context-and-durable-projections` — owns durable dispatch
  intent, completion ordering, and parent, sibling, and child coverage.
- `internal/agent/state.go`, `loop.go`, `subagent.go`, and `agent.go` — current
  suspended exchange, tool scheduling, child context, recovery, checkpoint, and
  replay paths.
- `internal/agent/store.go:sessionLog.append` and
  `internal/e2e/harness_test.go:process.kill` — current persistence and
  hard-kill seams for deterministic fault injection.
- `~/src/references/repos/personal/gamma/internal/agent/runner.go:startTool`,
  `execute`, and `updateTool`, and `engine.go:recoverTurn` — prior art for
  persisting lifecycle transitions around execution and closing interrupted
  calls without executing them again.

## Goal

Make the durable log distinguish a tool call that has not begun, one whose
effect may have happened, and one whose result is safely recorded. Recovery must
reuse recorded results, report uncertain effects as unknown, and dispatch only
calls that are durably known not to have started.

## Implementation

- `internal/agent/state.go` — add validated per-call dispatch and completion
  records scoped by turn and optional parent call. Retain enough call identity,
  target, approval, result, and delegation data to fold checkpoints, account for
  changed files once, and reconstruct interrupted parent and child updates.
  Reject starts without a matching suspended exchange or child, duplicate or
  out-of-order transitions, mismatched results, and a completed model exchange
  that disagrees with its per-call records.
- `internal/agent/loop.go` — sync a dispatch record immediately before each
  `Tool.Execute`, including local and client-delegated execution, and sync its
  completion before publishing a terminal ACP update or advancing model history.
  Propagate either persistence failure out of the batch, stop later groups from
  dispatching, and never turn a poisoned session into a successful tool
  exchange. Preserve already-running parallel siblings long enough to collect
  their outcomes while keeping cancellation responsive.
- `internal/agent/loop.go` and `subagent.go` — make live and recovered batches
  consult durable per-call progress. Reuse completed results, convert a started
  call with no completion to a failed unknown-outcome result, and execute only
  untouched calls. Apply the same boundary to child calls so a restarted parent
  cannot recreate a child effect.
- `internal/agent/agent.go` and `state.go` — when activation interrupts an open
  nonrecoverable exchange, durably close its calls before the turn outcome:
  preserve completed results, mark started calls unknown, and mark untouched
  calls interrupted before start. A recoverable permission wait keeps its
  current generation and progress; answering it cannot redispatch a started
  sibling. Replay terminal call state once, including nested calls that were not
  absorbed into a completed delegation, without treating an unknown effect as a
  successful file change.

## Tests

- `internal/agent/state_test.go` — cover valid parent and child lifecycle folds,
  checkpoint round trips, interrupted projection and replay, plus malformed
  scope, ordering, duplicate, and result-mismatch records.
- `internal/agent/agent_test.go` and `integration/agent_loop_test.go` — fail the
  session log before dispatch and after a test tool performs an effect. Prove
  the first executor never runs, the second effect runs once without false
  success, later siblings stop, cancellation returns, and a separate session
  completes.
- `integration/agent_loop_test.go` — seed recovered permission batches with
  completed, started, and untouched siblings and prove only untouched calls run;
  repeat the unknown-outcome case inside a delegated task.
- `internal/e2e/session_test.go` — hard-kill the real binary after local and
  client-terminal effects but before exchange completion. Load and replay must
  identify the calls as unknown, never issue the effect again, preserve recorded
  completed siblings, and keep stdout valid ACP.

## Decisions

- Record the dispatch boundary for every tool, including reads. One uniform
  lifecycle avoids guessing which current or future tool may have an external
  effect; only changed-file accounting continues to distinguish successful edit
  results.
- Port Gamma's durable start-before-handler and terminal-after-handler ordering,
  but represent a missing terminal outcome explicitly as unknown. Gamma's
  generic interrupted result is not evidence that an external effect failed.
