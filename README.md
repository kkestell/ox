# Ox

Ox is an ACP-native coding agent written in Rust. The Ox ACP server can be used
with any ACP-compatible client, and the TUI can be used with any ACP-compatible
server. Ox is a single 9MB binary.

## Quick Start

Download the [latest release](https://github.com/kkestell/ox/releases), then
extract `ox` into a directory on your `PATH`, such as `~/.local/bin`:

```sh
tar -xzf ox-*.tar.gz -C ~/.local/bin
```

Save your OpenRouter API key to the system keychain:

```sh
ox auth login
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
ox [--dir <DIR>] [--server <SERVER>]
```

Starts the TUI. `--dir` selects the workspace and defaults to the current
directory. `--server` selects a server by its configured name; it is required
when more than one server is configured.

### `ox run`

```text
ox run [--dir <DIR>] [--model <MODEL>] [--effort <EFFORT>] <PROMPT>
```

Runs one noninteractive prompt and prints the final answer. `--dir` selects the
workspace and defaults to the current directory. `--model` accepts an OpenRouter
model ID and overrides the configured model. `--effort` defaults to `default`
and accepts `default`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or
`max`; the selected model must support it. Headless runs use `auto` mode.

### `ox auth`

```text
ox auth login
ox auth logout
```

`login` verifies an OpenRouter API key and saves it in the system keyring.
`logout` removes the saved key. `OPENROUTER_API_KEY`, when set, takes precedence
over the saved key.

### `ox acp`

```text
ox acp
```

Serves the Ox server over standard input and output for an ACP client. ACP
clients that append authentication commands to the configured server can use
these aliases:

```text
ox acp auth login
ox acp auth logout
```

Every command supports `-h` or `--help`. `ox` also supports `-V` or `--version`.

## Configuration

Global settings live in `~/.config/ox/settings.json`. A workspace can override
`model`, `effort`, and `mode` in `<workspace>/.ox/settings.json`; each present
workspace value replaces the global value. Both files are optional.

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "effort": "default",
  "mode": "ask",
  "favorites": ["deepseek/deepseek-v4.1-flash"],
  "models": {
    "deepseek/deepseek-v4.1-flash": { "providers": ["deepseek"] }
  },
  "servers": [
    { "name": "Other", "command": "other-acp-server", "args": [] }
  ]
}
```

- `model` is an OpenRouter model ID. The default is
  `~deepseek/deepseek-flash-latest`.
- `effort` is `default`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or
  `max`. It defaults to `default` and must be supported by the selected model.
- `mode` is `ask` or `auto`. It defaults to `ask`; `ask` requests permission
  before shell actions, while `auto` runs them without asking.
- `favorites` is an ordered list of OpenRouter model IDs. It applies only to the
  terminal client, can be set only in the global file, and defaults to an empty
  list.
- `models` maps OpenRouter model IDs to a `providers` list of OpenRouter
  provider slugs. Requests for that model go only to those providers, tried in
  order. It can be set only in the global file.
- `servers` configures ACP servers for the terminal client and can be set only
  in the global file. Each server requires a unique, nonempty `name` and a
  `command`; `args` is an optional list of arguments that defaults to empty. If
  `servers` is absent or empty, the client starts the bundled Ox server.
