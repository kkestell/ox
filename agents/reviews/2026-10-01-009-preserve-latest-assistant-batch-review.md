# Preserve the latest assistant batch review

## Scope and coverage

Reviewed commit `bfd8891`, which removes the newest assistant batch from the
compaction candidates so every compaction keeps it unchanged. The review covered
`candidates`, `cut`, `has_candidate`, `input_fits`, and `compact` in
`compaction.rs`; their callers in the prompt run and the manual `/compact`
command; and the changed tests in `compaction.rs`, `acp/prompt.rs`, `acp.rs`,
and `subagents.rs`. Lenses: correctness, testing, simplicity, and documentation.

The saved transcript of subagent `dd00ce0f` was checked against the change: its
checkpoint covered the whole transcript, including the newest assistant batch,
which the new rule keeps. No live provider request was run.

## Fixed

- **The oversized OpenAI summary test no longer reached the summarizer**
  (`crates/ox-server/src/compaction.rs:643`): its fixture had one assistant
  batch, so after this change `compact` found no candidate and returned before
  any summarizer request. The test still passed while checking nothing. A
  temporary request-count assertion confirmed zero summarizer requests. The
  fixture now adds a later answer, and the test asserts that one summarizer
  request is made before the oversized summary is refused.

## Findings

No open findings.

## Checks run

- `cargo test -p ox-server an_oversized_openai_summary_never_becomes_a_checkpoint`
  — failed with the request-count assertion before the fixture fix, passed
  after.
- `make check` — passed. Twelve terminal tests were skipped because they require
  `make e2e`; terminal behavior did not change.

## Verdict

The change follows the approved plan and keeps the newest assistant batch in
every compaction path. One test that the change had silently emptied was fixed.
No open findings remain.
