# Rendering Layer PHAME Refactor

## Goal

Fix the structural violations in `src/Ox/Rendering/` and the Ox root: duplicated
word-wrap algorithms, flat directory with no sub-grouping, width-math spread
across every file with no shared measurement primitive, leaky chrome-subtraction
contracts between parent and child containers, and a 731-line Viewport that owns
layout for four independent regions.

## Desired outcome

- One word-wrap implementation, used everywhere.
- One column-measurement function (`string.Length` today, wide-char-aware tomorrow),
  called from word-wrap and every width-clipping site instead of raw `.Length`.
- The chrome-subtraction contract is explicit: each container subtracts its own
  chrome before calling `child.Render(contentWidth)`. Children never know what was
  subtracted above them. SubagentRenderable's comment about "5 cols already
  subtracted" disappears because the contract is self-evident from the API.
- Viewport delegates region rendering to collaborator classes, each owning its
  own buffer-writing and width math. Viewport becomes a frame coordinator.
- The `Rendering/` directory has visible sub-grouping so related files cluster
  and unrelated files don't look like peers.
- All existing tests pass. No behavior changes.

## How we got here

The codebase grew through a sequence of feature plans (006-tui-renderable through
008-simple-tui-repl, then flattening in 006-flatten-event-list). Each plan added
renderables and layout logic without refactoring the structural seams. A prior
PHAME refactor (007-tui-phame-refactor) extracted EventRouter, InputReader,
PermissionHandler, and TreeChrome — shrinking Program.cs from 660 to 353 lines.
But Rendering/ was never reorganized, and the word-wrap duplication and
width-math scattering predate those fixes.

## Approaches considered

### Option A — Bottom-up: shared text primitives first

- Summary: Extract `TextLayout` (word-wrap + column measurement) first, then
  fix chrome contracts, then decompose Viewport, then reorganize the directory.
  Each phase is independently useful.
- Pros: Smallest diffs first. Each step compiles and passes tests. The shared
  primitives are immediately useful to both TextRenderable and TodoSection.
- Cons: The directory reorganization comes last, which means intermediate
  commits add new files to the same flat directory before grouping happens.
- Failure modes: If the project stalls mid-plan, you get the most valuable
  fix (deduplication) but still have a flat directory.

### Option B — Top-down: directory structure first

- Summary: Move files into subdirectories first, then extract TextLayout, then
  decompose Viewport, then fix chrome contracts.
- Pros: Makes the grouping visible immediately so subsequent files land in
  the right place.
- Cons: The initial move is a large rename-only commit that touches every
  `using` and test file. If any subsequent phase changes its mind about
  grouping, the directory shuffle was wasted motion.
- Failure modes: Bikeshedding the directory structure before the abstractions
  are clear. The right grouping depends on what gets extracted, so doing it
  first is premature.

### Option C — Mixed: extract primitives, then reorganize, then decompose

- Summary: Phase 1 extracts TextLayout (the duplicated algorithm) and
  ColumnMeasure (the missing measurement abstraction). Phase 2 fixes the
  chrome-subtraction contract. Phase 3 decomposes Viewport into collaborators.
  Phase 4 reorganizes the directory around the now-clear module boundaries.
- Pros: Each phase resolves one PHAME violation cleanly. Abstractions are
  extracted before directory structure, so grouping reflects real boundaries.
  Viewport decomposition happens after the width-math primitives exist, so
  the extracted collaborators use the shared primitives from day one.
- Cons: Slightly more total commits than Option A (chrome contract fix is a
  separate phase). Directory reorg still comes last.
- Failure modes: Same as A — stalling before Phase 4 leaves the directory flat.
  Acceptable because the structural improvements in Phases 1-3 are the high
  value items.

## Recommended approach

**Option C.** The word-wrap duplication and missing measurement primitive are
High severity and affect the most files. Fixing them first gives every
subsequent phase cleaner building blocks. The chrome contract fix is a focused
abstraction improvement that should land before Viewport decomposition (which
would otherwise propagate the leaky contract into new collaborator classes).
Directory reorganization comes last because the right grouping is obvious
only after the extraction work reveals the real module boundaries.

