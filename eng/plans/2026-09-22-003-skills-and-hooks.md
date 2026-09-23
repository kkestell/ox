# Skills and before-stop hooks

## Goal

Support workspace skills invoked as slash commands. A skill may declare a
`before_stop` hook: an external command that judges each finished answer during
the prompt run that invoked the skill and either sends the model back to work
or lets the run stop. Remove the built-in `/init` and `/goal` commands. Prove
the design with an instruction-only `init` skill in this repository and an
example goal skill whose Python judge calls `ox run`.

A hook belongs to one prompt run. When that run ends, nothing remains enabled.
Invoking the skill again starts a new run with new arguments.

This plan follows [architecture](../architecture.md),
[code style](../code-style.md), [glossary](../glossary.md), and
[testing](../testing.md).

## Related code

- `src/acp.rs`: fixed command advertisement, `/init` substitution, the `/goal`
  stub, `ActiveSession`, prompt dispatch, and the headless entry point.
- `src/acp/prompt.rs`: `PromptInput`, input admission, the model loop,
  `PromptOutcome`, `commit`, and `finish`.
- `src/sessions.rs`: `TranscriptEntry`, `validate_transcript`,
  `saved_settings`, `append_user`, entry encoding, and session title adoption.
- `src/compaction.rs`: `projection`, `projection_at`, `ranked_cuts`, and
  `material`.
- `src/openrouter.rs`: `chat_messages`, which turns transcript entries into
  request messages.
- `src/acp/convert.rs`: tool call updates and `replay_transcript`.
- `src/tools/shell.rs`: process-group spawning, output capture, and cleanup.
- `src/main.rs`: `ox run`, which currently prints nothing on success.
- `src/prompts/init_prompt.md`: the `/init` prompt, which moves to
  `.agents/skills/init/SKILL.md`.

## Decisions

### Skill definitions

A skill is `.agents/skills/<name>/SKILL.md` directly under the session
workspace. The file has YAML frontmatter followed by Markdown instructions:

```markdown
---
name: goal
description: Work toward an objective until a judge accepts the result.
argument-hint: "<objective>"
hooks:
  before_stop:
    command: python3 scripts/judge.py
---

Work toward the objective given as arguments. Verify the result before
finishing.
```

Ox reads `name`, `description`, `argument-hint`, and `hooks`, and ignores other
keys. `name` must match the directory and contain only lowercase letters,
digits, and hyphens. `description` and the instructions must be nonblank. The
file may be at most 32 KiB. `hooks` ignores unknown hook kinds and accepts an
empty map. A `before_stop` definition must contain only one nonblank `command`.
Parse the frontmatter with one serde YAML crate.

Load the skill catalog when a session becomes active and keep it in
`ActiveSession`, beside the system prompt; a repeated load keeps both. A
missing `.agents/skills` directory means no workspace skills. An invalid
definition, or a skill named `compact`, fails session activation with its
path, like an invalid `AGENTS.md`.

Ox has no built-in skills. Delete the built-in `/init` command, `INIT_PROMPT`,
and `src/prompts/init_prompt.md`, and delete the `/goal` stub. Move the init
prompt's text into `.agents/skills/init/SKILL.md` in this repository as the
instructions of an instruction-only skill, so `/init` keeps working here as a
workspace skill. `/goal` exists when a workspace installs the example.

Advertise `/compact` and every catalog skill as ACP commands. `argument-hint`
becomes the command's `UnstructuredCommandInput` hint.

### Invocation

A prompt whose first word is `/<name>` for a catalog skill invokes it. The
rest of the text, trimmed, is its arguments, kept as literal data. Other
slash-prefixed text remains an ordinary user message. Headless `ox run` never
invokes skills.

An invocation saves a skill invocation entry in place of the user message:

```rust
TranscriptEntry::SkillInvocation(SkillInvocation {
    name: String,
    arguments: String,
    instructions: String,
})
```

The saved instructions keep model context stable when `SKILL.md` later
changes. Everywhere Ox treats a user message as the start of a turn, a skill
invocation does the same: the settings block comes immediately before it,
`append_user` saves it with the settings block in one transaction, and it
passes the same input admission. Session title adoption and replay use
`/<name> <arguments>`. Model requests send it as one user-role message:

```text
Skill /<name> invoked.

Instructions:
<instructions>

Arguments:
<arguments>
```

The hook command and skill directory are not saved. They come from the
catalog and are passed to the prompt run through `PromptInput`.

### Hook feedback

Hook feedback is saved as:

```rust
TranscriptEntry::HookFeedback(HookFeedback {
    skill: String,
    decision: HookDecision, // Continue or Stop
    message: String,
})
```

It follows an assistant message with no tool calls. Model requests send it as
a user-role message labeled `Feedback from the <skill> before_stop hook:`.
Saving a `stop` decision gives the next turn the judge's conclusion as model
context.

### Compaction

