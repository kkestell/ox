# ox

Ox is a small, lightweight, [ACP-native](https://agentclientprotocol.com) coding agent.

## Installation

Download the latest release from [GitHub Releases](https://github.com/kkestell/ox/releases) and extract the archive for your platform:

```sh
tar -xzf ox_VERSION_OS_ARCH.tar.gz
```

Copy the binary to a directory on your `PATH`:

```sh
install ox ~/.local/bin/ox
```

Install [ripgrep](https://github.com/BurntSushi/ripgrep#installation) and make
`rg` available on your `PATH` for the `glob` and `grep` tools.

The `read_file`, `glob`, and `grep` tools read only inside the session workspace
and return at most 16 KiB per result. `read_file` accepts a 1-based line `offset`
and a line `limit` (default 200, maximum 1,000). Oversized lines return a marked
preview; their omitted portions cannot be retrieved through line pagination.
Truncated searches ask the model to narrow the search path or pattern.

## Authentication

When prompted, enter your [OpenRouter](https://openrouter.ai) API key. It is saved to the system keyring.

```sh
ox auth login
```