Key tradeoff: the directory stays flat through Phases 1-3. That's acceptable —
the flat structure is a Medium finding while the duplicated code and leaky
contracts are High.

## Related code

- `src/Ox/Rendering/TextRenderable.cs` — Contains `WrapText` (lines 115-170), the word-wrap + CellRow construction
- `src/Ox/Rendering/TodoSection.cs` — Contains `WordWrap` (lines 85-128), near-identical word-wrap returning strings
- `src/Ox/Rendering/Viewport.cs` — 731-line layout engine; `AppendClipped` (line 598), `BuildFrame` (line 340), region renderers
- `src/Ox/Rendering/EventList.cs` — Subtracts `TreeChrome.CircleChrome` before calling `child.Render()` (line 116)
- `src/Ox/Rendering/SubagentRenderable.cs` — Passes `availableWidth` through to inner list with leaky comment (line 98-101)
- `src/Ox/Rendering/TreeChrome.cs` — Defines `CircleChrome = 2` constant; owns circle-row construction
- `src/Ox/Rendering/CellRow.cs` — Boundary type between renderables and Viewport
- `src/Ox/Rendering/IRenderable.cs` — Core contract: `Render(int availableWidth)`
- `src/Ox/Rendering/Sidebar.cs` — Passes `availableWidth` through to sections
- `src/Ox/Rendering/ContextSection.cs` — Leaf renderable; uses `CellRow.FromText`
- `src/Ox/Rendering/ToolRenderable.cs` — Leaf renderable with lifecycle state machine
- `src/Ox/Rendering/Terminal.cs` — ANSI lifecycle helpers; no width math
- `tests/Ur.Tests/TuiRenderingTests.cs` — ~1200 lines of rendering tests
- `tests/Ur.Tests/TodoTests.cs` — TodoSection, Sidebar, Viewport sidebar/splash tests

## Current state

### Duplication

`TextRenderable.WrapText` (static, returns `List<CellRow>`) and
`TodoSection.WordWrap` (static, returns `List<string>`) implement the same
algorithm line-for-line: boundary-space check at the break column, `LastIndexOf(' ')`
fallback, hard-break when no space exists. The only difference is the return type
and `TextRenderable`'s paragraph-split (`\n`) preprocessing.

### Width math scattering

Every site that measures text width uses `.Length` directly. There is no shared
`ColumnWidth` function. Sites: `TextRenderable.WrapText`, `TodoSection.WordWrap`,
`Viewport.AppendClipped`, `EventList.Render` (`availableWidth - CircleChrome`),
`TreeChrome.MakeCircleRow`/`MakeContinuationRow` (hardcoded 2-col prefix),
`ToolRenderable.Render` (string append), `SubagentRenderable.Render` (pass-through).

### Chrome contract

The `IRenderable.Render(int availableWidth)` contract says "fit within this many
columns." Each container is responsible for subtracting its own chrome before
calling children. This contract is *implicit* — there is no documentation of it,
and SubagentRenderable's comment on line 98-101 ("the outer EventList's child
chrome (5 cols) is already subtracted") shows a child reasoning about its parent's
budget, which violates the contract.

Currently the actual nesting chain is:
- Viewport calls `_root.Render(leftWidth)` — subtracts sidebar allocation
- EventList calls `child.Render(availableWidth - CircleChrome)` — subtracts 2 for `● `
- SubagentRenderable calls `_innerList.Render(availableWidth)` — passes through
- Inner EventList calls `child.Render(availableWidth - CircleChrome)` — subtracts 2 again

This is actually correct behavior — each level subtracts its own chrome. The
leaky part is the *comment* and the *lack of a documented contract*. When
SubagentRenderable says "5 cols already subtracted" it's referencing the old
5-column chrome from the tree layout era. The comment is stale and misleading.

### Viewport size

731 lines across: lifecycle (Start/Stop/Dispose ~40), state + config (~80),
frame coordination (BuildFrame ~50), conversation rendering (~30), splash (~15),
input area (~90), sidebar (~25), throbber (~50), helpers (~60). The input area
renderer is the most complex region (ghost text, cursor, clipping, border math).

