# Ox simplification fixes

Status: implemented and validated on 2026-09-21. Based on the
[latest simplification review](../reviews/2026-09-21-simplification-review.md),
checked against commit `264364c` on 2026-09-21.

Implement all three findings as separate changes in the order below. Each
removes a representation or a redundant path while preserving the current
session API, transcript encoding, and observable prompt behavior.

## 1. Store workspace paths on sessions

Change `src/sessions.rs`:

- Remove the `workspaces` table and replace `sessions.workspace_id` with
  `workspace_path TEXT NOT NULL`. Do not make the path unique: multiple sessions
  can use the same workspace.
- Reduce `SessionStore::create` to one session insert under the existing
  connection mutex. Remove its explicit transaction, workspace insert, and
  workspace-ID subquery. Keep absolute-path validation and empty transcripts.
- Read the path directly from `sessions` in `list` and `summary`, removing both
  joins. Preserve the selected column order expected by `summary_row`.
- Keep exact path-string filtering, activity ordering with the session ID as
  the tie-breaker, and the transcript-entry foreign key with deletion cascade.
  Retain the transactions for reads and transcript writes.

Update the logical-schema paragraph in `eng/ox-architecture-design.md` to
describe the path stored on each session. The public session types and the
`AGENTS.md` source map already describe the intended responsibilities and need
no structural changes.

Validation: run the session-store tests and
`acp::tests::load_and_list_use_the_same_exact_workspace_path`. Existing tests
cover multiple sessions sharing a path, absolute-path rejection, filtered
listing, activity ordering, and deletion cascade. Extend the existing ordering
test with equal timestamps to exercise its ID tie-breaker.

Use fresh temporary databases for validation. When installing this schema,
recreate the disposable local database through the existing install workflow;
it already removes `ox.db` and its SQLite sidecar files. Do not add a migration,
schema version, or automatic recovery for an older schema.

## 2. Store tool results in execution order

Change `UncommittedAssistantBatch` in `src/acp/prompt.rs`:

- Replace `outcomes: Vec<Option<ToolOutcome>>` with
  `results: Vec<ToolResult>`, initially empty.
- In `execute`, build one `ToolResult` from the current call and its observed
  outcome, append it to the batch, then send its finished ACP update. Iteration
  no longer needs an index. Permission denial also appends a failed result for
  that call before continuing.
- Remove `set_outcome`. Replace `fill_empty_outcomes` with a small operation
  that creates results for `message.tool_calls[results.len()..]`, appends them,
  and returns the newly created results for the remaining ACP updates.
- Make `complete` call `AssistantBatch::new` with clones of the message and
  stored results. Keep its invariant failure if the complete batch is invalid.
  Do not consume or clear the uncommitted batch before a successful save.

The representation relies on sequential execution: observed results always
form the first part of the call list. Keep tool execution sequential. A short
comment on the results field should explain that relationship.

Preserve these ordering rules:

- Record an observed outcome before attempting its finished ACP update.
- On cancellation or an error, retain every observed result and give only the
  remaining calls explicit not-started outcomes, using the existing text and
  outcome variants.
- Let an active patch finish and let shell cancellation complete process
  cleanup before recording its result.
- Validate and save the complete assistant batch through the existing commit
  path. Extend the in-memory transcript and clear the uncommitted batch only
  after `SessionStore::append_batch` succeeds.
- Keep the operation guard until save attempts and response handling finish.
  Preserve the existing handling of failed saves and failed ACP updates.

Validation: run `acp::prompt::tests::`. Its existing tests exercise ordered
results, cancellation before and during execution, retained shell and patch
results, failed ACP updates, failed saves, and results in the next model
request. The ACP permission tests also need to pass. Adapt tests only where
signatures change; do not add tests of vector mechanics.

## 3. Resolve settings from the existing prompt-startup read

Change `src/acp.rs` and `src/acp/prompt.rs`:

- Replace `ServerState::session_settings` with a simple selection lookup that
  returns a cloned `Option<SessionSettings>`. Name it `selected_settings` to
  distinguish an explicit selection from settings saved in the transcript.
  Remove its database read, model validation, and insertion into the map.
