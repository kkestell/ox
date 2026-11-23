# Line numbers, word wrap, and Markdown preview in the file viewer

## Goal

The file viewer shows a file's text with no line numbers, never wraps it, and
shows a Markdown file only as source. After this change the source view numbers
each line in a gutter, a button in the viewer's header turns word wrap on and
off, and a Markdown file opens as a rendered preview, with a second header
button that switches it to its source and back. Word wrap and line numbers apply
only to the source view.

## Related code

- `cmd/ox/web/src/FileView.tsx` — the viewer: its header with the file's tab,
  the polled read, and the `<pre role="tabpanel">` filled with `highlight`'s
  HTML. Gains the header buttons and the preview.
- `cmd/ox/web/src/highlight.ts` — `highlight(path, text)` returns the escaped,
  colored HTML of the whole file joined by `\n`, and `languageFor(path)` picks
  the language from the file's name. Gains one element per line and exports
  `languageFor`, the one test of whether a file is Markdown.
- `cmd/ox/web/src/Markdown.tsx` — the transcript's renderer: markdown-it with raw
  HTML escaped, images disabled, and links opening in a new tab. The preview
  reuses it unchanged.
- `cmd/ox/web/src/ox.tsx:185-189, 262-263` — `SessionControl` holds `file`, the
  open file's path, for the life of the page, so the open file survives
  switching views and reconnecting, and a draft's control becomes its session's.
  The path becomes an `OpenFile` value that carries the Markdown view.
- `cmd/ox/web/src/App.tsx:211-235` — `Layout` mounts `FileView` keyed by
  workspace and by `control.file()`, and owns the page-lifetime panel state
  through `createPanels`. Gains the word-wrap signal.
- `cmd/ox/web/src/FileTree.tsx:115-125` — `FileRow` marks the open file and
  calls `setFile` on click.
- `cmd/ox/web/src/style.css:64-90, 419-440, 250-265` — the lucide mask icons,
  the viewer's header and pane, and the `.markdown` styles.
- `cmd/ox/web/highlight.test.mjs` — Node tests of `highlight`'s output.
- `cmd/ox/web/e2e/workspace.spec.ts:924-1085` — the file viewer specs, with the
  `viewer`, `tab`, `pane`, `openFile`, `clocked`, and `answered` helpers.

## Decisions

- **Line numbers are CSS counters on per-line elements.** `highlight` wraps
  each line in `<span class="line">`, with the line's `\n` (and its restored
  `&#13;`) inside the span at its end, so the pane's `textContent` stays exactly
  the file's text. The stylesheet numbers each `.line` with a counter in
  `::before`. Generated content is not part of the text, so selection, copy, and
  find-in-page see only the file, by construction. A trailing newline does not
  start a line of its own: when the text ends with `\n`, the empty piece after
  it gets no `.line`, as GitHub and `wc -l` count lines, and an empty file has
  no lines. Empty lines keep one line of height (`min-height: 1lh`).
- **Gutter layout.** Numbers are right-aligned in muted color in a gutter as
  wide as the largest number: `highlight` returns `{ html, lines }`, and the
  pane sets `--digits` from `lines` so the gutter's width is
  `calc(var(--digits) * 1ch)`. The pane's content is a `<code>` of width
  `max-content` (at least the pane's width) so every line spans the scrolled
  width, and the number is an inline-block `::before` with `position: sticky;
  left: 0` and the pane's background, so the gutter stays in view while the text
  scrolls sideways, as in GitHub and VS Code. Each `.line` has a hanging indent
  the gutter's width plus its gap (`padding-left` and a negative `text-indent`,
  reset to 0 on the `::before`), so a wrapped line's continuation rows align
  with its text and carry no number.
- **Word wrap** sets `white-space: pre-wrap; overflow-wrap: anywhere` on the
  pane and lets the `<code>` take the pane's width. Wrap starts off, as in
  GitHub and VS Code.
- **Header buttons** sit at the right end of the viewer's header, after the tab
  list, as VS Code places editor actions: `button.icon` toggles with
  `aria-pressed`, following `PanelToggles`. "Toggle word wrap" uses lucide's
  `wrap-text.svg`; "Toggle preview" uses `eye.svg` and is pressed while the
  preview shows. The preview button shows only for a Markdown file. The wrap
  button shows only while the source view shows. Both stay while the pane shows
  a read error, so the header does not shift while a deleted file is gone.
- **Preview** renders `<Markdown text={text()} />` inside a
  `<div role="tabpanel" class="preview">` that scrolls, and is re-rendered only
  when the read text changes (the existing `text` memo), so a re-read keeps its
  scroll position. A Markdown file is one `languageFor` names `"markdown"`
  (`.md`, `.markdown`). Its styling reuses `.markdown`, with the pane's padding,
  the body's text size, and the foreground color.
