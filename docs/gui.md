# GUI Chat Client (Ur.Gui)

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

A native desktop chat client for Ur built with AvaloniaUI using the MVVM pattern. Ur.Gui provides the same core conversation experience as Ur.Tui — start a session, run turns, see streaming responses, manage models and extensions — but on a native windowed canvas that can render Markdown, syntax-highlighted code, file diffs, and other rich content that a terminal cannot express.

The initial spike reproduces Ur.Tui's functionality using idiomatic Avalonia (standard controls, INPC bindings, `ObservableCollection`) with no custom styling. The goal is correct architecture, not visual polish.

### Non-Goals

- Does not share ViewModels with any other frontend. Ur.Gui is a standalone Avalonia application. ViewModel sharing can be revisited if a second GUI frontend (e.g. an IDE extension) materializes.
- Does not implement rich content rendering (Markdown, diffs, syntax highlighting) in the initial spike. Those are additive once the architecture is sound.
- Does not replace Ur.Tui. Both frontends coexist. Ur.Tui is optimized for headless and remote use; Ur.Gui is for interactive desktop use.
- Does not own any Ur domain logic. Session persistence, readiness evaluation, extension lifecycle, and turn execution are library concerns.
- Does not handle session resume in v1. Always creates a new session.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Ur (library) | Session creation, turn execution, readiness, model catalog, extension catalog | `UrHost`, `UrSession`, `UrConfiguration`, `ExtensionCatalog` |
| AvaloniaUI | Window, controls, XAML binding, layout, theming | Avalonia 11 desktop APIs |

### Dependents

None. Leaf node.

## Interface

### Application Lifecycle

- **Start:** `Program.Main` builds the Avalonia application. `App.OnFrameworkInitializationCompleted` creates and shows `MainWindow`, then calls `UrHost.Start(cwd)` asynchronously. The window shows a loading state while the host initializes.
- **First-run setup:** If `UrHost.Readiness` has blockers, the appropriate dialog is presented (API key entry, then model picker) before the chat UI becomes active.
- **Chat:** User types in the input, submits, the agent loop runs on a background task. Events are dispatched back to the UI thread and projected into ViewModel state.
- **Exit:** Window close. Terminal restoration is not applicable; Avalonia handles window teardown.

## Data Structures

### MainWindowViewModel

- **Purpose:** Application shell. Owns the host lifecycle and which session is currently active. Bound to `MainWindow`.
- **Shape:**
  - `IsLoading: bool` — true while `UrHost.Start` is in progress
  - `IsReady: bool` — true once the host is initialized and all readiness blockers are cleared
  - `ActiveSession: SessionViewModel?` — the current session; null while loading or during first-run setup
- **Invariants:**
  - `ActiveSession` is null while `IsLoading` is true.
  - All mutations happen on the UI thread.
- **Responsibility boundary:** `MainWindowViewModel` knows about sessions as units (create, select). It does not know about messages, input, or turn state — those belong to `SessionViewModel`.

### SessionViewModel

- **Purpose:** Owns one `UrSession` and all conversation state for it. The primary interactive ViewModel.
- **Shape:**
  - `Session: UrSession` — the underlying library session (not exposed to the view directly)
  - `Messages: ObservableCollection<MessageViewModel>` — conversation in display order
  - `InputText: string` — the current chat input (two-way bound)
  - `IsTurnRunning: bool` — true while an agent turn is in flight
- **Invariants:**
  - All mutations happen on the UI thread (via `Dispatcher.UIThread.Post` from background event consumers).
  - `Messages` is append-only; no message is removed or reordered during a session.
  - `IsTurnRunning` gates the submit action — sending while a turn is running is not permitted.
  - The `Session` reference is set at construction and never replaced. A new session means a new `SessionViewModel`.
- **Key operation — `SendMessage(string input)`:** Adds a user `MessageViewModel`, starts the agent turn on a background task, and drains `AgentLoopEvent`s back onto the UI thread. This is the entry point for all conversation activity.

