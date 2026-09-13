# Settings

Ox reads JSON settings when a session is created or loaded. Global settings live
at `$XDG_CONFIG_HOME/ox/settings.json`, or `$HOME/.config/ox/settings.json` when
`XDG_CONFIG_HOME` is unset. Workspace settings live at
`<workspace>/.ox/settings.json`.

`models` maps an exact OpenRouter model ID to the complete set of request
settings Ox sends when that model is selected. `default_model` names the entry a
new session starts on. Ox rejects unknown keys and invalid values instead of
ignoring them.

```json
{
  "default_model": "openai/gpt-5.4",
  "models": {
    "openai/gpt-5.4": {
      "max_tokens": 8192,
      "temperature": 0.2,
      "reasoning": {
        "enabled": true,
        "effort": "high",
        "exclude": false
      },
      "provider": {
        "only": ["openai"],
        "allow_fallbacks": true,
        "data_collection": "deny",
        "max_price": {
          "prompt": 5,
          "completion": 20
        }
      }
    },
    "z-ai/glm-4.6": {
      "provider": { "only": ["z-ai"] }
    }
  },
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
  }
}
```

## Model profiles

At least one model must be configured, and `default_model` or `--model` must
name a configured one. A client offers exactly the configured models, and
selecting one applies that entry's whole set of request settings to subsequent
turns. Nothing carries over from the model selected before it, so provider
routing, sampling, and output limits are always the ones written beside the
model in use.

The reasoning option's `default` value means the selected model's configured
reasoning, or the provider's own default when that model configures none.
Choosing an explicit effort overrides it until the model changes.

Every configured model is resolved against OpenRouter's model catalog when a
session activates, so an unavailable model or an unsupported setting is reported
then rather than when it is selected. `max_tokens` must be positive and
`temperature` must be between `0` and `2`. Provider routing supports `order`,
`only`, `ignore`, `quantizations`, `sort`, `data_collection`, `allow_fallbacks`,
and `max_price.prompt` and `max_price.completion`. The
[provider routing guide](https://openrouter.ai/docs/guides/routing/provider-selection)
lists the provider slugs those lists accept.

## Precedence

The two `models` maps merge by exact model ID. A model only one file defines is
kept as written, and two definitions of the same model merge one field at a
time, with the workspace value winning. An explicit empty list such as
`"ignore": []` clears an inherited list rather than falling through to it. The
workspace `default_model` overrides the global one, and `--model` overrides
both. A durable session selection is a model ID, so reloading a session resolves
that model against the current files and fails clearly when it is gone.

The `process` object is global-only. A workspace settings file cannot choose
logging, tracing, the provider endpoint, or language servers. `log_level`
accepts `debug`, `info`, `warn`, or `error`. The command-line flags
`--log-level`, `--openrouter-base-url`, and `--trace` override their global
values.

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

Its other process flags are `--model`, which selects a configured model, and the
process flags described above. Put flags before the optional `login` command.
