# Browser client

Ox's first-party browser client is a local conversation and supervision surface
for registered server-local workspaces. The Bun host starts and supervises one
Ox process per registered workspace, while the browser connects only to that
host. A browser refresh or a second browser does not cancel running work.

Install [Bun](https://bun.sh/) and Ox first. From an Ox checkout, install the
client dependencies and build its browser bundle:

```sh
cd client
bun install
bun run build
```

Start the host:

```sh
bun run start
```

It prints a loopback URL such as `http://127.0.0.1:3000`; open that URL in a
browser, then register an absolute server-local workspace path. The host saves
canonical workspace roots in `$XDG_CONFIG_HOME/ox/workspaces.json`, or
`$HOME/.config/ox/workspaces.json` when the XDG path is not absolute. Every
registered workspace runs its own Ox process, so selecting a workspace changes
what the browser shows without interrupting work elsewhere, and a failure in one
workspace leaves the others running. Removing a registry entry stops that
workspace's Ox process; it does not delete workspace files or Ox session
history.

When a workspace's Ox process fails to start or exits, restart it from the
browser. A restart starts a replacement process and reopens the workspace's
stored conversations; it does not resume a turn that was interrupted.
Diagnostics from the process that stopped stay visible in support details.

The host automatically uses Ox's stored credential when one is available. It
uses `ox` from `PATH` by default. Use `--ox` to choose an executable, and repeat
`--ox-arg` for arguments passed through to Ox:

```sh
bun run start -- \
  --ox /path/to/ox \
  --ox-arg --model \
  --ox-arg your-provider/model
```

The browser presents Ox's conversations, prompts, permissions, forms, file and
terminal activity, and MCP servers. MCP header and command environment values
are saved in the Bun host for later conversation activations; they are cleared
from the page afterward and never appear in host snapshots.

## Trusted-network access

The host binds to loopback by default. To reach it from a trusted private
network, explicitly bind its address, for example a Tailscale address:

```sh
bun run start -- --host 100.64.0.10
```

The host has no TLS or user authentication. Anyone who can reach a non-loopback
listener can use the browser client with its registered-workspace privileges,
including selecting roots, approving tools, and supplying MCP definitions. Bind
it only to a network and devices you trust; never expose it directly to the
public internet.
