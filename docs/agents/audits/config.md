# Runtime Configuration Audit — Ox

## Fully Working Runtime Modification

| Mechanism                  | Trigger                    | Settings Affected                         |
| -------------------------- | -------------------------- | ----------------------------------------- |
| `/model <id>`              | Slash command              | `ox.model` (persisted)                    |
| `/connect` wizard          | Slash command or first-run | `ox.model`, API key (keyring)             |
| Permission prompt approval | Tool call in agent loop    | Permission grants (persisted to `.jsonl`) |

## Partially Implemented / Unused Infrastructure

| Mechanism                               | Status                                                                                                                               | Location                                                        |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------- |
| **`/set` command**                      | Registered in autocomplete, returns "not yet implemented"                                                                            | `BuiltInCommandRegistry.cs:26`, `UrSession.cs:425`              |
| **`/clear` command**                    | Registered in autocomplete, returns "not yet implemented"                                                                            | `BuiltInCommandRegistry.cs:22`, `UrSession.cs:425`              |
| **`SetBoolSetting` / `GetBoolSetting`** | Full API exists on `UrConfiguration`, tested, but **zero production callers**                                                        | `UrConfiguration.cs:180-189, 210-214`                           |
| **`ClearSelectedModel`**                | API exists, **no caller in production or test code**                                                                                 | `UrConfiguration.cs:159-161`                                    |
| **`ClearApiKey`**                       | API exists, **only called by test code**                                                                                             | `UrConfiguration.cs:149-152`                                    |
| **`ConfigurationScope.Workspace`**      | `SettingsWriter` fully supports workspace-scoped writes, tested, **no UI exposes the scope choice**                                  | `SettingsWriter.cs`, tested in `SettingsLoaderTests.cs:129-147` |
| **`ox.turnsToKeepToolResults`**         | Bindable property in `UrOptions`, read on every turn via `IOptionsMonitor<UrOptions>`, **no command or UI to change it**, no schema registered | `UrOptions.cs`, `UrConfiguration.cs`                   |

## Key Takeaways

1. **`/set` is the big unused feature.** The entire generic settings infrastructure exists (`SetSetting`, `SetStringSetting`, `SetBoolSetting`, `ClearSetting`, `GetSetting`), is tested, and works — but the `/set` slash command is a stub that just says "not yet implemented."

2. **`/clear` is another stub.** Registered but unimplemented.

3. **`ox.turnsToKeepToolResults` is a hidden setting.** It's read from config on every turn but has no UI surface. Users could manually edit `settings.json` to change it, and the standard `IOptionsMonitor<UrOptions>` would pick it up after a `Reload()` is triggered by a write through `SettingsWriter`.

4. **No file watching, signals, API endpoints, or hot-reload mechanisms exist.** The only way settings refresh is via the explicit `IConfigurationRoot.Reload()` call after a write through `SettingsWriter`.

## Settings Persistence and Reload Mechanism

The write-and-reload pipeline in `SettingsWriter`:

1. **Validate** against `SettingsSchemaRegistry` — only `ox.model` has a registered schema currently (`ServiceCollectionExtensions.cs`)
2. **Write** to the appropriate JSON file (user or workspace scope)
3. **Reload** via `IConfigurationRoot.Reload()` — this is the only reload trigger
4. **Options pipeline** — standard `IOptionsMonitor<UrOptions>` backed by `IConfiguration`. Changes propagate after `Reload()`.

Settings files:

| File                      | Purpose                                   | Written at Runtime?               |
| ------------------------- | ----------------------------------------- | --------------------------------- |
| `~/.ox/settings.json`     | User-level settings                       | Yes — via `SettingsWriter`        |
| `.ox/settings.json`       | Workspace-level settings (overrides user) | Yes — via `SettingsWriter`        |
| `providers.json`          | Provider/model definitions                | No — static, read-only at startup |
| `.ox/permissions.jsonl`   | Workspace-scoped permission grants        | Yes — via `PermissionGrantStore`  |
| `~/.ox/permissions.jsonl` | Always-scoped permission grants           | Yes — via `PermissionGrantStore`  |

## Environment Variables (Headless Mode Only)

Pattern: `UR_API_KEY_{PROVIDER}` (e.g. `UR_API_KEY_GOOGLE`, `UR_API_KEY_OPENAI_COMPATIBLE`)

- Selected via `Program.cs` at startup for headless/container mode
- Read lazily on each `GetSecret()` call (not cached)
- `SetSecret` and `DeleteSecret` are no-ops in `EnvironmentKeyring`
- Not configurable at runtime — keyring implementation is fixed for process lifetime

## CLI Arguments (Startup-Only Overrides)

| Flag                         | What It Overrides                                       | Runtime Mutable? |
| ---------------------------- | ------------------------------------------------------- | ---------------- |
| `--fake-provider <scenario>` | Sets `SelectedModelOverride` to `fake/{scenario}`       | No               |
| `--headless`                 | Selects headless runner path                            | No               |
| `--yolo`                     | Auto-grants all tool permissions                        | No               |
| `--prompt <msg>`             | Single prompt for headless mode                         | No               |
| `--model <model-id>`         | Sets `SelectedModelOverride` (ephemeral, not persisted) | No               |
| `--max-iterations <n>`       | Caps agent loop iterations                              | No               |

## Settings Schema Registry

Only **one** schema is registered in production:

| Key        | Schema               | Where Registered                     |
| ---------- | -------------------- | ------------------------------------ |
| `ox.model` | `{"type": "string"}` | `ServiceCollectionExtensions.cs:227` |

The `ox.turnsToKeepToolResults` key has no registered schema — unknown keys are allowed per `SettingsWriter.cs:128-129`.

## Not Present

| Mechanism                                   | Status          |
| ------------------------------------------- | --------------- |
| File watching / hot-reload                  | Not implemented |
| API endpoints / RPC / HTTP server           | Not present     |
| Signal-based config reload (SIGHUP, SIGUSR) | Not present     |
| Runtime environment variable changes        | Not detected    |
| Runtime skill loading/reloading             | Not implemented |