### Directory structure

13 files flat in `Rendering/`. No subdirectories. Natural groupings that emerged
from the review:
- **Text layout**: TextLayout (to be extracted), CellRow
- **Contracts**: IRenderable, ISidebarSection, BubbleStyle
- **Renderables**: TextRenderable, ToolRenderable, SubagentRenderable
- **Containers**: EventList, TreeChrome, Sidebar
- **Layout engine**: Viewport, Terminal
- **Sidebar sections**: ContextSection, TodoSection

## Structural considerations

**Hierarchy:** The rendering layer sits between Ur (domain) and Te (terminal
primitives). Dependency direction is clean: Rendering files import `Te.Rendering`
downward and are imported by `Ox.EventRouter` upward. Only `TodoSection` imports
from Ur (`Ur.Todo`), which is a domain-layer dependency in the rendering layer —
acceptable because `TodoStore` is a simple data model, not business logic.

**Abstraction:** The word-wrap algorithm is a text-layout concern, not a
renderable concern. It should be a standalone utility that both `TextRenderable`
and `TodoSection` call. The return type mismatch (CellRow vs string) is
superficial — the core algorithm produces line-break positions. `TextRenderable`
wraps the strings into CellRows as a separate step.

**Modularization:** Viewport currently has high cohesion along the "owns the
buffer" axis — every region renderer needs `_buffer` and `WriteRow`. The
decomposition must handle this: either pass the buffer to collaborators, or have
collaborators return rows and let Viewport write them. The latter preserves
Viewport's buffer ownership while shrinking its layout logic.

**Encapsulation:** Viewport's `_buffer` is `internal readonly` for test access.
The tests inspect cells at specific coordinates. Decomposing Viewport must not
break this test contract, but the extraction of region renderers into methods
that return `CellRow`s would actually improve it — tests could assert on returned
rows without knowing which buffer row they land on.

## Implementation plan

### Phase 1: Extract shared text layout

Extract the duplicated word-wrap algorithm and introduce a column-measurement
function that all width math routes through.

- [ ] **1.1 Create `TextLayout.cs` in `src/Ox/Rendering/`.** Static class with:
  - `static int ColumnWidth(ReadOnlySpan<char> text)` — returns `text.Length`
    today. Single call site for all width measurement. Comment explains this is
    the extension point for future wide-character support.
  - `static int ColumnWidth(char ch)` — single-char overload, returns 1 today.
  - `static IReadOnlyList<string> WordWrap(string text, int width)` — the
    shared word-wrap algorithm extracted from `TodoSection.WordWrap`. Takes
    a single string (no paragraph splitting), returns `List<string>`. Uses
    `ColumnWidth` internally instead of `.Length`.
  - `static IReadOnlyList<string> WordWrapParagraphs(string text, int width)` —
    handles `\n` splitting and trailing-newline trimming (from
    `TextRenderable.WrapText`), delegates to `WordWrap` per paragraph.

- [ ] **1.2 Rewrite `TextRenderable.WrapText` to use `TextLayout`.** Change from
  a self-contained 55-line method to: call `TextLayout.WordWrapParagraphs`, then
  convert each string to `CellRow.FromText(line, fg, bg, decorations)`. The
  method stays private and static but shrinks to ~10 lines.

- [ ] **1.3 Rewrite `TodoSection.WordWrap` to delegate to `TextLayout.WordWrap`.**
  Replace the 40-line private method body with a one-line call to
  `TextLayout.WordWrap(text, width)`.

- [ ] **1.4 Update `Viewport.AppendClipped` to use `TextLayout.ColumnWidth`.**
  Replace `text.Length` with `TextLayout.ColumnWidth(text)` and
  `_inputPrompt.Length` with `TextLayout.ColumnWidth(_inputPrompt)` in the
  input-area renderer.

- [ ] **1.5 Run tests.** `dotnet test` — all rendering tests must pass unchanged.
  No behavior should change since `ColumnWidth` returns `.Length` today.

