# Claude Code ACP

A Rust [Agent Client Protocol (ACP)](https://agentclientprotocol.com/) bridge
for the Claude Code CLI. It lets ACP clients use an installed, authenticated
`claude` command through their existing Claude Code subscription. It speaks
the draft ACP protocol v2, which [`docs/acp-v2.md`](../../docs/acp-v2.md)
charts.

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

The bridge advertises the baseline v2 session methods, `session/new`,
`session/list`, `session/resume`, `session/close`, `session/prompt`,
`session/cancel`, and `session/update`, with `session/set_config_option`, the
`image` and `embeddedContext` prompt capabilities, and stdio MCP servers.
[`docs/acp-v2.md`](../../docs/acp-v2.md) charts every ACP v2 feature and the
bridge's support of it.

### Sessions

Clients create, list, resume, and close sessions with `session/new`,
`session/list`, `session/resume`, and `session/close`. A session takes
prompts once it is new or resumed, until it is closed. Bridge session
metadata is stored in `~/.local/share/claude-code-acp/acp-sessions.json`,
which several bridge processes can share; each change is written under a lock
file beside it. A registry file that cannot be parsed is moved aside to
`acp-sessions.json.corrupt-<time>` and a new one is started. Native Claude
sessions are discovered from local project transcripts. `session/list` returns
up to 50 sessions per page and lists a bridge session under its own ID in place
of the Claude transcript it continues, so every listed ID can be resumed.
Resuming a session requires its original `cwd` and applies the request's MCP
servers. New sessions start fresh; continuing a conversation requires resuming
its session ID. A failed resumed prompt reports the error without retrying in
a fresh conversation.

`session/resume` with `replayFrom: {"type": "start"}` replays the conversation
before it answers. The bridge reads the `user` and `assistant` entries of the
session's Claude transcript and converts them with the same code that converts
a live prompt's output, so a replayed turn produces the same message, thought,
and tool call updates, with a `user_message` for each prompt. Prompt resource
links and embedded text replay as the text Claude received, and an embedded
PDF as a `document` resource. Claude keeps each subagent's transcript in the
session's `subagents` directory, beside a `.meta.json` naming the tool call
that started it, and the bridge replays it under that tool call as a live
subagent shows. Tool calls without a result end as failed. After the replay,
or at once when the request has no `replayFrom`, the bridge sends the
session's current `state_update`, and then answers. Any other `replayFrom`
cursor is rejected with `-32602`.

`session/close` interrupts the session's `claude`, closes its stdin, and
answers once `claude` has exited. The session stays in the registry, so
`session/resume` opens it again. Closing a session that is not open succeeds.

### Prompts and streaming

`session/prompt` answers once the prompt enters the conversation: the bridge
sends the prompt as a `user_message` with a new `messageId` and answers with
that ID. The turn's output follows as `session/update` notifications:
`agent_message_chunk` and `agent_thought_chunk` while `claude` streams, and
`agent_message` and `agent_thought` for text it did not stream. Every tool
call reports through `tool_call_update`. The first update for a tool call ID
carries its name, title, kind, status `pending`, and input. The title is
Markdown: the tool's name followed by the first line of its main argument as
inline code. A tool call's result sets its `content`: the result's text and
images, with any other block Claude returns shown as its JSON.

A subagent's lines carry `parent_tool_use_id`, the tool call that started it.
Its messages and streamed text stay out of the conversation; each tool it
calls adds a line with that tool's title to the parent tool call's `content`,
ahead of the parent's result, which carries the subagent's answer. A tool
call of the subagent that asks permission appears as a tool call of its own.

Prompts can contain these content blocks, which reach Claude in order:

| Block                     | Sent to Claude as                                      |
| ------------------------- | ------------------------------------------------------ |
| Text                      | Text                                                   |
| Image                     | An image (PNG, JPEG, GIF or WebP)                      |
| Resource link             | A Markdown link, `[@name](uri)`, Claude can open       |
| Embedded text resource    | Text in a `<context ref="uri">` block                  |
| Embedded image or PDF     | An image or a document                                 |

A session's first prompt starts `claude`, and the session keeps that process:
the bridge holds its stdin open and sends each later prompt to it as a
message with a `uuid`, the prompt's `messageId`. A prompt sent while a turn
runs waits for Claude to take it. The bridge takes the prompt as inserted once
Claude says it started the prompt, with a `command_lifecycle` line in state
`started` or, under `--replay-user-messages`, by echoing the prompt with
`isReplay`. Claude stays running between prompts, so background tasks it
started keep running. When one reports back, Claude starts a turn by itself,
and the bridge sends that turn's updates and permission requests to the client
as it does a prompt's. `session/cancel` stops such a turn too.

The bridge reports what Claude is doing with `state_update`. A session is idle
until Claude takes a prompt or starts a turn, which makes it `running`. A
permission request that blocks the turn makes it `requires_action`, and it is
`running` again once the request is answered. When a turn ends the state is
`idle`, with the turn's `stopReason`: `end_turn`, `max_turn_requests` for a
Claude result of `error_max_turns`, `cancelled`, or `error` for any other
Claude error, with the error's message as its details. A `claude` that exits
during a turn ends it with `error`, carrying `claude`'s stderr, or with
`cancelled` when the session is closing.

Clients can stop a turn with `session/cancel`, including immediately after
sending a prompt or while a tool permission request is open. The bridge
interrupts Claude, which stops the turn and the tools it is running, withdraws
an open permission request, drops the prompts it has not taken, which are
answered with `-32800`, and waits for the next prompt. Tool calls that started
without a result are reported as failed when a turn ends.

Claude runs in the bridge's process group, so a client that stops the bridge
by killing its process group also stops Claude and the processes Claude
started. Claude also exits when the bridge does, because its stdin closes.
When `claude` exits while the bridge runs, the next prompt starts another
that resumes the conversation, if Claude saved a transcript for it. A prompt
that was waiting for Claude to take it reports the exit as its error.

The bridge forwards stdio MCP server definitions to Claude. An MCP server's
`command` is an absolute path to an executable whose name is in
`CLAUDE_ACP_MCP_ALLOWED_COMMANDS`.

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
request's tool call carries the tool's title and input. A call without an
argument to show takes Claude's own title for the request, escaped as Markdown
text. The client can offer `Allow always` when Claude supplies a rule to save.

## Development

```sh
cargo fmt -p claude-code-acp --check
cargo test -p claude-code-acp
cargo clippy -p claude-code-acp --all-targets -- -D warnings
```

The integration tests use a fake `claude` executable to exercise ACP stdio,
streaming, persistence, and cancellation without invoking a model.
