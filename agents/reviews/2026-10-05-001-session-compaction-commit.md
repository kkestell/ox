# Review: commit f6117ee, session compaction

## Scope and coverage

Reviewed commit f6117ee against its plan,
`agents/plans/2026-10-05-002-session-compaction.md`: the compaction entry in
`internal/transcript`, `Store.AppendCompaction`, the request encoding in
`internal/openrouter`, `Turn.compact` and `Turn.summarize` in `internal/agent`,
the live and replayed tool call in `internal/server`, and their tests. Also read
the code that consumes saved transcripts outside the diff: the terminal client's
tool call rendering in `internal/tui`, `scripts/export_session.py`, and the
evaluation metrics in `evals/eval.py`. No live model request was made, so the
quality of real summaries is not covered.

## Fixed

- **Evaluation metrics skip compaction requests** (`evals/eval.py:374`):
  `transcript_metrics` read only `assistant_batch` rows, so a build that
  compacts reported fewer requests, fewer tokens, and a lower cost than it
  spent, which skews every comparison against a build that does not compact. It
  now counts each compaction as a request and adds its saved usage.

## Findings

### medium

#### correctness

- **A compaction at the start of a turn replaces the user's new message**
  (`internal/agent/agent.go:291`): when the previous turn's last response
  crossed the threshold, the trigger fires on the first iteration of the next
  turn, after `Start` has saved the new turn start. The summary request
  (`agent.go:371`) includes that message, the compaction is saved after it, and
  the request that answers the user carries only the system prompt and the
  summary. The model sees the user's newest request, and any image attached to
  it, only as the summary's paraphrase.
  `TestCompactionReplacesTheTranscriptInLaterRequests` shows it: the request
  after compaction holds no "Continue" message. This is likely the most common
  time the trigger fires, since every turn ends with a response. A fix is to
  keep the new turn start verbatim after the compaction (for example, by
  summarizing the transcript before the new turn start and sending that turn
  start again after the summary), or to compact before saving the turn start.
  Needs a decision: the plan says a compaction covers every entry before it, and
  either fix changes that rule or where the compaction is saved.

## Checks run

- `make check` — passed.
- Ran `transcript_metrics` from `evals/eval.py` on a scratch database with one
  assistant batch and two compactions (one without usage): 3 requests, input
  tokens and cost summed across the batch and the priced compaction.

## Verdict

The compaction works as the plan describes and its tests cover the main paths.
One fix was made to the evaluation metrics. One medium finding remains: a
compaction at the start of a turn hides the user's new message from the model,
which needs a decision on where a compaction sits relative to that message.
