# Review batch ownership and turn settings

## Scope and coverage

Reviewed all uncommitted changes in `src/acp.rs`, `src/acp/prompt.rs`,
`src/acp/convert.rs`, `src/sessions.rs`, `src/openrouter.rs`,
`src/compaction.rs`, and `src/hooks.rs`, including the complete test diff.
Checked the associated architecture, glossary, testing guidance, and source
map. Read the batch-and-turn simplification plan and earlier simplification
review as context for the intended design.

Selected lenses: correctness, error handling, concurrency, resources, API
design, testing, architecture, documentation, Rust ownership, and Rust idioms.

Traced turn admission and settings capture through persistence and reload;
batch execution through cancellation, permission failures, hook failures,
failed updates, and failed commits; and call/outcome pairing through replay,
hook reports, model requests, and compaction. Checked summary projection,
request estimates, checkpoint boundaries, and repeated skill invocations.
The retired result-identity and settings-block representations have no
remaining source references.

Validation used local HTTP and ACP fixtures, temporary databases, and real
local subprocesses. No live OpenRouter request or interactive ACP client was
tested. Existing database compatibility is intentionally outside the design;
the repository requires database recreation for these stored-shape changes.

## Findings

No confirmed findings.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed, 122 tests.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed,
  1 test.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed,
  4 tests.
- Focused searches for retired types, transcript variants, and staging fields:
  no remaining source references.
- `git diff --check`: passed.

Test ownership changes were reviewed:

- Added coverage for an update failure while announcing pending calls, alongside
  the existing failure after an observed outcome. Both cases check persistence
  and the absence of subsequent updates.
- Replaced settings-block and duplicated-result-identity cases with turn-start
  decoding, ordered outcome counts, and latest-turn settings checks, including
  returning to Default effort and Ask mode. The removed cases concern states
  the new representation cannot express.
- Moved latest-summary projection coverage from OpenRouter into compaction,
  and added a comparison between ranking byte counts and projected requests.
- Split existing activation, permission, hook lifecycle, hook failure, image,
  and transcript-validation checks into tests named for their guarantees.
  The relevant execution, persistence, replay, and request assertions remain;
  no loss of a required external behavior guarantee was identified.

All required validation commands passed; none were skipped. Only this review
document was added during the review.

## Verdict

No changes are requested. The implementation follows the intended ownership
and storage redesign, and the required validation passes.
