# Skills and hooks review

## Resolution

The user confirmed that hooks should remain visible in the ACP client, including
on replay, so the visibility finding and its proposed removals are dismissed.
The four remaining findings are addressed: unknown hook kinds and empty hook
maps are accepted; generic Rust hook code uses hook terminology; hook inputs
use the saved skill invocation; and the example README explains the nested
agent's permissions. The original findings and verdict below describe the
reviewed state before these fixes.

Validation after the fixes: all 91 Rust tests and the Python test passed;
`cargo build`, `cargo fmt --check`, and `git diff --check` passed.
`cargo clippy --all-targets` reported only the existing `too_many_arguments`
warning on `compaction::compact`.

The fixes add no test functions and add 32 net lines to existing Rust tests
(49 added, 17 removed), covering shared hook declarations on session creation
and loading, malformed known hooks, and the updated hook input API. Across the
complete change from the previous commit, Rust test modules add one test
function and 719 net lines (810 added, 91 removed); the new Python test file
adds one test function and 112 lines. That growth covers the new hook protocol,
continuation and failure behavior, persistence, replay, compaction, process
cleanup, and the example's nested run, which had no coverage before this
feature.

## Scope and coverage

Reviewed the uncommitted skills and `before_stop` hook work on the `rust`
branch against
[skills and before-stop hooks](../plans/2026-09-22-003-skills-and-hooks.md):
`src/skills.rs`, `src/hooks.rs`, `src/process.rs`, the changes to `src/acp.rs`,
`src/acp/prompt.rs`, `src/acp/convert.rs`, `src/sessions.rs`,
`src/compaction.rs`, `src/openrouter.rs`, `src/tools/shell.rs`, and
`src/main.rs`, `examples/skills/goal/`, `.agents/skills/init/`, and the
documentation changes in `AGENTS.md` and `eng/`.

This was a general review using the correctness, naming, api-design,
architecture, concurrency, resources, testing, and documentation lenses. It
covers the two points the user raised: hook runs shown in the ACP client, and
the word "judge" in Ox's Rust code.

[Skill lifecycle hooks](../plans/2026-09-22-004-skill-lifecycle-hooks.md) is not
implemented yet, so this review does not cover it.

## Findings

### Medium

#### Correctness

- **Hook runs are shown in the ACP client** (`src/acp/prompt.rs:340`,
  `src/acp/convert.rs:143`, `src/acp/convert.rs:210`): The user's requirement
  is that hook runs never appear in the ACP client. Right now each run sends a
  pending execute tool call titled `<skill> before_stop hook`, then an
  in-progress update, then a completed or failed update carrying the hook's
  message. Replay also rebuilds each saved `HookFeedback` as a completed tool
  call. The plan specified this behavior (Prompt run, step 2, and the paragraph
  after it), so the code matches the plan but not the requirement. The prompt
  test checks the updates through `described_updates`, and
  `tool_updates_carry_tool_call_titles_failed_statuses_and_unparsable_arguments`
  checks the replay.

  Fix:
  - Delete `hook_call_id`, `hook_call_title`, `pending_hook_call`,
    `finished_hook_call_update`, and `replayed_hook_call`.
  - In `replay_transcript`, skip `HookFeedback` the same way it skips model and
    settings entries.
  - Merge `run_hook` and `judge` into one function that checks cancellation,
    runs the hook, and saves its feedback.
  - A hook error still reaches the client as the prompt's error response.
  - Update the tests, the `hook run | tool kind execute` row in
    `eng/glossary.md:26`, the paragraph at `eng/architecture.md:232`, the
    `src/acp/convert.rs` entry in `AGENTS.md`, and the plan's replay sentence if
    you keep plans current.

  One side effect needs a decision. Once the tool call is gone, the next
  answer's message chunks follow the previous answer's chunks with nothing
  between them, so a client may join the two answers into one message. The
  client also shows no activity while a hook runs, which can take up to 600
  seconds.

#### API design

