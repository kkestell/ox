# Remove Ox-specific coupling from Ur

## Goal

Ur is a general-purpose agent library. It must not reference its consumer (Ox) by name. Right
now, Ur hardcodes `".ox"` directory names, `"ox"` config section names, `"ox.model"` settings
keys, `ox-{date}.log` log filenames, and `${OX_SKILL_DIR}` / `${OX_SESSION_ID}` template
variables. All of these are Ox application decisions that belong in Ox's startup code, not in
the library.

## Desired outcome

The only occurrence of the string `"ox"` inside `src/Ur/` is the `InternalsVisibleTo("Ox")`
attribute (a C# compilation necessity, not a design coupling). Every Ox-specific default is
either removed entirely or pushed to Ox's startup code where it belongs. Ur's internals refer
only to its own name or to generic terms ("the host", "the application").

## How we got here

Plan `2026-04-12-005` did a mechanical rename from `.ur` → `.ox` across the codebase. It noted
in its "Structural considerations" section that the rename would introduce Ox-specific strings
into Ur and deferred fixing the coupling. This plan does the fix.

## Summary of approach

Every Ox-specific value in Ur is parameterised through the DI boundary (`AddUr()`) rather than
hardcoded:

1. **Config section name** — `AddUr()` gains a required `configSection` parameter. Ur binds
   `UrOptions` and resolves the model settings key from this parameter.
2. **Workspace subdirectory name** — `UrOptions` gains a `WorkspaceDirectoryName` property.
   `Workspace` reads it instead of hardcoding `".ox"`. Ox sets it to `".ox"` in its configure
   callback.
3. **User data directory** — `DefaultUserDataDirectory()` (the `~/.ox` fallback) is removed from
   Ur. Ox computes it before calling `AddUr()` and always provides it. `UserDataDirectory`
   becomes non-nullable by convention (Ur throws if it is null at startup).
4. **Log directory** — `UrFileLoggerProvider` accepts a `logDir` constructor parameter. Ox
   constructs it with the appropriate path.
5. **Model settings key** — `UrConfiguration.ModelSettingKey` changes from a `const` to an
   instance field initialised as `$"{configSection}.model"`.
6. **Skill template variables** — `${OX_SKILL_DIR}` and `${OX_SESSION_ID}` are renamed to
   `${SKILL_DIR}` and `${SESSION_ID}`. These are user-visible strings in SKILL.md files;
   all in-tree references (tests) are updated.
7. **Comments** — Any Ur comment that names "OxApp", "Ox", or "OxConfiguration" is rewritten
   to say "the host" or "the application layer".

## Related code

- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — `AddUr()`, `DefaultUserDataDirectory()`, config section bindings; primary change site
- `src/Ur/Workspace.cs` — `UrDirectory` property hardcodes `".ox"`
- `src/Ur/Configuration/UrOptions.cs` — gains `WorkspaceDirectoryName`; comments updated
- `src/Ur/Configuration/UrConfiguration.cs` — `ModelSettingKey` becomes instance field
- `src/Ur/Logging/UrFileLoggerProvider.cs` — hardcoded log dir; gains constructor parameter
- `src/Ur/Logging/UrFileLogger.cs` — hardcoded `ox-{date}.log` file prefix; fixed via `logDir` parameter above (directory path supplied, filename prefix is `ur-{date}.log` or configurable via a separate param)
- `src/Ur/Settings/ConfigurationScope.cs` — comment-only; references `~/.ox/settings.json`
- `src/Ur/Permissions/PermissionGrantStore.cs` — comment-only; references `{workspace}/.ox/`
- `src/Ur/Skills/SkillExpander.cs` — `${OX_SKILL_DIR}` / `${OX_SESSION_ID}` renamed to `${SKILL_DIR}` / `${SESSION_ID}`
- `src/Ur/Skills/SkillDefinition.cs` — comment references `${OX_SKILL_DIR}`
- `src/Ur/Skills/SkillLoader.cs` — comment references `~/.ox/skills/`
- `src/Ur/Skills/SkillFrontmatter.cs` — comment references `~/.ox/skills/`
- `src/Ur/Sessions/UrSession.cs` — comments reference "OxApp"
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — comment references "Ox layer"
- `src/Ur/Providers/IProvider.cs` — comment references "Ox"
- `src/Ur/Providers/Fake/FakeProvider.cs` — comment references "OxConfiguration"
- `src/Ox/Program.cs` — calls `DefaultUserDataDirectory()`; moves that call inline, adds `WorkspaceDirectoryName = ".ox"`
- `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` — calls `AddUr()` and `AddUrSettings()`; gains `configSection: "ox"` and `WorkspaceDirectoryName = ".ox"`
- `tests/Ur.Tests/Skills/SkillExpanderTests.cs` — test data uses `${OX_SKILL_DIR}` / `${OX_SESSION_ID}`
- `tests/Ur.Tests/Skills/SkillToolTests.cs` — test data uses `${OX_SESSION_ID}`

## Current state

- `Workspace.cs:10` hardcodes `Path.Combine(RootPath, ".ox")`.
- `ServiceCollectionExtensions.cs:79,95` hardcodes the config section `"ox"`.
- `ServiceCollectionExtensions.cs:221–223` hardcodes `~/.ox` as the user data directory default.
- `UrConfiguration.cs:28` hardcodes `"ox.model"` as the model settings key constant.
- `UrFileLoggerProvider.cs:15–17` hardcodes `~/.ox/logs` as the log directory.
- `UrFileLogger.cs:79` hardcodes `ox-{date}.log` as the log filename prefix.
- `SkillExpander.cs:63–64` hardcodes `${OX_SKILL_DIR}` / `${OX_SESSION_ID}` as template variable names.

## Structural considerations

**Hierarchy** — This change restores the correct dependency direction. Ur is an inner layer; Ox
is the outer layer. Outer layers know about inner layers, never the reverse. Currently Ur
knows about Ox by name in a dozen places.

**Encapsulation** — The parameterization all flows through the existing `AddUr()` entry point,
which is already Ur's public extension-method API. Adding a `configSection` parameter to an
existing extension method is a minor surface-area change with a clear owner.

**Abstraction** — The config section name and directory names are host policy decisions. Moving
them to the host's startup code is not over-engineering; it is the minimum change needed to
restore proper abstraction.

**Note on `InternalsVisibleTo("Ox")`**: This attribute in
`src/Ur/Properties/AssemblyInfo.cs` is a .NET compilation mechanism needed to give Ox access to
Ur's `internal` types. It is not design coupling — Ox is Ur's only host — and is explicitly
excluded from this plan.

**Note on `UrConfiguration.SecretService = "ur"`**: The keyring service name is already generic
(uses "ur", not "ox") and changing it would break stored API keys on user machines. Leave it.

## Implementation plan

### Phase 1 — `AddUr()` config section parameter

- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Add `string configSection` as a
  required parameter to `AddUr()` immediately before the optional `configure` callback.
  Replace both `configuration.GetSection("ox")` calls with `configuration.GetSection(configSection)`.
  Update `RegisterCoreSchemas()` to accept and use `configSection` when registering the model
  settings key.

### Phase 2 — `UrOptions.WorkspaceDirectoryName` and `Workspace`

- [ ] `src/Ur/Configuration/UrOptions.cs` — Add `public string WorkspaceDirectoryName { get; set; } = ".ur"`.
  Update the `WorkspacePath` doc comment to remove the `".ox/"` mention. Update the
  `UserDataDirectory` doc comment to remove the `"~/.ox/"` mention.
- [ ] `src/Ur/Workspace.cs` — Add `workspaceDirectoryName` constructor parameter.
  Replace `Path.Combine(RootPath, ".ox")` with `Path.Combine(RootPath, workspaceDirectoryName)`.
  Update the `EnsureDirectories` doc comment to remove `".ox"`.
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Pass `snapshot.WorkspaceDirectoryName`
  to the `Workspace` constructor.

### Phase 3 — Remove `DefaultUserDataDirectory()`

- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Remove the `DefaultUserDataDirectory()`
  helper method. Replace `snapshot.UserDataDirectory ?? DefaultUserDataDirectory()` with
  `snapshot.UserDataDirectory ?? throw new InvalidOperationException("UrOptions.UserDataDirectory must be set by the host before calling AddUr.")`.
  (Keep `DefaultUserSettingsPath()` — it is generic: `{userDataDir}/settings.json`.)
- [ ] `src/Ox/Program.cs` — Inline the `~/.ox` computation where `DefaultUserDataDirectory()`
  was called:
  `var userDataDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".ox");`
  Remove the import of `ServiceCollectionExtensions.DefaultUserDataDirectory()`.

### Phase 4 — `UrFileLoggerProvider` log directory

- [ ] `src/Ur/Logging/UrFileLoggerProvider.cs` — Add `string logDir` constructor parameter.
  Replace the hardcoded `~/.ox/logs` initializer with the provided `logDir`.
- [ ] `src/Ur/Logging/UrFileLogger.cs` — Change the log filename prefix from `ox-` to `ur-`.
  Update the doc comment accordingly.
- [ ] `src/Ox/Program.cs` — Pass `Path.Combine(userDataDir, "logs")` to
  `new UrFileLoggerProvider(...)`.

### Phase 5 — `UrConfiguration.ModelSettingKey`

- [ ] `src/Ur/Configuration/UrConfiguration.cs` — Change `internal const string ModelSettingKey`
  to `internal string ModelSettingKey { get; }`. Add `configSection` to the constructor
  signature and initialise `ModelSettingKey = $"{configSection}.model"`.
  Update the inline comment above the field.
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Pass `configSection` to the
  `UrConfiguration` constructor in the DI factory lambda.

### Phase 6 — Skill template variable rename

- [ ] `src/Ur/Skills/SkillExpander.cs` — Rename `${OX_SKILL_DIR}` → `${SKILL_DIR}` and
  `${OX_SESSION_ID}` → `${SESSION_ID}` in `ApplyBuiltins()`. Update all XML doc comments in
  this file to use the new names.
- [ ] `src/Ur/Skills/SkillDefinition.cs` — Update the doc comment on `SkillDirectory` to
  reference `${SKILL_DIR}`.
- [ ] `tests/Ur.Tests/Skills/SkillExpanderTests.cs` — Replace all occurrences of
  `${OX_SKILL_DIR}` with `${SKILL_DIR}` and `${OX_SESSION_ID}` with `${SESSION_ID}` in
  test data strings.
- [ ] `tests/Ur.Tests/Skills/SkillToolTests.cs` — Replace `${OX_SESSION_ID}` with
  `${SESSION_ID}` in test data strings.

### Phase 7 — Host call-site updates

- [ ] `src/Ox/Program.cs` — Pass `configSection: "ox"` to `AddUr()`.
- [ ] `src/Ox/Program.cs` — Set `o.WorkspaceDirectoryName = ".ox"` in the `AddUr` configure
  callback.
- [ ] `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` — Pass `configSection: "ox"` to `AddUr()`.
  Set `o.WorkspaceDirectoryName = ".ox"` in the configure callback. Pass the computed log dir
  to `UrFileLoggerProvider` if logging is used in tests (check whether `UrFileLoggerProvider`
  is instantiated here; if not, skip).
- [ ] `tests/Ox.Tests/TestSupport/TestHostBuilder.cs` — Same updates as above if this file
  also calls `AddUr()`.

### Phase 8 — Comment cleanup (no behaviour change)

- [ ] `src/Ur/Settings/ConfigurationScope.cs` — Update doc comment: replace `~/.ox/settings.json`
  and `$WORKSPACE/.ox/settings.json` with generic descriptions (`{userDataDir}/settings.json`
  and `{workspace}/{directoryName}/settings.json`).
- [ ] `src/Ur/Permissions/PermissionGrantStore.cs` — Replace `{workspace}/.ox/permissions.jsonl`
  with `{workspace state dir}/permissions.jsonl` in the class doc comment.
- [ ] `src/Ur/Skills/SkillLoader.cs` — Replace `~/.ox/skills/` and `.ox/skills/` with
  generic path descriptions.
- [ ] `src/Ur/Skills/SkillFrontmatter.cs` — Replace `"/home/user/.ox/skills/commit"` in the
  example comment with a generic example.
- [ ] `src/Ur/Sessions/UrSession.cs` — Replace all references to "OxApp" with "the host" or
  "the application layer".
- [ ] `src/Ur/AgentLoop/AgentLoopEvent.cs` — Replace "Ur and Ox layers" with "Ur and the host
  application".
- [ ] `src/Ur/Providers/IProvider.cs` — Replace "Ox" in the comment with "the host application".
- [ ] `src/Ur/Providers/Fake/FakeProvider.cs` — Replace "OxConfiguration" with the appropriate
  host concept.
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Update the class-level XML doc comment
  to remove "ox" from the `Configure<UrOptions>` section name description; describe the
  section as "the host-provided config section".

## Impact assessment

- **Build break**: `AddUr()` gains a required parameter. Every call site must be updated before
  the project compiles. There are two call sites: `src/Ox/Program.cs` and
  `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` (plus possibly `tests/Ox.Tests/TestSupport/TestHostBuilder.cs`).
- **Skill template variable breakage**: Any user SKILL.md file on disk referencing
  `${OX_SKILL_DIR}` or `${OX_SESSION_ID}` will pass through unexpanded after this change.
  Users must update their skill files. Consistent with AGENTS.md — no backward compatibility.
- **Settings file compatibility**: The model settings key changes from `"ox.model"` to the
  host-provided `"{configSection}.model"`. Since Ox passes `"ox"`, the key stays `"ox.model"` at
  runtime. **No settings files are affected** — the on-disk JSON structure is unchanged from the
  user's perspective.
- **Log filename**: `ox-{date}.log` → `ur-{date}.log`. Any external log-watching scripts that
  match the filename pattern will need updating.

## Validation

- [ ] `dotnet build` in repo root — must compile with no errors after all call sites are updated.
- [ ] `dotnet test` — all existing tests must pass. Focus on `Ur.Tests/Skills/` to verify skill
  variable rename is consistent.
- [ ] Manual: start Ox and verify `.ox/sessions/` is created (confirms `WorkspaceDirectoryName`
  flows correctly through `Workspace`).
- [ ] Manual: verify `~/.ox/logs/ur-{date}.log` is created (new filename prefix).
- [ ] Manual: grep `src/Ur/` for the string literal `"ox"` — the only remaining occurrence
  should be inside `Properties/AssemblyInfo.cs`.

## Open questions

None — scope is fully defined.