A long hook-driven run is the run most likely to compact past its own skill
invocation. When the latest compaction checkpoint covers the latest skill
invocation and no user message follows that invocation, `projection` and
`projection_at` repeat the skill invocation's message right after the
summary. Once a later user message exists, the invocation is ordinary history
carried by the summary. Input admission and request estimates get the rule
through those functions. `ranked_cuts` sizes each cut from a fixed base plus
suffix sums, so it adds the repeated message's size to every cut past the
invocation.

`ranked_cuts` treats a skill invocation as the start of a turn, like a user
message. `material` includes skill invocations as user requests and hook
feedback under its label.

### Hook protocol

Run `/bin/sh -c <command>` in the skill directory. The hook inherits the
environment, including `OPENROUTER_API_KEY`. Write one JSON object to stdin
and close it:

```json
{
  "skill": "goal",
  "arguments": "Make the parser tests pass.",
  "workspace": "/abs/workspace",
  "ox": "/abs/path/to/ox",
  "model": "…",
  "effort": "medium",
  "answer": "…"
}
```

`ox` is `std::env::current_exe()`. `answer` is the text of the assistant
message just committed.

The hook must exit zero and print exactly one JSON object to stdout:

```json
{"decision": "continue", "message": "Two parser tests still fail. Fix them."}
```

| Decision   | Effect                                                   |
| ---------- | -------------------------------------------------------- |
| `continue` | Save the feedback and make another model request         |
| `stop`     | Save the feedback and end the prompt run with `EndTurn`  |

`message` must be nonblank. `stop` covers both an achieved objective and a
judge that finds the agent blocked on the user; the message says which.

Limits are local constants: a 600-second deadline, 16 KiB of stdout, a 4 KiB
stderr tail, and 50 continuations per prompt run. Output overflow, invalid
UTF-8 or JSON, an unknown decision, a blank message, a nonzero exit, a
timeout, or a further `continue` past the limit ends the run with a hook error.
The error names the skill and includes the stderr tail. Nothing is saved for
a failed hook.

### Prompt run

After `run_model_loop` commits an assistant batch with an `openrouter::Stop::Finished`
stop, and the run was started by a skill invocation with a `before_stop` hook,
run the hook:

1. Return `Cancelled` if cancellation was requested.
2. Send an ACP tool call of kind execute with an Ox-generated ID
   `hook-<uuid>` and tool call title `<skill> before_stop hook`, then mark it
   in progress.
3. Run the hook, selecting on cancellation.
4. On success, check the feedback against input admission, save it, extend the
   transcript copy, and send the finished tool call update with the message.
   Feedback that does not fit is a hook error.
5. `continue` loops to the next model request in the same `PromptRun`.
   `stop` returns `Finished`.

A hook error sends a failed tool call update and returns a new
`PromptOutcome::Hook(io::Error)`. Like `OpenRouter`, it leaves no uncommitted
assistant batch and becomes an ACP error response. Refusal, token limit,
request failure, and cancellation never run a hook. Invoking the skill is the
approval to run its hook, in both Ask and Auto mode.

The hook's ACP tool call is only an ACP update. It is not a model tool call,
never enters an assistant batch, and replay rebuilds it from the saved
`HookFeedback` as a completed tool call with the same title and message.

### Process execution

Move the shared child-process code from `src/tools/shell.rs` into
`src/process.rs`: spawn in a new process group, optional stdin bytes written
concurrently with capture, bounded stdout and stderr capture, deadline and
cancellation, and group cleanup. Shell parsing, the API-key removal, and
`ToolOutcome` rendering stay in the shell tool. Hook JSON handling lives in
`src/hooks.rs`.

Cleanup sends SIGTERM to the group, waits until the group is empty or a grace
period passes, then sends SIGKILL and reaps the child. The shell tool uses no
grace period, keeping its current behavior. Hooks use two seconds so a nested
`ox run` can stop its own shell process groups. The operation guard stays held
through cleanup.

### Headless output

`prompt::run` returns the ACP stop reason together with the final answer: the
text of the assistant message committed with a `Finished` stop. ACP builds
`PromptResponse` from the stop reason. `ox run` prints the answer to stdout on
`EndTurn` and prints nothing otherwise. Headless execution treats SIGTERM like
SIGINT.

### Example goal skill

Ship `examples/skills/goal/` with `SKILL.md` and `scripts/judge.py`, standard
library only. The judge:

1. Reads the hook input.
2. Runs `ox run --dir <workspace> --model <model> --effort <effort> <prompt>`
   as an argument list, with `OX_DATA_DIR` set to a temporary directory it
   deletes afterward. The prompt asks the judge to inspect the workspace
   against the objective and the answer without changing files, and to reply
   with only the decision JSON, using `stop` when the objective is met or the
   agent needs the user.
3. Extracts one JSON object from the output, accepting a code fence around it,
   validates it, and prints it. Invalid output or a failed `ox run` exits
   nonzero with the reason on stderr.

## Naming

- **Skill**: A `SKILL.md` definition and its directory, in the workspace's
  `.agents/skills/`.
- **Skill catalog**: The skills available to an active session, loaded when it
  becomes active.
