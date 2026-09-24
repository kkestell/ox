# Simplify and delete code

## Scope and coverage

Reviewed the whole `src/` tree at `942f374`, looking for code that can be
simplified or deleted without changing observable behavior. The working tree
was clean when the review began; unrelated concurrent edits to `AGENTS.md`,
`README.md`, `src/cancellation.rs`, and the OpenRouter status-error text of
`src/openrouter.rs` appeared while it ran, and every line number below refers
to `942f374`.

Production code was read throughout `src/`: `main.rs`, `auth.rs`,
`cancellation.rs`, `system_prompt.rs`, `text_file.rs`, `settings.rs`,
`skills.rs`, `hooks.rs`, `process.rs`, `tools/workspace.rs`, and
`acp/operations.rs` in full, and the remaining modules through their callers
and the regions named below. Test code was read where it decides reachability
(the default tool-call titles, malformed stored transcript rows, the compaction
ranking test) and sampled elsewhere. The example skills, the Python scripts,
and the build tooling were not reviewed, and no live OpenRouter or ACP client
was exercised.

Lenses: architecture, API design, readability, correctness, performance,
naming, testing, and Rust idioms. Severity below is simplification priority; no
finding claims a demonstrated user-facing failure.

The duplicated output capture and process-group cleanup in `src/process.rs` and
`src/shell_processes.rs` is already covered by concurrent work in this
workspace (`eng/reviews/2026-09-24-008-simplification-review.md` and
`eng/plans/2026-09-24-008-shared-output-capture.md`), so it is not repeated
here.

## Findings

### Medium

#### Architecture

- **`transcript_entries.ts` is written and never read** (`src/sessions.rs:47`,
  `src/sessions.rs:1076`, `src/sessions.rs:1104`, `src/sessions.rs:1394`).

  The column is set by both entry inserts
  (`INSERT INTO transcript_entries (session_id, ts, kind, data)`) and read by
  nothing: `read_transcript` selects `kind, data` and orders by the rowid `id`,
  and no query, index, or document consumes `ts`. Its only effect is that
  `write_entries` threads a timestamp through `insert_entry` for that column
  alone. Entry order is already the rowid, and session activity already lives
  in `sessions.updated_at`. Fix: drop the column and the `at` parameter of
  `insert_entry` (updating the test-only insert at `src/sessions.rs:1394`);
  `write_entries` keeps `now()` for `update_activity_and_adopt_session_title`.
  The project recreates `ox.db` rather than migrating, so no migration is
  needed.

- **The SSE reader carries multi-line `data:` state that nothing produces**
  (`src/openrouter.rs:576`, `src/openrouter.rs:628-641`).

  `CompletionStream.data` exists to join consecutive `data:` lines with a
  newline and flush them on the blank line. Every producer emits exactly one
  `data:` line per event (`fixture::sse`, `src/openrouter.rs:1187-1194`, and the
  ACP and prompt test bodies), and joining lines would only parse for JSON that
  OpenRouter does not send. Fix: pass each stripped `data:` line straight to
  `process_sse_event`, deleting the `data` field, the `std::mem::take` flush,
  and the join branch; `line` becomes an empty-line no-op plus one parse call.

#### API design

- **`save_turn_start` returns a one-element vector**
  (`src/acp/prompt.rs:400-435`, `src/acp/prompt.rs:230-235`,
  `src/acp/prompt.rs:438-446`).

  The function builds `Vec<SessionUpdate>`, pushes exactly one
  `SessionInfoUpdate`, and returns it; `run` unwraps the option, and `start`
  loops over the vector to send it. `save_turn_start` is called only from `run`
  and `start` only from `run`, so the collection carries no variable content.
  Fix: return `Result<Option<SessionUpdate>>` from `save_turn_start`, take the
  update in `start`, and send it once. This deletes the vector, the push, and
  the loop, and the two call sites read as "announce the saved turn start".

- **`Workspace::regular_file` is a second regular-file check with one caller**
  (`src/tools/workspace.rs:127-131`, `src/tools/search.rs:175`,
  `src/tools/workspace.rs:105-110`).

  The only caller is the glob branch of `SearchOutput::add_candidate`
  (`workspace.regular_file(&relative).unwrap_or(false)`), and `read_file`
  already answers the same question by opening the target without following
  links and rejecting a non-regular file with `InvalidInput`. `statat` plus
  `FileType::from_raw_mode` is a second implementation of that rule. Fix:
  delete `regular_file` and test `workspace.read_file(&relative).is_ok()`, as
  the grep branch already does with the same workspace path.

