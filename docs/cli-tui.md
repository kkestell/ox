# TUI Chat Client (Ur.Tui)

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

The terminal chat client for Ur. A persistent chat UI with a message list, multi-line input, modal overlays, and a slash command system. Built on the [Ur.Terminal](terminal-framework.md) framework. Consumes the Ur library's public API (`UrHost`, `UrSession`, `UrConfiguration`, `ExtensionCatalog`) and renders `AgentLoopEvent` streams as a live chat experience.

The detailed runtime/orchestration design lives in [tui-runtime.md](tui-runtime.md). This document stays focused on the TUI as a building block; the runtime doc captures the missing application layer between render-loop integration and leaf widgets.

### Non-Goals

- Does not own Ur domain business rules. Session persistence, readiness evaluation, extension lifecycle, and turn execution are library concerns.
- Does not handle session resume in v1. Always creates a new session.
- Does not render Markdown or rich text in v1. Assistant responses are plain text.
- Does not implement its own terminal primitives. All cell/buffer/layer/compositor work is delegated to Ur.Terminal.

The TUI does own interaction workflow: routing keys, driving first-run setup, dispatching slash commands, and projecting backend events into display state.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Ur.Terminal (framework) | Cell grid, compositing, render loop, input, IComponent contract | All framework types |
| Ur (library) | Session creation, turn execution, readiness, model catalog | `UrHost`, `UrSession`, `UrConfiguration` |

### Dependents

None. Leaf node.

## Interface

### App Lifecycle

- **Start:** Boot `UrHost`. Initialize terminal (raw mode, alternate buffer). Start render loop.
- **First-run setup:** If readiness has blockers, show modals (API key, then model picker) before entering chat.
- **Chat:** Frame loop processes key events, routes to components, drains agent events, updates state.
- **Exit:** `/quit` command, Ctrl+C at idle prompt, or Esc during first-run setup. Restore terminal state.

## Data Structures

### ChatState

- **Purpose:** All mutable application state. Updated during the "process events" phase of each frame.
- **Shape:**
  - `Messages: List<DisplayMessage>` — the conversation
  - `ActiveModal: IComponent?` — the currently open modal, or null
  - `ScrollOffset: int` — how far the user has scrolled back in messages
  - `IsTurnRunning: bool` — whether an agent turn is in flight
- **Invariants:**
  - State is only mutated during the event-processing phase of the frame loop (between key drain and render). No concurrent access.
  - `ActiveModal` non-null means key events route to the modal, not to the chat input.

### DisplayMessage

- **Purpose:** UI representation of a message. Separate from the library's `ChatMessage`.
- **Shape:**
  - `Role: MessageRole` — User, Assistant, Tool, System
  - `Content: StringBuilder` — mutable; streaming assistant messages append chunks here
  - `IsStreaming: bool` — true while chunks are still arriving
  - `ToolName: string?` — for tool-related messages
  - `IsError: bool` — for errors
  - `Timestamp: DateTimeOffset`
- **Why not ChatMessage:** The UI needs mutable streaming state and role-specific rendering hints. `ChatMessage` is a persistence/transport type.

## Internal Design

### Layer Architecture

Two layers in the compositor:

- **Layer 0 (base):** Message list + chat input. Both components render to this layer's buffer, each in their own rect. The message list fills available space; the chat input occupies the bottom rows. As the input grows (more lines), its rect grows and the message list's rect shrinks.
- **Layer 1 (overlay):** Modal, when active. Content in the centered modal rect, shadow mask stamped around/below it. When no modal is active, this layer is fully transparent (composited away to nothing).

### Layout

Layout is handled by the [terminal layout system](terminal-layout.md) rather than manual arithmetic.

**Base layer (message list + input):**
```
VerticalStack
├── MessageList                  [SizeConstraint.Fill]
└── ChatInput (Border = true)    [SizeConstraint.Content → MeasureHeight includes border]
```

The VerticalStack calls `ChatInput.MeasureHeight(width)`, which internally calls `MeasureContentHeight` (wrapped line count) and adds border + padding overhead. MessageList gets the remaining space. Both are Widgets — ChatInput has `Border = true`, MessageList has no chrome.

**Overlay layer (modals):**
```
Center(modalWidth, modalHeight)
└── ApiKeyModal (Border = true, Background = color, Padding = ...)
```

Center positions the modal in the screen center. The modal's base `Widget.Render` draws border/background, then calls `RenderContent` with the inner rect. Modal widgets no longer compute their own centering, border drawing, or background fills — their `RenderContent` just renders content.

### Components

All extend `Widget` from Ur.Terminal.

**MessageList** — The most complex component. Renders messages bottom-up: latest message at the bottom of the rect, previous messages stacked upward. Each message type (user, assistant, tool call, system) has distinct rendering (prefix, color, formatting). Message height is dynamic (word wrapping). If messages overflow the top of the rect, the topmost message is clipped. Scroll offset shifts the viewport.

