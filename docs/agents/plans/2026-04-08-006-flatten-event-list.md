# Flatten Event List and Improve Tool Display Names

## Goal

Replace the nested tree rendering in the event list with a flat layout: each event gets a simple `● ` prefix, blank lines separate top-level events, and tool output uses only `└─` for subordination. Rename tool display names from snake_case API names to friendly PascalCase names (e.g., `bash` → `Bash`, `read_file` → `Read`) and drop argument key names from the formatted call string.

## Desired outcome

Before:
```
└─ ● Please run dotnet test
   ├─ ● bash(command: "dotnet test")
   │    └─ Exit code: 0
   │       --- stdout ---
   │         Determining projects to restore...
   │       (17 more lines)
   └─ ● Tests passed: 439 passed, 0 failed.
```

After:
```
● Please run dotnet test

● Bash("dotnet test")
  └─ Exit code: 0
     --- stdout ---
     Determining projects to restore...
     (17 more lines)

● Tests passed: 439 passed, 0 failed.
```

Key changes:
- No tree connectors (├─, └─, │) between top-level events
- Each top-level event gets `● ` prefix (2 cols: circle + space)
- Blank line between top-level events
- Tool output retains `└─` for subordination (already in ToolRenderable)
- Tool names: `bash` → `Bash`, `read_file` → `Read`, `write_file` → `Write`, `update_file` → `Edit`, `glob` → `Glob`, `grep` → `Grep`, `run_subagent` → `Subagent`, `todo_write` → `Todo`
- Arguments rendered without key names: `Bash("dotnet test")` not `bash(command: "dotnet test")`

## Related code

- `src/Ur/AgentLoop/AgentLoopEvent.cs` — `FormatCall()` method formats tool name + args for display
- `src/Ox/Rendering/EventList.cs` — The root conversation container; renders tree chrome for all events
- `src/Ox/Rendering/TreeChrome.cs` — Shared tree-drawing constants and chrome helpers
- `src/Ox/Rendering/ToolRenderable.cs` — Renders a single tool call lifecycle; already uses `└─` for output
- `src/Ox/Rendering/SubagentRenderable.cs` — Groups subagent events; uses inner EventList
- `src/Ox/EventRouter.cs` — Routes events to renderables; creates ToolRenderable/SubagentRenderable
- `tests/Ur.Tests/TuiRenderingTests.cs` — Tests for ToolRenderable, EventList, SubagentRenderable

## Current state

- EventList renders a two-level tree: User items at level 1, Circle items (tool calls, assistant text) nested at level 2 underneath their parent User
- TreeChrome provides all tree-drawing helpers: `MakeChildRow` (├─ ● / └─ ●), `MakeChildContinuationRow` (│ / spaces), `PrependNestPrefix` (│ + indent for nesting)
- `ChildChrome` = 5 columns (├─ ● ), `NestChrome` = 3 columns (│  )
- ToolRenderable already renders tool output with `└─` prefix and 3-space continuation indent — this is the desired subordination style
- `FormatCall()` uses raw `ToolName` (snake_case) and formats as `tool_name(key: "val")`
- BubbleStyle enum has User, Circle, Plain — the User/Circle distinction drives the nesting that we're removing

## Structural considerations

The nesting logic (User as parent, Circle as children) permeates EventList's Render method. Flattening means:
- BubbleStyle can be simplified — the User vs Circle distinction no longer drives tree depth. Both get the same `● ` prefix. We keep the enum so EventList still knows which items get circles and which are Plain, and so User items can still have a blue circle.
- TreeChrome simplifies dramatically — only need a method to prepend `● ` (circle + space) in the appropriate color. The `└─` for tool output is already handled inside ToolRenderable.
- SubagentRenderable's inner EventList will also flatten, which is correct — subagent children should render the same way.

## Implementation plan

### 1. Add display name mapping to FormatCall

