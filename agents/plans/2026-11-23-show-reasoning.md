# Show Claude's and Ur's reasoning in the transcript

## Goal

Claude launched with `-p` streams empty thinking, and Ur drops the reasoning
OpenRouter streams. Neither reaches the transcript, and Ur does not return
reasoning to the model on multi-step tool turns. After the change, both
harnesses publish their reasoning as a new `reasoning_delta` update, the daemon
records it in the history, the page shows it as a collapsed Thinking row in the
transcript, and Ur returns the current turn's reasoning in its requests.
Resolves #49.

## Related code

- `internal/claude/claude.go` `startProcess` — Claude's launch flags.
- `internal/claude/agent.go` `handleOutput`, case `stream_event` — publishes
  top-level `text_delta`; thinking deltas are dropped by the `delta.Type`
  filter.
- `internal/claude/messages.go` `message.Event.Delta` — decodes only `.text`.
- `internal/ur/stream.go` `chunk`, `read`, `message` — parses `content` and
  `tool_calls` only; `message` is both the request shape and the saved
  conversation entry.
- `internal/ur/ur.go` `Handle` (`delta`), `conversation`, `body` — saves each
  streamed fragment as an `assistant` entry, joins a response's entries into one
  request message, and builds the request.
- `internal/agent/agent.go` — update types.
- `internal/server/history.go` `record`, `entries` — records listed types and
  joins consecutive `text_delta` fragments of one message.
- `internal/server/mcp.go` `renderEntry` — `read_session` rendering; unknown
  types return `false` and are skipped.
- `internal/fake/fake.go` — echo harness used by server tests and `make e2e`.
- `cmd/ox/web/src/protocol.ts` `OtherUpdate`; `cmd/ox/web/src/ox.tsx` `Entry`,
  `apply`; `cmd/ox/web/src/SessionView.tsx` `Transcript`, `ToolCall`;
  `cmd/ox/web/src/style.css` `details.tool` rules.

## Decisions

- **One update type.** `reasoning_delta` (`agent.UpdateReasoningDelta`) carries
  a fragment of top-level reasoning in `text`, for both harnesses, exactly as
  `text_delta` carries reply text. The page appends consecutive fragments into
  one reasoning entry; a `text_delta`, `tool_use`, or any other entry between
  them starts a new one. The history joins consecutive `reasoning_delta`
  fragments of one message the way it joins `text_delta`.
- **Claude.** `startProcess` adds `--thinking-display=summarized` to every
  launch, new and resumed. `handleOutput` publishes each top-level
  `content_block_delta` whose `delta.type` is `thinking_delta` with nonempty
  `.thinking` as `reasoning_delta`. `messages.go` decodes `delta.thinking`.
  Subagent lines (nonempty `parent_tool_use_id`) stay out, per **Main
  conversation only**. The complete `thinking` block on the `assistant` line is
  ignored, as complete text blocks are; `signature_delta` and
  `redacted_thinking` carry no text and are ignored. `streamedText` and the
  result fallback concern reply text only and do not change.
- **Ur, parsing.** `chunk.Choices[].Delta` gains `reasoning_details`, a list of
  `reasoningDetail{Type, ID, Format, Index, Text, Summary, Data, Signature}`
  with `omitempty` on every field except `type` and `index`. Ur reads
  `reasoning_details` only and ignores the duplicate `delta.reasoning` string.
  The displayable text of a detail is `text` for `reasoning.text`, `summary` for
  `reasoning.summary`, and none for `reasoning.encrypted`. `read`'s callback
  becomes `onDelta(text string, reasoning []reasoningDetail)`, called for a
  chunk with either.
- **Ur, saving.** `message` gains `ReasoningDetails []reasoningDetail
  `json:"reasoning_details,omitempty"``. `Handle(delta)` saves one `assistant`
  entry per chunk carrying its content and its reasoning fragments, then
  publishes `reasoning_delta` with the joined displayable text if nonempty, then
  `text_delta` if the content is nonempty. A save failure ends the turn as
  today. Saved files without the field load unchanged.
