# Simpler file tools

## Plan

`agents/plans/2026-09-29-007-simpler-file-tools.md`

## Summary

The plan's goal is met: `write_file` and `edit_file` replace `apply_patch`.

## Decisions

- Argument decoding requires a JSON object before validating the strict argument
  structs, so positional arrays cannot bypass the tool schemas.
- File-tool tests own literal writes, unique edits, failed matches leaving files
  untouched, unchanged results, and filesystem rejection. The link-swap write
  regression moved to the workspace tests, which also cover exclusive creation.
  Prompt persistence, cancellation, interruption, ACP content conversion, and
  terminal diff guarantees retain their owning tests. Patch parsing, chunk
  matching, multi-file preparation, move, delete, and partial-batch guarantees
  were removed as planned.

## Automated checks

- `make check` — Passed after correcting compilation issues in the new test
  fixtures. Final run passed 67 client tests, 166 server tests, Markdown and
  Rust formatting, the build, and Clippy.
- `make e2e` — Passed all 12 tmux tests, including the replacement tool title,
  summary, output toggle, diff, and color assertions. No required checks remain
  skipped.
