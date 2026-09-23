# Naming review — whole codebase

Status: Resolved

## Scope and coverage

This review covers the production code in all 21 files under `src/**/*.rs`,
checked against `eng/glossary.md`, `eng/architecture.md`, `eng/code-style.md`,
and the communication rules in `AGENTS.md`. Only the `naming` lens was used.
`eng/reviews/2026-09-22-001-naming-review.md` was read first. Its resolved
findings and rejected claims are not raised again unless later code reopened
them.

Coverage gaps: test modules were checked by their test names and helper names,
not read line by line. `Makefile`, `scripts/run.py`, the example skills, and
the prompt Markdown files were not reviewed. The dated plans and reviews keep
their original vocabulary on purpose.

## Findings

### Medium

#### Naming

- **`/compact` is called a fourth session operation but runs as a prompt
  operation** (`src/acp/operations.rs:1`, `src/acp/operations.rs:15`): The
  module comment says the module "Allows at most one prompt, load, delete, or
  compaction per session". The `AGENTS.md` file map says the same, and
  `eng/architecture.md` calls `/compact` "the cancellable `/compact` session
  operation" and says "Prompt, load, delete, and `/compact` are session
  operations". The code has no compaction operation. `Operation` has only
  `Prompt`, `Load`, and `Delete`. `/compact` takes its guard from `try_prompt`
  (`src/acp.rs:733`) and is cancelled through `Operation::Prompt`. The test is
  named `manual_compact_command_uses_the_active_prompt_without_saving_a_message`,
  and the glossary defines a session operation as "One prompt, load, or
  delete", as does architecture invariant 1. As a result, a reader who trusts
  the module comment looks for a compaction variant that does not exist.
  `SessionOperations::cancel`, documented as "Signals the active prompt", also
  cancels `/compact` without saying so. The comment's "per session" also reads
  like a limit over the session's whole life. The intended meaning, as the
  glossary states, is at most one operation running at a time. Fix: state in
  one place that `/compact` is a prompt request and holds a prompt operation,
  which matches the code and the glossary. Then correct
  `src/acp/operations.rs:1` to say "at a time", and correct the `AGENTS.md`
  entry for `src/acp/operations.rs` and the two architecture sentences.

- **Three names each claim to describe how a prompt run ended**
  (`src/acp/prompt.rs:55`, `src/acp/prompt.rs:101`, `src/hooks.rs:168`):
  - `PromptOutcome` is the glossary's prompt outcome.
  - `PromptOutput` is documented as "How a prompt run ended", but it holds the
    ACP stop reason and the final answer.
  - `hooks::RunOutcome` is documented as "How a prompt run ended, derived from
    its final response." It is the `after_run` `outcome` field, and the
    glossary does not define it.

  Both enums have `Finished`, `Cancelled`, `TokenLimit`, and `Refused`
  variants. Within `src/acp/prompt.rs`, `outcome` is a `PromptOutcome` at `:91`
  and `:342`, but in `run_after_run` at `:532` it is a `RunOutcome`. As a
  result, a reader cannot tell from the names or comments which value hook
  scripts receive or why there are two. Fix: document `PromptOutput` as the
  stop reason and final answer a prompt run returns. Rename `RunOutcome` to
  `AfterRunOutcome`, or add a glossary term for the `after_run` outcome that
  says it is derived from the prompt run's result.

### Low

#### Naming

- **`RunHooks` names the run, not the hook source, and every holder is called
  `hooks`** (`src/hooks.rs:83`): A `RunHooks` holds the hooks from one source:
  either the global settings, or one skill together with its directory. Values
  of four different types are all bound as `hooks`:
  - a `Hooks` map, as the field `RunHooks::hooks`;
  - one `RunHooks`, as `for hooks in self.hooks_for(kind)` and in the closure
    parameters at `src/acp/prompt.rs:311`, `:477`, and `:514`;
  - a `Vec<RunHooks>`, as `PromptRun::hooks`;
  - a `Box<RunHooks>`, as `Dispatch::Skill::hooks`.

  This produces `hooks.hooks.command(kind)` at `src/acp/prompt.rs:393` and
  `src/hooks.rs:271`. `hooks_for` returns sources, not commands. The
  distinction matters at the one place where global and skill ordering is
  decided. Fix: rename the type `HookSource`, bind values as `source`, and
  rename `hooks_for` to `sources_for`.

