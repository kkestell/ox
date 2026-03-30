# Ur.Tui Spike: Widgets, Layout, and Modals

## Goal

Build a standalone TUI spike on top of Ur.Terminal that implements the core widgets, layout system, and modal flows described in `docs/cli-tui.md` — without any dependency on the Ur library. The spike uses dummy data and fake event streams so we can iterate on the UI in isolation. The code should be structured so that swapping in real `UrHost`/`UrSession`/`UrConfiguration` later is a clean substitution, not a rewrite.

## Desired outcome

A running `Ur.Tui` app that demonstrates:

- **MessageList** component: renders messages bottom-up with word wrap, scrolling, role-based coloring, and streaming simulation
- **ChatInput** component: single-line text entry, submit on Enter, slash command dispatch
- **ApiKeyModal** component: masked text input, Enter to submit, Esc to dismiss/exit
- **ModelPickerModal** component: searchable, scrollable list of dummy models, type-to-filter, arrow nav, Enter to select
- **Layout**: two-layer compositor (base + overlay), arithmetic layout that adapts to terminal resize
- **First-run flow**: API key modal → model picker → chat (mirrors the real boot sequence)
- **Turn simulation**: submitting a message triggers a fake streaming response via `IAsyncEnumerable<AgentLoopEvent>` with realistic timing
- **Slash commands**: `/model` opens the picker, `/quit` exits

All widget code should be testable against `TestTerminal` — render to a buffer, assert cell contents.

## Related documents

- `docs/cli-tui.md` — Full TUI architecture: ChatState, DisplayMessage, layer architecture, layout, components, key routing, slash commands, turn execution, cancellation
- `docs/terminal-framework.md` — Framework architecture consumed by the TUI

## Related code

- `Ur.Terminal/Components/IComponent.cs` — The contract all TUI widgets implement (`Render`, `HandleKey`)
- `Ur.Terminal/Core/Buffer.cs` — Drawing surface: `WriteString`, `DrawBox`, `Fill`, `Set`/`Get`
- `Ur.Terminal/Core/Rect.cs` — Layout primitive
- `Ur.Terminal/Rendering/Layer.cs` — Compositable layer with shadow mask
- `Ur.Terminal/Rendering/Compositor.cs` — Stacks layers, handles shadow dimming
- `Ur.Terminal/App/RenderLoop.cs` — Frame loop: drains keys, calls processFrame, composes, diffs, flushes
- `Ur.Terminal/Input/KeyEvent.cs` — Key + modifiers + char
- `Ur.Terminal/Terminal/TestTerminal.cs` — Mock terminal for widget unit tests
- `Ur.Tui/Program.cs` — Current demo app (will be replaced by this spike)
- `Ur/UrConfiguration.cs` — Real readiness/blocker API we'll mock: `Readiness`, `AvailableModels`, `SetApiKeyAsync`, `SetSelectedModelAsync`
- `Ur/UrSession.cs` — Real `RunTurnAsync` returning `IAsyncEnumerable<AgentLoopEvent>` we'll simulate
- `Ur/AgentLoop/AgentLoopEvent.cs` — Event types: `ResponseChunk`, `ToolCallStarted`, `ToolCallCompleted`, `TurnCompleted`, `Error`
- `Ur/Providers/ModelInfo.cs` — `ModelInfo(Id, Name, ContextLength, MaxOutputTokens, InputCostPerToken, OutputCostPerToken, SupportedParameters)`

## Current state

- `Ur.Terminal/` is complete: core types, compositor, screen diff, key input, render loop — all tested
- `Ur.Tui/` has a framework demo (modal toggle, shadow, key display) but no real widgets or app structure
- `Ur.Tui.csproj` references only `Ur.Terminal` — no Ur library dependency, and we keep it that way for this spike
- No test project for TUI code yet

## Constraints

- **No Ur library dependency.** `Ur.Tui.csproj` must not reference `Ur.csproj`. Dummy types live in `Ur.Tui` for now. When we integrate, we swap these for the real types.
- .NET 10.0, xUnit for tests, following existing `Method_Scenario_Expected` naming
- All components implement `IComponent` from `Ur.Terminal`
- Frame-based architecture: all state mutation happens during the event-processing phase (between key drain and render), never during render
- Single-line input in v1 (Enter submits, no Shift+Enter)
- No Markdown rendering — plain text only

