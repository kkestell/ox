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

## Development

Run the app from `ui/`:

```sh
cd ui
npm install
npm run tauri dev
```

Build a release bundle:

```sh
npm run tauri build
```

Build and test every crate from the repository root:

```sh
cargo build --workspace
cargo test --workspace
```

Run live ACP smoke checks with an installed, authenticated Claude Code or
OpenCode CLI:

```sh
make smoke-claude     # haiku
make smoke-opencode   # openai/gpt-6-luna
```

These opt-in checks build the required Rust binaries and exercise streaming,
disconnect/reconnect and replay, conversation continuity, tool permissions,
cancellation, and process cleanup through `oxd` and `oxctl`. They invoke real
models and are separate from `cargo test`. Each run prints the temporary
directory containing its results, events, logs, and test project.

To select a model explicitly or change the default 120-second timeout per phase:

```sh
python3 scripts/smoke-acp.py claude --model haiku --timeout 180
python3 scripts/smoke-acp.py opencode --model openai/gpt-6-luna
```

The Make shortcuts also accept `CLAUDE_MODEL` or `OPENCODE_MODEL` overrides.
