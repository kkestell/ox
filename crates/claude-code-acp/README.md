# Claude Code ACP

A Rust [Agent Client Protocol (ACP)](https://agentclientprotocol.com/) bridge
for the Claude Code CLI. It lets ACP clients use an installed, authenticated
`claude` command through their existing Claude Code subscription.

Based on the original
[claude-code-acp](https://github.com/harukitosa/claude-code-acp) by Haruki Tosa.

```text
ACP client  <-- JSON-RPC over stdio -->  claude-code-acp  <-- subprocess -->  claude
```

## Build and run

Install and authenticate the
[Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code), then install
the bridge from this directory:

```sh
make install
```

This builds a release binary and copies it to `~/.local/bin/claude-code-acp`.

### Zed

In Zed, open **User → AI → General → External Agents → Add Custom Agent** and
fill in:

| Field                 | Value                                                                                          |
| --------------------- | ---------------------------------------------------------------------------------------------- |
| Agent Name            | `Claude Code`                                                                                  |
| Command               | The absolute path to the installed binary, for example `/Users/you/.local/bin/claude-code-acp` |
| Arguments             | Leave empty                                                                                    |
| Environment Variables | Add any optional `CLAUDE_ACP_*` settings you want                                              |

Then click **Save**. Use your actual home directory in the Command field; Zed
launches the executable directly, so a literal `~` may not be expanded.

For other ACP clients, configure the command path to the same installed binary.
To run from the workspace build directory without installing:

```sh
cargo build --release -p claude-code-acp
../../target/release/claude-code-acp
```

## Configuration

| Environment variable              | Meaning                                            | Default                            |
| --------------------------------- | -------------------------------------------------- | ---------------------------------- |
| `CLAUDE_ACP_MODEL`                | Initial model; clients can change it per session   | `claude-opus-5-5`                  |
| `CLAUDE_ACP_ALLOWED_TOOLS`        | Comma-separated tools passed as `--allowedTools`   | none                               |
| `CLAUDE_ACP_MAX_TURNS`            | Maximum Claude turns per prompt                    | unlimited                          |
| `CLAUDE_ACP_TIMEOUT`              | Per-prompt limit in ms, excluding permission waits | none                               |
| `CLAUDE_ACP_SKIP_PERMISSIONS`     | Pass `--dangerously-skip-permissions` when `true`  | `false`                            |
| `CLAUDE_ACP_STORE_DIR`            | Bridge session metadata directory                  | `~/.local/share/claude-code-acp`   |
| `CLAUDE_CONFIG_DIR`               | Claude Code directory for native session discovery | `~/.claude`                        |
| `CLAUDE_ACP_MCP_ALLOWED_COMMANDS` | Comma-separated allowed MCP executable names       | `node,npx,python3,python,deno,bun` |

The bridge removes `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` from the
Claude subprocess environment so an inherited API credential cannot change the
intended subscription authentication path.

## ACP support

The bridge supports `initialize` and `authenticate`, along with the session
methods described below.

### Sessions

Clients can create, load, list, and resume sessions with `session/new`,
`session/load`, `session/list`, and `session/resume`. Bridge session metadata is
stored in `~/.local/share/claude-code-acp/acp-sessions.json`, which several
bridge processes can share; each change is written under a lock file beside it.
A registry file that cannot be parsed is moved aside to
`acp-sessions.json.corrupt-<time>` and a new one is started. Native Claude
sessions are discovered from local project transcripts. `session/list` returns
up to 50 sessions per page and lists a bridge session under its own ID in place
of the Claude transcript it continues, so every listed ID can be loaded. Loading
or resuming a session requires its original `cwd` and applies the request's MCP
servers. New sessions start fresh; continuing a conversation requires loading or
resuming its session ID. A failed resumed prompt reports the error without
retrying in a fresh conversation.

### Prompts and streaming

`session/prompt` accepts text content blocks and streams message, thought, and
tool call updates. Image and audio prompts are not supported.

Each prompt runs `claude` in its own process group. Clients can stop a prompt
with `session/cancel`, including immediately after sending it or while a tool
permission request is open. Cancelling, a timeout, or an error sends `SIGTERM`
to the group and `SIGKILL` two seconds later; when Claude exits, any processes
it left in the group are killed. Tool calls that started without a result are
reported as failed when a prompt is cancelled or fails. A cancelled or failed
prompt keeps its Claude conversation for the next prompt when Claude saved a
transcript for it.

A prompt that reaches `CLAUDE_ACP_MAX_TURNS` ends with the `max_turn_requests`
stop reason. Other Claude errors are reported as an `Error:` message and the
prompt ends with `end_turn`.

The bridge forwards stdio MCP server definitions to Claude.

### Models and modes

Model selection is exposed through `configOptions` and
`session/set_config_option`.

The mode selector offers `auto`, `manual`, `acceptEdits`, and `plan`. The bridge
passes the selected mode to Claude as `--permission-mode` on each prompt. When
`CLAUDE_ACP_SKIP_PERMISSIONS=true`, it passes
`--dangerously-skip-permissions` instead.

### Tool permissions

When Claude asks for tool permission, the bridge sends an ACP permission request
to the client and forwards the decision to the running Claude process. The
client can offer `Allow always` when Claude supplies a rule to save.

## Development

```sh
cargo fmt -p claude-code-acp --check
cargo test -p claude-code-acp
cargo clippy -p claude-code-acp --all-targets -- -D warnings
```

The integration tests use a fake `claude` executable to exercise ACP stdio,
streaming, persistence, and cancellation without invoking a model.
