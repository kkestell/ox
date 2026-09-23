# ACP usage reporting review

## Scope and coverage

Reviewed the current working tree changes to ACP usage reporting, including the
OpenRouter request and stream path, compaction, transcript storage, ACP updates,
tests, and associated plan and documentation. Lenses: correctness, API design,
error handling, testing, performance, documentation, and Rust idioms. The
reviewer made no live OpenRouter request.

## Findings

No open findings. The earlier claim that usage reporting would fail without an
opt-in was too strong: a live Ox session showed context and cost with the old
request bodies. [OpenRouter's support guidance](https://openrouter.ai/support/)
still recommends `"usage": {"include": true}`. Both streamed request bodies
now send it, and existing request tests assert it.

## Checks run

- `cargo fmt --all -- --check` — passed.
- `cargo test --all-targets --all-features` — passed, 95 tests.
- `cargo build --all-features` — passed.
- `cargo clippy --all-targets --all-features -- -D warnings` — passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts` — passed, 1 test.
- `python3 -m unittest discover -s examples/skills/careful/scripts` — passed, 4 tests.
- `git diff --check` — passed.
- Follow-up test delta: no new test functions and two net test lines added to
  existing request assertions.

## Verdict

The documented usage opt-in is present in ordinary and summarizer requests.
