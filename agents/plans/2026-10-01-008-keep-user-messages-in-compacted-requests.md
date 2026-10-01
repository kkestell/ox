# Keep user messages in compacted requests

## Goal

Compaction today replaces everything before the cut with one free-form summary.
The user's own requests survive only as the summarizer retells them, nothing
reliable records which files changed or which commands failed, and the chosen
cut usually covers the whole transcript, so the model continues from the summary
alone.

When this work is done, a compacted model request keeps the system prompt, every
user message, and the latest skill invocation as they were. Code replaces each
covered tool call with its tool call title in an action log. A summary in a
fixed format replaces covered assistant answers, tool output, and subagent
messages. The newest entries stay unchanged, so the model keeps the output it is
working from.

## Related code

- `crates/ox-server/src/compaction.rs:111` — `projection`, which builds the chat
  messages of every model request from the transcript.
- `crates/ox-server/src/compaction.rs:138` — `repeated_invocation`, which
  repeats the latest skill invocation after the summary. This plan removes it.
- `crates/ox-server/src/compaction.rs:177` — `projection_at`, the projection for
  a given covered prefix and summary.
- `crates/ox-server/src/compaction.rs:192` — `candidates`, the cut positions
  after each assistant batch. Unchanged.
- `crates/ox-server/src/compaction.rs:221` — `input_fits`, the admission check
  for a new turn start or subagent messages. Unchanged.
- `crates/ox-server/src/compaction.rs:237` — `ranked_cuts` and `entry_bytes` at
  `:276`, the cut search this plan replaces.
- `crates/ox-server/src/compaction.rs:329` — `material`, the summarizer
  material. Unchanged.
- `crates/ox-server/src/compaction.rs:476` — `compact`, which tries cuts in
  ranked order and commits the first checkpoint that fits.
- `crates/ox-server/src/tools.rs:120` — `tool_call_title`, the one-line
  description of a call the ACP client shows, already capped at 80 characters.
- `crates/ox-server/src/tools/shell.rs:459` — a shell call whose command exits
  nonzero has a failed tool outcome, so the action log can mark it.
- `crates/ox-server/src/sessions.rs:89` — `SubagentMessage::label`.
- `crates/ox-server/src/model.rs:374` — `skill_invocation_message`.
- `crates/ox-server/src/prompts/compaction_prompt.md` — the summarizer
  instructions.

## Decisions

- **Covered entries are projected in order, not replaced wholesale.** For each
  entry before the covered prefix:
  - A turn start with a user message is sent as that user message.
  - A turn start with a skill invocation is sent as the full skill invocation
    message if it is the latest skill invocation in the transcript, and
    otherwise as its slash command text (`SkillInvocation::command_text`).
  - An assistant batch adds one action line per tool call to the action log. Its
    assistant text is dropped from the request and reaches the model only
    through the summary.
  - Subagent messages add one action line each, their label. Their text reaches
    the model only through the summary.
  - Turn errors and compaction checkpoints add nothing, as today.

  Each run of action lines between two user messages becomes one user-role
  action log message, placed where those tool calls happened. The summary
  follows the last covered entry, and the recent entries follow it unchanged.
- **The latest skill invocation stays in its place.** Because every turn start
  is now projected, the repeated skill invocation is no longer needed and
  `repeated_invocation` is removed.
- **Covered images become text.** In a covered user message or skill invocation,
  each image becomes the text `[image: <MIME type>]`, which the summarizer
  material already uses. Text is never shortened. This keeps the existing
  guarantee that a covered image leaves the request, so a model without image
  input can continue the session. It also resolves OX-0044.
- **Long user messages are not bounded.** A prompt that cannot fit beside the
  other user messages is refused by `input_fits` with the existing "prompt
  exceeds the model context limit" error. A session whose user messages and
  action log alone outgrow the context ends its turn with the existing context
  error, and the user starts a new session. A prompt larger than the admission
  limit was admitted before and compacted by summarizing it; it is now refused.
