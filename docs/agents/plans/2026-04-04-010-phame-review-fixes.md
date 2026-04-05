# Fix PHAME Review Violations

## Goal

Address all 9 findings from the PHAME design review of the Skills subsystem
integration: 1 High, 4 Medium, and 4 Low severity issues spanning hierarchy,
abstraction, and modularization principles. The changes are pure refactoring —
no new features, no behavior changes.

## Desired outcome

- The `Tools` namespace has no dependency on `Skills` (hierarchy fixed).
- `UrHost.StartAsync` is decomposed into named initialization steps (readability).
- `UrSession.RunTurnAsync` delegates skill coordination rather than doing it inline (feature envy reduced).
- Argument extraction helpers are consolidated in `ToolArgHelpers` (single home for the pattern).
- All existing tests pass without modification (or with only namespace/import updates).

## Related code

- `src/Ur/Tools/SkillTool.cs` — The tool that bridges Skills into the tool system; currently in wrong namespace.
- `src/Ur/Tools/BuiltinTools.cs` — Contains `RegisterSkillTool` method that couples Tools→Skills.
- `src/Ur/UrHost.cs` — Startup orchestrator; `StartAsync` is 70 lines, `BuildSessionToolRegistry` will absorb skill tool registration.
- `src/Ur/Sessions/UrSession.cs` — Session turn driver; has slash command parsing and skill expansion inline.
- `src/Ur/AgentLoop/AgentLoop.cs` — `IsPermissionDeniedAsync` mixes permission logic with argument marshaling.
- `src/Ur/AgentLoop/PermissionMeta.cs` — Holds `TargetExtractor` func; target extraction could live here.
- `src/Ur/Tools/ToolArgHelpers.cs` — Existing shared argument extraction helpers; natural home for `ExtractStringArg`.
- `src/Ur/Skills/SkillExpander.cs` — Expansion logic; could absorb slash command tag wrapping.
- `src/Ur/Extensions/ExtensionCatalog.cs` — `ReplaceOverrides` mixes levels.
- `tests/Ur.Tests/BuiltinToolTests.cs` — Tests for builtin tool registration; will need import updates.
- `tests/Ur.Tests/Skills/` — Skill tests that may reference SkillTool.

## Current state

- The Skills subsystem was just integrated (plan 009). It works correctly but
  the wiring created a few structural shortcuts that the PHAME review caught.
- `SkillTool` lives in `Ur.Tools` but depends on `Ur.Skills` — an upward
  dependency since Tools is a lower-level technical layer.
- `BuiltinTools.RegisterSkillTool` is a static method that instantiates
  `SkillTool`, which was a convenience during initial implementation but
  creates unnecessary coupling between the Tools and Skills layers.
- `UrHost.StartAsync` has been growing with each new subsystem (extensions,
  skills) and is now 70 lines of sequential initialization.
- `UrSession.RunTurnAsync` handles slash command parsing, skill expansion,
  system prompt building, tool registry construction, and message persistence
  in a single method.

## Structural considerations

These fixes are ordered so that each phase builds on the previous one:

1. **Phase 1 (Hierarchy)** moves `SkillTool` to `Ur.Skills` and eliminates the
   Tools→Skills dependency. This must come first because later phases touch the
   same files.
2. **Phase 2 (Modularization)** decomposes `UrHost.StartAsync` while the method
   is still familiar from Phase 1 changes.
3. **Phase 3 (Modularization)** extracts slash command parsing from `UrSession`
   into the Skills namespace, reducing feature envy.
4. **Phase 4 (Abstraction)** cleans up mixed-level helpers across several files.

No phase changes public API. All changes are internal reorganization.

## Implementation plan

### Phase 1 — Fix hierarchy: move SkillTool to Skills namespace

This is the highest-severity finding. The Tools layer should not depend on Skills.

- [x] Move `src/Ur/Tools/SkillTool.cs` to `src/Ur/Skills/SkillTool.cs` and
  change its namespace from `Ur.Tools` to `Ur.Skills`. Update the `using`
  directives — it no longer needs `using Ur.Skills` but will need
  `using Ur.Tools` for `ToolArgHelpers`.

- [x] Remove the `RegisterSkillTool` method from `BuiltinTools.cs` entirely.
  Also remove the duplicate `<summary>` XML doc block above it (lines 73-82
  have two `<summary>` tags — the first is orphaned).

- [x] In `UrHost.BuildSessionToolRegistry` (lines 89-106), replace the call to
  `BuiltinTools.RegisterSkillTool(registry, Skills, sessionId)` with inline
  registration that instantiates `SkillTool` directly:
  ```csharp
  // Skill invocation tool, bound to this session's ID.
  if (registry.Get("skill") is null)
  {
      registry.Register(
          new SkillTool(Skills, sessionId),
          Permissions.OperationType.ReadInWorkspace,
          targetExtractor: args => ToolArgHelpers.ExtractStringArg(args, "skill"));
  }
  ```
  This keeps skill tool registration at the orchestration layer where both
  Tools and Skills are already visible, eliminating the upward dependency.

- [x] `BuiltinTools.cs`: make `ExtractStringArg` `internal` (it's currently
  `private`) so `UrHost` can reference it for the targetExtractor lambda above.
  Alternatively, move it to `ToolArgHelpers` as part of Phase 4 and do that
  step first — see Phase 4 notes.

- [x] Update any test files that import `Ur.Tools.SkillTool` to use
  `Ur.Skills.SkillTool` instead.

- [x] Verify: `dotnet build` succeeds; `dotnet test` passes.

