# Ox

Ox is a local ACP coding agent using OpenRouter. Run `ox` to start the ACP server.

## Image input

ACP prompts can include up to four PNG, JPEG, WebP, or GIF images, with at most
10 MiB of decoded image data total. Select an image-capable model before the
first turn of the session. Ox saves images for session replay and later model
requests. Text-only models reject image prompts before saving them.

## Background commands

The `shell` tool normally waits for its command to finish. With
`background: true` it starts the command, such as a development server or a
long build, and returns a process ID at once; background commands have no
timeout. The `shell_process` tool then works with the session's background
commands:

- `list` shows each process ID, command, and state.
- `read` returns the state and the output kept for stdout and stderr, and can
  wait up to 30 seconds for the command to end.
- `write` sends text to the command's stdin exactly as given, and can close
  stdin afterward. Input is plain text through a pipe, not a terminal.
- `stop` sends SIGTERM to the command's process group and SIGKILL two seconds
  later if it is still running.

Ox keeps the last 14 KiB of each output stream, so repeated reads can show the
same output and earlier output is dropped; redirect a full log to a workspace
file when you need it. A session keeps up to 16 background commands and makes
room by forgetting the oldest finished one.

A background command keeps running across turns and when a prompt is cancelled.
Deleting the session, closing the ACP connection, a SIGINT, SIGTERM, or SIGHUP
to Ox, and the end of an `ox run` stop its whole process group at once with
SIGKILL, without the grace period of an explicit `stop`. A command that
survives Ox being killed with SIGKILL keeps running. Loading a saved session
shows the earlier results but starts nothing again.

In Ask mode, Ox asks before starting a command and before each `write`,
including one that only closes stdin. Approving a start does not approve later
input. Listing, reading, and stopping need no approval.

## Auth and headless runs

`ox auth login` saves an OpenRouter API key in the system keyring; `ox auth logout` removes it. `OPENROUTER_API_KEY` takes precedence over the saved key.

`ox run [--dir <workspace>] [--model <model-id>] [--effort <effort>] '<prompt>'` runs one prompt and prints its final answer. The effort must be `default` or one the model lists. Headless runs do not invoke skills.

## Skills and hooks

Put a `SKILL.md` with `name` and `description` YAML frontmatter and Markdown instructions at `.agents/skills/<name>/SKILL.md`, then invoke it as `/<name> <arguments>` in an ACP session. Skills can declare `hooks` with commands for `before_run`, `before_tool`, `after_tools`, `before_stop`, and `after_run`. Hook commands receive JSON on stdin and return JSON on stdout. See the [goal](examples/skills/goal/README.md) and [careful](examples/skills/careful/README.md) examples for definitions and the protocol.

## Settings

`ox` and `ox run` download OpenRouter's model catalog at startup and keep the models released in the last six months, other than `:batch` variants, that accept tools, take and produce text, and have a context limit above 8,000 tokens, sorted by name. Each model offers Default plus the reasoning efforts OpenRouter lists for it. They also require `~/.config/ox/settings.json`, whose `model` names the default model:

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "hooks": {"before_run": {"command": "printf '{}'"}}
}
```

`make install` overwrites this file with [examples/settings.json](examples/settings.json). Optional global `hooks` run from `~/.config/ox` before invoked skill hooks and also apply to `ox run`. Ox reads the file at startup.
