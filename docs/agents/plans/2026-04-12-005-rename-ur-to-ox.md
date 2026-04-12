# Rename .ur directories to .ox

## Goal

Rename the `~/.ur` and `$WORKSPACE/.ur` directory paths to `~/.ox` and `$WORKSPACE/.ox` throughout the codebase. Update all code that references these paths, and rename "ur" to "ox" inside the files stored in those directories (settings.json section key, log file naming, skill template variables).

## Desired outcome

After this change, Ox reads and writes all user data and workspace state under `.ox/` instead of `.ur/`. Existing `~/.ur/` directories on disk must be manually renamed to `~/.ox/` by the user.

## Related code

- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Defines `DefaultUserDataDirectory()` which hardcodes `".ur"` (line 223)
- `src/Ur/Workspace.cs` — Defines `UrDirectory` property which hardcodes `".ur"` (line 10)
- `src/Ur/Logging/UrFileLoggerProvider.cs` — Hardcodes `".ur"` in log directory path (line 17)
- `src/Ur/Logging/UrFileLogger.cs` — Log file named `ur-{date}.log` (line 79)
- `src/Ox/Program.cs` — Hardcodes `".ur"` for workspace settings path (line 43)
- `src/Ur/Configuration/UrConfiguration.cs` — Config section key `"ur.model"` (line 28), keyring service name `"ur"` (line 33)
- `src/Ur/Skills/SkillExpander.cs` — Template variables `${UR_SKILL_DIR}` and `${UR_SESSION_ID}` (lines 63-64)
- `scripts/install.sh` — `UR_CONFIG_DIR="$HOME/.ur"` (line 7)
- `scripts/boo.sh` — `$WORKSPACE/.ur/skills/greet` (lines 63-64)
- `evals/EvalRunner/ContainerRunner.cs` — Container mount `/root/.ur/providers.json` (line 68), `.ur/sessions` (line 150)
- `evals/Containerfile` — `mkdir -p /root/.ur` (line 50)
- `.gitignore` — `.ur/` entry (line 3)
- `docs/config.md` — Documentation referencing `.ur` paths
- `docs/development/evals.md` — Documentation referencing `.ur` paths

## Current state

The `.ur` directory name is hardcoded as a string literal in every location that constructs a path to the user data directory or workspace state directory. There is no centralized constant, configuration option, or DI-injected value for the directory name — each call site independently strings together `".ur"` via `Path.Combine`.

The workspace root already has a `.ox/` directory (containing `screen-dumps/` and `screenshots/` from TUI development). The user home directory does not have `~/.ox/` — only `~/.ur/` exists there.

## Hardcoded assumptions (identification only — NOT part of this plan)

These are places where the `.ur` directory name or related "ur" identifiers are baked into the codebase rather than configured via DI. The Ox project (host layer) should ideally own these values and inject them into the Ur library layer.

| Location | What's hardcoded | Should be |
|---|---|---|
| `ServiceCollectionExtensions.DefaultUserDataDirectory()` | `Path.Combine(..., ".ur")` | Injected by Ox host via `UrOptions.UserDataDirectory` |
| `Workspace.UrDirectory` | `Path.Combine(RootPath, ".ur")` | Workspace directory name injected or constant from host |
| `UrFileLoggerProvider._logDir` | `Path.Combine(..., ".ur", "logs")` | Log directory injected by host |
| `Ox.Program.Main()` line 43 | `Path.Combine(workspacePath, ".ur", "settings.json")` | Should use Workspace properties instead of constructing the path independently |
| `UrConfiguration.ModelSettingKey` | `"ur.model"` | Section key should be host-configurable |
| `UrConfiguration.SecretService` | `"ur"` | Keyring service name is hardcoded |
| `ServiceCollectionExtensions` line 79 | `configuration.GetSection("ur")` | Section name should be host-configurable |
| `ContainerRunner` / `Containerfile` | `/root/.ur` | Container paths hardcoded to match `DefaultUserDataDirectory()` |
| `install.sh` | `"$HOME/.ur"` | Script mirrors the hardcoded default |