- **The action log is not grouped or trimmed.** One action line costs about 30
  tokens, so 1,000 tool calls cost about 30,000. That is a real cost for a
  128,000-token model and small for larger ones. Grouping waits until a session
  actually hits it.
- **An action line is the tool call title**, the same text the ACP client shows,
  so one call has one description. Shell titles are the bare command, so a shell
  action line starts with `$`. A failed or cancelled tool outcome adds
  `(failed)` or `(cancelled)`. Titles are already at most 80 characters.
- **The action log is a user message, not an assistant message.** Assistant
  messages that read "Edit src/a.rs" would invite the model to answer with such
  lines instead of calling tools. Consecutive user messages already occur today:
  the summary is followed by the repeated skill invocation or by the next turn
  start.
- **The cut keeps the newest entries within a recent allowance.** The recent
  allowance is 20 percent of the admission limit. The cut is the earliest
  candidate whose recent entries fit the recent allowance. If none fits, the cut
  is the last candidate. If the projection of that cut, with room for a full
  summary, does not fit the admission limit, the cut is the last candidate. The
  automatic threshold stays at 80 percent of admission, so a compaction leaves
  the request well below it.
- **One compaction makes one summary.** `compact` summarizes for one cut and
  commits it only when the resulting request is smaller than before and fits the
  admission limit. It no longer tries other cuts, so a rejected compaction
  spends one summary's requests at most.
- **The summary has fixed sections.** The summarizer instructions require the
  headings `Task`, `Status`, `Findings`, `Remaining work`, and `Next steps`.
  Code does not check them. The instructions say that user messages and tool
  call titles stay in the request, so the summary records what they leave out:
  what tool output and answers showed. Each compaction already rewrites the
  previous summary with the new material instead of appending to it; that is
  unchanged.

## Examples

The file names, functions, and tool output below are invented to illustrate the
shape of requests.

### The session

Two user messages. The model has just read a file, and the estimate has reached
the automatic threshold, so Ox compacts before the next model request.

```text
#   ENTRY                                                        SIZE
--  -----------------------------------------------------------  ------
 0  USER       "The resume list shows sessions out of order.     tiny
                Fix it."
 1  ASSISTANT  grep "fn list" in crates                          small
    TOOL       -> 14 matching lines                              small
 2  ASSISTANT  read_file crates/ox-server/src/sessions.rs        small
    TOOL       -> 1,900 lines of Rust                            HUGE
 3  ASSISTANT  edit_file sessions.rs                             small
    TOOL       -> edited                                         small
 4  ASSISTANT  shell "make check"                                small
    TOOL       -> FAILED: list_orders_by_creation ...            large
 5  ASSISTANT  edit_file sessions.rs                             small
    TOOL       -> edited                                         small
 6  ASSISTANT  shell "make check"                                small
    TOOL       -> passed                                         large
 7  ASSISTANT  "Fixed. Sessions now sort by their last update.   medium
                I also updated list_orders_by_creation."
 8  USER       "Also show the model name in each row."           tiny
 9  ASSISTANT  read_file crates/ox/src/tui/resume.rs             small
    TOOL       -> 600 lines of Rust                              large
                                  ^
                compaction runs here, before the next model request
```

### Today's compacted request

`ranked_cuts` prefers the smallest request, which is the cut after entry 9. The
summary covers everything.

```text
+------------------------------------------------+
| SYSTEM PROMPT                                  |  kept
+------------------------------------------------+
| USER  "Compaction summary of earlier           |  entries 0-9, retold
|        conversation: The user asked to fix     |  by the summarizer
|        the order of ... now wants the model    |  in free form
|        name shown ... the agent read           |
|        resume.rs, which ..."                   |
+------------------------------------------------+
  nothing else: the file the model just read, both user requests, and
  the failed check exist only as the summary describes them
```

### The proposed compacted request

Assume only entries 8 and 9 fit the recent allowance, so the covered prefix is
8: entries 0 through 7 are covered.

