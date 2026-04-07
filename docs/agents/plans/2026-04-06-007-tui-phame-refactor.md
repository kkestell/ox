# Ur.Tui PHAME refactor — Critical + High findings

## Goal

Decompose Program.cs (660-line god module), fix layer violations, address
thread-safety gaps, and reduce EventList's responsibilities. Scope is the
2 Critical and 8 High findings from the PHAME review. Each task is a single
commit that leaves the codebase compiling and tests passing.

## Desired outcome

- Program.cs is ~100 lines of orchestration wiring, not 660 lines of everything.
- The Rendering layer has zero imports from `Ur.AgentLoop`.
- All cross-thread state in Viewport is protected consistently.
- EventList is a composite coordinator, not a god module.
- No duplicate input-reading implementations.
- No shared volatile bool coordinating unrelated concerns.

## How we got here

The PHAME review identified 20 findings (2 Critical, 8 High, 9 Medium, 1 Low).
The codebase grew organically through 6 prior plans (001–006) that each added
features without refactoring the structural seams. Program.cs absorbed routing,
input reading, permission UI, and signal handling because there was no other
home. EventList absorbed tree partitioning and chrome generation for the same
reason. The user chose to tackle Critical + High only, incremental commits, no PRs.

## Related code

- `src/Ur.Tui/Program.cs` — God module. Contains EventRouter (234 lines),
  two input readers, permission callback, escape-key monitor, REPL loop.
- `src/Ur.Tui/Rendering/Viewport.cs` — Mixed abstraction levels, inconsistent
  thread-safety on cross-thread fields.
- `src/Ur.Tui/Rendering/EventList.cs` — God module. Tree partitioning +
  chrome generation + child management in one 365-line class.
- `src/Ur.Tui/Rendering/ToolRenderable.cs` — Imports `Ur.AgentLoop` directly
  for `ToolCallStarted` in constructor.
- `src/Ur.Tui/Rendering/SubagentRenderable.cs` — Hardcodes tree chrome chars
  that should come from a shared source.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — Defines `ToolCallStarted.FormatCall()`,
  `SubagentEvent`, `ResponseChunk`, `ToolCallCompleted`, `TurnCompleted`, `Error`.

## Current state

- EventRouter is a `private sealed class` nested inside Program, closing over
  `eventList` via primary constructor parameter.
- ToolRenderable constructor takes `ToolCallStarted` and calls `.FormatCall()`.
  EventRouter already calls `FormatCall()` for subagent tool calls (line 504)
  but not for regular tool calls.
- `ReadLineInViewport` (lines 288–327) and `CancellableReadLine` (lines 366–411)
  implement the same polling loop over `Console.KeyAvailable`/`Console.ReadKey`
  with different echo strategies. `_pauseKeyMonitor` (volatile bool, line 31)
  coordinates the escape-key monitor with `ReadLineInViewport` to prevent
  keystroke races.
- Viewport fields `_inputPrompt`, `_sessionId`, `_modelId` are plain fields
  written from the main thread and read from the timer thread in `BuildFrame`.
  `_dirty` and `_turnRunning` are `volatile`, showing awareness of the issue,
  but these three are not.
- `BuildFrame` (lines 237–333) is a single 95-line method rendering header,
  conversation, input area, and status bar inline. The body has extra
  indentation — a fossil from a removed `lock` block.
- EventList.Render() recomputes tree partitioning from scratch every frame
  with no caching. TextRenderable caches its output (lines 27–29, 77–79).
- EventList defines tree-drawing constants (BranchChar, etc.) that
  SubagentRenderable.MakeEllipsisRow duplicates as a magic string.

## Structural considerations

**Hierarchy:** The documented architecture lists 5 layers (Terminal → Viewport →
Renderables → EventRouter → Program). The refactoring honors this by pushing
EventRouter out of Program and removing domain imports from Renderables.

**Abstraction:** The two input readers are a textbook missing abstraction from
organic growth. Unifying them into an InputReader with a pluggable echo
strategy eliminates duplication and centralizes Console.ReadKey ownership.

