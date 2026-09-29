# ox

ox is a small interactive ACP client. Each invocation starts one server and one
new session in the supplied directory. It requires an already authenticated
server that supports text prompts without client filesystem or terminal
capabilities.

Install both binaries to `~/.local/bin` with Rust:

```sh
make install
ox /path/to/project
```

The directory defaults to the current directory. `scripts/run-tui` builds a
debug binary and forwards its arguments to ox.

By default, `ox` starts `ox-acp`. To use another server, configure servers in
`$XDG_CONFIG_HOME/ox/tui.json`, or `~/.config/ox/tui.json`:

```json
{
  "servers": [
    { "name": "Other", "command": "other-acp-server", "args": [] }
  ]
}
```

Use your server's actual executable and ACP arguments. Select a server with
`--server` when more than one is configured. Run `ox-acp` directly to serve an
ACP client such as Zed; `ox-acp auth login` authenticates it, and `ox-acp run`
runs a headless prompt.

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
