# TUI Runtime

> Part of: [TUI Chat Client](cli-tui.md)

## Purpose and Scope

The frame-oriented application runtime for `Ur.Tui`. It owns interaction workflow: key routing, first-run/readiness flow, slash-command dispatch, turn lifecycle orchestration, and projection of backend `AgentLoopEvent`s into renderable UI state. This is the missing middle layer between the generic terminal framework and the leaf UI widgets.

### Non-Goals

- Does not own Ur domain rules. Model selection, API key persistence, extension activation, and turn execution still belong to the backend/library.
- Does not treat UI widgets as the source of truth for application workflow. Widget state may exist, but runtime decisions must be driven by explicit app state.
- Does not support concurrent turns in v1.
- Does not introduce a general-purpose navigation stack or screen framework.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|------------|------------------|-----------|
| Ur.Terminal | Frame loop integration, compositing, layers, input primitives | `Compositor`, `Layer`, `Rect`, `KeyEvent` |
| TUI components | Leaf rendering and widget-local editing behavior | `MessageList`, `ChatInput`, modal components |
| TUI backend seam | Readiness, session creation, extension mutations, turn execution | `IChatBackend`, `IChatSession` |

### Dependents

| Dependent | What it needs | Interface |
|-----------|---------------|-----------|
| `Ur.Tui/Program.cs` | One frame callback for the render loop | `ProcessFrame(keys)` |
| Leaf components | Stable render state and explicit modal/turn inputs | runtime-owned state projections |

## Interface

### Process Frame

- **Purpose:** Advance the TUI by one frame.
- **Inputs:** Terminal dimensions from the compositor, pending `KeyEvent`s, queued `AgentLoopEvent`s.
- **Outputs:** Updated runtime state and rendered layers; `false` when the app should exit.
- **Errors:** Backend failures are translated into user-visible system/error messages instead of tearing down the process.
- **Preconditions:** Runtime has been initialized with a backend and layers.
- **Postconditions / Invariants:** All app-state mutation for the frame is complete before rendering starts.

### Start Turn

- **Purpose:** Translate a submitted user message into a running turn.
- **Inputs:** Trimmed user text and the current `IChatSession`.
- **Outputs:** User message appended, streaming assistant placeholder created, turn runner started.
- **Errors:** If no session exists or a turn is already running, the request is ignored.
- **Preconditions:** Readiness flow is complete and no turn is active.
- **Postconditions / Invariants:** Exactly one active turn exists in v1.

### Handle Modal Outcome

- **Purpose:** Apply app-level consequences of modal interaction.
- **Inputs:** Current modal state plus any submitted selection/value.
- **Outputs:** Backend calls, follow-up modal transitions, or exit request.
- **Errors:** Mutation failures surface as UI feedback and system messages.
- **Preconditions:** A modal is active.
- **Postconditions / Invariants:** Modal transitions are explicit; dismissal rules differ between first run and normal chat.

## Quality Attributes

| Attribute | Requirement | Implication for design |
|-----------|-------------|------------------------|
| Maintainability | `ChatApp`-level orchestration must stay understandable to one developer | Separate frame shell, turn runner, command routing, and event projection responsibilities |
| Correctness | Event-driven UI state must survive repeated tool calls and modal transitions | Use stable identifiers in app state rather than searching by display text or tool name |
| Responsiveness | Streaming responses should feel live without excessive redraw logic | Background turn runner feeds a queue; the frame loop drains and projects events |
| Simplicity | The runtime should remain a thin application layer, not a second framework | No widget tree, screen stack, or generalized dispatcher unless a concrete need appears |

## Data Structures

Current code collapses most of this into `ChatApp` plus [`ChatState`](/Users/kyle/src/ur/Ur.Tui/State/ChatState.cs). That shape is no longer strong enough. The target split below is architectural guidance for the refactor.

### Runtime State

- **Purpose:** The authoritative application state for the TUI runtime.
- **Shape:**
  - `Transcript` - ordered display messages
  - `Viewport` - scroll position and layout-relevant view state
  - `Modal` - explicit modal/workflow state
  - `Turn` - running-turn metadata or null
  - `ExitRequested` - whether the frame loop should stop
- **Invariants:**
  - Runtime state is expressed in app concepts, not generic widget types.
  - Rendering reads runtime state but does not define it.
- **Why this shape:** The current `IComponent? ActiveModal` field couples workflow to concrete widgets and forces `ChatApp` to inspect widget internals.

### Modal State

- **Purpose:** Represent the current interaction workflow without using `IComponent` as the data model.
- **Shape:** A discriminated set of runtime concepts such as `ApiKeyPrompt`, `ModelSelection`, and `ExtensionManagement`.
- **Invariants:**
  - Modal kind is explicit.
  - Data needed to apply outcomes lives in runtime state, not only inside widget instances.
- **Why this shape:** The runtime needs to reason about first-run exit rules, follow-up transitions, and mutation blocking without type-switching on view classes.

### Turn State

- **Purpose:** Track the currently running turn and its projection into transcript state.
- **Shape:**
  - `Session`
  - `Cancellation`
  - `StreamingAssistantMessageId`
  - `ToolMessagesByCallId`
  - event queue / runner handle