**Modularization:** Program.cs and EventList.cs each violate single-purpose.
Program decomposes into InputReader, EventRouter (own file), PermissionHandler,
and a thin Main. EventList decomposes into EventList (child management) +
TreeChrome (static helpers) + cached tree partitioning.

**Encapsulation:** Viewport's thread-safety should be systematic, not piecemeal.
The Func<Color> callbacks crossing thread boundaries need at minimum a
documented contract.

## Implementation plan

Each task is one commit. Tasks are ordered so each builds on the previous.

### Phase 1: Decompose Program.cs

- [x] **1. Extract EventRouter to own file.** Move the `EventRouter` class from
  Program.cs (lines 426–658) to `src/Ur.Tui/EventRouter.cs`. Change from
  `private sealed class` to `internal sealed class`. The primary constructor
  parameter `EventList eventList` stays as-is. Update Program.cs to
  instantiate the now-external class. Pure mechanical move — zero behavior
  change.

- [x] **2. Fix ToolRenderable domain leak.** Change `ToolRenderable` constructor
  from `ToolRenderable(ToolCallStarted started)` to `ToolRenderable(string
  formattedCall)`. Remove `using Ur.AgentLoop` from ToolRenderable.cs. Update
  EventRouter to call `started.FormatCall()` at routing time and pass the
  string (same pattern already used for subagent calls at line 504). After
  this commit, the Rendering namespace has zero `Ur.AgentLoop` imports.

- [x] **3. Unify input readers into InputReader.** Create
  `src/Ur.Tui/InputReader.cs` containing an `internal sealed class InputReader`
  that:
  - Owns all `Console.ReadKey` access (serializes the escape monitor and
    line reader — no more `_pauseKeyMonitor` flag).
  - Exposes `ReadLineAsync(string promptPrefix, Action<string> onPromptChanged,
    CancellationToken ct)` for viewport-mode input.
  - Exposes `ReadLineAsync(CancellationToken ct)` for pre-viewport (direct
    echo) input.
  - Exposes `MonitorEscapeKeyAsync(CancellationTokenSource turnCts)` that
    respects whether a ReadLine is in progress.
  - Delete `ReadLineInViewport`, `CancellableReadLine`, `MonitorEscapeKeyAsync`
    from Program.cs. Delete the `_pauseKeyMonitor` field.
  - This resolves: duplicate input readers (Abstraction/High), inappropriate
    intimacy via shared volatile bool (Modularization/High).

- [x] **4. Extract PermissionHandler.** Move `BuildCallbacks` (Program.cs lines
  216–280) into `src/Ur.Tui/PermissionHandler.cs` as an `internal static class
  PermissionHandler` with a static `Build` method. This method takes the
  dependencies it needs (EventRouter, InputReader, Viewport) as parameters.
  Program.Main calls `PermissionHandler.Build(...)` instead of the local
  method. After this commit, Program.cs should be ~100–120 lines: Main +
  EnsureReadyAsync + signal handlers.

### Phase 2: Viewport fixes

- [x] **5. Fix Viewport thread-safety.** Make `_inputPrompt`, `_sessionId`, and
  `_modelId` consistent with the existing thread-safety approach. Two options:
  (a) mark them `volatile` (works for reference types where we only need
  visibility, not atomicity of compound state), or (b) acquire `_redrawLock`
  in SetInputPrompt/SetSessionId/SetModelId. Option (a) is simpler and
  sufficient since each field is read independently. Add a comment block
  documenting the thread-safety contract for all cross-thread fields. Also
  fix the fossil indentation in BuildFrame (extra 4 spaces from removed lock
  block).

- [x] **6. Extract BuildFrame into region methods.** Split `BuildFrame` (95
  lines) into 4 private methods within Viewport:
  - `RenderHeader(ScreenBuffer buffer, int width)` — session ID + heavy rule
  - `RenderConversation(ScreenBuffer buffer, int width, int viewportHeight)` —
    tail-clip and write conversation rows
  - `RenderInputArea(ScreenBuffer buffer, int width, int viewportHeight)` —
    top rule + text row + bottom rule
  - `RenderStatusBar(ScreenBuffer buffer, int width, int viewportHeight)` —
    throbber + model ID
  - `BuildFrame` becomes 15–20 lines calling these in order.
  - Keep these as private methods in Viewport (no new classes — that would be
    over-engineering for the current feature set).

