# Skill lifecycle hooks

## Goal

Extend skill hooks with `before_run`, `before_tool`, `after_tools`, and
`after_run`. With them a skill can supply initial context, reject individual
tool calls, check the effects of each tool batch, and report how the prompt
run ended. The existing `before_stop` behavior and assistant-batch guarantees
do not change.

This extends [skills and before-stop hooks](2026-09-22-003-skills-and-hooks.md)
with the same command protocol, process runner, and hook feedback entry. It
follows [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), and [testing](../testing.md).

## Related code

- `src/skills.rs`: frontmatter parsing and hook definitions.
- `src/hooks.rs`: hook input, output validation, and command execution.
- `src/tools.rs`: tool name constants that `tools` filters are checked
  against.
- `src/acp.rs`: skill dispatch, which passes the invoked skill's hooks to the
  prompt run.
- `src/acp/prompt.rs`: turn-start saving, tool approval and execution,
  assistant-batch commits, `before_stop` continuations, and `finish`.
- `src/sessions.rs`: `HookFeedback`, transcript validation, and persistence.
- `src/openrouter.rs`: hook feedback projected into model requests.
- `src/compaction.rs`: hook feedback in summarizer material and estimates.
- `src/acp/convert.rs`: live hook updates and transcript replay.
- `examples/skills/goal/`: the existing `before_stop` example, which must keep
  working with the added input fields.

## Decisions

### Definitions

A skill declares at most one command per hook kind:

```yaml
hooks:
  before_run:
    command: python3 scripts/context.py
  before_tool:
    command: python3 scripts/check_call.py
    tools: [shell, apply_patch]
  after_tools:
    command: python3 scripts/format.py
    tools: [apply_patch]
  before_stop:
    command: python3 scripts/judge.py
  after_run:
    command: python3 scripts/report.py
```

`tools` is allowed only on `before_tool` and `after_tools`. It is a nonempty
list of distinct names from the concrete tool set; omitting it matches every
tool. Commands must be nonblank, and a known hook definition rejects unknown
fields. Unknown hook kinds and an empty `hooks` map remain accepted.

`Skill` holds a `Hooks` structure with one optional field per kind. Dispatch
passes the invoked skill's `Hooks` and directory to the prompt run in place of
today's single `hooks::Hook`. A closed `HookKind` enum names the kind in
inputs, errors, labels, and ACP titles.

Hooks run only in the prompt run that invoked their skill, as today. Invoking
the skill approves all of its hook commands in both Ask and Auto mode. Hooks
never run for ordinary user messages, `/compact`, headless prompts, replay,
or compaction requests, and they never run for another hook's command.

### Command protocol

The protocol is unchanged: `/bin/sh -c` in the skill directory, inherited
environment, one JSON object on stdin, and one JSON object on stdout from a
zero exit. Every input gains `kind`, `session_id`, `mode`, and `run_id` beside
the existing `skill`, `arguments`, `workspace`, `ox`, `model`, and `effort`.
`run_id` is a UUID created once per prompt run and shared by all of its hook
commands, so scripts can correlate the hooks of one run.

| Hook | Added input | Stdout |
| --- | --- | --- |
| `before_run` | None | `{}` or `{"message": "..."}` |
| `before_tool` | `tool`: `call_id`, `name`, raw `arguments` string | `{"decision": "allow"}` or `{"decision": "deny", "message": "..."}` |
| `after_tools` | `tools`: list of `call_id`, `name`, `arguments`, `outcome`, `text` | `{}` or `{"message": "..."}` |
| `before_stop` | `answer` | `{"decision": "continue" or "stop", "message": "..."}` |
| `after_run` | `outcome`, `answer` or null, `error` or null | `{}` |

Each kind has its own output type with `deny_unknown_fields`. A message must
be nonblank when present; `deny` and both `before_stop` decisions require one,
and `allow` takes none. Tool arguments stay a raw string so malformed model
arguments keep their normal failed-tool result.

