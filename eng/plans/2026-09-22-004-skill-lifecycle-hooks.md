# Skill lifecycle hooks

## Goal

Extend skill hooks with `after_tool`, `before_tool`, `before_run`, and
`after_run`. Skills can check tool effects, reject individual tool calls,
supply initial context, and report the final prompt outcome. Preserve the
existing `before_stop` continuation behavior and complete assistant batches.

Build on [skills and before-stop hooks](2026-09-22-003-skills-and-hooks.md).
Its implementation and tests are still being completed; this plan extends
that work rather than introducing a second hook mechanism. Follow
[architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), and [testing](../testing.md).

## Related code

- `src/skills.rs`: frontmatter parsing and the session's skill catalog.
- `src/hooks.rs`: hook inputs, output validation, and command execution.
- `src/process.rs`: bounded capture, deadlines, cancellation, and cleanup.
- `src/acp.rs`: skill dispatch and the operation guard around a prompt run.
- `src/acp/prompt.rs`: input admission, tool approval and execution,
  assistant-batch commits, hook continuations, and final response construction.
- `src/sessions.rs`: hook feedback, transcript validation, and persistence.
- `src/openrouter.rs`: hook feedback projected into model requests.
- `src/compaction.rs`: feedback in summaries, request estimates, and cuts.
- `src/acp/convert.rs`: live hook updates and transcript replay.

## Decisions

### Definitions and scope

Keep one optional command per hook kind on the invoked skill:

```yaml
hooks:
  before_run:
    command: python3 scripts/context.py
  before_tool:
    command: python3 scripts/check_call.py
    tools: [shell, apply_patch]
  after_tool:
    command: python3 scripts/check_result.py
    tools: [apply_patch]
  before_stop:
    command: python3 scripts/judge.py
  after_run:
    command: python3 scripts/report.py
```

Use a concrete `Hooks` structure with optional fields and a closed `HookKind`
enum. Replace the singular hook passed through dispatch and `PromptInput`
with the invoked skill's hook definitions and directory. Read the name and
arguments from the saved skill invocation for the current turn.

`tools` is an optional list of exact names from the concrete tool set. Omission
matches every known tool; an empty list, duplicate name, or unknown name is a
definition error. Reject `tools` on other known hook kinds. Ignore unknown hook
kinds and accept an empty `hooks` map, while rejecting unknown fields and blank
commands within known hook definitions. Instruction-only skills remain valid.

Hooks run only for the explicitly invoked skill and expire with its prompt
run. Invoking the skill authorizes its declared commands under the existing
environment and working-directory rules. Catalog loading, replay, ordinary
user messages, `/compact`, and headless prompts do not activate hooks. Hooks
do not intercept other hook commands or compaction model requests.

### Command protocol

Keep JSON on stdin, one JSON object on stdout, commands executed in the skill
directory, inherited environment, and the shared process runner. Include
`kind`, `session_id`, and a per-run UUID `run_id` in every input, alongside
`skill`, `arguments`, `workspace`, `ox`, `model`, `effort`, and `mode`. All
calls in one prompt run share `run_id`; hook continuations retain it.

Use event-specific Rust input and output types. Decode stdout according to
the requested hook kind and reject fields belonging to another response type.

| Hook | Additional input | Successful stdout |
| --- | --- | --- |
| `before_run` | None; the invocation arguments identify the task. | `{}` or `{"message":"Current workspace context..."}` |
| `before_tool` | `tool`: call ID, name, and the raw arguments string. | `{"decision":"allow"}` or `{"decision":"deny","message":"Reason..."}` |
| `after_tool` | `tool` plus `result`: actual outcome kind and bounded text. | `{}` or `{"message":"Check diagnostics..."}` |
| `before_stop` | `answer`: the committed finished answer. | Existing `continue` or `stop` decision with a message. |
| `after_run` | `outcome`, nullable `answer`, and nullable `error`. | `{}` |

Messages must be nonblank when present; denial and before-stop decisions
require them. Allow decisions carry no message. Tool arguments remain a raw
string because malformed model arguments must retain their normal failed-tool
behavior. Unknown tool names bypass tool hooks and reach the existing
unknown-tool result.

Use these command deadlines as local constants: 30 seconds for `before_run`,
10 for `before_tool`, 60 for `after_tool`, the existing 600 for `before_stop`,
and 5 for `after_run`. Retain 16 KiB stdout, the 4 KiB stderr tail, and the
existing two-second process-group cleanup grace. Cleanup and output draining
may extend beyond the command deadline. Protocol and process errors name the
skill and hook kind and include the stderr tail.

### Before the first model request

Run `before_run` once after the skill invocation is admitted and saved, before
the first ordinary model request. It does not run again after compaction,
request retries, or a before-stop continuation.

