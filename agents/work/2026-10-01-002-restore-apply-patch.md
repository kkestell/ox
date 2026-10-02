# Restore apply_patch

## Plan

`agents/plans/2026-10-01-002-restore-apply-patch.md`

## Summary

`apply_patch` is restored as the only file-changing model tool. It supports
adding, updating, moving, and deleting files in one preflighted call, reports
text and diff content to ACP clients, and applies changes through confined
workspace descriptors. The plan's goal is met.

## Departures from the plan

- `Workspace::normalize_path` was removed rather than retained because it was
  used only by the deleted file-tool module. The current absolute-path handling
  for read and search tools remains in `Workspace::relative_name`.
- The OpenAI stream fixture was updated in addition to the named prompt fixtures
  so no model-completion fixture refers to a removed file tool.

## Decisions

- Added direct workspace tests for delete and move target swaps alongside the
  restored write-swap coverage. These verify all three patch mutation paths stay
  confined after preparation.
- Kept the current persisted turn-error assertion for an invalid completion; the
  historical patch fixture predated that behavior.

## Automated checks

- `cargo test --workspace` — Passed.
- `make check` — Passed.
- `make e2e` — Initially failed because two terminal expectations still said
  `Edit a.tally`; passed after changing them to `Apply patch to a.tally` (12
  tests).

## Manual verification

- Not run. The live stress scenario in `scripts/run.py` requires configured
  provider credentials and can be reproduced by passing its opening prompt to
  `python3 scripts/run.py`.

## Follow-up work

None.
