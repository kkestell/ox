# Render messages and child reports as Markdown

## Goal

Messages from other sessions and child reports are agent-written but show as
raw text, so `**Files:**`, backticked paths, and `- ` lists appear literally.
They render as Markdown under the same rules as replies. The user's own prompts
stay plain text.

## Related code

- `cmd/ox/web/src/SessionView.tsx` — `PromptBubble` renders each report's
  `report.text`, and `MessageBubble` renders `props.input.text` for both prompts
  and messages (`from` set), as plain text. Replies and reasoning use `Markdown`.
- `cmd/ox/web/src/Markdown.tsx` — the one renderer for model output: raw HTML
  escaped, images disabled, links in a new tab.
- `cmd/ox/web/src/style.css` —
  `.prompt` sets `white-space: pre-wrap`, and `.markdown code` and
  `.markdown pre` use `var(--secondary)`, the prompt bubble's own background.
- `cmd/ox/web/e2e/session.spec.ts` — "a reply renders Markdown", "a child
  session shows its title, its directory, and its parent's message, also after a
  reload", and "a child's reply reaches its parent as a report headed with its
  title, also after a reload".

## Decisions

- **Requirements:** Messages in the transcript, Child reports, and Replies as
  Markdown. Rendering is a pure function of the text `oxd` already sends, so
  pending, taken, reloaded, and second-page views all render the same way. The
  rule that agent-written text renders and the user's text does not lives in
  `MessageBubble`: it renders `Markdown` when `input.from` is set and plain text
  otherwise. Reports always render `Markdown`.
- **Construction:** `Markdown` stays the single renderer, so messages and
  reports get the reply rules (escaped HTML, no images, safe links) without
  restating them.
- **Styling:** `.markdown` sets `white-space: normal` so it overrides the
  bubble's `pre-wrap`, which would otherwise turn the newlines markdown-it emits
  between blocks into blank lines. Inside a bubble, `.prompt .markdown code` and
  `.prompt .markdown pre` use `var(--background)` so code stands out against the
  bubble's `var(--secondary)`.

## Check your assumptions

1. **Messages and reports render as Markdown; your prompts do not.**
   - **Plan:** Text other sessions send, and child reports, show formatted, like
     replies. What you type shows exactly as typed. Approved by the user.
   - **Alternative:** Render your own prompts too, which would reformat text
     you wrote, such as `*` or `_` in pasted code.

## Test plan

- e2e, "a child session shows its title, its directory, and its parent's
  message, also after a reload": send `message: "**hello** child"`; the existing
  `toHaveText("child parenthello child")` still holds, and the message article's
  `getByRole("strong")` has text `hello`, before and after the reload. Fails
  before the change, when the article holds the literal asterisks.
- e2e, "a child's reply reaches its parent as a report headed with its title,
  also after a reload": create the child with `message: "**hello**"`; the
  fake's echo carries it into the report, and the report region's
  `getByRole("strong")` has text `hello`, before and after the reload. Fails
  before the change.
- e2e, "a reply renders Markdown": the session's `Prompt` article contains the
  literal `**bold**` and has no `strong` or `list`, guarding that the user's
  prompt stays plain text.

## Implementation plan

1. `SessionView.tsx`: in `PromptBubble`, render each report as
   `<Markdown text={report.text} />` under its `.from` header. In
   `MessageBubble`, render `<Markdown text={props.input.text} />` when
   `props.input.from !== undefined`, and `props.input.text` otherwise.
2. `style.css`: add `white-space: normal` to the `.markdown` rule, and add
   `.prompt .markdown code, .prompt .markdown pre { background: var(--background); }`
   (with `.markdown pre code` keeping `background: none`).
3. `e2e/session.spec.ts`: the three test changes above.
4. `docs/functional-requirements.md`: the changes below.

## Requirement changes

- **Changed:** Messages in the transcript.
  - Old: "A message shows in the recipient's transcript as its text, headed with
    the label of the session that sent it, pending at the end of the transcript
    until Claude takes it, on every page and after a reload."
  - New: "A message shows in the recipient's transcript rendered as Markdown
    under the same rules as replies, headed with the label of the session that
    sent it, pending at the end of the transcript until Claude takes it, on
    every page and after a reload."
- **Changed:** Child reports. In the sentence "Reports wait until … each headed
  with its child's label; a child waiting on the user does not hold them back.",
  "each headed with its child's label" becomes "each headed with its child's
  label and rendered as Markdown". The rest of the requirement is unchanged.