### Phase 2: Fix the chrome-subtraction contract

Make the implicit contract explicit and fix the stale comment.

- [ ] **2.1 Document the chrome contract on `IRenderable.Render`.** Add a
  `<remarks>` block to the XML doc on `IRenderable.Render(int availableWidth)`
  explaining the contract: "Each container subtracts its own chrome before
  calling `Render` on its children. Children must not account for parent chrome —
  `availableWidth` is the full budget available to *this* renderable."

- [ ] **2.2 Fix the stale comment in `SubagentRenderable`.** The comment on lines
  98-101 references "the outer EventList's child chrome (5 cols)" which is from
  the old tree layout. Replace it with a contract-aligned comment: the inner
  list receives the same `availableWidth` because SubagentRenderable does not add
  its own columnar chrome — the outer EventList already subtracted CircleChrome
  before calling SubagentRenderable, and SubagentRenderable's children are
  rendered by the inner EventList which subtracts CircleChrome again on its own.

- [ ] **2.3 Use `TextLayout.ColumnWidth` in `EventList.Render`** for the chrome
  subtraction: `availableWidth - TreeChrome.CircleChrome` is fine (it's a
  constant), but ensure `TreeChrome.CircleChrome` is derived from
  `TextLayout.ColumnWidth` of the circle prefix string, not hardcoded as `2`.
  This makes the constant self-documenting and future-proof.

- [ ] **2.4 Run tests.** `dotnet test` — no behavior change.

### Phase 3: Decompose Viewport into collaborators

Extract the region renderers from Viewport into focused classes. Each collaborator
receives the rows it needs to populate and the width budget, and writes into the
shared buffer. Viewport remains the frame coordinator and buffer owner.

- [ ] **3.1 Extract `ComposerPanel`.** New `internal sealed class ComposerPanel`
  in `src/Ox/Rendering/ComposerPanel.cs`. Owns:
  - The input-area state: `_inputPrompt`, `_completionSuffix`
  - The status-line state: `_turnRunning`, `_modelId`, `_turnStartedAtTickMs`,
    `_lastAnimatedThrobberCounter`, and the `_tickCountProvider`
  - `SetInputPrompt(string)`, `SetCompletion(string?)`, `SetTurnRunning(bool)`,
    `SetModelId(string?)`, `ThrobberTick()` — moved from Viewport
  - `RenderInputArea(ConsoleBuffer buffer, int startRow, int width)` — the
    current `RenderInputArea` private method, including `WritePanelBorderRow`,
    `WritePanelInteriorRow`, `AppendClipped`, throbber cell generation
  - `IsDirty` flag: set when any setter fires, cleared by Viewport after render
  - The `InputAreaRows` constant stays here (it's the panel's height)
  - Static throbber helpers (`ComputeThrobberCounter`, `BuildThrobberCells`)
    become methods on ComposerPanel — they're only used by the status line

  Viewport's `SetInputPrompt`, `SetCompletion`, `SetTurnRunning`, `SetModelId`,
  `ThrobberTick` become thin forwarders to the composer panel (they still exist
  on Viewport because Program.cs and InputReader call them).

- [ ] **3.2 Extract `SplashRenderer`.** New `internal static class SplashRenderer`
  in `src/Ox/Rendering/SplashRenderer.cs`. Move `SplashLines` and `RenderSplash`
  out of Viewport. The method signature becomes
  `static void Render(ConsoleBuffer buffer, int width, int viewportHeight)`.
  Small extraction (~15 lines) but removes dead weight from Viewport.

- [ ] **3.3 Simplify Viewport.** After extraction, Viewport should shrink from
  ~731 to ~350-400 lines. It keeps: lifecycle (Start/Stop), resize detection,
  `_redrawLock`, `BuildFrame`, `RenderConversation` (which is already compact),
  `RenderSidebar`, `WriteRow`, and the dirty-flag mechanism. `BuildFrame`
  delegates to ComposerPanel, SplashRenderer, and its own simple methods.

