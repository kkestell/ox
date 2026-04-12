# Idiomatic .NET Generic Host & DI for Ur

## Goal

Make Ur a textbook idiomatic .NET library in its usage of the Generic Host and Dependency Injection. Every public interface should be overridable via standard DI. The host application (Ox) owns logging, configuration sources, and application-level concerns like the model catalog. Ur owns the agent loop, session management, and provider abstraction.

## Desired outcome

- `AddUr()` is a thin convenience that registers Ur's internal service graph with sensible defaults. It does not touch logging, does not construct `IConfigurationRoot`, and does not hold test escape hatches.
- Every extension point (`IProvider`, `ISessionStore`, `ICompactionStrategy`, `IKeyring`) is a public interface overridable via normal DI registration.
- Ox's `Program.cs` uses `Host.CreateApplicationBuilder()` and owns logging configuration, configuration sources, and provider construction from `providers.json`.
- `UrStartupOptions` is deleted entirely. Its remaining config values (`WorkspacePath`, `UserDataDirectory`, `UserSettingsPath`, `SelectedModelOverride`) merge into `UrOptions` and are configured via the standard `Configure<UrOptions>` pipeline. Test overrides use direct DI registration.
- `UrOptionsMonitor` is deleted; the standard `Configure<T>` + `IOptionsMonitor<T>` pipeline is used.
- `ProviderConfig` (the providers.json loader) and model catalog concerns move entirely to Ox.
- Ur's provider interface (`IProvider`) knows nothing about model catalogs — it creates chat clients, reports readiness, and that's it (same as today).
- All existing tests pass after the refactoring with updated DI setup.

## How we got here

We reviewed the 13 issues documented in `host-di-issues.md` and grouped them into a coherent refactoring plan. Key decisions from brainstorming:

1. **`AddUr()` stays but gets thin.** It registers Ur's internal object graph with defaults. The host can override any public interface before or after calling it. It does not own logging or configuration construction.
2. **Provider extensibility via standard DI.** `IProvider` becomes public. External consumers register `services.AddSingleton<IProvider, MyProvider>()`. The providers.json type-switch moves to Ox.
3. **`ISessionStore` at message-level abstraction.** The compact-boundary sentinel is an implementation detail of the JSONL backend. The interface exposes `ReplaceAllAsync` instead, letting backends choose their own compaction persistence strategy.
4. **`ICompactionStrategy` owns the full decision.** The strategy decides whether to compact (threshold check) and how (summarization). The caller just invokes it every turn and acts on the boolean.
5. **Ur doesn't know about model catalogs.** `ProviderConfig`, model listing, and context window resolution move to Ox. Ur receives context window information via a delegate/interface from the host.
6. **`UrConfiguration` splits.** Library-level concerns (selected model, readiness, settings, keyring) stay in Ur. Application-level concerns (model listing, provider catalog) move to Ox.

## Related code

### Ur (library) — files being modified

- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Main DI registration. Loses logging config, configuration construction, provider-type switch, test overrides. Gains `IConfiguration` parameter.
- `src/Ur/Hosting/UrHost.cs` — Loses `ProviderConfig` dependency, `ResolveContextWindow`, `_chatClientFactoryOverride`, `_additionalTools`, `UrStartupOptions` parameter. Gains cleaner constructor via DI interfaces and `IOptions<UrOptions>`.
- `src/Ur/Hosting/UrStartupOptions.cs` — Deleted. Remaining config values (`WorkspacePath`, `UserDataDirectory`, `UserSettingsPath`, `SelectedModelOverride`) merge into `UrOptions` and are configured via the standard `Configure<UrOptions>` pipeline.
- `src/Ur/Configuration/UrOptionsMonitor.cs` — Deleted. Replaced by standard options pipeline.
- `src/Ur/Configuration/UrConfiguration.cs` — Loses model catalog methods (`ListAllModelIds`, `ListProviders`, `ListModelsForProvider`, `ProviderRequiresApiKey`). Keeps settings, keyring, readiness, selected model.
- `src/Ur/Providers/IProvider.cs` — Changes from `internal` to `public`.
- `src/Ur/Providers/ProviderRegistry.cs` — Stays internal. Populated from `IProvider` DI registrations.
- `src/Ur/Providers/ProviderConfig.cs` — Moves to Ox entirely.
- `src/Ur/Sessions/SessionStore.cs` — Renamed to `JsonlSessionStore`, implements new `ISessionStore`.
- `src/Ur/Sessions/UrSession.cs` — Consumes `ISessionStore` and `ICompactionStrategy` instead of concrete types. Compaction persistence uses `ISessionStore.ReplaceAllAsync`.
- `src/Ur/Compaction/Autocompactor.cs` — Becomes non-static, implements `ICompactionStrategy`. Registered in DI.
- `src/Ur/Logging/UrFileLoggerProvider.cs` — Must become `public` so Ox can register it.

