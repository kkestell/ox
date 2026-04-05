# Subagent Tool

## Goal

Add a `run_subagent` tool that lets the active agent delegate a well-scoped subtask to a fresh,
independent agent loop and receive the result as a string.

## Desired outcome

The LLM can call `run_subagent(task: string)` to spawn a child agent that runs its own
LLM–tool–loop cycle against a clean message history, then returns its final response as the tool
result. This enables task decomposition (e.g., "research how X works, then come back and use the
findings") without muddying the parent conversation with the child's intermediate tool calls.

## How we got here

Six open-source coding agents were surveyed (Roo-Code, OpenDevin/OpenHands, Cline, Goose,
Continue, Aider) to understand the design space. The Ur codebase was also explored to map the
architecture points the change must touch. A subagent-as-tool pattern (Roo-Code's `NewTaskTool`,
Cline's `SubagentRunner`, Goose's `run_subagent_task`) appeared consistently as the most natural
fit for an agent that already has a clean loop + tool-dispatch separation.

## Approaches considered

### Option A — Minimal delegate injection

SubagentTool accepts a `Func<string, CancellationToken, Task<string>>` delegate as its constructor
argument. `UrHost.BuildSessionToolRegistry` creates and passes the delegate, which closes over the
chat client, tool registry, and workspace. No new interface.

- **Pros:** Zero new types; consistent with existing anonymous factory patterns.
- **Cons:** Delegates are opaque — hard to describe in XML doc, awkward to mock in tests,
  and the parameter list balloons if we later need to pass additional context (depth, system prompt
  override, etc.).
- **Failure mode:** The closed-over state silently grows over time with no contract.

### Option B — ISubagentRunner interface + SubagentRunner class

Define a small `ISubagentRunner` interface in `Ur.AgentLoop`. Implement it in a `SubagentRunner`
class that wires chat client + tool registry + workspace into a fresh `AgentLoop` call.
`SubagentTool` (in `Ur.Tools`) depends only on `ISubagentRunner`, breaking the
`Tools → AgentLoop → Tools` cycle at the interface boundary.

Registration follows the factory pattern introduced by plan 005: add a factory lambda in
`UrHost.BuildSessionToolRegistry` (not `BuiltinToolFactories.All`), mirroring how `SkillTool`
is handled there because it also needs cross-layer wiring (closing over `ISubagentRunner` built
from per-turn context). SubagentTool implements `IToolMeta` to declare its `OperationType`.

- **Pros:** Testable via mock; explicit contract; extensible (depth limits, model override, etc.)
  can be added to the interface without changing call sites.
- **Cons:** One extra type relative to Option A.
- **Failure mode:** If ISubagentRunner grows too many methods it becomes a mini-facade.

### Option C — SubagentTool lives in AgentLoop/ namespace

Avoid the cycle by not putting SubagentTool in `Ur.Tools` at all — put it in `Ur.AgentLoop`.
Register it manually in `BuildSessionToolRegistry`.

- **Pros:** No interface needed; no circular dependency.
- **Cons:** Breaks the established convention that tools live in `Ur.Tools`; `AgentLoop/` becomes
  a mixed namespace (loop logic + tool implementations).
- **Failure mode:** Precedent for other "special" tools migrating to AgentLoop/; cohesion erodes.

## Recommended approach

**Option B** — `ISubagentRunner` interface.

This parallels how `SkillTool` is handled: a tool with a cross-layer dependency is registered
in `UrHost.BuildSessionToolRegistry` via a factory lambda (not in `BuiltinToolFactories.All`),
because it needs more than the minimal `ToolContext` that builtin factories receive. The
interface makes the boundary explicit and keeps `SubagentTool` testable without pulling in a
real agent loop.

Key tradeoffs:
- One extra interface type in exchange for clean testability and a stable contract.
- Subagent tool registration lives in `BuildSessionToolRegistry`, consistent with `SkillTool`.
- `ToolContext` must be extended with `ChatClient`, `TurnCallbacks?`, and `SystemPrompt?`
  before subagent factories can close over them. The comment in `UrSession.RunTurnAsync`
  already anticipates this step.

## Related code

- `src/Ur/Tools/BuiltinToolFactories.cs` — all builtin tool factories; SubagentTool explicitly NOT added here (see SkillTool precedent)
- `src/Ur/UrHost.cs` — `BuildSessionToolRegistry` is the wiring point; SubagentTool registered here alongside SkillTool and extension tools
- `src/Ur/Sessions/UrSession.cs` — calls `BuildSessionToolRegistry`; comment anticipates extending `ToolContext` with per-turn fields for future tools like SubagentTool
- `src/Ur/ToolContext.cs` — currently carries `Workspace` and `SessionId`; must be extended with `ChatClient`, `TurnCallbacks?`, `SystemPrompt?` before SubagentTool can be registered via a factory
- `src/Ur/AgentLoop/AgentLoop.cs` — the loop SubagentRunner will instantiate; constructor is `(IChatClient, ToolRegistry, Workspace)`
- `src/Ur/AgentLoop/ToolInvoker.cs` — permission enforcement happens here; subagent uses the same invoker path automatically
- `src/Ur/Tools/PermissionMeta.cs` — SubagentTool will register as `OperationType.Execute`
- `src/Ur/Tools/SkillTool.cs` — model for a tool registered at host level via factory lambda in `BuildSessionToolRegistry`; also implements `IToolMeta`
- `tests/Ur.Tests/BuiltinToolTests.cs` — pattern to follow for new tool tests

## Current state

- `AgentLoop` is stateless: it takes a mutable `List<ChatMessage>` and runs until the LLM
  produces no tool calls. A fresh empty list produces a fully isolated turn — exactly what a
  subagent needs.
- `UrSession.RunTurnAsync` builds wrapped callbacks and a system prompt, then calls
  `UrHost.BuildSessionToolRegistry` to get the tool registry, then constructs and runs an
  `AgentLoop`. A `SubagentRunner` does the same steps without session persistence.
- Tool registration is factory-based (plan 005): `BuiltinToolFactories.All` contains a factory
  tuple per builtin tool; `SkillTool` and extension tools are added in `BuildSessionToolRegistry`
  alongside them. The same pattern applies for SubagentTool.
- `ToolContext` currently carries only `Workspace` and `SessionId`. Its comment explicitly
  names SubagentTool as the motivator for extending it with `ChatClient`, `TurnCallbacks?`, and
  `SystemPrompt?`. This extension is a prerequisite for SubagentTool implementation.
- Permission grants live in `PermissionGrantStore` inside `UrSession`. The subagent will run
  inside the same session turn — it reuses the session's wrapped callbacks, so grants already
  obtained by the parent (e.g., file write permissions) carry over naturally.
- `BuildSessionToolRegistry` uses `if (registry.Get(name) is null)` guards to prevent
  double-registration; SubagentTool registration should follow the same guard.

## Structural considerations

**Hierarchy (dependency direction):** The cycle `Ur.Tools → Ur.AgentLoop → Ur.Tools` is the core
structural challenge. `ISubagentRunner` is the seam: `Ur.Tools` depends on the interface, the
concrete `SubagentRunner` (in `Ur.AgentLoop`) depends on `AgentLoop`, and `UrHost` (above both)
wires them. Dependencies flow inward at every step.

**Abstraction:** The tool surface is appropriately thin — `run_subagent(task)` → string. The loop
mechanics, message accumulation, and result extraction are hidden inside `SubagentRunner`.

**Modularization:** Single responsibility is maintained: `SubagentTool` is a call adapter;
`SubagentRunner` is a loop coordinator; `ISubagentRunner` is the contract between them.

**Encapsulation:** The subagent's tool registry is a copy of the parent's minus `run_subagent`
itself. This prevents unbounded recursion without needing a depth counter. The registry copy is
constructed inside `SubagentRunner` from the parent registry, not exposed to the tool.

## Research

### Repo findings

- **Roo-Code (`NewTaskTool`):** Task-stack model; child inherits all tools; parent pauses while
  child runs. Result is the task ID, not the content — the UI renders the child's own thread.
  Too complex for Ur's headless use case.
- **Cline (`SubagentRunner`):** Separate runner class, NOT exposed as a tool — called directly
  from task context. Restricts subagent to read-only tools. Returns a typed result with token
  stats. Good pattern for the runner class; Ur does not need read-only restrictions since the
  permission system already gates writes.
- **Goose (`subagent_handler.rs`, `SubagentRunParams`):** Full new agent with provider/extension
  inheritance; mandatory `FinalOutputTool` for structured return; streaming callback for parent
  observability. The `FinalOutputTool` pattern is interesting but unnecessary for Ur since the
  last assistant message already serves as the natural exit signal when tool calls run dry.
- **OpenDevin (`AgentController.start_delegate`):** Shared event stream + metrics accumulator;
  depth level tracking; delegate controller tears itself down on finish. Ur doesn't have a shared
  event stream, making the depth tracking the most portable insight — worth a simple depth guard.
- **Continue / Aider:** No subagent systems. Single-agent models.

### Key insights

1. Excluding the subagent tool from the child registry (no self-recursion) is simpler and safer
   than an arbitrary depth counter. Most production agents use depth limits as a backstop, not the
   primary guard.
2. Subagents should NOT get a fresh permission grant scope — re-prompting for grants the user
   already approved in the same session is friction without safety benefit. Share the session
   callbacks.
3. The final assistant message (the one produced when the LLM stops calling tools) is a clean,
   natural result value. No special "FinalOutputTool" is needed.
4. Token/cost stats (Cline pattern) are nice to have but belong in a follow-up.

## Implementation plan

- [x] **Define `ISubagentRunner`** — create `src/Ur/AgentLoop/ISubagentRunner.cs` with a single
  `Task<string> RunAsync(string task, CancellationToken ct)` method; mark `internal`.

- [x] **Implement `SubagentRunner`** — create `src/Ur/AgentLoop/SubagentRunner.cs`.
  Constructor: `(IChatClient client, ToolRegistry tools, Workspace workspace, TurnCallbacks? callbacks, string? systemPrompt)`.
  `RunAsync`: create an empty `List<ChatMessage>`, prepend task as the first user `ChatMessage`,
  instantiate a fresh `AgentLoop`, run `RunTurnAsync` to completion, return the last
  `ResponseChunk` text accumulated or a default "no response" string if the subagent emitted only
  tool calls with no final text.

- [x] **Implement `SubagentTool`** — create `src/Ur/Tools/SubagentTool.cs`.
  Constructor: `(ISubagentRunner runner)`.
  Name: `"run_subagent"`. Single required string param `task`.
  `InvokeCoreAsync`: extract `task`, call `_runner.RunAsync(task, ct)`, return result string.
  No exception handling beyond what `InvokeCoreAsync` already catches — errors propagate as tool
  error results via `ToolInvoker`.

- [x] **Build subagent-scoped tool registry inside `SubagentRunner`** — when `SubagentRunner` is
  constructed it receives the parent's `ToolRegistry`. Create a filtered copy that omits
  `run_subagent` (to prevent direct self-recursion). Pass this filtered registry to `AgentLoop`.
  Add a `FilteredCopy(params string[] excludedNames)` method to `ToolRegistry` (or a simpler
  defensive `Clone` + removal approach).

- [x] **Register `SubagentTool` in `UrHost.BuildSessionToolRegistry`** — after building the base
  registry (builtins + skill + extensions), construct a `SubagentRunner` from the session's chat
  client, tool registry, workspace, and pass-through callbacks. Create and register
  `SubagentTool` with `OperationType.Execute` and a `TargetExtractors.FromKey("task")` extractor.
  Guard with `if (registry.Get(...) is null)` like the other registrations.

- [x] **Thread callbacks and system prompt through `SubagentRunner`** — `UrSession.RunTurnAsync`
  builds `wrappedCallbacks` and `systemPrompt` before creating `AgentLoop`. These need to reach
  `SubagentRunner`. The runner is created inside `BuildSessionToolRegistry`, but callbacks and the
  system prompt are built in `RunTurnAsync`. **Adjustment:** move `SubagentTool` registration out
  of `BuildSessionToolRegistry` and into `RunTurnAsync`, after `wrappedCallbacks` and
  `systemPrompt` are available. Register it by calling a new
  `BuiltinTools.RegisterSubagentTool(registry, runner)` helper or inline.

- [x] **Add `ToolRegistry.Clone` or `FilteredCopy`** — needed so `SubagentRunner` can produce a
  registry without `run_subagent`. Implement as an internal method on `ToolRegistry` that copies
  all registered tools and their `PermissionMeta` except the excluded names.

- [x] **Write unit tests** — in `tests/Ur.Tests/`:
  - `SubagentToolTests.cs`: mock `ISubagentRunner`; verify task string passed through; verify
    result returned as tool output; verify error propagation.
  - Extend `BuiltinToolTests.cs` or add `SubagentRunnerTests.cs` for the runner: use a fake
    `IChatClient` that returns one message with no tool calls; assert SubagentRunner returns the
    message text.

- [x] **Run `make inspect` and fix any issues** before committing.

## Impact assessment

- **Code paths affected:** `UrHost.BuildSessionToolRegistry` (or `UrSession.RunTurnAsync` —
  see threading note above), `ToolRegistry` (new `FilteredCopy` method), new files in
  `AgentLoop/` and `Tools/`.
- **Data or schema impact:** None — subagent results are ephemeral strings; no new persistence.
- **Dependency or API impact:** `ISubagentRunner` is `internal`; no public API surface changes.
  `ToolRegistry` grows one internal method.

## Validation

- **Tests:** All existing tests must stay green. New tests cover SubagentTool and SubagentRunner
  in isolation (mocked dependencies throughout).
- **Lint/format/typecheck:** `make inspect` — read `inspection-results.txt` and resolve all
  reported issues before committing.
- **Manual verification:** Run the TUI (`Ur.Tui`), ask the agent to "research X and summarize",
  observe the subagent tool call appearing in the output and a coherent string result flowing
  back into the parent turn.

## Open questions

- **Callbacks threading:** Should `SubagentTool` registration move into `UrSession.RunTurnAsync`
  (where callbacks are available) or should `SubagentRunner` be rebuilt each turn by being passed
  a callbacks factory? The latter keeps `BuildSessionToolRegistry` self-contained but adds a layer.
  Decide during implementation based on which approach disturbs fewer call sites.
