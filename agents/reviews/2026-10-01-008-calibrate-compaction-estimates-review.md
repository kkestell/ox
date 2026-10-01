# Calibrate compaction estimates review

## Scope and coverage

Reviewed commit `b30d7fe`, which calibrates request token estimates from the
latest assistant batch's reported input usage. The review covered the estimate,
admission, cut, and compaction paths in `compaction.rs`; the prompt-run callers;
the OpenAI and OpenRouter usage parsers; and the new regression test. Lenses:
correctness, performance, testing, readability, simplicity, and Rust idioms.

No live provider request was run.

## Fixed

- **Zero reported input tokens disable compaction**
  (`crates/ox-server/src/compaction.rs:58`): a provider response with zero input
  tokens made bytes per token infinite, so every later request estimated as zero
  and context-overflow recovery rejected its own summary as no smaller. Zero now
  falls back to the default ratio, with the case covered by the existing
  table-driven estimate test.

## Findings

No open findings.

## Checks run

- `cargo test -p ox-server compaction` — passed, 20 tests.
- `make check` — passed after formatting this review; the initial run stopped at
  that Markdown formatting check.

## Verdict

The calibrated estimate follows the approved plan and remains usable when a
provider reports unusable token usage. No open findings remain.
