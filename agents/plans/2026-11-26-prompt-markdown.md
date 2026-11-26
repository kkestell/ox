# Render prompts as Markdown

## Goal

A user's own prompt shows as raw text (`think Count to **three**.` shows the asterisks) while replies, reasoning, messages from other sessions, and child reports render as Markdown. The prompt should render as Markdown under the same rules as replies.

## Related code

- `cmd/ox/web/src/SessionView.tsx` — `MessageBubble` renders `props.input.text` as a plain string when `from` is undefined, and `<Markdown>` otherwise. `PromptBubble` renders sent and queued prompts through it, so the queued prompt is covered by the same change.
- `cmd/ox/web/src/style.css` — `.prompt, .error` set `white-space: pre-wrap`; `.markdown` resets it to normal.
- `cmd/ox/web/e2e/session.spec.ts` — "your own prompt stays plain text" asserts the old behavior.
- `docs/functional-requirements.md` — "Prompts as sent".

## Decisions

- **Requirements:** "Prompts as sent" changes; "Messages in the transcript" and "Replies as Markdown" already define the rules the prompt now follows. A prompt renders from its text alone, so it renders the same in flight (local and queued), after a reload, and on a second page.
- **Construction:** `MessageBubble` renders `<Markdown text={props.input.text ?? ""} />` for every prompt; the `from` header stays the only difference for messages. This removes the plain-text branch.
- **Whitespace:** Every Markdown view keeps single line breaks (`breaks: true` in markdown-it), at the user's direction. `.prompt` stops setting `pre-wrap`; `.error` keeps it.
- **Overlap:** PR #137 (`remove-thinking-icon`) edits the Thinking row in `SessionView.tsx` and `style.css`. This change touches `MessageBubble` and the `.prompt, .error` rule only, so no textual overlap is expected.

## Test plan

- Prompt renders Markdown (strong, list), e2e: replace "your own prompt stays plain text" in `session.spec.ts` with "your own prompt renders Markdown", asserting `strong` and `list` inside the `Prompt` article and no literal `**`.
- Queued prompt renders Markdown, e2e: extend the same spec only if an existing queued-prompt spec in `session.spec.ts` can carry one assertion; otherwise covered by the shared bubble.

## Implementation plan

1. `SessionView.tsx`: in `MessageBubble`, render the `from` header only when `from` is defined, and `<Markdown text={props.input.text ?? ""} />` always.
2. `style.css`: change `.prompt, .error { white-space: pre-wrap; ... }` so `pre-wrap` applies to `.error` alone; keep `overflow-wrap: break-word` on both.
3. `session.spec.ts`: flip the test as above.
4. `docs/functional-requirements.md`: apply the requirement change.
5. Run `make test` and `make e2e`.

## Requirement changes

- **Changed:** Prompts as sent.
  - Old: Prompts show in the transcript as the plain text the user sent, followed by the images attached to them.
  - New: **Prompts as sent.** Prompts show in the transcript rendered as Markdown under the same rules as replies, followed by the images attached to them.