## Implementation plan

### Phase 1: Source code — directory paths

- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs:223` — Change `".ur"` to `".ox"` in `DefaultUserDataDirectory()`
- [ ] `src/Ur/Workspace.cs:10` — Change `".ur"` to `".ox"` in `UrDirectory` property
- [ ] `src/Ur/Logging/UrFileLoggerProvider.cs:16-17` — Change `".ur"` to `".ox"` in `_logDir` initialization
- [ ] `src/Ox/Program.cs:43` — Change `".ur"` to `".ox"` in `workspaceSettingsPath`

### Phase 2: Source code — config section key

- [ ] `src/Ur/Configuration/UrConfiguration.cs:28` — Change `"ur.model"` to `"ox.model"` in `ModelSettingKey`
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs:79` — Change `GetSection("ur")` to `GetSection("ox")`
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs:95` — Change `GetSection("ur")` to `GetSection("ox")`

### Phase 3: Source code — log file naming

- [ ] `src/Ur/Logging/UrFileLogger.cs:79` — Change `"ur-"` to `"ox-"` in log filename pattern

### Phase 4: Source code — skill template variables

- [ ] `src/Ur/Skills/SkillExpander.cs:63` — Change `${UR_SKILL_DIR}` to `${OX_SKILL_DIR}`
- [ ] `src/Ur/Skills/SkillExpander.cs:64` — Change `${UR_SESSION_ID}` to `${OX_SESSION_ID}`
- [ ] `src/Ur/Skills/SkillDefinition.cs:57` — Update doc comment for `${UR_SKILL_DIR}` to `${OX_SKILL_DIR}`

### Phase 5: Test code — directory paths

- [ ] `tests/Ur.Tests/TestSupport/TestHostBuilder.cs:50` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/HeadlessRunnerTests.cs:40` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/HeadlessRunnerTests.cs:55` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/HostSessionApiTests.cs:45` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/HostSessionApiTests.cs:113` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/HostSessionApiTests.cs:159` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/Skills/SkillSessionTests.cs:24` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/PermissionTests.cs:249` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/PermissionTests.cs:659` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.Tests/Skills/SkillExpanderTests.cs:103,107` — Change `.ur` to `.ox` in test data strings
- [ ] `tests/Ur.Tests/Skills/SkillExpanderTests.cs:113` — Change `${UR_SESSION_ID}` to `${OX_SESSION_ID}` (test data)
- [ ] `tests/Ur.Tests/Skills/SkillExpanderTests.cs:126` — Change `${UR_SKILL_DIR}` and `${UR_SESSION_ID}` to `${OX_*}`
- [ ] `tests/Ur.Tests/Skills/SkillToolTests.cs:113` — Change `${UR_SESSION_ID}` to `${OX_SESSION_ID}`
- [ ] `tests/Ur.IntegrationTests/MultiProviderSmokeTests.cs:99` — Change `".ur"` to `".ox"`
- [ ] `tests/Ur.IntegrationTests/MultiProviderSmokeTests.cs:104` — Change `".ur"` to `".ox"`

### Phase 6: Scripts

- [ ] `scripts/install.sh:7` — Change `$HOME/.ur` to `$HOME/.ox` and rename variable `UR_CONFIG_DIR` to `OX_CONFIG_DIR`
- [ ] `scripts/install.sh:24` — Update echo message to reference `.ox`
- [ ] `scripts/boo.sh:63-64` — Change `$WORKSPACE/.ur/` to `$WORKSPACE/.ox/`

### Phase 7: Evals / container

- [ ] `evals/EvalRunner/ContainerRunner.cs:68` — Change `/root/.ur/providers.json` to `/root/.ox/providers.json`
- [ ] `evals/EvalRunner/ContainerRunner.cs:150` — Change `.ur/sessions` to `.ox/sessions`
- [ ] `evals/Containerfile:50` — Change `/root/.ur` to `/root/.ox`

### Phase 8: Config and gitignore

