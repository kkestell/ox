# Review of the uncommitted changes: changing the model between turns

## Scope and coverage

This review covers every uncommitted change in the working tree against `HEAD`:

- The implementation of
  [the plan to change the model between turns](../plans/2026-09-24-006-change-model-between-turns.md)
  in `src/sessions.rs`, `src/acp/prompt.rs`, `src/acp.rs`, `src/compaction.rs`,
  `src/openrouter.rs`, `src/acp/convert.rs`, and their tests.
- The workspace settings follow-up: the `ox run` test in `src/main.rs`.
- The documentation changes in `AGENTS.md`, `README.md`, `eng/architecture.md`,
  and `eng/glossary.md`.

Lenses: correctness, api-design, error-handling, testing, architecture,
documentation, comments, and rust-idioms. The review traced each caller of
`append_turn_start`, `saved_settings`, `config_options`, `PromptInput`, and
`compaction::has_images`. It also traced the paths that decide which model a
request uses: new session, load, configuration changes, prompt runs, automatic
compaction, `/compact`, and the headless entry point.

The review did not live-test an ACP client that switches models, because
headless runs cannot change the model partway through a session. The live
OpenRouter checks recorded in the plan cover sending continuation metadata to
a different model, and a model without image input rejecting an earlier image.

## Findings

No finding affects behavior. The model now follows the same path as the effort
level and session mode:

- The prompt run's settings snapshot supplies the model for every model request
  in the turn, including automatic compaction.
- The turn start saves the model.
- Load restores the model from the latest turn start and rejects it when it is
  outside the model catalog.
- `/compact` uses the model of the ACP selections.

The image check runs only for a model without image input, before the turn
start is saved, against the same projection the model request would send. A
repeated skill invocation after a summary is therefore still counted.

The change retires a concept. It removes the model entry and its validation,
row kind, and match arms, the model lock and its configuration update, and the
store read in `set_config_option`. Across the six files it touches, production
code shrinks by 48 lines and test code by 121.

### Low

#### Comments

- **Ragged wrap after an edit** (`src/sessions.rs:55`): the `TranscriptEntry`
  doc comment ends its second line early ("turn start. Each assistant batch
  contains its message"), a leftover from removing the model-entry sentence.
  `eng/architecture.md:272` has the same problem in the compaction paragraph,
  where "selections, at its summarizer effort (its lowest effort level" breaks
  mid-thought. Nothing is wrong in either, but both read unevenly. Rewrap both
  paragraphs.

## Checks run

- `cargo fmt --all -- --check`: passed.
- `cargo test --all-targets --all-features`: 151 passed.
- `cargo build --all-features`: passed.
- `cargo clippy --all-targets --all-features -- -D warnings`: passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: passed.
- A search of `src`, `eng/architecture.md`, `eng/glossary.md`, `AGENTS.md`, and
  `README.md` found no remaining mention of the model entry or the model lock.
- A line count with `rsloc` against `HEAD` for the six changed files: production
  code went from 4,060 to 4,012 lines, and test code from 6,584 to 6,463.

## Verdict

Ready to commit. The only finding is two paragraphs to rewrap. Existing
sessions in `ox.db` do not load after this change, so run `make install` or
delete the session database before using a build that includes it.
