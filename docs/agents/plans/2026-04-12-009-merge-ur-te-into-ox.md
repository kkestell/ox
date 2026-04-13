# Merge Ur and Te into Ox as a single layered project

## Goal

Collapse `Ur`, `Ur.Providers.*`, and `Te` into a single `Ox` assembly with three clearly
delineated layers — **Terminal**, **Agent**, **App** — expressed in both the folder tree
and namespace hierarchy. Abandon the "Ur is a standalone library" framing and everything
that grew out of it (the `AddUr` DI extension, the Ox-coupling cleanup effort, the
provider-per-csproj split, the `InternalsVisibleTo("Ox")` plumbing).

## Desired outcome

- One library+executable assembly: `src/Ox/Ox.csproj` (produces `Ox.dll` + `Ox` executable).
- No project named `Ur`, `Ur.Providers.*`, `Te`, or `Te.Demo` exists anywhere.
- Every file lives under one of `src/Ox/{Terminal,Agent,App}/`, with `Program.cs` at
  the `src/Ox/` root.
- Namespaces match the folder layer: `Ox.Terminal.*`, `Ox.Agent.*`, `Ox.App.*`. `Program.cs`
  lives in `namespace Ox;` (no suffix; it's the app entry, not domain code).
- Dependency rule (enforced by inspection): `App` may reference `Agent` and `Terminal`;
  `Agent` must not reference `Terminal`; `Terminal` references neither. Grep
  `src/Ox/Agent/` for `Ox.Terminal` and `src/Ox/Terminal/` for `Ox.Agent` — both must
  return zero matches.
- Tests live in `tests/Ox.Tests/` (unit) and `tests/Ox.IntegrationTests/` (integration),
  with subfolders mirroring the `src/Ox/` layer layout.
- Grep `src/` and `tests/` for the identifier `Ur` — zero matches outside of string
  literals in historical plan documents.
- No public `AddUr`/`AddOx` extension method. DI wiring is an internal helper called
  once from `Program.cs`.
- Ox-specific strings (`.ox`, `"ox"` config section, `~/.ox/`, `${OX_SKILL_DIR}`,
  `${OX_SESSION_ID}`) are hardcoded wherever they're used. No `configSection`
  parameter, no `WorkspaceDirectoryName` option.

## How we got here

Plans 007 (provider split) and 008 (Ox-decoupling) were both motivated by the idea that
Ur is a reusable agent library and Ox is one of several possible consumers. That framing
has proven to be a source of churn: the library/consumer boundary introduced
parameterization, dependency-injection surface, and a "no Ox strings in Ur" rule that
produced repeated mechanical cleanups without delivering real value. There are no other
consumers and none are planned. Collapsing into one project eliminates the boundary
friction while preserving the layering that actually matters (UI primitives vs. agent
runtime vs. product).

The user confirmed: one assembly, all providers folded in, everything named `Ur`
renamed to `Ox`, one test project plus one integration test project, `Te.Demo` deleted,
plan 008 deleted.

## Summary of approach

Execute a large, atomic structural move:

1. Delete dead artifacts (`Te.Demo`, plan 008).
2. Physically move source trees into `src/Ox/{Terminal,Agent,App}/`.
3. Fold each `Ur.Providers.*/` into `src/Ox/Agent/Providers/<Vendor>/`, pulling the
   vendor NuGet references into `src/Ox/Ox.csproj`.
4. Rewrite namespaces: `Te.*` → `Ox.Terminal.*`; `Ur.*` → `Ox.Agent.*`; current `Ox.*`
   (top-level app code) → `Ox.App.*`; `Program.cs` stays `namespace Ox;`.
5. Delete the obsolete project files (`Ur.csproj`, `Te.csproj`, `Ur.Providers.*.csproj`)
   and update `Ox.slnx`.
6. Rework DI — delete the public `AddUr`, `AddUrSettings`, and `Add{Vendor}Provider`
   extension methods; replace with a single internal `OxServices.Register` called from
   `Program.cs`. Collapse all the deferred parameterization from plan 008 by hardcoding
   Ox-specific strings at their point of use.
7. Drop `InternalsVisibleTo("Ox")` and audit `public` → `internal` across what was Ur
   (anything not reached from `Program.cs` or reflection can be internal).
8. Consolidate tests: merge `tests/Ur.Tests/` + `tests/Te.Tests/` + `tests/Ox.Tests/`
   into one `tests/Ox.Tests/`; rename `tests/Ur.IntegrationTests/` to
   `tests/Ox.IntegrationTests/`.
9. Update any stale `using Ur.*` / `using Te.*` in evals (project references should
   already be clean — evals invoke Ox via shell, not reference).
10. Build, run tests, verify the layer-boundary grep checks pass.

## Related code

- `Ox.slnx` — solution manifest; lists all projects that get deleted or moved.
- `src/Ur/Ur.csproj` — deleted; its NuGet package references migrate to `Ox.csproj`.
- `src/Ur/Properties/AssemblyInfo.cs` — hosts `InternalsVisibleTo("Ox")`; entire file
  becomes unnecessary.
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — current `AddUr`/`AddUrSettings`
  public entry points; becomes internal `OxServices` helper.
- `src/Ur/Hosting/UrHost.cs` — class name, namespace, and any self-references get
  renamed; consider renaming to `OxHost` for symmetry.
- `src/Ur/Configuration/UrOptions.cs` — class name is `UrOptions`; rename to `OxOptions`.
  Similarly `UrConfiguration`, `UrSession`, `UrFileLogger`, `UrFileLoggerProvider`,
  `UrSettingsConfigurationSource` — every `Ur`-prefixed type gets renamed.
- `src/Ur/Workspace.cs` — hardcode `.ox` directory name (no `WorkspaceDirectoryName`
  parameter).
- `src/Ur/Logging/UrFileLoggerProvider.cs`, `UrFileLogger.cs` — hardcode `~/.ox/logs/`
  and `ox-{date}.log` filename prefix.
- `src/Ur/Skills/SkillExpander.cs` — keep `${OX_SKILL_DIR}` / `${OX_SESSION_ID}` as
  template variables (not the `SKILL_DIR` form from plan 008; we're going back to the
  Ox-branded names since Ox is no longer a mere host).
- `src/Ur/Providers/*` — Fake provider and provider-registry interfaces.
- `src/Ur.Providers.{OpenAI,Google,OpenRouter,Ollama,ZaiCoding,OpenAiCompatible}/` — each
  is a single-file project; moves into `src/Ox/Agent/Providers/<Vendor>/`. Their
  `.csproj` files hold the vendor NuGet references that must migrate to `Ox.csproj`.
- `src/Te/Input/`, `src/Te/Rendering/` — entire folder subtree moves to
  `src/Ox/Terminal/`. Namespaces change from `Te.Input` / `Te.Rendering` to
  `Ox.Terminal.Input` / `Ox.Terminal.Rendering`.
- `src/Te.Demo/` — deleted.
- `src/Ox/*` (all current top-level app code) — moves into `src/Ox/App/` with namespace
  `Ox.App.*`. Exception: `Program.cs` stays at `src/Ox/Program.cs` with namespace `Ox`.
- `src/Ox/Ox.csproj` — gains the NuGet package references from `Ur.csproj` and every
  provider project. Loses all `ProjectReference` entries (they're all folded in).
- `tests/Ur.Tests/`, `tests/Te.Tests/`, `tests/Ox.Tests/` — merge into one
  `tests/Ox.Tests/` with `Agent/`, `Terminal/`, `App/` subfolders mirroring src.
- `tests/Ur.IntegrationTests/` — rename to `tests/Ox.IntegrationTests/`.
- `evals/EvalRunner/`, `evals/EvalShared/` — project references confirmed clean (do not
  reference Ur today); still sweep for dormant `using Ur.*` statements.
- `docs/agents/plans/2026-04-12-008-remove-ox-coupling.md` — delete.
- `boo`, `Makefile`, `scripts/` — may contain `Ur`/`Te` project-name references (e.g.
  test invocations, build scripts). Grep and update.
- `AGENTS.md`, `CLAUDE.md` — instruction files; check for `Ur`/`Te` references.
- `providers.json` — config file; check whether it names `Ur`-specific keys.

## Current state

- Solution has 19 projects; after this plan it has 5: `Ox`, `Ox.Tests`,
  `Ox.IntegrationTests`, `EvalShared` (+ `.Tests`), `EvalRunner` (+ `.Tests`). (5 or 7
  depending on whether eval test projects stay separate — they do; no change to evals
  structure beyond potential using-directive cleanup.)
- `src/Ox/Ox.csproj` currently has 8 `ProjectReference` entries (Ur, Te, six providers)
  and one package reference (`Microsoft.Extensions.Hosting`). After this plan, zero
  project references and ~12 package references (Hosting + everything currently in
  Ur.csproj + OpenAI/Google/etc. SDKs).
- `AddUr` is called from `src/Ox/Program.cs` and `tests/Ur.Tests/TestSupport/TestHostBuilder.cs`
  (and possibly `tests/Ox.Tests/TestSupport/`). Those call sites collapse into the new
  `OxServices.Register(...)` pattern (or its test equivalent).
- Plan 008 is documented but unimplemented; its entire diff is discarded.

## Structural considerations

**Hierarchy.** The three layers (Terminal, Agent, App) are a real architectural claim,
not a superficial reorganization. Terminal is UI primitives with no knowledge of
agents; Agent is the LLM runtime with no knowledge of terminals; App is the product
that binds them. Making this hierarchy evident in the filesystem + namespaces means any
future code review can catch a layer violation by reading the `using` block. We do not
need MSBuild-level enforcement — inspection and a CI grep are sufficient.

**Abstraction.** Collapsing `AddUr` ceremonies is the main abstraction win. The
`configSection` / `WorkspaceDirectoryName` / `logDir` parameters exist only to support
a consumer that does not exist. Removing them makes the agent-layer entry points
describe what they actually do (register sessions, load skills, open the workspace)
rather than negotiating with a hypothetical host.

**Modularization.** "Providers are their own layer" was a false boundary — they are
just `IProvider` implementations, the same shape as any `ITool`. Under the new layout
they live at `src/Ox/Agent/Providers/<Vendor>/`, a plain subfolder with no special
status. Vendor NuGet references move to the single `Ox.csproj`; the packaging cost of
carrying all provider SDKs in one artifact is accepted (single-assembly was the user's
choice).

**Encapsulation.** With one assembly, the public/internal distinction becomes much
stronger: almost nothing needs to be `public`. Types that were `public` solely so Ox
could construct them from another assembly (e.g. `UrHost`, `UrConfiguration`,
`UrOptions`, the provider classes) can all become `internal`. This is a downgrade in
surface area, not an upgrade — worth doing because every `public` type is a promise
about cross-project stability that no longer applies.

**`InternalsVisibleTo("Ox")`** disappears — single assembly, single visibility scope.

## Refactoring

The structural refactoring happens inside this plan; there is no feature work to
sequence after it. The plan *is* the refactor. Two specific refactors need to happen
*before* the big move, so the move itself stays mechanical:

- **Preparatory rename: `Ox.Configuration.OxConfiguration` → `ModelCatalog`.** The
  current app-layer `OxConfiguration` (`src/Ox/Configuration/OxConfiguration.cs`) is a
  ~112-line model-catalog / provider-readiness facade. Its name collides with the
  agent-layer `UrConfiguration` (~282 lines; settings, keyring, readiness) which
  becomes `OxConfiguration` after the rename. The two classes have genuinely different
  responsibilities and must not be merged — merging would produce a god class that
  spans the Agent/App boundary. Resolution: rename the app-layer class to `ModelCatalog`
  in a separate commit *before* Phase 2. `ModelCatalog` reflects what it is (a model
  catalog you query) rather than what it is not (a general configuration object). This
  also keeps `git log --follow` clean through the structural move.

- **DI entry point redesign.** Instead of a public `AddUr(services, configuration,
  configure)` extension method, introduce an internal static helper `OxServices`
  placed at `src/Ox/App/OxServices.cs` (namespace `Ox.App`). This is App-layer code,
  not Agent-layer code — it wires up providers from the app-layer `ProviderConfig` /
  `ProviderRegistration`, registers the app-layer `ModelCatalog`, and orchestrates
  bootstrap. Placing it under `src/Ox/Agent/` would violate the layer rule because it
  would force Agent code to reference app-layer types. `OxServices.Register` has a
  single method signature: `Register(IServiceCollection services, IConfiguration
  configuration, Action<OxOptions>? configure = null)`. `Program.cs` calls it once.
  Provider registration collapses similarly: no `AddOpenAiProvider()` extension on
  `IServiceCollection`; `OxServices.Register` dispatches through the existing
  `ProviderRegistration.AddProvidersFromConfig` (now an internal method in
  `src/Ox/App/Configuration/ProviderRegistration.cs`), which calls per-vendor factory
  methods on each provider class (e.g. `OpenAiProvider.Register(services,
  configuration)`). The `AddUrSettings` configuration-source helper becomes an internal
  static method on `OxServices` invoked during host configuration.

## Implementation plan

### Phase 0 — Preparatory rename (separate commit)

Run this as a standalone commit *before* Phase 1 so the big structural move stays
mechanical and `git log --follow` is not confused by a rename-within-a-move.

- [ ] Rename `src/Ox/Configuration/OxConfiguration.cs` class and file to
  `ModelCatalog.cs` / `class ModelCatalog`. Namespace remains `Ox.Configuration` for
  now — it moves to `Ox.App.Configuration` in Phase 3 with the rest of the app-layer
  code.
- [ ] Update every reference to the old `OxConfiguration` type across the codebase
  (Program.cs, tests, evals if applicable). Grep for `OxConfiguration` — at this point
  the only hits should be in `Ox.Configuration.ModelCatalog` (the old one, renamed)
  and `Ur.Configuration.UrConfiguration` (the other one, untouched). No file should
  still reference an `Ox.Configuration.OxConfiguration` type.
- [ ] `dotnet build` — succeeds. Commit.

### Phase 1 — Delete dead artifacts

- [ ] Delete `src/Te.Demo/` (entire directory, including `bin/`, `obj/`).
- [ ] Delete `docs/agents/plans/2026-04-12-008-remove-ox-coupling.md`.
- [ ] Remove `src/Te.Demo/Te.Demo.csproj` entry from `Ox.slnx`.

### Phase 2 — Move source trees

Do these moves with `git mv` so history is preserved. No content edits in this phase;
only file/directory relocation.

- [ ] Create `src/Ox/Terminal/`, `src/Ox/Agent/`, `src/Ox/App/`.
- [ ] Move `src/Te/Input/` → `src/Ox/Terminal/Input/`.
- [ ] Move `src/Te/Rendering/` → `src/Ox/Terminal/Rendering/`.
- [ ] Delete the empty `src/Te/` directory (along with its `bin/`, `obj/`,
  `Properties/`, `Te.csproj`).
- [ ] Move every Ur subfolder (`AgentLoop/`, `Compaction/`, `Configuration/`, `Hosting/`,
  `Logging/`, `Permissions/`, `Prompting/`, `Providers/`, `Sessions/`, `Settings/`,
  `Skills/`, `Todo/`, `Tools/`) and every top-level Ur file (`Workspace.cs`,
  `ToolContext.cs`) to `src/Ox/Agent/<same-name>`.
- [ ] Delete `src/Ur/Properties/AssemblyInfo.cs` (the `InternalsVisibleTo` attribute is
  now meaningless).
- [ ] Delete the empty `src/Ur/` directory (along with its `bin/`, `obj/`, `Ur.csproj`).
- [ ] For each `src/Ur.Providers.<Vendor>/` project, move every `.cs` file into
  `src/Ox/Agent/Providers/<Vendor>/`. Note: `Fake/` is already under
  `src/Ur/Providers/Fake/` and moves along with the other Agent folders in the step
  above — the `Ur.Providers.*` projects are the six vendor projects.
- [ ] Capture each provider project's package references (vendor SDK versions) before
  deleting the `.csproj` files — they get merged into `Ox.csproj` in Phase 4.
- [ ] Delete each `src/Ur.Providers.<Vendor>/` directory.
- [ ] Move every current `src/Ox/` top-level file and subfolder (except `Program.cs`,
  `Properties/`, `Ox.csproj`, `bin/`, `obj/`) into `src/Ox/App/`. Specifically:
  `AutocompleteEngine.cs`, `ComposerController.cs`, `HeadlessRunner.cs`, `OxApp.cs`,
  `OxBootOptions.cs`, `ScreenDumpWriter.cs`, `Configuration/` (now containing
  `ModelCatalog.cs`, `ProviderConfig.cs`, `ProviderRegistration.cs`), `Connect/`,
  `Conversation/`, `Input/`, `Permission/`, `Views/`.
- [ ] Verify `src/Ox/App/Configuration/ProviderRegistration.cs` exists after the move.
  Its `AddProvidersFromConfig` method is the app-layer dispatch point that replaces
  the per-vendor `Add<Vendor>Provider` extensions. It will be invoked from
  `OxServices.Register` in Phase 5.

### Phase 3 — Rewrite namespaces

- [ ] In every file under `src/Ox/Terminal/`, rewrite `namespace Te.Input;` →
  `namespace Ox.Terminal.Input;` and `namespace Te.Rendering;` →
  `namespace Ox.Terminal.Rendering;`. Update all `using Te.Input;` / `using Te.Rendering;`
  across the codebase to the new namespaces.
- [ ] In every file under `src/Ox/Agent/`, rewrite `namespace Ur;` → `namespace Ox.Agent;`,
  `namespace Ur.AgentLoop;` → `namespace Ox.Agent.AgentLoop;`, and every other
  `namespace Ur.<Sub>` → `namespace Ox.Agent.<Sub>`. Update all `using Ur.*;` across the
  codebase to `using Ox.Agent.*;`.
- [ ] In every file now under `src/Ox/App/`, rewrite `namespace Ox;` →
  `namespace Ox.App;` and `namespace Ox.<Sub>;` → `namespace Ox.App.<Sub>;` (the folders
  `Configuration`, `Connect`, `Conversation`, `Input`, `Permission`, `Views`). Update
  `using Ox.<Sub>;` references as needed.
- [ ] In `src/Ox/Program.cs`, keep the namespace as `Ox;` (top-level). Update `using`
  directives to point at `Ox.App.*`, `Ox.Agent.*`, `Ox.Terminal.*`.
- [ ] Rename `Ur`-prefixed types to `Ox`-prefixed: `UrOptions` → `OxOptions`,
  `UrConfiguration` → `OxConfiguration` (note: there is *already* a type named
  `OxConfiguration` under `src/Ox/Configuration/`; disambiguate — probably rename the
  current `OxConfiguration` to `ProviderConfig` or merge responsibilities), `UrHost` →
  `OxHost`, `UrSession` → `OxSession`, `UrFileLogger` → `OxFileLogger`,
  `UrFileLoggerProvider` → `OxFileLoggerProvider`, `UrSettingsConfigurationSource` →
  `OxSettingsConfigurationSource`. Resolve the `OxConfiguration` name clash before
  renaming (see Open questions).
- [ ] Grep `src/` and `tests/` for the identifier `Ur` — every remaining match must be
  inside a string literal (log message, comment, documentation path). Replace each with
  `Ox` unless it refers to a historical plan filename or external package.

### Phase 4 — Merge csproj contents

- [ ] Copy the `<PackageReference>` entries from `src/Ur/Ur.csproj` into
  `src/Ox/Ox.csproj` (Microsoft.Extensions.AI, Configuration, DI.Abstractions,
  FileSystemGlobbing, Logging, Options, etc.).
- [ ] Copy each vendor SDK `<PackageReference>` from the six provider csproj files into
  `src/Ox/Ox.csproj`. Deduplicate against existing entries.
- [ ] Merge `<NoWarn>` / `<AllowUnsafeBlocks>` / other property-group flags that existed
  in `Ur.csproj` into `Ox.csproj` (CA1848, CA1873).
- [ ] Remove every `<ProjectReference>` from `src/Ox/Ox.csproj` — there should be none
  after this phase.
- [ ] Update `Ox.slnx`: remove `Ur.csproj`, `Te.csproj`, `Te.Demo.csproj`, and all
  `Ur.Providers.*.csproj` project entries. Verify `Ox.csproj` is still listed.

### Phase 5 — Collapse DI surface

- [ ] Create `src/Ox/App/OxServices.cs` (namespace `Ox.App`, `internal static class
  OxServices`). This is App-layer code, not Agent-layer: it orchestrates app bootstrap
  by calling into Agent-layer registration and then wiring up app-specific services
  (ProviderRegistration, ModelCatalog, etc.). Placing it in Agent/ would violate the
  layer rule since it references App-layer types.
- [ ] `OxServices` exposes two internal methods:
  - `Register(IServiceCollection services, IConfiguration configuration,
    Action<OxOptions>? configure = null)` — the full bootstrap body.
  - `AddSettingsSources(IConfigurationBuilder builder, string userSettingsPath,
    string workspaceSettingsPath)` — invoked during `ConfigurationBuilder` setup.
- [ ] Lift the body of `AddUr` from `src/Ox/Agent/Hosting/ServiceCollectionExtensions.cs`
  into `OxServices.Register`. The file
  `src/Ox/Agent/Hosting/ServiceCollectionExtensions.cs` can then be deleted entirely;
  there is no reason for the Agent layer to expose a registration extension method. If
  there are per-service factory helpers worth keeping in Agent (e.g.
  `CreatePlatformKeyring`, `RegisterCoreSchemas`), move them to plain internal static
  methods on the relevant type rather than leaving a registration-extensions file
  behind.
- [ ] Re-evaluate the `src/Ox/Agent/Hosting/` folder. After removing the DI
  extensions, the only remaining file is `UrHost.cs` → `OxHost.cs`. If `OxHost` is
  primarily a service facade (looks up DI-registered services and exposes them to the
  app) consider whether `Hosting` is still a meaningful folder name or whether
  `OxHost.cs` belongs at `src/Ox/Agent/OxHost.cs` at the folder root. Not a blocker —
  judge by what makes the tree readable.
- [ ] Delete `AddOpenAiProvider`, `AddGoogleProvider`, `AddOpenRouterProvider`,
  `AddOllamaProvider`, `AddZaiCodingProvider`, `AddOpenAiCompatibleProvider` extension
  methods from each provider file. Replace with `internal static void
  Register(IServiceCollection services, IConfiguration configuration)` static methods
  (or direct registration calls) invoked from `ProviderRegistration.AddProvidersFromConfig`.
- [ ] Update `ProviderRegistration.AddProvidersFromConfig` (now in
  `src/Ox/App/Configuration/`) to call the new per-vendor `Register` methods instead
  of the deleted extension methods. `OxServices.Register` invokes
  `ProviderRegistration.AddProvidersFromConfig` as one of its steps.
- [ ] Remove the `configSection` plumbing in `OxConfiguration` (née `UrConfiguration`):
  replace `ModelSettingKey` logic with the const `"ox.model"`.
- [ ] Remove the `WorkspaceDirectoryName` option from `OxOptions` (née `UrOptions`).
  `Workspace` hardcodes `.ox`.
- [ ] Update `src/Ox/Program.cs` to call `OxServices.Register(...)` instead of
  `builder.Services.AddUr(...)` and similar.
- [ ] Remove `DefaultUserDataDirectory()` helper indirection if it existed; inline as
  `Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".ox")`
  at the single call site.

### Phase 6 — Collapse Ox-specific string parameterization

All of plan 008's deferred parameterization is reversed. Every Ox-specific string gets
hardcoded at its point of use.

- [ ] `src/Ox/Agent/Workspace.cs` — hardcode `Path.Combine(RootPath, ".ox")`.
- [ ] `src/Ox/Agent/Hosting/ServiceCollectionExtensions.cs` (or wherever
  `OxServices.Register` ends up) — hardcode `configuration.GetSection("ox")`.
- [ ] `src/Ox/Agent/Logging/OxFileLoggerProvider.cs` — hardcode `~/.ox/logs` as the log
  directory.
- [ ] `src/Ox/Agent/Logging/OxFileLogger.cs` — hardcode `ox-{date}.log` as the filename
  prefix.
- [ ] `src/Ox/Agent/Configuration/OxConfiguration.cs` — hardcode `ModelSettingKey =
  "ox.model"` (const).
- [ ] `src/Ox/Agent/Skills/SkillExpander.cs` — confirm template variables are
  `${OX_SKILL_DIR}` and `${OX_SESSION_ID}`. (They already are on `main`; plan 008's
  proposed rename to `${SKILL_DIR}`/`${SESSION_ID}` was never merged. Verify.)
- [ ] Update doc comments in `ConfigurationScope.cs`, `PermissionGrantStore.cs`,
  `SkillLoader.cs`, `SkillFrontmatter.cs`, `SkillDefinition.cs`, `UrSession.cs` (now
  `OxSession.cs`), `AgentLoopEvent.cs`, `IProvider.cs`, `FakeProvider.cs` to refer to
  concrete Ox paths/types (e.g. `~/.ox/settings.json`, `OxSession`, etc.) rather than
  the generic "host"/"application layer" wording plan 008 imposed.

### Phase 7 — Visibility audit

- [ ] Grep `src/Ox/Agent/` for `public class`, `public interface`, `public record`,
  `public struct`, `public enum`. For each hit, determine whether any caller lives
  outside `src/Ox/Agent/` (i.e. in `src/Ox/App/` or reflection-driven paths). If not,
  change to `internal`.
- [ ] Same audit for `src/Ox/Terminal/` and `src/Ox/App/`.
- [ ] **Serialization boundary check.** Before changing any type's visibility, check
  whether it is referenced by a `[JsonSerializable]` source-generator context (e.g.
  `SettingsJsonContext`, any other `JsonSerializerContext` subclass) or bears
  `[JsonPropertyName]` attributes. If the source-generated context is `public`, its
  referenced types must be `public`; if the context is `internal`, they may be
  `internal`. Mismatches either fail the build or cause a silent reflection fallback.
  Grep for `JsonSerializable` and `JsonSerializerContext` across the merged tree, list
  every type each context references, and keep context + referenced-types in
  compatible visibility brackets. `PermissionGrantStore`, `FakeScenario`, session
  records, and anything under `Settings/` are the likely hotspots.
- [ ] Ensure at least `Program`'s `Main` remains accessible to the runtime (no explicit
  `public` needed — top-level statements or `internal` both work).
- [ ] Delete any `InternalsVisibleTo` attributes that remain anywhere in the codebase
  (they were put in place to let Ox see Ur internals; no longer needed).

### Phase 8 — Test consolidation

- [ ] Move every file under `tests/Ur.Tests/` into `tests/Ox.Tests/Agent/`, preserving
  the subfolder structure (`AgentLoop/`, `Compaction/`, `Configuration/`, `Hosting/`,
  `Logging/`, `Permissions/`, `Prompting/`, `Providers/`, `Sessions/`, `Settings/`,
  `Skills/`, `Todo/`, `Tools/`, `TestData/`, `TestSupport/`).
- [ ] Move every file under `tests/Te.Tests/` into `tests/Ox.Tests/Terminal/`.
- [ ] Create `tests/Ox.Tests/App/` and move existing `tests/Ox.Tests/` files into it
  (`ComposerControllerTests.cs`, `HeadlessRunnerTests.cs`, `OxBootOptionsTests.cs`,
  `ScreenDumpWriterTests.cs`, `Connect/`, `Permission/`, `Views/`). Note: current
  `TestSupport/` may need to merge with `Agent/TestSupport/` — decide per-file whether
  each helper is App-specific or Agent-specific; shared helpers go at
  `tests/Ox.Tests/TestSupport/`.
- [ ] Delete `tests/Ur.Tests/` and `tests/Te.Tests/` directories.
- [ ] Update `tests/Ox.Tests/Ox.Tests.csproj` — merge package references and
  `ProjectReference` changes (should end with one `ProjectReference` to
  `../../src/Ox/Ox.csproj`).
- [ ] Delete `tests/Ur.Tests/Ur.Tests.csproj` and `tests/Te.Tests/Te.Tests.csproj`.
- [ ] Rewrite namespaces in all moved test files: `Ur.Tests.*` → `Ox.Tests.Agent.*`;
  `Te.Tests` → `Ox.Tests.Terminal`; existing `Ox.Tests.*` → `Ox.Tests.App.*`.
- [ ] Rewrite `using` directives: `using Ur.*;` → `using Ox.Agent.*;`;
  `using Te.*;` → `using Ox.Terminal.*;`.
- [ ] In `tests/Ox.Tests/TestSupport/TestHostBuilder.cs` (and any other DI-setup test
  helpers): replace `AddUr(...)` with `OxServices.Register(...)` calls. Remove all
  `configSection` and `WorkspaceDirectoryName` arguments.
- [ ] Rename `tests/Ur.IntegrationTests/` → `tests/Ox.IntegrationTests/`. Rename its
  csproj. Update its namespaces (`Ur.IntegrationTests.*` → `Ox.IntegrationTests.*`) and
  using directives.
- [ ] Update `Ox.slnx`: remove `Ur.Tests.csproj`, `Te.Tests.csproj`,
  `Ur.IntegrationTests.csproj` entries; add `Ox.IntegrationTests.csproj` entry. Keep
  `Ox.Tests.csproj`.

### Phase 9 — Evals and scripts

- [ ] Grep `evals/` for `using Ur.`, `using Te.`, and any bare references to `Ur`/`Te`.
  Replace with `Ox.*` equivalents. Project references do not need updating (evals
  invoke Ox via shell commands, not assembly references).
- [ ] Grep `boo`, `Makefile`, `scripts/` for project names `Ur`, `Te`, `Ur.Tests`,
  `Te.Tests`, `Ur.IntegrationTests`, `Te.Demo`, `Ur.Providers`. Replace with `Ox`,
  `Ox.Tests`, `Ox.IntegrationTests` as appropriate.
- [ ] Grep `AGENTS.md`, `CLAUDE.md`, `README` (if any), `docs/` for `Ur`/`Te` concept
  references. Update to Ox-layered terminology where it matters; preserve historical
  plan documents verbatim (they describe history).
- [ ] Check `providers.json` for any `Ur`-branded keys. Update if present.

### Phase 10 — Build, test, verify

- [ ] `dotnet build` from repo root — zero errors, zero warnings (other than pre-existing).
- [ ] `dotnet test tests/Ox.Tests/Ox.Tests.csproj` — all unit tests pass.
- [ ] `dotnet test tests/Ox.IntegrationTests/Ox.IntegrationTests.csproj` — all
  integration tests pass.
- [ ] `dotnet test` (solution-wide) — all tests across evals also pass.
- [ ] `boo` end-to-end harness — any boo targets succeed.
- [ ] Run the Ox binary manually: `dotnet run --project src/Ox` in an empty workspace;
  verify `.ox/` is created and contains `sessions/`, etc.; verify `~/.ox/logs/ox-<date>.log`
  is produced.
- [ ] Grep checks:
  - `grep -r "namespace Ur" src/ tests/` → zero results.
  - `grep -r "namespace Te" src/ tests/` → zero results.
  - `grep -r "using Ur" src/ tests/` → zero results.
  - `grep -r "using Te" src/ tests/` → zero results.
  - `grep -r "Ox.Terminal" src/Ox/Agent/` → zero results (layer rule).
  - `grep -r "Ox.Agent" src/Ox/Terminal/` → zero results (layer rule).
  - `grep -r "Ox.App" src/Ox/Agent/` → zero results (layer rule).
  - `grep -r "Ox.App" src/Ox/Terminal/` → zero results (layer rule).
  - `find . -name "Ur.csproj" -o -name "Te.csproj" -o -name "Te.Demo.csproj"` → zero
    results.
  - `find src/ -name "Ur.Providers.*.csproj"` → zero results.
  - `grep -r "InternalsVisibleTo" src/` → zero results.

## Impact assessment

- **Code paths affected:** every source file moves; every namespace changes; every
  csproj changes; the solution file changes. This is a whole-tree refactor.
- **Data/schema impact:** none. The on-disk contract (`~/.ox/`, `.ox/` in workspaces,
  settings schema, session JSONL layout, skill variables `${OX_SKILL_DIR}` /
  `${OX_SESSION_ID}`) is preserved. Existing workspaces and user data directories
  continue to work.
- **Dependency impact:** `src/Ox/Ox.csproj` aggregates every package reference that was
  previously spread across Ur and the six provider projects. The resulting binary
  carries all vendor SDKs unconditionally (OpenAI, Google, OpenRouter, Ollama,
  ZaiCoding, OpenAiCompatible). This is the acknowledged cost of the "one assembly"
  decision. No external API surface (since there was no external API — Ur was not
  published).
- **Git history:** using `git mv` preserves per-file blame/history. Namespace rewrites
  within moved files appear as a content edit in the same commit — consider splitting
  the renames into a separate commit per layer so `git log --follow` remains navigable.

## Validation

- Tests: every existing unit and integration test must pass after the consolidation.
  No new tests are added by this plan; the validation is structural. If a test fails,
  it's because of a missed namespace reference, a missed DI wiring change, or a
  hardcoded string that was missed.
- Lint/format/typecheck: `dotnet build` at `AnalysisMode=Recommended` must succeed;
  analyzer violations (CA1848, CA1873 are already suppressed) should match current
  state.
- Manual verification: run Ox, verify workspace bootstrap, verify log creation, verify
  skills expand `${OX_SKILL_DIR}` / `${OX_SESSION_ID}` correctly.
- Boundary verification: the grep checks in Phase 10 are the architecture's smoke test.
  They should be run on every branch that touches `src/Ox/Agent/` or `src/Ox/Terminal/`
  to catch layer violations early. Consider adding them to `boo` or a pre-commit hook
  once the refactor lands.

## Gaps and follow-up

- **CI enforcement of the layer rule.** The plan relies on human inspection and manual
  grep to enforce `Agent ⊥ Terminal`. If that drifts, the architecture claim quietly
  rots. Follow-up: either add grep-based layer checks to `boo`, or introduce a
  Roslyn analyzer (probably overkill for a one-developer project).
- **Public API surface reduction.** Phase 7's visibility audit is necessarily
  conservative — anything unclear stays `public` until proven otherwise. A follow-up
  pass once the assembly is stable could tighten further.

## Open questions

None — all structural decisions are committed. The `OxConfiguration` name clash is
resolved by Phase 0's preparatory rename to `ModelCatalog`; `OxServices` placement is
committed to `src/Ox/App/`; `OxFileLogger`/`OxFileLoggerProvider`/etc. type renames
are specified in Phase 3. Anything not called out above (e.g. whether to retain the
`Hosting/` subfolder in the Agent layer once its sole occupant is `OxHost.cs`) is a
Phase-5 judgment call that does not affect correctness.
