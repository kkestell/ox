# Ox

Ox is a coding agent written in Go that speaks ACP v1 over standard input and
output. It works with any ACP-compatible client and uses OpenRouter as its model
provider.

Ox ships as a single self-contained 11 MB binary.

## Features

Sessions:

- Saved sessions that can be reopened, resumed, and managed.
- Recover a turn paused on a permission request.
- Cancel in-progress model, tool, or shell work.
- Run turns in separate sessions concurrently.
- Keep long sessions within the model's context limits.

Modes and models:

- Plan, code, and autonomous modes.
- Choose a configured model and reasoning level for each session.
- Configure provider routing, sampling, and output limits per model.

Coding tools:

- Read, search, edit, and create files in the workspace.
- Run shell commands.
- Query configured language servers for definitions, references, symbols, and
  diagnostics.
- Ask clarifying questions, track tasks, and delegate bounded tasks to parallel
  subagents.
- Fetch public web pages and store workspace memory.

Permissions and safety:

- Review permission prompts before tool calls run, or use autonomous mode to run
  without prompts.
- Reuse approved tool calls within a session.
- Keep file access inside the workspace.
- Restrict tools available in plan mode.

Extensibility:

- Load reusable Agent Skills from the workspace.
- Connect client-configured MCP servers over stdio and Streamable HTTP.
- Use search tools supplied by an MCP server.

Configuration and diagnostics:

- Store credentials in the OS keyring or a private local file with `ox login`.
- Configure Ox globally or per workspace.
- Write an optional diagnostic JSONL trace.

## Getting started

Ox targets Unix-like systems. Install a released binary as described in the
[installation guide](docs/installation.md), then store an OpenRouter credential
and configure at least one model:

```sh
ox login
```

Configure your models in `$XDG_CONFIG_HOME/ox/settings.json` and name one with
`default_model`, or pass `--model` to pick one of them. Start Ox from your ACP
client; see the [Zed guide](docs/zed.md) for a worked setup.

## Documentation

- [Installation](docs/installation.md) — released binaries and upgrades.
- [Settings](docs/settings.md) — configuration fields and precedence.
- [Web access](docs/web.md) — fetching and search.
- [Zed setup](docs/zed.md) — running Ox from an ACP client.
- [Browser client](docs/browser.md) — starting the local browser client safely.
- [Specification](docs/spec.md) — target product behavior and limits.

## Development

Building from a checkout requires Go 1.26.4 or later.

- `make install` — install the current checkout to `$HOME/.local/bin/ox`.
- `make run` — install the current checkout, build the browser client, and start
  it.
- `make check` — documentation formatting, Go checks, client type and unit
  checks, and Go unit tests.
- `make check-all` — every `make check` gate plus race-enabled Go tests and the
  browser-process suite.
- `make test-eval` — the fake-provider evaluation smoke test.
- `make format` — format Go and Markdown.

Design and build notes live in [`eng/`](eng/).
