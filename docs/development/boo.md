# Testing Ox with Boo

Boo is the headless terminal driver for Ox. It runs a persistent PTY session in
the background and exposes that session exclusively through the `boo` CLI.

## Core commands

```bash
boo start <command>     # launch a session daemon
boo screen              # print the current screen contents
boo type <text>         # send UTF-8 text to the session
boo press <key>         # send a key (enter, escape, d --ctrl, etc.)
boo wait <text>         # poll screen until text appears (--timeout N)
boo alive               # report whether the child process is still running
boo stop                # tear down the session
```

Each command is one-shot. The daemon keeps the PTY and terminal state alive
between invocations.

## Deterministic testing with the fake provider

Ox supports a `--fake-provider <scenario>` flag that registers a scripted
provider. This replaces live model calls with deterministic, pre-defined
responses — no API keys or credentials needed.

Built-in scenarios:
- **hello** — single short text response
- **long-response** — multi-chunk streamed response for scroll/render testing
- **tool-call** — assistant calls `read_file`, then summarizes
- **permission-tool-call** — assistant calls `write_file` (permission-gated)
- **error** — provider throws an error on the first turn
- **multi-turn** — three turns of simple conversation

Custom scenarios can be provided as a JSON file path instead of a built-in name.

## Quick start (deterministic)

The helper script builds Ox and Boo, creates an isolated workspace at
`/tmp/ox-test`, and starts a Boo session with the fake provider:

```bash
./scripts/boo.sh                          # default: hello scenario
./scripts/boo.sh --scenario=long-response # specific scenario
./scripts/boo.sh --live                   # live provider (needs .env)
```

After that, run Boo commands from the `boo/` directory:

```bash
cd boo
uv run boo screen
uv run boo type $'What is 2+2?\n'
uv run boo stop
```

## Verification recipes

### Basic response rendering
```bash
./scripts/boo.sh --scenario=hello
cd boo
uv run boo screen               # should show the hello response
uv run boo stop
```

### Long streamed responses
```bash
./scripts/boo.sh --scenario=long-response
cd boo
uv run boo screen               # verify scroll and soft-wrap behavior
uv run boo stop
```

### Tool call visibility
```bash
./scripts/boo.sh --scenario=tool-call
cd boo
uv run boo screen               # should show tool call start/completion
uv run boo stop
```

### Permission prompt
```bash
./scripts/boo.sh --scenario=permission-tool-call
cd boo
uv run boo screen               # should show the permission prompt
uv run boo type $'y\n'          # approve the permission
uv run boo screen               # should show tool completion and response
uv run boo stop
```

### Cancellation via Escape
```bash
./scripts/boo.sh --scenario=long-response
cd boo
uv run boo type $'hello\n'
uv run boo press escape          # cancel mid-stream
uv run boo screen                # should show [cancelled]
uv run boo stop
```

### Clean EOF / session shutdown
```bash
./scripts/boo.sh
cd boo
uv run boo press d --ctrl         # send Ctrl+D
uv run boo alive                  # should report "not alive"
uv run boo stop
```

## Bug-fix workflow

When reproducing a Boo-visible UI bug:

1. Add or update the narrow unit/integration test that reproduces the bug.
2. Run the deterministic Boo smoke that exercises the same behavior.
3. Fix the bug.
4. Verify both the unit test and the Boo smoke pass before declaring victory.

## Automated test suite

Run all tests (dotnet + boo native + boo Python + Ox Boo smokes):

```bash
make test        # from repo root
```

Or run individual parts:

```bash
dotnet test Ox.slnx                         # .NET unit tests
make -C boo test                            # native smoke + CLI tests
cd boo && uv run python tests/test_ox_boo_smoke.py  # Ox TUI smokes
```
