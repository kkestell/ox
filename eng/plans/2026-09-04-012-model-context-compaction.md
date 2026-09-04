# Compact Model Context Durably

## Sources

- `eng/roadmap.md#model-context-compaction` — owns the compaction scope and
  history, restart, usage, and protected-prefix gates.
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#provider-boundary` — own durable history, ACP replay,
  frozen request configuration, and provider translation.
- `internal/agent/agent.go`, `internal/agent/loop.go`, and
  `internal/agent/state.go` — current prompt lifecycle, request assembly, usage
  publication, append-before-mutate records, checkpoints, and replay.
- `~/src/references/repos/personal/beta/src/compaction.rs:plan_compaction` and
  its tests, plus `src/agent.rs:maybe_compact` and `src/session.rs:rebuild` —
  prior art for threshold planning, complete tool groups, summary splices, and
  data-driven restart recovery.
- `~/src/references/repos/personal/delta/cmd/fleur/compaction.go` and
  `compaction_test.go` — prior art for the 80% trigger, 20% retained tail,
  compact plain-text summary input, failure atomicity, and durable context
  checkpoints that leave the transcript intact.
- `~/src/references/repos/personal/eta/internal/agent/compact.go` — prior art
  for a continuation-focused structured summarizer prompt.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json:$defs.UsageUpdate`
  — `used` is the number of tokens currently in context, while cost is
  cumulative.

## Goal

Before a new turn starts, compact a session whose last measured provider prompt
used at least 80% of its context window. Persist the exact compacted model
history while retaining the complete records used for ACP replay.

## Implementation

- `internal/agent/compact.go` — add pure request-size estimation,
  head/middle/tail planning, complete assistant-tool-result grouping, and
  plain-text transcript rendering. Keep the first user message and a roughly 20%
  recent tail, and summarize only when replacing the middle materially shrinks
  the request. Include the ordinary system prompt and tool declarations in
  occupancy estimates, but never in summary input or replacement history.
- `internal/agent/agent.go` — after claiming an idle session but before
  committing the next user message, run the summarizer with the activation's
  frozen model/provider settings, the dedicated summarizer system prompt, and no
  tools. An empty, failed, or cancelled summary leaves the log and history
  untouched. Commit a successful compaction before publishing its usage.
- `internal/agent/state.go` — add a compaction record containing the splice
  boundaries, exact summary message, summarizer usage, and estimated compacted
  occupancy. Accept it only with no durable open turn and only at complete
  message-group boundaries. Fold it into provider history, cumulative usage and
  cost, current context occupancy, cloning, and checkpoint projection. Replay
  the record only as a `usage_update`; the earlier user, assistant, and tool
  records remain the authoritative ACP transcript.
- `internal/agent/event.go`, `internal/agent/adapter.go`, and replay usage code
  — represent context occupancy explicitly. Normal model responses publish and
  persist `PromptTokens`, not completion-inclusive `TotalTokens`; compaction
  publishes its estimated post-splice size and cumulative cost.
- `eng/architecture.md` and `eng/roadmap.md` — record the durable split between
  full replay records and compacted provider history, then remove the completed
  slice from the roadmap.

## Tests

- `internal/agent/compact_test.go` — port Beta and Delta's planning tests for
  the 80/20 boundary, protected head, indivisible tool exchanges, no-useful-
  middle cases, request estimation, and transcript rendering without reasoning
  details or binary content.
- `internal/agent/state_test.go` — prove valid splices survive a checkpoint and
  restart, invalid/open-turn splices fail, summarizer usage is counted once,
  compacted occupancy is restored, and replay still emits the full transcript
  followed by the compacted usage update.
- `integration/agent_loop_test.go` and `internal/e2e/session_test.go` — drive a
  small-window session across multiple turns; verify the summary request has no
  ordinary system prompt or tools, the next request uses the recorded summary,
  restart does not recompute it, `session/load` remains lossless, and summary
  failure or cancellation does not partly compact the session.
- `internal/e2e/browser/harness.ts` and `lifecycle.spec.ts` — make the mock
  model window configurable and prove the pinned client accepts the live
  compacted `usage_update`; update the existing ordinary usage assertion to
  prompt-token occupancy.

## Decisions

- Do not port Delta's oversized-tool-result truncation or a client-visible
  compaction transcript event. A result too large to leave a useful middle is
  left intact for the provider to reject clearly; Ox does not silently discard
  model-visible tool output, and ACP v1 has no compaction update.