- **Ur, returning.** `conversation` joins a response's reasoning fragments into
  its message and merges consecutive fragments with the same `index`:
  concatenate `text`, `summary`, and `data`, and keep the first nonempty `type`,
  `id`, and `format` and the last nonempty `signature`. This is the form a
  non-streamed response returns; a live probe (DeepSeek V4.1 Flash, effort low)
  showed fragments of `{"type":"reasoning.text","text":…,"format":"unknown","index":0}`
  and OpenRouter accepting the merged detail on the assistant message of the
  next request. `body` sends `reasoning_details` only on assistant messages
  after the conversation's last `user` message, the current turn's tool loop,
  where reasoning models require it. Earlier turns' reasoning stays saved and is
  not sent, so a model change between turns never sends one model's reasoning
  to another, and requests do not grow with every earlier turn's reasoning.
- **`read_session`.** `renderEntry` already skips unknown types, so other
  sessions' Claude reads no reasoning. `docs/protocol.md` lists
  `reasoning_delta` with `tool_result` and `parked` as not shown.
- **Fake harness.** A prompt `think <text>` streams `<text>` word by word as
  `reasoning_delta`, then the usual echo reply, so server and e2e tests reach
  the new path.
- **Page.** `Entry` gains `{ kind: "reasoning"; text: string }`. `Transcript`
  renders it as `<details class="reasoning" aria-label="Thinking">`, collapsed,
  whose `<summary>` holds the lucide `brain` icon in the marker column and the
  word Thinking, matching a tool call's row; the body is `<Markdown>` in muted
  text, indented to the tool card's left edge. The entry object is mutated in
  place, so an open row stays open and fills as fragments arrive.
- **Requirements.** **One transcript** holds because `reasoning_delta` is
  recorded and joined like `text_delta`, so a reload, a second page, and a
  reconnect rebuild the same entries. **Main conversation only** holds by the
  `parent_tool_use_id` filter. **Replies as Markdown**'s rules apply to
  reasoning through the same `Markdown` component. **Following output** is
  unchanged; a collapsed row does not grow. **Settings shown are settings
  used** is untouched: the new flag does not depend on any setting.
- **Settings.** `--thinking-display=summarized` is passed on every launch, so a
  parked session resumes with it and no control request changes it.
- **Construction.** Reasoning has one source per harness: Claude's
  `thinking_delta` events and Ur's `reasoning_details`. Ur's saved entries are
  the only copy of its reasoning; the request form is derived from them in
  `conversation` and `body`.
- **Ownership.** No new state, timer, or lock. One stored field: Ur's saved
  `reasoning_details`, owned by the session goroutine through `save`. It is
  needed because each request of a tool turn is built from the saved
  conversation, its only copy, and the model needs the turn's reasoning back.
  It is written with its fragment, so it has no race with the request that
  reads it, and it goes with the conversation file. A resume always starts a
  new turn, so no resumed request sends saved reasoning.
- **Claude Code.** `docs/harnesses/claude-code.md` (`stream_event`) records
  empty thinking without the flag; the findings in #49 record that
  `--thinking-display summarized` streams summarized text in `thinking_delta`
  and fills the final block with a `signature`, and that
  `showThinkingSummaries` in `--settings` changes nothing (2.1.330). The
  `TestLive` case confirms the flag end to end, and the doc gains these facts.

## Check your assumptions

1. **A new requirement for reasoning in the transcript.**
   - **Plan:** Add **Reasoning**: "When Claude or Ur reasons, the transcript
     shows a collapsed Thinking row where the reasoning happened; expanding it
     shows the reasoning, streaming in as Markdown under the same rules as
     replies. Claude's reasoning is its own summary of its thinking."
   - **Alternative:** Leave reasoning out of the requirements, so a later change
     may drop or move it without the user's approval.
2. **Reasoning starts collapsed.**
   - **Plan:** Each stretch of reasoning shows as one Thinking row, collapsed,
     the way tool calls do; the user expands the ones they want to read. The
     transcript stays as long as it is today.
   - **Alternative:** Show reasoning expanded in muted text above each reply.
     The user reads it without a click, at the cost of a transcript several
     times longer to scroll.
3. **Reasoning streams live.**
   - **Plan:** The Thinking row appears as soon as the model starts reasoning,
     and an expanded row fills as the reasoning streams in, so the user can see
     what the model is doing during a long turn.
   - **Alternative:** Show the row only once the reasoning ends. The user waits
     with no sign of progress through long reasoning.

## Test plan

- Claude passes `--thinking-display=summarized` on new and resumed launches:
  Claude harness, `TestClaudeRunsAsAnOxSession` in
  `internal/claude/claude_test.go`.
