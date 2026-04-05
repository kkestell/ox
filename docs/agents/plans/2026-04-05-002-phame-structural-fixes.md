# PHAME Structural Fixes — ToolRegistry Placement, Abstraction Leaks, Encapsulation

## Goal

Fix the 8 High-severity findings from the PHAME design review of `src/Ur/`:
3 hierarchy violations (upward dependencies on `Ur.AgentLoop`),
3 abstraction violations (`AIFunctionArguments` leaking through public API),
1 encapsulation violation (`ToolRegistry.All()` returning mutable list),
and 1 abstraction violation (`Settings.Get` exposing `JsonElement`).

## Desired outcome

- No lower-level layer (`Ur.Tools`, `Ur.Extensions`) imports `Ur.AgentLoop`.
- `AIFunctionArguments` does not appear in any public API signature.
- `ToolRegistry.All()` returns `IReadOnlyList<AITool>`.
- `Settings.Get` is internal; callers use typed accessors.
- All existing tests pass without behavioral changes.

## How we got here

A PHAME review of `src/Ur/` identified 8 High-severity structural issues. The
three Hierarchy findings share a root cause: `ToolRegistry` lives in
`Ur.AgentLoop` but is consumed by lower layers. The three Abstraction findings
share a root cause: `AIFunctionArguments` (from Microsoft.Extensions.AI) leaks
through `ToolRegistry.Register`'s public signature into `PermissionMeta` and all
tool registration lambdas. The remaining two are independent fixes.

## Approaches considered

### Workstream 1: ToolRegistry placement

#### Option A — Move to `Ur.Tools` (chosen)

- Summary: Relocate `ToolRegistry` and `PermissionMeta` to the `Ur.Tools`
  namespace. Dependencies then flow AgentLoop → Tools → Permissions (downward).
- Pros: Natural fit (tools namespace already owns tool implementations), no new
  directories, eliminates all 3 upward dependencies.
- Cons: `Ur.Tools` grows slightly; `PermissionMeta` moves with it.
- Failure modes: If PermissionMeta acquires AgentLoop-specific dependencies
  later. Currently it only depends on `OperationType` (Permissions) and
  `AIFunctionArguments` (external).

#### Option B — New `Ur.ToolManagement` namespace

- Summary: Create a standalone namespace between Tools and AgentLoop.
- Pros: Maximum separation. Cons: Adds a layer for two files — lasagna risk.
- Failure modes: Over-engineering; another namespace to import everywhere.

### Workstream 2: AIFunctionArguments leakage

#### Option A — Replace with `IReadOnlyDictionary<string, object?>` in public API

- Summary: Change `ToolRegistry.Register` to accept
  `Func<IReadOnlyDictionary<string, object?>, string>?`.
- Pros: Simple, removes external type. Cons: Loses semantic clarity of what the
  function receives.

#### Option B — Create `ITargetExtractor` interface (chosen)

- Summary: Define `ITargetExtractor` with a `string Extract(IReadOnlyDictionary<string, object?> arguments)` method. `ToolRegistry.Register` accepts
  `ITargetExtractor?` instead of `Func<AIFunctionArguments, string>?`.
- Pros: Named abstraction; self-documenting; extensible if extraction needs
  grow. Cons: Slightly more ceremony.
- Failure modes: Over-engineering for 7 one-liner lambdas. Mitigated by
  providing a static factory method for the common case.

## Recommended approach

**Workstream 1:** Move ToolRegistry + PermissionMeta to `Ur.Tools`.
**Workstream 2:** Introduce `ITargetExtractor` in `Ur.Tools`.
**Workstream 3:** Fix `All()` return type and `Settings.Get` visibility.

Key tradeoffs: The moves are namespace-only refactors with no behavioral
changes. The `ITargetExtractor` interface adds a type but removes the
`AIFunctionArguments` dependency from the public surface.

## Related code

