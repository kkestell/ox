# Ox

A Cargo workspace containing the Ox desktop app, its session daemon, and the Claude
Code ACP bridge.

| Path                      | Description                                                                       |
| ------------------------- | --------------------------------------------------------------------------------- |
| `crates/ox/`              | Ox desktop app (Tauri 2)                                                          |
| `crates/claude-code-acp/` | ACP bridge for the Claude Code CLI ([README](crates/claude-code-acp/README.md))   |
| `crates/oxd/`             | Session daemon that owns ACP agent processes and serves Ox UIs over a Unix socket |
| `crates/ox-protocol/`     | oxd wire types and client                                                         |
| `crates/oxctl/`           | Command-line client for oxd                                                       |
| `ui/`                     | Ox frontend (TypeScript, Vite)                                                    |

See [architecture](docs/architecture.md), [where sessions run](docs/worktrees.md),
[ACP v2 support](docs/acp-v2.md), and
[research inherited from Hypercube](docs/research/README.md).

## Features

- **Bring your own ACP agent.** Run compatible ACP v2 agents in one app.
- **Claude Code bridge included.** A custom ACP wrapper brings Claude Code into
  Ox, including model and effort controls, tool permissions, and background tasks.
- **Multiple projects, parallel sessions.** Organize sessions by workspace and
  keep independent tasks running.
- **Isolated Git worktrees.** Give each task its own checkout, using a new,
  existing, or remote branch.
- **Sessions that survive the app.** Close and reopen Ox without interrupting
  agents; resume saved conversations after daemon restarts.
- **Live conversation and tool activity.** Follow streaming responses, thinking,
  tool calls, results, and background-task updates.
- **File and image attachments.** Send text, images, and PDFs to agents that
  support them.
- **Permission controls.** Approve or reject tool requests and interrupt running
  work.
- **Persistent history.** Retain conversations, tool output, and images
  independently of the agent process.
- **Archive and restore.** Release a session's worktree while keeping its
  conversation and branch.
- **Safe cleanup.** Detect unsaved work before removing worktrees and preserve
  branches containing unique commits.
- **CLI access.** Start, prompt, monitor, and manage sessions through `oxctl`.

## Development

Run the app from the repository root:

```sh
(cd ui && npm install)
make dev
```

`make dev` builds `oxd` and the Claude bridge and runs the app against its own
`oxd` on `target/dev/oxd.sock`, which it stops before building and after the
app exits. `oxctl` reaches that daemon with `OX_SOCKET` set to the same path:

```sh
OX_SOCKET="$PWD/target/dev/oxd.sock" cargo run -p oxctl -- ls
```

Build a release bundle from `ui/`:

```sh
npm run tauri build
```

Build every crate, then check formatting, lints, and tests across the Rust
workspace, the UI, and the scripts, from the repository root:

```sh
cargo build --workspace
make check
```

`make fix` applies the formatters and safe lint fixes.

Run the end-to-end tests of the app:

```sh
make e2e
```

`make e2e` builds the app with the `webdriver` feature, which embeds a
WebDriver server, and runs the WebdriverIO specs in `ui/e2e` against it. The
specs start sessions of `oxd-fake-agent`, a scripted ACP agent, with `oxctl`
and drive them through the app's UI. Sessions started from the app's New
Session menu run the Claude bridge, which starts `claude` only for a prompt,
and the specs send them none.

Run live ACP smoke checks with an installed, authenticated Claude Code CLI:

```sh
make smoke-claude     # claude-haiku-5-5
```

These opt-in checks build the required Rust binaries and exercise streaming,
disconnect/reconnect and replay, conversation continuity, tool permissions,
cancellation, and process cleanup through `oxd` and `oxctl`. They invoke real
models and are separate from `cargo test`. Each run prints the temporary
directory containing its results, events, logs, and test project.

To select a model explicitly or change the default 120-second timeout per phase:

```sh
python3 scripts/smoke-acp.py --model claude-haiku-5-5 --timeout 180
```

`make smoke-claude` also accepts a `CLAUDE_MODEL` override.