Save a returned message as hook feedback after checking input admission; it
becomes model context without changing the captured system prompt. `{}` adds
no transcript entry. A process error, invalid response, or oversized feedback
ends the run before model work. The accepted invocation remains saved, and
`after_run` receives the failure.

### Before each tool call

For each matching call from a validated completion, run `before_tool` before
Ask-mode shell permission and before tool execution. Recheck cancellation
before every new hook, permission request, or tool operation.

An allow decision proceeds to the existing permission path. A deny decision
skips both permission and execution, and becomes a failed `ToolResult` for
the original call with the skill name and denial reason. Other calls in the
completion proceed in order. This gives the model an actionable result while
preserving one result per call. Denial is a successful hook execution, not a
hook protocol error.

Do not rewrite tool arguments. Do not append separate model feedback for
before-tool decisions: the denied tool result already carries the reason,
and an allow decision supplies no context.

A failed before-tool command ends the run. `finish` must now handle
`PromptOutcome::Hook` with an uncommitted assistant batch: preserve observed
results, give the current and remaining unstarted calls explicit failed
results, and attempt to commit the complete batch before returning the error.

### After tools, at the assistant-batch boundary

Dispatch `after_tool` after all calls in a completion have resolved and the
complete assistant batch has committed, before the next model request. Run
the hook once for each matching call actually passed to `tools::execute`, in
call order, including calls that returned a failed outcome. Denied calls and
unstarted placeholders do not qualify. Keep this eligibility information with
the uncommitted assistant batch until commit; it is not durable state.

This timing is deliberate: formatter commands see the workspace after the
whole batch and cannot invalidate patches still waiting in that batch. Each
invocation receives the historical result of its associated call, but sees
the current workspace, including changes from later calls and earlier hooks.
Document this distinction in the protocol.

Only dispatch after-tool hooks on the normal path after a successful batch
commit. If cancellation or another run-level failure interrupted the batch,
save observed results through existing finalization and skip new after-tool
work. `after_run` reports the interruption.

Admit and save each nonempty hook message before its successful finished ACP
update and before starting another hook. A check failure expressed as a
message is model feedback, not a failed original tool result. A hook protocol
or process error stops the run; the original batch and any earlier saved
feedback remain intact. Hook side effects are not rolled back.

The initial protocol exposes existing raw arguments and bounded result text.
It does not claim to know every changed path, especially for shell commands.
Checks and formatters must inspect the workspace or use skill-specific paths;
they must not rely on parsing truncated tool output as an exhaustive file list.

### Before stopping

Keep `before_stop` behavior and its continuation limit. Only a finished model
answer reaches it; neither tool feedback nor run reporting counts toward the
50-continuation limit. It continues to judge an answer that has already been
streamed and committed.

### After the prompt run

Run `after_run` once after normal finalization has attempted to save any
outstanding assistant batch and determined the response. Keep the operation
guard held through this command and its cleanup, then send the original
response. It runs after any saved skill invocation, including before-run
failure, refusal, token limit, cancellation, and storage or transport failure.
Rejected input and cancellation before invocation persistence do not run it.

Expose `outcome` as `finished`, `cancelled`, `token_limit`, `refused`, or
`failed`. Include an answer only for `finished`, and an error string only for
`failed`. Use the outcome after finalization so a storage failure is reported
instead of an earlier apparent success. This reports Ox's response decision,
not confirmation that the ACP client received it.

Move invocation saving and its initial ACP notifications inside the owned
asynchronous run path, and track whether persistence succeeded before sending
updates. An update failure immediately after the save must still reach this
finalizer. Keep startup validation failures before persistence hook-free.

`after_run` observes a cancellation that has already happened: use its own
short command deadline rather than the already-latched prompt cancellation
signal. It cannot resume the model or change the response. Its own failure is
reported on stderr and through a best-effort failed ACP hook update without
replacing the primary outcome. A disconnected ACP client must not prevent the
command from running. Do not start it from `Drop`, retry it, or rerun it after
restart; process termination can prevent delivery.

After-run output supplies no model context and is not saved as hook feedback.
Scripts that record outcomes use the supplied identifiers themselves.

### Transcript and ACP representation

Replace the before-stop-only payload with a `HookFeedback` containing the
skill name and a tagged enum with these variants:

- `BeforeRun { message }`
- `AfterTool { call_id, message }`
- `BeforeStop { decision, message }`

Keep `HookDecision` scoped to before-stop `Continue` and `Stop`; define a
separate allow/deny type for before-tool responses. Successful commands with
no feedback, before-tool decisions, failed commands, and after-run observers
do not create additional transcript entries.

Validation requires before-run feedback immediately after its skill
invocation, at most once. After-tool feedback forms a contiguous sequence
after a complete tool-bearing assistant batch, references calls in that batch
in order without duplicates, and belongs to the current skill invocation.
Before-stop feedback follows an assistant message without tool calls and
belongs to that invocation. Never insert feedback between an assistant
message and its tool results.

