# Clean up the four most complex functions

## Goal

`rsloc --items` ranks four functions highest for cognitive complexity:
`compaction::material`, `read::execute`, `compaction::next_piece`, and
`patch::prepare`. None has a doc comment. Split or restructure each so its
control flow reads directly, and document only the intent and contracts the code
cannot state. `next_piece` also stops rebuilding and serializing the whole
summarizer request for every size it tries.

Behavior does not change: summarizer requests, `read_file` output, patch
results, and every error message stay byte-for-byte the same.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/compaction.rs` — `material` turns the entries a cut covers into labeled
  text, `next_piece` fills one summarizer request from it, and `compact` drives
  both. `summary_request_fits` serializes a complete summarizer body to test
  its size.
- `src/openrouter.rs` — `summarizer_body` puts the previous summary and the
  piece into one user-role message string.
- `src/sessions.rs` — `ToolOutcome`, whose status name is matched out in three
  places.
- `src/hooks.rs` — `ToolReport::new`, one of those three matches.
- `src/tools/read.rs` — `execute` pages numbered lines; `line` reads one line.
- `src/tools/patch.rs` — `prepare` checks every operation and builds its
  change before `apply_prepared` touches the disk.

## Decisions

- Replace the `(String, String, usize)` tuple that `material` returns and
  `next_piece` consumes with a named struct. Its fields then document
  themselves, and the one non-obvious field, the part number, gets a doc
  comment.
- `next_piece` measures the summarizer body once with an empty piece. JSON
  string escaping is per character, so a piece adds exactly its escaped length
  to that body. The fit check compares that sum with the same token formula as
  before, so the pieces are identical to the current ones. Keep the halving
  search.
- Add `ToolOutcome::status` returning `completed`, `failed`, or `cancelled`,
  and use it in `compaction.rs`, `hooks.rs`, and the shell test that repeats the
  match.
- `read::execute` records why a page ended in a small enum instead of the
  `deferred` and `preview` flags. The closing lines are chosen from it.
- `patch::prepare` loses its closure. One function prepares an operation and
  another prepares an update, including a move. The order of checks, and so the
  first error reported, stays the same: target path, source file, new contents,
  then destination.

## Naming

- **Summarizer material** — The labeled text from the transcript entries a
  compaction cut covers after the latest checkpoint, sent to summarizer
  requests. It is `material` in code.
- **Material field** — One labeled text value in summarizer material, with the
  part number it continues from. It is `MaterialField` in code.
- **Piece** — The summarizer material sent in one summarizer request. It is
  `piece` in code, as today.
- **Page** — The numbered lines one `read_file` call returns. Its ending is
  `PageEnd` in code: the line limit or end of file, a next line deferred to the
  next page, or a line truncated to fit.

## Test plan

- Add `summarizer_pieces_fit_the_context_limit_and_keep_all_material` in
  `src/compaction.rs`: split material containing quotes, backslashes, newlines,
  control characters, and non-ASCII text into pieces. Each piece's complete
  summarizer body fits the context limit, and joining the pieces' field text
  reproduces the material. This covers the size shortcut, which no test covers
  today.
- The existing compaction, `read_file`, and `apply_patch` tests own the
  unchanged behavior and must pass unmodified, apart from reading the struct's
  fields instead of tuple positions in
  `summarizer_material_describes_images_by_mime_type`.

## Implementation plan

1. `src/sessions.rs`: add `ToolOutcome::status`. Use it in `src/hooks.rs` and
   the matching test in `src/tools/shell.rs`.
2. `src/compaction.rs`: add `MaterialField`. Split `material` into one helper
   per transcript entry kind that contributes text, with an exhaustive match in
   `material`. Document `material`.
3. `src/compaction.rs`: replace `summary_request_fits` with a one-time
   measurement of the empty-piece body and an escaped-length helper. Move the
   halving search into its own function and document `next_piece`.
4. `src/tools/read.rs`: add `PageEnd` and a page-reading function that returns
   the page text, the next line number, and its `PageEnd`. `execute` validates
   arguments, skips to the offset, and writes the closing lines. Document
   `execute`'s output format.
5. `src/tools/patch.rs`: replace the closure in `prepare` with
   `prepare_operation` and `prepare_update`. Document `prepare`.
6. Run `rsloc --items` to confirm the four functions dropped in complexity, and
   run full validation.
