# Restore apply_patch

## Goal

Restore the `apply_patch` tool from the implementation immediately before commit
`803ab91` replaced it. Remove `write_file` and `edit_file`, returning the model
tool set to one patch-based file-changing tool that can add, update, move, and
delete files in one call.

## Related code

- `crates/ox-server/src/tools/patch.rs` — the removed patch language parser,
  exact line matcher, preflight checks, filesystem changes, summaries, diff
  content, and focused tests; restore from Git history and adapt to the current
  module boundaries.
- `crates/ox-server/src/tools/file.rs` — the replacement `write_file` and
  `edit_file` implementation and tests; deleted.
- `crates/ox-server/src/tools/workspace.rs` — descriptor-based confined file
  access; needs the removed delete and no-replace move operations used by
  patches.
- `crates/ox-server/src/tools.rs` — tool names, schemas, titles, dispatch, and
  tool-set tests.
- `crates/ox-server/src/acp/convert.rs` and `crates/ox-server/src/acp/prompt.rs`
  — ACP classification and prompt execution, persistence, replay, cancellation,
  and interruption fixtures.
- `crates/ox/src/tui/transcript.rs` and `crates/ox/tests/tui.rs` —
  diff-rendering fixtures and terminal expectations that name the file-changing
  tool.
- `scripts/run.py` — the live file-tool stress prompt.

## Decisions

- Restore the final reviewed `apply_patch` behavior from Git history rather than
  designing another patch format. Its `patch` argument supports `Add File`,
  `Update File`, `Move to`, and `Delete File`; updates use ordered exact-line
  chunks with optional literal anchors.
- `apply_patch` replaces `write_file` and `edit_file`; it is not added alongside
  them. The server advertises six tools: shell, shell process, read, glob, grep,
  and apply patch.
- Preserve the existing safety boundary. Parse and prepare every operation
  before writing, reject paths outside the workspace, symbolic-link targets,
  duplicate targets, invalid source files, and existing add or move
  destinations. Apply through the descriptor-based `Workspace` methods so a path
  swapped after preparation cannot redirect a write, delete, or move.
- Preserve the historical observable output: `Applied patch.` followed by an
  operation summary for the model, and matching text and diff blocks for ACP
  clients. Tool call titles are `Apply patch to path` or
  `Apply patch to N
  files`, with `Apply patch` as the malformed-argument
  fallback, and ACP classifies the tool as `Edit`.
- Keep the current synchronous execution and cancellation boundary. Cancellation
  can prevent a patch before it starts, but does not interrupt a patch between
  prepared filesystem operations. A filesystem failure may leave completed
  earlier operations in place and reports completed, failed, and unattempted
  operations.

## Test plan

- Restore parser and matching coverage for malformed patches, exact and forward
  chunk matching, anchors, additions, line endings, and final newlines.
- Restore execution coverage for mixed add, update, move, delete, and no-op
  operations; summaries and diff content; invalid paths and files; duplicate
  targets; symbolic-link confinement and target swaps; all-operation preparation
  before writes; and partial application reporting after a filesystem failure.
- Update `tools.rs` coverage to assert the six-tool schema, strict `patch`
  arguments, apply-patch titles, dispatch, output bounds, and rejection of the
  removed `write_file` and `edit_file` names.
- Restore the prompt fixture that applies a multi-file patch and verifies saved
  outcomes, ACP replay, cancellation before execution, persistence after later
  interruption, and no execution for an invalid model completion.
- Use `apply_patch` in ACP conversion, transcript rendering, and tmux fixtures,
  retaining their existing edit-kind, text/diff output, wrapping, toggling, and
  color assertions.

## Implementation plan

1. Create `crates/ox-server/src/tools/patch.rs` from the final implementation
   before `803ab91`, including its schema description, parser, matcher,
   preparation and application stages, summaries, diff blocks, changed-path
   extraction, and tests. Adapt imports and fixture names only where the current
   code requires it.
2. Modify `crates/ox-server/src/tools/workspace.rs` to restore descriptor-based
   `remove_file` and no-replace `move_file` operations and their `RenameFlags`
   import. Retain current absolute-path normalization used by the read and
   search tools, and add or restore confinement tests for patch mutations after
   path preparation.
3. Modify `crates/ox-server/src/tools.rs` to replace the file module and its two
   constants, schemas, titles, descriptions, and dispatch branches with `patch`,
   `APPLY_PATCH`, and strict deserialization of the `patch` argument. Dispatch
   through `bounded_result`, and update shared tool registration, validation,
   title, absolute-path, and unknown-tool tests. Delete
   `crates/ox-server/src/tools/file.rs`.
4. Modify `crates/ox-server/src/acp/convert.rs` to classify `APPLY_PATCH` as an
   edit and restore a multi-file patch outcome fixture that verifies text and
   diff blocks in live and replayed ACP updates.
5. Modify `crates/ox-server/src/acp/prompt.rs` to restore the multi-file
   `apply_patch` harness, expected title, summary, content, and patch-oriented
   test names while preserving the current provider and session harness.
6. Modify `crates/ox/src/tui/transcript.rs` and `crates/ox/tests/tui.rs` so
   their file-edit calls and expected titles use `apply_patch`, while keeping
   their existing diff-rendering assertions.
7. Modify the stress-prompt comment in `scripts/run.py` to exercise one patch
   call containing multiple updates, an anchored repeated-section change, a move
   with an edit, an addition, and a deletion, then verify the resulting
   workspace.
