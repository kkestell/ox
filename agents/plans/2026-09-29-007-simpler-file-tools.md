# Replace apply_patch with write_file and edit_file

## Goal

Replace the patch language with two single-file text tools: `write_file` creates
or overwrites a file, and `edit_file` replaces one exact occurrence of text.
Moves and deletions use `shell`.

Follow [architecture](../architecture.md), [code style](../code-style.md),
[naming](../glossary.md), and [testing](../testing.md).

## Related code

- `crates/ox-server/src/tools.rs` — schemas, execution, tool call titles, and
  tool-set tests for the main agent and subagents.
- `crates/ox-server/src/tools/patch.rs` — the parser, matching, preparation,
  filesystem operations, summaries, diff output, and tests to remove.
- `crates/ox-server/src/tools/workspace.rs` — descriptor-based file access that
  prevents symbolic links from redirecting reads and writes.
- `crates/ox-server/src/acp/convert.rs` — tool kinds and conversion of tool call
  content into live and replayed ACP updates.
- `crates/ox-server/src/acp/prompt.rs` — file-change fixtures that test
  execution, saved outcomes, cancellation, and interruption.
- `crates/ox/src/tui/transcript.rs`, `crates/ox/tests/tui.rs` — diff rendering
  fixtures and assertions using the old tool.
- `scripts/run.py` — a live stress prompt written for the patch language.

## Decisions

### Tool arguments

Both tools accept JSON objects with required string arguments and reject unknown
fields. Their schemas contain short descriptions and one example each.

| Tool         | Arguments                      | Effect                                      |
| ------------ | ------------------------------ | ------------------------------------------- |
| `write_file` | `path`, `content`              | Write the complete file content.            |
| `edit_file`  | `path`, `old_text`, `new_text` | Replace one exact occurrence of `old_text`. |

`write_file` creates missing parent directories and an absent file, or
overwrites an existing UTF-8 regular file. Empty `content` creates or truncates
an empty file. Reading an existing file before writing provides its old text for
the diff; a read error other than absence fails before writing.

`edit_file` requires an existing UTF-8 regular file and nonempty `old_text`.
Find the first and last occurrence; they must exist at the same byte offset.
Otherwise, return an error explaining that the text was absent or ambiguous,
without writing. Ambiguous text needs more surrounding context in `old_text`.
Empty `new_text` deletes the matched text. Identical old and new text succeeds
without writing after checking the match.

Strings are literal after JSON decoding. Neither tool interprets patch syntax,
regular expressions, or escapes, normalizes line endings, nor adds a final
newline. An edit preserves every byte outside the replacement; the caller
supplies the line endings within the replacement.

### Workspace and execution

Paths are relative to the workspace. Normalize `.` components and reject empty
paths, the workspace root, absolute paths, and parent traversal before any file
operation. Use the existing descriptor-based workspace operations, which reject
symbolic links in parents and at the file itself and reject nonregular files.
The new tools do not resolve link targets.

Keep file operations synchronous within the existing execution path, so
cancellation cannot interrupt a call between reading and writing. File tools
continue to need no permission in Ask mode. Moves and deletions performed
through `shell` use its existing permission rules.

One call changes one file. Retain the existing exclusive creation for absent
files and existing-file writes for overwrites. No multi-file preparation or
rollback is needed. Return filesystem failures as failed tool outcomes; a write
failure can leave that file partially written.

### Output and registration

Return `Added path`, `Modified path`, or `Unchanged path` as the model's text
and a `ToolContent::Text` block. Successful changes also return a
`ToolContent::Diff` with the absolute workspace path and complete old and new
text; new files use `old_text: None`. Unchanged files have no diff.

Register both tools for the main agent and subagents where `apply_patch` was
registered. Tool call titles are `Write path` and `Edit path`, with `Write file`
and `Edit file` as the existing malformed-argument fallback. Both have ACP kind
`Edit`. Remove `apply_patch` completely from registration and execution.

## Naming

- `write_file` — the tool that writes complete UTF-8 file content; use
  `WRITE_FILE` for its tool-name constant.
- `edit_file` — the tool that replaces one exact occurrence in a UTF-8 file; use
  `EDIT_FILE` for its tool-name constant.
- `content`, `old_text`, `new_text` — the JSON argument names above, also used
  in the deserialized argument structs and tool descriptions.
- `tools/file.rs` — the module owning both tools' schemas, arguments, execution,
  and file-tool tests.

## Test plan

- New file-tool tests own creation with missing parents, overwrite, truncation,
  and unchanged results, including exact contents and diff output.
- Exact-edit tests own unique matching, absent and ambiguous matches leaving the
  file untouched, empty `old_text` rejection, insertion through replacement,
  deletion through empty `new_text`, and unchanged replacements. Use varied
  inputs for whitespace, Unicode, LF, CRLF, and final newlines.
- File-tool boundary tests own malformed arguments and invalid paths, rejection
  of nonregular and non-UTF-8 files, and symbolic-link confinement. Retain the
  existing test that replaces a validated target with an outside link before
  writing, adapted to the workspace boundary.
- Update the tool-set and tool call title tests for both tools. The removed
  `apply_patch` name must fail as an unknown tool.
- Adapt the prompt-run fixtures to a single `write_file` call. Preserve their
  distinct guarantees: saving a completed outcome and content, cancellation
  before execution, persistence after a later interruption, and no execution
  from an invalid completion.
- Keep the ACP conversion and terminal diff rendering tests' current guarantees,
  using the replacement tools. Update the tmux fixture to edit `a.tally` with
  `edit_file` and retain its summary, output toggle, diff, and color assertions.
- Remove patch-parser, chunk-matching, multi-file preparation, move, delete, and
  partial-batch tests whose guarantees disappear with the tool.

## Implementation plan

1. Create `crates/ox-server/src/tools/file.rs` with the two schemas, strict
   argument structs, synchronous execution, summaries, diff output, and owning
   tests. Share only the path, file-reading, and result construction needed by
   both tools.
2. Modify `crates/ox-server/src/tools/workspace.rs`: add the relative-path
   normalization needed for writes to absent files, retain descriptor-based
   reads and writes, and remove unused `remove_file`, `move_file`, and
   `RenameFlags`. Move the write confinement regression here.
3. Modify `crates/ox-server/src/tools.rs`: replace the patch module and constant
   with the file module and two constants, register both schemas, dispatch their
   calls through `bounded_result`, update tool call titles and tests, and rename
   the shared temporary-workspace prefix from `ox-patch` to `ox-tools`. Delete
   `crates/ox-server/src/tools/patch.rs`.
4. Modify `crates/ox-server/src/acp/convert.rs` to classify both tools as `Edit`
   and update its content-conversion fixture. Modify
   `crates/ox-server/src/acp/prompt.rs` to replace the patch fixtures and their
   names, arguments, expected tool call titles, summaries, and content.
5. Modify `crates/ox/src/tui/transcript.rs` to use `edit_file` and `write_file`
   in the existing diff fixtures. Modify `crates/ox/tests/tui.rs` to call
   `edit_file` and expect `Edit a.tally` in the rendering test.
6. Modify only the obsolete stress-prompt comment in `scripts/run.py`: exercise
   nested creation and overwrite with `write_file`, exact edits and ambiguous
   match failures with `edit_file`, and moves and deletions with `shell`.
