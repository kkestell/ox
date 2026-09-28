# Show tool output review

## Scope and coverage

Reviewed commit `03a5ed9`, which saves tool call content beside each tool
outcome's text and shows it in the transcript view under Ctrl+O, against the
plan in `agents/plans/2026-09-28-003-show-tool-output.md` and the work log in
`agents/work/2026-09-28-003-show-tool-output.md`. The diff covers
`crates/ox-acp/src/sessions.rs`, `tools.rs`,
`tools/{patch,read,search,shell}.rs`, `acp/convert.rs`, `crates/ox/src/tui.rs`,
`crates/ox/src/tui/transcript.rs`, the fake server's `render` script, and the
tests. The paging in `read.rs`, the `rg` enumeration loop in `search.rs`, the
shell `report` function, and the approval dialog in `tui.rs` were read in full
as callers of the changed code. Lenses: `correctness`, `simplicity`,
`readability`, `testing`, and `documentation`.

Not measured: the transcript view recomputes every diff's hunks on each draw
while output is shown. That follows the existing design of rendering the whole
transcript per frame, and nothing suggested it is slow, so it was not treated as
a finding.

## Fixed findings

- **The search summary keyed off a sentinel string it had just written**
  (`crates/ox-acp/src/tools/search.rs:245`): `finish` decided whether to send a
  count by checking `self.output.starts_with("No matches found.")`, three lines
  after writing that text. The reader has to connect the two strings to see that
  the branch means "something was found". The condition is now `self.files > 0`,
  which is what the summary reports. `files` is incremented before every append
  in both glob and grep mode, so the behavior is unchanged, including the
  truncated case.
- **The glossary lacked the term the plan introduced**
  (`agents/glossary.md:41`): the plan's naming section defines tool call content
  as distinct from the tool outcome's text, and the code uses both `content` and
  `text` on `ToolOutcome`, but the glossary defined only the outcome. Added a
  one-line entry for tool call content and noted that the outcome carries the
  model's text.

## Findings

No open findings.

## Checks run

- `make check` — passed.
- `make check-docs` — passed after formatting this review.

## Verdict

The change matches the plan. Both findings were fixed; nothing is left open.
