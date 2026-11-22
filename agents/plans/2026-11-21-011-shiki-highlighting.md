# Highlight the file viewer with Shiki

## Goal

The file viewer's highlight.js grammars are shallow: much of a `.tsx` file comes
out uncolored because it goes through the plain TypeScript grammar, Go call
sites and fields stay plain, several emitted classes have no CSS, and the light
colors are an older GitHub palette than the dark ones. Replace highlight.js with
Shiki 4, using its GitHub light and dark default themes as dual themes that
follow the page's scheme, so every file colors the way GitHub colors it. The
transcript's Markdown code blocks stay unhighlighted.

## Related code

- `cmd/ox/web/src/highlight.ts` — `highlight(path, text)`: picks a language
  from the file name and returns the HTML for the file's text. Replaced
  wholesale except for the name and extension tables.
- `cmd/ox/web/src/FileView.tsx` — puts `highlight`'s result into
  `<pre role="tabpanel" innerHTML=…>` from a memo. Unchanged: the signature
  stays synchronous and the result stays the `<pre>`'s contents.
- `cmd/ox/web/src/style.css:389-407` — the tab panel's `<pre>` rule and the
  hand-written `.hljs-*` rules.
- `cmd/ox/web/e2e/workspace.spec.ts`, test "the file viewer shows each
  session's open file" — asserts `.hljs-keyword`, `.hljs-comment`, and
  `.hljs-subst` and their colors.
- `cmd/ox/web/package.json`, `package-lock.json` — the `highlight.js`
  dependency.

## Decisions

- **Shiki setup.** One module-level highlighter built at import with
  `createHighlighterCoreSync` from `shiki/core`, the engine from
  `createJavaScriptRegexEngine()` (`shiki/engine/javascript`), the themes
  `@shikijs/themes/github-light-default` and `github-dark-default`, and the 26
  grammars imported statically from `@shikijs/langs/<id>`: bash, c, css, diff,
  docker, go, ini, java, javascript, jsx, json, jsonc, make, markdown, ocaml,
  python, rust, scss, sql, swift, typescript, tsx, xml, html, yaml, toml.
  Shiki 4.6.0 loads all 26 with the JavaScript engine and tokenizes a mixed
  TSX and Makefile sample in each without error.
- **Tokens, not `codeToHtml`.** `highlight` calls `highlighter.codeToTokens`
  and writes the spans itself. `codeToHtml` wraps its output in its own
  `<pre class="shiki" style="background-color:…" tabindex="0"><code>`, which
  would nest a second scroll container and background inside the tab panel's
  `<pre>`, and it drops the `\r` of every CRLF line break (Shiki splits lines
  on `\r?\n` and joins them with `\n`). From tokens, the result is only
  `<span style="…">` elements, the existing `<pre role="tabpanel">` and its CSS
  stay as they are, and the pane keeps its own background.
- **Dual colors.** Options `themes: { light: "github-light-default", dark:
  "github-dark-default" }, defaultColor: "light-dark()"`. Each token's
  `htmlStyle` then holds `color: light-dark(<light>, <dark>)`, which follows the
  page's `color-scheme: light dark` with no CSS. Each span's `style` attribute
  is `stringifyTokenStyle(token.htmlStyle)` (exported by `shiki/core`).
- **Font styles.** With dual themes, Shiki gives italic and bold (Markdown
  emphasis, strong, headings) only as `--shiki-light-font-style` /
  `--shiki-light-font-weight` and their `-dark-` twins. Both GitHub themes use
  the same font styles, so one rule applies the light ones:
  `.viewer pre[role="tabpanel"] span { font-style: var(--shiki-light-font-style); font-weight: var(--shiki-light-font-weight); }`.
  An unset variable leaves both properties inherited.
- **Carriage returns.** `text.split("\n")` gives the same lines as Shiki's
  token lines. Each line's spans are joined, then `&#13;` is appended when the
  source line ends in `\r`, and the lines are joined with `\n`. A lone `\r`
  stays inside a token's content and is written as `&#13;` by the escape.
