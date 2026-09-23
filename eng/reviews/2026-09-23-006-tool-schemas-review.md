# Tool schema implementation review

## Scope and coverage

Reviewed the working-tree implementation of
`eng/plans/2026-09-23-008-tool-schemas.md`: `src/tools.rs`, the four tool
modules, their callers and tests, and the associated `AGENTS.md` and
`eng/todo.md` updates. Applied correctness, API design, testing, readability,
architecture, documentation, performance, and Rust idioms lenses. No live
OpenRouter request was made; the existing request test uses a local server.

## Findings

No confirmed findings. The five JSON tool definitions remain identical and in
the same order. The patch description is unchanged. `tools::schemas()` retains
its signature, and `ordinary_body` still sends its result.

## Checks run

- Compared each old and moved `json!` definition after removing formatting
  whitespace outside string values: all five matched. Compared the patch
  description byte for byte: matched.
- `git diff --check`: passed.
- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: passed, 96 tests.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed.

## Verdict

The implementation matches the plan and is ready to proceed.