- **`compaction::budget` returns three unnamed limits** (`src/compaction.rs:29`):
  The function returns `(usize, usize, usize)`. Callers bind the parts by
  position as `(admission, _, _)`, `(admission, trigger, _)`
  (`src/acp/prompt.rs:655`), and `(admission, _, target)`, or read `.0`
  (`src/compaction.rs:419`) and `.1` (`src/compaction.rs:541`).
  `eng/architecture.md` calls the middle value "the automatic threshold". The
  code calls it `trigger`, and the definition gives it no name. As a result, a
  swapped position compiles, and the definition does not say which limit is
  which. Fix: return a small struct with `admission`, `automatic_threshold`,
  and `cut_target` fields.

- **`append_user` also saves skill invocations** (`src/sessions.rs:774`): The
  method appends the settings block and a turn start, which is either a user
  message or a skill invocation. `PromptRun::save_turn_start` calls it with a
  `turn_start` (`src/acp/prompt.rs:270`). Its `settings` parameter is a
  `SessionSettingsChange`, which the caller names `settings_change`. Fix:
  rename the method `append_turn_start` and the parameter `settings_change`.

- **Hook runs are called "hook calls"** (`src/acp/convert.rs:190`,
  `src/acp/prompt.rs:34`): `hook_call_id`, `pending_hook_call`,
  `finished_hook_call_update`, and `replayed_hook_call` use the name "hook
  call", and so does the `NO_FEEDBACK` comment. `AGENTS.md` says "hook tool
  calls". The glossary term is "hook run". It defines a tool call as
  model-produced, and `eng/architecture.md` says a hook run "is not a model
  tool call". Fix: use `hook_run_id`, `pending_hook_run`,
  `finished_hook_run_update`, and `replayed_hook_run`, and say "hook run" in
  the comment and in `AGENTS.md`.

- **`HookDecision` names only the `before_stop` decision**
  (`src/sessions.rs:186`, `src/hooks.rs:238`): `before_tool` hooks also return
  a decision, `ToolDecision`, so the generic name covers only one of the two.
  The `before_stop` response type is `StopDecision`, and it contains a
  `HookDecision`, so `output.decision` at `src/acp/prompt.rs:597` goes from one
  decision name to the other. Fix: rename `HookDecision` to `StopDecision` and
  the response struct to `StopResponse`, and rename the glossary entry
  "Hook decision" to "Stop decision".

- **Prompt text is called a user message before slash commands are
  recognized** (`src/acp/convert.rs:26`, `src/acp.rs:146`):
  `prompt_to_user_message` and `dispatch(user_message, …)` apply that name to
  text that may be `/compact` or a skill invocation, and neither becomes a user
  message. The glossary says Ox recognizes a slash command "before saving
  anything". `Dispatch::UserMessage` is the one case where the text actually
  becomes a user message. Fix: rename the function `prompt_text`, and rename
  the `dispatch` parameter and the binding at `src/acp.rs:729` to
  `prompt_text`.

- **`main::load_settings` also downloads and installs the model catalog**
  (`src/main.rs:148`): Its name matches `settings::load`, which it calls, but
  it also calls `fetch_catalog` and `install_catalog`. So a network request and
  the default-model check are hidden behind a name that suggests reading one
  file. Fix: rename it `load_settings_and_catalog`.

- **"prefix" stands for the system prompt** (`src/acp.rs:387`): The comment
  reads "A repeated load keeps the prefix and skill catalog captured by the
  first load." The value it describes is `system_prompt`. Elsewhere, "prefix"
  means a compaction checkpoint's covered transcript prefix. Fix: say "keeps
  the system prompt and skill catalog".

- **The settings and skill loaders read files through `system_prompt`**
  (`src/system_prompt.rs:42`, `src/settings.rs:42`, `src/skills.rs:67`):
  `system_prompt::read_text` is the 32 KiB UTF-8 reader for `AGENTS.md`,
  `settings.json`, and each `SKILL.md`. At two of its three call sites, the
  module name suggests that settings or a skill is system prompt text. Fix:
  move the reader somewhere that covers all three callers, and name it for its
  bound, for example `read_bounded_text`.

- **Summarizer settings are named "summary"** (`src/openrouter.rs:35`,
  `src/openrouter.rs:275`): `CatalogModel::summary_effort` and `summary_body`
  build the summarizer request. `eng/architecture.md` calls this effort "its
  summarizer effort", and the glossary uses "summarizer requests" and
  "summarizer cost". "Summary" also names the compaction summary and the
  session summary. Fix: rename them `summarizer_effort` and `summarizer_body`.

