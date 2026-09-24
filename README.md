# Ox

Ox is a local ACP coding agent using OpenRouter. Run `ox` to start the ACP
server.

## Getting Started

Extract and install `ox`:

```bash
tar -xzf ox-<version>-<target>.tar.gz
install ox ~/.local/bin/ox
```

Save your OpenRouter API key to the system keychain:

```bash
ox auth login
```

Configure `ox` in your ACP client of choice, or run it headless:

```bash
ox run 'hello, ox'
```

## Auth

`ox auth login` saves an OpenRouter API key in the system keyring;
`ox auth logout` removes it. `OPENROUTER_API_KEY` takes precedence over the
saved key.

## Settings

Create `~/.config/ox/settings.json` before starting `ox`. The `model` field is
required and names the default model:

```json
{
  "model": "deepseek/deepseek-v4.1-flash"
}
```

Ox reads the file at startup, so restart it after editing settings.
`make install` overwrites the file with
[examples/settings.json](examples/settings.json).

Ox loads its model choices and effort levels from OpenRouter at startup.

## Skills

Ox loads `.agents/skills/<name>/SKILL.md` from the workspace when a session
becomes active. Each file has YAML frontmatter with `name` and `description`,
followed by Markdown instructions. A prompt whose first word is `/<name>`
invokes that skill and passes the remaining text as its arguments.

Skills may also declare hooks, described in [Skill Hooks](#skill-hooks).

The [goal](examples/skills/goal/README.md) and
[careful](examples/skills/careful/README.md) skills show complete definitions.

## Hooks

Hooks run commands at these points in a prompt run:

| Hook kind     | When it runs                                     |
| ------------- | ------------------------------------------------ |
| `before_run`  | Before the first model request                   |
| `before_tool` | Before each tool call                            |
| `after_tools` | After a batch of tool calls                      |
| `before_stop` | After an answer that would finish the prompt run |
| `after_run`   | After the prompt run ends                        |

Hook commands receive JSON on stdin and return JSON on stdout.

### Global Hooks

Declare global hooks under `hooks` in `~/.config/ox/settings.json`. They apply
to prompt runs in every workspace. Their commands run from `~/.config/ox`, so
relative script paths start there.

When a global hook and a skill hook have the same kind, Ox runs the global hook
first. See the
[global hooks example](examples/skills/careful/README.md#global-hooks) for a
configuration you can adapt.

### Skill Hooks

Declare skill hooks under `hooks` in a skill's `SKILL.md`. They run only in a
prompt run that invokes the skill. Their commands run from the skill directory.

The [careful skill](examples/skills/careful/README.md) documents the input and
output for every hook; the [goal skill](examples/skills/goal/README.md) shows
how `before_stop` can continue a prompt run.

## Headless prompts

`ox run [--dir <workspace>] [--model <model-id>] [--effort <effort>] '<prompt>'`
runs one headless prompt and prints its final answer. The effort level must be
`default` or one the model lists. Headless prompts do not invoke skills.
