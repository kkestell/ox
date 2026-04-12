# Project compaction summary as a system message

## Goal

Stop the LLM from seeing the compaction summary as a user message. The summary
is a structured bullet-point recap of the conversation — not something the user
said. Presenting it as `ChatRole.User` can confuse the model into attributing
summary statements to the user.

## Desired outcome

After compaction, `BuildLlmMessages` yields the summary as a `ChatRole.System`
message immediately after the transient system prompt. The persisted message list
is unchanged — the projection happens at the LLM-call boundary, consistent with
how `BuildLlmMessages` already strips `UsageContent` and clears old tool results.

## Approaches considered

### Option A — Detect summary in BuildLlmMessages, project as System

- Summary: Keep the stored role unchanged. In `BuildLlmMessages`, detect messages
  whose text starts with `<context-summary>` and yield them as `ChatRole.System`
  instead of their stored role.
- Pros: Follows the existing projection pattern in `BuildLlmMessages`. The
  `<context-summary>` tag was designed for exactly this kind of machine
  recognition. Storage format and LLM presentation are decoupled. Explicit and
  documented in one place.
- Cons: Content-based detection via string prefix. Fragile if a user ever types
  `<context-summary>` literally (extremely unlikely).
- Failure modes: False positive on user-typed tag text. Mitigated by the tag
  being a machine-oriented XML fragment no human would type.

### Option B — Change stored role to `ChatRole.System`

- Summary: One-line change in `Autocompactor.cs`. Since `BuildLlmMessages`
  already yields all messages from the list as-is, the summary would pass through
  as System naturally.
- Pros: Simplest possible change. No detection logic.
- Cons: Relies on the implicit convention that `_messages` never contains System
  messages except summaries. Not self-documenting. Breaks the pattern of
  `BuildLlmMessages` being the explicit projection layer. Other code (e.g.
  `SerializeConversation` on re-compaction) would label it `[system]` instead of
  `[user]` — minor but changes the summarizer's input.
- Failure modes: Future code adding System messages to the list would
  silently change semantics.

## Recommended approach

**Option A.** `BuildLlmMessages` is already the projection layer between
persistence and the LLM. Adding summary-role projection there is consistent with
its existing responsibilities (UsageContent stripping, tool result clearing). The
tag-based detection uses the exact machine-recognition mechanism the tag was
designed for.

## Related code

- `src/Ur/AgentLoop/AgentLoop.cs` — `BuildLlmMessages` (line 160): the projection
  layer where the fix goes. Already imports `Ur.Compaction`.
- `src/Ur/Compaction/Autocompactor.cs` — `TryCompactAsync` (line 118): creates
  the summary message with `ChatRole.User`. Comment at line 117 explains the
  rationale that this plan supersedes. `SummaryOpenTag` constant (line 41) is
  the detection key.
- `src/Ur/Sessions/UrSession.cs` — `RunTurnAsync` (line 209): calls
  `TryCompactAsync` on `_messages`, then passes the same list into
  `AgentLoop.RunTurnAsync`, which calls `BuildLlmMessages`.
- `src/Ur/Compaction/SummaryPrompt.cs` — The summarization prompt. Output is
  structured, neutral, and reads like system context, not user speech.
- `tests/Ur.Tests/AutocompactorTests.cs` — `HighFill_CompactionOccurs` (line 38)
  asserts `ChatRole.User` on the stored message. Must be updated.

## Current state

- `BuildLlmMessages` applies two projections: (1) `ToolResultClearer` replaces
  old tool results, (2) `UsageContent` is filtered out. Both produce transient
  views without mutating the persisted list.
- The summary message is always the first element of `_messages` after compaction
  (it replaces `messages[0..cutPoint)`). This means it's naturally the first
  message yielded from the list — right after the transient system prompt.
- `SummaryOpenTag` and `SummaryCloseTag` are `internal const` in
  `Autocompactor`, already visible to `AgentLoop` which imports the namespace.
- No code outside `Autocompactor.cs` and its tests references the summary tags.

## Implementation plan

- [x] **Add `IsCompactionSummary` helper to `Autocompactor`** — A static
  `internal` method: `IsCompactionSummary(ChatMessage msg)` returns true if any
  `TextContent` in the message starts with `SummaryOpenTag`. Putting it on
  `Autocompactor` keeps the detection logic co-located with the tag constants.

- [x] **Project summary messages as System in `BuildLlmMessages`** — In the
  `foreach` loop over `projected`, check `Autocompactor.IsCompactionSummary(msg)`
  before the existing `UsageContent` fast path. If true, yield a new
  `ChatMessage(ChatRole.System, ...)` with the same content list (minus any
  `UsageContent`) and `continue`. This is the third projection step; update the
  method's XML doc to document it.

- [x] **Update the comment block in `Autocompactor.TryCompactAsync`** — Replace
  the "User role is chosen deliberately" comment (lines 117–121) with a note
  explaining that the stored role is cosmetic: `BuildLlmMessages` projects the
  summary as a System message at send time, so the persisted role doesn't reach
  the LLM.

- [x] **Update `AutocompactorTests.HighFill_CompactionOccurs`** — Change the
  role assertion from `ChatRole.User` to whatever stored role we settle on
  (User is fine since the projection happens elsewhere). Add a comment
  clarifying that the LLM-facing role is tested via `BuildLlmMessages` coverage,
  not here.

## Structural considerations

**Hierarchy**: `AgentLoop` already depends on `Ur.Compaction` (it imports the
namespace). Calling `Autocompactor.IsCompactionSummary` doesn't create a new
dependency edge.

**Abstraction**: `BuildLlmMessages` is the right abstraction level for this. It
already owns the contract "transform persisted messages into what the LLM sees."
Adding role projection is a natural extension of that contract, not a leak.

**Encapsulation**: The `SummaryOpenTag` constant and the detection logic both
live on `Autocompactor`. External code calls a named method rather than
inspecting tag strings directly.

**Modularization**: No new modules needed. The detection method belongs on
`Autocompactor` (owns the tags), and the projection belongs in `BuildLlmMessages`
(owns the LLM message pipeline).

## Validation

- Run `AutocompactorTests` — all existing tests should pass (they test the
  stored message, not the projected one).
- Manually test with boo: trigger compaction in a long conversation, inspect the
  messages sent to the LLM (via debug logging or provider request inspection) and
  confirm the summary appears as `ChatRole.System`.
