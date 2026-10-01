# Repeat the skill invocation across later turns review

## Scope and coverage

Commit `769c1b6`, which makes compaction repeat the latest skill invocation
after the summary whenever the summary covers it. The review covered
`repeated_invocation`, `projection_at`, and `ranked_cuts` in `compaction.rs`,
their tests, and the image check in `acp/prompt.rs` that reads the projection.
It was checked against
`agents/plans/2026-10-01-007-repeat-the-skill-invocation-across-turns.md`.

Lenses: correctness, testing, simplicity.

## Findings

### Low

#### Correctness

- **OX-0044: A compacted skill invocation with an image blocks models without
  image input until another skill invocation**
  (`crates/ox-server/src/compaction.rs:138`): the repeated skill invocation
  carries its images, and `save_turn_start` rejects any turn whose projection
  contains an image when the selected model does not accept images
  (`crates/ox-server/src/acp/prompt.rs:397`). Before this commit the image left
  the projection once a later turn began. Now, after a skill invocation with an
  image is compacted, every later plain message on such a model fails with "the
  selected model does not accept images", and `/compact` cannot clear it because
  the covered skill invocation is always repeated. A temporary test confirmed
  `has_images` is true for a transcript of a skill invocation with an image, a
  plain message turn, a checkpoint covering both, and a new plain message. A fix
  needs a decision, such as repeating the skill invocation without its images
  once its own turn has ended.

## Checks run

- `cargo test -p ox-server compaction` — passed.
- A temporary test of the image case above — passed, confirming the finding;
  removed afterwards.

## Verdict

The change meets its plan and its tests cover the new rule. One low severity
finding is left open.
