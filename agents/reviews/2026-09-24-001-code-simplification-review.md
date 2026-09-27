# Simplify batch ownership and turn settings

## Scope and coverage

Reviewed the current Rust implementation at `c4c8d64`, surveying production
code throughout `src/` and closely tracing prompt execution, transcript
persistence, settings, replay, hook reporting, and model request encoding.
The working tree was clean at the start. Read the architecture, code style,
glossary, testing guidance, and previous simplification work as context.

Selected lenses: architecture, readability, API design, Rust ownership, and
Rust idioms. Severity expresses the priority of reducing maintenance cost;
these findings do not claim demonstrated user-facing failures.

Inspected relevant existing tests for ordered tool outcomes, malformed stored
batches, database reopen, interrupted execution, failed updates and commits,
hook errors, settings restoration, and changes made during a turn. This was
not an exhaustive test-suite, security, performance, or live-provider audit.
Python scripts and build tooling were outside the review scope. No code changes
were made; proposed net source reductions have not been measured.

## Findings

### Medium

#### Architecture

- **Store effort and mode with the turn they govern.**
  (`src/sessions.rs:63`, `src/sessions.rs:584`,
  `src/sessions.rs:716`, `src/acp/prompt.rs:214`)

  Effort and mode only take effect when a user message or skill invocation
  starts a turn, and the store already commits them together. Their separate
  transcript entries nevertheless introduce a grammar that every reader must
  understand: an optional block, either ordering of its two settings, no
  duplicates, and a required following turn start. `settings_block_end` parses
  that grammar using a second cursor and two flags. `saved_settings` folds the
  whole transcript to recover the last values. Prompt startup computes changes
  against the previous settings, stages them in `settings_entries`, and later
  splices that block into the prospective transcript.

  This is additional reasoning for a compact storage representation, without
  an independently supported operation that appends or consumes a settings
  change. Replay and model encoding skip those entries. The existing settings
  tests include cases solely for the two orderings, duplicate entries, and
  orphaned blocks (`src/sessions.rs:1614`). Adding another per-turn setting
  would extend all of this machinery.

  **Suggested fix:** keep the single initial model entry, but introduce one
  turn-start entry containing the captured effort, mode, and either a user
  message or a skill invocation. Store both scalar settings on every turn.
  Recover the session model from its initial entry and current effort and mode
  from the latest turn start; only an empty session needs default settings.
  Prompt startup still resolves saved versus selected settings before
  constructing that entry.

  This replaces the standalone `Effort` and `Mode` entries, their change
  detection, the staged settings vector, and `settings_block_end` with a small
  turn-start structure and an input enum. Consumers must handle the input
  inside that structure, so this is a storage redesign rather than a local
  cleanup. Repeating two scalar settings per turn is the tradeoff. A turn's
  settings become readable directly from the same entry as its input.

  Preserve synchronous capture before the prompt future starts, admission
  before persistence, atomic storage of input and settings, the fixed model,
  and selection changes applying only to the next turn. Retain the guarantees in
  `configuration_selections_validate_and_restore_saved_values` and
  `setting_changes_apply_to_the_next_turn_while_the_system_prompt_stays_captured`
  (`src/acp.rs:1447`, `src/acp.rs:1615`). Adapt their stored-shape assertions and
  the existing malformed-transcript tests instead of keeping tests for the
  retired settings-block grammar. Checkpoint indices change with the entry
  layout; recreate the database and update the architecture, glossary, and
  source map when implementing it.

#### API design

- **Derive result identities from the calls already in the batch.**
  (`src/sessions.rs:457`, `src/sessions.rs:484`,
  `src/sessions.rs:502`, `src/acp/prompt.rs:178`)

  Keeping committed batches together has made `ToolResult.call_id` and
  `ToolResult.name` redundant. A batch already contains the calls, and results
  must have the same length and order. Production execution walks those calls
  sequentially and copies their identities into each result; interruption
  cleanup does the same for the remaining suffix. There is no independently
  arriving result to match by ID.

  The duplicate representation still permits mismatched names, duplicate result
  IDs, and reordered IDs, so `pair_results` must reject them. Consumers then
  choose between copies of the same identity: OpenRouter encoding reads the
  result's ID (`src/openrouter.rs:544`), while replay and hook reporting zip
  calls with results and use the call's identity (`src/acp/convert.rs:304`,
  `src/hooks.rs:153`). Readers must know that validation makes these choices
  equivalent.

  **Suggested fix:** store an ordered `Vec<ToolOutcome>` beside the assistant
  message. The outcome at index `i` belongs to call `i`. Keep validation of the
  assistant message and require exactly one outcome per call, both at append
  and database-read boundaries. Pass a call and its outcome to ACP conversion
  and hook reporting; zip the same values for OpenRouter encoding and
  compaction material. An incomplete batch holds only the observed outcome
  prefix until execution or cleanup fills it.

  This retires `ToolResult`, `tool_result`, the copied identity fields, and the
  identity-comparison branches of `pair_results`. It replaces them with a
  length check and direct pairing at projections. Keep this ordering contract
  explicit: do not introduce a result map or permit arbitrary completion order.
  Preserve raw call arguments, unique nonempty call IDs, outcome status and
  text, and identical external tool IDs. The change affects the stored shape;
  use database recreation rather than compatibility code.

  Existing tests should still reject missing or extra outcomes and invalid
  calls, and verify that distinct outcomes retain the correct call IDs in
  requests, replay, hooks, and interrupted runs. Remove malformed-result cases
  that only concern identity fields that no longer exist. In particular,
  preserve the ordered external-message assertions in
  `several_tool_calls_in_one_message_get_ordered_results`
  (`src/acp/prompt.rs:2613`) and the database-reopen coverage
  (`src/sessions.rs:1209`).