- **Invariants:**
  - At most one turn is active in v1.
  - Tool messages are correlated by `CallId`, not `ToolName`.
- **Why this shape:** `AgentLoopEvent` already exposes `CallId`; the runtime should preserve that identity all the way to UI projection.

### Display Message Identity

- **Purpose:** Give transcript entries stable identity beyond role/text.
- **Shape:** Each display message should have a message id; tool messages should also retain the originating `CallId`.
- **Invariants:**
  - Streaming updates target one known assistant message.
  - Tool completion updates exactly one known tool message.
- **Why this shape:** Searching backward for "the last tool message with this tool name" is brittle and fails when the same tool is called multiple times in one turn.

## Internal Design

The runtime should be factored into four small pieces behind the existing `ProcessFrame` entry point.

### 1. Frame Shell

Owns the high-level frame sequence:

1. Resize layers if needed.
2. Drain pending turn events.
3. Route keys into typed intents.
4. Apply intents to runtime state and backend-facing operations.
5. Render from the resulting state.

This keeps frame timing logic simple and makes the rest of the system testable without terminal rendering.

### 2. Input And Command Routing

Keyboard handling should produce typed intents such as:

- `ExitRequested`
- `CancelTurn`
- `ScrollTranscript`
- `SubmitInput`
- `OpenModelPicker`
- `OpenExtensionManagement`
- `ModalInput`

Slash commands remain a useful surface, but the runtime should not treat `Dictionary<string, Action<string>>` as its primary application model. Parsing is one concern; deciding what the app does is another.

### 3. Turn Runner And Event Projection

The background task that consumes `IChatSession.RunTurnAsync(...)` should be isolated from transcript mutation. The runner owns cancellation and queuing; an event projector consumes `AgentLoopEvent`s and updates runtime state.

This separation matters because the projector has real data-model rules:

- create the streaming assistant placeholder once
- create tool messages keyed by `CallId`
- finalize the active assistant message on `TurnCompleted`
- surface backend errors without losing transcript consistency

### 4. Rendering And Layout

Layout arithmetic and modal shadow placement should be pure rendering concerns. Given runtime state and terminal size, rendering chooses rects and paints layers. It should not also decide workflow transitions or backend operations.

## Error Handling And Failure Modes

| Failure Mode | Detection | Recovery | Impact on Dependents |
|--------------|-----------|----------|----------------------|
| API key/model mutation fails | backend call throws or returns failure state | keep modal open when possible, show inline feedback and system message | user can retry without app restart |
| Turn fails mid-stream | `Error` event or exception from turn runner | mark turn idle, preserve transcript so far, append error message | transcript remains readable |
| Tool call completion cannot be correlated | missing `CallId` mapping | append a system error and finalize turn conservatively | avoids silent transcript corruption |
| Modal is blocked during active turn | runtime sees active turn state | keep modal read-only or reject mutation intent | prevents unsafe mid-turn mutations |

## Design Decisions

### Runtime State Uses App Concepts, Not `IComponent`

- **Context:** The current `ChatState.ActiveModal` is typed as `IComponent?`, while runtime code switches on `ApiKeyModal`, `ModelPickerModal`, and `ExtensionManagerModal`.
- **Options considered:**
  - Keep widget instances as the application state model.
  - Introduce explicit runtime state for modal and turn workflow, with widgets rendering that state.
- **Choice:** Explicit runtime state.
- **Rationale:** Application workflow should not depend on concrete widget implementations. This reduces coupling and makes state transitions testable without invoking rendering code.
- **Consequences:** Some widget-local state will need an adapter or view-model layer during refactor.

### Tool Events Are Correlated By `CallId`

- **Context:** `ToolCallStarted` and `ToolCallCompleted` already expose `CallId`.
- **Options considered:**
  - Match started/completed events by tool name.
  - Match by `CallId` and carry that identity into transcript state.
- **Choice:** Match by `CallId`.
- **Rationale:** Tool names are not unique within a turn. Identity already exists in the event contract; the runtime should not throw it away.
- **Consequences:** `DisplayMessage` or equivalent transcript state needs a stable tool-call identifier.

### `ProcessFrame` Stays As The Public Entry Point

- **Context:** The render loop already depends on a single frame callback.
- **Options considered:**
  - Keep one `ProcessFrame` facade and refactor behind it.
  - Replace the frame callback with a larger app object graph or event bus.
- **Choice:** Keep `ProcessFrame`.
- **Rationale:** The problem is overloaded internals, not the existence of a single integration point.
- **Consequences:** Refactoring can be incremental and should not require render-loop changes.

## Open Questions

- **Question:** Should modal widgets become pure renderers, or should each keep a small view-model object behind an app-owned `ModalState`?
  **Context:** `ApiKeyModal`, `ModelPickerModal`, and `ExtensionManagerModal` currently own meaningful workflow state.
  **Current thinking:** A hybrid is likely best. Keep widget-local editing/search state near the widget, but move workflow transitions and submitted values into runtime-owned state.

- **Question:** Should slash commands remain string-dispatched for v1, or graduate to a typed command parser now?
  **Context:** The current table is simple, but more commands will otherwise push more application logic into ad hoc lambdas.
  **Current thinking:** Parse into typed intents now, even if the syntax stays slash-command based.