### MessageViewModel

- **Purpose:** UI representation of a single message. Separate from the library's `ChatMessage`, which is a persistence/transport type.
- **Shape (base):**
  - `Timestamp: DateTimeOffset`
- **Subclasses:**
  - `UserMessageViewModel` — `Text: string` (immutable once added)
  - `AssistantMessageViewModel` — `Text: string` (mutable via `Append`); `IsStreaming: bool`
  - `ToolMessageViewModel` — `ToolName: string`; `Result: string?`; `IsError: bool`; `CallId: string` (identity for matching `ToolCallCompleted` to `ToolCallStarted`)
  - `SystemMessageViewModel` — `Text: string`; `IsError: bool`
- **Invariants:**
  - Only `AssistantMessageViewModel` with `IsStreaming = true` has `Text` mutated after construction.
  - `IsStreaming` transitions true → false exactly once; never reversed.
- **Why a class hierarchy rather than a role enum:** Avalonia's `DataTemplate` system dispatches by type. Role-specific templates bind to role-specific properties without code-behind conditionals. Adding a new message type is adding a class and a template.
- **Why StringBuilder with INPC rather than immutable replacement:** Streaming responses can produce hundreds of chunks per second. Replacing the `AssistantMessageViewModel` in the `ObservableCollection` on each chunk would cause excessive list churn. A mutable `Text` property that raises `PropertyChanged` lets Avalonia update only the affected binding.

## Internal Design

### Startup Sequence

```
Program.Main
  → AppBuilder.Configure<App>().UsePlatformDetect()
       .StartWithClassicDesktopLifetime(args)
         → App.OnFrameworkInitializationCompleted
              → new MainWindow { DataContext = new MainWindowViewModel() }
              → Desktop.MainWindow = window
              → base.OnFrameworkInitializationCompleted()   // shows window
              → await UrHost.Start(cwd)                     // async; window visible
              → vm.Host = host                              // triggers IsLoading = false
              → if !IsReady → show setup dialogs
```

The window is shown immediately with `IsLoading = true`. `UrHost.Start` is awaited after the window is presented. This matches how Avalonia applications idiomatically handle async startup — it avoids blocking the UI thread and gives visual feedback during initialization.

### ViewModel Hierarchy

```
MainWindowViewModel
└── ActiveSession: SessionViewModel?
      ├── Session: UrSession           (library object, not exposed to view)
      ├── InputText: string
      ├── IsTurnRunning: bool
      └── Messages: ObservableCollection<MessageViewModel>
            ├── UserMessageViewModel
            ├── AssistantMessageViewModel   (mutable Text, IsStreaming)
            ├── ToolMessageViewModel        (ToolName, CallId, Result)
            └── SystemMessageViewModel      (errors, status)
```

`MainWindow` binds to `ActiveSession` via `ContentControl` or a `DataTemplate` for `SessionViewModel`. While `ActiveSession` is null (loading, first-run), the window shows the appropriate loading/setup UI. Once set, the session view takes over.

### Event Consumption

`SessionViewModel.SendMessage(input)` owns the full turn lifecycle:

```
SendMessage(input):
  Messages.Add(new UserMessageViewModel(input))
  IsTurnRunning = true
  var streamingMsg = new AssistantMessageViewModel()
  Messages.Add(streamingMsg)
  _ = Task.Run(async () =>
    await foreach (var evt in Session.RunTurnAsync(input, ct))
      Dispatcher.UIThread.Post(() => Project(evt, streamingMsg)))

Project(AgentLoopEvent, streamingMsg):
  ResponseChunk      → streamingMsg.Append(chunk)
  ToolCallStarted    → Messages.Add(new ToolMessageViewModel(...))
  ToolCallCompleted  → find by CallId, update result
  TurnCompleted      → streamingMsg.IsStreaming = false; IsTurnRunning = false
  Error              → Messages.Add(new SystemMessageViewModel(error))
```

