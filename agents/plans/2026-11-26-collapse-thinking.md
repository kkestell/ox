# Collapse thinking behind a Thinking row

## Goal

Claude's thinking summaries fill the transcript and bury its replies. Each
stretch of reasoning shows as one collapsed Thinking row, styled like a tool
call, that expands to the full reasoning. Replies stay as they are.

Ox routes nothing wrongly. `internal/claude/agent.go` publishes `text_delta`
as replies and `thinking_delta` as reasoning, and falls back to the result's
text when a turn streamed no reply. In the last 20 Claude sessions in the
user's store, every turn ended with a reply, and reasoning outweighed reply
text about 4 to 1 (349k against 92k characters). Collapsing the reasoning hides
no content the user relies on.

## Related code

- `cmd/ox/web/src/SessionView.tsx` — `Transcript` renders a `reasoning` entry
  as `<article class="reasoning" aria-label="Thinking">`. `ToolCall` is the
  `<details class="tool">` row to copy, and `keepRowInPlace` on the transcript
  keeps any clicked `<summary>` in place.
- `cmd/ox/web/src/style.css` — `.log .reasoning` (muted text) and the
  `details.tool` rules for the row's summary, spacing, and marker slot.
- `cmd/ox/web/src/ox.tsx` — consecutive `reasoning_delta` updates append to one
  `reasoning` entry; a snapshot reconciles entries by position, so an open row
  stays open across a reconnect, as tool calls do.
- `cmd/ox/web/e2e/session.spec.ts` — "reasoning shows in the transcript, before
  and after a reload", driven by the fake harness's `think <text>` input.

## Decisions

- **Row.** A `reasoning` entry renders as
  `<details class="reasoning" aria-label="Thinking">`, closed by default. Its
  `<summary>` has an icon in the tool row's marker slot, lucide's `brain.svg`,
  and the name "Thinking". Its body is the `Markdown` of the reasoning,
  indented to the summary's text like a tool call's card, in muted text, with
  no card. The open state is the element's own, as for tool calls: it lasts
  while the page shows the session, survives a reconnect, and resets on
  reload.
- **Shared row styles.** The `details.tool` rules for the summary's layout,
  hidden disclosure marker, and collapsed spacing apply to
  `details:is(.tool, .reasoning)`, so adjacent collapsed rows of either kind
  pack the same way. Card and `pre` rules stay on `.tool`.
- **Keeping the row in place.** `keepRowInPlace` already handles any
  `<details>` in the transcript. Its comment says "the row" instead of "the
  tool call".
- **Requirements:** Reasoning changes as below. Tool calls is unchanged.
  Following output holds: a collapsed row does not grow as reasoning streams.
- **Ur.** Ur's reasoning is the same entry kind and collapses the same way.
- **Overlap.** The jump-to-bottom worktree (AcJTUL9hkHz) rewrites the
  transcript's `div.log`, which carries `onClick={keepRowInPlace}`, and edits
  `style.css` and `session.spec.ts` elsewhere. This change touches the
  `reasoning` `Match`, the reasoning and tool-row CSS, and the reasoning test.
  Whichever lands second keeps `keepRowInPlace` on the transcript.

## Check your assumptions

1. **Thinking is collapsed by default.**
   - **Plan:** each stretch of reasoning shows as a "Thinking" row, closed,
     like a tool call. Clicking it shows the reasoning; it is closed again
     after a reload. Requirement change: Reasoning, old text "When Claude or
     Ur reasons, the transcript shows the reasoning in full where it happened,
     streaming in as Markdown under the same rules as replies. Claude's
     reasoning is its own summary of its thinking." New text below.
   - **Alternative:** remember each row's open state across reloads, which
     needs stored per-row state for little gain.
2. **The row reads "Thinking" at all times.**
   - **Plan:** the label stays "Thinking" while the reasoning streams and
     after.
   - **Alternative:** "Thinking…" while it streams and "Thinking" after, which
     adds a streaming state to the row.

## Test plan

- e2e, in "reasoning shows in the transcript, before and after a reload"
  (renamed "reasoning shows collapsed in the transcript, before and after a
  reload"): the Thinking group is visible and its Markdown hidden; clicking
  its summary shows "Count to three." with `three` strong; after a reload the
  row is collapsed again. Fails on the old code, which renders an article
  with the text visible.
- e2e, in the same test: clicking the summary leaves its top where it was on
  screen, as "Tool calls" requires of tool rows.

## Implementation plan

1. `cmd/ox/web/src/SessionView.tsx`: render the `reasoning` `Match` as the
   `<details>` row above; update `keepRowInPlace`'s comment.
2. `cmd/ox/web/src/style.css`: replace `.log .reasoning` with the row's body
   styles and icon (`--icon: url(lucide-static/icons/brain.svg)`), and widen
   the shared `details.tool` summary and spacing rules to
   `details:is(.tool, .reasoning)`.
3. `cmd/ox/web/e2e/session.spec.ts`: update the reasoning test as above.
4. `docs/functional-requirements.md`: replace Reasoning with the approved text.

## Requirement changes

- **Changed:** Reasoning.
  - Old: **Reasoning.** When Claude or Ur reasons, the transcript shows the
    reasoning in full where it happened, streaming in as Markdown under the
    same rules as replies. Claude's reasoning is its own summary of its
    thinking.
  - New: **Reasoning.** When Claude or Ur reasons, the transcript shows a
    collapsed Thinking row where it happened; expanding it shows the reasoning
    in full, streaming in as Markdown under the same rules as replies, and
    keeps its summary where it was on screen.