Deadlines are local constants chosen by kind: 30 seconds for `before_run`, 10
for `before_tool`, 60 for `after_tools`, 600 for `before_stop`, and 5 for
`after_run`. The 16 KiB stdout limit, 4 KiB stderr tail, and two-second
cleanup grace apply to every kind. Errors name the skill and hook kind and
include the stderr tail.

Every hook command is shown as an ACP execute tool call titled
`<skill> <kind> hook`, as `before_stop` is today.

### Before the first model request

`before_run` runs once, after the skill invocation is saved and before the
first model request. Compaction, request retries, and `before_stop`
continuations do not rerun it. A message passes input admission and is saved
as hook feedback; `{}` saves nothing. A hook error ends the run before any
model request.

### Before each tool call

For each call matching the `tools` filter, `before_tool` runs before the Ask
mode permission request and before execution. Unknown tool names never match
and keep their unknown-tool result.

`allow` continues to the existing permission path. `deny` skips permission and
execution and records a failed tool result for that call:
`<skill> before_tool hook denied this call: <message>`. The model sees the
reason in that result, so a decision saves no hook feedback. Later calls in the
batch proceed. The hook cannot rewrite arguments.

A `before_tool` error ends the run with the batch incomplete. `finish` gives
the current call and every later call a failed `Not started: <error>` result
and commits the batch, as it does for other interruptions. This is the only
hook error that can leave an uncommitted batch.

### After each tool batch

`after_tools` runs once per tool-bearing assistant batch, after the batch
commits and before the next model request, when at least one call in the
batch matches its filter. Its input lists every matching call in call order
with its saved result, including failed and denied calls. It sees the
workspace after the whole batch, so a formatter cannot disturb a patch that is
still waiting to run in the same batch.

It does not run when the run stopped before the batch completed. A message
passes input admission and is saved as hook feedback; `{}` saves nothing. A
hook error ends the run; the committed batch stays saved and hook side effects
are not undone.

The input carries tool arguments and result text, not a list of changed
paths. A script that needs changed files inspects the workspace itself.

### Before stopping

`before_stop` keeps its behavior and its 50-continuation limit. Only
`continue` decisions count toward the limit.

### After the prompt run

`after_run` runs once for every prompt run that saved its skill invocation,
after `finish` has saved any outstanding batch and produced the result. It
runs inside the prompt run's future, so the operation guard stays held and the
response is sent after it ends. Input rejected by admission, or cancellation
observed before the invocation is saved, saves nothing and runs no hook.

The turn start's ACP updates are sent before the future starts, so a failure
after the save returns an error without reaching `finish`. Move those updates
into the future so their failure becomes `PromptOutcome::AcpUpdate` and
reaches `finish` and `after_run`. Admission and the store write stay synchronous, so rejected input
is still an immediate error response.

`outcome` is `finished`, `cancelled`, `token_limit`, `refused`, or `failed`,
derived from the result of `finish`, so a storage failure while finishing is
reported as `failed`. `answer` is set only for `finished` and `error` only for
`failed`.

`after_run` ignores the prompt's cancellation signal, because it may be
reporting that cancellation; its short deadline bounds it, including during
connection shutdown. Its ACP updates are best effort. Its failure is written
to stderr and never replaces the prompt run's result. Nothing it prints enters
the transcript.

### Transcript and model context

`HookFeedback` keeps `skill` and replaces `decision` and `message` with a
tagged enum:

- `BeforeRun { message }` immediately follows a skill invocation.
- `AfterTools { message }` immediately follows the last tool result of a
  tool-bearing assistant batch.
- `BeforeStop { decision, message }` follows an assistant message without tool
  calls, as today.

`HookDecision` stays the `before_stop` `continue` or `stop`; `before_tool` uses
a separate allow or deny type that is never saved. Transcript validation
checks each variant's placement. No migration is needed; recreate the
database.

