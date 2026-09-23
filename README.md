# Ox

Ox is a local ACP coding agent using OpenRouter. Run `ox` to start the ACP server.

## Auth and headless runs

`ox auth login` saves an OpenRouter API key in the system keyring; `ox auth logout` removes it. `OPENROUTER_API_KEY` takes precedence over the saved key.

`ox run [--dir <workspace>] [--model <model-id>] [--effort <default|low|medium|high>] '<prompt>'` runs one prompt and prints its final answer. Headless runs do not invoke skills.

## Skills and hooks

Put a `SKILL.md` with `name` and `description` YAML frontmatter and Markdown instructions at `.agents/skills/<name>/SKILL.md`, then invoke it as `/<name> <arguments>` in an ACP session. Skills can declare `hooks` with commands for `before_run`, `before_tool`, `after_tools`, `before_stop`, and `after_run`. Hook commands receive JSON on stdin and return JSON on stdout. See the [goal](examples/skills/goal/README.md) and [careful](examples/skills/careful/README.md) examples for definitions and the protocol.

## Settings

Ox has no built-in models. `ox` and `ox run` require `~/.config/ox/settings.json` with at least one model; the first is the default:

```json
{
  "models": [
    {
      "id": "deepseek/deepseek-v4.1-flash",
      "name": "DeepSeek V4.1 Flash",
      "context_limit": 1048576,
      "effort_mapping": {"low": "low", "medium": "high", "high": "max"}
    }
  ],
  "hooks": {"before_run": {"command": "printf '{}'"}}
}
```

`make install` overwrites this file with [examples/settings.json](examples/settings.json). `effort_mapping` gives the OpenRouter effort sent for each Ox effort level. Optional global `hooks` run from `~/.config/ox` before invoked skill hooks and also apply to `ox run`. Ox reads the file at startup.
