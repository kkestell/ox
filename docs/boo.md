# Testing Ox with Boo

Boo is the headless terminal driver for Ox. It runs a persistent PTY session in
the background and exposes that session exclusively through the `boo` CLI.

## Core commands

```bash
boo start <command>     # launch a session daemon
boo screen              # print the current screen contents
boo type <text>         # send UTF-8 text to the session
boo press <key>         # send a key (enter, escape, d --ctrl, etc.)
boo alive               # report whether the child process is still running
boo stop                # tear down the session
```

Each command is one-shot. The daemon keeps the PTY and terminal state alive
between invocations.

## Quick start

The helper script builds Ox and Boo, creates an isolated workspace at
`/tmp/ur-tui-test`, and starts a Boo session pointed at Ox:

```bash
./scripts/boo.sh
```

After that, run Boo commands from the `boo/` directory:

```bash
cd boo
uv run boo screen
uv run boo type $'What is 2+2?\n'
uv run boo stop
```

## Verification notes

- `boo screen` is the main debugger. Check it after every input.
- `boo press escape` is useful for cancel-path verification.
- `boo press d --ctrl` is the quickest way to verify clean EOF handling.