### Phase 3: EventList decomposition

- [x] **7. Extract TreeChrome.** Create
  `src/Ur.Tui/Rendering/TreeChrome.cs` containing an `internal static class
  TreeChrome` with:
  - The tree-drawing constants (BranchChar, LastBranchChar, VerticalChar,
    HorizontalChar, CircleChar, ChildChrome, NestChrome).
  - The three static helper methods: `MakeChildRow`, `MakeChildContinuationRow`,
    `PrependNestPrefix`.
  - Update EventList to call `TreeChrome.*` instead of local methods/constants.
  - Update SubagentRenderable.MakeEllipsisRow to use `TreeChrome.BranchChar`,
    `TreeChrome.HorizontalChar`, `TreeChrome.CircleChar` instead of the
    hardcoded `"├─ ● ..."` string.

- [~] **8. Add render caching to EventList.** EventList.Render() recomputes
  tree partitioning on every frame. Add caching analogous to
  TextRenderable's pattern:
  - Track `_lastChildCount` and `_lastWidth`. If neither changed, return
    cached rows.
  - Invalidate the cache in `Add()` (already fires Changed).
  - The Changed event from child renderables already sets `_dirty` on the
    viewport, which calls Render() — but the partition (which items are
    User/Circle/Plain, who is last) only changes when children are added, not
    when child content changes. Cache the partition separately from the
    rendered rows.
  - This is the subtlest task: content changes (e.g., TextRenderable.Append)
    fire Changed and trigger re-render, which must still re-render child rows
    (they may have new text), but need not recompute the partition. Consider
    two-level caching: (a) partition cache (invalidated on Add), (b) full row
    cache (invalidated on any Changed). Even just (b) would be an improvement
    for static history rows that don't change.

- [x] **9. Document Func<Color> thread-safety contract.** The `Func<Color>?`
  callbacks stored in EventList._children (line 87) are invoked during
  Render() on the timer thread but close over state mutated on the main
  thread (e.g., `ToolRenderable._state`). This works today because the reads
  are on value types (enum, bool). Add a doc comment on `EventList.Add()`
  documenting the contract: "getCircleColor callbacks may be invoked on any
  thread; implementations must be thread-safe or return value-type snapshots."
  Verify that all existing callbacks satisfy this (they do — CircleColor
  reads an enum field, which is atomic on all .NET platforms).

## Validation

- `make inspect` must pass after every commit (per AGENTS.md).
- Run the existing test suite after each commit.
- Manual verification: launch the TUI, send a message, verify tree rendering,
  tool call display, subagent display, escape-cancel, and permission prompts
  all work identically to before.

## Impact assessment

- **Code paths affected:** All rendering paths are touched but behavior is
  preserved. The only semantic change is ToolRenderable's constructor
  signature (string instead of ToolCallStarted).
- **Data or schema impact:** None.
- **Dependency or API impact:** ToolRenderable's constructor changes from
  `ToolCallStarted` to `string`. SubagentRenderable's constructor may drop
  the unused `subagentId` parameter (Medium finding, not in scope, but note
  it if it falls out naturally).

## Gaps and follow-up

- **Medium findings deferred:** BubbleStyle to own file, dead Viewport.Root
  property, SubagentRenderable unused subagentId param, ScreenBuffer indexer
  setter. These are worth doing but don't block the structural improvements.
- **Layout region composability:** Task 6 extracts private methods, not a
  composable ILayoutRegion system. If new regions are added frequently, a
  follow-up plan should introduce composable layout. For now, private methods
  are sufficient.
- **EventRouter handler registry:** The review noted that adding new event
  types requires editing the switch statement. A handler registry pattern
  could make this extensible, but with only 5 event types it's premature.
  Revisit if the event type count grows significantly.
