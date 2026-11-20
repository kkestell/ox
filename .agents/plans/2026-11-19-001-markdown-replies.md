# Render replies and the ExitPlanMode plan as Markdown

## Goal

From [`docs/todo.md`](../../docs/todo.md) (Prompting): "Render replies as
Markdown. Replies and the `ExitPlanMode` plan show as plain text."

When done, Claude's reply text in the transcript renders as Markdown while it
streams and after, and an `ExitPlanMode` plan renders as Markdown on its
approval card and in its transcript tool card. Prompts, tool input and
results, failed results, and `exited` entries stay plain. Model output never
reaches the page as live HTML: raw HTML in a reply shows as text, images are
not loaded, and links open in a new tab.

The change is confined to the web client. `oxd`, the relay, and the protocol
are untouched: replies already arrive as `text_delta` fragments that the page
concatenates, and the plan arrives as `input.plan` of an `approval` and a
`tool_use` update.

## Related code

- `cmd/ox/web/src/ox.tsx:76-81` — `apply` appends each `text_delta` to the last
  `reply` entry's `text`; a reply's full text is always in the store, so the
  view can re-render it whole on every fragment.
- `cmd/ox/web/src/SessionView.tsx:113-131` — `Transcript` renders `reply().text`
  as a text node in `<div class="reply">`; the change point for replies.
- `cmd/ox/web/src/SessionView.tsx:133-152` — `ToolCall`, the transcript tool
  card: summary with the first line of `toolSubject`, card with
  `toolInputJSON` and the result in `<pre>`.
- `cmd/ox/web/src/ApprovalRequest.tsx:46-55` — the approval card shows
  `toolSubject(input)` (for `ExitPlanMode`, the whole plan, `pre-wrap`) in the
  `<summary>`, and the input JSON when expanded.
- `cmd/ox/web/src/SessionView.tsx:16-41` — `pinToBottom` follows `childList`,
  `subtree`, and `characterData` mutations, so replacing a reply's children on
  each fragment keeps the log pinned.
- `cmd/ox/web/src/style.css:148` — `.prompt, .reply, .error { white-space: pre-wrap }`.
- `cmd/ox/web/e2e/session.spec.ts` — fake-harness specs; the fake replies
  `"You said: " + text`, streamed a word at a time (`internal/fake/fake.go:120`),
  so the prompt's text controls the reply's Markdown.
- `cmd/ox/web/e2e/approval.spec.ts`, `cmd/ox/web/e2e/services.ts:26-32` — the
  Haiku spec and its catalog, which offers `manual` and `acceptEdits` only.
- `internal/server/live_test.go:196-207` — Haiku reliably calls `ExitPlanMode`
  when asked in a `plan` session, through a `can_use_tool` approval.

## Decisions

- **Library: `markdown-it` 15** (ships its own types, one runtime dependency
  tree, CommonMark plus tables and strikethrough). One module instance,
  configured once:
  - `html: false` (its default, written out): raw HTML in the source is
    escaped to text. With raw HTML off, markdown-it's output contains only the
    tags its own rules emit, so it is safe for `innerHTML` without a separate
    sanitizer.
  - Its default `validateLink` stays: `javascript:`, `vbscript:`, `file:`, and
    non-image `data:` URLs do not become links.
  - `linkify: true`: bare URLs become links, as Claude often writes them.
  - `md.disable("image")`: an image is never fetched, so model output cannot
    make the page load a URL by itself. `![alt](url)` then renders as `!`
    followed by a link to `url`.
  - A `link_open` renderer rule sets `target="_blank"` and
    `rel="noopener noreferrer"` on every link, so clicking a link in a reply
    never navigates the Ox page away and discards unsent text.
- **Component:** `Markdown(props: { text: string; class?: string })` in a new
  `cmd/ox/web/src/Markdown.tsx` renders
  `<div class={["markdown", props.class]} innerHTML={md.render(props.text)} />`.
  The `innerHTML` expression tracks `props.text`, so each `text_delta`
  re-renders the reply's whole text. Rendering the whole reply per fragment is
  quadratic in the reply's length; replies are a few kilobytes, so this is
  kept until measured otherwise.
