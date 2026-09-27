# Code simplification review

## Scope and coverage

Reviewed the current Rust implementation at `022ff7d`, with a broad production
code survey and close tracing of transcript persistence, replay, compaction,
prompt completion, and hook reporting. The working tree was clean when the
review began. Read the architecture, code style, glossary, testing guidance,
and recent simplification reviews as context.

Selected lenses: architecture, readability, API design, Rust idioms, and Rust
ownership. Findings concern maintenance cost and cognitive load; their severity
indicates simplification priority, not a demonstrated user-facing failure.

Read relevant existing tests for batch validation and persistence, ordered tool
results, cancellation, failed commits, ACP update failures, hook continuation,
after-run reporting, and compaction. This was not an exhaustive test-suite,
security, performance, or live-provider audit. Python examples and build scripts
were outside the code review scope. No implementation changes were made, so
proposed source reductions have not been measured.

## Findings

### Medium

#### Architecture

- **Keep a committed assistant batch together in the transcript.**
  (`src/sessions.rs:64`, `src/sessions.rs:485`, `src/sessions.rs:881`)

  Ox already has the useful unit: `AssistantBatch`, containing one assistant
  message and its ordered results. Every production batch append saves the
  whole unit atomically. However, `append_batch` and `PromptRun::commit`
  immediately split it into separate assistant-message and tool-result entries.
  Several consumers must then reconstruct the relationship:

  - `assistant_batch_end` walks the following entries and collects their
    results before validating the batch (`src/sessions.rs:687`). The transcript
    validator separately rejects orphan results and accumulates completed-batch
    offsets for checkpoint validation (`src/sessions.rs:531`).
  - Replay carries the preceding message's calls across iterations and searches
    them by ID for each result (`src/acp/convert.rs:271`).
  - `run_after_tools` slices the transcript by the number of calls and asserts
    that every entry in that suffix is a result (`src/acp/prompt.rs:502`).
  - Compaction advances over `1 + message.tool_calls.len()` entries to find a
    safe cut (`src/compaction.rs:148`).

  These are multiple implementations of the same structural rule. Changing a
  batch's representation or traversal requires remembering all of them, even
  though no supported operation commits an isolated result.

  **Suggested fix:** replace the two top-level transcript variants with
  `AssistantBatch(AssistantBatch)` and persist that value as one entry. Keep
  message and result validation at construction and database-read boundaries;
  deserialization alone must not establish validity. Replay and hook reporting
  can iterate the batch directly. A checkpoint can check that its covered
  prefix ends after a batch entry, removing the completed-offset collection and
  binary search. OpenRouter encoding expands the batch into the same assistant
  and tool messages it sends today.

  This retires cross-entry result association, batch-length cursor arithmetic,
  and duplicate batch flattening. The replacement is the batch type that
  already exists, plus its serialization and direct iteration at consumers.
  Preserve the separate incomplete batch used during tool execution, including
  its cancellation and failed-update cleanup: that state represents real work
  still in progress.

  This is the largest change in the review. It changes stored entry shape and
  checkpoint indices; follow the repository's database recreation policy and
  update the architecture and glossary with the implementation. Preserve the
  existing tests' atomicity, ordered-result, replay, and interruption guarantees
  while adapting their transcript expectations.

#### API design

- **Carry the final answer in the finished outcome instead of parallel state.**
  (`src/acp/prompt.rs:57`, `src/acp/prompt.rs:102`,
  `src/acp/prompt.rs:151`, `src/acp/prompt.rs:371`)

  A successful answer currently depends on three pieces agreeing:
  `PromptOutcome::Finished`, `PromptRun.answer`, and the returned
  `PromptOutput { stop_reason, answer }`. `run_before_stop` assigns the answer
  before the hooks decide whether the run should continue. That answer remains
  in the run across another model request, and `finish` must take it and filter
  it according to the eventual stop reason (`src/acp/prompt.rs:888`).

  The output type also permits combinations that the documented contract
  forbids, such as `EndTurn` without an answer or cancellation with an answer.
  Consumers rely on the convention differently: `run_after_run` matches the ACP
  stop reason, including an unreachable fallback, while the headless caller
  tests only whether an answer exists (`src/acp/prompt.rs:534`,
  `src/acp.rs:797`). The current paths uphold the contract, but understanding it
  requires tracing assignments and filtering across the whole run.

  **Suggested fix:** carry the answer in `PromptOutcome::Finished(String)` only
  after the before-stop hooks accept it, and replace the public output struct
  with four variants: `Finished(String)`, `Cancelled`, `TokenLimit`, and
  `Refused`. Map those variants to ACP stop reasons at the ACP response boundary;
  the headless caller directly extracts `Finished(answer)`. Hook reporting can
  exhaustively match the same output.

  This removes the run's optional answer field, its initialization and later
  filtering, the invalid output combinations, and the match over unsupported
  ACP endings. It replaces them with payloads on existing outcome types rather
  than adding another state object. Keep the current error distinctions and
  cleanup order, and continue running after-run hooks on the result after
  cleanup. The existing continuation and after-run outcome tests cover the
  behavior that matters here. The principal benefit is less state to track,
  rather than a large line-count reduction.

### Low

#### Readability

- **Remove compaction ranking criteria that cannot change the order.**
  (`src/compaction.rs:32`, `src/compaction.rs:193`,
  `src/compaction.rs:227`)

  Candidates are sorted by
  `(estimate >= original, estimate > cut_target, estimate)`. Both booleans are
  nondecreasing functions of `estimate`, so this tuple has exactly the same
  ordering, including ties, as `estimate` alone. Crossing either threshold can
  never put a larger estimate before a smaller one. The purported 60% preference
  therefore adds no selection behavior.

  A reader must nevertheless understand `Budget.cut_target`, the `original`
  argument to `ranked_cuts`, and the three-part key. The compaction test's
  above-target assertion shows that a reduction is accepted; it does not show
  that the target influences selection (`src/compaction.rs:764`).

  **Suggested fix:** sort candidates by their estimate alone, remove
  `cut_target` and the ranking function's `original` argument, and adjust the
  test wording to describe admission and actual reduction. Keep `original` in
  `compact`, where the check against the actual summary size is necessary.
  Retain the admission filter, stable ordering for ties, candidate retries,
  and the final actual-size check. This preserves the current behavior while
  deleting an ineffective tuning concept. A policy that deliberately preserves
  more recent material would be a separate behavior change.

## Checks run

- Traced the production callers and consumers cited above and inspected the
  relevant existing test bodies. Confirmed that committed batch writes are
  atomic and that no production caller independently appends a tool result.
- Searched all uses of `cut_target`, `ranked_cuts`, `PromptOutput`, the answer
  field, and batch traversal helpers to establish the affected surface.
- Checked the ranking equivalence with a standalone Python assertion over all
  83,521 combinations of two estimates and two thresholds from 0 through 16,
  including
  equal estimates and threshold boundaries. All comparisons agreed. The
  monotonicity argument above establishes the general case.
- Inspected the review diff and ran `git diff --check`: passed.
- Did not run Cargo formatting, tests, build, Clippy, or the example Python test
  suites. Only this review document changed; repository guidance calls for
  focused inspection for documentation-only changes. No refactor has been
  implemented or runtime-validated.

## Verdict

Three supported simplification opportunities; no high-severity findings.
Keeping committed batches together offers the largest reduction in repeated
reasoning. Carrying answers in finished outcomes is a smaller, independent
improvement. Removing the redundant ranking criteria is a narrow cleanup with
an exact behavior-preservation argument. Prioritize these over adding shared
helpers for small repeated expressions.
