# Adopt Microsoft.Extensions.Hosting and DependencyInjection

## Goal

Replace Ur's manual dependency wiring with the standard .NET Generic Host and DI container.

## Desired outcome

1. Application-scoped services are registered in `IServiceCollection` and resolved from `IServiceProvider`. The container handles construction ordering automatically.
2. Both entry points (CLI and TUI) boot through `IHost`.
3. Logging uses `ILogger<T>` instead of the static `UrLogger`.
4. Configuration uses `IConfiguration` / `IOptions<T>` instead of the custom `UrConfiguration` facade.

## How we got here

The user wants full adoption: testability, conventional structure, and host features. Deep analysis revealed that the 10-step async initialization in `UrHost.StartAsync()` is a hand-rolled dependency resolver — exactly what DI replaces. The async is gratuitous: every `await` in the startup path is either `File.ReadAllTextAsync`, `JsonSerializer.DeserializeAsync`, `Console.Error.WriteLineAsync`, or `LuaState.DoStringAsync` (CPU-bound Lua evaluation returning `ValueTask`). Making these synchronous lets each service be constructed by a normal DI singleton factory, with the container sorting out ordering through its dependency graph.

Per-session and per-turn objects (UrSession, AgentLoop, ToolRegistry, SubagentRunner, PermissionGrantStore) all need per-call parameters (session ID, chat client, callbacks, system prompt). They stay as procedural construction — DI is for application-scoped singletons only.

## Design decisions

### D1: Make startup methods synchronous

The prerequisite that makes everything else simple. Every async method in the startup path becomes sync:

| Method | Async reason | Sync replacement |
|--------|-------------|-----------------|
| `ExtensionLoader.DiscoverAllAsync()` | `File.ReadAllTextAsync`, `DoStringAsync` | `File.ReadAllText`, `DoStringAsync(...).AsTask().GetAwaiter().GetResult()` (completes synchronously — CPU-bound Lua eval in sandboxed state with no I/O) |
| `ExtensionLoader.ActivateAsync()` | Same | Same |
| `ExtensionCatalog.CreateAsync()` | `overrideStore.LoadAsync()`, `ActivateAsync()` | Sync versions of both |
| `ExtensionOverrideStore.LoadAsync()` | `JsonSerializer.DeserializeAsync`, `Console.Error.WriteLineAsync` | `JsonSerializer.Deserialize`, `Console.Error.WriteLine` |
| `SkillLoader.LoadAllAsync()` | `File.ReadAllTextAsync`, `Console.Error.WriteLineAsync` | `File.ReadAllText`, `Console.Error.WriteLine` |

`ModelCatalog.LoadCache()` and `SettingsLoader.Load()` are already synchronous.

For LuaCSharp's `DoStringAsync`: this is the only API the library exposes, but it returns `ValueTask` and the Lua state is configured with sandboxed no-op I/O (`NoOpFileSystem`, `NoOpStandardIo`). The execution is purely CPU-bound and completes synchronously. Calling `.AsTask().GetAwaiter().GetResult()` is safe here because there's no actual async work to deadlock on.

### D2: How the CLI interacts with the Generic Host

Keep `HostRunner` but have it build and start an `IHost`. All 6 command files remain unchanged — they still receive `(UrHost host, CancellationToken ct)`.

```csharp
internal static class HostRunner
{
    public static async Task<int> RunAsync(
        Func<UrHost, CancellationToken, Task<int>> action,
        CancellationToken ct = default)
    {
        DotEnv.Load(options: new DotEnvOptions(probeForEnv: true, probeLevelsToSearch: 8));

        var builder = Host.CreateApplicationBuilder();
        builder.Services.AddUr(new UrStartupOptions { WorkspacePath = Environment.CurrentDirectory });

        using var app = builder.Build();
        await app.StartAsync(ct);

        try
        {
            return await action(app.Services.GetRequiredService<UrHost>(), ct);
        }
        finally
        {
            await app.StopAsync(ct);
        }
    }
}
```

`ur --help` still skips boot (System.CommandLine handles it before the handler runs).

### D3: How the TUI interacts with the Generic Host

The REPL loop becomes a `BackgroundService`. `stoppingToken` replaces `appCts.Token`.

