# Show tool output

## Goal

Ctrl+O toggles the transcript view between the current one-row tool calls and an
expanded form that shows each finished call's tool call content under its row,
in both live turns and replay. Every transcript item is separated by a blank row
in both forms. Ox ACP sends content the client can show without parsing the text
the model reads: a unified diff for each patched file, statistics for reads and
searches, and the captured output for shell commands.

At 60 columns, expanded:

```text
● Shell cargo test
  └ Exit code: 0
    running 2 tests
    test result: ok. 2 passed; 0 failed

● Read src/main.rs
  └ Lines 1–120 of 120

● Search for fn main in src (files matching *.rs)
  └ 3 matches in 2 files

● Apply patch to src/main.rs
  └ Modified src/main.rs
    @@ -12,7 +12,7 @@
     fn main() {
    -    let config = Config::load();
    +    let config = Config::load()?;
         run(config)
     }

● Final answer from subagent 8b71d0e4-52c9-4a36-b8f7-6e0a93c4d215
  └ The auth module validates tokens in middleware.rs.
```

Collapsed, the same calls are their `●` rows, one blank row apart, and the
subagent's answer keeps its text.

## Related code

- `crates/ox-acp/src/sessions.rs` — `ToolOutcome` is three variants, each
  holding only the text the model reads. `AssistantBatch` saves one outcome per
  tool call, and every entry is stored as JSON.
- `crates/ox-acp/src/tools.rs` — `execute_other` and `bounded_result` turn each
  tool's `Result<String, String>` into a `ToolOutcome`.
- `crates/ox-acp/src/tools/read.rs` — `execute` knows the offset, `next`, and
  whether more remains before it formats the page.
- `crates/ox-acp/src/tools/search.rs` — `SearchOutput` appends one line per file
  or match; `scan_file` appends grep matches.
- `crates/ox-acp/src/tools/patch.rs` — `prepare_update` holds the source and
  updated contents, `Prepared` holds each operation's summary, and
  `apply_prepared` joins the summaries into the model's text.
- `crates/ox-acp/src/tools/shell.rs` — `report` joins the status and the decoded
  stdout and stderr tails for both `shell` and `shell_process` reads.
- `crates/ox-acp/src/acp/convert.rs` — `finished_tool_call_update` and
  `replayed_tool_call` send one text block from `output_content`, and
  `raw_output` sends the text.
- `crates/ox/src/tui/transcript.rs` — `item_lines` renders a named call as one
  row and a nameless call with its content; `lines` suppresses the blank row
  between adjacent `●` rows; `content_lines` and `prefixed` render content;
  `tool_content` flattens content blocks to text.
- `crates/ox/src/tui.rs` — `Ui::show_thinking`, `Screen::show_thinking`, and the
  Ctrl+T arm of `key` are the pattern for the toggle. `approval_lines` reuses
  `content_lines` for the dialog body.
- `crates/ox-fake-server/src/lib.rs` — the `render` script sends the tool calls
  the tmux transcript test checks.
- `crates/ox/tests/tui.rs` —
  `the_transcript_view_renders_thinking_tools_and_wrapped_replies`.

## Decisions

- The transcript saves the tool call content beside the model's text, because
  replay derives from the transcript alone and a diff cannot be rebuilt from the
  model's text. `ToolOutcome` becomes a struct: `status: ToolStatus`
  (`Completed`, `Failed`, `Cancelled`), `text: String`, and
  `content: Vec<ToolContent>`. `ToolContent` is `Text(String)` or
  `Diff { path: PathBuf, old_text: Option<String>, new_text: String }`, the
  shape of ACP's v1 `Diff` block, with `path` absolute. Empty `content` means
  the client shows `text`; no tool needs to show nothing. The saved JSON shape
  changes, so `ox.db` is recreated.
- Tool call content is a projection for the client, not information for the
  model. `raw_output` stays the text.
- Content per tool:
  - `read_file`: `Lines {offset}–{last} of {last}` when the page reached the end
    of the file, else `Lines {offset}–{last}, more remains`. The empty-file and
    past-end results keep their text.
  - `glob`: `{files} files`. `grep`: `{matches} matches in {files} files`. Both
    add `, truncated` when the output was cut. `No matches found.` keeps its
    text.
  - `apply_patch`: for each completed operation, a `Text` block with its summary
    (`Modified src/main.rs`) followed by a `Diff` block for adds, updates,
    deletes, and moves that change contents. An add has no `old_text`; a delete
    has an empty `new_text`; a move's `path` is the destination. Unchanged and
    content-preserving moves send only the summary. A failed patch keeps its
    error text.
  - `shell` and `shell_process` reads: the status, then stdout, then stderr, as
    separate `Text` blocks, omitting an empty stream. The omission notices stay
    in the model's text only.
  - Everything else keeps its text: background starts, shell process list,
    write, and stop, the subagent tools, and every failure and cancellation.