- Top-level nonempty `thinking_delta` publishes `reasoning_delta` with its
  text and the prompt's message ID; an empty `thinking_delta`, a subagent's,
  and the complete `thinking` block on an `assistant` line publish nothing:
  Claude harness, new `TestThinkingIsPublished` in
  `internal/claude/prompt_test.go`, built with `promptingAgent`. No existing
  test feeds thinking events.
- Ur publishes `reasoning_delta` before `text_delta` for a response that
  reasons, and no reasoning update for an encrypted detail: Ur,
  `TestTwoTurns`, with an `sse` variant that streams `reasoning_details`
  fragments first. Its second request carries no `reasoning_details` for the
  first turn.
- Ur returns the turn's merged `reasoning_details` on the assistant message
  that called the tools: Ur, `TestShellAuto`, whose first response streams two
  `reasoning.text` fragments of index 0 before its calls; the second request's
  assistant message carries one detail with the joined text.
- The history records `reasoning_delta` and joins consecutive fragments of one
  message, separately from adjacent `text_delta`: server,
  `TestHistoryMergesTextAndKeepsConversation`.
- The page shows a collapsed Thinking row, expands to the streamed reasoning,
  and shows it again after a reload: e2e, new test in
  `cmd/ox/web/e2e/session.spec.ts` sending `think …` to the fake harness. No
  existing spec sends a prompt the fake answers with reasoning.
- Claude at effort high streams reasoning end to end: live, a `TestLive`
  section that creates a Haiku session at effort `high`, sends a prompt that
  needs a few steps of arithmetic, and waits for a `reasoning_delta` with
  nonempty text before the `result`.
- Ur returns reasoning on a live tool turn: live, `TestLiveUr`'s manual shell
  turn already exercises the follow-up request; assert a `reasoning_delta`
  arrives in that turn.

## Implementation plan

1. `internal/agent/agent.go`: add `UpdateReasoningDelta = "reasoning_delta"`.
2. `internal/claude/messages.go`: decode `delta.thinking`.
   `internal/claude/agent.go`: publish top-level nonempty `thinking_delta` as
   `reasoning_delta`. `internal/claude/claude.go`: add
   `--thinking-display=summarized` to `args`. Tests above.
3. `internal/ur/stream.go`: add `reasoningDetail`, the `reasoning_details`
   field on `chunk` deltas and on `message`, and `read`'s `onDelta(text,
   reasoning)`; `stream` delivers `delta{turn, text, reasoning}`.
   `internal/ur/ur.go`: `Handle(delta)` saves and publishes as decided;
   `conversation` joins and merges details; `body` strips details before the
   last `user` message. Tests above.
4. `internal/server/history.go`: record `reasoning_delta`, and join
   consecutive fragments of one message for both delta types. Test above.
5. `internal/fake/fake.go`: the `think <text>` prompt, documented in the
   header comment of `cmd/ox/web/e2e/session.spec.ts` beside the other fake
   prompt forms.
6. `cmd/ox/web/src/protocol.ts`: add `{ type: "reasoning_delta"; text: string }`.
   `cmd/ox/web/src/ox.tsx`: the `reasoning` entry and its `apply` case.
   `cmd/ox/web/src/SessionView.tsx`: the Thinking row. `cmd/ox/web/src/style.css`:
   `details.reasoning` rules sharing the tool row's summary and spacing, a
   `.brain` icon from `lucide-static`, muted body text. E2e test above.
7. `internal/server/live_test.go`: the `TestLive` section and the
   `TestLiveUr` assertion.
8. Documentation updates below.

## Requirement changes

- **Added**, under Transcript after **Tool calls**: **Reasoning.** When Claude
  or Ur reasons, the transcript shows a collapsed Thinking row where the
  reasoning happened; expanding it shows the reasoning, streaming in as
  Markdown under the same rules as replies. Claude's reasoning is its own
  summary of its thinking.

## Documentation updates

- `docs/protocol.md`: the `reasoning_delta` row in the update table; the
  recorded update list and the joining of consecutive `reasoning_delta`
  fragments in `session/get`'s history; `reasoning_delta` among the updates
  `read_session` does not show.
- `docs/harnesses/claude-code.md`: `--thinking-display summarized` in the
  launch command and flag list (accepted, absent from `--help`, 2.1.330); the
  `stream_event` bullet on thinking replaced with: without the flag every
  `thinking_delta` is empty and the final block is
  `{"type":"thinking","thinking":""}`; with it `thinking_delta` streams
  summarized text and the final block carries it with a `signature`;
  `showThinkingSummaries` in `--settings` changes neither.
