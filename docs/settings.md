# Settings

Ur uses JSON settings files with two scopes:

| Scope     | Path                           | Priority                |
| --------- | ------------------------------ | ----------------------- |
| User      | `~/.ur/settings.json`          | Lower                   |
| Workspace | `$WORKSPACE/.ur/settings.json` | Higher (overrides user) |

## File Format

Nested JSON where top-level keys are namespaces:

```json
{
  "ur": {
    "model": "openai/gpt-4o"
  }
}
```

## Core Settings

| Key        | Type   | Description                                                                  |
| ---------- | ------ | ---------------------------------------------------------------------------- |
| `ur.model` | string | Selected model ID (e.g., `"anthropic/claude-3.5-sonnet"`, `"openai/gpt-4o"`) |

## Extension Settings

Extensions define their own settings in `manifest.lua`:

```lua
return {
  name = "my-extension",
  version = "1.0.0",
  settings = {
    ["my-extension.enabled"] = {
      type = "boolean",
      description = "Enable the extension"
    },
    ["my-extension.greeting"] = {
      type = "string",
      description = "Greeting message"
    }
  }
}
```

Settings keys should be dot-namespaced (e.g., `my-extension.enabled`). Schemas support JSON Schema types: `string`, `boolean`, `number`, `integer`, `array`, `object`.

When set, these appear in `settings.json` as nested JSON under the extension's namespace:

```json
{
  "my-extension": {
    "enabled": true,
    "greeting": "Hello, world!"
  }
}
```

## CLI Commands

```bash
# API key (stored in OS keyring)
ur config set-api-key sk-or-...
ur config clear-api-key

# Model selection
ur config set-model anthropic/claude-3.5-sonnet
ur config set-model openai/gpt-4o --scope workspace
ur config clear-model

# Arbitrary settings (value must be valid JSON)
ur config get ur.model
ur config set my-extension.enabled true
ur config set my-extension.threshold 0.5
ur config clear my-extension.enabled
```

The `--scope` flag accepts `user` (default) or `workspace`.

## Programmatic Access

- `UrConfiguration.GetSetting(key)` — read raw JSON value
- `UrConfiguration.GetStringSetting(key)` — typed string accessor
- `UrConfiguration.GetBoolSetting(key)` — typed boolean accessor
- `UrConfiguration.SetSettingAsync(key, value, scope)` — write value
- `UrConfiguration.ClearSettingAsync(key, scope)` — remove value
