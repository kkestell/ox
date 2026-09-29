# Ratatui text and redraw

## Plan

`agents/plans/2026-09-29-001-ratatui-text-and-redraw.md`

## Summary

The plan's goal is met. Transcript rows and composer editing use grapheme
clusters, and redraws reuse each unchanged item's formatted rows.

## Automated checks

- `make check` — passed.
- `make check-docs` — passed.
- `make e2e` — passed all 10 tmux tests.
- `git diff --check` — passed.

The test diff adds composer editing, composer wrapping, rendered transcript
cell, and visible page coverage. Existing chunk, tool update, and Thinking tests
now render before an update or tick to exercise cache invalidation. No test
guarantee was removed or moved.

## Manual verification

1. Timed 100 redraws of 500 unchanged user items, each containing eight copies
   of a 50-character sentence, at width 80 with a 15-row page. A temporary
   ignored test in `transcript.rs` used this command before and after the
   change:

   ```sh
   cargo test -p ox --bin ox redraw_timing -- --ignored --nocapture
   ```

   The original full-format redraws took 3.633 seconds. Cached visible-page
   redraws took 0.112 seconds, about 32 times faster. The timing test was
   removed after measurement.
