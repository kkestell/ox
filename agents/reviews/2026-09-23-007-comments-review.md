# Codebase comments review

## Scope and coverage

Reviewed the current working tree using the `comments` lens across all Rust
modules in `src/`, including test comments and fixtures; `scripts/run.py`; the
Python scripts and tests in both example skills; `Makefile`; and `Cargo.toml`.
Checked comments against their surrounding implementation and traced relevant
callers, hook decisions, process cleanup, transcript storage, compaction, and
tool execution.

Repository guidance supplied context. Standalone documentation, prompt text,
generated files, dependencies, and other review lenses were outside this
review. No live OpenRouter or ACP editor integration was exercised. Existing
changes to the review skill and its comments lens were left untouched.

## Findings

### Low

#### Comments

- **A hook's stop decision does not necessarily end the run**
  (`src/sessions.rs:181`): The `StopDecision` comment says "`Stop` ends the
  run." With global and skill hooks, one hook can return `Stop` while the other
  returns `Continue`; the run continues. `run_before_stop`
  (`src/acp/prompt.rs:370`) runs both hooks and retains any continuation
  decision. Its lifecycle test covers both orders of this disagreement. The
  comment could lead a maintainer to stop processing hooks at the first
  `Stop`, bypassing another hook's request for more work. Describe these as
  individual hook decisions and state that the run ends only when no hook
  requests continuation.

- **The careful example overstates the effect of hook failures**
  (`examples/skills/careful/scripts/careful.py:6`): The module docstring says
  a nonzero exit ends the prompt run, but the script also implements
  `after_run`. That hook runs after the result is determined; its failure is
  reported without changing the result or preventing the next hook from
  running (`src/acp/prompt.rs:537`). For example, failure to append
  `runs.jsonl` does not turn a successful run into a failure. Someone adapting
  this example could incorrectly rely on that hook to reject a result.
  Qualify the docstring: earlier hook failures end the run, while `after_run`
  failures only report an error.

- **The catalog parser's explanation is attached to its time constant**
  (`src/openrouter.rs:75`): The block beginning "Parses OpenRouter's
  `GET /models` response" documents `RECENT_SECONDS`, because the constant
  immediately follows it. `parse_catalog` at line 85 has no attached
  explanation. Editor documentation for the parser therefore omits its
  filtering rules, while the constant's documentation describes a parsing
  operation and arguments it does not have. Move the parser explanation
  directly above `parse_catalog`, leaving "About six months" above the
  constant.

## Checks run

- Inventoried source files and comments with `rg`, then inspected their
  implementation, callers, and relevant test assertions.
- `cargo test --all-features
  acp::prompt::tests::lifecycle_hooks_run_at_their_points_and_their_feedback_reaches_the_model
  -- --exact`: passed, one test.
- `cargo test --all-features
  acp::prompt::tests::hook_errors_keep_saved_work_and_after_run_reports_every_outcome
  -- --exact`: passed, one test.
- `git diff --check` and inspection of the new report: passed.
- Full formatting, test, build, Clippy, and example-test validation was not
  run. This change adds only a review document; the focused tests above
  confirmed the two behavioral claims underlying the findings.

## Verdict

Three low-severity comment corrections; no high- or medium-severity findings
within this lens. Correct the two hook explanations and relocate the parser
comment. No implementation changes were made.