- **The link-allowing workspace check lost the name the last review gave it**
  (`src/tools/workspace.rs:50`): Finding 7 of the 2026-09-22 naming review
  gave the two workspace path checks names that state their symbolic-link
  rule. `b225d17` replaced `workspace_path_allowing_link_target` with
  `Workspace::resolve_existing`, which no longer states that rule. Its partner,
  `patch::workspace_path_rejecting_links` (`src/tools/patch.rs:176`), kept its
  name. As a result, a reader adding a tool again cannot tell from the names
  which check applies which rule. Fix: rename the method
  `resolve_allowing_link_target`.

## Checks run

- Read the production code of all 21 files under `src/**/*.rs`, and the test
  function and helper names in each test module.
- Read `eng/glossary.md`, `eng/architecture.md`, `eng/code-style.md`,
  `AGENTS.md`, and `eng/reviews/2026-09-22-001-naming-review.md`.
- Searched `src/`, `AGENTS.md`, and `eng/` for `hook call`, `hook_call`,
  `RunOutcome`, `PromptOutput`, `append_user`, `budget(`, and
  `system_prompt::read_text`, and traced each occurrence.
- Ran `git log -S` and `git show b225d17` to confirm which commit removed
  `workspace_path_allowing_link_target`.
- After the fixes: `cargo fmt --all -- --check`,
  `cargo build --all-features`,
  `cargo clippy --all-targets --all-features -- -D warnings`, and
  `cargo test --all-targets --all-features` (96 passed) are clean. Both example
  test commands in `eng/testing.md` pass.
- Repeated the searches for every old name. The only matches left are Python's
  `Path.read_text` in the example tests.

## Resolutions

Every finding is resolved as suggested. The resolutions, in finding order:

- `src/acp/operations.rs` now says it allows at most one prompt, load, or
  delete to run for a session at a time, and that `/compact` runs as a prompt
  operation. `SessionOperations::cancel` says it also cancels a running
  `/compact`. `AGENTS.md`, the glossary's session operation entry, and
  `eng/architecture.md` say the same.
- `PromptOutput` is documented as the ACP stop reason and final answer a prompt
  run returns. `hooks::RunOutcome` is now `hooks::AfterRunOutcome`, documented
  as the `after_run` input's `outcome`.
- `RunHooks` is `HookSource`, and its values are bound as `source`.
  `PromptInput::hooks` and `PromptRun::hooks` are `hook_sources`, and
  `Dispatch::Skill::hooks` is `hook_source`. `hooks_for` is `sources_for`.
- `compaction::budget` returns a `Budget` struct with `admission`,
  `automatic_threshold`, and `cut_target` fields. No caller reads a position
  anymore.
- `append_user` is `append_turn_start`, with a `settings_change` parameter. Its
  test is named `turn_start_append_adopts_a_session_title_once_and_updates_activity`.
- The hook run update functions are `hook_run_id`, `pending_hook_run`,
  `finished_hook_run_update`, and `replayed_hook_run`. The comments and
  `AGENTS.md` say "hook run".
- `HookDecision` is `StopDecision`, and the `before_stop` response struct is
  `StopResponse`. The glossary entry is now "Stop decision", and
  `eng/architecture.md` uses the new term.
- `convert::prompt_to_user_message` is `convert::prompt_text`. The `dispatch`
  parameter and the prompt handler's binding are `prompt_text`, and the
  `convert` module comment says prompt text.
- `main::load_settings` is `load_settings_and_catalog`.
- The load comment says "system prompt" instead of "prefix".
- The bounded reader moved into the new `src/text_file.rs` as
  `text_file::read_bounded`, with its `MAX_BYTES`. `AGENTS.md` lists the new
  module.
- `summary_effort` and `summary_body` are `summarizer_effort` and
  `summarizer_body`.
- `Workspace::resolve_existing` is `resolve_allowing_link_target`.

## Verdict

The core vocabulary still holds: transcript, turn start, ACP selections, saved
settings, tool call title, and session title each have one name and match their
definitions. There are two medium findings. The code and documents disagree on
whether `/compact` is its own session operation, and three names each describe
how a prompt run ended. The low findings are local renames and comment fixes,
and one of them restores a resolution from the previous review. None of the
findings changes runtime behavior. All of them are now resolved, and full
validation passes.
