# Syntax highlighting in the file viewer

## Goal

The file viewer shows a file's text in a plain `<pre>`. It should color the
text by the file's language, chosen from the file's name, with highlight.js's
core and a fixed set of registered languages, in GitHub's light and dark
colors (the scheme Hypercube uses). A file of a language Ox does not register
shows as plain text. The page re-highlights only when the file's text changes.

## Related code

- `cmd/ox/web/src/FileView.tsx` — the view of one file, keyed by workspace and
  path; `text` is a plain function over `createPoll`'s latest answer, rendered
  as the child of `<pre role="tabpanel">`.
- `cmd/ox/web/src/poll.ts` — `createPoll` sets a new `Polled` object on every
  answer, once a second, whether or not the text changed.
- `cmd/ox/web/src/Markdown.tsx` — renders library HTML with `innerHTML`, with
  a comment on what the HTML can hold.
- `cmd/ox/web/src/style.css` — `:root` colors with `light-dark()`;
  `.viewer pre[role="tabpanel"]` sets the pane's font and `--foreground`.
- `cmd/ox/web/e2e/workspace.spec.ts` — "the file viewer shows each session's
  open file": the scroll-position check across unchanged reads and the
  `data-marked` check.
- `~/src/hypercube/internal/ui/web/files/highlight.ts` — the language list
  this plan follows.
- `highlight.js` 11.12.0 — `lib/core`, `lib/languages/*`, and
  `styles/github.css` / `styles/github-dark.css`, whose selector groups are
  identical, so each group becomes one rule with `light-dark()`.

## Decisions

- **One module owns highlighting.** New `src/highlight.ts` imports
  `highlight.js/lib/core`, registers the languages, holds the file-name map,
  and exports `highlight(path: string, text: string): string`, the HTML of
  `text` in the language `path` names:
  `hljs.highlight(text, { language, ignoreIllegals: true }).value`.
  `ignoreIllegals` keeps a file the grammar does not expect from throwing.
- **Plain text goes through the same path.** `plaintext` is registered and is
  the language of every unrecognized name, so `highlight` always returns
  escaped HTML and the view has one rendering: `innerHTML` on the `<pre>`.
  No auto-detection: `highlightAuto` is not used.
- **Registered languages** (highlight.js names): `bash`, `c`, `cpp`, `css`,
  `diff`, `dockerfile`, `go`, `ini`, `java`, `javascript`, `json`,
  `makefile`, `markdown`, `ocaml`, `plaintext`, `python`, `rust`, `scss`,
  `sql`, `swift`, `typescript`, `xml`, `yaml`. highlight.js's `shell` is
  console sessions, so shell scripts use `bash`. JSX and TSX use
  `javascript` and `typescript`, whose grammars cover them; JSONC and JSONL
  use `json`; TOML uses `ini`; HTML and SVG use `xml`.
- **The file-name map** lives in `highlight.ts` as two `Record<string, string>`
  constants. `names`, checked first against the whole base name:
  `Dockerfile`, `Containerfile` → `dockerfile`; `Makefile`, `makefile`,
  `GNUmakefile` → `makefile`. `extensions`, checked against the lowercased
  text after the base name's last `.` when that dot is not its first
  character (so `.gitignore` has none):
  - `c`, `h` → `c`; `cc`, `cpp`, `cxx`, `hh`, `hpp`, `hxx` → `cpp`
  - `css` → `css`; `scss` → `scss`
  - `diff`, `patch` → `diff`
  - `dockerfile` → `dockerfile`; `mk` → `makefile`
  - `go` → `go`; `java` → `java`; `rs` → `rust`; `swift` → `swift`
  - `html`, `htm`, `xhtml`, `xml`, `svg`, `plist` → `xml`
  - `ini`, `toml` → `ini`
  - `js`, `mjs`, `cjs`, `jsx` → `javascript`
  - `ts`, `mts`, `cts`, `tsx` → `typescript`
  - `json`, `jsonc`, `jsonl` → `json`
  - `md`, `markdown` → `markdown`
  - `ml`, `mli` → `ocaml`; `py`, `pyi` → `python`
  - `sh`, `bash`, `zsh` → `bash`
  - `sql` → `sql`; `yml`, `yaml` → `yaml`
- **How the HTML reaches the DOM.** `<pre role="tabpanel" innerHTML={html()}>`.
  highlight.js escapes all source text and emits only
  `<span class="hljs-…">`, so the file's content cannot add elements or load
  anything. A comment above `highlight` says so, as `Markdown.tsx` does.
- **Re-highlighting only on change.** `FileView`'s `text` becomes a
  `createMemo`; string equality stops the once-a-second new `Polled` object
  from propagating when the file is unchanged. `html` is a second `createMemo`
  over `text`, returning `undefined` while `text` is, and `highlight(path, t)`
  otherwise. An unchanged read therefore neither re-highlights nor writes
  `innerHTML`, which keeps the pane's DOM and scroll position.
- **Large files.** Measured in Node with the registered grammars on 1 MiB
  inputs: JavaScript 51 ms, Markdown 50 ms, Go 70 ms, JSON 147 ms (about
  98k spans, 4.9 MB of HTML). That is a one-off cost per change of a rare
  file size, so highlighting runs synchronously in the memo, with no worker
  and no size cutoff. The bundle grows by about 114 KB minified, 39 KB
  gzipped; it is imported eagerly like `markdown-it`.