- **Skill invocation**: A transcript entry holding a skill's name, arguments,
  and instructions, saved in place of the user message for that turn.
- **Hook**: An external command a skill declares for `before_stop`, the point
  after a finished answer is committed.
- **Hook decision**: `continue` or `stop`.
- **Hook feedback**: A transcript entry holding a hook's skill, decision, and
  message. It is neither a user message nor a tool result.
- **Hook continuation**: A model request in the same prompt run caused by a
  `continue` decision.

A hook's `stop` decision is distinct from an OpenRouter stop.

## Test plan

Extend existing tests; the test-function count stays flat except where noted.

- `slash_commands_have_acp_metadata_and_prompt_dispatch`: the catalog
  advertisement with input hints, a workspace skill with literal arguments, and unknown slash text as a user
  message.
- `activation_validates_the_session_and_captures_the_system_prompt_once`: the
  catalog loads once, and an invalid definition or a collision fails
  activation. Include a definition with extra keys like the repository's own
  skills.
- `read_rejects_a_malformed_transcript` and
  `empty_transcripts_are_valid_and_settings_fold_in_order`: placement rules for
  the two new entries and a settings block before a skill invocation.
  `a_saved_batch_survives_database_reopen_in_order`: both entries round-trip.
- `user_append_adopts_a_session_title_once_and_updates_activity`: a skill
  invocation adopts `/<name> <arguments>`.
- `a_text_answer_is_saved_in_the_transcript`, or one new prompt test if that
  one cannot hold it: `continue` then `stop` with a fake hook script, the
  continuation limit, a failing hook with nothing saved, and no hook after
  refusal, token limit, or cancellation.
- `automatic_compaction_precedes_the_next_model_request`: compaction during a
  hook continuation repeats the covered skill invocation after the summary.
  `manual_compaction_uses_a_summary_and_keeps_the_complete_transcript`: both
  entries appear in compaction material.
- `tool_updates_carry_tool_call_titles_failed_statuses_and_unparsable_arguments`:
  replay of hook feedback as a completed execute tool call.
- `timeout_and_cancellation_stop_the_group` and `environment_excludes_api_key`:
  kept against the moved process code. Hook stdin, the grace period, and API-key
  inheritance join them.
- `headless_sigint_cleans_up_and_saves_even_with_repeated_signals`: SIGTERM,
  and stdout printing the final answer on success and nothing on failure.
- `judge.py`: one Python test with a fake `ox` executable covering both
  decisions, fenced output, invalid output, and a failed run. It needs no
  OpenRouter key.

## Implementation plan

1. `src/skills.rs`: skill definition, frontmatter parsing, and catalog
   loading. Add the YAML crate. Move `src/prompts/init_prompt.md` to
   `.agents/skills/init/SKILL.md` with `name: init` and a description.
2. `src/acp.rs`: keep the catalog in `ActiveSession`, advertise it, and replace
   `prompt_user_message` with dispatch returning compact, skill invocation, or
   user message. Delete the built-in `/init`, `INIT_PROMPT`, and the `/goal`
   stub. Pass the invoked skill through
   `PromptInput`.
3. `src/sessions.rs`: add `SkillInvocation`, `HookFeedback`, and
   `HookDecision`, with encoding, validation, and settings folding. Let
   `append_user` save either a user message or a skill invocation; add
   `append_hook_feedback`.
4. `src/openrouter.rs`, `src/compaction.rs`, `src/acp/convert.rs`: request
   messages for both entries; after-summary repetition in `projection` and
   `projection_at`; skill invocations as turn starts in `ranked_cuts`; both
   entries in `material`; hook tool call updates and replay.
5. `src/process.rs`, `src/tools/shell.rs`, `src/hooks.rs`: move the process
   code with the grace period, then implement the hook protocol.
6. `src/acp/prompt.rs`: the hook step after a finished commit,
   `PromptOutcome::Hook`, and the continuation limit.
7. `src/acp/prompt.rs`, `src/acp.rs`, `src/main.rs`: return the final answer,
   print it from `ox run`, and handle SIGTERM.
8. `examples/skills/goal/`: `SKILL.md`, `scripts/judge.py`, and a README with
   the hook protocol and copy installation into `.agents/skills/goal/`.

## Documentation updates

- `AGENTS.md`: add `src/skills.rs`, `src/hooks.rs`, `src/process.rs`, and
  `examples/skills/goal/`; update the entries for `src/acp.rs`,
  `src/acp/prompt.rs`, `src/sessions.rs`, `src/tools/shell.rs`, and
  `src/main.rs`; remove `src/prompts/init_prompt.md`.
- `eng/architecture.md`: slash commands (`/compact` only built in) and skills, the two transcript
  entries and their placement, the hook step in the turn boundary, the
  after-summary repetition, hook execution under capability and trust
  (inherits the API key, approved by invocation), and headless stdout.
- `eng/glossary.md`: the terms above, and skill invocation in the transcript
  entry list.
- `eng/testing.md`: `ox run` prints the final answer; testing the example
  judge.
