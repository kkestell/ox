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
    "trace": "/absolute/path/to/ox-trace.jsonl",
    "language_servers": {
      "gopls": {
        "command": "gopls",
        "extensions": ["go"]
      },
      "typescript": {
        "command": "typescript-language-server",
        "args": ["--stdio"],
        "extensions": [".ts", ".tsx"]
      }
    }
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
logging, tracing, the provider endpoint, or language servers. `log_level`
accepts `debug`, `info`, `warn`, or `error`. The command-line flags
`--log-level`, `--openrouter-base-url`, and `--trace` override their global
values.

All fields are optional, but a model must be supplied by one of the three
layers. `max_tokens` must be positive and `temperature` must be between `0` and
`2`. Reasoning support and effort names are validated against OpenRouter's model
catalog.

Provider routing supports `order`, `only`, `ignore`, `quantizations`, `sort`,
`data_collection`, `allow_fallbacks`, and `max_price.prompt` and
`max_price.completion`.

## Language servers

`process.language_servers` maps a name you choose to the command Ox runs and the
file extensions it answers for. `command` and at least one extension are
required; `args` is optional. Extensions are matched case-insensitively with or
without a leading dot, and two servers cannot claim the same extension, because
the file being queried is what selects the server.

A server is started the first time a session queries a file it owns, and is shut
down when that session closes. Each session runs its own copy, in the session's
workspace, with the same privileges as Ox. Ox never installs a language server:
a command that is missing or fails to start is reported as a failed tool call.

The language tools are `lsp_definition`, `lsp_references`,
`lsp_document_symbols`, `lsp_workspace_symbols`, and `lsp_diagnostics`. They are
always offered to the model, so a query against an unconfigured extension is
answered with a clear failure rather than a missing tool. Positions are 1-based
lines and columns counted in Unicode characters, and results are
workspace-relative. When the client owns the file contents, queries use the
client's copy of a file rather than what is on disk.

OpenRouter credentials are separate from settings. Run `ox login` to verify and
store a key in the operating-system keyring. For a headless process, pass
`--credential-file /absolute/path/to/credential`; the file must be owned by the
current user, readable only by that user, regular rather than symlinked, and
contain one nonempty credential. A file credential cannot be changed by login or
logout. `--no-keyring` disables keyring reads and writes while leaving an
explicit credential file available.

Its other process flags are `--model`, which overrides the activation model, and
the process flags described above. Put flags before the optional `login`
command.
