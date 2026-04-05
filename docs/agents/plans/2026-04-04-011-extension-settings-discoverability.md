# Plan: Extension Settings Discoverability & Model List `--all`

Two features that surface existing but hidden information to users.

## Feature 1: Extension Settings Discoverability

**TL;DR**: Extension-defined settings schemas are registered and validated but never shown to users. Add a `ur extensions settings <id>` CLI command and the supporting library plumbing to expose them.

### Steps

**Phase A — Library: Expose schemas through public API**

1. Add `SettingsSchemas` to `ExtensionInfo` — add an `IReadOnlyDictionary<string, JsonElement>` property alongside the existing UI-facing fields (Id, Name, Tier, etc.). Update the constructor and `ExtensionCatalog.ToInfo()` to pass through `extension.SettingsSchemas`.
   - File: `src/Ur/Extensions/ExtensionInfo.cs`
   - File: `src/Ur/Extensions/ExtensionCatalog.cs` (the `ToInfo()` method at the bottom)

2. Add `GetExtensionSettings(string extensionId)` method to `ExtensionCatalog` — returns the `IReadOnlyDictionary<string, JsonElement>` for a given extension ID, or throws `ArgumentException` for unknown IDs. This parallels the existing `SetEnabledAsync`/`ResetAsync` pattern using `GetRequiredExtension()`.
   - File: `src/Ur/Extensions/ExtensionCatalog.cs`

**Phase B — CLI: Add `ur extensions settings <id>` command**

3. Add `BuildSettings()` subcommand in `ExtensionCommands.cs` — takes an `<extension-id>` argument, calls `host.Extensions.List()` to find the extension, then prints each schema key and its JSON schema in a readable format.
   - File: `src/Ur.Cli/Commands/ExtensionCommands.cs`
   - Pattern: follow existing `BuildList()` / `BuildEnable()` structure
   - Output format: for each setting key, print the key name and serialize the schema JSON prettily. If  extension has no settings, print "No settings defined for <id>."
   - Wire into `Build()` via `extensions.Add(BuildSettings())`

### Relevant Files
- `src/Ur/Extensions/ExtensionInfo.cs` — add SettingsSchemas property
- `src/Ur/Extensions/ExtensionCatalog.cs` — update ToInfo(), add GetExtensionSettings()
- `src/Ur.Cli/Commands/ExtensionCommands.cs` — add BuildSettings() subcommand

---

## Feature 2: Model List `--all` Flag

**TL;DR**: `ModelCatalog.Models` already holds the unfiltered catalog, but `UrConfiguration` only exposes `AvailableModels` (filtered to tool-capable). Add an `AllModels` property to `UrConfiguration` and wire the existing `--all` flag in `ModelCommands.cs`.

### Steps

4. Add `AllModels` property to `UrConfiguration` — expose the raw catalog sorted by ID, without the text+tool filtering applied by `AvailableModels`.
   - File: `src/Ur/Configuration/UrConfiguration.cs`
   - Pattern: `public IReadOnlyList<ModelInfo> AllModels => _modelCatalog.Models.OrderBy(m => m.Id, StringComparer.OrdinalIgnoreCase).ToList();`

5. Wire `--all` flag in `ModelCommands.BuildList()` — replace the `_ = parseResult.GetValue(allOpt)` placeholder with a conditional: when `--all` is set, use `host.Configuration.AllModels`; otherwise use `host.Configuration.AvailableModels`. Update the summary line to indicate which view is shown.
   - File: `src/Ur.Cli/Commands/ModelCommands.cs`
   - Remove the "reserved for future use" comment

### Relevant Files
- `src/Ur/Configuration/UrConfiguration.cs` — add AllModels property
- `src/Ur.Cli/Commands/ModelCommands.cs` — wire --all flag (~3 lines changed)

---

## Verification

1. `dotnet build src/Ur/Ur.csproj` — library compiles
2. `dotnet build src/Ur.Cli/Ur.Cli.csproj` — CLI compiles
3. `dotnet test` — existing tests pass (no behavioral changes to existing paths)
4. `make inspect` — read `inspection-results.txt` and fix any issues
5. Manual: `ur extensions settings system:git` — shows settings or "No settings defined"
6. Manual: `ur models list` — same output as before (filtered)
7. Manual: `ur models list --all` — shows all models including non-tool-capable ones

## Decisions

- Settings schemas are included on `ExtensionInfo` directly rather than requiring a separate round-trip, since the data is already loaded at discovery time and is small.
- `AllModels` is a computed property (like `AvailableModels`) to keep the same pattern — no caching needed since the catalog is already in memory.
- No new test files needed — these are thin plumbing additions. Existing extension and model tests cover the underlying data paths.

## Further Considerations

1. **Settings current values**: Should `ur extensions settings <id>` also show the *current value* of each setting alongside the schema? Recommendation: yes, show current value from `host.Configuration.GetSetting(key)` next to each schema entry. This makes the command a one-stop diagnostic tool.
2. **Schema display format**: Should schemas be printed as raw JSON or in a human-readable summary (e.g., "type: boolean, default: false")? Recommendation: print a summary line (key, type, default if present) with a `--json` flag for raw output — but this can be deferred to keep scope tight. Start with pretty-printed JSON.