- **Plain text.** A file no language claims skips Shiki: `highlight` returns
  the escaped text with no spans, as today.
- **Escape.** One `escape(text)` replaces `&` with `&amp;`, `<` with `&lt;`,
  and `\r` with `&#13;`. It is applied to every token's content and to plain
  text.
- **Construction.** The grammars live in one object keyed by Shiki id
  (`const languages = { bash, c, …, toml }`); `type Language = keyof typeof
  languages`; the highlighter loads `Object.values(languages)`. The name and
  extension tables are `Record<string, Language>`, so a table can name only a
  loaded grammar, and `languageFor` returns `Language | undefined`, with
  `undefined` meaning plain text. Shiki ids differ from highlight.js's in two
  places: `dockerfile` becomes `docker`, `makefile` becomes `make`.
- **Mapping.** As today, except: `.jsx`→`jsx`, `.tsx`→`tsx`, `.jsonc`→`jsonc`,
  `.html`/`.htm`/`.xhtml`→`html`, `.toml`→`toml`, and `.cc`, `.cpp`, `.cxx`,
  `.hh`, `.hpp`, `.hxx` are removed and show as plain text.
- **Trust boundaries.** The file's text reaches the DOM only through `escape`,
  inside spans `highlight` writes. Attribute values come only from the bundled
  theme colors. The returned HTML holds no element but `<span>` and loads
  nothing. The comment above `highlight` states this, replacing the
  highlight.js one.
- **Requirements.** **Syntax highlighting** holds as written: languages still
  come from the file name, colors are GitHub's light or dark to match the page,
  and unrecognized files are plain text. **File viewer updates** keeps its
  memo: an unchanged read is not highlighted or rendered again.
- **Size.** The release `ox` binary grows from about 8.9 MB to about 10.4 MB,
  within the budget the user accepted. The work log reports the measured size.

## Check your assumptions

None.

## Test plan

All in the e2e test "the file viewer shows each session's open file" in
`cmd/ox/web/e2e/workspace.spec.ts`. That file has uncommitted hunks from
another change; edit only this test.

- Go coloring and scheme: replace `.hljs-keyword` with
  `panel.locator("span").first()` (text `package`, color `rgb(207, 34, 46)` in
  light and `rgb(255, 123, 114)` in dark), and `.hljs-comment` with
  `panel.locator("span", { hasText: "//" })` for the unchanged-read and
  changed-file checks.
- Plain text: the existing `span` count of 0 for `a.txt` and `main.txt` stays.
- Carriage returns: the existing `template.ts` `textContent` check stays.
  Remove the `.hljs-subst` count and color checks with the CSS rule they
  guarded.
- TSX: add `src/view.tsx` containing `const v = <div />;\n`; opening it shows a
  span with text `div` colored `rgb(17, 99, 41)` in light. Fails on the old
  code, which ran `.tsx` through the TypeScript grammar.
- Font styles: add `src/notes.md` containing `*a*\n`; opening it shows the span
  `*a*` with `font-style` `italic`. Fails without the font-style rule.

## Implementation plan

1. `cmd/ox/web/package.json`: remove `highlight.js`; add `shiki`,
   `@shikijs/langs`, and `@shikijs/themes` at `^4.6.0` under `dependencies`.
   Run `npm install` in `cmd/ox/web` to update `package-lock.json`.
2. `cmd/ox/web/src/highlight.ts`: rewrite per Decisions — imports, `languages`
   object and `Language` type, highlighter constant, the two tables on Shiki
   ids with the mapping changes, `languageFor`, `escape`, and `highlight`
   building spans from `codeToTokens`.
3. `cmd/ox/web/src/style.css`: delete the syntax-highlighting comment and every
   `.hljs-*` rule; add the font-style rule under a one-line comment naming
   Shiki's dual-theme font variables.
4. `cmd/ox/web/e2e/workspace.spec.ts`: the test changes above.
5. Build the release `ox` binary and record its size in the work log.

## Requirement changes

None.