- [ ] In `src/Ur/AgentLoop/AgentLoopEvent.cs`, add a static dictionary mapping snake_case tool names to display names:
  - `bash` → `Bash`
  - `read_file` → `Read`
  - `write_file` → `Write`
  - `update_file` → `Edit`
  - `glob` → `Glob`
  - `grep` → `Grep`
  - `run_subagent` → `Subagent`
  - `todo_write` → `Todo`
  - Fallback: capitalize first letter of each `_`-separated word (PascalCase) for unknown tools (extensions, etc.)
- [ ] Change `FormatCall()` to use the display name and omit argument keys: `DisplayName("val1", "val2")` instead of `tool_name(key1: "val1", key2: "val2")`

### 2. Simplify EventList rendering to flat layout

- [ ] Rewrite `EventList.Render()` to iterate children linearly:
  - For each Plain item: render verbatim (unchanged)
  - For each User or Circle item: render with `● ` prefix (circle char + space = 2 columns), circle color from `getCircleColor` (blue for User, supplied color for Circle)
  - Content width = `availableWidth - 2` (just the `● ` prefix)
  - Continuation rows for wrapped text: 2 spaces (align with content after `● `)
  - Insert a blank `CellRow` between each top-level item (except before the first and after the last)
- [ ] Remove `FindLastTopLevelIndex()`, `RenderUserItem()`, `RenderChild()`, `RenderNestedChild()` — no longer needed
- [ ] Update `ChildChrome` constant (now 2, was 5) or replace with a new simpler constant

### 3. Simplify TreeChrome

- [ ] Remove or gut `TreeChrome` — the only tree chrome now is:
  - `● ` prefix for top-level items (2 cols)
  - Continuation indent (2 spaces) for wrapped text
  - The `└─` inside ToolRenderable is self-contained and doesn't use TreeChrome
- [ ] Remove `MakeChildRow`, `MakeChildContinuationRow`, `MakeLastParentContinuationRow`, `PrependNestPrefix` — these all produced the complex tree connectors
- [ ] Add a simple `MakeCircleRow(CellRow content, Color circleColor)` that prepends `● ` and `MakeContinuationRow(CellRow content)` that prepends 2 spaces
- [ ] Update `ChildChrome` to 2 (or rename to `CircleChrome`). Remove `NestChrome`.

### 4. Update SubagentRenderable

- [ ] SubagentRenderable's inner EventList will automatically flatten (it reuses EventList). No structural changes needed, but verify that the blank-line separators and `● ` prefix look correct for subagent children.
- [ ] The subagent's signature row (row 0) stays as-is (dark gray `_formattedCall`). The inner EventList renders flat below it.
- [ ] Update the MakeEllipsisRow to match the new flat style (just `● ...` instead of `├─ ● ...`)

### 5. Update ToolRenderable result indentation

- [ ] The `└─` prefix and `   ` continuation indent in ToolRenderable are currently 3 columns. These need to be indented to align under the tool content (after the `● ` prefix). Currently the `└─` is at column 0 of the content area, which means it will appear at column 2 in the final output (after `● `). This matches the desired output — verify it looks correct.

### 6. Update tests

- [ ] Update `TuiRenderingTests.ToolRenderableTests` — `FormatCall()` output changes from `read_file(path: "foo.txt")` to `Read("foo.txt")`; update all `Assert.Contains("read_file", ...)` to `Assert.Contains("Read", ...)`
- [ ] Update `EventList` tests — tree chrome assertions (├─, └─, │ between events) must change to flat `● ` prefix and blank line separators
- [ ] Update `SubagentRenderable` tests — ellipsis row format changes
- [ ] Update `ViewportBufferTests` — `ChildChrome` width changes from 5 to 2, affecting column positions
- [ ] Run `dotnet test` and fix any remaining failures

## Validation

- Tests: `dotnet test` — all existing tests updated and passing
- Manual verification: Run the TUI, send a message that triggers tool calls, verify:
  - Events are flat with `● ` prefix
  - Blank lines between events
  - Tool names show as `Bash("...")`, `Read("...")`, etc.
  - Tool output shows with `└─` subordination
  - No tree connectors (├─, │) between events
  - Subagent blocks render correctly with flat inner events

## Open questions

- Should extension/MCP tool names also get display name mapping, or is the PascalCase fallback sufficient? (Current plan: PascalCase fallback for unknown tools)
