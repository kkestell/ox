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

The `shell` tool runs a fresh, noninteractive `/bin/sh` command on macOS and
Linux, starting in the session workspace with stdin connected to `/dev/null`.
It supports builds, tests, Git, package commands, and scripts. Calls time out
after 120 seconds by default; the model can request 1–600 seconds.

Shell results include the exit status and separate stdout and stderr tails,
at most 16 KiB total. The streams share 14 KiB: 7 KiB each, with unused space
given to the other stream. Earlier output may be omitted; Ox keeps no full
hidden log. Redirect long logs to a workspace file for later inspection.

Commands run with Ox's permissions and can access paths outside the workspace
and the network. They inherit Ox's environment, which the model can inspect,
except that `OPENROUTER_API_KEY` is removed before spawning. This prevents
incidental inheritance of that variable; credentials in files or available
through other mechanisms remain accessible.

Timeout, ACP Stop, connection shutdown, and Ctrl-C in `ox run` stop the shell's
process group and reap the shell before saving its result. Partial filesystem
changes may remain. Background children are also stopped when the shell exits;
there are no persistent shell sessions or interactive input. Programs that
deliberately detach into another process group or session are unsupported.
Cleanup after forced termination or an Ox crash is not guaranteed.

## Authentication

When prompted, enter your [OpenRouter](https://openrouter.ai) API key. It is saved to the system keyring.

```sh
ox auth login
```
