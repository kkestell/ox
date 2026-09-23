# Ox

Ox is a local ACP coding agent using OpenRouter. Run `ox` to start the ACP server.

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