### Ox (application) — files being modified

- `src/Ox/Program.cs` — Switches to `Host.CreateApplicationBuilder()`. Owns logging, configuration sources, provider construction.
- `src/Ox/Ox.csproj` — Adds `Microsoft.Extensions.Hosting` package reference.
- `src/Ox/OxApp.cs` — Model catalog queries move from `UrConfiguration` to Ox-level configuration.

### Ox (application) — new files

- `src/Ox/Configuration/ProviderConfig.cs` — Moved from Ur. Loads and validates providers.json.
- `src/Ox/Configuration/OxConfiguration.cs` — Application-level configuration facade. Wraps model catalog queries, context window resolution, provider listing for the TUI.

### Tests

- `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` — Updates to new DI pattern: direct service registration instead of `UrStartupOptions` overrides.
- `tests/Ur.Tests/TestSupport/TestEnvironment.cs` — `TestProviderConfig` may need adjustment since `ProviderConfig` moves to Ox.

### Documentation and evals

- `docs/development/adding-llm-providers.md` — Significantly stale (references methods that no longer exist). Needs rewrite to reflect Ox-centric provider registration and providers.json-driven model catalog.
- `docs/development/evals.md` — Verify eval container startup still works with new DI patterns. Eval runner itself is unaffected (doesn't reference `ProviderConfig`).
- `Makefile` — Verify `make test` and `make evals-build` pass after refactoring.
- `src/Ox/Program.cs:77` — Error message references `docs/settings.md` which doesn't exist.

## Current state

- **Existing behavior:** Ur's `AddUr()` is a monolith that owns logging, configuration, provider construction, and test overrides. External consumers cannot implement custom providers, session stores, or compaction strategies because the interfaces are internal.
- **Existing patterns:** DI factory delegates for singleton registration. Procedural construction for per-session objects. Configuration via `UrSettingsConfigurationSource` (user + workspace layers).
- **Test infrastructure:** `TestHostBuilder` already uses `Host.CreateApplicationBuilder()`. Tests inject fakes via `UrStartupOptions` overrides.

## Structural considerations

### Hierarchy

The refactoring clarifies the layer boundary between Ur (library) and Ox (application):
- **Ur** provides interfaces (`IProvider`, `ISessionStore`, `ICompactionStrategy`) and default implementations. It consumes `IConfiguration` but doesn't create it.
- **Ox** owns application configuration (providers.json, logging, Generic Host lifecycle) and registers concrete implementations.
- Dependencies flow strictly downward: Ox → Ur → Te. No upward references.

### Abstraction

Each new interface is at the right level:
- `IProvider` — provider-level (create client, check readiness). No model catalog knowledge.
- `ISessionStore` — message-level (append, read, replace). No JSONL-specific concepts.
- `ICompactionStrategy` — full compaction decision (should I compact? do it). Caller just acts on the boolean.

### Encapsulation

- `ProviderRegistry` stays internal — it's an implementation detail of how Ur collects providers.
- `JsonlSessionStore` implementation details (sentinels, append-only format) stay behind `ISessionStore`.
- Concrete provider implementations (`OpenAiCompatibleProvider`, `GoogleProvider`, `OllamaProvider`) stay `internal` to Ur. Ox constructs them via `InternalsVisibleTo("Ox")`. External consumers implement `IProvider` from scratch — Ur doesn't commit to maintaining concrete provider constructor signatures as public API.

### Risk: UrConfiguration split

The split of `UrConfiguration` into library-level (Ur) and application-level (Ox) concerns is the most complex structural change. The risk is that OxApp currently accesses everything through `UrHost.Configuration`. After the split, OxApp needs to access both `UrConfiguration` (library) and `OxConfiguration` (application). This is a clean separation but requires updating all callsites in OxApp.

## Implementation plan

The plan is organized into phases. Each phase should compile and tests should pass before moving to the next. This ordering minimizes risk by doing interface extractions first, then behavioral changes.

### Phase 1: Extract public interfaces (no behavioral change)

These are pure additive changes — extract interfaces from existing concrete types, register them in DI, update consumers to depend on the interface.

- [ ] **1.1 Make `IProvider` public.** Change `internal interface IProvider` to `public interface IProvider` in `src/Ur/Providers/IProvider.cs`. Concrete provider classes (`OpenAiCompatibleProvider`, `GoogleProvider`, `OllamaProvider`) stay `internal` — Ox already has `InternalsVisibleTo("Ox")` in `src/Ur/Properties/AssemblyInfo.cs`, so it can construct them. External third-party consumers implement `IProvider` from scratch. This avoids committing Ur to maintaining concrete provider constructor signatures as public API.
- [ ] **1.2 Extract `ISessionStore` interface.** Create `src/Ur/Sessions/ISessionStore.cs` with:
  ```csharp
  public interface ISessionStore
  {
      Session Create();
      IReadOnlyList<Session> List();
      Session? Get(string id);
      Task AppendAsync(Session session, ChatMessage message, CancellationToken ct = default);
      Task<IReadOnlyList<ChatMessage>> ReadAllAsync(Session session, CancellationToken ct = default);
      Task ReplaceAllAsync(Session session, IReadOnlyList<ChatMessage> messages, CancellationToken ct = default);
      Task WriteMetricsAsync(Session session, SessionMetrics metrics, CancellationToken ct = default);
  }
  ```
  Rename `SessionStore` to `JsonlSessionStore`. Implement `ISessionStore`. The `ReplaceAllAsync` contract: after this call, `ReadAllAsync` returns exactly the provided messages. The JSONL implementation achieves this by writing a compact boundary sentinel followed by the new messages (append-only on disk), but that's an implementation detail. Document the interface contract clearly: "After `ReplaceAllAsync` completes, `ReadAllAsync` returns exactly `messages`." Remove `AppendCompactBoundaryAsync` from the public surface — it becomes a private implementation detail of `ReplaceAllAsync`. Register as `services.AddSingleton<ISessionStore, JsonlSessionStore>()`.
- [ ] **1.3 Extract `ICompactionStrategy` interface.** Create `src/Ur/Compaction/ICompactionStrategy.cs`:
  ```csharp
  public interface ICompactionStrategy
  {
      Task<bool> TryCompactAsync(
          List<ChatMessage> messages,
          IChatClient chatClient,
          int contextWindow,
          long lastInputTokens,
          CancellationToken ct);
  }
  ```
  Note: `ILogger` is NOT a parameter — it's constructor-injected via DI. The strategy is a singleton so the logger lives for the app's lifetime. `IChatClient` stays as a parameter because it varies per-session while the strategy is a singleton.
  Make `Autocompactor` non-static, implement `ICompactionStrategy`. Constructor takes `ILogger<Autocompactor>`. Register as `services.AddSingleton<ICompactionStrategy, Autocompactor>()`.
- [ ] **1.4 Update `UrSession` to consume interfaces.** Change `UrSession` to depend on `ISessionStore` and `ICompactionStrategy` instead of `SessionStore` and `static Autocompactor`. After compaction, call `ISessionStore.ReplaceAllAsync(session, messages)` instead of the boundary-then-append pattern. The `ICompactionStrategy` is injected (likely passed from `UrHost` which gets it from DI).
- [ ] **1.5 Update `UrHost` to consume `ISessionStore`.** Replace `SessionStore` field with `ISessionStore`. Update `CreateSession` and `OpenSessionAsync` to pass the `ICompactionStrategy` to `UrSession`.
- [ ] **1.6 Update `AddUr()` to register interfaces.** Register `ISessionStore` → `JsonlSessionStore` and `ICompactionStrategy` → `Autocompactor` as singletons.
- [ ] **1.7 Verify.** Build, run all tests. No behavioral change — just interface extraction.

### Phase 2: Fix hosting and options patterns

- [ ] **2.1 Replace `UrOptionsMonitor` with standard options pipeline.** In `AddUr()`, replace:
  ```csharp
  services.AddSingleton<IOptionsMonitor<UrOptions>>(sp =>
      new UrOptionsMonitor(sp.GetRequiredService<IConfiguration>()));
  ```
  with:
  ```csharp
  services.Configure<UrOptions>(configuration.GetSection("ur"));
  ```
  This requires `AddUr()` to accept `IConfiguration` as a parameter (done in 2.3). Delete `src/Ur/Configuration/UrOptionsMonitor.cs`. The standard `IOptionsMonitor<UrOptions>` implementation from `Microsoft.Extensions.Options` handles change notifications via the configuration reload token. Verify that `SettingsWriter.Reload()` still propagates changes through the pipeline — it calls `IConfigurationRoot.Reload()`, which fires change tokens, which the standard `OptionsMonitor<T>` listens to.
  **Binding verification:** `UrSettingsConfigurationSource` stores keys as `"ur:model"` and `"ur:turnsToKeepToolResults"`. The call `configuration.GetSection("ur")` returns a section where `section["model"]` and `section["turnsToKeepToolResults"]` resolve those keys. `Configure<UrOptions>(section)` uses `ConfigurationBinder.Bind()`, which matches property names case-insensitively to configuration keys: `Model` → `"model"`, `TurnsToKeepToolResults` → `"turnsToKeepToolResults"`. Both match. Default values (`TurnsToKeepToolResults = 3`) are preserved when keys are absent, since `Bind()` only overwrites properties with matching keys. No `IConfigureOptions<T>` fallback needed.
- [ ] **2.2 Make `UrFileLoggerProvider` public.** Change its accessibility so Ox can register it in its logging pipeline.
- [ ] **2.3 Change `AddUr()` signature to accept `IConfiguration`.** New signature:
  ```csharp
  public static IServiceCollection AddUr(
      this IServiceCollection services,
      IConfiguration configuration,
      Action<UrOptions>? configure = null)
  ```
  The `Action<UrOptions>` callback lets hosts set values that don't come from config files (e.g., `WorkspacePath` from `Directory.GetCurrentDirectory()`, `SelectedModelOverride` from CLI args). These are applied after `Configure<UrOptions>(section)` binds from `IConfiguration`, so code overrides win. The key change: `AddUr()` consumes `IConfiguration`, it does not build it.
- [ ] **2.4 Remove logging configuration from `AddUr()`.** Delete the `services.AddLogging(builder => { builder.ClearProviders(); ... })` block. Logging is the host's responsibility.
- [ ] **2.5 Remove `IConfigurationRoot` construction from `AddUr()`.** Delete the factory delegate that builds `ConfigurationBuilder` with `UrSettingsConfigurationSource`. The host provides `IConfiguration`. `SettingsWriter` depends on `IConfigurationRoot` (to call `Reload()`). `Host.CreateApplicationBuilder()` automatically registers `IConfigurationRoot` in the DI container — `SettingsWriter` resolves it from DI and calls `Reload()` on the *same root* that the host's configuration sources are registered on. This is critical: `Reload()` only propagates to sources on that specific root.
  For the `UrSettingsConfigurationSource` instances (user + workspace settings files), provide a public helper method:
  ```csharp
  public static IConfigurationBuilder AddUrSettings(
      this IConfigurationBuilder builder,
      string userSettingsPath,
      string workspaceSettingsPath)
  ```
  `UrSettingsConfigurationSource` stays `internal` — only the `AddUrSettings` extension method constructs it, so it doesn't need to be public. Ox calls `builder.Configuration.AddUrSettings(...)` before `builder.Services.AddUr(...)`. This ensures the settings sources are part of the host's configuration root, so `SettingsWriter.Reload()` propagates changes through the standard options pipeline.
- [ ] **2.6 Defer `ProviderConfig.Load()` into a factory delegate.** Currently called synchronously at registration time (line 139). Move into `services.AddSingleton(sp => ProviderConfig.Load(path))` so file I/O happens at first resolution, not during registration. (This is a temporary step — ProviderConfig moves to Ox in Phase 4, but deferring now is a quick improvement.)
- [ ] **2.7 Switch Ox's `Program.cs` to `Host.CreateApplicationBuilder()`.** Replace:
  ```csharp
  var services = new ServiceCollection();
  services.AddUr(startupOptions);
  using var sp = services.BuildServiceProvider();
  ```
  with:
  ```csharp
  var builder = Host.CreateApplicationBuilder(args);
  builder.Logging.ClearProviders();
  builder.Logging.AddProvider(new UrFileLoggerProvider());
  builder.Configuration.AddUrSettings(userSettingsPath, workspaceSettingsPath);
  builder.Services.AddUr(builder.Configuration, options => { ... });
  using var host = builder.Build();
  await host.StartAsync();
  var urHost = host.Services.GetRequiredService<UrHost>();
  ```
  Add `Microsoft.Extensions.Hosting` package reference to `Ox.csproj`. Update the headless and TUI paths to use `host.StopAsync()` for graceful shutdown.
- [ ] **2.8 Verify.** Build, run all tests. The logging and configuration ownership has shifted to the host.

### Phase 3: Delete `UrStartupOptions`

`UrStartupOptions` existed because the old `AddUr()` needed values before the DI container was built. With the Generic Host pattern, `Configure<UrOptions>` handles that natively. The remaining config values merge into `UrOptions`; test overrides become direct DI registrations.

- [ ] **3.1 Merge remaining config values into `UrOptions`.** Add `WorkspacePath`, `UserDataDirectory`, `UserSettingsPath`, and `SelectedModelOverride` to `UrOptions`. These are set via the `Action<UrOptions>` callback in `AddUr()`:
  ```csharp
  builder.Services.AddUr(builder.Configuration, o =>
  {
      o.WorkspacePath = Directory.GetCurrentDirectory();
      o.SelectedModelOverride = bootOptions.ModelOverride;
  });
  ```
  `Configure<UrOptions>(section)` binds file-based settings (`Model`, `TurnsToKeepToolResults`), then the `Action<UrOptions>` callback sets runtime values. Code overrides win.
- [ ] **3.2 Move `KeyringOverride` to direct DI registration.** Tests register `IKeyring` directly:
  ```csharp
  services.AddSingleton<IKeyring>(new TestKeyring());
  ```
  `AddUr()` registers the platform keyring via `services.TryAddSingleton<IKeyring>(CreatePlatformKeyring())` so pre-registered keyrings win.
- [ ] **3.3 Move `ChatClientFactoryOverride` to DI.** This is a test escape hatch. Tests should instead register a fake `IProvider` that returns the desired `IChatClient`. The fake provider pattern already exists (`FakeProvider`). Remove the override check from `UrHost.CreateChatClient()`.
- [ ] **3.4 Move `AdditionalTools` to DI.** `ToolRegistry` is per-session (constructed procedurally in `UrSession`), so there's no natural DI seam for the registry itself. However, the *additional tools* are a fixed set injected at startup — they don't change per-session. Solution: register a `Func<IEnumerable<AITool>>` (or a simple marker type like `AdditionalToolSource`) as a DI singleton. `UrHost.CreateSession` resolves it and merges the tools into the per-session `ToolRegistry`. Tests register their fake tools via this DI service. If no additional tools are registered, the default is an empty collection.
- [ ] **3.5 Move `FakeProvider` to direct DI registration.** Tests register fake providers directly via `services.AddSingleton<IProvider>(new FakeProvider())`. Ox's `--fake-provider` flag does the same. `FakeProvider` should be `public` so test consumers can construct it.
- [ ] **3.6 Delete `UrStartupOptions`.** All values are now on `UrOptions` or handled via direct DI registration. Delete `src/Ur/Hosting/UrStartupOptions.cs`. Remove all references.
- [ ] **3.7 Update `TestHostBuilder` to use direct DI registration.** Replace:
  ```csharp
  builder.Services.AddUr(new UrStartupOptions {
      KeyringOverride = keyring ?? new TestKeyring(),
      ChatClientFactoryOverride = chatClientFactory,
      ...
  });
  ```
  with:
  ```csharp
  builder.Services.AddSingleton<IKeyring>(keyring ?? new TestKeyring());
  if (fakeProvider is not null)
      builder.Services.AddSingleton<IProvider>(fakeProvider);
  builder.Services.AddUr(builder.Configuration, o =>
  {
      o.WorkspacePath = workspace.WorkspacePath;
      o.UserDataDirectory = workspace.UserDataDirectory;
  });
  ```
- [ ] **3.8 Verify.** Build, run all tests. `UrStartupOptions` is gone.

### Phase 4: Move model catalog to Ox

- [ ] **4.1 Move `ProviderConfig` to Ox.** Move `src/Ur/Providers/ProviderConfig.cs` to `src/Ox/Configuration/ProviderConfig.cs`. Update namespace. This class loads and validates providers.json — it's Ox's application configuration.
- [ ] **4.2 Move the provider-type switch to Ox.** The `foreach/switch` block in `AddUr()` that instantiates providers from `ProviderConfig` entries moves to Ox's `Program.cs` (or a helper method). Ox reads `ProviderConfig`, constructs providers, and registers them as `IProvider` singletons. `AddUr()` no longer knows about providers.json or provider types.
- [ ] **4.3 Create `OxConfiguration` in Ox.** Create `src/Ox/Configuration/OxConfiguration.cs` — an application-level configuration facade. Constructor takes `ProviderConfig` and `IEnumerable<IProvider>` (all registered providers from DI). Provides:
  - `ListAllModelIds()` — all "provider/model" strings for autocomplete
  - `ListProviders()` — `(key, displayName)` pairs for wizard
  - `ListModelsForProvider(key)` — `(id, name)` pairs for wizard
  - `ProviderRequiresApiKey(key)` — delegates to the matching `IProvider` from DI
  - `ResolveContextWindow(modelId)` — first checks `ProviderConfig` (covers all providers.json entries), then falls back to the `IProvider` instance (covers `FakeProvider` which declares context windows on its scenarios). This two-step resolution preserves the existing behavior from `UrHost.ResolveContextWindow`.
  Register as a singleton in Ox's DI setup.
- [ ] **4.4 Remove model catalog methods from `UrConfiguration`.** Delete `ListAllModelIds`, `ListProviders`, `ListModelsForProvider`, `ProviderRequiresApiKey` from `UrConfiguration`. These are now on `OxConfiguration`.
- [ ] **4.5 Remove `ProviderConfig` dependency from `UrHost`.** `UrHost` no longer needs `ProviderConfig` or `ResolveContextWindow`. The context window resolver is provided by the host when creating sessions. Currently `ResolveContextWindow` is already passed as a `Func<string, int?>` delegate to `UrSession`. Change `UrHost.CreateSession` to accept the resolver as a parameter (or have Ox pass it). The `UrHost.ResolveContextWindow` public method is removed — Ox's `OxConfiguration.ResolveContextWindow` replaces it.
- [ ] **4.6 Update `OxApp` to use `OxConfiguration`.** Replace `host.Configuration.ListAllModelIds()` etc. with `oxConfig.ListAllModelIds()`. `OxApp` now takes both `UrHost` (for sessions) and `OxConfiguration` (for model catalog).
- [ ] **4.7 Update `HeadlessRunner` if needed.** Check if it uses any model catalog methods — it likely only uses `UrHost.CreateSession()`, which should still work.
- [ ] **4.8 Update test infrastructure.** `TestProviderConfig` stays in the test project but now constructs an Ox-level `ProviderConfig`. Tests that need model metadata register it through the Ox layer.
- [ ] **4.9 Remove `ProvidersJsonPath` from `UrOptions`.** This was needed because Ur loaded providers.json. Now Ox does, so the path is Ox's concern — it's just a local variable in Ox's `Program.cs`.
- [ ] **4.10 Verify.** Build, run all tests. Ur no longer knows about providers.json or model catalogs.

### Phase 5: Update documentation, evals, and remaining cleanup

- [ ] **5.1 Rewrite `docs/development/adding-llm-providers.md`.** This guide is significantly affected:
  - Step 2 ("Register in DI") changes: providers are now registered in Ox (via `providers.json` for built-in types, or `services.AddSingleton<IProvider, MyProvider>()` for custom providers), not in `ServiceCollectionExtensions.AddUr()`.
  - The `IProvider` interface reference table needs updating — `GetContextWindowAsync` and `ListModelIdsAsync` are already gone from the current interface (the doc is stale). After this refactoring, context windows come from `providers.json` (Ox), not the provider.
  - The "Context Window Resolution" and "Model Discovery" sections are obsolete — both are now driven by `providers.json`.
  - The "Architecture Overview" section needs updating: `UrHost.CreateChatClient` still parses `ModelId` and dispatches to the registry, but the registration path changed.
  - The checklist at the bottom needs updating to reflect the Ox-centric registration.
- [ ] **5.2 Update `docs/development/evals.md` if needed.** The eval runner doesn't reference `ProviderConfig` directly — it passes the `providers.json` path to the container, which runs the Ox binary. Since the JSON format is unchanged and the Ox binary handles DI internally, the eval docs should be stable. Verify the container startup path still works: the container runs `Ox --headless --prompt ...`, which now uses `Host.CreateApplicationBuilder()` internally. Check that environment variable handling (`UR_API_KEY_{PROVIDER}`) and the `EnvironmentKeyring` still work in the headless path.
- [ ] **5.3 Fix stale error message in `Program.cs`.** Line 77 references `docs/settings.md` which does not exist. Update to reference `docs/providers.md` or remove the reference.
- [ ] **5.4 Verify Makefile targets.** Run `make test` and `make evals-build` to confirm nothing broke. The Makefile targets shell out to `dotnet` and `podman` — they don't reference DI directly, but the underlying binaries use the new DI patterns.
- [ ] **5.5 Make `UrHost` constructor public (or keep internal).** Issue #11: decide whether `UrHost` should support construction outside DI. Recommendation: keep internal constructor. DI-only creation is the intended pattern and `AddUr()` is the entry point. No change needed.
- [ ] **5.6 Verify `Ox.csproj` has explicit `Microsoft.Extensions.Hosting` reference.** Should already be done in 2.7. Verify it's not relying on transitive references.
- [ ] **5.7 Final cleanup.** Remove any dead code, unused `using` statements, or orphaned files from the refactoring (including `UrStartupOptions.cs`, `UrOptionsMonitor.cs`, the old `ProviderConfig.cs` location).
- [ ] **5.8 Full validation pass.** Build the entire solution. Run all tests. Manually test Ox TUI: verify the connect wizard works, model selection works, compaction works, headless mode works. Run `make evals-build` to verify the container builds.

## Impact assessment

- **Code paths affected:** Every file that touches DI setup, configuration, provider construction, session storage, or compaction. This is a pervasive refactoring.
- **API impact:** `AddUr()` signature changes. `UrStartupOptions` is deleted (values merge into `UrOptions`). `UrConfiguration` loses model catalog methods. `UrHost.ResolveContextWindow` is removed. All provider, session store, and compaction interfaces become public.
- **Test impact:** `TestHostBuilder` changes significantly. Tests that use `UrStartupOptions` overrides need to switch to direct DI registration. Behavioral test outcomes should be identical.
- **No data/schema impact:** Session JSONL format, settings.json format, providers.json format — all unchanged.

## Validation

- **Tests:** All existing tests must pass after each phase. No new tests are required by this refactoring (existing coverage exercises the same code paths through the new interfaces). If any test relies on `UrStartupOptions` test overrides, update it to use direct DI registration.
- **Build:** `dotnet build` succeeds with no warnings related to the changes.
- **Manual verification:** After completion, run `Ox` in TUI mode: verify the connect wizard, model selection, context percentage display, and compaction all work. Run in headless mode with `--fake-provider` to verify that path.

## Open questions

None — all questions resolved during planning.