## Approach

### Dummy types mirroring the Ur library

Define minimal stand-ins inside `Ur.Tui` that match the shape of the real Ur types. This means the app code will call methods like `configuration.Readiness`, `configuration.SetApiKeyAsync(key)`, `session.RunTurnAsync(input)` — the exact same call sites that the real integration will use. The only difference is the implementation behind them.

Dummy types needed:
- `DummyConfiguration` — wraps a mutable `ApiKey` and `SelectedModelId`. Returns `UrChatReadiness`-shaped data. `AvailableModels` returns a hardcoded list of ~20 `DummyModelInfo` entries. We don't reuse the real `UrChatReadiness`/`UrChatBlockingIssue`/`ModelInfo` types (they live in the Ur assembly), but we mirror their shape exactly.
- `DummySession` — `RunTurnAsync(string input)` returns `IAsyncEnumerable<DummyAgentLoopEvent>` that simulates streaming: emits `ResponseChunk` tokens with 30-80ms delays, occasionally emits `ToolCallStarted`/`ToolCallCompleted`, then `TurnCompleted`.
- `DummyAgentLoopEvent` hierarchy — mirrors `AgentLoopEvent`: `ResponseChunk`, `ToolCallStarted`, `ToolCallCompleted`, `TurnCompleted`, `Error`. Same property names.

### Application structure

The app entry point (`Program.cs`) follows the lifecycle from the architecture doc:
1. Boot dummy configuration
2. Initialize terminal (raw mode, alt buffer, hide cursor)
3. Create compositor with base layer + overlay layer
4. Check readiness → if blockers, show modals (API key, then model picker)
5. Enter chat loop: frame-based render loop with key routing, turn execution, slash commands
6. Exit: `/quit`, Ctrl+C at idle, Esc during first-run

A `ChatApp` class (not a component — it *owns* components) encapsulates `ChatState`, key routing, the slash command table, and the turn runner. It provides the `processFrame` callback to `RenderLoop`.

### Widget design

Each widget is an `IComponent`. Widgets are stateful — they own their mutable state (text buffer, scroll position, filter string, etc.). The app reads state from widgets (e.g., `chatInput.Text` after Enter) and tells widgets what to do (e.g., `chatInput.Clear()`). Widgets never reach up to app state.

---

## Implementation plan

### Phase 1: Project structure + ChatState + dummy types

- [x] **Create `Ur.Tui.Tests/Ur.Tui.Tests.csproj`** — xUnit test project referencing `Ur.Tui` and `Ur.Terminal`. Add to `Ur.slnx`.
- [x] **Define `ChatState`** — `Ur.Tui/State/ChatState.cs`. Fields: `List<DisplayMessage> Messages`, `IComponent? ActiveModal`, `int ScrollOffset`, `bool IsTurnRunning`. The single mutable state object for the app.
- [x] **Define `DisplayMessage`** — `Ur.Tui/State/DisplayMessage.cs`. Fields: `MessageRole Role`, `StringBuilder Content`, `bool IsStreaming`, `string? ToolName`, `bool IsError`, `DateTimeOffset Timestamp`. Enum `MessageRole`: `User`, `Assistant`, `Tool`, `System`.
- [x] **Define dummy types** — `Ur.Tui/Dummy/`:
  - `DummyBlockingIssue` enum: `MissingApiKey`, `MissingModelSelection`
  - `DummyReadiness` class: `bool CanChat`, `IReadOnlyList<DummyBlockingIssue> BlockingIssues`
  - `DummyModelInfo` record: `string Id`, `string Name`, `int ContextLength`, `decimal InputCostPerToken`, `decimal OutputCostPerToken`
  - `DummyConfiguration` class: mutable `ApiKey`/`SelectedModelId`, `DummyReadiness Readiness` property, `IReadOnlyList<DummyModelInfo> AvailableModels` (hardcoded ~20 models), `SetApiKey(string)`, `SetSelectedModel(string)`
  - `DummyAgentLoopEvent` hierarchy: abstract base + `DummyResponseChunk { Text }`, `DummyToolCallStarted { CallId, ToolName }`, `DummyToolCallCompleted { CallId, ToolName, Result, IsError }`, `DummyTurnCompleted`, `DummyError { Message, IsFatal }`
  - `DummySession` class: `async IAsyncEnumerable<DummyAgentLoopEvent> RunTurnAsync(string input, CancellationToken ct)` — generates fake streaming response with delays