- [ ] **3.4 Update tests.** Tests that assert on throbber cells via
  `Viewport.ComputeThrobberCounter` / `Viewport.BuildThrobberCells` now call
  `ComposerPanel.ComputeThrobberCounter` / `ComposerPanel.BuildThrobberCells`.
  Tests that call `viewport.SetInputPrompt` etc. continue to work because
  Viewport forwards to the panel. Tests that inspect `viewport._buffer`
  coordinates are unaffected — the buffer layout is identical.

- [ ] **3.5 Run tests.** `dotnet test` — all tests pass with no behavior change.

### Phase 4: Reorganize directory structure

Now that the module boundaries are clear, introduce subdirectories.

- [ ] **4.1 Create subdirectories and move files.**

  ```
  src/Ox/Rendering/
    TextLayout.cs           — shared text measurement + word-wrap
    CellRow.cs              — boundary type
    IRenderable.cs          — core contract
    ISidebarSection.cs      — sidebar contract
    BubbleStyle.cs          — enum (extract from EventList.cs if not already separate)
    Terminal.cs             — ANSI lifecycle
    Viewport.cs             — frame coordinator
    ComposerPanel.cs        — input area + status line
    SplashRenderer.cs       — splash art
    Conversation/
      EventList.cs          — root conversation container
      TreeChrome.cs         — circle prefix helpers
      TextRenderable.cs     — streaming text blocks
      ToolRenderable.cs     — tool call lifecycle
      SubagentRenderable.cs — subagent grouping
    Sidebar/
      Sidebar.cs            — sidebar container
      ContextSection.cs     — context usage display
      TodoSection.cs        — todo list display
  ```

  Contracts (`IRenderable`, `ISidebarSection`, `CellRow`, `BubbleStyle`) and
  layout infrastructure (`Viewport`, `ComposerPanel`, `SplashRenderer`,
  `Terminal`, `TextLayout`) stay at the root because they're cross-cutting.
  Conversation-tree renderables and sidebar sections get their own
  subdirectories because they form cohesive clusters.

- [ ] **4.2 Update namespaces.** Files in `Conversation/` become
  `namespace Ox.Rendering.Conversation`; files in `Sidebar/` become
  `namespace Ox.Rendering.Sidebar`. Update all `using` directives in:
  - `src/Ox/EventRouter.cs` (imports EventList, TextRenderable, ToolRenderable,
    SubagentRenderable)
  - `src/Ox/PermissionHandler.cs` (imports Viewport)
  - `src/Ox/Program.cs` (imports EventList, Viewport, ContextSection, TodoSection,
    Sidebar, TextRenderable)
  - `tests/Ur.Tests/TuiRenderingTests.cs`
  - `tests/Ur.Tests/TodoTests.cs`

- [ ] **4.3 Extract `BubbleStyle` to its own file** if it's still defined inside
  `EventList.cs`. It's a shared enum used by EventRouter and SubagentRenderable —
  it belongs at the rendering root, not buried in a container class.

- [ ] **4.4 Run tests.** `dotnet test` — pure namespace/file-move, no behavior change.

## Validation

- **Tests:** `dotnet test` after every phase. All ~439 tests must pass.
- **Manual:** `dotnet run --project src/Ox` — verify the TUI renders identically:
  conversation with streaming text, tool calls with lifecycle colors, subagent
  nesting, sidebar with todo items and context usage, ghost-text autocomplete,
  throbber animation, terminal resize, splash screen on fresh session.
- **Grep checks:**
  - After Phase 1: `grep -rn "LastIndexOf(' ')" src/Ox/` returns only
    `TextLayout.cs`.
  - After Phase 2: `grep -rn "5 cols" src/Ox/` returns nothing.
  - After Phase 3: `wc -l src/Ox/Rendering/Viewport.cs` < 450.

## Open questions

- Should `TextLayout` live in `Ox.Rendering` or in `Te.Rendering`? It has no Ox
  dependencies — only `System` and potentially `Te.Rendering.CellRow`. If Te
  gains text-layout needs (Te's own demo, future widgets), it would need to
  duplicate or import from Ox. Moving it to Te would be cleaner long-term but
  expands the scope of this plan.
