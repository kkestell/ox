# Simplification and deletion review

## Scope and coverage

This review covers the whole `src/` tree at `942f374`, looking for code that can
be simplified or deleted without changing observable behavior. Lenses:
architecture, readability, rust-idioms, dependencies, comments, and testing.

All non-test source was read in full. Test modules were reviewed by listing
every test name in the large modules and reading the bodies of tests that
looked like overlaps; the remaining test bodies (about 7,000 lines, mostly in
`src/acp.rs` and `src/acp/prompt.rs`) were not read line by line. The example
skills under `examples/` and `Cargo.toml` were not reviewed.

## Findings

### Medium

#### Architecture

- **Two implementations of output capture and cleanup**
  (`src/process.rs:44`, `src/process.rs:208`, `src/shell_processes.rs:341`):
  `process::run`, used by ordinary shell calls and hooks, and
  `shell_processes::supervise`, used by background commands, each have their
  own copy of the same steps: read stdout and stderr fairly, stop on a read
  error, clean up the process group, drain the pipes within a deadline, and
  fold late read errors into either the final state or the diagnostics. The
  two copies share only `Capture::append` and `ProcessGroup`. Evidence: both
  build `"Reading {name} failed: {error}"` (`src/process.rs:222`,
  `src/process.rs:252`, `src/shell_processes.rs:384`,
  `src/shell_processes.rs:478`); both write the same "Output capture stopped
  before EOF" diagnostic (`src/process.rs:248`, `src/shell_processes.rs:434`);
  both apply the rule that a read error decides the state only after a natural
  exit (`src/process.rs:250-261`, `src/shell_processes.rs:441-452`); and
  `Capture.done`, `Capture.error`, `Capture::read`, and `Capture::drain` exist
  only for the `process::run` copy, while `supervise` keeps its own buffers,
  `Stream` enum, and `drain` function. The copies already drain differently:
  `run` puts a single `grace + OUTPUT_DRAIN_TIMEOUT` deadline on the drain,
  while `supervise` allows `OUTPUT_DRAIN_TIMEOUT` after cleanup finishes.
  Because `run`'s grace period is never interrupted, the two produce the same
  result, but a fix to one copy will not reach the other. `AGENTS.md` says
  supervisors "share its output capture", which is only partly true.
  Suggested fix: move one capture routine into `process.rs`. It should read
  both pipes into a caller-supplied `append` sink until a caller-supplied
  future completes, then drain after cleanup with the `supervise` deadline
  rule, and return the read errors. Add one function that folds those errors
  and the drain result into `(failure, diagnostics)`. `run` passes local
  captures, `supervise` passes the shared `Output`, and `Capture` goes back to
  holding bytes and `omitted`.

### Low

#### Rust idioms

- **Hand-built cancellation signal** (`src/cancellation.rs:17-52`):
  `PromptCancellation` builds a latched signal out of a `oneshot` channel, a
  `Mutex<Option<Sender>>`, and a `Shared<BoxFuture>`. `tokio::sync::watch`,
  which the crate already uses in `shell_processes.rs`, provides the same thing
  directly: `Arc<watch::Sender<bool>>`, with `cancel` as `send_replace(true)`,
  `is_cancelled` as `*borrow()`, and `cancelled` as a moved receiver's
  `wait_for(|c| *c)`. Behavior is unchanged, including that outstanding
  `cancelled()` futures complete once every clone is dropped. Checked in a
  temporary worktree: the file went from 52 to 31 lines (10 insertions, 31
  deletions), and `cargo build`, all 172 tests, and clippy with `-D warnings`
  passed with no other changes.

#### Readability

- **A turn start's single update is returned as a list**
  (`src/acp/prompt.rs:400`, `src/acp/prompt.rs:438`): `save_turn_start`
  returns `Option<Vec<SessionUpdate>>`, but the vector always holds exactly one
  `SessionInfoUpdate` (`src/acp/prompt.rs:426-434`), and `start` loops over
  it. Return `Option<SessionUpdate>` and send it directly.

- **`after_run` repeats the hook-run presentation**
  (`src/acp/prompt.rs:699-720`): `run_after_run` rebuilds the same pending,
  in-progress, and finished updates that `run_hook` sends
  (`src/acp/prompt.rs:552-559`, `src/acp/prompt.rs:568-574`), differing only
  in ignoring send errors. Extract one helper that announces a hook run and
  returns its call ID, and use it in both places. `run_after_run` can keep
  discarding the result.

- **Patch arguments are parsed outside the patch module**
  (`src/tools.rs:104-108`, `src/tools.rs:294-300`): every other tool module
  parses its own arguments, but `PatchArgs` and its `"arguments: …"` error live
  in `tools.rs`. Move them into `patch.rs` behind a
  `patch::execute(workspace, arguments) -> Result<String, String>` so that
  `execute_other` dispatches the same way for every tool. Keep patch results
  out of `bounded_result`, which asserts success output fits 16 KiB; a patch
  touching many files can produce a longer summary.

