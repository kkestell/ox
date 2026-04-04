# Boo

Boo is a small terminal automation library for debugging interactive TUI
applications.

Its API is intentionally layered:

- low-level primitives when you need exact PTY bytes and exact timing
- higher-level helpers when you want scripts to read like user intent

## Build

### macOS

```bash
brew install sdl3 sdl3_ttf zig
```

```bash
make build
```

Or with CMake directly:

```bash
cmake -S . -B build
cmake --build build -j2
```

## Python API

The build copies the native library into the `boo/` package directory, and the
wrapper loads it from there by default. You can override that with
`BOO_NATIVE_LIB`.

### Quick example

```python
import boo

with boo.launch(
    "printf 'ready\\n'; read x; printf 'seen:%s\\n' \"$x\"",
    visible=True,
) as session:
    session.wait_for_text("ready", timeout=2)

    with session.capture() as capture:
        screen = session.type("hello\\n", wait_for="seen:hello")

    print(screen)
    print("input bytes:", capture.input_hex())
    print("all input bytes:", boo.hex_bytes(session.input_bytes()))
    session.run_until_exit()
```

Visible sessions stay responsive while you keep calling `step(...)`,
`wait(...)`, `wait_for_text(...)`, `wait_for_idle(...)`, or
`run_until_exit()`.

### `boo.launch(...)`

Creates and launches a session.

```python
boo.launch(
    command=None,
    *,
    cols=80,
    rows=24,
    cwd=None,
    env=None,
    font_size=16,
    padding=4,
    capture_output=False,
    visible=True,
    window_title="boo",
)
```

Arguments:

- `command`: `None` launches the user's shell, a string runs via `/bin/sh -lc`, and a sequence runs as an explicit argv
- `cols`, `rows`: initial terminal size
- `cwd`: working directory for the child process
- `env`: environment overrides as a mapping
- `font_size`, `padding`: window rendering settings
- `capture_output`: whether to retain a full-session raw output transcript
- `visible`: whether to open a visible window
- `window_title`: window title for visible sessions

`Session.launch(...)` still exists, but `boo.launch(...)` is the preferred
entry point.

## Low-level session methods

- `step(timeout_ms=0)`: pump one iteration of the event loop
- `wait(seconds, poll_interval=0.016)`: keep pumping for a fixed duration
- `run_until_exit(poll_interval=0.016)`: keep pumping until the child exits and return its exit status
- `send_bytes(data)`: send exact bytes to the session
- `send_text(text)`: send UTF-8 text to the session
- `send_key(key, ctrl=False, alt=False, shift=False, super_=False, action=None)`: send a key event
- `mouse_down(x, y, button="left", ...)`: press a mouse button at a terminal cell
- `mouse_up(x, y, button="left", ...)`: release a mouse button at a terminal cell
- `mouse_move(x, y, ...)`: move the mouse to a terminal cell
- `scroll(x, y, delta_y=0, delta_x=0, ...)`: send wheel input at a terminal cell
- `click(x, y, button="left", wait_for=None, ...)`: ergonomic mouse click helper
- `input_bytes()`: return all raw input bytes sent so far
- `output_bytes()`: return all raw output bytes received so far
- `activity()`: return byte counts plus current input/output quiet durations
- `screen_text(trim=True, unwrap=False)`: return the current rendered screen text
- `wait_for_text(text, timeout=5.0, poll_interval=0.05, trim=True, unwrap=False)`: wait until text appears and return the current screen
- `wait_for_idle(idle_for=0.25, timeout=5.0, poll_interval=0.05)`: wait until no PTY output has arrived for the requested duration
- `resize(cols, rows)`: resize the terminal
- `is_alive()`: report whether the child process is still running
- `exit_status()`: return the exit status, or `None` if the process has not exited yet
- `terminate()`: terminate the child process
- `close()`: free the native session handle

### Key actions

`send_key()` sends a full keystroke by default. If you need a specific key
phase, pass one of these action strings:

- `"press"`
- `"release"`
- `"repeat"`
- `"press_and_release"`

Common key names include letters, digits, punctuation, and names like
`enter`, `tab`, `backspace`, `escape`, `up`, `down`, `left`, `right`, `home`,
`end`, `page_up`, `page_down`, and `f1` through `f12`.

### Mouse input

Mouse coordinates use zero-based terminal cell positions, not raw pixels. So
`x=0, y=0` targets the top-left visible cell, and `x=1, y=1` targets the next
cell down and to the right.

Buttons:

- `"left"`
- `"right"`
- `"middle"`
- `"x1"`
- `"x2"`

Dragging is stateful: call `mouse_down(...)`, then `mouse_move(...)`, then
`mouse_up(...)`.

```python
session.mouse_down(10, 5)
session.mouse_move(14, 8)
session.mouse_up(14, 8)

session.scroll(10, 5, delta_y=1)
session.click(3, 2, wait_for=boo.idle(0.2))
```

## Higher-level helpers

- `press(...)`: ergonomic key helper that can optionally wait for the next state
- `type(...)`: ergonomic text helper that can optionally wait for the next state
- `capture(...)`: start an offset-based input/output capture window
- `boo.idle(...)`: build a `wait_for=` idle condition for `press(...)` or `type(...)`

Examples:

```python
session.press("enter", wait_for=boo.idle(0.3, timeout=2.0))
screen = session.type("hello\\n", wait_for="seen:hello")

with session.capture() as capture:
    session.press("up")

print(capture.input_hex())
```

## Exact-byte debugging

Use `send_bytes(...)` when you need byte-for-byte control, such as escape
sequences, control characters, or protocol debugging:

```python
session.send_bytes(b"\\x1b[A")
print(boo.hex_bytes(session.input_bytes()))
```

For scoped debugging, use a capture window:

```python
with session.capture() as capture:
    session.send_key("up")

print(capture.input)
print(capture.output_hex())
```

Full-session output retention is opt-in. Use `capture_output=True` at launch if
you want `output_bytes()` to accumulate the session's raw output from the
beginning, or rely on `capture(include_output=True)` for targeted windows.

## Exceptions

- `boo.BooError`: base exception for wrapper and native-call failures
- `boo.WaitTimeoutError`: raised by `wait_for_text()` or `wait_for_idle()` on timeout
- `boo.ProcessExitedError`: raised when the process exits before a requested condition is satisfied

## Examples

Run the visible example:

```bash
uv run python examples/shell.py
```

## Tests

```bash
make test
```

The test suite is headless: it sets `SDL_VIDEODRIVER=dummy` and launches
sessions with `visible=False`, so `ctest` does not open a window.