```csharp
var builder = Host.CreateApplicationBuilder(args);
builder.Services.AddUr(new UrStartupOptions { WorkspacePath = Environment.CurrentDirectory });
builder.Services.AddHostedService<TuiService>();
await builder.Build().RunAsync();
```

`TuiService` injects `UrHost`, `IHostApplicationLifetime`, `ILogger<TuiService>`. The REPL loop moves into `ExecuteAsync`. Ctrl+C triggers the host's console lifetime → `StopApplication()` → cancels `stoppingToken`. Per-turn CTS links to `stoppingToken` (unchanged from current pattern with `appCts`). Viewport cleanup runs in the `finally` block and in the `ProcessExit` handler.

### D4: UrHost survives as a DI-registered singleton

Constructor changes from private to accepting DI-resolved dependencies. `static StartAsync()` is deleted. Public API surface is unchanged: `CreateSession()`, `OpenSessionAsync()`, `ListSessions()`, `Configuration`, `Extensions`, `WorkspacePath`.

### D5: No new interfaces extracted

Tests use real services with temp directories today. They inject test doubles only for `IKeyring` (already an interface) and `IChatClient` (already an interface from M.E.AI). No test mocks `SessionStore`, `ModelCatalog`, `SkillRegistry`, or `ExtensionCatalog`. Register concrete types.

### D6: ToolContext / ToolFactory unchanged

Per-turn tool construction stays procedural. `ToolContext(Workspace, SessionId)` and `delegate AIFunction ToolFactory(ToolContext)` are unaffected by DI.

### D7: Configuration design

**Two `UrSettingsConfigurationProvider` instances** — one for user settings, one for workspace settings. Workspace provider added second (higher priority). This mirrors the current merge semantics via `IConfiguration`'s "last provider wins" rule.

**File format change**: Current settings files use flat dot-namespaced keys (`{"ur.model": "gpt-4"}`). This is changed to nested JSON (`{"ur": {"model": "gpt-4"}}`) to align with `IConfiguration`'s native section model. No dot-to-colon translation layer needed. `IOptions<UrOptions>` binds to `config.GetSection("ur")` directly. Extension settings like `my-extension.debug` become `{"my-extension": {"debug": "true"}}`. The migration is handled during Phase 4 — the `SettingsWriter` writes nested JSON, and a one-time migration converts existing flat files on first load.

**`IOptions<UrOptions>`** for the one core setting:
```csharp
public sealed class UrOptions { public string? ModelId { get; set; } }
```

**Extension settings** stay on `IConfiguration` with string-based access. Dynamic schema validation happens in `SettingsWriter` on writes, not on load.

**Secrets** stay on `IKeyring` (DI-registered singleton). Not in `IConfiguration`.

**`UrConfiguration` survives** as a thin wrapper over `IOptionsMonitor<UrOptions>` + `IKeyring` + `ModelCatalog` + `IConfiguration` + `SettingsWriter`. Same public API. All 30+ production call sites and 60+ test call sites are unchanged.

**Writes** go through a new `SettingsWriter` service that validates against `SettingsSchemaRegistry`, writes the JSON file, and calls `IConfigurationRoot.Reload()`.

### D8: Logging — 5 call sites, 2 files

Custom `UrFileLoggerProvider` writes to `~/.ur/logs/ur-{date}.log` in the existing format. `AgentLoop` gains `ILogger<AgentLoop>` passed from `UrSession` via `ILoggerFactory` on `UrHost`. `TuiService` gets `ILogger<TuiService>` from DI.

## Related code

**Changed:**
- `src/Ur/UrHost.cs` — Private constructor → DI-injectable. `static StartAsync()` deleted.
- `src/Ur/Extensions/ExtensionLoader.cs` — Async methods → sync.
- `src/Ur/Extensions/ExtensionCatalog.cs` — `CreateAsync` → `Create` (sync factory).
- `src/Ur/Extensions/ExtensionOverrideStore.cs` — `LoadAsync` → `Load` (sync).
- `src/Ur/Skills/SkillLoader.cs` — `LoadAllAsync` → `LoadAll` (sync).
- `src/Ur.Cli/HostRunner.cs` — Rebuilt to construct `IHost`.
- `src/Ur.Tui/Program.cs` — Rewritten as `HostApplicationBuilder` + `TuiService : BackgroundService`.
- `src/Ur/Logging/UrLogger.cs` — Deleted, replaced by custom `ILoggerProvider`.
- `src/Ur/AgentLoop/AgentLoop.cs` — Gains `ILogger<AgentLoop>` parameter.
- `src/Ur/Configuration/UrConfiguration.cs` — Rebuilt internals (same public API).
- `src/Ur/Configuration/Settings.cs` — Deleted, replaced by `IConfigurationProvider`.
- `src/Ur/Configuration/SettingsLoader.cs` — Deleted, logic absorbed into provider.
- `tests/Ur.Tests/TestSupport/TestEnvironment.cs` — `StartHostAsync()` rebuilt to use DI.