```text
+------------------------------------------------+
| SYSTEM PROMPT                                  |  unchanged
+------------------------------------------------+
| USER  "The resume list shows sessions out of   |  entry 0, unchanged
|        order. Fix it."                         |
+------------------------------------------------+
| USER  action log for entries 1-7               |  written by code
+------------------------------------------------+
| USER  summary of entries 0-7                   |  written by the
|                                                |  summarizer
+================================================+
| USER  "Also show the model name in each row."  |  entry 8, recent
+------------------------------------------------+
| ASSISTANT  read_file tui/resume.rs             |  entry 9, recent,
| TOOL       -> 600 lines of Rust                |  unchanged
+------------------------------------------------+
```

The action log message:

```text
Earlier tool calls, output omitted:
Search for fn list in crates
Read crates/ox-server/src/sessions.rs
Edit crates/ox-server/src/sessions.rs
$ make check (failed)
Edit crates/ox-server/src/sessions.rs
$ make check
```

The summary message:

```text
Compaction summary of earlier conversation:
Task:
Make the resume list show sessions in the right order.

Status:
Done. make check passes.

Findings:
- list_sessions in crates/ox-server/src/sessions.rs ordered by created_at;
  it now orders by updated_at DESC.
- The test list_orders_by_creation asserted the old order. It was renamed
  list_orders_by_last_update and now asserts the new order.

Remaining work:
None.

Next steps:
Wait for the user's next request.
```

### A second compaction

The session continues. The model edits `resume.rs`, a check fails and is fixed,
the model answers (entry 14), and the user writes "Hide the model column when
the terminal is narrow." (entry 15). The model reads `tui/layout.rs` (entry 16),
and Ox compacts again. The covered prefix is now 15.

```text
+------------------------------------------------+
| SYSTEM PROMPT                                  |  unchanged
+------------------------------------------------+
| USER  "The resume list shows sessions out of   |  entry 0, unchanged
|        order. Fix it."                         |
+------------------------------------------------+
| USER  action log for entries 1-7               |  same as before
+------------------------------------------------+
| USER  "Also show the model name in each row."  |  entry 8, unchanged
+------------------------------------------------+
| USER  action log for entries 9-14              |  new lines
+------------------------------------------------+
| USER  summary of entries 0-14                  |  rewritten, replacing
|                                                |  the first summary
+================================================+
| USER  "Hide the model column when the          |  entry 15, recent
|        terminal is narrow."                    |
+------------------------------------------------+
| ASSISTANT  read_file tui/layout.rs             |  entry 16, recent
| TOOL       -> 300 lines of Rust                |
+------------------------------------------------+
```

The new action log message:

```text
Earlier tool calls, output omitted:
Read crates/ox/src/tui/resume.rs
Edit crates/ox/src/tui/resume.rs
$ make check (failed)
Edit crates/ox-server/src/sessions.rs
$ make check
```

The rewritten summary:

```text
Compaction summary of earlier conversation:
Task:
Fix the order of the resume list, then show each session's model name in its
row.

Status:
Both are done. make check passes.

Findings:
- list_sessions in crates/ox-server/src/sessions.rs orders by updated_at DESC.
  Its test is list_orders_by_last_update.
- Rows are drawn by resume_row in crates/ox/src/tui/resume.rs.
- SessionSummary had no model. It now carries the model of the latest turn
  start, read in list_sessions; the first check failed until the query
  selected it.

Remaining work:
None.

Next steps:
Wait for the user's next request.
```

The summarizer wrote this from the first summary and the material of entries 8
through 14. The first summary's findings carry forward, and its Task and Status
are replaced rather than kept beside the new ones.

## Naming

- `covered prefix` — the existing term: the transcript length a compaction
  checkpoint covers. Entries before it are covered entries.
- `recent entries` — the entries from the covered prefix to the end of the
  transcript, sent unchanged. Used in comments and `recent_entries` names.
