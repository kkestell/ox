# Subagents review

## Scope and coverage

Reviewed commit `ac277156e781d4f4b0c7155e4f540e823c050bef` against its parent:
subagent ownership and coordination tools, the shared prompt loop, child
sessions, message projection and compaction, ACP permissions and usage,
associated tests, and documentation. Traced the existing cancellation, hook,
tool execution, and session-operation paths that the feature uses.

Selected lenses: correctness, concurrency, resources, error handling, security,
testing, Rust ownership, Rust idioms, and documentation.

No live OpenRouter or interactive ACP client testing was performed. The
cancellation reproduction below isolates a ready tool batch; the separate
mock HTTP test did not reproduce the race. No application code or committed
tests were changed.

## Findings

### Medium

#### Concurrency

- **Forward parent cancellation before polling the child turn**
  (`src/subagents.rs:442`): The biased selection polls `turn` before checking
  the main prompt's cancellation. Until the cancellation branch wins, the
  child's separate signal remains unset. If the child is scheduled with work
  ready before the main task reaches owner shutdown, it can start tools after
  the parent cancellation has already been latched. The checks in
  `src/acp/prompt.rs:968` and `src/acp/prompt.rs:983` inspect only the child's
  signal, so they do not prevent this.

  A controlled test in a temporary checkout passed a validated patch batch
  through the production `AgentTurn::process_batch` executor as the ready
  child future, cancelled the parent, and then polled `run_turns`. The patch
  created `cancelled-write`, failing the assertion that cancellation prevents
  new tool effects. Moving the parent-cancellation branch before `turn` made
  the same test pass. Poll cancellation first, forward it to the child, and
  retain the existing await that lets the interrupted turn save and clean up.
  Add a regression covering cancellation with child work already ready.

### Low

#### Documentation

- **Keep the manual follow-up scenario within one user prompt**
  (`eng/testing.md:44`): The instructions ask the user to receive a subagent's
  question in one turn and answer that same subagent in a second turn. Once
  the first prompt returns, `AgentTurn` awaits `Subagents::shutdown`, which
  removes every subagent. The next prompt creates a new owner, so
  `send_message` cannot resolve the previous subagent's ID. The documented
  procedure cannot verify retained follow-up conversations as written.

  Give Ox both file choices and the intended answer in one initial prompt,
  and ask it to wait for the child's question, send the answer, and wait for
  the result before finishing. Keep the cancellation scenario as a separate
  user prompt.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: 172 tests passed.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: 1 test passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: 4 tests
  passed.
- `git diff --check HEAD^ HEAD`: passed.
- Temporary reproduction workspace:
  `/private/tmp/ox-review-subagents-l7g4cn86`. Ran
  `cargo test --offline --manifest-path <workspace>/Cargo.toml --target-dir
  /Users/kyle/projects/ox/target review_parent_cancellation_precedes --
  --nocapture`. The ready-batch regression failed on the reviewed ordering;
  the buffered HTTP response case passed. Both passed after reversing the
  two selection branches in the temporary copy. That experimental change
  was then reverted. Initial harness attempts needed a request-driving
  correction and a type correction; a loopback bind denied by the sandbox
  passed when rerun outside it.
- Inspected the review document and final working-tree diff. Only this review
  document was added; production and test source files remain unchanged.

## Verdict

Fix the cancellation ordering and correct the manual follow-up procedure.
The existing full validation suite passes, but it does not catch the ready
child-work cancellation case.