### Phase 2: ChatInput component

- [x] **Implement `ChatInput`** — `Ur.Tui/Components/ChatInput.cs`. Implements `IComponent`.
  - State: `StringBuilder _text`, `int _cursorPos`
  - `Render`: draws a prompt marker (e.g., `> `) followed by the text content. Highlights the cursor position.
  - `HandleKey`:
    - Printable char → insert at cursor, advance
    - Backspace → delete behind cursor
    - Delete → delete at cursor
    - Left/Right → move cursor
    - Home/End → jump to start/end
    - Enter → returns false (signal to app that input was submitted)
  - Public: `string Text` (current content), `void Clear()` (reset after submit)
- [x] **Test `ChatInput_Render_ShowsPromptAndText`** — render into a buffer, assert `> hello` appears
- [x] **Test `ChatInput_HandleKey_PrintableChar_InsertsAtCursor`** — type 'a', verify text is "a"
- [x] **Test `ChatInput_HandleKey_Backspace_DeletesBehindCursor`** — type "ab", backspace, text is "a"
- [x] **Test `ChatInput_HandleKey_Enter_ReturnsFalse`** — Enter returns false (submit signal)
- [x] **Test `ChatInput_HandleKey_ArrowKeys_MovesCursor`** — left/right navigation

### Phase 3: MessageList component

- [x] **Implement `MessageList`** — `Ur.Tui/Components/MessageList.cs`. Implements `IComponent`.
  - Takes `ChatState` (read-only reference to messages + scroll offset)
  - `Render`: draws messages bottom-up. Latest message at the bottom of the rect, previous messages stack upward. Each message gets:
    - Role prefix and color: `You: ` (cyan), assistant text (white), `[tool: name]` (yellow), `System: ` (red for error, gray otherwise)
    - Word wrapping: break long lines at the rect width, accounting for the prefix on the first line
    - Streaming indicator: append `▍` cursor to the last line of a streaming assistant message
    - Scroll offset shifts the viewport upward
  - `HandleKey`: PageUp/PageDown adjust scroll offset. Return true if consumed.
  - Clip: if messages overflow the top of the rect, the topmost visible message is clipped (partially rendered)
- [x] **Implement word wrap utility** — `Ur.Tui/Util/WordWrap.cs`. Static method: `List<string> Wrap(string text, int width)`. Breaks at spaces when possible, hard-breaks words longer than width.
- [x] **Test `WordWrap_ShortLine_NoWrap`** — text fits in width, returns single line
- [x] **Test `WordWrap_LongLine_WrapsAtSpace`** — breaks at word boundary
- [x] **Test `WordWrap_LongWord_HardBreaks`** — word longer than width gets split
- [x] **Test `MessageList_Render_SingleUserMessage`** — one user message renders with `You: ` prefix
- [x] **Test `MessageList_Render_MultipleMessages_BottomUp`** — latest message at bottom of rect
- [x] **Test `MessageList_Render_ScrollOffset_ShiftsViewport`** — with scroll offset, older messages become visible
- [x] **Test `MessageList_Render_StreamingMessage_ShowsCursor`** — streaming assistant message ends with `▍`
- [x] **Test `MessageList_Render_LongMessage_WordWraps`** — message exceeding rect width wraps

### Phase 4: ApiKeyModal component

- [x] **Implement `ApiKeyModal`** — `Ur.Tui/Components/ApiKeyModal.cs`. Implements `IComponent`.
  - State: `StringBuilder _text`, `int _cursorPos`, `bool Submitted`, `bool Dismissed`, `string? Value`
  - `Render`: centered bordered box (fixed width ~50, height ~7). Title: "API Key". Input field shows `*` for each character. Hint text: "Enter your OpenRouter API key" and "Esc to cancel".
  - `HandleKey`:
    - Printable char → append to text
    - Backspace → delete
    - Enter → set `Submitted = true`, `Value = _text.ToString()`, return false
    - Escape → set `Dismissed = true`, return false
    - Other → return true (consumed, no-op)
  - Public: `bool Submitted`, `bool Dismissed`, `string? Value`
