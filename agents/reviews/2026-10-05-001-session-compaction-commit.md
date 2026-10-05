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
- **A compaction at the start of a turn replaced the user's new message**
  (`internal/transcript/transcript.go`, `RequestEntries`): when the previous
  turn's last response crossed the threshold, the compaction ran after the new
  turn start was saved, so the model read the user's newest request, and any
  image attached to it, only as the summary's paraphrase. Requests now send the
  latest turn start before the compaction right after the summary, word for
  word, and the image check in `Start` reads the same entries. Fixed in fa09d43
  after the user chose this rule.

## Findings

None open.

## Checks run

- `make check` — passed, again after the fix in fa09d43.
- Ran `transcript_metrics` from `evals/eval.py` on a scratch database with one
  assistant batch and two compactions (one without usage): 3 requests, input
  tokens and cost summed across the batch and the priced compaction.

## Verdict

The compaction works as the plan describes and its tests cover the main paths.
Both findings are fixed: the evaluation metrics count compaction requests, and
the user's latest message reaches the model word for word after a compaction.