- **Requirements.** File viewer, File viewer updates, Syntax highlighting, and
  Open file hold unchanged: the source view still shows the colored text, and
  the preview re-renders on each changed read, so it shows changes on disk
  within about a second; a read error replaces either view with its reason. The
  view choices are page state that touches no request: no reply can be lost,
  and a reconnect, switching views, or a second page leaves them as they are.
  Reloading resets them with the open file. New requirements Line numbers, Word
  wrap, and Markdown preview below.
- **Construction.** A view choice exists only for a Markdown file: `OpenFile`
  is `{ path: string; view?: "preview" | "source" }`, and `setFile` alone
  builds it, with `view: "preview"` when `languageFor(path)` is `"markdown"`
  and no `view` otherwise. The viewer shows the preview button when `view` is
  set and the preview when it is `"preview"`, so no reader checks the file's
  language again. `setFile` with the path already open keeps the current value,
  so clicking the open file in the tree keeps its view. `setView(view)` changes
  `view` only when the open file has one. "No line for the empty piece after a
  final newline" is decided once, in `highlight`, which returns the line count
  the gutter uses.
- **Ownership.**
  - `OpenFile.view` lives in `SessionControl` beside the path it belongs to,
    replacing the `file` path signal. Needed so a Markdown file switched to
    source stays in source through switching sessions and reconnecting, as the
    open file does; `FileView` is remounted on each switch and cannot hold it.
    It is page-only, so it has no races with `oxd`; it goes when the file closes,
    another file opens, or the control is dropped. A draft's control becomes its
    session's, so the view carries with the open file.
  - `wrap` is a `createSignal(false)` in `Layout`, beside `createPanels`,
    passed to `FileView` as `wrap` and `onWrap`. Needed so the choice holds for
    every file the page opens until reload. Page-only, no races, gone with the
    page.
- **Trust boundaries.** The preview renders workspace files, which may come from
  anyone, through `Markdown.tsx`: raw HTML is escaped, images are disabled, so
  the preview loads nothing, markdown-it's link validation drops `javascript:`
  and other unsafe URLs, and links open in a new tab with `noopener`. The line
  markup adds only `<span class="line">` around text that still reaches the DOM
  only through `escapeHtml`.

## Check your assumptions

1. **Word wrap is one choice for the whole page, kept until reload.**
   - **Plan:** Turning wrap on applies to every file in every session from then
     on, and a reload turns it off again, as the panel widths reset. Wrap starts
     off.
   - **Alternative:** Remember the choice across reloads. It survives a reload,
     at the cost of Ox's first browser-stored setting, which a reload of the
     open file and panels does not keep. Or keep it per file, which makes the
     user turn it on again for each file.
   - Adds the requirement: "**Word wrap.** A button above the file's text wraps
     long lines to the pane's width and turns wrapping off again. The choice
     holds for every file the page opens until the page reloads, and starts
     off."
2. **Each Markdown file opens as a preview, and switching it to source lasts
   while it stays open.**
   - **Plan:** Every Markdown file opens rendered. Switching one to source keeps
     it in source through switching sessions and reconnecting; closing it,
     opening another file, or reloading brings back the preview next time.
   - **Alternative:** Make the switch one choice for the page, so after picking
     source every Markdown file opens as source until reload. Fewer clicks for
     someone editing Markdown, but the preview is no longer what a Markdown
     file opens as.
   - Adds the requirement: "**Markdown preview.** A Markdown file opens
     rendered, as the transcript renders Markdown, and a button above it
     switches between the rendering and its source. The choice lasts while the
     file stays open in its session, through switching views and reconnecting."
3. **Line numbers always show in the source view.**
   - **Plan:** Each line is numbered in a gutter that stays in view while the
     text scrolls sideways; there is no button to hide them, and copying text
     copies none of the numbers.
   - **Alternative:** A third header button to hide them, which adds a control
     few would use.
   - Adds the requirement: "**Line numbers.** The file viewer numbers each line
     of a file's text in a gutter that stays in view as the text scrolls
     sideways, and copying text from the pane copies none of the numbers."

## Test plan

- `highlight` emits one `.line` per line, the empty piece after a final newline
  gets none, an empty file gets none, and `lines` counts them: Node, a new test
  in `cmd/ox/web/highlight.test.mjs` (no existing case asserts line structure).
  Update "files of no registered language come back as escaped plain text" to
  the new markup; "highlighting keeps the file's text" and "a final carriage
  return…" keep passing on `html`.