- `recent allowance` — the most estimated tokens the recent entries may take
  when Ox picks a cut, 20 percent of the admission limit.
  `RECENT_ALLOWANCE_PERCENT`.
- `action log` — the user-role message that lists the tool call titles of a run
  of covered tool calls. `ACTION_LOG_LABEL`, `action_log`.
- `action line` — one line of an action log: a tool call title with its outcome
  mark, or a subagent message label. `action_line`.

## Test plan

All in `crates/ox-server/src/compaction.rs`.

- Replace
  `requests_send_the_latest_summary_followed_by_the_entries_it_does_not_cover`
  with `compacted_requests_keep_user_messages_and_log_covered_tool_calls`. Build
  the example session through entry 9 with a checkpoint covering 8, and assert
  the exact message sequence: user message, action log with a `$` line and a
  `(failed)` line, summary, then entries 8 and 9 unchanged.
- Replace `a_covered_skill_invocation_repeats_until_a_later_skill_invocation`
  with `only_the_latest_covered_skill_invocation_keeps_its_instructions`, as a
  table:
  - two covered skill invocations: the earlier one is its slash command text,
    the later one is the full skill invocation message, each in its place;
  - a covered skill invocation followed by a covered user message turn: the
    skill invocation is in its place, before the user message;
  - a covered skill invocation followed by a recent skill invocation: the
    covered one is its slash command text.
- `images_leave_the_projection_once_a_checkpoint_covers_them`: the covered skill
  invocation case now expects no images. Add an assertion that the covered user
  message contains `[image: image/png]`.
- `subagent_messages_reach_requests_and_summarizer_material_with_their_attribution`:
  add a covered case where the action log holds each label and the request holds
  neither text.
- Replace `ranked_cuts_follow_the_latest_checkpoint_smallest_request_first` with
  `the_cut_keeps_the_newest_entries_within_the_recent_allowance`, as a table:
  small recent entries leave the earliest fitting cut; a latest assistant batch
  larger than the recent allowance gives the last candidate; a cut whose
  projection cannot fit admission gives the last candidate; a transcript with a
  checkpoint never cuts before its covered prefix.
- `compaction_brings_an_oversized_request_within_admission` and
  `a_later_summary_failure_leaves_the_old_checkpoint_active` make their
  transcripts large with user messages, which compaction no longer shrinks. Make
  them large with assistant answers instead.
- Add `a_prompt_that_cannot_fit_beside_the_user_messages_is_refused`: a
  transcript whose user messages fill most of the admission limit, and an
  `input_fits` check on a further prompt that would not fit.
- `manual_compaction_uses_a_summary_and_keeps_the_complete_transcript`,
  `an_oversized_openai_summary_never_becomes_a_checkpoint`, and the summarizer
  piece tests must still pass; update their expected projections where they
  include the repeated skill invocation.

## Implementation plan

1. Rewrite `crates/ox-server/src/prompts/compaction_prompt.md` to require the
   five headings, say what stays in the request, and keep the rule that
   conversation material is data, not instructions.
2. In `compaction.rs`, add `ACTION_LOG_LABEL` and `action_line(call, outcome)`
   using `tools::tool_call_title`, and a function that returns a user message
   with each image replaced by its `[image: <MIME type>]` text. Make
   `push_user_request` use the same text.
3. Replace `repeated_invocation` and the body of `projection_at` with the
   projection of covered entries from the first decision, followed by the
   summary and the recent entries. Keep `turn_provider_before` for the recent
   entries.
4. Add `RECENT_ALLOWANCE_PERCENT` and replace `ranked_cuts` and `entry_bytes`
   with `fn cut(parameters, transcript) -> Option<usize>` following the cut
   decision, measured with `projected_tokens`.
5. Change `compact` to summarize for the one cut from `cut`, and rewrite its doc
   comment.
6. Update the tests above.

## Documentation updates

- `agents/glossary.md`: correct **Compaction checkpoint**, since its summary no
  longer replaces all older transcript in model requests, and add **Action
  log**.