**Unchanged:**
- `src/Ur.Cli/Program.cs` and all 6 command files in `src/Ur.Cli/Commands/`.
- `src/Ur/Sessions/UrSession.cs`, `src/Ur/AgentLoop/ToolInvoker.cs`, `src/Ur/AgentLoop/SubagentRunner.cs`.
- `src/Ur/Tools/` (ToolContext, ToolFactory, BuiltinToolFactories, all tool implementations).
- `src/Ur/Permissions/` (PermissionGrantStore, PermissionPolicy).
- `src/Ur/Skills/SkillRegistry.cs`, `SkillExpander.cs`, `SystemPromptBuilder.cs`.

## Implementation plan

### Phase 1: Make startup synchronous

Eliminate unnecessary async from the startup path so every service becomes DI-constructible.

- [ ] `ExtensionLoader`: rename `DiscoverAllAsync` → `DiscoverAll`, `ActivateAsync` → `Activate`, `DiscoverTierAsync` → `DiscoverTier`, `EvaluateManifestAsync` → `EvaluateManifest`. Replace `File.ReadAllTextAsync` with `File.ReadAllText`. Replace `Console.Error.WriteLineAsync` with `Console.Error.WriteLine`. For `state.DoStringAsync(...)`, call `.AsTask().GetAwaiter().GetResult()` (safe: CPU-bound Lua, sandboxed no-op I/O).
- [ ] `ExtensionOverrideStore`: rename `LoadAsync` → `Load`. Replace `JsonSerializer.DeserializeAsync` with `JsonSerializer.Deserialize` (sync stream overload or read file to string first). Replace `Console.Error.WriteLineAsync` with `Console.Error.WriteLine`. Keep `WriteGlobalAsync` and `WriteWorkspaceAsync` as async — these are called from user-initiated commands, not startup.
- [ ] `ExtensionCatalog`: rename `CreateAsync` → `Create`. Change to call sync `overrideStore.Load()` and sync `ExtensionLoader.Activate()`.
- [ ] `SkillLoader`: rename `LoadAllAsync` → `LoadAll`, `LoadFromDirectoryAsync` → `LoadFromDirectory`. Replace `File.ReadAllTextAsync` with `File.ReadAllText`, `Console.Error.WriteLineAsync` with `Console.Error.WriteLine`.
- [ ] Update all call sites of the renamed methods (primarily `UrHost.StartAsync()` and tests).
- [ ] Verify all tests pass.

### Phase 2: Bootstrap the Generic Host and DI container

Wire both entry points through `IHost`. Register all services as singletons. Delete `UrHost.StartAsync()`.

- [ ] Add `Microsoft.Extensions.Hosting` to `Ur.Cli.csproj` and `Ur.Tui.csproj`.
- [ ] Add `Microsoft.Extensions.DependencyInjection.Abstractions` to `Ur.csproj`.
- [ ] Create `src/Ur/Hosting/UrStartupOptions.cs`:
  ```csharp
  public sealed class UrStartupOptions
  {
      public required string WorkspacePath { get; init; }
      public string? UserDataDirectory { get; init; }
      public string? UserSettingsPath { get; init; }
      public string? SystemExtensionsPath { get; init; }
      public string? UserExtensionsPath { get; init; }
      public IKeyring? KeyringOverride { get; init; }
      public Func<string, IChatClient>? ChatClientFactoryOverride { get; init; }
      public ToolRegistry? AdditionalTools { get; init; }
  }
  ```