- `src/Ur/AgentLoop/ToolRegistry.cs` — The class being moved; public API surface
- `src/Ur/AgentLoop/PermissionMeta.cs` — Internal record stored by ToolRegistry; moves with it
- `src/Ur/AgentLoop/ToolInvoker.cs` — Consumes ToolRegistry and PermissionMeta (will import from new location)
- `src/Ur/AgentLoop/AgentLoop.cs` — Receives ToolRegistry via constructor
- `src/Ur/Tools/BuiltinTools.cs` — Registers 6 tools with target extractors; currently imports Ur.AgentLoop
- `src/Ur/Extensions/Extension.cs` — Registers extension tools into ToolRegistry; currently imports Ur.AgentLoop
- `src/Ur/Extensions/ExtensionCatalog.cs` — Iterates extensions to register tools; currently imports Ur.AgentLoop
- `src/Ur/UrHost.cs` — Orchestration point that wires everything together
- `src/Ur/Sessions/UrSession.cs` — Receives ToolRegistry indirectly from host
- `src/Ur/Tools/ToolArgHelpers.cs` — Used by target extractors; stays in Ur.Tools
- `src/Ur/Configuration/Settings.cs` — Has public `Get` returning `JsonElement?`
- `src/Ur/Configuration/UrConfiguration.cs` — Only external caller of `Settings.Get`
- `tests/Ur.Tests/BuiltinToolTests.cs` — Constructs ToolRegistry directly
- `tests/Ur.Tests/ExtensionSystemTests.cs` — Constructs ToolRegistry directly
- `tests/Ur.Tests/PermissionTests.cs` — Constructs ToolRegistry directly
- `tests/Ur.Tests/HostSessionApiTests.cs` — Uses ToolRegistry via host API

## Structural considerations

**Hierarchy:** After the move, the dependency graph becomes:
`Ur.AgentLoop` → `Ur.Tools` → `Ur.Permissions`. No upward or circular deps.
`Ur.Extensions` → `Ur.Tools` (same level, acceptable — extensions register tools).

**Abstraction:** `ITargetExtractor` creates a named boundary between tool
registration (domain concept: "what does this tool operate on?") and the AI
client library (infrastructure: `AIFunctionArguments`). The interface lives in
`Ur.Tools`, the natural home for tool-related contracts.

**Encapsulation:** `ToolRegistry.All()` returning `IReadOnlyList` prevents
accidental mutation of the cached list. `Settings.Get` becoming internal keeps
`JsonElement` from leaking to consumers outside `Configuration`.

## Refactoring

Refactoring is done before adding new types. Each step is independently
committable and testable.

1. **Move ToolRegistry.cs** from `Ur.AgentLoop` to `Ur.Tools`. Update namespace
   declaration. Update all `using Ur.AgentLoop` imports that exist solely for
   ToolRegistry. This eliminates the 3 upward dependency violations.

2. **Move PermissionMeta.cs** from `Ur.AgentLoop` to `Ur.Tools`. It must move
   with ToolRegistry because ToolRegistry stores and returns it. Update
   namespace + imports in ToolInvoker.cs and AgentLoop.cs.

3. **Introduce ITargetExtractor** in `Ur.Tools`. Change `ToolRegistry.Register`
   to accept `ITargetExtractor?` instead of `Func<AIFunctionArguments, string>?`.
   Update `PermissionMeta` to store `ITargetExtractor?`. Provide a static helper
   (e.g., `TargetExtractor.FromKey(string key)`) for the common pattern of
   extracting a named string argument.

4. **Fix ToolRegistry.All() return type** — change from `IList<AITool>` to
   `IReadOnlyList<AITool>`. Update the `_allCache` field type to match.

5. **Make Settings.Get internal** — change access modifier from `public` to
   `internal`. Verify no external assembly references it.

## Implementation plan

### Phase 1: Move ToolRegistry and PermissionMeta (Hierarchy fixes)