- The pane's text, and so what is copied, is the file's text with no line
  numbers: e2e, the existing "the pane keeps a file's carriage returns" and "a
  re-read of an unchanged file keeps the text selected in the pane" keep
  passing unchanged. How the gutter looks is judged by the user from headless
  Chrome screenshots.
- "the wrap button wraps long lines to the pane": e2e, new in
  `workspace.spec.ts`. A file of one long line opens with "Toggle word wrap"
  `aria-pressed="false"`; a horizontal wheel scrolls the pane (`scrollLeft`
  300, as the scroll-position test checks); pressing the button sets
  `aria-pressed="true"` and the same wheel leaves `scrollLeft` at 0. Geometry
  is the behavior here.
- "the wrap choice holds for the next file and resets on reload": e2e, new.
  Press wrap on `a.txt`, open `b.txt`, the button is pressed; reload, reopen,
  it is not.
- A non-Markdown file has no preview button: e2e, add to "clicking a file opens
  it under its tab and marks its row…", asserting "Toggle preview" has count 0
  for `a.txt`.
- "a Markdown file opens as a preview and its button shows the source": e2e,
  new. `notes.md` holding `# Title\n\ntext\n` opens with heading "Title" in the
  pane, "Toggle preview" pressed, and no "Toggle word wrap"; pressing it shows
  the source text `# Title`, no heading, and the wrap button.
- "a Markdown file's source view holds through switching sessions, and the next
  Markdown file opens as a preview": e2e, new, after the pattern of "each
  session and draft keeps its open file…". Switch `a.md` to source in session
  "first", switch to "second" and back: still source; click `a.md` in the tree
  again: still source; open `b.md`: preview.
- "the preview shows changes on disk": e2e, new `clocked` test after "the pane
  shows changes on disk…": rewrite `notes.md` from `# One` to `# Two`, run the
  clock 1 s, the heading reads "Two".

## Implementation plan

1. `cmd/ox/web/src/highlight.ts`: export `languageFor`. Change `highlight` to
   return `{ html: string; lines: number }`, where `html` wraps each line, both
   the colored and plain paths, in `<span class="line">…</span>` with its `\n`
   inside the span at its end, and omits the empty piece after a final `\n`.
   Update the trust-boundary comment to name the line spans. Update
   `highlight.test.mjs` and add the line-structure test.
2. `cmd/ox/web/src/ox.tsx`: replace the `file` path signal with
   `file: OpenFile | undefined` (export the type), `setFile(path | undefined)`
   building it as Construction describes, and `setView(view)`. Update the
   control's comment.
3. `cmd/ox/web/src/FileTree.tsx`: compare `control().file()?.path`.
4. `cmd/ox/web/src/App.tsx`: key the viewer's `Show` on
   `control.file()?.path`; add `const [wrap, setWrap] = createSignal(false)` in
   `Layout`; pass `view`, `onView`, `wrap`, and `onWrap` to `FileView`.
5. `cmd/ox/web/src/FileView.tsx`: add the header buttons after the tab list;
   render the preview when `view === "preview"`, otherwise the `<pre>` with a
   `<code>` holding `highlight`'s `html`, `--digits` from its `lines`, and a
   `wrap` class while wrap is on. The error message replaces either view as
   now.
6. `cmd/ox/web/src/style.css`: add `.wrap-text` and `.eye` icon classes; the
   header's button group at its right end; the gutter, sticky numbers, and
   hanging indent; the wrap mode; and the preview pane's padding, size, and
   color over `.markdown`.
7. `cmd/ox/web/e2e/workspace.spec.ts`: the e2e cases above.
8. `docs/functional-requirements.md` and `docs/architecture.md` as below.
9. Check the views in headless Chrome through `make dev`, light and dark:
   a long source file with wrap off and on, scrolled sideways, and a Markdown
   preview, and save screenshots for the user.

## Requirement changes

- **Added**, under "Files" after **Syntax highlighting**:
  - **Line numbers.** The file viewer numbers each line of a file's text in a
    gutter that stays in view as the text scrolls sideways, and copying text
    from the pane copies none of the numbers.
  - **Word wrap.** A button above the file's text wraps long lines to the
    pane's width and turns wrapping off again. The choice holds for every file
    the page opens until the page reloads, and starts off.
  - **Markdown preview.** A Markdown file opens rendered, as the transcript
    renders Markdown, and a button above it switches between the rendering and
    its source. The choice lasts while the file stays open in its session,
    through switching views and reconnecting.

## Documentation updates

- `docs/architecture.md` ("Web UI"): the `Layout` row of the component table
  adds word wrap to the page-lifetime state it owns; the session control
  paragraph names the open file's Markdown view with the open file.