Model requests send each feedback entry as a user-role message labeled
`Feedback from the <skill> <kind> hook:`. Replay rebuilds each entry as a
completed hook tool call. Hooks that saved nothing, `before_tool` decisions,
and `after_run` appear only in live updates. Compaction material and request
estimates include every variant. When a checkpoint covers a skill invocation,
projection repeats only the invocation; the summary carries its `before_run`
feedback.

## Naming

- **Hook kind**: `before_run`, `before_tool`, `after_tools`, `before_stop`, or
  `after_run`. `HookKind` in code and `kind` in hook input.
- **Hook feedback**: a transcript entry holding a hook's saved message from
  `before_run`, `after_tools`, or `before_stop`. It is neither a user message
  nor a tool result.
- **Hook decision**: `continue` or `stop` from `before_stop`, unchanged.
- **Tool decision**: `allow` or `deny` from `before_tool` for one model tool
  call. It is never saved.
- **Run ID**: a UUID identifying one prompt run in hook input, kept in
  `PromptRun`.

## Test plan

- Skill loading accepts each hook kind and `tools` filter, and rejects `tools`
  on other kinds, empty or duplicate filters, unknown tool names, blank
  commands, and unknown fields in known definitions.
- `before_run` feedback reaches the first model request once and is not
  repeated after a `before_stop` continuation; `{}` saves nothing; oversized
  feedback or a hook error ends the run before any model request; rejected
  input runs no hook.
- In `shell_permissions_control_execution_and_save_results`, a denial skips
  both permission and execution and its reason reaches the next model request,
  and an allowed call still requires Ask approval.
- A `before_tool` error saves the complete batch with `Not started` results for
  the current and later calls.
- `after_tools` runs once after the batch commits: a script that inspects the
  workspace sees every patch in the batch, its input lists failed and denied
  calls, and its feedback reaches the next model request. Its error leaves the
  batch saved.
- `after_run` receives `finished` with the answer, `cancelled` after
  cancellation during a tool, and `failed` when the turn start's ACP update
  fails after the save; a failing or timed-out `after_run` does not change the
  prompt result.
- Transcript round-trip, malformed-placement, replay, and compaction cases
  cover the three feedback variants.
- Hook output validation covers each kind's response type. Existing process
  cleanup coverage is reused. Tests use fake commands and the fake OpenRouter
  server.

## Implementation plan

1. `src/skills.rs`, `src/hooks.rs`, `src/acp.rs`: add `Hooks`, `HookKind`,
   filters, the common input fields, and per-kind input and output types.
   Move `before_stop` onto the generalized runner without changing its
   behavior.
2. `src/sessions.rs`, `src/openrouter.rs`, `src/compaction.rs`,
   `src/acp/convert.rs`: change `HookFeedback` to the tagged form with
   placement validation, labels, projection, and replay.
3. `src/acp/prompt.rs`: add `run_id`, move the turn start's ACP updates into
   the future, and run `before_run` before the model loop.
4. `src/acp/prompt.rs`: run `before_tool` before approval, record denials as
   failed results, and handle its errors in `finish`.
5. `src/acp/prompt.rs`: run `after_tools` after each tool-bearing commit.
6. `src/acp/prompt.rs`: run `after_run` on the result of `finish`.
7. `examples/skills/`: add a small skill that supplies initial workspace
   context, denies a tool call, checks each tool batch, and records the run
   outcome, with a README covering batch timing and the difference between a
   check message and a hook error. Confirm the goal example still passes its
   test.

## Documentation updates

- `AGENTS.md`: the skill, hook, prompt-run, and example entries.
- `eng/architecture.md`: skill definitions, hook ordering within the turn,
  `after_tools` batch timing, feedback placement, `after_run` after
  cancellation, invariant 15, and the "one hook point" constraint.
- `eng/glossary.md`: hook, hook feedback, and prompt run; add hook kind, tool
  decision, and run ID.
- `eng/testing.md`: the new example's test.
