# Hook removal review

## Scope and coverage

Reviewed commit `480fd74` (`Remove hooks support`) against its parent. Checked
hook execution removal across prompt turns, ACP dispatch, settings, skills,
transcripts, compaction, process execution, subagents, tests, and current
architecture documentation. Used correctness, testing, resource, and
documentation lenses. No material code path was excluded; no live ACP session
was run.

## Findings

No confirmed findings. Older review documents and research reports retain
historical references.

## Checks run

- `rg` for hook names and hook protocol symbols across active source,
  documentation, and examples: no implementation or active documentation
  references remain; only the intentional settings rejection tests remain.
- `git diff --check 480fd74^ 480fd74`: passed.
- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed (157 tests).
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- Follow-up cleanup removed the two obsolete hook-specific settings assertions;
  the generic unknown-field cases continue to cover that validation.

## Verdict

The removal is clean and complete in the active code and documentation. The
removed hook-specific transcript and settings behavior is no longer wired into
prompt execution, and the tests and example hook skills were removed with it.
