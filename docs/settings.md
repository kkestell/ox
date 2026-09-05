# Settings

Ox reads JSON settings when a session is created or loaded. Global settings live
at `$XDG_CONFIG_HOME/ox/settings.json`, or `$HOME/.config/ox/settings.json` when
`XDG_CONFIG_HOME` is unset. Workspace settings live at
`<workspace>/.ox/settings.json`.

Workspace values override global values one field at a time. `--model` overrides
the model from either file. Ox rejects unknown keys and invalid values instead
of ignoring them.

```json
{
  "model": "openai/gpt-5.4",
  "process": {
    "log_level": "info",
    "openrouter_base_url": "https://openrouter.ai/api/v1",
    "trace": "/absolute/path/to/ox-trace.jsonl"
  },
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

The `process` object is global-only. A workspace settings file cannot choose
logging, tracing, or the provider endpoint. `log_level` accepts `debug`, `info`,
`warn`, or `error`. The command-line flags `--log-level`,
`--openrouter-base-url`, and `--trace` override their global values.

All fields are optional, but a model must be supplied by one of the three
layers. `max_tokens` must be positive and `temperature` must be between `0` and
`2`. Reasoning support and effort names are validated against OpenRouter's model
catalog.

Provider routing supports `order`, `only`, `ignore`, `quantizations`, `sort`,
`data_collection`, `allow_fallbacks`, and `max_price.prompt` and
`max_price.completion`.

OpenRouter credentials are separate from settings. Run `ox login` to verify and
store a key in the operating-system keyring. For a headless process, pass
`--credential-file /absolute/path/to/credential`; the file must be owned by the
current user, readable only by that user, regular rather than symlinked, and
contain one nonempty credential. A file credential cannot be changed by login or
logout. `--no-keyring` disables keyring reads and writes while leaving an
explicit credential file available.

Ox does not read `OX_*` or `OPENROUTER_*` variables. Its other process flags are
`--model`, which overrides the activation model, and the process flags described
above. Put flags before the optional `login` command.
