# Browser client

Ox's first-party browser client is a local conversation and supervision surface
for one server-local workspace. The Bun host starts and supervises Ox, while the
browser connects only to that host. A browser refresh or a second browser does
not cancel running work.

Install [Bun](https://bun.sh/) and Ox first. From an Ox checkout, install the
client dependencies and build its browser bundle:

```sh
cd client
bun install
bun run build
```

Start the host with an absolute workspace path:

```sh
bun run start -- --workspace /absolute/path/to/workspace
```

It prints a loopback URL such as `http://127.0.0.1:3000`; open that URL in a
browser. The host automatically uses Ox's stored credential when one is
available. The host uses `ox` from `PATH` by default. Use `--ox` to choose an
executable, and repeat `--ox-arg` for arguments passed through to Ox:

```sh
bun run start -- \
  --workspace /absolute/path/to/workspace \
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
bun run start -- --host 100.64.0.10 --workspace /absolute/path/to/workspace
```

The host has no TLS or user authentication. Anyone who can reach a non-loopback
listener can use the browser client with its workspace privileges, including
approving tools and supplying MCP definitions. Bind it only to a network and
devices you trust; never expose it directly to the public internet.
