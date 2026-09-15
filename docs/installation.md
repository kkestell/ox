# Installing Ox

Ox is distributed as a standalone archive. Each release provides one executable
named `ox` for macOS and Linux on both amd64 and arm64, alongside an
`install.sh` that places it and writes starter settings. There is no Windows
build, no package-manager formula, and no automatic update.

## Requirements

- A Unix-like system. Ox assumes POSIX shell, process-group, and file-permission
  semantics.
- An ACP client, such as Zed. See the [Zed guide](zed.md).
- An OpenRouter model and credential.

A released binary does not need Go. Building from a checkout does.

## Download

Pick the archive for your platform from the
[releases page](https://github.com/kkestell/ox/releases):

| System                 | Archive                            |
| ---------------------- | ---------------------------------- |
| macOS on Apple silicon | `ox_<version>_darwin_arm64.tar.gz` |
| macOS on Intel         | `ox_<version>_darwin_amd64.tar.gz` |
| Linux on arm64         | `ox_<version>_linux_arm64.tar.gz`  |
| Linux on amd64         | `ox_<version>_linux_amd64.tar.gz`  |

Replace `<version>` with the release version, such as `1.0.0`. A prerelease
carries a suffix, such as `1.0.0-alpha1`, and is not marked as the latest
release. The archive contains `ox` and `install.sh` at its top level and nothing
else.

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
`install.sh` clears the extended attributes on the binary it installs; after a
manual install, `xattr -c "$HOME/.local/bin/ox"` does the same.

## Install

Extract the archive and run its installer:

```sh
tar -xzf ox_1.0.0_darwin_arm64.tar.gz
./install.sh
```

It copies `ox` to `$HOME/.local/bin`, which is a conventional location on both
systems, and writes a starter `settings.json` naming a few models when you do
not have one yet. It never changes settings that already exist, so it is also
the way to upgrade. The file it writes is the global settings file described in
the [settings guide](settings.md), at `$XDG_CONFIG_HOME/ox/settings.json` or
`$HOME/.config/ox/settings.json`.

Installing by hand does the same thing without the settings file:

```sh
mkdir -p "$HOME/.local/bin"
install -m 0755 ox "$HOME/.local/bin/ox"
```

If `$HOME/.local/bin` is not already on your `PATH`, add it to your shell
profile, for example `export PATH="$HOME/.local/bin:$PATH"`.

## Sign in and configure

Store an OpenRouter credential with `ox login`, then edit the models in your
settings to the ones you want. Ox refuses to create a session until at least one
model is configured and either `default_model` or `--model` names it. The
[settings guide](settings.md) covers configuration fields, file locations, and
precedence.

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

Ox does not update itself. Download the new archive, verify it, and run its
installer again:

```sh
tar -xzf ox_1.1.0_darwin_arm64.tar.gz
./install.sh
```

Stop your ACP client first so it does not keep a running process on the old
binary. Settings, credentials, sessions, and memory live outside the archive and
survive the replacement.
