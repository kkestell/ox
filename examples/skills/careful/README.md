# Careful skill

`/careful <request>` asks Ox to work on a request in a Git workspace with four
hooks, all run by `scripts/careful.py`:

- `before_run` gives the model the workspace's Git status before its first
  model request.
- `before_tool` denies shell commands that can destroy work, such as `rm -rf`,
  `git reset --hard`, `git clean -f`, and `git push`.
- `after_tools` runs `git diff --check` after each batch of patches and reports
  whitespace errors to the model.
- `after_run` appends how the prompt run ended to `runs.jsonl` in the skill
  directory.

## Installation

Copy this directory into a Git workspace:

```sh
mkdir -p <workspace>/.agents/skills
cp -R examples/skills/careful <workspace>/.agents/skills/careful
```

Ox loads the workspace's skills when a session is created or first loaded, so
start a new session or restart Ox after copying. The hooks need `python3` and
`git`. Add `.agents/skills/careful/runs.jsonl` to the workspace's `.gitignore`
to keep the run log out of the Git status.

## Hook definitions

A skill declares at most one command per hook kind under `hooks`:

```yaml
hooks:
  before_run:
    command: python3 scripts/careful.py
  before_tool:
    command: python3 scripts/careful.py
  after_tools:
    command: python3 scripts/careful.py
  after_run:
    command: python3 scripts/careful.py
```

Each definition has a nonblank `command`, run with `/bin/sh -c` in the skill
directory. No other fields are allowed. The script checks tool names to apply
its shell and patch rules. The [goal skill](../goal/README.md) shows
`before_stop`.

Skill hooks run only in the prompt run that invoked their skill. Invoking the
skill approves all of its hook commands in both Ask and Auto mode. Skill hooks
do not run for ordinary messages, `/compact`, or headless prompts.

## Global hooks

To enable hooks across workspaces, define them in `~/.config/ox/settings.json`
next to its required `model`:

```json
{
  "model": "deepseek/deepseek-v4.1-flash",
  "hooks": {
    "before_tool": { "command": "python3 scripts/careful.py" },
    "after_run": { "command": "python3 scripts/careful.py" }
  }
}
```

For this example, copy `scripts/careful.py` to
`~/.config/ox/scripts/careful.py`. All five hook kinds are supported. Commands
run in `~/.config/ox`; their `workspace` input identifies the session workspace.
Global input uses `"skill": null` and empty `arguments`. Hook definitions use
the same rules as skills, including ignoring unknown hook kinds. Omitted or
null `hooks`, or an empty map, enables none. Invalid settings fail startup with the file path.
Restart Ox to pick up edits.

Global commands run on ordinary ACP and headless prompts in both Ask and Auto
mode. At each hook point, the global command runs before the invoked skill's
command. Both run even if the first denies a tool call or asks to continue.
Either denial blocks a call; either continuation requests another model
response, counting once toward the limit. Each hook's feedback is saved.
Ordinary hook errors stop the run; `after_run` attempts both commands even if
one fails. Hooks do not execute during `/compact`, replay, or compaction.

Ox sets `OX_IN_HOOK=1` for every hook command. Nested Ox processes inherit the
marker and skip global hooks, preventing recursive invocation.

## Hook protocol

Ox writes one JSON object to the command's stdin and closes it. Every hook
receives these fields:

```json
{
  "kind": "before_tool",
  "skill": "careful",
  "arguments": "Tidy the notes.",
  "session_id": "…",
  "mode": "ask",
  "run_id": "…",
  "workspace": "/abs/workspace",
  "ox": "/abs/path/to/ox",
  "model": "…",
  "effort": "medium"
}
```

`run_id` is shared by every hook command of one prompt run. The command exits
zero and prints one JSON object to stdout:

| Hook          | When it runs                                              | Added input                                                        | Stdout                                                              | Deadline |
| ------------- | --------------------------------------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------- | -------- |
| `before_run`  | Once, after the user message or skill invocation is saved | None                                                               | `{}` or `{"message": "..."}`                                        | 30 s     |
| `before_tool` | Before each tool call and its permission request          | `tool`: `call_id`, `name`, raw `arguments` string                  | `{"decision": "allow"}` or `{"decision": "deny", "message": "..."}` | 10 s     |
| `after_tools` | Once per tool batch, after the batch is saved             | `tools`: list of `call_id`, `name`, `arguments`, `outcome`, `text` | `{}` or `{"message": "..."}`                                        | 60 s     |
| `before_stop` | After each finished answer                                | `answer`                                                           | `{"decision": "continue" or "stop", "message": "..."}`              | 600 s    |
| `after_run`   | Once, after the prompt run's result is known              | `outcome`, `answer` or null, `error` or null                       | `{}`                                                                | 5 s      |

A message is nonblank. A `before_run` or `after_tools` message is saved and sent
to the model as feedback. A denial skips the permission request and the call,
and gives the call a failed result:
`skill /careful before_tool hook denied this call: <message>`. The model sees
the reason in that result, and later calls in the batch still run.

`after_run`'s `outcome` is `finished`, `cancelled`, `token_limit`, `refused`,
or `failed`. `answer` is set only for `finished` and `error` only for `failed`.
`after_run` also runs after cancellation, so it can report it. Nothing it
prints reaches the model.

Every command has 16 KiB of stdout. Ox keeps the last 4 KiB of stderr for its
error message. On cancellation, Ox sends SIGTERM to the command's process group
and SIGKILL two seconds later; `after_run` is not cancelled, and its deadline
bounds it. The command inherits Ox's environment, including
`OPENROUTER_API_KEY`.

### Batch timing

A model response can contain several tool calls, which Ox runs in order as one
batch. `after_tools` runs once, after every call in the batch has a result and
the batch is saved, and before the next model request. It sees the workspace
after the whole batch, so a check cannot disturb a patch still waiting to run.
Its `tools` input lists every call in call order with its result,
including failed and denied calls. It carries tool arguments and result text,
not changed paths, so this skill inspects the workspace with `git diff --check`.
`after_tools` does not run for a batch the prompt run stopped before
completing.

### Check messages and hook errors

A check message is feedback: the run continues, and the model receives the
message with its next request. Use one for problems the model can fix, such as
the whitespace errors this skill reports.

A hook error ends the prompt run with an error: any nonzero exit, a timeout, or
output that is not one valid object for the hook kind. Use one when the hook
itself cannot work, such as `git` failing outside a Git repository. Work already
saved stays saved, and effects in the workspace are not undone. A `before_tool`
error gives the current call and every later call in its batch a failed
`Not started: <error>` result. An `after_run` error is written to Ox's stderr
and does not change the prompt run's result.

## Testing the hooks

```sh
python3 -m unittest discover -s examples/skills/careful/scripts
```

The test uses a temporary Git repository and needs no OpenRouter key.