- [ ] `.gitignore:3` — Replace `.ur/` with `.ox/` (consolidate with existing `.ox/screen-dumps/` line if appropriate)
- [ ] `docs/config.md` — Replace all `~/.ur/` and `.ur/` path references with `~/.ox/` and `.ox/`

### Phase 9: Comments and doc strings

Update all comments and XML doc strings that reference `.ur` paths or the "ur" config section. These are documentation-only changes that keep the code self-consistent:

- [ ] `src/Ur/Configuration/UrOptions.cs:28,34` — Update comments referencing `.ur/`
- [ ] `src/Ur/Logging/UrFileLoggerProvider.cs:7` — Update doc comment
- [ ] `src/Ur/Logging/UrFileLogger.cs:8` — Update doc comment
- [ ] `src/Ur/Settings/ConfigurationScope.cs:5-6` — Update doc comment
- [ ] `src/Ur/Skills/SkillLoader.cs:7` — Update doc comment
- [ ] `src/Ur/Skills/SkillDefinition.cs:60` — Update doc comment
- [ ] `src/Ur/Skills/SkillFrontmatter.cs:31` — Update comment
- [ ] `src/Ur/Permissions/PermissionGrantStore.cs:11` — Update doc comment
- [ ] `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Update comments referencing `"ur"` section

## Structural considerations

This is a mechanical rename that preserves the existing architecture as-is. Two known structural issues are identified but explicitly scoped out per user request:

1. **No centralized constant for the directory name** — The `".ox"` string will appear in 40+ locations, same as `".ur"` did today. Centralizing into a constant or DI-injected value is deferred to a future plan. The "Hardcoded assumptions" table above catalogs every location.
2. **Path duplication between `Program.cs` and `Workspace`** — `Program.cs:43` independently constructs the workspace settings path that `Workspace.SettingsPath` already computes. This plan patches both with the same new string but doesn't eliminate the duplication. A future refactor could add a static `Workspace.GetSettingsPath(rootPath)` method.

## Impact assessment

- **Code paths affected**: Every startup path that resolves user data or workspace directories. Every settings read/write. Every skill expansion. Every log file write. Every container eval run.
- **Data impact**: Existing `~/.ur/settings.json` files on disk contain `{"ur": {"model": "..."}}`. After this change, the code reads `{"ox": {"model": "..."}}` from `~/.ox/settings.json`. Users must rename their directory and update the JSON structure. The `providers.json` and `permissions.jsonl` formats are unchanged.
- **Skill template variable breakage**: User-authored skill files that reference `${UR_SKILL_DIR}` or `${UR_SESSION_ID}` will pass through unexpanded after this change, silently producing broken prompts. Users must update their skill files to use `${OX_SKILL_DIR}` and `${OX_SESSION_ID}`.
- **Breaking change**: Yes — users must `mv ~/.ur ~/.ox`, update `settings.json` to use `"ox"` instead of `"ur"` as the top-level section key, and update any skill files using `${UR_*}` variables.

## Validation

- [ ] Run `dotnet test` — all existing tests must pass with updated paths
- [ ] Run `dotnet build` — confirm no compilation errors
- [ ] Manual: start Ox in a workspace and verify `.ox/sessions/` is created instead of `.ur/sessions/`
- [ ] Manual: verify `~/.ox/logs/ox-{date}.log` is created instead of `~/.ur/logs/ur-{date}.log`
- [ ] Manual: verify settings read/write works with the new `"ox"` section key

## Open questions

- The `UR_API_KEY_*` environment variable prefix and `UR_RUN_*` test gate variables use the "UR" prefix. These are NOT inside the `.ur`/`.ox` directories, so they are out of scope for this plan. Should they be renamed in a follow-up?
- The keyring service name `"ur"` (in `UrConfiguration.SecretService`) means existing API keys stored in the OS keyring are filed under service="ur". Renaming this would break access to stored keys. This is out of scope — flag it as a future consideration.
- The `.ox/` directory already exists in the workspace with `screen-dumps/` and `screenshots/`. After this change, session/skill/settings state will also live in `.ox/`. The gitignore should be updated to ignore `.ox/` entirely rather than specific subdirectories. Confirm this is acceptable.
