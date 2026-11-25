# Ox

Ox runs coding agent sessions side by side on your own machine in a web UI.
Start Claude Code and Ur sessions across your repositories, follow and approve
their work from your computer or phone, and let sessions delegate tasks to each
other.

## Features

- Run Claude Code using your Claude subscription alongside Ur, Ox's built-in
  harness for OpenRouter models with shell commands. Choose the model, reasoning
  effort, and permission mode for each session, and monitor your Claude
  subscription limits under the composer.
- Organize sessions by workspace directory, or start a session in its own Git
  worktree on a new branch outside the repository so parallel tasks stay
  isolated.
- Let a Claude session start child sessions (in Claude or Ur), message other
  sessions, and read their transcripts. When a child finishes a turn, Ox sends
  its final reply back to the parent automatically.
- Follow streaming Markdown replies, reasoning, and expandable tool calls,
  attach images by pasting or dropping them, send a follow-up prompt while an
  agent works, and stop a turn at any time.
- Review and approve tool requests or answer Claude's multi-step questions from
  any open page.
- Browse each session's working directory in a live-updating file tree and read
  files beside the transcript with syntax highlighting, line numbers, and
  rendered Markdown preview.
- Keep workspaces, settings, and transcripts across restarts, and archive
  finished sessions without losing their history. Idle sessions park
  automatically and resume their conversation on the next prompt.
- Stay in sync across every open browser tab while sessions keep running with
  no tabs open. Expose the web UI over Tailscale to follow and steer sessions
  from your phone, including as an iPhone Home Screen app.

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