Project saved feedback as labeled user-role messages including hook kind and,
for after-tool feedback, call ID. Generalize ACP hook titles and replay from
the same fields. Replay denial through the original failed tool result;
successful no-message hooks and after-run observers have only live updates.

Include all feedback variants in compaction material and estimates. Keep
assistant batches indivisible and retain the existing repetition of the
active skill invocation after a summary. A cut may leave trailing feedback
after the summary; labels must remain understandable without the original
call. Do not rerun hooks during projection or replay.

## Naming

- **Hook kind**: one of the five supported execution points, represented by
  `HookKind` and the `kind` input field.
- **Hook feedback**: durable model context from `before_run`, `after_tool`, or
  `before_stop`; distinct from tool results and operational hook errors.
- **Before-tool decision**: allow or deny for one model tool call; separate
  from a before-stop hook decision.
- **Run ID**: a UUID identifying one prompt run for its hook commands; kept in
  `PromptRun` and shared across its hook continuations.

## Test plan

- Extend catalog activation and slash-dispatch tests for each optional hook,
  exact tool filters, malformed definitions, and captured per-run settings.
- Extend the prompt harness's hook coverage for before-run feedback reaching
  the first request once, empty responses, oversized feedback, and no startup
  hook on rejected input or cancellation before persistence.
- Extend `shell_permissions_control_execution_and_save_results` for hook
  denial suppressing permission and execution, allowed calls retaining Ask
  approval, and denial reasons reaching the next model request.
- Extend ordered tool-result and interruption tests: after-tool scripts run
  only after every call and the batch commit; a formatter cannot affect an
  unstarted patch in the same batch; actual failed calls qualify, denied calls
  do not; hook failure preserves the batch and earlier feedback. A before-tool
  failure resolves and saves all outstanding call results.
- Extend the before-stop lifecycle test to prove continuations do not repeat
  before-run setup or invoke after-run early. Add an after-run lifecycle test
  only if existing lifecycle coverage cannot express its distinct guarantee:
  once per saved invocation, finalization failures reflected in its input,
  cancellation and disconnected transport still reach it, and observer failure
  or timeout does not replace the original result.
- Extend transcript round-trip, malformed-transcript, replay, and compaction
  cases for the new feedback placements and call associations. Verify the
  model projection as well as the saved transcript.
- Extend existing hook/process cases for response validation and shorter
  deadlines. Reuse process-group cleanup coverage rather than duplicating it
  for every hook kind. Use fake commands and the existing fake OpenRouter
  server; no live model or external notification service is needed.

## Implementation plan

1. `src/skills.rs`, `src/hooks.rs`, `src/acp.rs`: introduce optional hook
   definitions, filters, `HookKind`, captured invocation data, common command
   context, and typed event inputs and responses. Adapt `before_stop` to the
   shared runner and kind-aware errors without changing its behavior.
2. `src/sessions.rs`, `src/openrouter.rs`, `src/compaction.rs`,
   `src/acp/convert.rs`: implement tagged feedback, placement validation,
   labels, persistence, model projection, and replay. Keep operational
   no-message hooks out of the transcript.
3. `src/acp/prompt.rs`: add after-tool eligibility to the batch's owned state,
   dispatch after-tool hooks after commit, admit feedback, and preserve
   original outcomes on hook failure. Complete the ordered-batch tests.
4. `src/acp/prompt.rs`: run before-tool decisions before permission, save
   denials as failed tool results, and handle hook failures while a batch is
   incomplete. Complete permission and interruption coverage.
5. `src/acp/prompt.rs`: move turn-start persistence into the asynchronous
   lifecycle and run before-run setup once. Preserve admission, setting
   capture, session title adoption, and saved-before-model guarantees.
6. `src/acp/prompt.rs`, `src/acp.rs`: separate finalization from response
   return sufficiently to run the bounded after-run observer with the actual
   outcome and the operation guard still held. Preserve primary errors and
   make observer ACP updates best effort.
7. `examples/skills/`: add a small documented skill demonstrating initial
   workspace context, an explicit denied tool call, a deterministic
   after-tool check, and local outcome recording. Document formatter timing,
   empty responses, and the distinction between check diagnostics and hook
   execution errors. Keep the existing goal example's before-stop protocol
   working with the added common input fields.

## Documentation updates

- `AGENTS.md`: update the hook, skill, prompt-run, process, and example entries
  to match the extended responsibilities.
- `eng/architecture.md`: hook ordering, after-tool batch timing, feedback
  placement, invocation scope, and bounded after-run work after cancellation.
- `eng/glossary.md`: extend hook and hook-feedback definitions; add hook kind,
  before-tool decision, and run ID without changing prompt outcome terminology.
- `eng/testing.md` and the skill examples: protocol fixtures, local examples,
  outcome reporting, and which successful hooks are absent from replay.
