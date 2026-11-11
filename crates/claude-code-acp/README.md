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

`session/load` replays the conversation before it answers. The bridge reads the
`user` and `assistant` entries of the session's Claude transcript and converts
them with the same code that converts a live prompt's output, so a replayed
turn produces the same message, thought, and tool call updates, plus user
message chunks for the prompts. Prompt resource links and embedded text replay
as the text Claude received, and an embedded PDF as a `document` resource.
Tool calls without a result end as failed. `session/resume` opens a session
without replaying it.

### Prompts and streaming

`session/prompt` streams message, thought, and tool call updates. Prompts can
contain these content blocks, which reach Claude in order:

| Block                     | Sent to Claude as                                      |
| ------------------------- | ------------------------------------------------------ |
| Text                      | Text                                                   |
| Image                     | An image (PNG, JPEG, GIF or WebP)                      |
| Resource link             | A Markdown link, `[@name](uri)`, Claude can open       |
| Embedded text resource    | Text in a `<context ref="uri">` block                  |
| Embedded image or PDF     | An image or a document                                 |

The bridge advertises the `image` and `embeddedContext` prompt capabilities.

A session's first prompt starts `claude`, and the session keeps that process:
the bridge holds its stdin open and sends each later prompt to it as a
message. Claude stays running between prompts, so background tasks it started
keep running. When one reports back, Claude starts a turn by itself, and the
bridge sends that turn's updates and permission requests to the client as it
does a prompt's. `session/cancel` stops such a turn too.

ACP reports a turn only as the answer to a prompt, so the bridge also sends
the extension notification `_ox/agent_state`, with the `sessionId` and a
`state` of `running` or `idle`, each time Claude starts or stops working. A
prompt's response reaches the client before anything Claude does after that
prompt's turn.

Clients can stop a prompt with `session/cancel`, including immediately after
sending it or while a tool permission request is open. The bridge interrupts
Claude, which stops the turn and the tools it is running, withdraws an open
permission request, and waits for the next prompt. Tool calls that started
without a result are reported as failed when a turn ends.

Claude runs in the bridge's process group, so a client that stops the bridge
by killing its process group also stops Claude and the processes Claude
started. Claude also exits when the bridge does, because its stdin closes.
When `claude` exits while the bridge runs, the next prompt starts another
that resumes the conversation, if Claude saved a transcript for it, and a
prompt that was waiting reports the exit as its error.

A prompt whose result Claude reports as `error_max_turns` ends with the
`max_turn_requests` stop reason. Other Claude errors are reported as an `Error:` message and the
prompt ends with `end_turn`.

The bridge forwards stdio MCP server definitions to Claude.

### Models and modes

Model selection is exposed through `configOptions` and
`session/set_config_option`.

The mode selector offers `auto`, `manual`, `acceptEdits`, and `plan`. Claude
starts with the session's model and mode. A change made while it runs is sent
to it, and `session/set_config_option` returns Claude's error if it refuses
the change.

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