### Phase 2 — Decompose UrHost.StartAsync

- [x] Extract the body of `StartAsync` (lines 144-201) into named private
  static methods. The method should read as a high-level recipe:
  ```csharp
  var workspace = InitializeWorkspace(workspacePath);
  var modelCatalog = InitializeModelCatalog(userDataDirectory);
  var (schemaRegistry, extensionEntries) = await InitializeExtensionsAsync(
      workspace, userDataDirectory, systemExtensionsPath, userExtensionsPath, ct);
  var settings = LoadSettings(schemaRegistry, userSettingsPath, workspace, userDataDirectory);
  var extensions = await ActivateExtensionsAsync(extensionEntries, workspace, userDataDirectory, ct);
  var skillRegistry = await LoadSkillsAsync(userDataDirectory, workspace, ct);
  var sessions = new SessionStore(workspace.SessionsDirectory);
  ```
  Each extracted method is `private static` and returns exactly what the
  orchestrator needs. Keep the methods in `UrHost.cs` — they are not
  independently useful, just decompositions for readability.

- [x] Verify: `dotnet build` succeeds; `dotnet test` passes. No behavior change.

### Phase 3 — Reduce UrSession feature envy: extract slash command handling

- [x] Create `src/Ur/Skills/SlashCommandParser.cs` with two static methods
  extracted from `UrSession`:
  - `ParseName(string input) → string` (was `ParseSlashCommandName`)
  - `ParseArgs(string input) → string` (was `ParseSlashCommandArgs`)

  These are pure string functions with no session state dependency.

- [x] Move the tag-wrapping logic from `UrSession.TryExpandSlashCommand`
  (lines 190-195) into a new static method in `SlashCommandParser`:
  `FormatExpansion(string skillName, string args, string expandedContent) → string`.
  This consolidates all slash-command formatting in one place.

- [x] Simplify `UrSession.TryExpandSlashCommand` to delegate to
  `SlashCommandParser` for parsing and formatting, and to `SkillExpander` for
  expansion. The method should be ~5 lines of coordination.

- [x] Update `RunTurnAsync`'s slash command block (lines 102-118) to use
  `SlashCommandParser.ParseName` instead of the private methods.

- [x] Verify: `dotnet build` succeeds; `dotnet test` passes.

### Phase 4 — Clean up mixed abstraction levels

These are all Low severity but worth fixing while we're in these files.

- [x] **Move `ExtractStringArg` to `ToolArgHelpers`.**
  `BuiltinTools.ExtractStringArg` and `BuiltinTools.ExtractFilePath` do the
  same JsonElement/string coercion that `ToolArgHelpers` already handles for
  `AIFunctionArguments`. Add an overload in `ToolArgHelpers` that accepts
  `IDictionary<string, object?>` (the type used by `PermissionMeta.TargetExtractor`):
  ```csharp
  internal static string ExtractStringArg(IDictionary<string, object?> args, string key)
  ```
  Then delete the private copies from `BuiltinTools` and update all
  `targetExtractor` lambdas to reference `ToolArgHelpers.ExtractStringArg`.

- [x] **Extract permission-target resolution from `AgentLoop.IsPermissionDeniedAsync`.**
  Lines 255-256 cast `call.Arguments` and invoke `TargetExtractor`. Extract
  this into a static method on `PermissionMeta`:
  ```csharp
  internal string ResolveTarget(FunctionCallContent call)
  {
      var rawArgs = call.Arguments as IDictionary<string, object?>
          ?? new Dictionary<string, object?>();
      return TargetExtractor?.Invoke(rawArgs) ?? call.Name;
  }
  ```
  Then `IsPermissionDeniedAsync` calls `meta.ResolveTarget(call)` instead of
  inlining the argument marshaling. When `meta` is null, fall back to
  `call.Name`.

- [x] **Simplify `ExtensionCatalog.ReplaceOverrides`.**
  Replace the Clear+foreach pattern with a one-liner comment and consider
  renaming to clarify intent. The simplest fix:
  ```csharp
  private static void ReplaceOverrides(
      Dictionary<ExtensionId, bool> destination,
      Dictionary<ExtensionId, bool> source)
  {
      destination.Clear();
      foreach (var (id, enabled) in source)
          destination[id] = enabled;
  }
  ```
  This is already fine structurally. The real fix is to rename it to something
  like `CopyOverrides` so the name matches the mechanism — "Replace" implies
  swapping the reference, not mutating in place. Minor.

- [x] Verify: `dotnet build` succeeds; `dotnet test` passes.

## Validation

- **Build:** `dotnet build` must succeed after each phase.
- **Tests:** `dotnet test` must pass after each phase with no skips.
- **Grep for stale imports:** After Phase 1, `grep -r "Ur.Tools" src/Ur/Skills/SkillTool.cs`
  should return nothing (no leftover old namespace). After Phase 1,
  `grep -r "using Ur.Skills" src/Ur/Tools/` should return nothing (Tools no
  longer depends on Skills).
- **Manual:** Confirm `UrHost.StartAsync` reads as a clear sequence of named
  steps after Phase 2.

## Impact assessment

- **Code paths affected:** Only internal wiring; no public API changes, no
  behavior changes, no new dependencies.
- **Test impact:** Some tests may need `using` directive updates after
  `SkillTool` moves namespaces. No test logic changes expected.
- **Risk:** Low. Each phase is a self-contained refactoring with build+test
  verification gates.

## Open questions

None — all findings have concrete fixes with clear implementation paths.
