# The bridge asks Claude's AskUserQuestion questions as a form

- Task: `2026-11-12-013-ask-user-question-bridge`
- Status: complete
- Plan: `.agents/plans/2026-11-12-013-ask-user-question-bridge.md`

## Changes

Implemented as the plan designs it, with the deviations below.

- `crates/claude-code-acp/src/questions.rs` (new): `form` and `answers`, plus unit tests for property names and order, titles, descriptions, `oneOf`/`anyOf` options with descriptions, the singular and plural message, malformed input, a chosen label, `Other` replacing a single choice, multi-select labels in option order followed by `Other`, a blank question left out, and unknown labels and wrong types ignored.
- `crates/claude-code-acp/src/bridge.rs`: `Bridge::initialize` takes `&self` and stores `ElicitationCapabilities::supports_form` in `forms`. `StartRequest` and `Updates::new` get it.
- `crates/claude-code-acp/src/main.rs`: calls `initialize` on the shared bridge; declares `questions`.
- `crates/claude-code-acp/src/runner.rs`: `StartRequest::ask_user_question`. Without it, `claude` gets `--disallowedTools AskUserQuestion` ahead of `--permission-mode`. A `can_use_tool` for `AskUserQuestion` runs `Updates::ask_user` in the same abortable task as a permission.
- `crates/claude-code-acp/src/updates.rs`: the shared pending tool call preamble is `ask_about(permission, kind)`, and the deny decision is `deny(permission, message)`. `ask_user` denies at once when the client takes no forms or the input has no showable questions. Otherwise it sends the tool call with kind `other`, holds `Asking`, sends `elicitation/create`, and maps accept, decline, and cancel/other/error to the plan's decisions and tool call statuses.
- `crates/claude-code-acp/tests/protocol.rs`: helpers `until_method` and `initialize_with`. New tests: `ask_user_question_becomes_a_form`, `declined_questions_deny_the_tool`, `withdrawn_questions_cancel_the_elicitation`, `clients_without_forms_disallow_ask_user_question`.
- `crates/claude-code-acp/README.md`: an "AskUserQuestion" section after "Tool permissions"; the ACP support summary and the cancellation paragraph mention question forms.
- `docs/acp-v2.md`: claude-code-acp rows for `initialize`, `session/cancel`, `session/request_permission`, `elicitation/create` (supported, with the mapping and the plain-text fallback), the agent's `$/cancel_request` (supported), and `elicitation.form` (supported).
- `scripts/smoke-acp.py`: `check_question`, run after `check_permission`, as the plan describes. It also checks that the form offers `Blue` and that `elicitation_resolved` was recorded.

Changed paths:

- `crates/claude-code-acp/src/questions.rs`
- `crates/claude-code-acp/src/bridge.rs`
- `crates/claude-code-acp/src/main.rs`
- `crates/claude-code-acp/src/runner.rs`
- `crates/claude-code-acp/src/updates.rs`
- `crates/claude-code-acp/tests/protocol.rs`
- `crates/claude-code-acp/README.md`
- `docs/acp-v2.md`
- `scripts/smoke-acp.py`

## Deviations

- `clients_without_forms_disallow_ask_user_question` reads the arguments the fake claude records in `calls`, rather than having the fake claude exit non-zero. It also checks that a flag follows `AskUserQuestion`, which covers the variadic-list requirement.
- `form` also returns `None` when a question's `options` is empty or an option has no string `label`. The plan does not cover that input. Claude's schema requires 2–4 labelled options.
- `ask_user` checks the capability and the input before it sends the tool call preamble. When it denies at once, the client gets neither a `tool_call_update` nor `requires_action` from it.
- The agent's `$/cancel_request` row is marked *supported* and also covers permission requests. Aborting a permission task already dropped its `SentRequest`, and dropping one sends `$/cancel_request`.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `make check-scripts` | passed | `/tmp/ox-013/check-scripts.log` |
| `make check-rust` | failed: fmt and clippy passed. All tests passed except `a_git_directory_git_rejects_is_refused_with_gits_reason` in `oxd --test worktrees`. That is the known git 2.50.1 wording failure, in code this change does not touch. | `/tmp/ox-013/check-rust.log` |
| `cargo test --workspace --no-fail-fast` | the same `worktrees` failure, and `streams_and_resumes_a_persisted_session` failed once (pre-existing flake, see Limitations). Every other target passed: claude-code-acp 21 unit + 53 of 54 protocol, ox 13, ox-protocol 12, oxctl 1, oxd 48 unit, daemon 60, worktrees 48 of 49. | `/tmp/ox-013/test-no-fail-fast.log` |
| `make smoke-claude` | passed, all checks, including `ask_user_question_answered_as_a_form` (response `Blue`). Haiku called AskUserQuestion on the first run. Claude's tool result read `Your questions have been answered: "Which color?"="Blue"`. | `/tmp/ox-013/smoke-claude.log`, artifacts `/private/tmp/ox-acp-c8cmd89k` |

Fixes undone to confirm the tests fail without them:

- Without `--disallowedTools AskUserQuestion`, `clients_without_forms_disallow_ask_user_question` fails.
- When `AskUserQuestion` is routed to `permission`, `ask_user_question_becomes_a_form`, `declined_questions_deny_the_tool` and `withdrawn_questions_cancel_the_elicitation` time out waiting for `elicitation/create`.
- When decline maps to "The user did not answer.", `declined_questions_deny_the_tool` fails.

## Limitations

- `streams_and_resumes_a_persisted_session` is flaky at HEAD: 5 failures in 30 runs of an unmodified `git archive HEAD` copy, and 1 in 15 with this change. Its turn ends `error` with `claude exited with exit status: 0: `. The fake claude exits right after printing its `result` line. `Output::read` sometimes sees the exit before that line: its one-shot `ready(lines.next_line())` drain can return `Pending` before the reactor marks stdout readable. This is outside this unit and was not fixed.
- An AskUserQuestion tool call's `kind` changes from `execute`, from Claude's assistant message, to `other` when the form opens, as the plan specifies. Every other tool call stays `execute`.
- These approved assumptions are implemented as written:
  - No field is required.
  - `Other` text replaces a single choice.
  - The message is generic.
  - Decline and cancel deny the tool.
  - A client without forms loses the tool for the whole `claude` process.
  - The smoke check depends on Haiku calling the tool.
- Option previews, the free-form `response` field, and URL elicitation are not implemented (plan non-goals).
- The app shows nothing for the elicitation until unit 014.
