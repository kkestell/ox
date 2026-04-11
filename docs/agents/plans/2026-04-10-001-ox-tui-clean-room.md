# Clean-Room Ox TUI Implementation

## Goal

Build a clean-room terminal user interface for conversing with LLMs, implementing `docs/functional-requirements.md` on top of Te (terminal rendering/input) and Ur (agent framework). The implementation is staged — each stage delivers a testable increment that the user verifies manually before proceeding.

## Desired outcome

A fully functional TUI matching the functional requirements: configuration phase, splash screen, conversation display with streaming, tool call rendering, permission prompts, scrolling, status line, slash commands, and screenshot capture. Clean, well-commented code with clear module boundaries.

## Architecture

### Project structure

```
src/Ox/
├── Ox.csproj                       # net10.0, references Te + Ur
├── Program.cs                      # Entry point: CLI args, config phase, bootstrap
├── OxApp.cs                        # Main loop, owns all components, dispatches events
├── Theme.cs                        # Color constants from §11 of the spec
├── Screenshot.cs                   # Ctrl+G / F12 buffer dump to .ox/screen-dumps/
├── Conversation/
│   ├── ConversationEntry.cs        # Discriminated union of entry types
│   ├── ConversationView.cs         # Scrollable entry list renderer
│   └── TextWrapper.cs              # Word-wrap with configurable continuation indent
├── Input/
│   ├── TextEditor.cs               # Single-line editable text buffer with cursor
│   ├── InputAreaView.cs            # 5-row bordered input region
│   ├── Throbber.cs                 # 8-bit binary counter animation
│   └── Autocomplete.cs             # Slash command / skill tab completion
└── Permission/
    └── PermissionPromptView.cs     # Floating 3-row approval panel
```

### Main loop design

OxApp runs a single-threaded render loop that wakes on three signals:

```
while (!exit)
{
    CheckResize()
    DrainPendingEvents()      // Ur agent events from ConcurrentQueue
    DrainInput()              // Te input events from InputCoordinator.Reader
    Render()                  // Clear buffer → draw views → buffer.Render(Console.Out)

    await Task.WhenAny(
        inputCoordinator.Reader.WaitToReadAsync(),   // stdin has data
        wakeSignal.WaitAsync(),                      // Ur event arrived
        tickDelay                                    // 1s tick (only when turn active)
    )
}
```

- **Input events** fire eagerly on Te's reader thread via InputCoordinator callbacks (for Escape detection), but are also queued in the channel for serial main-loop processing.
- **Ur events** arrive on a background task running `session.RunTurnAsync()`. They enqueue into a `ConcurrentQueue<AgentLoopEvent>` and release a `SemaphoreSlim` to wake the main loop. All state mutation happens on the main thread when the queue is drained.
- **Throbber tick** — when a turn is active, `Task.WhenAny` includes a 1-second delay so the throbber counter advances even without input or Ur events.

### Threading model

```
┌─────────────────────┐     ┌──────────────────────┐
│  Te stdin reader     │     │  Ur turn task         │
│  (background thread) │     │  (background task)    │
│                      │     │                       │
│  Parses ANSI bytes   │     │  RunTurnAsync() loop  │
│  → KeyPressed event  │     │  → enqueue events     │
│  → MouseEvent event  │     │  → release semaphore  │
│  → Channel.Write()   │     │                       │
└──────────┬───────────┘     └──────────┬────────────┘
           │                            │
           ▼                            ▼
┌──────────────────────────────────────────────────────┐
│  Main loop (single thread)                           │
│                                                      │
│  Drains both queues → mutates state → renders buffer │
└──────────────────────────────────────────────────────┘
```

State is only mutated on the main thread. Background threads only enqueue. This avoids locks entirely.

**Permission prompt bridge:** `TurnCallbacks.RequestPermissionAsync` runs on the Ur turn task. It creates a `TaskCompletionSource<PermissionResponse>`, enqueues a "show prompt" marker, and awaits the TCS. The main loop shows the prompt and completes the TCS when the user responds — unblocking the Ur task.

