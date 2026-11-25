# Ox

Ox runs coding agent sessions side by side on your own machine in a web UI.
Follow and approve work from your desk or phone, and let sessions delegate
tasks to each other.

## Features

- Run Claude Code with your Claude subscription alongside Ur, Ox's built-in
  harness for OpenRouter models.
- Run sessions across your local repositories, or give a session its own Git
  worktree on a new branch.
- Let a Claude session start child sessions in Claude or Ur, message other
  sessions, and read their transcripts. Finished child turns report back
  automatically.
- Approve tool calls, answer questions, send follow-up prompts mid-turn, and
  browse each session's files beside its transcript.
- Keep sessions running after you close the browser. Transcripts survive
  restarts, and idle sessions park and resume automatically.
- Stay in sync across every open tab, or serve the UI over Tailscale to check
  in and approve work from your phone.

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
- `OPENROUTER_API_KEY` in `oxd`'s environment (or in `.env` when running via
  `make`) for Ur sessions
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

`oxd` requires `ox-mcp` in the same directory; export `OPENROUTER_API_KEY` before
starting `bin/oxd` directly if you use Ur (`.env` is loaded only by `make`). Run
`bin/oxd -help` or `bin/ox -help` for path, address, and parking-timeout flags.

## Configuration and state

Ox keeps its files in the XDG base directories (all overridable by flag):

- **Configuration:** `$XDG_CONFIG_HOME/ox/config.json` (default
  `~/.config/ox/config.json`) — written with the built-in Claude Code and Ur
  catalog on first start.
- **Database and Ur conversations:** `$XDG_STATE_HOME/ox/ox.db` and
  `$XDG_STATE_HOME/ox/ur/` (default `~/.local/state/ox/`).
- **Socket:** `$XDG_RUNTIME_DIR/ox/oxd.sock`, or `$XDG_STATE_HOME/ox/oxd.sock`
  (default `~/.local/state/ox/oxd.sock`) when `$XDG_RUNTIME_DIR` is unset.
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