- [ ] Create `src/Ur/Hosting/ServiceCollectionExtensions.cs` with `AddUr(this IServiceCollection, UrStartupOptions)`. Register all services as singletons via factory delegates. The container resolves them in dependency order:
  ```csharp
  public static IServiceCollection AddUr(this IServiceCollection services, UrStartupOptions options)
  {
      services.AddSingleton(options);
      services.AddSingleton(sp => {
          var w = new Workspace(options.WorkspacePath);
          w.EnsureDirectories();
          return w;
      });
      services.AddSingleton<IKeyring>(sp =>
          options.KeyringOverride ?? CreatePlatformKeyring());
      services.AddSingleton(sp => {
          var dir = options.UserDataDirectory ?? DefaultUserDataDirectory();
          var catalog = new ModelCatalog(Path.Combine(dir, "cache"));
          catalog.LoadCache();
          return catalog;
      });
      services.AddSingleton(sp =>
          new SessionStore(sp.GetRequiredService<Workspace>().SessionsDirectory));

      // Extension discovery produces the extension list.
      // Schema registry and catalog both depend on it.
      services.AddSingleton(sp => {
          var workspace = sp.GetRequiredService<Workspace>();
          var dir = options.UserDataDirectory ?? DefaultUserDataDirectory();
          return ExtensionLoader.DiscoverAll(
              options.SystemExtensionsPath ?? DefaultSystemExtensionsPath(dir),
              options.UserExtensionsPath ?? DefaultUserExtensionsPath(dir),
              workspace.ExtensionsDirectory);
      });
      services.AddSingleton(sp => {
          var registry = new SettingsSchemaRegistry();
          RegisterCoreSchemas(registry);
          foreach (var ext in sp.GetRequiredService<List<Extension>>())
              RegisterExtensionSchemas(registry, ext);
          return registry;
      });
      services.AddSingleton(sp => {
          var workspace = sp.GetRequiredService<Workspace>();
          var dir = options.UserDataDirectory ?? DefaultUserDataDirectory();
          var loader = new SettingsLoader(sp.GetRequiredService<SettingsSchemaRegistry>());
          return loader.Load(
              options.UserSettingsPath ?? DefaultUserSettingsPath(dir),
              workspace.SettingsPath);
      });
      services.AddSingleton(sp => {
          var workspace = sp.GetRequiredService<Workspace>();
          var dir = options.UserDataDirectory ?? DefaultUserDataDirectory();
          var overrideStore = new ExtensionOverrideStore(dir, workspace);
          return ExtensionCatalog.Create(
              sp.GetRequiredService<List<Extension>>(), overrideStore);
      });
      services.AddSingleton(sp => {
          var workspace = sp.GetRequiredService<Workspace>();
          var dir = options.UserDataDirectory ?? DefaultUserDataDirectory();
          var skills = SkillLoader.LoadAll(
              Path.Combine(dir, "skills"), workspace.SkillsDirectory);
          return new SkillRegistry(skills);
      });
      services.AddSingleton(sp => new UrConfiguration(
          sp.GetRequiredService<ModelCatalog>(),
          sp.GetRequiredService<Settings>(),
          sp.GetRequiredService<IKeyring>()));
      services.AddSingleton<UrHost>();  // all deps via constructor injection

      return services;
  }
  ```
  No `IHostedService`. No `TaskCompletionSource`. Plain singleton factories.
- [ ] Change `UrHost` constructor from `private` to `public`/`internal`. Accept all dependencies as constructor parameters. Delete the `static StartAsync()` overloads. Move the static helper methods (`RegisterCoreSchemas`, `RegisterExtensionSchemas`, `CreatePlatformKeyring`, `Default*Path`) into `ServiceCollectionExtensions` or a shared `UrDefaults` helper.
- [ ] Rewrite `src/Ur.Cli/HostRunner.cs` per D2 above.
- [ ] Rewrite `src/Ur.Tui/Program.cs` per D3 above: `HostApplicationBuilder` + `TuiService : BackgroundService`.
- [ ] Update `TempExtensionEnvironment.StartHostAsync()` and test helper methods to build a container:
  ```csharp
  public async Task<UrHost> StartHostAsync(IKeyring? keyring = null, ...)
  {
      var builder = Host.CreateApplicationBuilder();
      builder.Services.AddUr(new UrStartupOptions {
          WorkspacePath = WorkspacePath,
          UserDataDirectory = UserDataDirectory,
          UserSettingsPath = UserSettingsPath,
          SystemExtensionsPath = SystemExtensionsPath,
          UserExtensionsPath = UserExtensionsPath,
          KeyringOverride = keyring ?? new TestKeyring(),
          ChatClientFactoryOverride = chatClientFactory,
      });
      _host = builder.Build();
      await _host.StartAsync();  // no-op since no hosted services, but starts DI
      return _host.Services.GetRequiredService<UrHost>();
  }
  ```
