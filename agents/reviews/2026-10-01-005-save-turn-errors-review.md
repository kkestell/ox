# Save turn errors review

## Scope and coverage

Commit `a6d9430`, which saves a turn error as the last transcript entry of a
turn that ends with an error. The review covered the changes in `sessions.rs`,
`acp/prompt.rs`, `acp/convert.rs`, `compaction.rs`, `openai.rs`, and
`openrouter.rs`, and the code that depends on them: compaction cut selection,
replay in the Ox client's transcript view, and the session export script. It
was checked against `agents/plans/2026-10-01-006-save-turn-errors.md`.

Lenses: correctness, error-handling, testing, simplicity.

## Findings

### Fixed

- **Three failure tests stopped checking the whole transcript**
  (`crates/ox-server/src/acp/prompt.rs:1503`, `:2247`, `:2316`): the commit
  changed exact transcript assertions to prefix checks plus a check of the last
  entry, so an extra entry saved before the turn error would go unnoticed. The
  tests now compare every entry before the turn error, and the ACP update test
  compares the whole transcript with its known turn error text.

No open findings.

## Checks run

- `make check` — passed.

## Verdict

The change meets its plan. One test weakness was fixed; nothing is left open.
