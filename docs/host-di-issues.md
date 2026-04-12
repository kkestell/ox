# Review: .NET Generic Host & DI Usage in Ur/Ox

> **Status (post-refactor):** All issues below have been resolved by the
> implementation in `docs/agents/plans/2026-04-12-004-idiomatic-generic-host-di.md`.
> This document is retained as historical context for why the refactoring was done.

## 🔴 Critical: Extensibility is Blocked for External Consumers

The task requirement — *"someone can reference the Ur library and then use ConfigureServices to use custom providers, compaction strategies, session storage backends"* — is **not achievable today**. These are the blockers:

### 1. `IProvider` is `internal`
`src/Ur/Providers/IProvider.cs:14` — The interface itself is `internal`, so external consumers cannot implement it. All provider implementations (`OpenAiCompatibleProvider`, `GoogleProvider`, `OllamaProvider`) are also `internal sealed`.

**Fix:** Make `IProvider` `public`. Provider implementations can stay internal if you register them yourself, but the interface must be public for custom providers.

### 2. `ProviderRegistry` is `internal` with no registration hook
`src/Ur/Providers/ProviderRegistry.cs:10` — There's no way for an external consumer to register a custom provider. The registry is populated exclusively inside `ServiceCollectionExtensions.AddUr()` via a hardcoded `switch` on provider type strings.

**Fix:** Either make `ProviderRegistry` public with a `Register()` method, or (better) use the open-generics pattern where consumers call `services.AddSingleton<IProvider, MyCustomProvider>()` and `AddUr()` discovers them via `sp.GetServices<IProvider>()`.

### 3. Provider type switch is hardcoded
`src/Ur/Hosting/ServiceCollectionExtensions.cs:146-171` — The `switch (entry.Type)` block only knows about `"openai-compatible"`, `"google"`, and `"ollama"`. Unknown types are silently skipped. There's no way to register a custom provider type resolver.

**Fix:** Replace with a provider-type registry pattern (e.g., `IProviderFactory` interface keyed by type string) so consumers can add support for new provider types without modifying Ur source.

### 4. No session storage abstraction
`src/Ur/Sessions/SessionStore.cs` — There is no `ISessionStore` interface. `SessionStore` is a concrete class registered directly. Consumers cannot substitute a different storage backend (e.g., SQLite, cloud storage).

**Fix:** Extract an `ISessionStore` interface and register it in DI. Keep `SessionStore` as the default JSONL implementation.

### 5. No compaction strategy abstraction
`src/Ur/Compaction/Autocompactor.cs` — Compaction is a `static` class with no interface. There's no way to swap compaction strategies (e.g., sliding window, embedding-based summarization, no-op compaction).

**Fix:** Extract an `ICompactionStrategy` interface. Register it in DI. Keep `Autocompactor` as the default implementation.

---

## 🟡 Moderate: Non-Idiomatic DI / Hosting Patterns

### 6. `Program.cs` builds `ServiceCollection` manually instead of using Generic Host
`src/Ox/Program.cs:26-27` — Production code does:
```csharp
var services = new ServiceCollection();
services.AddUr(startupOptions);
using var sp = services.BuildServiceProvider();
```

But the test harness (`TestHostBuilder.cs:42`) correctly uses `Host.CreateApplicationBuilder()`. This inconsistency means:
- Production gets no `IHost` lifecycle (no graceful shutdown, no `IHostedService`)
- Logging and configuration infrastructure diverge between test and production
- The `Microsoft.Extensions.Hosting` package is referenced only in test projects, not in `Ox.csproj`

**Fix:** Use `Host.CreateApplicationBuilder()` in production. This gives you `IHost`, proper disposal, and consistent infrastructure.

### 7. `AddUr()` clears logging providers — too aggressive for a library
`src/Ur/Hosting/ServiceCollectionExtensions.cs:39-43` — `builder.ClearProviders()` removes all existing logging configuration. This is the host application's responsibility, not the library's. If Ox is the only consumer, this should be done in `Program.cs`, not in `AddUr()`.

**Fix:** Remove logging configuration from `AddUr()`. Have the host (Ox) configure logging before calling `AddUr()`.

### 8. `IConfigurationRoot` built inside a DI factory
`src/Ur/Hosting/ServiceCollectionExtensions.cs:67-74` — The configuration root is constructed inside `AddSingleton<IConfigurationRoot>(sp => ...)`. This means:
- Configuration isn't available during DI registration (only after the container is built)
- The `ProviderConfig.Load()` call at line 139 can't read from this configuration (it reads from file directly)
- Other DI registrations that might need config values must use the factory pattern

**Idiomatic approach:** Build `IConfiguration` *before* calling `AddUr()` and pass it in (or configure it on the `HostApplicationBuilder` which pre-populates it). Libraries should consume `IConfiguration`, not create it.

### 9. Custom `UrOptionsMonitor` instead of the standard options pipeline
`src/Ur/Configuration/UrOptionsMonitor.cs` — A hand-rolled `IOptionsMonitor<T>` that:
- Re-binds from `IConfiguration` on every `CurrentValue` access (defeating the purpose of `IOptionsMonitor` caching)
- Returns `null` from `OnChange()` (violates the interface contract — callers may NRE)
- Duplicates binding logic that `services.Configure<UrOptions>(configuration.GetSection("ur"))` gives for free

**Fix:** Use the standard `Configure<T>` + `IOptionsMonitor<T>` pipeline. If reload-on-write is needed, ensure `IConfigurationRoot.Reload()` propagates through the built-in change token mechanism.

### 10. `UrStartupOptions` mixes configuration with test overrides
`src/Ur/Hosting/UrStartupOptions.cs` — Options like `ChatClientFactoryOverride`, `FakeProvider`, `KeyringOverride`, and `AdditionalTools` are test-specific escape hatches baked into the public API. The idiomatic DI approach is:
- Register `IKeyring` directly via `services.AddSingleton<IKeyring>(myImpl)`
- Register `IProvider` for custom providers
- Use `services.Configure<UrOptions>()` for configuration values

**Fix:** Move test overrides to the test project (they already have `TestHostBuilder`). The public `UrStartupOptions` should only contain configuration values (paths, model overrides).

---

## 🟢 Minor: Style and Consistency Issues

### 11. `UrHost` is `public` but constructor is `internal`
`src/Ur/Hosting/UrHost.cs:26,68` — The class is public (so consumers can use it), but the constructor is internal (so only DI can create it). This works with the factory registration, but it means consumers can't construct it outside DI. This is fine if `AddUr()` is always the entry point, but it's worth noting that the `internal` constructor forces a DI-only creation pattern. If you want to support both DI and manual construction, make the constructor public.

### 12. `Microsoft.Extensions.DependencyInjection` concrete package is missing from `Ox.csproj`
`Ox.csproj` has no `Microsoft.Extensions.DependencyInjection` or `Microsoft.Extensions.Hosting` package references. It works only because `Ur.csproj` references `DependencyInjection.Abstractions` and the concrete `ServiceCollection` class comes transitively. This is fragile — the production host should explicitly reference the hosting package.

### 13. `ProviderConfig.Load()` is called at registration time, not from DI
`src/Ur/Hosting/ServiceCollectionExtensions.cs:139` — `ProviderConfig.Load()` runs synchronously during `AddUr()`, not inside a factory delegate. This means file I/O happens during service registration, which is unusual in DI. Moving it into a factory `sp => ProviderConfig.Load(path)` would defer I/O until the first resolution.