- **Streaming:** partial Markdown renders as markdown-it parses it at that
  moment. An unclosed fence renders as a code block to the end of the text;
  an unclosed `**` shows literally until its closing fragment arrives. No
  buffering or special handling.
- **Surfaces:**
  - Reply entries: `<Markdown class="reply" text={reply().text} />`.
  - `ExitPlanMode` approval card: when `tool_name === "ExitPlanMode"` and
    `input.plan` is a string, the card shows
    `<Markdown class="plan" text={plan} />` between the "Allow ExitPlanMode?"
    line and the `details`, and the `details` summary reads `Input` instead of
    repeating the plan. The expanded JSON stays, so Allow still sends the input
    as the card shows it.
  - `ExitPlanMode` transcript tool card: the same condition puts
    `<Markdown class="plan" text={plan} />` first in `.card`, before the input
    JSON and the result. The one-line summary detail is unchanged.
  - A helper `planOf(tool_name: string, input: Record<string, unknown>)` in
    `ox.tsx`, next to `toolSubject`, returns the plan string or `undefined`,
    used by both cards.
  - Everything else stays plain: prompts, tool input and results, failed
    `result` entries, `exited` entries, approval subjects for other tools.
- **Styling scope:** `.reply` leaves the `pre-wrap` rule (Markdown output has
  newlines between block tags that `pre-wrap` would show as blank lines).
  A `.markdown` block in `style.css`, using existing variables only: first and
  last child margins zero; paragraph, list, `pre`, blockquote, table spacing
  of `0.5rem`; headings at `1.125rem`/`1rem`/`0.875rem` weight 600; lists
  `padding-left: 1.5rem`; inline `code` in `--font-mono` `0.8125rem` on
  `--secondary` with `0.25rem` rounding; `pre` on `--secondary`,
  `overflow-x: auto`, `0.5rem 0.75rem` padding, `0.75rem` mono, no wrapping;
  blockquote with a `--border` left rule and `--muted-foreground` text;
  tables collapsed with `--border` cell borders; `hr` as a `--border` line;
  links in `color: inherit` with `text-decoration: underline`. `.approval .plan` gets the card's
  `--secondary` background, padding, and `max-height: 20rem; overflow-y: auto`
  like `.approval .input pre`; `details.tool .card .plan` gets
  `0.5rem 0.75rem` padding and a bottom `--border` line like `pre + pre`.
  No syntax highlighting.
- **Guarantees:**
  - 2 (typed text stays): links open in a new tab, so following one leaves the
    page and its unsent text in place.
  - 7 (Allow sends the input as the card shows it): the `ExitPlanMode` card
    renders the plan and keeps the full input JSON behind `Input`; the answer
    path is unchanged.
  - The rest are untouched; rendering reads the same store entries and adds
    no request, state, or wait.
- **Ownership:** no new state. The rendered HTML is derived in the view from
  the store's entry text and the approval's input.
- **Claude Code:** the plan is `input.plan` (`docs/claude-code.md` §13:
  `ExitPlanMode{plan}`), and `ExitPlanMode` in a `plan`
  session asks through `can_use_tool` (`internal/server/live_test.go:196`).
  If `input.plan` is missing or not a string, the cards fall back to today's
  rendering. The live check below confirms the plan arrives on the card.
- **Trust boundaries:** replies and plans are model output, which a prompt
  injection can steer. With raw HTML escaped, unsafe link schemes refused,
  and images disabled, model output can produce only markdown-it's own tags
  and user-clicked links that open in a new tab; it cannot run script or make
  the page fetch anything.

## Check your assumptions

None.

## Test plan

