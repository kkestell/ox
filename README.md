# Ox

## Quick Start

Download the newest release on the
[releases page](https://github.com/kkestell/ox/releases), then extract `ox` and
`ur` into a directory on your `PATH`, such as `~/.local/bin`:

```sh
tar -xzf ox-*.tar.gz -C ~/.local/bin
```

Save your OpenRouter API key:

```sh
ur auth login
```

Start ox in a project directory:

```sh
ox /path/to/project
```

Configure Zed to use ur as an external agent by adding the following to `~/.config/zed/settings.json`:

```sh
"agent_servers": {
  "Ur": {
    "type": "custom",
    "command": "/Users/username/.local/bin/ur",
  }
}
```

By default, `ox` starts `ur`. To use another server, configure servers in
`$XDG_CONFIG_HOME/ox/settings.json`, or `~/.config/ox/settings.json`:

```json
{
  "servers": [
    { "name": "Other", "command": "other-acp-server", "args": [] }
  ]
}
```

Use your server's actual executable and ACP arguments. Select a server with
`--server` when more than one is configured. Run `ur` directly to serve an ACP
client such as Zed, and `ur run` to run a headless prompt.

Type a prompt and press Enter. Paste preserves newlines and waits for Enter; the
input line shows newlines as `↵`. Long input shows its end. Backspace deletes
the last character; Ctrl-U clears the input. Slash commands are sent as ordinary
text.

Permission requests show numbered choices. Enter a number and press Enter.
Ctrl-C during a turn cancels it and its pending permissions; ox waits for the
turn to end before accepting another prompt. When idle, Ctrl-C clears nonempty
input or quits if empty. Ctrl-D quits immediately.

Replies remain plain text, including Markdown source, in terminal scrollback. ox
has no local saved history, session browser, or resume command. Any saved
history belongs to the server.

The terminal title shows `ox: ready`, `working`, `needs permission`, `finished`,
or `turn error`. A turn that ends while the terminal is not focused keeps its
result in the title until the terminal is focused. A turn ending or a permission
request in an unfocused terminal rings the bell.

Quitting ox ends its server connection and child process. A failed or
disconnected ACP server ends ox with an error; start ox again to create a new
session.

Run `make check` for every check and `make e2e` for the isolated tmux tests.