- **Colors.** `style.css` gets a "Syntax highlighting" block with highlight.js's
  GitHub selector groups, each `color: light-dark(<github.css>,
  <github-dark.css>)`:
  - `.hljs-doctag, .hljs-keyword, .hljs-meta .hljs-keyword,
    .hljs-template-tag, .hljs-template-variable, .hljs-type,
    .hljs-variable.language_` → `#d73a49`, `#ff7b72`
  - `.hljs-title, .hljs-title.class_, .hljs-title.class_.inherited__,
    .hljs-title.function_` → `#6f42c1`, `#d2a8ff`
  - `.hljs-attr, .hljs-attribute, .hljs-literal, .hljs-meta, .hljs-number,
    .hljs-operator, .hljs-variable, .hljs-selector-attr,
    .hljs-selector-class, .hljs-selector-id` → `#005cc5`, `#79c0ff`
  - `.hljs-regexp, .hljs-string, .hljs-meta .hljs-string` → `#032f62`,
    `#a5d6ff`
  - `.hljs-built_in, .hljs-symbol` → `#e36209`, `#ffa657`
  - `.hljs-comment, .hljs-code, .hljs-formula` → `#6a737d`, `#8b949e`
  - `.hljs-name, .hljs-quote, .hljs-selector-tag, .hljs-selector-pseudo` →
    `#22863a`, `#7ee787`
  - `.hljs-section` → `#005cc5`, `#1f6feb`, `font-weight: bold`
  - `.hljs-bullet` → `#735c0f`, `#f2cc60`
  - `.hljs-emphasis` → `font-style: italic`; `.hljs-strong` →
    `font-weight: bold`
  - `.hljs-addition` → `#22863a`, `#aff5b4` on `#f0fff4`, `#033a16`
  - `.hljs-deletion` → `#b31d28`, `#ffdcd7` on `#ffeef0`, `#67060c`

  The `.hljs` base rule and `.hljs-subst` are left out, and emphasis and
  strong take no color: untokenized text keeps the pane's `--foreground`
  and background. The rules are not scoped to `.viewer`; only highlight.js
  emits these classes.
- **Requirements.** File viewer: the pane still shows the text monospaced and
  unwrapped, now colored; its messages for binary, large, non-UTF-8, and
  unreadable files are unchanged (they render through the existing `<p>`
  branch). File viewer updates: a changed file re-highlights on the next
  read; a deleted file shows its message and returns highlighted. Open file:
  unchanged, since the language derives from the path the view is keyed by.
- **Construction.** The language is a function of the path, and the view's
  path never changes, so the shown colors always match the shown file's name.
  The HTML is derived from the text by memo, not stored beside it.
- **Trust boundaries.** File contents are workspace data. highlight.js's
  escaping is the boundary between them and the DOM; nothing else from the
  file reaches `innerHTML`.

## Check your assumptions

1. **The file viewer requirement gains highlighting.** Kept.
   - **Plan:** adds a requirement, Syntax highlighting: "The file viewer colors
     a file's text by its language, chosen from the file's name, in GitHub's
     light or dark colors to match the page; a file whose language Ox does not
     recognize shows as plain text." The File viewer requirement is unchanged.
   - **Alternative:** fold the same sentence into File viewer, which makes an
     already long requirement longer.

## Test plan

- Highlighting, colors, plain text, and re-highlighting only on change: e2e,
  in "the file viewer shows each session's open file" in
  `cmd/ox/web/e2e/workspace.spec.ts`, whose setup already opens files in the
  viewer. Add `src/main.go` with `package main\n\n// note\nfunc main() {}\n`.
  After `a.txt` returns with `back\n` and before the `bin.dat` click, so the
  later `aRow` and `bin.dat` assertions stand:
  - Assert `a.txt`'s panel has no `span` (plain text).
  - Click `main.go`; assert `.hljs-keyword` first has text `package` and
    `.hljs-comment` has `// note`; assert the keyword's computed color is
    `rgb(215, 58, 73)`, then `page.emulateMedia({ colorScheme: "dark" })`
    and assert `rgb(255, 123, 114)`, then restore `light`.
  - Set `data-marked` on the `.hljs-comment` span, wait 1.5 s, and assert
    `span[data-marked]` still exists: an unchanged read writes no new HTML.
    This fails if `text` is not a memo.
  - Write `// changed\n` to `main.go` and assert `.hljs-comment` has text
    `// changed` within 3 s.
- The existing `toHaveText` assertions on the panel keep passing: spans carry
  the same text.

## Implementation plan

1. `cmd/ox/web`: `npm install highlight.js@11.12.0` (dependency).
2. Create `cmd/ox/web/src/highlight.ts`: core import, the language imports and
   `registerLanguage` calls, `names`, `extensions`, a private `languageFor`,
   and exported `highlight(path, text)` with its comment on escaping.
3. `cmd/ox/web/src/FileView.tsx`: make `text` a `createMemo`, add the `html`
   memo, render `<pre role="tabpanel" innerHTML={html()} />` as the `Show`
   fallback.
4. `cmd/ox/web/src/style.css`: the syntax-highlighting rules after the file
   viewer block.
5. `cmd/ox/web/e2e/workspace.spec.ts`: the test steps above.
6. Docs below.

## Requirement changes

- **Added:** **Syntax highlighting.** The file viewer colors a file's text by
  its language, chosen from the file's name, in GitHub's light or dark colors
  to match the page; a file whose language Ox does not recognize shows as
  plain text. (After File viewer updates.)

## Documentation updates

- `docs/architecture.md`: a `highlight.ts` row in the web client's file table,
  "Syntax highlighting: the registered languages and the file names that
  choose them."