- **Reply rendering, safety, and plain prompts — e2e** (`session.spec.ts`, new
  test; no existing test asserts reply structure, and the fake harness setup
  there already reaches it). Name: "a reply renders as Markdown and its
  prompt stays plain". Send, through `prompt(page).fill(...)` and Enter, a
  multi-line prompt such as:

  ```
  **bold** `code` <b>raw</b> [site](https://example.com) [bad](javascript:alert(1)) ![pic](https://example.com/p.png) https://example.org

  - one
  - two
  ```

  Wait for Send to be visible, then assert:
  - `.reply strong` has text `bold`; `.reply code` has text `code`;
    `.reply li` has texts `["one", "two"]`.
  - `.reply b` has count 0 and `.reply` contains text `<b>raw</b>`.
  - `.reply img` has count 0, and a `.reply a` with text `pic` has
    `href` `https://example.com/p.png`.
  - `.reply a` with text `site` has `href` `https://example.com`, `target`
    `_blank`, and `rel` `noopener noreferrer`.
  - No `.reply a` has an `href` starting with `javascript:`, and `.reply`
    contains text `[bad](javascript:alert(1))`.
  - A `.reply a` has `href` `https://example.org/` or `https://example.org`
    (linkify; assert with `toHaveAttribute` on the link whose text is
    `https://example.org`).
  - `.prompt` has the prompt's literal text (contains `**bold**` and
    `<b>raw</b>`), and `.prompt strong` has count 0.

  The fake streams the reply a word at a time, so the test passes through
  partial renders on the way. Each condition the change adds (Markdown
  rendering, `html: false`, image disabled, link target, linkify, plain
  prompt) has an assertion that fails without it. Existing `.reply`
  `toHaveText` assertions keep passing because Playwright normalizes
  whitespace.
- **`ExitPlanMode` plan on the approval card and tool card — live**
  (`approval.spec.ts`, new test; the fake harness raises no approvals, and
  `TestLive` cannot observe the page). In `services.ts`, add
  `{ id: "plan", name: "Plan" }` to `claude.permission_modes`. Name: "an
  ExitPlanMode plan renders as Markdown". Open a new session, select
  `plan` in `select[name=permission_mode]`, and send: "Load and call the
  ExitPlanMode tool now with this plan, as a Markdown list of exactly two
  items: first step, second step." Assert, with the 120 s timeouts the
  existing test uses:
  - `.approval .plan li` count is at least 2 (use
    `.approval .plan li` `.nth(1)` `toBeVisible`), and
    `.approval summary .subject` has text `Input`.
  - Answer Deny and Submit; wait for Send.
  - Click the summary of the `details.tool` whose `.name` is `ExitPlanMode`
    and assert `.plan li` inside it is visible.

## Implementation plan

1. `cmd/ox/web/package.json`: add `markdown-it` `^15.0.3` to `dependencies`
   with `npm --prefix cmd/ox/web install markdown-it@^15.0.3`, updating
   `package-lock.json`.
2. `cmd/ox/web/src/Markdown.tsx` (new): the module-level markdown-it instance
   configured as in Decisions, with a short comment that raw HTML is escaped
   and images disabled because the text is model output, and the `Markdown`
   component.
3. `cmd/ox/web/src/ox.tsx`: add `planOf` next to `toolSubject`.
4. `cmd/ox/web/src/SessionView.tsx`: render reply entries with `Markdown`;
   in `ToolCall`, show `<Markdown class="plan" …/>` first in `.card` when
   `planOf(props.tool.name, props.tool.input)` returns a plan.
5. `cmd/ox/web/src/ApprovalRequest.tsx`: when `planOf` returns a plan, show
   it with `Markdown` before the `details`, and use `Input` as the summary
   subject.
6. `cmd/ox/web/src/style.css`: drop `.reply` from the `pre-wrap` rule; add
   the `.markdown`, `.approval .plan`, and `details.tool .card .plan` rules.
7. `cmd/ox/web/e2e/session.spec.ts`, `cmd/ox/web/e2e/approval.spec.ts`,
   `cmd/ox/web/e2e/services.ts`: the tests above.
8. Run `make test`, `make e2e`, and `make live` (the live spec is the only
   check of the `ExitPlanMode` cards). The report names the views touched:
   the session transcript (replies, tool cards) and the approval card.

## Documentation updates

- `docs/architecture.md`, Web UI table: add `Markdown.tsx` to the components
  row.
- `docs/todo.md`: remove the "Render replies as Markdown" item and the empty
  Prompting heading.
