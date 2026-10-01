# Preserve the latest assistant batch during compaction

## Goal

OX-0031: compaction may cover the newest assistant batch when that batch is
larger than the recent allowance or when reserving room for a full summary. The
next model request then receives the agent's immediate work only through the
summary.

When this work is done, every compaction keeps the newest complete assistant
batch unchanged. Older recent entries continue to stay unchanged while they fit
the recent allowance. This is one compaction rule for main sessions and child
sessions; it does not depend on what the next completion does.

## Related code

- `crates/ox-server/src/compaction.rs` — `candidates`, `has_candidate`,
  `input_fits`, and `cut` choose the covered prefix; the compaction tests own
  the request-projection and checkpoint guarantees.
- `crates/ox-server/src/acp/prompt.rs` — prompt-run tests exercise automatic
  compaction and the retry after a model provider reports context overflow.
- `crates/ox-server/src/acp.rs` — ACP tests exercise the manual `/compact`
  command.
- `agents/architecture.md` — assigns model-request projection and summary
  selection to compaction and defines an assistant batch as the atomic saved
  unit.
- `agents/testing.md` — requires each changed guarantee to have one owning test
  at its closest stable boundary.

## Decisions

- **The newest assistant batch is never covered.** A covered prefix may end
  after any earlier assistant batch, but not after the newest one. The newest
  assistant batch, including all of its tool calls and tool outcomes, remains
  among the recent entries and reaches the next model request unchanged.
- **One batch is the minimum, not the complete retention policy.** The existing
  recent allowance still keeps additional recent entries when they fit. If the
  newest assistant batch alone exceeds that allowance, `cut` chooses the deepest
  earlier candidate and keeps the batch anyway.
- **A transcript needs two uncovered assistant batches to compact.** With zero
  or one assistant batch after the latest compaction checkpoint, there is no
  valid candidate. `has_candidate`, `input_fits`, automatic compaction, manual
  compaction, and context-overflow retry all use that same rule.
- **Compaction does not break the invariant to force a request to fit.** If a
  summary beside the newest assistant batch cannot produce a smaller admitted
  request, `compact` leaves the transcript unchanged. The existing context error
  remains the outcome when the request is over admission. Handling one
  intrinsically oversized assistant batch is outside this change.
- **No prompt-run or subagent exception is added.** The compaction component
  owns the invariant, following the boundary in `agents/architecture.md`.

## Naming

- `assistant batch` — one model message and the outcome of each of its tool
  calls, saved together.
- `recent entries` — transcript entries after the covered prefix, sent to the
  model unchanged.
- `recent allowance` — the existing estimated-token allowance used to decide how
  many entries in addition to the newest assistant batch stay recent.

## Test plan

- In `compaction.rs`, change
  `the_cut_keeps_the_newest_entries_within_the_recent_allowance` to own the new
  selection guarantee. Cover small recent batches, a newest batch larger than
  the allowance, a projection with little summary room, an earlier compaction
  checkpoint, one assistant batch, and no assistant batch. Every selected cut
  must precede the newest assistant batch; the last two cases have no candidate.
- Add or strengthen one compaction test with large older material and a newest
  assistant batch containing recognizable assistant text, a tool call, and its
  outcome. After compaction, assert that the older material is summarized and
  the complete newest batch appears unchanged in the projected model request.
- Extend the `input_fits` cases to show that prospective input is rejected when
  it could fit only by covering the newest assistant batch.
- Update the automatic-compaction and context-overflow tests in `acp/prompt.rs`
  to provide older compactable material followed by a newest protected assistant
  batch. Keep their existing orchestration guarantees and assert the protected
  batch reaches the post-compaction model request.
- Update the manual-compaction tests in `acp.rs` to give `/compact` an older
  candidate while leaving the newest assistant batch unchanged.
- Adjust compaction failure and checkpoint fixtures that currently contain only
  one assistant batch so they still reach the behavior each test owns. Inspect
  the complete test diff as required by `agents/testing.md` and record which
  guarantees changed or moved in the work log.

## Implementation plan

1. In `compaction.rs`, make `candidates` omit the cut immediately after the
   newest assistant batch after the latest compaction checkpoint. Keep
   `has_candidate` and `input_fits` based on that function so every compaction
   entry point applies the same invariant.
2. Update `cut` and its comments so the deepest fallback candidate still keeps
   the newest assistant batch even when that batch exceeds the recent allowance
   or a full summary would exceed admission.
3. Add and update the `compaction.rs` tests above, including the observable
   projected-request regression for OX-0031.
4. Update the automatic, overflow-retry, OpenAI, and manual-compaction fixtures
   in `acp/prompt.rs` and `acp.rs` for the new minimum of two uncovered
   assistant batches, without changing the guarantees those tests own.
5. Mark OX-0031 fixed in `agents/issues.csv` and check it off in
   `agents/todo.md` after the implementation and checks pass.