- [x] **Test `ApiKeyModal_Render_ShowsBorderAndTitle`** — verify box-drawing chars and "API Key" text
- [x] **Test `ApiKeyModal_Render_MasksInput`** — type "secret", rendered chars are all `*`
- [x] **Test `ApiKeyModal_HandleKey_Enter_SetsSubmitted`**
- [x] **Test `ApiKeyModal_HandleKey_Escape_SetsDismissed`**

### Phase 5: ModelPickerModal component

- [x] **Implement `ModelPickerModal`** — `Ur.Tui/Components/ModelPickerModal.cs`. Implements `IComponent`.
  - Constructor takes `IReadOnlyList<DummyModelInfo>`
  - State: `string _filter`, `int _selectedIndex`, `int _scrollOffset`, `bool Submitted`, `bool Dismissed`, `DummyModelInfo? SelectedModel`
  - `Render`: centered bordered box (width ~60, height ~20). Title: "Select Model". Filter input at top. Scrollable list of models below, filtered by name/ID containing filter text. Selected model highlighted. Detail area at bottom shows context length and pricing for selected model.
  - `HandleKey`:
    - Printable char → append to filter, reset selection to 0
    - Backspace → delete from filter
    - Up/Down → move selection (clamp to filtered list bounds, scroll if needed)
    - Enter → set `Submitted = true`, `SelectedModel` to current selection, return false
    - Escape → set `Dismissed = true`, return false
  - Must handle large model lists efficiently: filter once per keystroke, render only visible window
- [x] **Test `ModelPickerModal_Render_ShowsModelList`** — verify model names appear in the box
- [x] **Test `ModelPickerModal_Filter_NarrowsList`** — type "claude", only claude models shown
- [x] **Test `ModelPickerModal_ArrowKeys_MoveSelection`** — down arrow moves highlight
- [x] **Test `ModelPickerModal_Enter_SetsSelectedModel`**
- [x] **Test `ModelPickerModal_Escape_SetsDismissed`**
- [x] **Test `ModelPickerModal_Filter_ResetsSelection`** — typing resets selection to first match

### Phase 6: ChatApp + layout + key routing

- [x] **Implement `ChatApp`** — `Ur.Tui/ChatApp.cs`. Not an `IComponent` — it owns components and state.
  - Owns: `ChatState`, `MessageList`, `ChatInput`, `DummyConfiguration`, `DummySession?`
  - Owns: `Layer baseLayer`, `Layer overlayLayer` (refs to compositor layers)
  - Provides `bool ProcessFrame(ReadOnlySpan<KeyEvent> keys)` callback for `RenderLoop`
  - **Layout** (computed each frame):
    - `inputHeight = 1` (v1 single-line)
    - `messageHeight = screenHeight - inputHeight`
    - `messageRect = Rect(0, 0, width, messageHeight)`
    - `inputRect = Rect(0, messageHeight, width, inputHeight)`
    - If modal active: compute centered modal rect + shadow rect
  - **Key routing** (per the architecture doc):
    1. If `ActiveModal` not null → `modal.HandleKey(key)`. If modal returns false → check `Submitted`/`Dismissed`, handle accordingly (set API key, select model, dismiss, exit)
    2. If no modal → `chatInput.HandleKey(key)`. If Enter (returns false):
       - If text starts with `/` → dispatch slash command
       - Otherwise → submit message, start turn
    3. Ctrl+C → cancel turn if running, exit if idle
  - **Slash command table**: `Dictionary<string, Action<string>>` — `/model` opens `ModelPickerModal`, `/quit` exits
  - **Render** (each frame, after key processing):
    1. Clear base layer, render `MessageList` and `ChatInput` into base layer content at their rects
    2. Clear overlay layer. If modal active, render modal into overlay, stamp shadow mask
  - **Turn execution**:
    1. Add user `DisplayMessage`, create streaming assistant `DisplayMessage`
    2. Fire-and-forget `DummySession.RunTurnAsync` → drain events into a `ConcurrentQueue`
    3. Each frame, drain the event queue: append chunks to streaming message, add tool messages, finalize on `TurnCompleted`
