# Boo session daemon

## Goal

Make boo session-oriented so an LLM agent can invoke the CLI multiple times against a persistent session — start a process, poke it, wait, check the screen, poke again — without scripting the entire interaction end-to-end in a single process.

## Desired outcome

A `boo` CLI with subcommands that communicate with a long-lived daemon over a Unix domain socket:

```bash
# Start a persistent session running ur-tui
boo start "ur-tui"

# Check what's on screen
boo screen

# Type something
boo type "hello world"

# Press a key
boo press enter

# Read the screen again
boo screen

# Tear it down
boo stop
```

Each invocation connects to the daemon, sends a command, gets a JSON response, and exits. The daemon keeps the boo `Session` alive between invocations, continuously pumping the event loop.

## How we got here

The user wants LLM agents (Claude Code) to drive interactive TUI applications through boo. The current model — scripting an entire interaction in one Python process with polling-based waits — is brittle because of timing. The agent workflow is fundamentally iterative: poke, wait (externally), observe, decide, poke again. This requires session state to survive across CLI invocations.

Key decisions from brainstorming:
- **Consumer**: LLM agents (tool-use across separate invocations)
- **IPC**: Unix domain socket (clean request-response framing)
- **CLI layer**: Python (leverages existing ctypes bindings)
- **Concurrency**: Single session at a time
- **Commands**: Minimal set — `start`, `screen`, `type`, `press`, `stop`

## Related code

- `boo/boo/__init__.py` — Python `Session` class with all PTY/SDL bindings. The daemon wraps this.
- `boo/src/boo_tester.c` — Native C layer. No changes needed — the Python bindings already expose everything.
- `boo/src/main.c` — Current C CLI entry point. Not modified; the new Python CLI is separate.
- `boo/pyproject.toml` — Needs a `[project.scripts]` entry for the `boo` CLI.
- `boo/Makefile` — May want a convenience target for the daemon.

## Current state

- The `Session` class in `boo/__init__.py` is fully functional: launch, step, send_text, send_key, screen_text, terminate, close.
- `step(timeout_ms)` drives the SDL event loop and PTY I/O. Must be called continuously.
- No daemon, socket, or CLI infrastructure exists yet.
- `pyproject.toml` has no `[project.scripts]` entry.

## Structural considerations

**Modularization**: Three new modules, each with a single clear responsibility:
- `daemon.py` — session lifecycle + socket server loop
- `client.py` — connect-send-receive-disconnect
- `cli.py` — argparse dispatch to daemon or client

This keeps the existing `__init__.py` (the library API) untouched. The daemon is a *consumer* of the library, not a modification to it.

**Abstraction**: The daemon exposes a deliberately thin command set (5 commands). This is the right level for LLM tool-use — each command maps to one observable action. Higher-level orchestration (wait-for-text, capture) stays in the library API for scripted use.

**Encapsulation**: The daemon owns the `Session` instance. Clients never touch it directly — they send commands and get JSON back. The socket protocol is the only interface boundary.

**Threading**: Not needed. The daemon runs a single-threaded loop that interleaves `step()` with `select()` on the socket. This avoids SDL's thread-safety constraints entirely. The loop:
1. `select()` on the listening socket + any active client fd, with a short timeout (~16ms)
2. If a client connects, accept it
3. If a client has data, read the JSON command, dispatch, send JSON response, close connection
4. Call `step(0)` to pump the event loop
5. Repeat

## Implementation plan

### 1. Socket protocol and client

- [x] **Create `boo/client.py`** — A `send_command(cmd: dict) -> dict` function that:
  - Connects to the Unix domain socket at a well-known path (`/tmp/boo.sock`)
  - Sends a JSON object + newline
  - Reads a JSON object + newline response
  - Closes the connection
  - Raises `BooError` if the socket doesn't exist (daemon not running) or the response has `"ok": false`

### 2. Daemon

- [x] **Create `boo/daemon.py`** — A `run_daemon(command, **launch_kwargs)` function that:
  - Creates and binds the Unix domain socket at `/tmp/boo.sock` (removing stale socket file first)
  - Writes daemon PID to `/tmp/boo.pid`
  - Launches a boo `Session` with the given command and options
  - Runs the main loop (described above): `select()` interleaved with `step()`
  - Dispatches incoming commands:
    - `{"cmd": "screen"}` → calls `session.screen_text()`, returns `{"ok": true, "text": "..."}`
    - `{"cmd": "type", "text": "..."}` → calls `session.send_text(text)`, returns `{"ok": true}`
    - `{"cmd": "press", "key": "...", "ctrl": false, ...}` → calls `session.send_key(...)`, returns `{"ok": true}`
    - `{"cmd": "stop"}` → terminates session, cleans up socket/pid files, exits
    - `{"cmd": "alive"}` → returns `{"ok": true, "alive": bool}` (health check)
  - Handles errors gracefully: if the child process exits, subsequent commands get an appropriate error
  - Cleans up socket and pid files on exit (atexit handler + signal handlers for SIGTERM/SIGINT)

### 3. CLI entry point

- [x] **Create `boo/cli.py`** with `main()` function and argparse subcommands:
  - `boo start <command> [--cols N] [--rows N] [--visible] [--headless]` — forks the daemon into the background (using `subprocess.Popen` with the daemon module), waits for the socket to appear (poll with short sleep, timeout after ~5s), prints confirmation
  - `boo screen [--trim/--no-trim] [--unwrap]` — sends `screen` command, prints the screen text to stdout
  - `boo type <text>` — sends `type` command
  - `boo press <key> [--ctrl] [--alt] [--shift]` — sends `press` command
  - `boo stop` — sends `stop` command
  - `boo alive` — sends `alive` command, prints status
  - All client subcommands print errors to stderr and exit non-zero on failure

### 4. Packaging

- [x] **Update `boo/pyproject.toml`** — Add `[project.scripts]` entry: `boo = "boo.cli:main"`

### 5. Makefile convenience target

- [x] **Update `boo/Makefile`** — Add a `daemon` target for quick manual testing (e.g., `make daemon CMD="bash"`)

## Validation

- **Manual verification**: Start a daemon with `boo start bash`, run `boo screen`, `boo type "echo hello\n"`, `boo screen` (should show "hello"), `boo stop`. Confirm socket and pid files are cleaned up.
- **Headless mode**: Verify `boo start --headless bash` works with `SDL_VIDEODRIVER=dummy`.
- **Error cases**: Verify `boo screen` with no daemon running gives a clear error. Verify `boo start` when a daemon is already running gives a clear error.
- **Process exit**: Start a short-lived command, wait for it to exit, verify `boo screen` returns the final screen state and `boo alive` reports not alive.
- **Lint**: Run `make inspect` and fix any issues.

## Open questions

- Should the socket path be configurable (e.g., `--socket /path/to/boo.sock`) or is `/tmp/boo.sock` sufficient? For single-session use, a fixed path is simpler, but it precludes running multiple daemons for different users on the same machine. A reasonable middle ground: `/tmp/boo-$USER.sock`.
