# Settings

Ox reads JSON settings when a session is created or loaded. Global settings live
at `$XDG_CONFIG_HOME/ox/settings.json`, or `$HOME/.config/ox/settings.json` when
`XDG_CONFIG_HOME` is unset. Workspace settings live at
`<workspace>/.ox/settings.json`.

Workspace values override global values one field at a time. `OX_MODEL`
overrides the model from either file. Ox rejects unknown keys and invalid values
instead of ignoring them.

```json
{
  "model": "openai/gpt-5.4",
  "max_tokens": 8192,
  "temperature": 0.2,
  "reasoning": {
    "enabled": true,
    "effort": "high",
    "exclude": false
  },
  "provider": {
    "order": ["OpenAI"],
    "allow_fallbacks": true,
    "data_collection": "deny",
    "max_price": {
      "prompt": 5,
      "completion": 20
    }
  }
}
```

All fields are optional, but a model must be supplied by one of the three
layers. `max_tokens` must be positive and `temperature` must be between `0` and
`2`. Reasoning support and effort names are validated against OpenRouter's model
catalog.

Provider routing supports `order`, `only`, `ignore`, `quantizations`, `sort`,
`data_collection`, `allow_fallbacks`, and `max_price.prompt` and
`max_price.completion`.

OpenRouter credentials are separate from settings. Ox first checks
`OPENROUTER_API_KEY`, then the operating-system keyring. Run `ox login` to store
a verified key in the keyring. `OX_LOG_LEVEL` controls process logging, and
`OX_OPENROUTER_BASE_URL` overrides the provider endpoint for development.