- [x] **Implement first-run flow** — On startup, check `DummyConfiguration.Readiness`. If `MissingApiKey` → set `ActiveModal = new ApiKeyModal()`. On API key submit → set key, check readiness again. If `MissingModelSelection` → set `ActiveModal = new ModelPickerModal(config.AvailableModels)`. On model select → set model. Then enter chat.
- [x] **Test `ChatApp_FirstRun_ShowsApiKeyModal`** — fresh config (no key, no model) → `ActiveModal` is `ApiKeyModal`
- [x] **Test `ChatApp_AfterApiKey_ShowsModelPicker`** — submit API key → `ActiveModal` switches to `ModelPickerModal`
- [x] **Test `ChatApp_AfterModelSelect_EntersChat`** — select model → `ActiveModal` is null, chat is active
- [x] **Test `ChatApp_SlashQuit_ExitsFalse`** — type `/quit`, Enter → `ProcessFrame` returns false
- [x] **Test `ChatApp_SlashModel_OpensModelPicker`** — type `/model`, Enter → `ActiveModal` is `ModelPickerModal`
- [x] **Test `ChatApp_SubmitMessage_AddsToChatState`** — type "hello", Enter → `ChatState.Messages` has user message + streaming assistant message
- [x] **Test `ChatApp_CtrlC_AtIdle_ExitsFalse`** — Ctrl+C with no turn running → returns false

### Phase 7: Wire up Program.cs + manual testing

- [x] **Rewrite `Ur.Tui/Program.cs`** — Replace the framework demo with the real app:
  ```
  Boot DummyConfiguration (no key, no model)
  Create AnsiTerminal, Compositor, layers, KeyReader
  Create ChatApp(config, compositor layers)
  Run RenderLoop with ChatApp.ProcessFrame
  Cleanup on exit
  ```
- [x] **Manual verification**:
  - App starts, shows API key modal
  - Type a key, see `*` characters, Enter submits
  - Model picker appears with list, type to filter, arrows to navigate, Enter selects
  - Chat appears, type a message, see dummy streaming response
  - `/model` reopens picker, `/quit` exits
  - Ctrl+C cancels a running turn, Ctrl+C at idle exits
  - Resize terminal mid-session — layout adapts
  - Esc during first-run modals exits cleanly

---

## Open questions

- **Modal dismiss vs. exit semantics during chat:** During first-run, Esc on the API key modal exits the app (per the architecture doc). But once in chat, if the user opens the model picker via `/model`, should Esc just dismiss the modal (back to chat) or exit? Current thinking: Esc always dismisses the modal. First-run API key modal is special — if dismissed, exit. All other modals just close.
- **Turn cancellation UX:** When Ctrl+C cancels a turn, should the partial streaming message be kept or removed? Current thinking: keep it as-is (finalize the `DisplayMessage` with `IsStreaming = false`), matching the architecture doc.
- **Cursor rendering in ChatInput:** The framework has no cursor concept — we're drawing cells. Should we render the cursor as an inverted-color cell at the cursor position? Or a block character? Current thinking: invert fg/bg at cursor position. Simple and visible.

## Risks and follow-up

- **Word wrap performance.** Re-wrapping all messages every frame could be expensive with many long messages. For the spike this is fine. In production, we'd cache wrapped lines and invalidate on resize.
- **Model list size.** The real catalog has 345+ models. The dummy list should include enough (~20) to test scrolling and filtering, but the component must be designed for hundreds. Filter on keystroke, render only the visible window.
- **Turn event queue threading.** `DummySession.RunTurnAsync` runs on a background task producing events. The frame loop drains them. `ConcurrentQueue` handles this, same as the architecture doc specifies. No lock contention issues expected at frame rates.
- **Integration seam.** The dummy types mirror the real Ur API shape. When integrating: delete `Ur.Tui/Dummy/`, add a project reference to `Ur`, and replace `DummyConfiguration`/`DummySession` with `UrConfiguration`/`UrSession`. The `ChatApp` call sites (`config.Readiness`, `session.RunTurnAsync`, etc.) should require minimal changes. The main risk is event type mismatches — the dummy `AgentLoopEvent` hierarchy must stay in sync with the real one.
