# Count only busy subagents

## Goal

OX-0036: the subagent limit counts idle subagents, so the main agent must stop
subagents whose answers it already has before it can start new ones. When this
work is done, the limit counts only subagents with a running task, and idle
subagents stay available for follow-up messages without blocking new work.

OX-0032 and OX-0042 are closed as won't fix. Model request retries already cover
the failure that caused OX-0032. For OX-0042, the main agent can inspect the
workspace itself after a subagent fails, and that check also sees changes made
through the shell.

## Related code

- `crates/ox-server/src/subagents.rs` — `MAX_SUBAGENTS`, `Subagents::start`,
  `Subagents::send`, and the test
  `at_most_four_subagents_start_including_idle_ones`.
- `crates/ox-server/src/tools/subagent.rs` — `start_schema` and `send_schema`
  describe the limit to the model.

## Decisions

- **Busy and stopping subagents count toward the limit.** Both have a running
  task. An idle subagent's task has ended and its conversation is saved, so any
  number of idle subagents may remain.
- **A follow-up message to an idle subagent is refused at the limit.** `send`
  returns an error when four subagents are already busy or stopping, instead of
  queuing the message until one finishes. A message to a busy subagent is still
  queued, because it does not add a running task.

## Naming

- `busy subagent` — a subagent whose task is running a turn (`Status::Busy`).
- `idle subagent` — a subagent whose turn ended with a final answer and that
  waits for a follow-up message (`Status::Idle`).

## Test plan

- Replace `at_most_four_subagents_start_including_idle_ones` with
  `at_most_four_subagents_are_busy_at_once`. Start one subagent and let it
  finish so it is idle, then start four whose replies are held by a `Gate`.
  Assert that a fifth `start` fails with the limit message and the states of all
  five, and that `send` to the idle subagent fails. Open the gate, let them
  settle, and assert that a new `start` succeeds while five subagents are idle
  and that `send` to an idle subagent starts its turn.
- Inspect the complete test diff as required by `agents/testing.md` and record
  in the work log which guarantees changed or moved.

## Implementation plan

1. In `subagents.rs`, add a `busy` count of subagents that are not idle. Check
   it in `start` and in the idle branch of `send` against `MAX_SUBAGENTS`, with
   errors that tell the main agent to wait for a subagent to finish or stop one
   with `stop_subagent`, followed by `describe`. Update the `MAX_SUBAGENTS`
   comment to say idle subagents do not count.
2. In `tools/subagent.rs`, change the `start_subagent` description to say at
   most four subagents can be busy at once and idle ones stay for follow-up
   messages until stopped. Add to the `send_message` description that a message
   to an idle subagent fails while four are busy.
3. Replace the test above.
4. In `agents/issues.csv`, set OX-0036 to `fixed` and OX-0032 and OX-0042 to
   `wontfix`. In `agents/todo.md`, check off all three, marking OX-0032 and
   OX-0042 as won't fix, and check off the "Subagent lifecycle (plan)" item.
