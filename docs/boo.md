# Testing Ur.Tui with Boo

Boo is a terminal automation library that can drive Ur.Tui through a real PTY,
letting you exercise every interactive feature — streaming, tool calls,
permission prompts, skills, extensions, and cancellation — without touching it
by hand.

## Architecture: daemon + CLI

Boo runs as a persistent **session daemon** that keeps an SDL window and PTY
alive across commands. You drive it from the shell (or from scripts) with
one-shot CLI calls:

```
boo start <command>     # launch a session
boo screen              # print current screen contents
boo type <text>         # type text into the session
boo press <key>         # press a key (enter, escape, d --ctrl, etc.)
boo alive               # check if the child process is still running
boo stop                # tear down the session
```

Each CLI call connects to the daemon's Unix socket, sends one JSON command,
and prints the result. There is no persistent client state.

## Quick start

The `scripts/boo.sh` script handles everything — building Ur.Tui and Boo,
setting up an isolated workspace at `/tmp/ur-tui-test`, and launching a
visible session:

```bash
./scripts/boo.sh
```

Pass `--headless` to skip the SDL window (for CI or scripted runs):

```bash
./scripts/boo.sh --headless
```

The workspace includes:

- **`.env`** — copied from the repo root so the API key is available.
- **`hello.txt`** — contains `test-sentinel`, useful for `read_file` tests.
- **`/greet` skill** — responds with "Hello, \<name\>! Boo says hi."
- **`dice` extension** — provides a `roll_dice` tool that returns "You rolled a 6!"

Once the session is up, all `boo` commands should be run from the `boo/`
directory:

```bash
cd boo
uv run boo screen
```

## Test recipes

Each recipe is a sequence of `boo` commands you can paste into your shell.
Check the screen after each step with `uv run boo screen`.

### 1. Boot & session banner

```bash
uv run boo screen
# Expect: "Session: ...", "Escape cancels a turn", ">"
```

### 2. Empty input re-prompt

```bash
uv run boo press enter
sleep 1
uv run boo screen
# Expect: a second bare ">" prompt, no error
```

### 3. Simple chat turn (streaming)

```bash
uv run boo type $'What is 2+2? Answer with just the number, nothing else.\n'
sleep 8
uv run boo screen
# Expect: "4" in the response, followed by ">"
```

### 4. Tool invocation (read_file, auto-allowed)

```bash
uv run boo type $'Use the read_file tool to read the file hello.txt. You must call the tool.\n'
sleep 12
uv run boo screen
# Expect: [tool: read_file]
#         [tool: read_file → ok] test-sentinel
```

### 5. Permission prompt (bash tool, grant)

```bash
uv run boo type $'Use the bash tool to execute: echo boo-test-sentinel\n'
sleep 10
uv run boo screen
# Expect: "Allow ExecuteCommand on 'echo boo-test-sentinel' by 'bash'? (y/n):"

uv run boo type $'y\n'
sleep 8
uv run boo screen
# Expect: [tool: bash → ok] ... boo-test-sentinel
```

### 6. Permission denial (bash tool)

```bash
uv run boo type $'Use the bash tool to execute: echo denied-sentinel\n'
sleep 10
uv run boo screen
# Expect: permission prompt

uv run boo type $'n\n'
sleep 10
uv run boo screen
# Expect: [tool: bash → error] Permission denied.
#         "denied-sentinel" should NOT appear in stdout output
```

### 7. Escape cancellation

```bash
uv run boo type $'Write a 3000-word essay about the history of computing.\n'
sleep 3
uv run boo press escape
sleep 2
uv run boo screen
# Expect: "[cancelled]" marker, then ">"
```

### 8. Post-cancellation recovery

```bash
uv run boo type $'Say the single word recovered and nothing else.\n'
sleep 10
uv run boo screen
# Expect: "recovered" in response, session still functional
```

### 9. Skill invocation (slash command)

```bash
uv run boo type $'/greet World\n'
sleep 12
uv run boo screen
# Expect: [tool: skill]
#         [tool: skill → ok]
#         "Hello, World! Boo says hi."
```

### 10. Extension tool invocation

```bash
uv run boo type $'Use the roll_dice tool. You must call the tool.\n'
sleep 12
uv run boo screen
# Expect: [tool: roll_dice]
#         "Allow WriteInWorkspace on 'roll_dice' by 'workspace:dice'?"

uv run boo type $'session\n'
sleep 10
uv run boo screen
# Expect: [tool: roll_dice → ok] You rolled a 6!
```

### 11. Session-scoped permission persists

```bash
uv run boo type $'Roll the dice again using the roll_dice tool.\n'
sleep 12
uv run boo screen
# Expect: [tool: roll_dice → ok] without a permission prompt
```

### 12. EOF exit (Ctrl+D)

```bash
uv run boo press d --ctrl
sleep 1
uv run boo alive
# Expect: "not alive" (exit code 1 from boo alive)
```

### 13. Slash command autocomplete

```bash
# Type "/p" — first matching command (e.g. /plan or whatever starts with "p")
# should appear with the suffix in gray and the cursor block on the first gray char.
uv run boo type /p
uv run boo screen
# Expect: "/p" in white, completion suffix in gray, cursor on first gray character.

# Accept the completion with Tab.
uv run boo press tab
uv run boo screen
# Expect: full command name in white (e.g. "/plan"), no ghost text, cursor at end.

# Clear the input and test "/cl" — matches "/clear" (first) and "/clamp" (second).
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo type /cl
uv run boo screen
# Expect: ghost text shows "ear" (from /clear — first registered match wins).

# Clear and test no-match case.
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo type /xyz
uv run boo screen
# Expect: no ghost text visible — cursor is the plain reverse-video blank cell.

# Clear the buffer before the next test.
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo press backspace
uv run boo stop
```

### Cleanup

```bash
uv run boo stop
rm -rf /tmp/ur-tui-test
```

## Tips

- **Sleep durations are conservative.** LLM response times vary by model and
  load. With a fast model, 5–8 seconds is usually enough for a simple turn.
  With a slow model or tool-heavy turn, allow 15–20s.

- **`boo screen` is your debugger.** Call it liberally between steps to see
  exactly what's on the terminal. The output matches what you'd see in the
  SDL window.

- **`boo screen --no-trim`** preserves trailing whitespace, which matters if
  you're checking for the `"> "` prompt (trimmed screens show just `">"`).

- **Model-dependent behavior.** Small models sometimes ignore tool-use
  instructions and answer inline. If a tool test fails because the model
  didn't call the tool, try a more explicit prompt or switch to a larger
  model.

- **The `--visible` flag** is for interactive debugging. `boo.sh` passes it
  by default; use `--headless` for CI or scripted runs where Boo works
  headless with `SDL_VIDEODRIVER=dummy`.
