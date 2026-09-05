# Compare Exact and Anchored Editing

## Sources

- `docs/spec.md#isolation-memory-and-delegated-work` — owns the single edit
  primitive, stale-read, confinement, preview, and text-preservation contract
- `eng/roadmap.md#edit-primitive-comparison` — owns the paired-run design, task
  minimum, recorded measures, adoption threshold, and fallback decision
- `eng/architecture.md#workspace-boundary` and
  `eng/architecture.md#evaluation-boundary` — keep mutation safety in the
  production tool boundary and comparative machinery outside the shipped agent
- `internal/tools/{read,edit,tools}.go` and `internal/tools/tools_test.go` —
  current exact-read/edit behavior, shared executor paths, and safety
  regressions
- `evals/internal/eval/`, `evals/cmd/ox-eval/`, and `evals/tasks/v1/edit/` —
  fresh-workspace runner, objective verification, metrics, and artifact format
- `~/src/references/repos/personal/iota/crates/app/src/tools/{hashlines,read_file,edit_file}.rs`
  — anchored line rendering, anchor validation, atomic range edits, and focused
  tests to adapt without Iota's absolute-path or text-format behavior

## Goal

Measure exact editing against an Iota-style anchored candidate on one fixed,
versioned corpus. Apply the roadmap threshold once, retain the evidence, and
leave Ox with only the winning production edit primitive.

## Implementation

- `evals/tasks/edit-v1/` — add at least 30 independently verifiable tasks with
  tool-neutral prompts and matched per-task budgets. Cover repeated text, stale
  reads, Unicode, LF/CRLF/BOM and trailing-newline preservation, insert/delete
  operations, disjoint edits, and coordinated multi-file work including
  failures. Add a runner-owned fixture-mutation phase for deterministic stale
  reads between prompts; mutation fixtures remain outside the agent workspace
  until that phase.
- `evals/internal/eval/` and `evals/cmd/ox-eval/` — label each candidate and
  record the evaluated binary digest. Accumulate usage across all task phases,
  distinguish provider retries from failed `edit_file` attempts, and retain task
  failure categories and latency. Extend task validation and execution for the
  fixture-mutation phase without giving Ox access to evaluator-owned files.
- Add a small edit-comparison report path under `evals/` that consumes the two
  artifact trees and refuses incomplete or unmatched evidence. Require the same
  model, task revisions, prompts, budgets, three repetitions per candidate, and
  at least 30 tasks. Report each run plus aggregate success, median total
  tokens, latency, edit retries, and failure categories. Select anchors only
  when their absolute success rate is at least five percentage points higher,
  median total tokens increase by at most ten percent, usage is complete, and
  the anchored safety tests all pass; otherwise select exact editing.
- `internal/tools/` — implement the anchored candidate behind a temporary
  evaluation build tag while keeping the current exact binary unchanged. Prefix
  read windows with deterministic absolute line/hash anchors and accept atomic
  nonoverlapping line-range replacements and insertions. Reuse Ox's existing
  confined path resolution, read evidence, local/client executors, permission
  projection, atomic replacement, mode preservation, and mutation reporting;
  adapt line handling to preserve UTF-8 BOM, line endings, and trailing-newline
  state rather than copying Iota's assumptions.
- Run both binaries against every corpus task three times with
  `openai/gpt-5.6-luna`, identical prompts and request budgets, and fresh
  workspaces. Store a sanitized machine-readable comparison under
  `evals/results/edit-v1/` containing candidate and binary identities, task
  revisions, per-run metrics, aggregate calculations, safety-gate status, and
  the resulting decision; do not store credentials or authorization headers.
- After recording the decision, remove the temporary build tag and losing
  implementation. If anchors win, make anchored reads and edits the sole
  production contract and update `docs/spec.md` and the workspace boundary in
  `eng/architecture.md`; if not, leave those sources and the exact tool
  unchanged. Update affected fake-model fixtures only for the selected schema.

## Tests

- Validate every comparison task, fixture mutation, objective verifier, and
  corpus-category/minimum-count requirement without contacting a provider.
- Test candidate identity and binary digests, multi-phase usage accumulation,
  edit-attempt metrics, matched-pair rejection, median and threshold boundary
  calculations, missing usage, incomplete repetitions, and deterministic
  fallback to exact editing.
- Port the anchored parser, hash, stale-anchor, range ordering, insertion,
  overlap, and atomic-failure tests. Run the existing confinement, evidence,
  delegated filesystem, permission-preview, mode, encoding, newline, and file
  mode tests against the candidate before it is eligible to win.
- Exercise a small fake-provider comparison through two supplied binaries to
  prove fresh workspaces, separate artifacts, metric extraction, and report
  generation before the live matrix.

## Decisions

- Compare two compile-time binaries instead of adding a runtime setting or
  hidden process switch. The temporary candidate cannot alter normal Ox runs,
  and the losing implementation is removed after the evidence is recorded.
- Count an edit retry as a failed `edit_file` completion, separately from HTTP
  provider retries. This measures model recovery from the primitive while
  preserving the existing transport metric.

## Extra validation

- Run the complete 30-task, three-repetition, two-candidate live matrix with the
  repository-authorized credential path and retain the generated comparison
  artifact before selecting the production implementation.
