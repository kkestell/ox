# Boo

Boo is a headless CLI for driving interactive terminal programs through a real
PTY. The only supported control surface is the `boo` command; the daemon keeps
the native session machinery private behind that CLI boundary.

## Build

```bash
cmake -S . -B build
cmake --build build -j2
```

The native build depends on `ghostty-vt` and no longer depends on SDL3 or
font/rendering libraries.

## CLI

```bash
uv run boo start "bash"
uv run boo screen
uv run boo type $'echo hello\n'
uv run boo press enter
uv run boo alive
uv run boo stop
```

`boo start` launches a background daemon that owns a single headless session.
Every other CLI command connects to that daemon over a Unix domain socket,
issues one command, prints the result, and exits.

## Tests

```bash
make test
```

`ctest` runs the native smoke test. A higher-level CLI smoke script also lives
in `tests/test_cli.py`, but it requires permissions to bind a local Unix socket
in the current environment.
