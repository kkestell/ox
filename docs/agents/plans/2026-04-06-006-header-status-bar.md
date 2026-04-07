# Header Status Bar

## Goal

Move the session ID out of the conversation EventList and into a persistent header
bar pinned to the top of the screen. The header shows the session ID left-aligned
and the current context fill percentage right-aligned, separated by a heavy
horizontal rule — visually mirroring the bottom input area.

## Desired outcome

```
20260406-204144-842                               14%
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
├─ ● Hello!
...
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
❯
─────────────────────────────────────────────────────
■ ■ ■ ■ ■                        claude-sonnet-4-6
```

- Session ID is BrightBlack (dim), left-aligned in the header row.
- Context percentage is BrightBlack, right-aligned. Displays after the first turn
  completes (when token usage is first known). Format: `14%`.
- The heavy rule below the header matches the top rule above the input area.
- The welcome TextRenderable is removed from the EventList entirely.

## Related code

- [src/Ur.Tui/Rendering/Viewport.cs](src/Ur.Tui/Rendering/Viewport.cs) — owns the layout; all row offsets change
- [src/Ur.Tui/Program.cs](src/Ur.Tui/Program.cs) — creates the welcome message (to remove); sets model ID; EventRouter handles TurnCompleted
- [src/Ur/AgentLoop/AgentLoopEvent.cs](src/Ur/AgentLoop/AgentLoopEvent.cs) — `TurnCompleted` needs an `InputTokens` field
- [src/Ur/AgentLoop/AgentLoop.cs](src/Ur/AgentLoop/AgentLoop.cs) — `StreamLlmAsync` yields `ChatResponseUpdate`; accumulate `Usage.InputTokenCount` here
- [src/Ur/Providers/ModelInfo.cs](src/Ur/Providers/ModelInfo.cs) — `ContextLength` is the denominator for the percentage
- [src/Ur/Providers/ModelCatalog.cs](src/Ur/Providers/ModelCatalog.cs) — `GetModel(id)` looks up `ModelInfo` by model ID

## Current state

- The session ID is rendered as a `TextRenderable` with `BubbleStyle.Plain` added to
  the EventList at startup (Program.cs:76-78). It scrolls away with the conversation.
- The Viewport has a 5-row input area at the bottom (top rule + input + bottom rule +
  status line + blank). No header area exists at the top.
- `TurnCompleted` carries no payload — there is no token usage in any event.
- The `AgentLoop` iterates `ChatResponseUpdate` objects but only extracts `TextContent`
  and `FunctionCallContent`; `ChatResponseUpdate.Usage` (a `UsageDetails?`) is ignored.
- Model context length is available via `host.Configuration.ModelCatalog.GetModel(id)?.ContextLength`.

## Structural considerations

**Hierarchy:** The layout change is fully internal to Viewport — no new public interfaces.
The only new surface is `SetSessionId()` and `SetContextPercent()`, which mirror the
existing `SetModelId()` pattern.

**Abstraction:** Token usage belongs at the AgentLoop layer (it comes from the LLM
response). Propagating it via `TurnCompleted` keeps the event stream as the clean
boundary between the agent and UI layers — the UI doesn't reach into the session.

**Modularization:** Percentage computation (inputTokens / contextLength) happens in
Program.cs's EventRouter, which already owns the logic for mapping events to viewport
calls. It has access to `host` (for model lookup) and `session` (for active model ID).

**Encapsulation:** The header rendering is entirely internal to `Redraw()`. Nothing
external needs to know about the new row offsets.

## Implementation plan

### 1. Accumulate token usage in AgentLoop

- [ ] In `AgentLoop.StreamLlmAsync`, accumulate `update.Usage?.InputTokenCount` across
  all yielded `ChatResponseUpdate` objects into a local `long? inputTokens`.
  Since `StreamLlmAsync` doesn't return a value, thread it out via a second side-channel
  holder (similar to `LlmErrorHolder`) so `RunTurnAsync` can read it after the loop.
- [ ] Add `long? InputTokens` to `TurnCompleted` (nullable: not set if the provider
  returned no usage data). Update the `yield return new TurnCompleted()` at
  AgentLoop.cs:106 to include the accumulated value.

### 2. Add header state to Viewport

