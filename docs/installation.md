# Installing Ox

Ox is distributed as a standalone archive. Each release provides one executable
named `ox` for macOS and Linux on both amd64 and arm64. There is no Windows
build, no package-manager formula, no installer, and no automatic update.

## Requirements

- A Unix-like system. Ox assumes POSIX shell, process-group, and file-permission
  semantics.
- An ACP client, such as Zed. See the [Zed guide](zed.md).
- An OpenRouter model and credential.

A released binary does not need Go. Building from a checkout does.

## Download

Pick the archive for your platform from the
[latest release](https://github.com/kkestell/ox/releases/latest):

| System                 | Archive                            |
| ---------------------- | ---------------------------------- |
| macOS on Apple silicon | `ox_<version>_darwin_arm64.tar.gz` |
| macOS on Intel         | `ox_<version>_darwin_amd64.tar.gz` |
| Linux on arm64         | `ox_<version>_linux_arm64.tar.gz`  |
| Linux on amd64         | `ox_<version>_linux_amd64.tar.gz`  |

Replace `<version>` with the release version, such as `1.0.0`. The archive
contains the executable at its top level and nothing else.

## Verify the archive

Each release publishes a `SHA256SUMS` file covering all four archives. Download
it next to your archive and check the one you downloaded; `--ignore-missing`
skips the archives you did not download.

```sh
sha256sum --check --ignore-missing SHA256SUMS  # Linux
shasum -a 256 --check --ignore-missing SHA256SUMS  # macOS
```

The archives are not signed with a Developer ID certificate and are not
notarized. macOS requires a code signature for arm64 binaries, so the Go linker
embeds an ad-hoc one; that is enough to run, but Gatekeeper may still refuse a
binary downloaded through a browser, which marks it with a quarantine attribute.
`xattr -c ./ox` clears the extended attributes on an installed binary.

## Install

Extract the archive and place the executable on your `PATH`. On both systems,
`$HOME/.local/bin` is a conventional location.

```sh
tar -xzf ox_1.0.0_darwin_arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 0755 ox "$HOME/.local/bin/ox"
```

If that directory is not already on your `PATH`, add it to your shell profile,
for example `export PATH="$HOME/.local/bin:$PATH"`.

## Sign in and configure

Store an OpenRouter credential with `ox login`, then name the models you want in
your settings. The [settings guide](settings.md) covers configuration fields,
file locations, and precedence.

```sh
ox login
```

Ox runs as an ACP server, so your client starts it; running `ox` by hand only
serves the standard input and output of that shell. The [Zed guide](zed.md)
shows a client configuration that points at an installed binary.

## Check the installed version

```sh
ox --version
```

That prints one line, such as `ox 1.0.0`. A binary built from a checkout reports
`ox devel`.

## Upgrade

Ox does not update itself. Download the new archive, verify it, and replace the
executable in place:

```sh
tar -xzf ox_1.1.0_darwin_arm64.tar.gz
install -m 0755 ox "$HOME/.local/bin/ox"
```

Stop your ACP client first so it does not keep a running process on the old
binary. Settings, credentials, sessions, and memory live outside the archive and
survive the replacement.