- **Another agent's `hooks` frontmatter fails every session in the workspace**
  (`src/skills.rs:43`): `.agents/skills/` is shared with other agents, and in
  this repository it is symlinked into `.claude/skills/`. Ox ignores unknown
  top-level keys, but `hooks` is parsed with `deny_unknown_fields` and requires
  `before_stop`. A `SKILL.md` that declares hooks in another agent's format,
  such as a `PreToolUse` key, or that contains `hooks: {}`, fails
  `session/new` and `session/load` for the whole workspace. The failure is
  confirmed by the `hooked` case in
  `activation_validates_the_session_and_captures_the_system_prompt_once`. I did
  not check which other agents currently read a `hooks` key from `SKILL.md`.

  Fix: ignore hook kinds Ox doesn't know, so that only a malformed
  `before_stop` is an error. Otherwise, document in the example README and
  `eng/architecture.md` that `hooks` is reserved for Ox in shared skill
  directories. Plan 004 keeps strict rejection, so decide this before
  implementing it.

### Low

#### Naming

- **"judge" is in Ox's own code** (`src/acp/prompt.rs:363`,
  `src/acp/prompt.rs:374`): The word came from the plan, which uses "judges" as
  the verb for what a hook does (Goal, and Hook protocol). It is not in the
  glossary. In the Rust code it appears in:
  - `PromptRun::judge`, which runs the hook and saves its feedback;
  - the panic message `"a hook judges a committed answer"`;
  - the test instructions `"Work until the judge stops you."` at
    `src/acp/prompt.rs:816`, `src/acp/prompt.rs:1145`, `src/sessions.rs:906`,
    and `src/compaction.rs:496`;
  - the skill fixtures in `src/acp.rs:901`, `941`, `984`, and `993`, which
    copy the example's `python3 scripts/judge.py`.

  Ox's concept is a hook that returns a hook decision. "Judge" belongs only to
  the goal example, where `judge.py` and its prompt use it correctly.

  Fix:
  - The merge in the first finding removes `judge`. If the functions stay
    separate, rename it for what it does, for example `run_and_save_hook`.
  - Use `"a before_stop hook runs on a committed answer"` as the panic message.
  - Use neutral fixture text, for example `"Work until the hook stops you."`
    and `python3 scripts/check.py`.
  - In `eng/testing.md:18`, write "the example goal skill's hook script".
  - `AGENTS.md:63` describes the example and can keep the word.

#### Architecture

- **The hook duplicates the skill invocation** (`src/hooks.rs:28`,
  `src/acp.rs:146`): `hooks::Hook` repeats `skill` and `arguments` from the
  `SkillInvocation` built next to it in `dispatch`. `PromptRun` then holds both
  copies, one in `hook` and one in the saved transcript entry. The only data
  the catalog adds is `command` and `directory`. Nothing reads the copies
  inconsistently today, but plan 004 adds hook kinds, and two sources for the
  invocation's name and arguments would spread to each of them.

  Fix: have `Hook` hold only `command` and `directory`. Have `hooks::run` take
  the `SkillInvocation`, or its name and arguments, from the prompt run's turn
  start.

#### Documentation

- **The example README doesn't say the nested run can change files**
  (`examples/skills/goal/README.md:40`): `judge.py` runs `ox run`, which is
  always in Auto mode with full shell access. Only the prompt asks it not to
  change files. The README describes the nested run as judging the answer and
  says nothing about the nested run's permissions.

  Fix: add one sentence saying that the nested run is an Auto-mode agent in
  the same workspace and that the prompt, not a permission, keeps it
  read-only.

## Checks run

- `cargo test`: 91 passed, including
  `a_before_stop_hook_continues_stops_and_fails_without_saving` and
  `headless_signals_clean_up_and_save_even_when_repeated`.
- `cargo clippy --all-targets`: one existing `too_many_arguments` warning on
  `compaction::compact`, which this work did not change.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: 1 passed.
- `grep` for "judge" across `src/`, `eng/`, `AGENTS.md`, and `examples/`.
- Traced by hand: hook-feedback placement and validation, compaction cut
  sizing with the repeated invocation, the continuation limit, cancellation
  between commit and hook, stdin writes to a hook that never reads them, and
  SIGTERM handling in `judge.py` and the nested `ox run`.

## Verdict

The core design is sound and well tested: skill loading, invocation, feedback
persistence, compaction repetition, and process cleanup all behave as planned.
Before committing, remove the hook's ACP tool calls and their replay, and
decide how consecutive answers should be separated in the client. Also decide
how strictly `hooks` is parsed before plan 004 builds on it. The "judge"
cleanup is small, and most of it goes away with the first fix.
