# Ox

Ox is an ACP-native coding agent. The Ox ACP server, `ox-server`, can be used
with any ACP-compatible client, and the TUI, `ox`, can be used with any
ACP-compatible server. A release contains both binaries; keep them in the same
directory.

## Quick Start

Download the [latest release](https://github.com/kkestell/ox/releases), then
extract `ox` and `ox-server` into a directory on your `PATH`, such as
`~/.local/bin`:

```sh
tar -xzf ox-*.tar.gz -C ~/.local/bin
```

Save your OpenRouter API key to the system keychain, sign in with your ChatGPT
account to use OpenAI models on your Codex subscription, or both:

```sh
ox auth login openrouter
ox auth login openai
```

Run the Ox TUI:

```sh
cd /path/to/project
ox
```

Configure Zed to use Ox as an external agent. Add the following to
`~/.config/zed/settings.json`:

```sh
"agent_servers": {
  "Ox": {
    "type": "custom",
    "command": "/Users/username/.local/bin/ox",
    "args": ["acp"],
  }
}
```

## Usage

### `ox`

```text
ox [--dir <DIR>] [--server <SERVER>] [--resume <SESSION>] [<PROMPT>]
```

Starts the TUI. `--dir` selects the workspace and defaults to the current
directory. `--server` selects a server by its configured name; it is required
when more than one server is configured. `--resume` opens the saved session with
that ID instead of a new session. A prompt is sent as the first message.

### `ox run`

```text
ox run [--dir <DIR>] [--model <MODEL>] [--effort <EFFORT>] <PROMPT>
```

Put options before the prompt. Use `--` before a prompt beginning with `-`.

Runs one noninteractive prompt and prints the final answer. `--dir` selects the
workspace and defaults to the current directory. `--model` accepts a qualified
model ID such as `openrouter:deepseek/deepseek-v4.1-flash` or
`openai:gpt-5.6-luna` and overrides the configured model. `--effort` defaults to
`default` and accepts `default`, `none`, `minimal`, `low`, `medium`, `high`,
`xhigh`, or `max`; the selected model must support it. Headless runs use `auto`
mode.

### `ox auth`

```text
ox auth login openrouter
ox auth logout openrouter
ox auth login openai
ox auth logout openai
```

OpenRouter login verifies an API key and saves it in the system keyring; logout
removes that key. Login reads the key from the terminal without echoing it, or
as one line of standard input. `OPENROUTER_API_KEY`, when set, takes precedence
over the saved key.

OpenAI login opens the ChatGPT sign-in in your browser and prints its URL. After
you approve it, Ox saves the sign-in in the system keyring and renews it as it
expires; logout removes it. OpenAI models use your Codex subscription's usage
limits and have no per-token cost.

Login leaves settings unchanged. When the server starts, it loads the model
catalog of each provider you are signed in to, OpenRouter's models first.
Restart the server after a login, logout, or other credential change.

### `ox acp`

```text
ox acp
```

Serves the Ox server over standard input and output for an ACP client. ACP
clients that append authentication commands to the configured server can use
these aliases:

```text
ox acp auth login <provider>
ox acp auth logout <provider>
```

`ox run`, `ox acp`, and `ox auth` run the same commands of `ox-server`, which
can also be invoked directly. Every command supports `-h` or `--help`. `ox` also
supports `-V` or `--version`.

## Teams

In an ACP session, ask Ox to divide independent work among workers. The session
you talk to is the orchestrator: it can create workers, send messages, list the
team, wait for reports, and remove workers. Workers run in Auto mode; Ask mode
requests permission before creating one. Workers have no terminal view. Teams
are unavailable in `ox run`.

By default, each worker gets a Git worktree at `.ox/worktrees/<name>` and a
branch named `ox/<name>` from the orchestrator's `HEAD`. Merge that branch from
the orchestrator's checkout before removing the worker. Removal refuses
uncommitted changes and unmerged branches. A worker can instead use an existing
directory, which removal leaves in place; outside Git, a directory is required.

The orchestrator picks each worker's model and effort from the `agentProfiles`
in your global settings file. Without profiles, workers use the orchestrator's
model and effort.

## Configuration

Global settings live in `~/.config/ox/settings.json`. A workspace can override
`model`, `effort`, `mode`, and `mcpServers` in `<workspace>/.ox/settings.json`;
each present workspace value replaces the global value. Both files are optional.

`OX_SETTINGS`, when set, holds launch settings: a JSON object in the same format
that can set `model`, `effort`, `mode`, `mcpServers`, and `hooks`. Its values
replace the workspace and global values, its MCP servers are added to theirs,
and its hooks run after the global hooks. It is never saved, and Ox removes it
from the environment of the commands, hooks, and MCP servers it runs. It lets a
program that launches Ox configure that launch without changing your files.

```json
{
  "model": "openrouter:deepseek/deepseek-v4.1-flash",
  "effort": "default",
  "mode": "ask",
  "favorites": ["openrouter:deepseek/deepseek-v4.1-flash"],
  "models": {
    "openrouter:deepseek/deepseek-v4.1-flash": {
      "providers": ["deepseek"]
    }
  },
  "agentProfiles": [
    {
      "id": "fast",
      "description": "Small, well-specified changes.",
      "model": "openrouter:deepseek/deepseek-v4.1-flash",
      "effort": "high"
    }
  ],
  "servers": [
    { "name": "Other", "command": "other-acp-server", "args": [] }
  ]
}
```

- `model` is a qualified model ID in the form `openrouter:<openrouter-model-id>`
  or `openai:<openai-model-id>`. It defaults to
  `openrouter:~deepseek/deepseek-flash-latest`, or to the first OpenAI model
  when you are not signed in to OpenRouter.
- `effort` is `default`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or
  `max`. It defaults to `default` and must be supported by the selected model.
- `mode` is `ask` or `auto`. It defaults to `ask`; `ask` requests permission
  before shell actions and MCP tools that are not marked read-only, while `auto`
  runs them without asking.
- `favorites` is a list of qualified model IDs that the terminal client's model
  picker shows first. It can be set only in the global file and defaults to an
  empty list.
- `models` maps qualified OpenRouter model IDs to a `providers` list of
  OpenRouter provider slugs. Requests for that model go only to those providers,
  tried in order. It can be set only in the global file.
- `agentProfiles` lists the models and efforts the orchestrator can give
  workers. Each profile requires a unique `id`, a `description` that tells the
  orchestrator when to use it, and a qualified `model`; `effort` defaults to
  `default` and must be supported by the model. When profiles are set, the
  orchestrator must pick one for each worker. It can be set only in the global
  file.
- `servers` configures ACP servers for the terminal client and can be set only
  in the global file. Each server requires a unique, nonempty `name` and a
  `command`; `args` is an optional list of arguments that defaults to empty. If
  `servers` is absent or empty, the client starts the `ox-server` next to `ox`.

### Instructions

Ox adds two optional instruction files to its system prompt:
`~/.config/ox/AGENTS.md`, which applies to every workspace, followed by
`<workspace>/AGENTS.md`. Each must be UTF-8 text of at most 32 KiB. They are
read when a session is created or first loaded, or once per headless run.

### MCP tools

`ox-server` supports tools from local MCP servers over stdio. Configure them in
`mcpServers`, separately from the terminal client's `servers` list:

```json
{
  "mcpServers": {
    "project": {
      "command": "project-mcp",
      "args": ["--stdio"],
      "env": { "PROJECT_PROFILE": "development" }
    }
  }
}
```

Each server needs a nonempty name and command. Arguments and environment values
must be strings. An absent workspace `mcpServers` key inherits the global map; a
present map replaces it entirely, and `{}` disables settings-defined servers.
ACP clients may also supply stdio servers; a matching name replaces the whole
settings definition. Remote transports are not supported. The terminal client
does not forward these settings to other ACP agents.

Configuration is read when a session is created or first loaded, or once per
headless run. Servers start on the first prompt, in the session's workspace, and
remain connected across turns and repeated loads. Tools and their permissions
are refreshed before each turn. Close and reload a session to pick up
configuration changes or recover a disconnected server. Commands and environment
values are not saved in session history.

Commands run directly with their argument arrays. They inherit Ox's environment
except `OPENROUTER_API_KEY`, then apply configured `env` values. Stdout carries
only MCP traffic; stderr goes to Ox's stderr. Configuration authorizes server
startup. In Ask mode, only tools explicitly marked `readOnlyHint: true` run
without approval. This trusts the configured server's annotation; Ox does not
verify that the tool is read-only. Other MCP calls show the server, tool, and
complete arguments for approval. Headless runs use Auto mode.

Initialization and complete tool discovery are limited to 30 seconds per server;
each call is limited to 120 seconds. Cancellation is forwarded to the server,
but does not undo remote effects. Failed or cancelled calls are never
automatically repeated. Closing or deleting a session, disconnecting its ACP
client, or exiting a headless run stops its server processes and their children.

Results preserve text, embedded text resources, resource-link metadata, and
structured JSON. Images, audio, and binary resources receive omission notices.
The complete text result is limited to 16 KiB; omissions do not change the
server's success or failure status. Resource links are not fetched. Standalone
resources, prompts, sampling, and elicitation are not supported.

### Hooks

`hooks` configures commands that `ox-server` runs during turns. It follows
Claude Code's hook schema and can be set only in the global file and the launch
settings. The events are `SessionStart`, `UserPromptSubmit`, `PostToolUse`, and
`Stop`, and the only hook type is `command`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "*",
        "hooks": [{ "type": "command", "command": "my-hook", "timeout": 10 }]
      }
    ]
  }
}
```

`matcher` is a regular expression matched against the whole tool name, such as
`shell|shell_process`; an absent or empty matcher, or `*`, matches every tool.
Only `PostToolUse` accepts a matcher. `timeout` is in seconds and defaults to
60. Settings are read when the server starts.

Ox runs each event's hooks in order, in Ask and Auto mode alike and without
approval:

- `SessionStart` before the first turn of a session since the server started.
- `UserPromptSubmit` before each turn.
- `PostToolUse` after each tool call that runs, if the matcher matches.
- `Stop` when the model finishes its answer.

Each command runs with `bash -c` in the session's workspace, with Ox's
environment except `OPENROUTER_API_KEY` and `OX_SETTINGS`. Stdin is a JSON
object with `session_id`, `cwd`, and `hook_event_name`, and for each event:

- `SessionStart`: `source`, `startup` for a session without turns, otherwise
  `resume`.
- `UserPromptSubmit`: `prompt`, the text of the message.
- `PostToolUse`: `tool_name`, `tool_input` (the call's arguments),
  `tool_use_id`, and `tool_response`, an object with the outcome's `status`
  (`completed`, `failed`, or `cancelled`) and `text`.
- `Stop`: `stop_hook_active`, true when a `Stop` hook already continued the
  turn.

A hook adds context to the turn by printing a JSON object:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PostToolUse",
    "additionalContext": "Text for the model."
  }
}
```

Ox adds the text to the turn as a user message after the prompt, for
`SessionStart` and `UserPromptSubmit`, or after the tool results, for
`PostToolUse`. The model reads it in its next request, the client is sent it as
a user message, and the session saves it. Empty output adds nothing.

A `Stop` hook continues the turn by printing a JSON object:

```json
{ "decision": "block", "reason": "Text for the model." }
```

Ox adds the reason as a user message after the answer, and the model continues.

A hook that exits nonzero, prints anything other than such an object, or exceeds
its timeout is reported on Ox's stderr and otherwise ignored; Ox kills a hook's
process group when it times out or the turn is cancelled. Anything a hook writes
to stderr is also reported on Ox's stderr.