- [ ] Update all test files. Verify all tests pass.
- [ ] Verify AoT publishing of Ur.Tui: `dotnet publish src/Ur.Tui -c Release`. If AoT fails with Generic Host, fall back to bare `ServiceCollection` for the TUI.

### Phase 3: Migrate logging to ILogger<T>

5 call sites, 2 files.

- [ ] Add `Microsoft.Extensions.Logging.Abstractions` to `Ur.csproj`.
- [ ] Create `src/Ur/Logging/UrFileLoggerProvider.cs` — custom `ILoggerProvider` that creates `UrFileLogger` instances.
- [ ] Create `src/Ur/Logging/UrFileLogger.cs` — custom `ILogger` that writes to `~/.ur/logs/ur-{date}.log`. Same format, daily-rolling, thread-safe, fire-and-forget as current `UrLogger`.
- [ ] Register in `AddUr()`: `services.AddLogging(b => b.AddProvider(new UrFileLoggerProvider()))`.
- [ ] Add `ILoggerFactory` parameter to `UrHost`. Store it. Pass to `UrSession` which creates `ILogger<AgentLoop>` per-turn.
- [ ] Add `ILogger<AgentLoop>` parameter to `AgentLoop`. Replace `UrLogger.Exception("LLM streaming error", ex)` at line 174 with `logger.LogError(ex, "LLM streaming error")`.
- [ ] In `TuiService`, inject `ILogger<TuiService>`. Replace 4 `UrLogger.*` calls.
- [ ] Delete `src/Ur/Logging/UrLogger.cs`.
- [ ] Verify log output by running TUI and checking `~/.ur/logs/`.

### Phase 4: Migrate configuration to IConfiguration / IOptions<T>

- [ ] Add `Microsoft.Extensions.Configuration` and `Microsoft.Extensions.Options` to `Ur.csproj`.
- [ ] Create `src/Ur/Configuration/UrOptions.cs`: `public sealed class UrOptions { public string? ModelId { get; set; } }`
- [ ] Create `src/Ur/Configuration/UrSettingsConfigurationProvider.cs` — reads a single nested-JSON `settings.json` file into `Data`. Keys map directly to `IConfiguration`'s colon-separated model (e.g., `{"ur": {"model": "gpt-4"}}` → `ur:model`).
- [ ] Create `src/Ur/Configuration/UrSettingsConfigurationSource.cs` wrapping the provider.
- [ ] Register both providers in `AddUr()` (user file first, workspace file second for override semantics). Bind `IOptions<UrOptions>` to `config.GetSection("ur")`.
- [ ] Create `src/Ur/Configuration/SettingsWriter.cs` — validates against `SettingsSchemaRegistry`, writes nested JSON to the correct file, calls `IConfigurationRoot.Reload()`.
- [ ] Add a one-time migration in the configuration provider's `Load()`: if the settings file contains flat dot-namespaced keys (e.g., `"ur.model"`), convert to nested format (`{"ur": {"model": ...}}`) and rewrite the file. This handles existing installations transparently.
- [ ] Rebuild `UrConfiguration` internals to use `IOptionsMonitor<UrOptions>` + `IKeyring` + `ModelCatalog` + `IConfiguration` + `SettingsWriter`. Public API unchanged.
- [ ] Delete `src/Ur/Configuration/Settings.cs` and `src/Ur/Configuration/SettingsLoader.cs`.
- [ ] Update `AddUr()` to register the configuration pipeline instead of `Settings`/`SettingsLoader`.
- [ ] Verify all tests pass. Verify `ur config get/set/clear` round-trips correctly.

## Validation

- **Build**: `dotnet build` for all projects after each phase.
- **Tests**: All existing tests must pass after each phase.
- **AoT**: `dotnet publish src/Ur.Tui -c Release` after Phase 2.
- **Manual**: After Phase 2: run `ur status` and the TUI. After Phase 3: check `~/.ur/logs/`. After Phase 4: verify `ur config set/get/clear`.

