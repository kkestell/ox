# Ox

Ox runs coding agent sessions, in Claude Code and Ur, side by side in a web UI.

## What Ox does

- **Claude Code and Ur side by side.** Drive local Claude Code sessions (billed
  to your Claude subscription) alongside Ur, Ox's built-in harness for
  OpenRouter models with shell commands.
- **Workspaces and parallel sessions.** Register workspace directories and run
  independent sessions with streaming Markdown transcripts, reasoning, tool
  calls, image attachments, and interactive approval and question forms.
- **Session worktrees.** Optionally start a session in its own git worktree on a
  new branch outside the repository.
- **Sessions working together.** Claude sessions can list, read, message, and
  start child sessions through Ox's built-in MCP server, with automatic child
  turn reports.
- **Live workspace browser.** Inspect each session's working directory in a
  live-updating file tree and syntax-highlighted file viewer with Markdown
  preview.
- **Persistence and parking.** Workspaces, settings, and transcripts live in
  SQLite and survive restarts; idle sessions park automatically and resume on
  the next prompt.
- **Synced pages and remote access.** Every open browser tab stays in sync. Set
  `tailscale_ip` in the configuration to also serve the web UI over Tailscale,
  including as an iPhone Home Screen app.

See [docs/functional-requirements.md](docs/functional-requirements.md) for the
full behavior Ox guarantees.

## Architecture

Ox builds three binaries into `bin/`:

- **`oxd`** — daemon that owns workspaces, sessions, persistence, and harness
  processes, and serves a newline-delimited JSON-RPC 2.0 API over a Unix socket.
- **`ox`** — HTTP server that embeds the Solid 2.0 web client and relays each
  browser tab to `oxd` over SSE and `/rpc`.
- **`ox-mcp`** — stdio MCP forwarder placed beside `oxd` so harnesses can reach
  Ox's inter-session tools.

See [docs/architecture.md](docs/architecture.md) and
[docs/protocol.md](docs/protocol.md) for details.

## Requirements

- Go (see `go.mod` for the version)
- Node.js and npm (for building the web UI)
- Claude Code (`claude` on `PATH`) for Claude sessions
- `OPENROUTER_API_KEY` (in the environment or `.env`) for Ur sessions
- `git` for worktree sessions

## Quick start

Run `oxd` and `ox` together in development mode:

```sh
make dev
```

This builds the web UI and Go binaries into `bin/` and serves the UI at
`http://localhost:8080`. Press Enter in the `make dev` terminal to rebuild and
restart whichever binary changed. In a linked git worktree, `make dev` keeps its
state under `.state/` and serves on a free port.

To build and run the binaries directly:

```sh
make build
bin/oxd   # in one terminal
bin/ox    # in another terminal
```

`oxd` requires `ox-mcp` in the same directory. Run `bin/oxd -help` or
`bin/ox -help` for path, address, and parking-timeout flags.

## Configuration and state

Ox keeps its files in the XDG base directories (all overridable by flag):

- **Configuration:** `$XDG_CONFIG_HOME/ox/config.json` (default
  `~/.config/ox/config.json`) — written with the built-in Claude Code and Ur
  catalog on first start.
- **Database and Ur conversations:** `$XDG_STATE_HOME/ox/ox.db` and
  `$XDG_STATE_HOME/ox/ur/` (default `~/.local/state/ox/`).
- **Socket:** `$XDG_RUNTIME_DIR/ox/oxd.sock`, or beside `ox.db` when unset.
- **Session worktrees:** `$XDG_DATA_HOME/ox/worktrees/` (default
  `~/.local/share/ox/worktrees/`).

See [docs/protocol.md](docs/protocol.md#configuration) for the configuration
schema, including `tailscale_ip` and `max_child_depth`.

## Development

```sh
make build   # build the web UI and Go binaries into bin/
make test    # run linters, Go tests (with -race), and web unit tests
make e2e     # run Playwright end-to-end tests against the fake harness
make live    # run live tests against Claude Code (Haiku) and Ur (OpenRouter)
make docs    # format docs/ with dprint
make hooks   # install the git pre-commit hook
```

## Documentation

- [docs/functional-requirements.md](docs/functional-requirements.md) — user-facing behavior
- [docs/architecture.md](docs/architecture.md) — package layout, concurrency, and web UI structure
- [docs/protocol.md](docs/protocol.md) — `oxd` JSON-RPC protocol, configuration, and MCP tools
- [docs/harnesses/claude-code.md](docs/harnesses/claude-code.md) — observed Claude Code CLI behavior
- [AGENTS.md](AGENTS.md) — engineering conventions and workflow