- **Two copies of the "present value" deserializer** (`src/hooks.rs:197-202`,
  `src/tools/shell.rs:33-35`): `deserialize_present_string` and `present` both
  turn a present value into `Some` so that an explicit `null` is rejected. One
  generic `fn present<'de, D, T: Deserialize<'de>>(d: D) -> Result<Option<T>, D::Error>`
  can serve both.

- **The session store reads `HOME` itself** (`src/sessions.rs:1188-1189`):
  `database_path` repeats the `HOME` lookup and error message of
  `settings::home_dir` (`src/settings.rs:36-40`). Call `settings::home_dir`.

- **Repeated option error text** (`src/acp.rs:339-366`): `set_config_option`
  builds the same "`{value}` is not a choice of configuration option
  `{config_id}`" error in three branches. Build it once in a closure.

#### Comments

- **`parse_catalog`'s documentation is attached to the wrong item**
  (`src/openrouter.rs:76-86`): the `///` block that describes the catalog
  filter is followed by `/// About six months.` and then `const RECENT_SECONDS`,
  so rustdoc attaches the whole block to the constant and `parse_catalog` has
  no documentation. Put `RECENT_SECONDS` above the block, with only its own
  line, so the block documents `parse_catalog`.

#### Testing

- **Search keeps stderr it never shows** (`src/tools/search.rs:156-157`,
  `src/tools/search.rs:284-296`, `src/tools/search.rs:449-453`): `drain_errors`
  saves up to 4 KiB of ripgrep's stderr, but the only use is
  `!errors.is_empty()`, because raw diagnostics are deliberately never
  forwarded. The test asserts that exactly 4,096 bytes are kept, which fixes an
  internal detail in place. Have it drain the pipe and return whether anything
  was written, and delete that assertion. The
  `unreadable_paths_are_reported_alongside_the_matches_that_were_found` test
  already covers the observable behavior.

- **Fixture reply builders overlap** (`src/openrouter.rs:966`,
  `src/openrouter.rs:1211`, `src/openrouter.rs:1231`): `shell_reply` (4 uses)
  and `tool_reply` (8 uses) are special cases of `calls_reply` (21 uses),
  differing only in fixed tool names and argument shapes. Replace them with
  `calls_reply` calls, or make them one-line wrappers over it.

- **Skill-loading guarantees tested through session activation**
  (`src/acp.rs:1472`, `src/acp.rs:1502`): `skills_directories_load_in_priority_order`
  and `an_empty_or_foreign_hooks_map_declares_no_hooks` test `skills::load`
  and frontmatter parsing, but build a `ServerState` and create or load
  sessions to get there. That activation reads the catalog is already owned by
  `activation_captures_the_system_prompt_and_skill_catalog_once_per_process`.
  As cases in `src/skills.rs` next to its two existing tests, they need only
  temporary directories.

## Unresolved questions

- `subagent_permission_requests_use_the_main_session_and_scoped_tool_call_ids`
  (`src/acp.rs:2719`) re-asserts the scoped tool call ID and the
  `Subagent: … Working directory:` content prefix, which
  `a_subagent_permission_request_goes_to_the_main_session_with_a_scoped_tool_call`
  (`src/acp/convert.rs:608`) owns as a unit test. The end-to-end test also
  covers routing through `Presentation`, which the unit test cannot. To settle
  whether the repeated content assertions guard anything, check whether any
  failure could break the end-to-end content without also breaking the unit
  test. If none can, cut the end-to-end test to session ID, tool call ID, and
  effects.

## Checks run

- Read all non-test source under `src/`, and `eng/architecture.md`,
  `eng/code-style.md`, `eng/glossary.md`, and `eng/testing.md`.
- Listed the tests in each large module and read the suspected overlapping
  tests.
- Used focused `grep` searches to confirm the duplicated capture strings, the
  single use of `drain_errors`' result, and the fixture helper use counts.
- In a temporary Git worktree at `942f374`, replaced `src/cancellation.rs` with
  the `watch` version and ran `cargo build --all-features` (passed),
  `cargo test --all-targets --all-features` (172 passed, 0 failed), and
  `cargo clippy --all-targets --all-features -- -D warnings` (clean). The
  worktree was then removed.
- This review changed no code in the repository, so full validation was not
  run on `main`.

## Verdict

The codebase is mostly lean; no dead modules or unused public items were
found. The one change worth planning is to merge the two output-capture
implementations in `process.rs` and `shell_processes.rs`, which removes a
parallel implementation in cleanup-sensitive code. The cancellation rewrite
is verified and can be applied as is. The remaining items are small local
cleanups.
