# Compaction review

## Scope and coverage

Reviewed the uncommitted compaction feature across `src/compaction.rs`, its
OpenRouter, session-store, ACP, and prompt-run callers, related tests, and the
session-compaction plan. Applied correctness, error-handling, concurrency,
performance, testing, Rust idioms, and documentation lenses. I did not verify
the model context limits against the live OpenRouter catalog or run a live model
request.

## Findings

### Medium

#### Performance

- **Cut ranking scales with the entire saved history**
  (`src/compaction.rs:205`): Once a checkpoint exists, another compaction still
  encodes and serializes every entry before its covered prefix, although none
  can enter a candidate projection. In the same function, each candidate calls
  `complete_turns_after`, which rescans the remaining transcript; a long tool
  loop therefore makes ranking quadratic in its assistant batches. Both costs
  grow during the long sessions this feature is meant to support, before a
  summary request can start or observe cancellation. Start the byte scan at the
  latest covered prefix and compute retained-turn counts in one reverse pass.

### Low

#### Documentation

- **Architecture still rules out the feature it now describes**
  (`eng/architecture.md:276`): The deliberate-constraints section says Ox has
  no context compaction or automatic retry, while the same document now
  describes automatic compaction and a one-time input-overflow retry. Remove
  those two items from that constraint list so the architecture gives one
  consistent account of the implemented behavior.

## Checks run

- `cargo test`: 90 passed after allowing local fixture servers to bind. The
  first sandboxed run failed at the fixture bind with `Operation not permitted`.
- `cargo build`: passed.
- Traced checkpoint validation, projection, input admission, cancellation,
  summarizer completion, overflow retry, and transcript replay in source.

## Follow-up

Both findings were fixed on 2026-09-22. Cut ranking now scans only the entries
after the latest covered prefix and counts completed turns in one pass. The
architecture constraint list now agrees with the compaction and overflow retry
behavior. After the fixes, `cargo fmt --check`, `cargo build`, and all 90 tests
passed.

## Verdict

The compaction paths passed the available tests, and both findings are resolved.
