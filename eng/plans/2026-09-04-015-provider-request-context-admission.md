# Admit Every Provider Request Within Context

## Sources

- `eng/roadmap.md#provider-request-context-admission` — owns the parent, tool
  continuation, child, failure, durability, and replay gates.
- `docs/spec.md#context-continuity` — owns request budgeting, safe compaction
  boundaries, protected context, persistence atomicity, and bounded failures.
- `eng/architecture.md#provider-boundary` and
  `eng/architecture.md#context-and-durable-projections` — place admission in
  agent orchestration and require the provider projection to remain separate
  from ACP replay.
- `internal/agent/compact.go`, `loop.go`, `subagent.go`, and `state.go` —
  current idle-only compaction, parent and child request assembly,
  append-before-mutate records, usage accounting, and checkpoints.
- `integration/agent_loop_test.go:TestPromptCompactsModelHistoryBeforeTheNextTurn`
  and
  `internal/e2e/session_test.go:TestSessionCompactionSurvivesRestartWithoutChangingReplay`
  — existing compaction and process-restart acceptance seams.
- `~/src/references/repos/personal/beta/src/agent.rs:maybe_compact` and
  `src/compaction.rs:plan_compaction` — prior art for compacting between tool
  groups before the next request; `src/subagent.rs` shows children inheriting
  the same compaction policy.
- `~/src/references/repos/personal/delta/cmd/fleur/compaction.go` and
  `compaction_test.go` — prior art for failure atomicity, durable projections,
  oversized recent groups, and conservative request estimates.

## Goal

Admit every parent and child provider request against the selected model's
context window. Compact long in-progress tool loops at durable, complete
message-group boundaries while keeping ACP replay lossless.

## Implementation

- `internal/agent/compact.go` — replace the last-response threshold check with
  one admission path that sizes the request about to be sent, including its
  system instructions, tool schemas, current messages, and configured
  `max_tokens` reserve. Use checked arithmetic and the existing conservative
  text estimate. Return a clear context-limit problem when the protected prefix
  or newest complete group cannot fit; return a distinct actionable problem for
  image or audio content that Ox cannot size conservatively instead of guessing
  from base64 bytes. Attempt at most one summary per admission, validate the
  post-summary request before sending it, and leave the prior projection intact
  on empty/refused/failed/cancelled summaries.
- `internal/agent/loop.go` — assemble each primary request through admission,
  after the pending user message or complete assistant/tool group is durable and
  before every provider call. Publish compacted occupancy and cumulative usage
  once, preserve the original request count semantics, and fail the accepted
  turn cleanly when admission cannot produce a fitting request.
- `internal/agent/subagent.go` — give each child the same admission loop and
  compact its private history between complete tool groups. Keep child tool
  calls out of parent model context, but retain the exact child projection and
  summarizer usage under the invoking parent tool-call identity.
- `internal/agent/state.go` and `internal/agent/agent.go` — generalize the
  compaction record and checkpoint projection so a primary or child compaction
  can commit during an open turn at a complete provider boundary. Persist child
  history as it advances rather than only inside the final delegation result;
  make the final result consume that durable projection so calls and usage are
  not counted twice. Validate scope, parent identity, splice boundaries, tool
  pairing, occupancy, and cumulative usage while folding. Permit checkpoints
  after these compaction records, including the open-turn and suspended
  projection required to reconstruct the exact boundary after a crash. Keep all
  compaction and child-projection records invisible to ACP transcript replay.
- `eng/architecture.md` — replace the idle-only limitation with the implemented
  request-admission and scoped durable-projection boundary.
- `eng/roadmap.md` — replace the completed summary with the evaluation baseline,
  remove the reviewed context-and-diagnostics and evaluation milestones, and
  remove this slice after its gates pass while leaving interrupted-effect
  recovery as the remaining context-continuity work.

## Tests

- Extend `internal/agent/compact_test.go` with request-admission cases for a
  pending prompt, tool schemas, configured output reserve, checked overflow,
  unsupported multimodal sizing, an oversized protected prefix, an irreducible
  recent group, and a summary that still does not fit.
- Extend `internal/agent/state_test.go` and store tests with parent and child
  compaction during open turns, invalid partial tool groups and scope IDs,
  checkpoint parity at each boundary, cumulative usage counted once, and
  unchanged full ACP replay.
- Extend `integration/agent_loop_test.go` with a long single-turn tool loop and
  a long child that compact before their next requests. Cover summary failure,
  empty output, cancellation, oversized initial input, and a child result that
  resumes into the parent without exposing child history.
- Add a focused process test in `internal/e2e` that stops and reloads at parent
  and child compaction boundaries, then proves the folded provider projections,
  complete tool pairs, usage, and replay match the pre-crash state.

## Decisions

- Admission uses configured `max_tokens` as the requested output reserve. When
  it is unset, no output amount was requested, so Ox budgets the full input and
  leaves the provider default unchanged.
- This slice persists provider-boundary child progress but does not infer that
  an interrupted external effect is safe to repeat. Dispatch/completion intent
  and unknown-outcome recovery remain owned by the next roadmap slice.