- **`Presentation` stores subagent suppression beside the identity that
  implies it** (`src/acp/prompt.rs:71-74`, `src/acp/prompt.rs:86-89`,
  `src/acp/prompt.rs:114-118`, `src/acp/prompt.rs:155-162`).

  `Target::Acp { updates }` is set true only by `Presentation::acp`, which
  builds a main identity, and false only by `Presentation::subagent`, which
  always sets `identity.subagent_id`. No other site reads or writes the field,
  and `Presentation::observed` is main-only, so
  `updates == identity.subagent_id.is_none()` holds for every constructed
  value. Two fields therefore encode one fact and must be assigned together.
  Fix: delete `updates` and decide in `sends_updates` and `send` by
  `self.identity.subagent_id.is_none()`, which is the rule the doc comment on
  `Target::Acp` states.

### Low

#### Architecture

- **`Drop` and `shutdown` repeat the subagent close-out**
  (`src/subagents.rs:144-157`, `src/subagents.rs:290-299`).

  Both set `open = false`, clear `messages`, and cancel every agent;
  `shutdown` additionally collects each task to await and clears the agents
  afterwards, while `Drop` drains them without awaiting. Fix: one `State`
  method that closes admission, discards messages, and cancels each
  agent, called by both paths, leaving `shutdown` to collect and await the
  tasks and `Drop` to clear the list.

- **Compaction ranks cuts with a second request-size model**
  (`src/compaction.rs:185-229`, `src/compaction.rs:59-70`,
  `src/compaction.rs:217-229`, test `src/compaction.rs:734-769`).

  `ranked_cuts` adds a base body of the summary, per-entry `message_bytes`, and
  the repeated invocation, while `projected_estimate` builds and measures the
  whole projected request; the image allowance expression and the per-message
  comma are written in both, and the test
  `ranked_cuts_follow_the_latest_checkpoint_smallest_request_first` exists to
  assert they agree. The incremental form is a deliberate performance choice
  (one serialization instead of one per candidate cut), so replacing it with
  `projected_estimate` per cut would trade memory work for simplicity. Fix
  that keeps the incremental path: express the per-message and per-body sizes
  through one helper so the image allowance and separator rule exist once, and
  let the existing test keep pinning the two views together.

#### Naming

- **`Operation::Load` and `Operation::Delete` are one state under two names**
  (`src/acp/operations.rs:16-20`, `src/acp/operations.rs:49-55`,
  `src/acp/operations.rs:59-67`).

  The only inspections of the value are `cancel` (`Prompt` vs
  `Load | Delete`), `close` (`if let Prompt`), and `Drop`, which is
  variant-independent; `OperationGuard` does not store the variant, so nothing
  can ask whether a busy session is loading or deleting. Fix: one variant for
  both, keeping `try_load` and `try_delete` as the two named entry points.

#### Readability

- **The choice-value error is rebuilt three times** (`src/acp.rs:337-343`,
  `src/acp.rs:354-359`, `src/acp.rs:361-367`).

  The `model`, `effort`, and `mode` arms each wrap their lookup in the same
  five-line closure producing `"{value} is not a choice of configuration
  option {config_id}"`. Fix: one local closure built from `value` and
  `request.config_id` and used by the three arms, so the message shape exists
  once.

- **`shutdown_shell_processes` signals owners that are already signalled**
  (`src/acp.rs:531-539`, `src/shell_processes.rs:209-220`,
  `src/acp.rs:522-529`).

  `ShellProcesses::shutdown` begins with `self.begin_shutdown()`, and
  `futures::future::join_all` polls every future before any of them awaits, so
  all owners are signalled on the first poll; `ServerState::begin_shutdown`,
  which already loops over the same owners, runs on both shutdown paths before
  this function. Fix: delete the `for` loop, keep `join_all`, and update the
  comment, which now attributes to the loop what the join already provides.

- **`before_run` and `after_tools` repeat one hook loop**
  (`src/acp/prompt.rs:447-461`, `src/acp/prompt.rs:662-672`).

  The two loops differ only in the event and in the
  `HookFeedbackContent` variant their closure builds; the other three hook
  loops accumulate a result and genuinely differ. Fix: one loop taking the
  event and the feedback constructor, so adding a feedback hook kind touches
  one place.

#### API design

