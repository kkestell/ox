# Compaction and hook architecture review

## Scope and coverage

Reviewed the feature paths across `src/compaction.rs`, `src/hooks.rs`,
`src/skills.rs`, `src/acp.rs`, `src/acp/operations.rs`, `src/acp/prompt.rs`,
`src/acp/convert.rs`, `src/sessions.rs`, `src/openrouter.rs`, and
`src/process.rs`, plus the goal and careful examples and the architecture,
glossary, testing, and feature-plan documents.

Selected lenses: architecture, correctness, API design, performance,
readability, Rust idioms, testing, and documentation. I traced how definitions
become prompt inputs, how hooks affect tool batches and transcript state, and
how compaction estimates, summarizes, commits, and projects that transcript.
No live OpenRouter request or interactive ACP client check was performed.

## Findings

### Low

#### Architecture

- **Compaction has a separate lifecycle owner that the architecture does not
  name** (`src/compaction.rs:398`, `src/acp.rs:213`,
  `eng/architecture.md:41`): `compaction::compact` sends one or more
  summarizer requests, validates the projected size, commits a checkpoint,
  and updates the caller's transcript. Both prompt runs and the explicit
  `/compact` session operation call it. The architecture instead says the
  prompt run is the only component that sequences model requests and
  transcript commits, and does not list compaction as an operation owner. The
  code's separate compaction workflow is reasonable, especially for `/compact`,
  but the stated boundary points maintainers toward a refactor that would put
  manual compaction inside a prompt run or duplicate the workflow. Name
  compaction as a cancellable session operation and describe it as owner of
  summarizer requests and checkpoint commits. `compaction.rs` also imports
  `PromptCancellation` from `acp::operations`; move that shared signal type to
  a lower-level module so the compaction implementation does not depend on the
  ACP layer.

#### Correctness

- **Transcript validation accepts hook feedback outside its skill run**
  (`src/sessions.rs:461`): The validator checks only the immediately preceding
  entry's variant. It accepts `before_stop` feedback after an ordinary user
  turn, and accepts feedback whose `skill` does not match the turn's saved
  `SkillInvocation`. `SessionStore::read` treats validation as the boundary
  for stored transcript data; accepted feedback is then replayed as a hook call
  and sent to the model as feedback (`src/acp/convert.rs:213`,
  `src/openrouter.rs:297`). Track the current turn's skill while validating;
  require all hook feedback to belong to that skill, and reject feedback in
  ordinary turns.

- **Compaction silently presents a partial tool result as complete**
  (`src/compaction.rs:325`): Summarizer material keeps only the first 2,000
  characters of each tool result, with no truncation marker or tail. For long
  command output, useful failure details often occur near the end. The
  summarizer can therefore produce a confident but incomplete summary. Include
  an explicit omission marker and retain useful context from both ends, or
  otherwise label the material as an excerpt.

#### API design

- **Hook responses are not restricted to JSON objects** (`src/hooks.rs:291`):
  Deserializing directly into a struct accepts sequence-shaped JSON for some
  response types, and `Feedback.message: Option<String>` treats an explicit
  `null` as no message. The documented protocol requires one response object;
  a present message must be a nonblank string. This lets a malformed command
  response be treated as valid feedback or a successful report. Validate the
  top-level JSON value as an object and distinguish an absent `message` from
  `null`. The same parser issue was confirmed in the earlier
  [skill lifecycle hooks review](2026-09-22-005-skill-lifecycle-hooks-review.md)
  and remains in the current implementation.

#### Performance

- **Checkpoint validation repeatedly scans all completed batches**
  (`src/sessions.rs:486`): `complete_batches` is built in ascending order, but
  each checkpoint uses linear `Vec::contains`. A long transcript with many
  checkpoints makes every full transcript validation proportional to batches
  times checkpoints. Reads validate the whole transcript, and checkpoint
  appends validate it again. Use `binary_search` on the already sorted vector
  or another direct boundary check to keep validation linear or better.

## Unresolved questions

- `ranked_cuts` in `src/compaction.rs` prefers retaining up to two completed
  turns through a ranking tuple that encodes the limit as `2`. I found no
  architecture or product requirement for that exact preference. If preserving
  two turns improves results, name and document the policy and cover it at the
  compaction boundary. Otherwise, remove the completed-turn scan and rank cuts
  by the context reduction they achieve.

## Checks run

- `cargo test`: 94 passed.
- `python3 -m unittest discover -s examples/skills/careful/scripts`: 4 passed.
- `python3 -m unittest discover -s examples/skills/goal/scripts`: 1 passed.
- Traced prompt, manual compaction, transcript validation, model projection,
  replay, cancellation, and hook process behavior through their callers.

## Verdict

The feature boundaries around skill loading, hook process execution, transcript
storage, and model encoding are mostly cohesive. The main architectural gap is
that compaction is a real second session-operation workflow without a named
place in the architecture. Tighten transcript and hook response validation,
make tool-output truncation explicit, and simplify checkpoint validation. Keep
hook lifecycle sequencing in `PromptRun`; extracting it solely to reduce that
file's size would separate behavior from the turn lifecycle that owns it.