- [ ] Add fields `_sessionId` (string?) and `_contextPercent` (int?) to Viewport.
- [ ] Add `SetSessionId(string id)` — stores `_sessionId`, sets `_dirty = true`.
- [ ] Add `SetContextPercent(int? percent)` — stores `_contextPercent`, sets `_dirty = true`.
- [ ] Add `private const int HeaderRows = 2;` alongside the existing `InputAreaRows = 5`.

### 3. Update Viewport layout in Redraw()

The current layout (0-indexed buffer rows, `viewportHeight = height - InputAreaRows`):

```
Rows 0 .. viewportHeight-1   — conversation
Row viewportHeight            — top rule (━)
Row viewportHeight+1          — input text
Row viewportHeight+2          — bottom rule (─)
Row viewportHeight+3          — status line
Row viewportHeight+4          — blank
```

New layout (`viewportHeight = height - InputAreaRows - HeaderRows`):

```
Row 0                          — header: session ID + context %
Row 1                          — header rule (━)
Rows 2 .. viewportHeight+1    — conversation
Row viewportHeight+2           — top rule (━)
Row viewportHeight+3           — input text
Row viewportHeight+4           — bottom rule (─)
Row viewportHeight+5           — status line
Row viewportHeight+6           — blank
```

- [ ] Change `viewportHeight` calculation: `var viewportHeight = height - InputAreaRows - HeaderRows;`
- [ ] Render header row at buffer row 0:
  - Left: `_sessionId` in BrightBlack (or empty if null).
  - Right: `_contextPercent` formatted as `"14%"` in BrightBlack, right-aligned (pad with spaces between).
- [ ] Render header rule (heavy `━`) at buffer row 1.
- [ ] Shift conversation rendering: start writing conversation rows at offset `HeaderRows`
  (i.e., the slice of `allRows` that was written at rows 0..viewportHeight-1 now writes
  at rows HeaderRows..viewportHeight+HeaderRows-1).
- [ ] Shift all remaining fixed rows down by `HeaderRows`:
  - Top rule: `viewportHeight + HeaderRows`
  - Input row: `viewportHeight + HeaderRows + 1`
  - Bottom rule: `viewportHeight + HeaderRows + 2`
  - Status line: `viewportHeight + HeaderRows + 3`
  - Blank row: implicit (buffer is zero-initialized to Cell.Empty each frame)
- [ ] Update the layout comment at the top of Viewport.cs to reflect the new row assignments.

### 4. Wire up in Program.cs

- [ ] Remove the welcome `TextRenderable` block (lines 74-78): delete `welcome`, `welcome.SetText(...)`, and `eventList.Add(welcome, ...)`.
- [ ] Before `viewport.Start()`, call `viewport.SetSessionId(session.Id)`.
- [ ] In `EventRouter.RouteAsync`, in the `case TurnCompleted tc:` branch (Program.cs:519
  and 602), after existing handling:
  - Look up `host.Configuration.ModelCatalog.GetModel(session.ActiveModelId)?.ContextLength`
  - If `tc.InputTokens` is non-null and `ContextLength > 0`, compute
    `percent = (int)Math.Round(tc.InputTokens.Value / (double)contextLength * 100)`
    and call `viewport.SetContextPercent(percent)`.
  - The EventRouter does not currently hold a `host` reference; pass `host` into its
    constructor or capture it in the closure alongside `viewport`.

### 5. Tests

- [ ] Add a unit test in `TuiRenderingTests` that constructs a Viewport with a 24-row
  terminal, calls `SetSessionId("20260406-204144-842")` and `SetContextPercent(14)`,
  and asserts that:
  - Row 0 starts with `"20260406-204144-842"` (BrightBlack cells)
  - Row 0 ends with `"14%"` (BrightBlack cells)
  - Row 1 is all `'━'` characters
  - Conversation rows begin at row 2

## Validation

- `make inspect` — run after implementation; fix any issues before committing.
- Manual: launch `ur chat`, confirm header row appears immediately with session ID and
  no `%` (before first turn). After first turn, confirm context percentage appears.
- Manual: confirm the welcome message no longer appears in the conversation list.
- Manual: resize terminal while running — confirm header stays pinned and conversation
  area adjusts correctly.