- **The `arguments:` parse error is rewritten in five modules**
  (`src/tools/read.rs:51`, `src/tools/search.rs:84`, `src/tools/search.rs:89`,
  `src/tools/shell.rs:105`, `src/tools/shell.rs:219-222`,
  `src/tools/subagent.rs:155`, `src/tools.rs:297-300`).

  `subagent.rs` already has the wrapper
  (`fn parse<'a, T: Deserialize<'a>>(arguments: &'a str) -> Result<T, String>`)
  while the other modules inline the same `format!("arguments: {error}")`
  mapping, which is the tool-error prefix every module must keep identical.
  Fix: one crate-visible `parse_arguments` next to the shared tool helpers,
  used by every module, deleting the inline copies and the local helper.

#### Correctness

- **`children_cost` restates the cost rule in SQL**
  (`src/sessions.rs:940-954`, `src/sessions.rs:723-734`).

  The child-cost query hardcodes `$.message.usage.cost` and
  `$.summarizer_cost` plus the two entry kinds, which is the rule
  `transcript_cost` already implements for the main transcript. Renaming a
  field or adding a cost-bearing entry kind would silently drop child cost from
  the usage update while main-transcript cost keeps working, with only
  `children_cost_sums_the_saved_costs_of_every_child_session` connecting them.
  Fix: derive both from one function, or read the children's entries and sum
  them with `transcript_cost`; note that this moves the sum from SQL into Rust,
  so the whole-child-transcript read per usage update is the trade-off to
  weigh.

#### Testing

- **Fixture reply builders re-implement `calls_reply`**
  (`src/openrouter.rs:980-997`, `src/openrouter.rs:1225-1242`,
  `src/openrouter.rs:1245-1265`).

  `shell_reply` and `tool_reply` each rebuild the same
  `{"role":"assistant","tool_calls":[...]}` event with `Some("tool_calls")`
  that `calls_reply` builds, fixing only the tool name and the argument object.
  Fix: map each helper's arguments to the `(&str, &str, Value)` triples and
  delegate to `calls_reply`, deleting about forty duplicated fixture lines
  while the call sites stay short.

## Unresolved questions

- `TranscriptEntry` is the only stored transcript type without serde
  attributes: `encode_entry`/`decode_entry` (`src/sessions.rs:1120-1154`) match
  on kind strings while `TurnInput` (`src/sessions.rs:119-129`) already uses
  the adjacent-tagged form. Reusing that form would delete `decode_entry` and
  `decode`, but the `kind` column is also used by the `children_cost` SQL, so
  the enum would still need a kind accessor, and the unknown-kind message in
  `a_nonempty_transcript_opens_with_a_turn_start` would change. Deciding this
  needs the cost rule above to be settled first; it was not counted as a
  reduction.
- The `unreachable!("list returned above")` arm at `src/tools/shell.rs:298`
  is dead, but removing it needs the second `match action` to stay exhaustive;
  the alternatives duplicate the `list` call as another unreachable arm, which
  is not a reduction. Confirming a clean restructure would need the exact
  replacement, so it is not reported as a finding.

## Checks run

- `cargo test --all-targets --all-features`: passed, 172 tests in the main
  binary plus one test in each of two other targets.
- `cargo clippy --all-targets --all-features --message-format short`: passed,
  no warnings, which also rules out compiler-visible dead code.
- `cargo fmt --all -- --check`: passed.
- `cargo build --all-features`: passed.
- Searches for the callers of every item named above (`regular_file`,
  `save_turn_start`, `insert_entry`, `children_cost`, `ranked_cuts`,
  `message_bytes`, `Operation::Load`/`Delete`, the fixture reply builders,
  `self.data`, and `arguments: `) to confirm each claim of single or duplicate
  use.
- Read `eng/architecture.md`, `eng/code-style.md`, `eng/glossary.md`, and
  `eng/testing.md`, plus earlier simplification reviews and plans for the
  intended design and already-retired concepts.
- No source file was changed; only this document was added.

## Verdict

No behavior defect was found; the tree is in good shape and the largest earlier
simplifications (batch ownership, turn settings, outcome payloads) are already
in place. The findings are small, independent reductions: the strongest are the
write-only `ts` column, the unreachable multi-line SSE state, the one-element
update vector, the single-use `regular_file`, and the duplicated subagent
suppression flag. Applying the medium items retires a column, a parameter, a
field, a branch, and two matched blocks, and each can be done on its own.
