# Review workspace settings

## Scope and coverage

Reviewed the workspace-settings feature: commit `42fa91a` (`.ox/settings.json`
workspace override, shared settings reader, default model moved out of the
model catalog, prompt runs always receiving the session selections) plus the
uncommitted refinements to `AGENTS.md`, `README.md`, `eng/architecture.md`,
`eng/glossary.md`, and the `parses_run_commands` test in `src/main.rs`.
Checked against the approved design in
`eng/plans/2026-09-24-005-workspace-settings.md`.

Selected lenses: correctness, error handling, security, testing,
documentation, and architecture.

Traced settings loading through startup ordering, `for_workspace` replacement,
`hooks` rejection, and catalog validation; activation through `new_session`
and `load_session` fallback behavior; headless `ox run` resolution; prompt and
`/compact` selection plumbing; and the updated tests. Confirmed ordering
(settings read before session creation, so a bad workspace file creates
nothing) via the new activation test.

Gaps: no live OpenRouter fetch or interactive ACP client was exercised;
review relied on the fixture catalog, unit tests, and local subprocesses.
Existing-database compatibility is intentionally out of scope per repository
policy.

## Findings

No confirmed findings.

The `hooks`-in-workspace rejection, catalog-first startup order, missing-file
passthrough, and empty-transcript-only use of the default model all match the
plan. `null` hooks in a workspace file are treated as absent while any
present hooks object (even empty) is rejected, which is consistent with
"may set every key except `hooks`". Directory-in-place and oversized,
non-UTF-8, unknown-key, and off-catalog-model failures all propagate with the
file path. Documentation in `README.md`, `eng/architecture.md`,
`eng/glossary.md`, and `AGENTS.md` agrees with the implemented behavior.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed, 150 tests.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed,
  1 test.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed,
  4 tests.
- `git diff --check`: passed.

## Verdict

No changes are requested. The implementation follows the approved workspace
settings design, the security-relevant `hooks` restriction is enforced with a
path-naming error, and all validation passes.