- Pass that optional snapshot through `PromptInput` to `PromptRun::open`.
  Name the input `selected_settings`; retain `PromptRun.settings` for the
  resolved settings used by every model request in the turn.
- Read and validate the session once in `PromptRun::open`, as today. Fold its
  saved settings once, using the existing defaults. Resolve the turn's settings
  according to the table below, then validate the effective model once.
- Derive `SessionSettingsChange` by comparing those resolved settings with the
  saved settings, preserving first-model insertion and changed-effort entries
  immediately before the user message.
- Keep new-session and load handlers seeding selections and configuration
  requests using their existing stored-settings fallback. Do not seed the map
  merely because a prompt arrived without a selection.
- Have headless execution pass `Some(default_settings())` explicitly. Update
  the existing prompt fixtures and ACP tests for the optional input.

| Transcript | Selection snapshot | Model for this turn | Effort for this turn |
| ---------- | ------------------ | ------------------- | -------------------- |
| Empty | Present | Selected model | Selected effort |
| Empty | Absent | Default model | Default effort |
| Nonempty | Present | Saved model | Selected effort |
| Nonempty | Absent | Saved model | Last saved effort |

Keep selection lookup, prompt startup, and user-message save synchronous in
the ordered ACP handler, before spawning the model loop. Drop the selections
mutex after cloning the snapshot. A later configuration request must affect
the next turn, even if the current prompt has not yet made its model request.
Missing sessions and unknown saved models must still fail before saving a user
message or requesting model output.

Update section 6 of `eng/plans/ox-session-settings.md` to describe the optional
snapshot and stored-settings fallback. The reduction applies when selections
are absent; ordinary prompts after new-session or load already read once.

Add focused coverage using the existing mock OpenRouter server:

- Create a session directly through the store, with a nondefault catalog model
  and nondefault saved effort, leaving the selections map empty. Run a prompt
  with the absent snapshot. Verify the request uses those saved settings and
  the transcript does not gain duplicate model or unchanged effort entries.
- Exercise an empty session without selections and verify the first prompt
  saves the default model and uses default effort.
- Exercise missing sessions and an unknown saved model through prompt startup,
  asserting an error, no model request, and no appended user message.
- Exercise a conflicting selected model on a started session and verify the
  saved model wins while the selected effort applies. Retain the existing test
  for an effort change during a running prompt.

Do not add database-read counters or another store abstraction to test the
removed lookup. Inspect the call path to confirm prompt startup is the only
session read for this operation.

## Completion checks

During implementation, run the focused checks above after each change. At the
end, run the affected suites once against the combined result:

```sh
cargo test sessions::tests::
cargo test acp::
cargo test openrouter::tests::requests_map_each_effort_for_each_model
```

Then build the changed binary and run one live headless prompt using the
`OPENROUTER_API_KEY` from `.env`, a temporary workspace, and a temporary
`OX_DATA_DIR`. Ask it to create a small file and read it back. Confirm a
successful exit, the expected file, and a readable saved transcript with one
result per tool call in call order. Inspect the database to confirm the session
contains its workspace path directly. Use mock tests for cancellation and
error behavior; the live run checks the combined request, tool, and persistence
path without relying on a model to reproduce failures.

The work is complete when all three redundant representations or paths are
gone, the focused tests and live run pass, and the architecture and settings
documents describe the resulting implementation. Mark this plan implemented
only after recording those results. Any unavailable live check should remain
explicitly outstanding.

## Implementation results

- `cargo test sessions::tests::` passed 7 tests.
- `cargo test acp::` passed 39 tests, including the four added prompt-settings
  startup cases and the existing permission and cancellation coverage.
- `cargo test openrouter::tests::requests_map_each_effort_for_each_model`
  passed.
- `cargo build` passed.
- A live headless run with `.env`, a temporary workspace, and a temporary
  `OX_DATA_DIR` exited successfully. It created `note.txt`, read back its exact
  contents, and saved two tool calls with one result each in call order.
- Inspection of the temporary database confirmed that `sessions` contains
  `workspace_path` directly and that no `workspaces` table is present.
