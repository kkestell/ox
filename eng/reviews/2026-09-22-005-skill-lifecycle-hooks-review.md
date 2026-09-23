# Review skill lifecycle hooks

## Scope and coverage

Reviewed the working-tree implementation of
`eng/plans/2026-09-22-004-skill-lifecycle-hooks.md` against the current plan:
skill definitions and filters, hook commands and response validation, prompt
ordering, permission decisions, cancellation, final reporting, transcript
persistence, replay, model context, compaction, and the careful and goal examples.
Traced the existing process runner and operation guards where the feature relies
on them.

Selected lenses: correctness, error-handling, resources, concurrency, api-design,
security, testing, documentation, architecture, and rust-idioms.

Validation used the local Rust suite, fake OpenRouter and ACP fixtures, example
script tests, and an isolated parser probe. No live OpenRouter requests or
interactive ACP client checks were performed. Implementation files were not
changed.

## Findings

### Low

#### API design

- **Reject malformed hook response shapes** (`src/hooks.rs:291`,
  `src/hooks.rs:150`, `src/hooks.rs:197`): The shared parser directly
  deserializes into structs, which also accept JSON arrays; `deny_unknown_fields`
  does not require an object. An isolated probe using the actual parser confirmed
  that `before_run` accepts `["context"]` as feedback and `after_run` accepts
  `[]` as successful reporting. It also confirmed that `after_tools` accepts
  `{"message": null}` as no feedback because `Option<String>` treats null like
  an absent field. These responses violate the plan's object-only protocol and
  its requirement that a present message be a nonblank string. A broken script
  can therefore be reported as successful, and null feedback silently lets the
  run continue without the required hook error. Require an object and reject a
  present null message while preserving `{}` as the valid no-feedback response.
  Extend the existing response-validation test with these cases.

## Checks run

- `cargo test`: all 94 tests passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: all four
  tests passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: its test passed.
- Temporary source-copy probe, run with `cargo test --offline --manifest-path
  <temporary-copy>/Cargo.toml review_protocol_probe -- --nocapture`: confirmed
  all three malformed responses above are accepted. The first probe setup lacked
  a compile-time included plan file; after copying that fixture, the probe passed.
  The temporary copy was removed, and no probe tests were added to the repository.
- Inspected the implementation diff and traced hook ordering, batch commits,
  failure completion, feedback placement, and shutdown ownership against the
  plan and architecture.

## Verdict

The lifecycle implementation follows the plan, with no confirmed high- or
medium-severity findings. Tighten response validation to enforce the documented
protocol.
