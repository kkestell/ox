# Save turn errors in the transcript

## Goal

OX-0027: when a turn ends with an error, the error goes only into the prompt's
ACP error response. The Ox client shows it once as a "Turn error" notice, and
nothing records it. An exported or reloaded session cannot show why the turn
stopped. When this work is done, a turn that ends with an error saves a turn
error as its last transcript entry. Replay shows it, the export includes it, and
model requests ignore it.

## Related code

- `crates/ox-server/src/acp/prompt.rs:214` — `run`, which turns the turn's
  `PromptOutcome` into its result. Both the main agent and subagents go through
  it.
- `crates/ox-server/src/acp/prompt.rs:246` — `PromptOutcome`; its `Display`
  gives the text that explains each failure.
- `crates/ox-server/src/acp/prompt.rs:723` — `complete_interrupted`, which
  already folds a failed save into the outcome that ends the turn.
- `crates/ox-server/src/sessions.rs:61` — `TranscriptEntry`, with
  `validate_transcript` (`:572`), `append_batch` (`:805`), `encode_entry`
  (`:1017`), and `decode_entry` (`:1031`).
- `crates/ox-server/src/acp/convert.rs:287` — `subagent_message_updates`, which
  shows entries the model did not write as finished tool calls with IDs that Ox
  generates. `replay_transcript` (`:312`) matches every entry kind.
- `crates/ox-server/src/openai.rs` `input`, `crates/ox-server/src/openrouter.rs`
  `chat_messages` (`:398`), and `crates/ox-server/src/compaction.rs` `material`
  (`:321`) — the other exhaustive matches over transcript entries.
- `crates/ox/src/tui.rs:1162` — the live "Turn error" notice. It stays as it is.

## Decisions

- **Every failed outcome is saved.** A model request failure, an ACP update
  failure, a permission failure, and a storage failure each save a turn error.
  Cancellation, the token limit, and a refusal are not errors and save nothing.
  A turn whose turn start was never saved has no turn to record and saves
  nothing.
- **A failed save of the turn error joins the original error.** As
  `complete_interrupted` does, the turn then fails with a storage error that
  names both failures. Nothing is hidden.
- **The text is the outcome's `Display`**, for example "the model request
  failed: OpenAI returned 503 ...", so it says which step failed.
- **Child sessions save turn errors too.** The rule lives in `run`, which both
  agents share. The main session already receives the subagent's failure as a
  subagent message; the child session's own transcript now also shows why it
  stopped.
- **Model requests and summarizer material skip turn errors**, as they skip
  compaction checkpoints. The turn error is there for the user.
- **Replay shows a turn error as a failed tool call titled "Turn error"** with
  the text as its content, following subagent messages. That keeps it apart from
  model text. During a live turn, the server sends no update for it; the
  prompt's error response already reports it.
- **Validation accepts any turn error.** Ox writes the text from a non-empty
  `Display`, and the transcript is already required to open with a turn start.

## Naming

- `turn error` — the transcript entry that records why a turn ended with an
  error. Glossary term, used in comments and the replayed tool call title.
- `TranscriptEntry::TurnError(String)` — the variant, stored with kind
  `turn_error` and its text as a JSON string.
- `SessionStore::append_turn_error` — appends one turn error and updates
  activity.
- `AgentTurn::save_turn_error` — takes the outcome that ends the turn, saves a
  turn error when it is a failure, and returns the outcome that ends the turn.
- `convert::turn_error_update` — the failed tool call update that replay sends
  for a turn error.

## Test plan

- `crates/ox-server/src/acp/prompt.rs`
  `temporary_failures_are_retried_up_to_the_attempt_limit`: the failing cases
  now expect `[turn start, turn error]`, with the expected error text in the
  turn error.
- `crates/ox-server/src/acp/prompt.rs`
  `an_invalid_completion_runs_no_file_write`: expect the transcript to end with
  a turn error.
- `crates/ox-server/src/acp/prompt.rs`
  `a_failed_batch_append_leaves_the_transcript_unchanged`: the trigger refuses
  every kind except `turn_start`, so saving the turn error fails too. The error
  names "disk full" and says a turn error was being saved, and the transcript
  stays `[turn start]`.
- The cancellation tests keep asserting `[turn start]`, which shows that a
  cancelled turn saves no turn error.
- `crates/ox-server/src/sessions.rs`
  `a_saved_batch_survives_database_reopen_in_order`: append a turn error and
  read it back in order.
- `crates/ox-server/src/compaction.rs`: add
  `turn_errors_reach_neither_requests_nor_summarizer_material`, covering both
  model providers through `projection` and checking `material`.
- `crates/ox-server/src/acp/convert.rs`: add
  `turn_errors_are_replayed_as_failed_tool_calls`.
- Update any other test that asserts the transcript after a failed turn, such as
  the subagent failure tests in `crates/ox-server/src/subagents.rs`, so it
  expects the turn error.

## Implementation plan

1. In `crates/ox-server/src/sessions.rs`, add `TranscriptEntry::TurnError`, its
   `turn_error` kind in `encode_entry` and `decode_entry`, an accepting arm in
   `validate_transcript`, and `append_turn_error` beside `append_batch`.
2. Make the matches in `openai.rs` `input`, `openrouter.rs` `chat_messages`, and
   `compaction.rs` `material` skip the new variant.
3. In `crates/ox-server/src/acp/convert.rs`, add `turn_error_update` and send it
   from `replay_transcript`.
4. In `crates/ox-server/src/acp/prompt.rs`, add `AgentTurn::save_turn_error` and
   call it in `run` on the outcome of the model loop, before `stop_subagents`.
5. Update the tests as the test plan describes.
6. Mark OX-0027 fixed in `agents/issues.csv` and check it off in
   `agents/todo.md`.

## Documentation updates

- `agents/glossary.md`: add **Turn error** after **Turn start**.
