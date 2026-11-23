# Review: line numbers, word wrap, and Markdown preview in the file viewer

## Scope and coverage

Reviewed the uncommitted changes on `file-view-modes` against base `8eb0d24`
(`git merge-base origin/main HEAD`, also `HEAD`): `highlight.ts`, `ox.tsx`,
`FileTree.tsx`, `App.tsx`, `FileView.tsx`, `style.css`, `highlight.test.mjs`,
`e2e/workspace.spec.ts`, `docs/architecture.md`, and
`docs/functional-requirements.md`. Checked against
`agents/plans/2026-11-23-file-view-modes.md`, with the user's approval of its
three recommendations and its requirement text, and against the requirements
File viewer, File viewer updates, Syntax highlighting, Open file, Line numbers,
Word wrap, and Markdown preview. Read the open `bug` issues (#5, #6, #33, #34,
#55); none touches the file viewer.

The implementer's flagged choices hold: the narrowed `/^Toggle (sessions|files)$/`
keeps the panel-toggles spec checking what it meant; `.editor-actions` avoids a
lint clash with `nav .actions`; the wrap button before the preview button keeps
the preview button at the header's right end when wrap hides; the gutter's 1px
shadow covers the half-clipped glyphs seen at fractional offsets; and the
`highlight.ts` row of `docs/architecture.md` still states what the file is.

Gaps:

- How the gutter, wrap, and preview look is for the user to judge from
  `/tmp/ox-fvm-screens/`.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                           |
| -- | -------- | ------------- | -------- | ------ | ----------------------------------------------- |
| F1 | Low      | Correctness   | Rejected | —      | Preview button looks pressed in the source view |
| F2 | Low      | Testing       | Rejected | —      | No spec observes the line numbers               |
| F3 | Low      | Documentation | Rejected | —      | `highlight.ts` row omits the line elements      |

## F1. Preview button looks pressed in the source view

Low · Correctness · Rejected · `cmd/ox/web/src/style.css:441`

In `light-2-source-scrolled.png` the eye button has a filled background while
the source shows, and in `dark-4-preview.png` it has none while the preview
shows.

### Evidence

Pressed state changes only the icon's color: `.viewer .editor-actions
button[aria-pressed="false"]` dims it. The filled background is
`button.icon:hover` (`style.css:103`), left by the screenshot script's click on
the eye button with the pointer still over it. `light-3-source-wrapped.png`
shows the same background on the wrap button just after its click. The e2e
specs assert `aria-pressed="true"` while the preview shows and `"false"` in the
source.

### Action

Rejected: a hover artifact of the screenshots, not the pressed state.

## F2. No spec observes the line numbers

Low · Testing · Rejected · `cmd/ox/web/src/style.css:452`

The numbers are CSS-generated content, so no Playwright spec sees them, and
removing them would fail no test.

### Evidence

`cmd/ox/web/AGENTS.md` keeps styling and pixel checks out of specs and leaves
how the page looks to the user. The structure the numbers count is guarded by
the new `highlight.test.mjs` case, and the pane's text staying the file's text
by the existing carriage-return and selection specs. A probe in headless Chrome
of the same markup and CSS copied `"one\n\nthree\nfour"` from a selection of
`one\n\nthree\nfour\n`, with no numbers and no doubled newlines.

### Action

Rejected: the number column is appearance, judged from the screenshots, as the
plan's Test plan settles.

## F3. `highlight.ts` row omits the line elements

Low · Documentation · Rejected · `docs/architecture.md:246`

The row describes `highlight.ts` as the registered languages and the file names
that choose them. It now also wraps each line in an element and decides which
files are Markdown through the exported `languageFor`.

### Evidence

The row stays true: the Markdown test is the language lookup the row names, and
the line markup is a detail of the highlighter's output, documented in the
function's comment.

### Action

Rejected: the row does not contradict the code, and `AGENTS.md` edits a document
only for a fact it owns that changed.

## Checks run

- From `/ox-work`'s report: `make test` passed and `make e2e` passed (143
  passed, 4 skipped) on the final tree; headless Chrome through `make dev`, light
  and dark. The new Node test and the six new or changed e2e tests failed on the
  base revision. `make live` does not apply: the change does not alter how Ox
  drives Claude Code.
- Spot check: removed `setFile`'s same-path guard; "a Markdown file's source
  view holds through switching sessions…" failed. Restored.
- Spot check: removed `highlight`'s `.slice(0, lines)`; "each line of a file is
  one line element…" and "files of no registered language…" failed. Restored,
  and `dist` rebuilt from the restored source.
- Probe: copying from the line markup in headless Chrome yields the file's
  text with no line numbers.

## Verdict

Ready to commit. No `Fix` or `Record` findings; F1–F3 are rejected.
