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

## Authentication

When prompted, enter your [OpenRouter](https://openrouter.ai) API key. It is saved to the system keyring.

```sh
ox auth login
```
