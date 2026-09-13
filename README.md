# Ox

Ox is a coding agent written in Go that speaks ACP v1 over standard input and
output. It works with any ACP-compatible client and uses OpenRouter as its model
provider.

## Features

Sessions:

- ACP v1 support.
- Durable session history with replay.
- Load, resume, list, close, and delete.
- Recovery of a turn paused on a permission request.
- Cancellation of provider, tool, and shell work.
- Concurrent turns in separate sessions.
- Compaction for long conversations.

Modes and models:

- Code, auto, and plan modes.
- Per-session selection among the models you configure.
- Complete per-model provider routing, sampling, and output limits.
- Reasoning effort selection, including provider defaults.

Built-in tools:

- `question` — ask the user a question.
- `skill` — load a workspace skill.
- `todo` — maintain the session plan.
- `subagent_start`, `subagent_send`, `subagent_stop`, `subagent_list`, and
  `subagent_wait` — coordinate concurrent turn-scoped child agents.
- `read_file`, `glob`, and `grep` — inspect the workspace.
- `write_file` and `edit_file` — change workspace files.
- `shell` — run commands.
- `web_fetch` — fetch public web pages.
- `memory_search`, `memory_write`, and `memory_delete` — manage workspace
  memory.
- `lsp_definition`, `lsp_references`, `lsp_document_symbols`,
  `lsp_workspace_symbols`, and `lsp_diagnostics` — query configured language
  servers.

Permissions and safety:

- Per-operation permission prompts.
- Auto mode execution without prompts.
- Reusable session grants, including parsed shell-command grants.
- Read-before-edit evidence.
- Workspace path confinement.
- Plan mode tool restrictions.

Extensibility:

- MCP servers over stdio and Streamable HTTP.
- Search through MCP.

Operations:

- OS-keyring credentials and `ox login`.
- Owner-only credential files.
- Global and workspace settings.
- Optional diagnostic JSONL trace.
- Git worktree workspaces.

## Getting started

Requirements: a Unix-like system, Go 1.26.4 or later, an ACP client, an
OpenRouter model ID, and an OpenRouter credential.

```sh
make install            # installs ox to $HOME/.local/bin/ox
~/.local/bin/ox login   # verifies and stores an OpenRouter key
```

Configure your models in `$XDG_CONFIG_HOME/ox/settings.json` and name one with
`default_model`, or pass `--model` to pick one of them. Start Ox from your ACP
client; see the [Zed guide](docs/zed.md) for a worked setup.

## Documentation

- [Settings](docs/settings.md) — configuration fields and precedence.
- [Web access](docs/web.md) — fetching and search.
- [Zed setup](docs/zed.md) — running Ox from an ACP client.
- [Specification](docs/spec.md) — target product behavior and limits.

## Development

- `make check` — formatting, vet, static analysis, and the race-enabled tests.
- `make test-eval` — the fake-provider evaluation smoke test.
- `make format` — format Go and Markdown.

Design and build notes live in [`eng/`](eng/).