This is the same projection logic as the TUI's frame-drain loop, driven by `Dispatcher.UIThread.Post` rather than a frame clock.

### Dialogs

First-run setup dialogs (API key, model picker) and the extension manager are Avalonia `Window` dialogs shown with `ShowDialog`. They are not ViewModels-as-modals — Avalonia's dialog system is the idiomatic mechanism.

The parent ViewModel (typically `MainWindowViewModel`) constructs each dialog ViewModel with exactly the data it needs as constructor arguments — no more. The model picker receives the model list (a plain collection fetched from `UrConfiguration`); the extension manager receives the extension info collection from `ExtensionCatalog`. Neither dialog ViewModel holds a reference to `UrHost`, `UrConfiguration`, or any other broad application object.

## Error Handling and Failure Modes

| Failure Mode | Detection | Recovery | Impact |
|---|---|---|---|
| `UrHost.Start` throws | `try/catch` around `await` in `OnFrameworkInitializationCompleted` | Show error dialog, exit | Hard failure — no session is possible |
| Agent turn error (non-fatal) | `AgentLoopEvent.Error { IsFatal = false }` | Add error `SystemMessageViewModel`; UI returns to input | User can retry |
| Agent turn error (fatal) | `AgentLoopEvent.Error { IsFatal = true }` | Add error message; set `IsTurnRunning = false` | Turn aborted; session may be in inconsistent state |
| Cancellation | User closes window during turn | Cancel the turn's `CancellationTokenSource` | In-flight message finalized as-is |

## Design Decisions

### INPC + ObservableCollection, not ReactiveUI

- **Context:** Avalonia MVVM can be done with plain INPC, the CommunityToolkit.Mvvm source generator, or ReactiveUI.
- **Options considered:** Plain INPC; CommunityToolkit.Mvvm; ReactiveUI.
- **Choice:** Plain INPC (or CommunityToolkit.Mvvm source generators for boilerplate reduction). No ReactiveUI.
- **Rationale:** ReactiveUI is a significant dependency and learning surface. The streaming model is simple enough — append text, flip a bool — that Rx adds complexity without benefit. CommunityToolkit.Mvvm source generators are AoT-compatible and reduce boilerplate without new concepts.
- **Consequences:** If the UI gains complex derived state (e.g., token count computed from message content), that logic will be manual INPC. Acceptable for the scope.
- **ADR:** [ADR-0013](decisions/adr-0013-avalonia-mvvm-no-reactiveui.md)

### Message role as class hierarchy, not enum-per-ViewModel

- **Context:** The TUI's `DisplayMessage` uses a `MessageRole` enum and renders conditionally. Avalonia's `DataTemplate` system dispatches by type.
- **Choice:** Subclass `MessageViewModel` per role (`UserMessageViewModel`, `AssistantMessageViewModel`, etc.).
- **Rationale:** Avalonia `DataTemplate` with `DataType` is the idiomatic way to avoid code-behind switch statements. Each subclass carries only the fields relevant to its role. Adding a new message type is adding a class and a template, not modifying an existing switch.
- **Consequences:** Small class hierarchy. The discriminant (role) is expressed by type rather than an enum field.

### Startup: async init after window shown

- **Context:** `UrHost.Start` is async and can take up to ~200ms (model cache fetch, extension discovery). Avalonia's startup is synchronous from `Main`.
- **Choice:** Show the window immediately with a loading state, then await `UrHost.Start` in `OnFrameworkInitializationCompleted`.
- **Rationale:** Blocking `Main` on `GetAwaiter().GetResult()` violates the no-sync-over-async constraint. Avalonia's event loop must be running for `Dispatcher.UIThread.Post` to work.
- **Consequences:** The window briefly shows a loading state. Acceptable and expected.

## Open Questions

None currently.
