# Display Tool Output in TUI

## Goal

Show tool results inline in the conversation tree when a tool call completes. While a tool is pending, render only the call signature (current behavior). Once complete, render the result as indented child lines beneath the signature, limited to a configurable maximum.

## Desired outcome

**Pending:**
```
├─ ● glob(pattern: "**/*.sln")
```

**Completed:**
```
├─ ● glob(pattern: "**/*.csproj")
│    └─ ./src/Ox/Ox.csproj
│       ./src/Ur/Ur.csproj
│       ./src/Te/Te.csproj
│       ./src/Ur.Cli/Ur.Cli.csproj
│       ./tests/Te.Tests/Te.Tests.cspro
```

**Completed (truncated):**
```
├─ ● read_file(path: "Program.cs")
│    └─ using System;
│       using System.IO;
│       ...
│       (42 more lines)
```

**Completed (error):**
```
├─ ● bash(command: "rm -rf /nope")
│    └─ Permission denied
```

## Current state

- `ToolRenderable` renders exactly one `CellRow`: the formatted call string in `BrightBlack`.
- `SetCompleted(bool isError)` updates circle color but receives no result data.
- `ToolCallCompleted` already carries `Result` (string) and `IsError` (bool).
- `EventRouter` calls `completedTool.SetCompleted(completed.IsError)` — discards the result.
- `SubagentRenderable` already demonstrates the pattern of a multi-row renderable with internal tree chrome (signature row + child rows with `└─` / continuation indent).

## Structural considerations

This change is well-contained within the existing architecture. `ToolRenderable` already owns the full lifecycle of a tool call — adding result rendering is a natural extension of its responsibility. No new renderables or event types are needed.

The result lines use a simpler tree structure than `EventList`'s `├─ ● ` chrome. Since a tool result is a single block of text (not a list of independent children), we use `└─` on the first result line and indentation on continuations — no circles, no branching. This visually subordinates the output to the tool call without implying it's a separate tree node.

**PHAME fit:**
- **Hierarchy**: Result rendering stays inside `ToolRenderable` — no upward dependency.
- **Abstraction**: The renderable already handles the full tool lifecycle; adding result display is at the same abstraction level.
- **Modularization**: No new module needed — this is a natural expansion of `ToolRenderable`.
- **Encapsulation**: `EventRouter` passes the result string in; `ToolRenderable` owns all formatting decisions.

## Related code

- `src/Ox/Rendering/ToolRenderable.cs` — Primary change target. Currently renders one row; will render signature + result rows.
- `src/Ox/EventRouter.cs` — Routes `ToolCallCompleted` to `ToolRenderable`. Must pass `Result` string through.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — `ToolCallCompleted` already carries `Result` and `IsError`. No changes needed.
- `src/Ox/Rendering/TreeChrome.cs` — Constants for tree-drawing characters. May add a helper for result-line chrome, or `ToolRenderable` can use the constants directly.
- `src/Ox/Rendering/SubagentRenderable.cs` — Reference implementation for multi-row renderable with internal tree chrome and tail-clipping.

## Implementation plan

### 1. Update `ToolRenderable.SetCompleted` signature

- [x] Change `SetCompleted(bool isError)` to `SetCompleted(bool isError, string? result)`.
- [x] Store the result string in a private field `_result`.

### 2. Render result lines in `ToolRenderable.Render`

- [x] Add a `MaxResultLines` constant (e.g., 5). This caps the number of result lines shown to avoid flooding the viewport with large tool outputs.
- [x] After the signature row, if `_result` is non-null and non-empty:
  - Split `_result` on newlines.
  - Take at most `MaxResultLines` lines.
  - First result line: render with `└─ ` prefix (3 chars: `└`, `─`, space) in `BrightBlack`.
  - Continuation result lines: render with `   ` prefix (3 spaces) in `BrightBlack`.
  - If total lines exceeded `MaxResultLines`: append a final line `   (N more lines)` in `BrightBlack`.
- [x] All result text should use `BrightBlack` foreground (same as the signature) to keep tool output visually recessive.

### 3. Update `EventRouter` to pass result through

- [x] In `RouteMainEvent`, `ToolCallCompleted` case: change `completedTool.SetCompleted(completed.IsError)` to `completedTool.SetCompleted(completed.IsError, completed.Result)`.
- [x] In `RouteSubagentEvent`, `ToolCallCompleted` case: same change for `subCompletedTool.SetCompleted(...)`.

### 4. Handle edge cases

- [x] Empty result string: render no result lines (just the signature row, same as today).
- [x] Result with only whitespace: treat as empty.
- [x] Very long single line: let it extend beyond viewport width (the viewport's `ConsoleBuffer` already clips at buffer width). No word-wrapping — tool output is typically structured text where wrapping would harm readability.

## Validation

- **Build**: `dotnet build` passes.
- **Manual verification**:
  - Run a tool that produces multi-line output (e.g., `glob`) — verify result lines appear with `└─` chrome.
  - Run a tool that produces more lines than `MaxResultLines` — verify truncation with "(N more lines)" message.
  - Run a tool that errors — verify red circle + error result text.
  - Run a tool with empty result — verify only signature row shown.
  - Verify subagent tool calls also show results.

## Open questions

- **Max lines value**: 5 seems reasonable for most tools, but glob results or file reads might benefit from more. Should we use a higher default (e.g., 10), or is 5 the right balance between visibility and viewport space?
  
This should be a constant that we can tweak. 

- **Result formatting**: Some tools return JSON (e.g., tool results passed back to the LLM). Should we show the raw result string, or do any tools need special formatting? Starting with raw newline-split seems simplest — we can add tool-specific formatters later if needed.

Agreed.