- [x] Move `src/Ur/AgentLoop/ToolRegistry.cs` to `src/Ur/Tools/ToolRegistry.cs`
- [x] Change namespace from `Ur.AgentLoop` to `Ur.Tools` in ToolRegistry.cs
- [x] Move `src/Ur/AgentLoop/PermissionMeta.cs` to `src/Ur/Tools/PermissionMeta.cs`
- [x] Change namespace from `Ur.AgentLoop` to `Ur.Tools` in PermissionMeta.cs
- [x] In `src/Ur/AgentLoop/ToolInvoker.cs`: replace `using Ur.AgentLoop` (self-ref) with `using Ur.Tools` for ToolRegistry + PermissionMeta
- [x] In `src/Ur/AgentLoop/AgentLoop.cs`: add `using Ur.Tools` if not present
- [x] In `src/Ur/Tools/BuiltinTools.cs`: remove `using Ur.AgentLoop` (ToolRegistry now local)
- [x] In `src/Ur/Extensions/Extension.cs`: replace `using Ur.AgentLoop` with `using Ur.Tools`
- [x] In `src/Ur/Extensions/ExtensionCatalog.cs`: replace `using Ur.AgentLoop` with `using Ur.Tools`
- [x] In `src/Ur/UrHost.cs`: add `using Ur.Tools` if not present; remove `using Ur.AgentLoop` if no longer needed
- [x] Update test files: `BuiltinToolTests.cs`, `ExtensionSystemTests.cs`, `PermissionTests.cs`, `HostSessionApiTests.cs` — replace `using Ur.AgentLoop` with `using Ur.Tools` where needed
- [x] Build and run all tests — no behavioral changes expected

### Phase 2: Introduce ITargetExtractor (Abstraction fixes)

- [x] Create `src/Ur/Tools/ITargetExtractor.cs` with `string Extract(IReadOnlyDictionary<string, object?> arguments)` method
- [x] Create a static helper class (e.g., `TargetExtractors`) with `FromKey(string key, string fallback = "(unknown)")` that returns an `ITargetExtractor` wrapping the common `GetOptionalString(args, key) ?? fallback` pattern
- [x] Update `ToolRegistry.Register` parameter from `Func<AIFunctionArguments, string>?` to `ITargetExtractor?`
- [x] Update `PermissionMeta` record field from `Func<AIFunctionArguments, string>?` to `ITargetExtractor?`
- [x] Update `PermissionMeta.ResolveTarget` to call `TargetExtractor?.Extract(call.Arguments ?? ...)` instead of constructing `AIFunctionArguments`
- [x] Update `BuiltinTools.cs`: replace lambda extractors with `TargetExtractors.FromKey("file_path")` etc.
- [x] Update `UrHost.cs`: replace SkillTool's extractor lambda with `TargetExtractors.FromKey("skill")`
- [x] Remove `using Microsoft.Extensions.AI` from PermissionMeta.cs if no longer needed
- [x] Build and run all tests

### Phase 3: Independent fixes (Encapsulation + Abstraction)

- [x] In `ToolRegistry.cs`: change `_allCache` field type from `IList<AITool>?` to `IReadOnlyList<AITool>?`
- [x] In `ToolRegistry.cs`: change `All()` return type from `IList<AITool>` to `IReadOnlyList<AITool>`
- [x] In `AgentLoop.cs`: update any variable receiving `All()` if type is explicit
- [x] In `Settings.cs`: change `public JsonElement? Get(string key)` to `internal JsonElement? Get(string key)`
- [x] Build and run all tests

## Validation

- `dotnet build src/Ur/Ur.csproj` — clean build, no warnings about missing types
- `dotnet test tests/Ur.Tests/Ur.Tests.csproj` — all existing tests pass
- `dotnet test tests/Ur.IntegrationTests/Ur.IntegrationTests.csproj` — integration tests pass
- `make inspect` — verify inspection-results.txt is clean of new findings
- Manual check: grep for `using Ur.AgentLoop` in `src/Ur/Tools/` and `src/Ur/Extensions/` — should find zero hits
- Manual check: grep for `AIFunctionArguments` in public API signatures — should only appear in tool `InvokeCoreAsync` overrides (inherited from framework)

## Open questions

- Should `ITargetExtractor` support async extraction, or is sync sufficient? All current extractors are synchronous string lookups, so sync seems right. If async is needed later, the interface can be extended.
