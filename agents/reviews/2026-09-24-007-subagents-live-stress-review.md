# Subagents live stress review

## Scope and coverage

Ran live headless prompts against commit
`ac277156e781d4f4b0c7155e4f540e823c050bef` with `scripts/run.py` and, for
the signal test, `target/debug/ox run` directly. Every run used the default
model, `deepseek/deepseek-v4.1-flash`, in auto mode, with its own
`OX_DATA_DIR` so each saved session could be inspected afterward.

Lens: live behavior of the subagent tools, cancellation, shared shell
processes, and saved child sessions.

Not covered: Ask mode permission requests, ACP replay and usage updates,
session listing, automatic compaction while subagent messages arrive, and a
subagent turn that fails. Headless runs cannot exercise the ACP paths.

## Findings

No confirmed defects. All ten scenarios behaved as the tool descriptions and
`src/prompts/subagent_prompt.md` describe.

| Scenario | Result |
|---|---|
| Four subagents, then a fifth | All four ran at the same time and wrote the right files. The fifth was refused with the "at most 4" error, which lists each subagent's state. |
| Question and follow-ups | The subagent asked its question before reading anything. Both replies continued the same conversation, and it remembered which file it had already summarized. |
| Messages sent to a busy subagent | Three messages queued with increasing counts and were answered in the order sent. Stopping a subagent in the middle of `sleep 120` took about 0.85 seconds and ended the `sleep`. |
| Concurrent edits | Three subagents each added 1 to a counter file 10 times with `apply_patch`, and the file ended at 30. The saved child sessions show exactly 30 successful patches and 13 failed ones: an edit made from an out-of-date read failed and was retried, and no update was lost. |
| Shared background commands | One subagent started an HTTP server. A second found it with `shell_process list` and made requests to it, then the main agent stopped it. |
| Subagent calling coordination tools | Each call returned "`<tool>` is available only to the main agent." |
| Finishing without waiting | Two subagents running `sleep 300` were stopped when the main agent finished, and no `sleep` processes were left. |
| SIGINT with busy subagents | Ox exited at once with status 1 and `ox: prompt stopped with Cancelled`. All three subagent `sleep` commands and the main agent's background `sleep` were killed. Each child session saved a cancelled outcome for its shell call, and the main session saved "Cancelled while waiting for subagents." |
| Bad arguments and slot reuse | Oversized, blank, unknown-field, and wrong-type arguments were all rejected. Stopping two of four idle subagents let two new ones start. |
| Real repository (tinyexpr at `c3b2f32`) | Three subagents split the reading, test review, and build. Both builds passed, and `REVIEW.md` credits each fact to the subagent that found it. |

## Resolved questions

Each of these behaved as the code intends. All three are settled.

- **Stopped and unknown IDs give the same error** (`src/subagents.rs:466`):
  `send_message` or `stop_subagent` for a subagent that was already stopped
  returned "No subagent X in this prompt run.", the same text as for an ID
  that never existed. Resolved by rewording the error to "No live subagent X
  in this prompt run." Keeping the IDs of stopped subagents only to give
  them a separate error would add state for no change in behavior, and the
  earlier stop already returned "Stopped subagent X."
- **Negative waits are accepted** (`src/tools/subagent.rs:212`): `wait` with
  `seconds: -1` is treated as 0, although the schema at
  `src/tools/subagent.rs:121` declares a minimum of 0. Kept as is. The
  schema tells the model the valid range, and treating a negative value as
  0 does no harm. The clamp must stay because `Duration::from_secs_f64`
  panics on a negative value.
- **Child sessions with no reply**: a subagent stopped before its first model
  reply leaves a child session holding only its turn start, with no later
  entry. Kept as is. A model request cancelled before its reply saves
  nothing for the main agent either (`src/acp/prompt.rs:809` and
  `src/acp/prompt.rs:826`), and the session accurately records a task that
  was assigned and never answered.

## Checks run

- Ten live scenarios as listed above. All exited 0 except the SIGINT run,
  which exited 1 as expected.
- Inspected each run's `ox.db` for child sessions, their parent links, and
  saved outcomes. Counted `apply_patch` outcomes per child session in the
  concurrent-edit run.
- Checked with `pgrep` that no `sleep` process outlived its run.
- After rewording the error:
  - `cargo fmt --all -- --check`: passed.
  - `cargo test --all-targets --all-features`: 172 tests passed.
  - `cargo build --all-features`: passed.
  - `cargo clippy --all-targets --all-features -- -D warnings`: passed.
  - `python3 -m unittest discover -s examples/skills/goal/scripts`: passed.
  - `python3 -m unittest discover -s examples/skills/careful/scripts`:
    passed.

## Verdict

Subagents held up under concurrency, queuing, stopping, shared background
commands, cancellation, and a real repository task. The one follow-up,
rewording the unknown-subagent error, is done.
