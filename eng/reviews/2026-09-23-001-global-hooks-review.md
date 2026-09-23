# Global hooks review

## Scope and coverage

Reviewed the uncommitted global hooks change on `rust` against the approved
plan, `eng/plans/2026-09-23-001-global-hooks.md`: the new `src/settings.rs`,
the moved hook definitions and `RunHooks` in `src/hooks.rs`, startup loading in
`src/acp.rs`, the per-point hook loops in `src/acp/prompt.rs`, optional
attribution and feedback placement in `src/sessions.rs`, label call sites in
`src/acp/convert.rs`, `src/openrouter.rs`, and `src/compaction.rs`, the
changed tests, and the documentation updates in `AGENTS.md`, `eng/`, and both
example READMEs. Compaction cut selection and projection were read to confirm
that adjacent feedback entries stay together.

Lenses: correctness, error-handling, security, testing, readability, and
documentation. Performance, concurrency, and dependencies were considered and
have no findings: the change adds no shared state, tasks, or crates, and the
per-point definition clones are small next to a process launch.

## Findings

No high or medium findings.

### Low

#### Documentation

- **Singular hook wording left in prompt-run comments**
  (`src/hooks.rs:92`, `src/acp/prompt.rs:30`, `src/acp/prompt.rs:306`,
  `src/acp/prompt.rs:471`, `src/acp/prompt.rs:501`,
  `src/acp/prompt.rs:530`): `Context` is still described as "the input fields
  every hook of one prompt run shares", but `skill` and `arguments` now differ
  between the global and skill definitions in the same run
  (`hook_context`, `src/acp/prompt.rs:436`). `MAX_HOOK_CONTINUATIONS` still
  says it limits `continue` decisions "from its hook"; it now limits model
  requests, and with two continuing hooks a run accepts 100 decisions (the test
  at `src/acp/prompt.rs:1513` asserts `2 * MAX_HOOK_CONTINUATIONS` feedback
  entries). The `start`, `run_before_tool`, `run_after_tools`, and
  `run_after_run` comments each describe one hook. Fix: describe `Context` as
  the input fields of one hook definition in a prompt run, describe the
  constant as the most hook continuations (model requests) one run accepts,
  and make the method comments plural.

- **Markdown tables and wrapping not reformatted**
  (`examples/skills/careful/README.md:111`,
  `examples/skills/goal/README.md:81`, `AGENTS.md:23`, `AGENTS.md:55`,
  `eng/glossary.md:123`): the edited `before_run` row of the careful protocol
  table and the `stop` row of the goal decision table no longer align with
  their columns, and several edited bullets wrap after a few words. The goal
  `stop` row also says the run ends "if the global hook also permits it",
  which leaves the meaning of "permits" unstated. Fix: rewrap and realign the
  edited lines, and state the rule directly, for example "the run ends unless
  a global `before_stop` hook requests continuation". The `AGENTS.md` entry for
  `examples/skills/careful/` could also mention that its README now documents
  global hooks.

#### Readability

- **Unformatted definition builder in the lifecycle test**
  (`src/acp/prompt.rs:1646`): the `definitions` expression puts
  `].into_iter().flatten().enumerate().map(...)`, `skill, directory,`, and a
  150-column `after_tools` command on single lines. `cargo fmt --check` passes
  because rustfmt skips an expression it cannot fit within the line width, so
  this block will stay unformatted. Fix: bind the long `after_tools` command
  and the two conditional commands to local variables before building
  `definitions`, so rustfmt can lay out the chain.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: 95 passed, 0 failed.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: 1 test
  passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: 4 tests
  passed.
- Traced both-hook decision handling: `before_tool` collects every denial,
  `before_stop` continues when either definition continues and checks the
  limit before saving each continuation, and `after_run` attempts every
  definition regardless of earlier failures.
- Traced `validate_transcript` placement against the preceding entry of a
  different kind, and compaction `candidates`, which cut only at batch ends,
  so adjacent feedback entries are never split.
- Confirmed that no test reads the developer's real settings: `ServerState`
  tests pass `None`, the headless child sets `HOME` to its workspace and
  removes `OX_IN_HOOK`, and the settings child sets `OX_IN_HOOK` before
  `HOME` would be read.

## Verdict

The implementation matches the approved design. Decision semantics, feedback
placement, attribution, nested suppression, and startup loading are correct,
and the tests cover global-only, skill-only, and combined runs with each side
deciding. The remaining work is comment and Markdown cleanup and formatting of
one test expression.
