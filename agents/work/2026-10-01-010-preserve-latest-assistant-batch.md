# Preserve the latest assistant batch during compaction

## Plan

`agents/plans/2026-10-01-010-preserve-latest-assistant-batch.md`

## Summary

Compaction candidates now always leave the newest complete assistant batch
unchanged. The cut-selection and projected-request tests own that new guarantee;
the former ability to compact a transcript with only one uncovered assistant
batch was intentionally removed. Automatic, overflow-retry, manual, OpenAI, and
child-session tests still own their existing orchestration guarantees with an
older compactable batch in each fixture.

## Departures from the plan

- `crates/ox-server/src/subagents.rs` was not listed in the plan's related code,
  but its child-session compaction fixture depended on compacting a single
  assistant batch. The fixture now supplies older material while asserting that
  the newest assistant batch reaches the model request unchanged.

## Automated checks

- `cargo test -p ox-server compaction::tests` — Passed all 18 compaction tests.
- `cargo test -p ox-server` — Passed all 210 Ox server tests and doc tests.
- `make check` — Passed formatting, 282 workspace tests, the workspace build,
  and Clippy. Twelve terminal tests were skipped because they require
  `make e2e`; terminal behavior did not change.
- Earlier `make check` runs stopped on formatting and then exposed a fixture
  whose large tool outcome was excerpted before summarization. Formatting and
  the fixture were corrected before the passing run.

## Manual verification

No live model-provider check was run. The scripted OpenRouter and OpenAI tests
inspect the post-compaction requests directly and cover the changed boundary.