### Key abstractions

**ConversationEntry** — a discriminated union (C# class hierarchy) representing every visual element in the conversation. Each variant carries the data needed to render it:

| Variant | Data |
|---------|------|
| `UserMessage` | text |
| `AssistantText` | text (mutable — grows during streaming) |
| `ToolCall` | callId, toolName, formattedSignature, status, result |
| `PlanUpdate` | list of (content, status) items |
| `SubagentContainer` | callId, child entries list |
| `Error` | message |
| `Cancellation` | (no data) |

**ConversationView** — owns the list of entries and scroll state. Renders entries top-to-bottom into a buffer region, applying word-wrap, circle prefixes, indentation, and spacing. Manages a vertical scrollbar and auto-scroll behavior.

**InputAreaView** — owns the TextEditor and renders the 5-row input region. Draws borders (rounded box-drawing characters), the text field contents, the horizontal divider, and the status line (throbber left, model/context right).

**PermissionPromptView** — renders the 3-row floating panel. Owns a secondary TextEditor for the permission input field. Computes its own vertical position (overlapping the bottom of the conversation area).

## Related code

- `src/Te/Rendering/ConsoleBuffer.cs` — double-buffered cell grid; the canvas for all rendering
- `src/Te/Rendering/Color.cs` — color constants (Default, Basic, Bright, Color256)
- `src/Te/Rendering/Cell.cs` — single terminal cell (rune + fg + bg + decorations)
- `src/Te/Input/InputCoordinator.cs` — event queue with Channel-based reader
- `src/Te/Input/TerminalInputSource.cs` — stdin reader (Unix raw mode + ANSI parsing)
- `src/Te/Input/KeyCode.cs` — portable key encoding with modifier masks (CtrlMask, ShiftMask, AltMask)
- `src/Te/Input/IInputSource.cs` — event source interface (KeyPressed, MouseEvent)
- `src/Ur/Hosting/UrHost.cs` — top-level API: CreateSession(), Configuration
- `src/Ur/Sessions/UrSession.cs` — RunTurnAsync() yields AgentLoopEvent stream
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — event hierarchy (ResponseChunk, ToolCallStarted, etc.)
- `src/Ur/Permissions/TurnCallbacks.cs` — RequestPermissionAsync callback
- `src/Ur/Configuration/UrConfiguration.cs` — model selection, API keys, readiness checks
- `src/Ur/Hosting/UrStartupOptions.cs` — DI bootstrap options (FakeProvider, WorkspacePath)
- `src/Te.Demo/Program.cs` — reference implementation showing Te's main loop pattern
- `docs/functional-requirements.md` — the spec; this plan implements it
- `docs/boo.md` — testing guide for the Boo headless terminal driver

## Current state

- Te provides low-level input and rendering but no UI framework — we build the application layer directly.
- Ur provides a complete agent backend with a clean consumer API (`RunTurnAsync` yields events).
- The Ox solution already includes an `src/Ox/Ox.csproj` project — we replace its contents entirely.
- Boo and `--fake-provider` scenarios exist for deterministic testing.

## Structural considerations

**Hierarchy:** Ox depends on Te (rendering) and Ur (agent). Te has no external dependencies. Ur has LLM provider dependencies. Dependencies flow downward: Ox → Te, Ox → Ur. Neither Te nor Ur knows about Ox.

**Abstraction:** Views handle rendering and local state. OxApp orchestrates views and manages the event loop. No view reaches into another view's internals. The Ur integration is contained to OxApp (and the permission bridge).

**Modularization:** Each file has a single clear purpose. Conversation/, Input/, and Permission/ group related code by visual region. Theme.cs centralizes colors. Screenshot.cs is standalone utility.

**Encapsulation:** ConsoleBuffer is passed to views for rendering — views don't own it. TextEditor exposes its text content but handles editing internally. ConversationEntry variants are immutable except for AssistantText.Text and ToolCall status/result (which only the main loop mutates).

---

## Implementation stages

Each stage is independently testable. Stop at the end of each stage for manual verification before proceeding.

### Stage 1: Application Shell + Layout + Screenshot

This stage delivers the empty application: alternate screen, splash logo, input area chrome, exit handling, and the screenshot feature.

**Tasks:**

- [ ] Replace `src/Ox/Ox.csproj` — target net10.0, reference Te and Ur projects, enable nullable + implicit usings + code style enforcement
- [ ] Create `Theme.cs` — static readonly Color fields for every element in §11 (Background=Black, Text=White, UserCircle=Blue, ToolSignature=BrightBlack, ToolCircleStarted=Yellow, ToolCircleSuccess=Green, ToolCircleError=Red, SplashLogo=BrightBlack, Border=White or light gray via Color256, Divider=darker gray via Color256, StatusText=gray via Color256, ThrobberActive=White, ThrobberInactive=BrightBlack)
- [ ] Create `InputAreaView.cs` — constructor takes width; `Render(ConsoleBuffer, int x, int y, int width)` draws 5 rows: top border (`╭` + `─` repeated + `╮`), empty text row, horizontal divider (`├` + `─` repeated + `┤`), empty status row, bottom border (`╰` + `─` repeated + `╯`). Border in Theme.Border, divider in Theme.Divider.
- [ ] Create `ConversationView.cs` — initially just renders the splash logo centered in the given region. Logo text: `▒█▀▀▀█ ▀▄▒▄▀` / `▒█░░▒█ ░▒█░░` / `▒█▄▄▄█ ▄▀▒▀▄` in Theme.SplashLogo. Center both horizontally and vertically.
- [ ] Create `OxApp.cs` — constructor takes terminal width/height. Creates ConsoleBuffer, TerminalInputSource (mouse enabled), InputCoordinator. Main loop: check terminal resize → clear buffer → render ConversationView in top region (rows 0 to height-6) → render InputAreaView in bottom region (rows height-5 to height-1) → `buffer.Render(Console.Out)` → wait for input. Handle Ctrl+C → set exit flag.
- [ ] Create `Program.cs` — write `\u001b[?1049h` (alternate screen) + `\u001b[?25l` (hide cursor), create and run OxApp, finally write `\u001b[?25h` (show cursor) + `\u001b[?1049l` (restore screen).
- [ ] Create `Screenshot.cs` — `Capture(ConsoleBuffer)`: read every cell from the buffer via `GetRenderedCell(x, y)`, write plain text (rune characters only, no ANSI) to `.ox/screen-dumps/screen-YYYYMMDD-HHmmss-fff.txt`. Also symlink or copy to `.ox/screen-dumps/latest.txt`.
- [ ] Wire Ctrl+G and F12 in OxApp input handling to trigger `Screenshot.Capture()`.

**Test procedure:**

1. `dotnet run --project src/Ox`
2. Verify: terminal switches to alternate screen, splash logo centered in dark gray, input area at bottom with rounded borders and dividers
3. Resize the terminal window — verify layout adapts (conversation area grows/shrinks, input area stays at bottom)
4. Press F12 — check that `.ox/screen-dumps/` contains a new timestamped file. Open it and verify contents match the visible screen.
5. Press Ctrl+C — terminal restores cleanly, app exits
6. Report findings with screenshots

---

### Stage 2: Text Input & User Messages

This stage adds text editing, message submission, and user message display. No LLM integration yet — messages are displayed but produce no response.

**Tasks:**

- [ ] Create `TextEditor.cs` — manages a `string` buffer and `int` cursor position. Methods: `InsertChar(char)`, `Backspace()`, `Delete()`, `MoveLeft()`, `MoveRight()`, `Home()`, `End()`, `Clear()`. Properties: `Text`, `CursorPosition`. Does not handle rendering — just the editing model.
- [ ] Wire keyboard input in OxApp: printable chars → `InsertChar`, Backspace key → `Backspace()`, Delete key → `Delete()`, Left/Right arrows → move cursor, Home/End → jump.
- [ ] Update InputAreaView to accept TextEditor state and render the text in row 1 (the text field row). Show a visible cursor (reverse video cell at cursor position).
- [ ] Create `ConversationEntry.cs` — abstract base with a `Kind` enum. Start with `UserMessageEntry(string Text)`. Each entry also carries a computed `RenderedLines` cache (populated by TextWrapper during rendering).
- [ ] Create `TextWrapper.cs` — static method `Wrap(string text, int width, int indent) → List<string>`. Wraps at word boundaries (spaces). If a word exceeds available width, hard-breaks at column limit. Continuation lines start at `indent` columns.
- [ ] Update ConversationView: maintain a `List<ConversationEntry>`. When entries exist, suppress the splash logo. Render entries top-to-bottom: user messages get `● ` prefix (blue circle + space), text in white, word-wrapped with continuation indent of 2 (past the circle). Blank line between entries. 1-column horizontal padding on each side.
- [ ] Implement message submission in OxApp: on Enter, if TextEditor is non-empty, read text, call `TextEditor.Clear()`, create `UserMessageEntry`, add to ConversationView.
- [ ] Implement `/quit` command: if submitted text starts with `/`, parse as command. `/quit` sets exit flag. Unknown commands add an error-like message "Unknown command: {name}".
- [ ] Implement Ctrl+D: if TextEditor.Text is empty, set exit flag. If non-empty, ignore.

**Test procedure:**

1. Launch app, type "Hello!" — characters appear in input field with visible cursor
2. Press Backspace — deletes last char. Use arrow keys — cursor moves.
3. Press Enter — input clears, `● Hello!` appears in conversation with blue circle
4. Splash logo is gone
5. Type a very long message (e.g. "The quick brown fox jumps over the lazy dog and keeps going for a very long time to test word wrapping behavior at the edge of the terminal") — verify word wrapping, continuation lines aligned past the circle
6. Submit several messages — verify blank line spacing between them
7. Type `/quit` and Enter — app exits
8. Relaunch, press Ctrl+D with empty input — app exits
9. Relaunch, type "abc", press Ctrl+D — does NOT exit (buffer non-empty)
10. Take screenshots at interesting states

---

### Stage 3: Ur Integration — Configuration & Streaming Chat

This stage connects Ur. The app now prompts for model/key configuration, runs turns against an LLM (or fake provider), streams responses, and displays the animated status line.

**Tasks:**

- [ ] Update `Program.cs` — before entering the TUI, run the configuration phase as plain console I/O:
  - Create DI container with `services.AddUr(options)` where options include `WorkspacePath` = cwd and `FakeProvider` from `--fake-provider` CLI arg
  - Resolve `UrHost`, check `Configuration.Readiness`
  - If no model selected: prompt "Enter model (provider/model):", read line, call `SetSelectedModelAsync`. Blank line → exit.
  - If unknown provider: re-prompt with guidance.
  - If missing API key: prompt "Enter API key for {provider}:", read line, call `SetApiKeyAsync`. Blank line → exit.
  - When `Readiness.CanRunTurns` is true, proceed to TUI.
- [ ] Pass `UrHost` into `OxApp` constructor.
- [ ] In OxApp initialization, create a session: `_session = _host.CreateSession(callbacks: turnCallbacks)` where `turnCallbacks` has `RequestPermissionAsync` (stubbed for now — auto-deny) and `SubagentEventEmitted`.
- [ ] Implement turn execution: when user submits a non-command message, start a background `Task` that calls `session.RunTurnAsync(input, cts.Token)`, iterates the result, enqueues each `AgentLoopEvent` into `ConcurrentQueue<AgentLoopEvent>`, and releases a `SemaphoreSlim` after each event.
- [ ] Update the main loop to `Task.WhenAny(inputReady, wakeSignal, tickDelay)` — drain pending events before rendering.
- [ ] Handle `ResponseChunk` — if no current AssistantTextEntry exists, create one. Append chunk text to it. (AssistantText.Text is a mutable StringBuilder; only the main thread mutates it.)
- [ ] Handle `TurnCompleted` — mark turn as inactive, update `LastInputTokens` for context % display.
- [ ] Handle `TurnError` — if fatal, add error entry (red circle, red text `[error] {message}`). If non-fatal, add error entry but turn continues.
- [ ] Add `AssistantTextEntry` to ConversationEntry hierarchy — white circle prefix, white text, same wrapping as user messages.
- [ ] Add `ErrorEntry(string Message)` — red circle, red text.
- [ ] Create `Throbber.cs` — maintains an `int` counter (starts at 1 when turn begins). `Tick()` increments it. `Render(ConsoleBuffer, int x, int y)` draws 8 circles separated by spaces: for each bit i (7 down to 0), if bit is set → Theme.ThrobberActive, else → Theme.ThrobberInactive. `Reset()` clears counter.
- [ ] Update InputAreaView to render the status line: Throbber on the left (visible only when turn active), model ID + context % on the right (e.g. `1%  google/gemini-3-flash-preview`). Context % hidden until first turn completes. Model ID always shown.
- [ ] Implement input queuing: if user submits while turn is active, store the text. When turn completes, if queued text exists, start a new turn automatically.
- [ ] Include a 1-second `Task.Delay` in `Task.WhenAny` when turn is active, so the throbber ticks even without other events. Call `Throbber.Tick()` each iteration when active.

**Test procedure:**

1. `dotnet run --project src/Ox -- --fake-provider hello`
2. TUI starts immediately (fake provider needs no configuration prompts)
3. Type "Hello!" and Enter
4. Verify: user message appears → throbber starts animating (binary counter in circles) → assistant response streams in with white circle → throbber stops → context % appears on status line
5. Check that model ID is visible on status line throughout
6. Take screenshots at: throbber running, response streaming, turn complete
7. `dotnet run --project src/Ox -- --fake-provider long-response` — verify longer streaming text renders correctly
8. `dotnet run --project src/Ox -- --fake-provider multi-turn` — submit 3 messages, verify all responses appear
9. `dotnet run --project src/Ox -- --fake-provider error` — verify error entry appears in red

---

### Stage 4: Tool Calls & Results

This stage adds tool call visualization with lifecycle colors and result display.

**Tasks:**

- [ ] Add `ToolCallEntry` to ConversationEntry — fields: `CallId`, `ToolName`, `FormattedSignature` (from `ToolCallStarted.FormatCall()`), `Status` enum (Started, AwaitingApproval, Succeeded, Failed), `Result` string, `IsError` bool.
- [ ] Handle `ToolCallStarted` event — create ToolCallEntry with Status=Started, formatted signature from the event.
- [ ] Handle `ToolAwaitingApproval` event — find entry by CallId, set Status=AwaitingApproval. (No prompt yet — Stage 5.)
- [ ] Handle `ToolCallCompleted` event — find entry by CallId, set Status=Succeeded or Failed based on IsError, store Result.
- [ ] Render tool call entries in ConversationView:
  - Circle color: yellow (Started/AwaitingApproval), green (Succeeded), red (Failed)
  - Signature in dark gray: `● Write("bar.txt", "foo")`
  - Arguments truncated to 40 chars with `...`, newlines collapsed to spaces (this is done by `FormatCall()` but verify)
- [ ] Render tool results beneath the signature, indented to align:
  ```
  ● Write("bar.txt", "foo")
    └─ Wrote 3 bytes to bar.txt
  ```
  Result text in dark gray. Word-wrap result lines with appropriate indent.
- [ ] Implement 5-line result limit: if result has more than 5 lines, show first 5 then `(N more lines)` in dark gray.
- [ ] Handle `TodoUpdated` event — create or update a PlanEntry:
  ```
  ● Plan
    ✓ Set up project structure
    ● Implement feature X
    ○ Write tests
  ```
  Status markers: `✓` completed (green), `●` in progress (yellow), `○` pending (dark gray).
- [ ] Add `PlanEntry` to ConversationEntry hierarchy.
- [ ] Suppress result display for successful todo_write tool calls (the plan block is sufficient). Show errors.

**Test procedure:**

1. `dotnet run --project src/Ox -- --fake-provider tool-call`
2. Verify: tool call entry appears with yellow circle, then transitions to green on completion
3. Verify: function-call format displayed in dark gray
4. Verify: result text appears beneath with `└─` prefix and proper indentation
5. Take screenshots at: tool started (yellow), tool completed (green), result visible
6. If the tool-call scenario produces long results, verify the 5-line truncation with "(N more lines)"

---

### Stage 5: Permission System

This stage adds the floating permission prompt and full permission handling.

**Tasks:**

- [ ] Create `PermissionPromptView.cs`:
  - 3-row floating panel: `╭─╮` top, content row, `╰─╯` bottom
  - Content: `Allow '{tool_name}' to {Operation} '{target}'? (y/n [scopes]):` followed by an inline text field
  - Positioned at `y = conversationBottom - 2` (overlaps bottom of conversation area, sits above input area)
  - Full terminal width
  - Borders in Theme.Border
- [ ] Create a secondary TextEditor instance for the permission prompt input field.
- [ ] Wire `TurnCallbacks.RequestPermissionAsync` in OxApp:
  - Store the `PermissionRequest` and create a `TaskCompletionSource<PermissionResponse>`
  - Enqueue a "show permission prompt" signal and wake the main loop
  - Await the TCS (this blocks the Ur turn task until the user responds)
- [ ] When the main loop sees the "show prompt" signal:
  - Set a flag indicating the permission prompt is active
  - Redirect keyboard input to the permission prompt's TextEditor
  - Render the PermissionPromptView overlaying the conversation area
- [ ] Handle permission prompt responses:
  - `y` or Enter → approve with first available scope from `request.AllowedScopes`
  - Typing a scope name (`once`, `session`, `workspace`, `always`) → approve with that scope
  - `n`, Escape, or Ctrl+C → deny
  - On resolve: complete the TCS, clear the prompt flag, restore input focus
- [ ] Verify the tool call's circle transitions to green/red after permission resolution.

**Test procedure:**

1. `dotnet run --project src/Ox -- --fake-provider permission-tool-call`
2. Submit a message that triggers a permission-gated tool
3. Verify: tool call appears with yellow circle, then permission prompt floats above input area
4. Verify: prompt shows tool name, operation, target, and scope options
5. Verify: main input area is blurred (not accepting input)
6. Type "y" and Enter — prompt disappears, tool executes, circle turns green, response continues
7. Take screenshot of the permission prompt state
8. Relaunch and deny: press Escape on the prompt — verify tool is denied (red circle), turn continues with agent seeing the denial
9. Take screenshot of the denial state

---

### Stage 6: Scrolling, Cancellation, Autocomplete & Polish

This stage adds scrolling, turn cancellation, sub-agent display, autocomplete, and final polish.

**Tasks:**

Scrolling:
- [ ] Add vertical scrollbar to ConversationView — drawn on the right edge column. Scrollbar thumb position reflects viewport position within total content height. Thumb in Theme.Border, track in Theme.Divider (or similar contrast).
- [ ] Implement auto-scroll: viewport stays pinned to the bottom as new content arrives (default behavior).
- [ ] Implement manual scroll: mouse wheel up/down and Page Up/Page Down move the viewport. When user scrolls up (away from bottom), auto-scroll disables.
- [ ] Re-engage auto-scroll: when viewport reaches the bottom again (either by scrolling down or by Page Down past the end), auto-scroll re-engages.
- [ ] Scrollbar only appears when content exceeds viewport height.

Cancellation:
- [ ] Implement Escape key during active turn: cancel the CancellationTokenSource passed to RunTurnAsync. The background task should catch OperationCanceledException gracefully.
- [ ] Add `CancellationEntry` to conversation — renders as plain `[cancelled]` text with no circle.
- [ ] After cancellation, the system returns to idle (throbber stops, input accepts new messages).

Sub-agents:
- [ ] Handle `SubagentEvent` — look up or create a SubagentContainerEntry by subagent ID. Add the inner event as a child entry within the container.
- [ ] Render the container entry: parent shows `● Subagent(...)` signature (yellow → green). Child entries rendered indented by 2 columns beneath the parent, following the same styling rules as top-level entries.

Autocomplete:
- [ ] Create `Autocomplete.cs` — given a prefix string matching `/<letters>`, search built-in commands (quit, clear, model, set) and registered skills for matching names.
- [ ] Return the first match's remaining characters as ghost text.
- [ ] Render ghost text in InputAreaView: after the cursor, draw the completion suffix in Theme.StatusText (gray/dim) to distinguish from typed text.
- [ ] Tab key: if ghost text is showing, append completion to TextEditor and move cursor to end.
- [ ] If no match or already exact match, Tab does nothing.

Polish:
- [ ] Trim trailing newlines from all entry text before rendering.
- [ ] Verify entry spacing: blank line between consecutive non-plain entries.
- [ ] Verify horizontal padding: 1-column gutter on each side for top-level entries.
- [ ] Handle unknown slash commands: display `Unknown command: {name}` as a local error message in the conversation (not sent to LLM).

**Test procedure:**

Scrolling:
1. `dotnet run --project src/Ox -- --fake-provider long-response` — submit a message
2. Verify: scrollbar appears on the right edge when content exceeds viewport
3. Scroll up with mouse wheel — verify content scrolls, auto-scroll is disabled, new content does NOT pull viewport to bottom
4. Scroll back to bottom — verify auto-scroll re-engages
5. Page Up / Page Down work as expected
6. Take screenshots showing scrollbar and mid-scroll state

Cancellation:
7. Start a turn with `--fake-provider long-response`
8. While response is streaming, press Escape
9. Verify: streaming stops, `[cancelled]` appears, throbber stops, input accepts new messages
10. Submit another message — verify a new turn starts normally

Autocomplete:
11. Type `/q` — verify ghost text shows `uit` in dim color
12. Press Tab — verify input becomes `/quit` with cursor at end
13. Type `/` alone — verify no ghost text (no single match)
14. Type `/xyz` — verify no ghost text (no match)

Sub-agents (if a fake scenario exists that triggers them):
15. Verify nested entries appear indented beneath the parent Subagent(...) entry

General:
16. Type `/foo` — verify "Unknown command: foo" appears
17. Final screenshot of a complete conversation with multiple entry types

---

## Validation

### Unit tests

Each stage should add tests for its core logic:

- `TextEditor` — insertion, deletion, cursor movement, boundary conditions
- `TextWrapper` — wrapping, hard breaks, indent, empty input, single-word-wider-than-width
- `Throbber` — counter values produce correct bit patterns
- `ConversationEntry` — each variant renders expected prefix and formatting
- `Autocomplete` — prefix matching, exact match, no match, empty input

### Boo smoke tests

After Stage 3, existing Boo scenarios should work (or new ones can be added):

```bash
./scripts/boo.sh --scenario=hello        # basic response
./scripts/boo.sh --scenario=tool-call     # tool rendering
./scripts/boo.sh --scenario=permission-tool-call  # permission flow
./scripts/boo.sh --scenario=long-response # scrolling
```

### Manual verification

Each stage's test procedure above covers the golden path and key edge cases. The Ctrl+G/F12 screenshot feature (from Stage 1) is the primary tool for capturing and sharing visual state.

## Open questions

- What should happen to the existing code in `src/Ox/`? I recommend deleting all existing files and starting fresh (clean room), but preserving the Ox.csproj's position in the solution. Confirm this approach.
- The functional requirements mention `/clear`, `/model`, and `/set` as "planned" built-in commands. Should we implement these in Stage 6, or leave them as stubs that print "not yet implemented"?
- Sub-agent fake provider scenario: does one exist, or should we create one for testing Stage 6 sub-agent display?
