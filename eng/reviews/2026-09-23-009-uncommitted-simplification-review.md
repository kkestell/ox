# Uncommitted simplification review

## Scope and coverage

Reviewed all uncommitted Rust changes against `145092e` in `src/acp.rs`,
`src/acp/convert.rs`, `src/acp/prompt.rs`, `src/compaction.rs`,
`src/openrouter.rs`, and `src/sessions.rs`, including their changed tests and
the callers of the changed interfaces. Read the accompanying documentation
changes, implementation plan, and earlier abstraction review as context.

Lenses: readability, architecture, API design, correctness, testing, Rust
ownership, and Rust idioms. The emphasis was whether the changes reduce the
amount a reader must track while preserving behavior. This was not a review
of unrelated code or a live OpenRouter integration test.

## Findings

No confirmed actionable findings.

## Simplification assessment

Yes. The strongest improvements remove repeated reasoning, rather than merely
shortening functions:

- **Request parameters carry the resolved model.**
  `ModelRequestParameters::new` (`src/openrouter.rs:262`) replaces validation
  that discarded its result. Request encoding, estimates, budgets, and usage
  updates now use the catalog model directly. Their callers no longer need
  to interpret or propagate impossible unknown-model errors. Grouping the
  model, effort, and system prompt also makes the prompt loop easier to read.
  Summarizer functions take only the catalog model they actually need.
- **One set of entries is checked, saved, and retained.**
  `PromptRun::save_turn_start` (`src/acp/prompt.rs:252`) uses the same
  prospective transcript for admission and the eventual in-memory state,
  saving its new suffix between those steps. Removing
  `SessionSettingsChange` eliminates the separate translations of settings
  into entries. The transcript still advances only after persistence succeeds.
  Admission failure consumes the pending settings entries, but the caller
  discards that run on failure; it never retries that partially consumed run.
- **Persistence rules have fewer owners.**
  `encode_entry` (`src/sessions.rs:1060`) owns the variant-to-row encoding;
  `append` and `write_entries` own the shared transaction and write sequence.
  `read_transcript` removes the repeated query and decoding code. Checkpoint
  validation remains inside its transaction, and assistant batches retain
  their typed boundary and ordered, atomic persistence.
- **Skill messages and turn starts have shared definitions.**
  `skill_invocation_message` (`src/openrouter.rs:446`) supplies the same text
  and image ordering to request encoding and compaction material. The shared
  turn-start predicate and search remove repeated definitions without adding
  a new state representation. Existing request tests and the expanded
  compaction assertions cover the image conversion.

There are modest tradeoffs. The store path now constructs owned entries and
reads a session summary even when its caller discards that summary. The skill
conversion copies image data, including when compaction ultimately uses only
the MIME marker. These costs are real, but this review found no evidence of a
material performance problem; introducing borrowed parallel representations
would undermine the intended simplicity.

`set_config_option` (`src/acp.rs:300`) also constructs and discards request
parameters with an empty system prompt just to validate settings. That is a
less natural use of the new type. It is a small local awkwardness, not enough
to outweigh the simpler request path or justify another abstraction now.

The public parameter fields and slice-based turn append still rely on trusted
internal callers. They are not complete type-level proofs of valid settings
or transcript ordering. The actual callers preserve those contracts, and the
plan explicitly chose these interfaces.

Across the six changed Rust files, production lines decrease from 4,913 to
4,863, and test-module lines decrease from 5,705 to 5,634. The 56 test functions
in those files are unchanged in count; the full suite still has 96 tests.
Counts include comments and blank lines, splitting at each test module.
The source reduction is 121 lines overall, including 50 production lines,
rather than the roughly 150 lines estimated by the earlier review.

## Checks run

- Inspected the complete diff, changed test bodies, request and compaction
  callers, settings selection, transcript validation, and persistence order.
- Compared production and test-module line counts with `HEAD`; confirmed no
  test functions were added or removed.
- `git diff --check`: passed.
- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed, 96 tests.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed,
  1 test.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed,
  4 tests.

All repository-required validation passed. No live provider requests or
performance benchmarks were run. No implementation code was changed during
this review.

## Verdict

Keep these changes. They reduce cognitive load by removing repeated model
resolution, settings-to-entry conversion, persistence plumbing, and skill
message construction. The request and prompt paths improve most; the store
changes offer a smaller but still worthwhile maintenance benefit. No fixes
are required by this review.