**ChatInput** — Multi-line text editor. Handles character input, backspace, delete, arrow key navigation within the text. Enter submits (sends the message or dispatches a slash command). The framework can now detect `Shift+Enter` via `KeyEvent.Mods`, but the chat input still intentionally treats Enter as single-line submit until multiline behavior lands in a separate change.

**ApiKeyModal** — Masked text input in a bordered box. Enter submits, Esc dismisses (on first run: exits the app). Characters display as `*`.

**ModelPickerModal** — Searchable list in a bordered box. Type to filter (by model name or ID), Enter to select, Esc to dismiss. Shows model metadata (context length, cost) for the selected item. Must handle 345+ models efficiently — filtering and rendering only the visible window. Delegates list rendering and selection/scroll management to `ScrollableList<T>` from [Ur.Terminal](terminal-layout.md); provides an item render callback for model-specific formatting.

**ExtensionManagerModal** — Searchable list of discovered extensions. Shows tier, enabled/disabled/error state, description, and version. Enter toggles the selected extension; resetting to default is available as a secondary action. Enabling a workspace extension requires an explicit confirmation step because it is a trust decision, not a cosmetic preference. In v1, toggles are only allowed while no turn is running. Delegates list rendering and selection/scroll management to `ScrollableList<T>` from [Ur.Terminal](terminal-layout.md).

### Key Routing

Each frame, the app drains pending key events and routes them:

1. If `ActiveModal` is not null → `modal.HandleKey(key)`. If the modal returns false (didn't consume), the app handles it (e.g., Esc to dismiss).
2. If no modal → `chatInput.HandleKey(key)`. On Enter:
   - If input starts with `/` → parse as slash command, dispatch.
   - Otherwise → submit as user message, start a turn.
3. Scroll keys (PageUp, PageDown) → adjust `ScrollOffset` on the message list regardless of focus.

### Slash Commands

Input starting with `/` is parsed as a command:

| Command | Action |
|---|---|
| `/model` | Open `ModelPickerModal` |
| `/extensions` | Open `ExtensionManagerModal` |
| `/quit` | Exit the app |

Extensible: the command table is a `Dictionary<string, Action<string>>` (command name → handler with argument string). Adding commands doesn't require structural changes.

### Turn Execution

When the user submits a message:

1. Add a `DisplayMessage(Role: User)` to `ChatState.Messages`.
2. Start `session.RunTurnAsync(input, ct)` on a background task.
3. Add a `DisplayMessage(Role: Assistant, IsStreaming: true)` to messages.
4. The background task drains `AgentLoopEvent`s into a concurrent queue.
5. Each frame, the app drains the agent event queue:
   - `ResponseChunk` → append text to the streaming assistant message
   - `ToolCallStarted` → add a `DisplayMessage(Role: Tool)` with tool name and retain the tool-call identity
   - `ToolCallCompleted` → update the matching tool message by `CallId`, not only by tool name
   - `TurnCompleted` → mark streaming message as `IsStreaming = false`, set `IsTurnRunning = false`
   - `Error` → add a system error message. If fatal, stop the turn.

The current implementation does this inside `ChatApp`; the target refactor splits the frame shell, turn runner, and event projector as described in [tui-runtime.md](tui-runtime.md).

### Cancellation

- Ctrl+C during a turn → cancel the `CancellationTokenSource` for the current turn. The turn runner stops, the streaming message is finalized as-is, and the app returns to input.
- Ctrl+C at idle → exit the app.
- Terminal restoration (raw mode exit, alternate buffer exit, cursor show) is guaranteed via `IDisposable` + crash handlers.

## Design Decisions

### Frame-based event processing, not channel-based

- **Context:** Previous design used `Channel<AppEvent>` with the main loop awaiting events. The framework now provides a frame-based render loop.
- **Choice:** Drain concurrent queues (keys + agent events) each frame instead of awaiting a unified channel.
- **Rationale:** The frame-based model naturally batches rapid LLM streaming events. Simpler than managing a channel — just ConcurrentQueue + drain. Matches the framework's render loop design.

### First-run Esc exits the app

- **Context:** On first run, the API key modal is shown immediately. Esc should not trap the user.
- **Choice:** Esc exits cleanly.
- **Rationale:** Hostile to trap a user in a modal. They can come back.

### Management flows stay modal-based in v1

- **Context:** The TUI already has modal affordances for API keys and model selection. Extension management is another structured interactive flow.
- **Choice:** Add `/extensions` as the entry point to an `ExtensionManagerModal`, rather than introducing a separate screen or a family of slash subcommands first.
- **Rationale:** This keeps the interaction model consistent and lets users browse, search, and inspect state before toggling anything.
- **Consequences:** The slash command table stays small. If scripting-oriented extension commands become important later, they can be added on top of the same library API.

## Open Questions

- How much widget-local state should remain inside modal components versus move into explicit runtime state?
- Should slash commands stay dictionary-dispatched, or should the TUI parse them into typed intents before applying workflow logic?