#### Rust ownership

- **Contain an uncommitted batch within the operation that completes it.**
  (`src/acp/prompt.rs:141`, `src/acp/prompt.rs:332`,
  `src/acp/prompt.rs:731`, `src/acp/prompt.rs:802`,
  `src/acp/prompt.rs:816`)

  `uncommitted_batch` is state on the whole prompt run, although only one model
  step creates and works on it. `run_model_step` installs it, `execute` assumes
  it exists, `commit` clones it and clears it, and the outer `finish` inspects
  it after the model loop has returned. Understanding any error path requires
  tracking whether it happened before installation, during tool execution,
  during commit, or after clearing the field. The `expect` calls and
  `unreachable` outcome combinations encode that nonlocal lifecycle.

  The cleanup itself is necessary: completed effects must survive a failed ACP
  update, and every unstarted call needs a final outcome. Its location on the
  prompt run is not necessary. Model requests finish before a batch is created;
  usage reporting and after-tools/before-stop hooks happen after the batch has
  committed. No later model step needs the incomplete batch.

  **Suggested fix:** make one batch-processing function own the local
  `UncommittedAssistantBatch`. Have its execution phase receive a mutable batch
  explicitly and return a result to that owner. The owner handles interruption,
  fills the unstarted suffix, attempts persistence, and sends any remaining
  updates before returning. On success it commits before the caller reports
  usage or invokes the following hooks. Keep this explicit asynchronous control
  flow; database writes and ACP updates do not belong in `Drop`.

  This removes the optional batch field from `PromptRun`, its initialization
  and reset, and the cross-method presence assumptions. `commit` can accept an
  owned completed batch, and the outer `finish` only converts the final outcome.
  The incomplete batch type and its cleanup policy remain: moving the code is
  useful only if its owner enforces that every exit passes through completion.

  Preserve the current cleanup order and error precedence, and do not retry a
  failed store append. Persist before extending the in-memory transcript, and
  keep observed outcomes before their ACP updates. Run after-run hooks only
  after this cleanup returns, while the operation guard remains held. Existing
  focused coverage includes cancellation
  during tools (`src/acp/prompt.rs:2659`), failed appends (`:2740`), failed
  updates after an observed result (`:2763`), and before-tool versus after-tools
  errors (`:1982`). These cases distinguish a safe ownership change from merely
  moving early returns into another helper.

## Checks run

- Surveyed production Rust modules and traced the callers and projections
  named above. Searched all `ToolResult`, `pair_results`, `uncommitted_batch`,
  and `settings_entries` uses to establish their ownership and affected surface.
- Inspected the cited existing test bodies. Confirmed that tool execution and
  cleanup construct results in call order, and that production settings writes
  happen with a turn start. Tests were read, not executed.
- Inspected the new review diff and checked it with
  `git diff --no-index --check /dev/null <review-file>`: no whitespace errors
  (exit 1 reports the new-file difference).
- Did not run Cargo formatting, tests, build, Clippy, or the example Python
  suites. Only this review document changed; repository guidance calls for
  focused inspection for documentation-only changes. No proposed refactor has
  been implemented or runtime-validated.

## Verdict

Three supported simplification opportunities; no high-severity findings.
Start by making batch completion local, then remove duplicated result
identities. Attaching settings to turn starts is the broader storage change.
Each recommendation removes state or a representation rule that readers
currently have to reconstruct. Measure the complete production and test diff
when implementing each; adding wrappers without retiring the old paths would
not deliver the intended simplification.
