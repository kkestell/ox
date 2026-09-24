# Goal skill

`/goal <objective>` asks Ox to work toward an objective. After each finished
answer, the skill's `before_stop` hook runs `scripts/judge.py`, which asks a
separate headless `ox run` in the same workspace to judge the answer. The judge
either sends Ox back to work with feedback or ends the prompt run.

The nested run is an Auto-mode agent with full shell access in the same
workspace; only its prompt asks it to avoid changing files, with no permission
restriction enforcing that instruction.

## Installation

Copy this directory into the workspace:

```sh
mkdir -p <workspace>/.agents/skills
cp -R examples/skills/goal <workspace>/.agents/skills/goal
```

To use it in every workspace, copy it into `~/.config/ox/skills/goal` instead.

Ox loads skills when a session is created or first loaded, so start a new
session or restart Ox after copying. The judge needs `python3` and
uses the session's model and effort level with the same OpenRouter key.

## Skill definition

A skill is `<name>/SKILL.md` in a skills directory, such as the workspace's
`.agents/skills/`: YAML frontmatter followed by Markdown instructions.

- `name`: The command name. It matches the directory and contains only
  lowercase letters, digits, and hyphens.
- `description`: The command description shown by the ACP client.
- `argument-hint`: Optional hint for the command's arguments.
- `hooks`: Optional hook commands, each run with `/bin/sh -c` in the skill
  directory. This skill declares `hooks.before_stop.command`. The
  [careful skill](../careful/README.md) describes every hook kind.

Ox ignores other top-level keys and unknown hook kinds. An empty `hooks` map
declares no hook; a `before_stop` definition must contain only a nonblank
`command`. The file is at most 32 KiB, and the description and instructions are
nonblank.

## Hook protocol

A `before_stop` hook runs after each assistant message that finishes the answer
without tool calls, during the prompt run that invoked the skill. It judges
only the main agent's answers: subagents run global hooks but never skill
hooks, and an answer that subagent messages superseded before it was judged
is not judged at all. Invoking the
skill approves running its hook in both Ask and Auto mode. The hook inherits
Ox's environment, including `OPENROUTER_API_KEY`, and `OX_IN_HOOK=1`. The
nested headless judge inherits this marker and skips global hooks.

Ox writes one JSON object to the hook's stdin and closes it:

```json
{
  "kind": "before_stop",
  "skill": "goal",
  "arguments": "Make the parser tests pass.",
  "session_id": "…",
  "mode": "ask",
  "run_id": "…",
  "workspace": "/abs/workspace",
  "ox": "/abs/path/to/ox",
  "model": "…",
  "effort": "medium",
  "answer": "…"
}
```

`ox` is the running Ox executable, `run_id` identifies the prompt run across
its hook commands, and `answer` is the text of the finished assistant message.

The hook exits zero and prints exactly one JSON object to stdout:

```json
{"decision": "continue", "message": "Two parser tests still fail. Fix them."}
```

| Decision   | Effect                                                                                                 |
| ---------- | ------------------------------------------------------------------------------------------------------ |
| `continue` | Ox saves the feedback and makes another model request                                                  |
| `stop`     | Ox saves the feedback and ends the prompt run unless a global `before_stop` hook requests continuation |

`message` is nonblank. Use `stop` both when the objective is met and when the
agent needs the user; the message says which. The saved feedback is model
context for later requests.

The hook has 600 seconds and 16 KiB of stdout. A prompt run permits 50 hook
continuations, counting once when both global and skill hooks request one. Ox
keeps the last 4 KiB of stderr for its error message. Any other
exit status, output that is not one valid decision object, a timeout, or a
`continue` past the limit ends the prompt run with an error, and nothing is
saved for that hook run. On cancellation, Ox sends SIGTERM to the hook's
process group and SIGKILL two seconds later.

## Testing the judge

```sh
python3 -m unittest discover -s examples/skills/goal/scripts
```

The test uses a fake `ox` executable and needs no OpenRouter key.