- The client renders a `Diff` block as unified hunks with three lines of
  context, computed with the `similar` crate. Added rows are `theme::GREEN`,
  removed rows `theme::RED`, hunk headers `theme::DIM`. Diff rows are clipped,
  not wrapped, so context indentation survives. Text blocks wrap as today.
- Content rows are indented under the `●` row: the first row starts with `└` and
  later rows with two spaces, so every content row begins four columns in.
  `prefixed` takes both prefixes; user and response items pass `❯` or `●` with
  two-space continuation. The approval dialog keeps its two-space body.
- A named call shows its content only while the toggle is on. A nameless call (a
  subagent's answer or failure) always shows it, as today. A pending or
  in-progress call has no content and shows only its row.
- Every transcript item is followed by one blank row. `lines` drops the
  `previous_bullet` logic and `item_lines` no longer reports whether an item is
  one `●` row.
- Ctrl+O toggles `Ui::show_output`, and `Screen` and `TranscriptView::lines`
  take it beside `show_thinking`. The toggle is not saved.

## Naming

- **Tool call content** — what the ACP client shows under a finished tool call:
  ACP `content` blocks, saved as `ToolContent` values in the outcome. It is
  distinct from the tool outcome's text, which the model reads.
- **Tool outcome** keeps its glossary definition; its status is `ToolStatus`.
- **Show output** — the client toggle, `Ui::show_output`.

## Test plan

- `sessions.rs`: the store round-trips an outcome with a `Diff` block.
- `read.rs`: the paging test also checks the summary for a page that reaches the
  end and one that does not.
- `search.rs`: the glob and grep tests also check the file and match counts,
  including the truncated form.
- `patch.rs`: one test applies a patch with an add, an update, a delete, a move
  with changes, a move without changes, and an unchanged update, and checks the
  content blocks in order.
- `shell.rs`: the run test checks the three blocks of a command that writes to
  both streams and that an empty stream sends no block.
- `convert.rs`: the finished and replayed tool call tests check that a `Diff`
  content becomes an ACP `Diff` block and that empty content sends the text.
- `transcript.rs`: the spacing case becomes "every item is followed by a blank
  row". A new test renders a call with a text block and a diff block with the
  toggle on, checking the `└` prefix, the four-column indent, the hunk rows, the
  row colors, and clipping; with the toggle off, only the row. The nameless call
  case keeps its content in both.
- `tui.rs` unit tests: Ctrl+O flips `show_output`.
- Fake server `render` script: the read call's content becomes `Lines 1–2 of 2`,
  and a fourth call, `patch-1` titled `Apply patch to
  a.tally` with name
  `apply_patch`, completes with a text block and a `Diff` block. The tmux test
  checks the collapsed screen with blank rows between calls, presses Ctrl+O, and
  checks the expanded rows including a `+` row.

## Implementation plan

1. In `crates/ox-acp/src/sessions.rs`, replace the `ToolOutcome` enum with the
   struct, add `ToolStatus` and `ToolContent`, and add `completed`, `failed`,
   and `cancelled` constructors with empty content plus `with_content`. Update
   every construction and match in `ox-acp`, including tests.
2. In `crates/ox-acp/src/tools.rs`, let `bounded_result` accept the text and
   content a tool returns, and route `read`, `glob`, `grep`, and `apply_patch`
   results with their content.
3. In `read.rs`, return the summary beside the page. In `search.rs`, count files
   and matches in `SearchOutput` and return the summary from `finish`. In
   `patch.rs`, keep the old and new contents on `Prepared`, read a deleted
   file's contents in `prepare_operation`, and have `apply_prepared` return the
   content blocks with the text. In `shell.rs`, have `report` return the three
   blocks with the text.
4. In `crates/ox-acp/src/acp/convert.rs`, map `ToolContent` to ACP blocks in
   place of `output_content`, sending the text when the content is empty.
5. Add `similar` to `crates/ox/Cargo.toml`. In
   `crates/ox/src/tui/transcript.rs`, render diff blocks, add the `└` prefix and
   four-column indent, give `prefixed` both prefixes, show named calls' content
   only with the toggle on, and always separate items with a blank row.
6. In `crates/ox/src/tui.rs`, add `show_output` to `Ui` and `Screen` and the
   Ctrl+O arm.
7. In `crates/ox-fake-server/src/lib.rs`, extend the `render` script, and update
   the tests above.
