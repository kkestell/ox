# Wide Markdown tables scroll inside their own box

## Goal

Resolves [#8](https://github.com/kkestell/ox/issues/8). On a phone, a reply holding a Markdown table wider than the screen makes the whole transcript scroll sideways, cutting off the start of every line once dragged. After the change, a table's cells wrap to fit when they can, a table that still cannot fit scrolls sideways inside its own box, and the transcript never scrolls horizontally.

## Related code

- `cmd/ox/web/src/style.css:253` — `.markdown table { border-collapse: collapse; }`. A table is as wide as the sum of its columns' narrowest widths, so a column of inline-code paths (`cmd/ox/web/src/FileView.tsx:14`) pushes it past the transcript's width; nothing clips or scrolls it short of `.log` (`style.css:233`), whose `overflow-y: auto` makes it the horizontal scroller too.
- `cmd/ox/web/src/style.css:250` — `.markdown pre` already scrolls sideways in its own box with `overflow-x: auto`; the table rule follows it.
- `cmd/ox/web/src/Markdown.tsx` — every reply, tool-call plan, and approval plan renders through `Markdown` with the `markdown` class, so the one rule covers all three.
- `cmd/ox/web/e2e/session.spec.ts:96` — `reply()` sends a prompt in a new session and returns its reply; the fake echoes the prompt as `You said: …`, so a prompt of Markdown becomes a reply of Markdown.
- `cmd/ox/web/e2e/session.spec.ts:1220` — `test.describe("on a touch screen")`, at 390 × 844 with touch.

## Decisions

- **Cause.** Rendering all 508 stored Claude replies from the local database through `style.css` at 390 px wide: 63 overflow the transcript, in both Chromium and Playwright's WebKit, and in all 63 the overflowing element is a table, its width set by inline-code paths in its cells. No reply overflows through a paragraph, list, inline code outside a table, link, or `pre`; `overflow-wrap: break-word` on `.markdown` already wraps those. The issue's "long inline code spans" are the cells of such tables.
- **Fix.** `.markdown table` gains `display: block; max-width: 100%; overflow-x: auto;`. The table box becomes as wide as the reply, its rows lay out inside it at their natural width with cells wrapping to fit, and only what still does not fit scrolls inside the box. With the rule applied to the same 508 replies, none overflows the transcript in either engine; 66 of 85 tables scroll inside their box and 19 fit. Breaking inline code anywhere instead would keep every table on screen but split paths mid-name and squeeze columns to a character or two.
- **Scope in `style.css`.** The change touches only the `.markdown table` rule. The rules other open fixes edit (page height, side padding, chevron focus) stay as they are.
- **Requirements:** **Replies as Markdown** and **Tool calls** (a plan renders as Markdown) keep their behavior; a table now stays within the reply. **Narrow screens** is unchanged. The change is CSS only, so no request lifecycle is involved.
- **Construction.** The invariant is that nothing inside a Markdown block can widen the transcript. Text wraps by the existing `overflow-wrap`; the two elements that cannot wrap, `pre` and `table`, each scroll in their own box.

## Check your assumptions

1. **A wide table scrolls sideways within the reply.** Kept.
   - **Plan:** A table whose cells cannot wrap narrow enough, such as a column of file paths, keeps each path whole and scrolls sideways inside its own box, the way code blocks already do. The rest of the reply stays put.
   - **Alternative:** Break paths and words anywhere so every table fits the screen without scrolling; on a phone, a three-column table of paths would show each cell as a narrow stack of fragments.

## Test plan

- A reply with a wide table on a 390 px touch screen leaves the transcript without horizontal scroll, and the table fits on screen: e2e, a new test in `test.describe("on a touch screen")` in `session.spec.ts`, named `a wide table scrolls inside its reply instead of the transcript`. It sends, through `reply()`, a prompt of a line of text, a blank line, and a two-column table (`| Title | Location |`) whose rows each hold an inline-code path of about 40 characters, such as `` `cmd/ox/web/e2e/workspace.spec.ts:352` ``. The leading line keeps the fake's `You said: ` prefix out of the header row. It asserts `await expect(markdown.getByRole("table")).toBeInViewport({ ratio: 1 })` and that the transcript's `scrollWidth` equals its `clientWidth` (`expect.poll` on `transcript(page).evaluate(...)`), since horizontal scroll has no ARIA state. No existing test renders a table or runs a reply at a narrow width. Before the change, the table's right edge sits at about 600 px, so both assertions fail; prove this by running the test against the unchanged `style.css`.
- Only a real iPhone in Safari can confirm the rest: that dragging sideways on a table scrolls the table and not the transcript, that a vertical drag starting on a table still scrolls the transcript, and that the original session no longer shows a scrollbar under the last message. The suite runs Chromium only; Playwright's WebKit on macOS matched Chromium on all 508 stored replies, before and after the fix, but is not iOS Safari.

## Implementation plan

- In `cmd/ox/web/src/style.css`, change `.markdown table { border-collapse: collapse; }` to `.markdown table { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; }`.
- Add the test above to `cmd/ox/web/e2e/session.spec.ts`.

## Requirement changes

None.
