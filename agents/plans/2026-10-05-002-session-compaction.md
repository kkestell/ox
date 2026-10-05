# Plan: compact the session at the compaction trigger

## Goal

When `Turn.needsCompaction` reports that the latest response used 80% of the
model's context limit, the next model request should no longer carry the whole
transcript. The turn asks the model for a summary of the session so far, saves
it as a compaction entry at the end of the transcript, and sends only the
summary as the earlier history from then on.

The transcript stays append-only. The database and replay keep the full history;
model requests read the transcript from the latest compaction onward.

## Related code

- `internal/agent/agent.go:257` — the TODO in `Turn.loop` where compaction runs.
- `internal/agent/agent.go:281` — `needsCompaction` reads the latest response's
  input tokens.
- `internal/agent/agent.go:183`, `:460` — `Start` rejects a text-only model when
  `hasImages` finds an image in the saved transcript.
- `internal/agent/agent.go` — `requestWithRetries` and `complete` make one model
  request and forward its text and reasoning as `TextDelta` and
  `ReasoningDelta`.
- `internal/transcript/transcript.go` — `Entry`, `Validate`, `UsageSummary`,
  `Encode`, `Decode`, and `SkillInvocation.Message`, whose wrapped user message
  is the pattern for the compaction's model message.
- `internal/store/store.go` — `AppendBatch` and `AppendTurnError` show how an
  entry is appended.
- `internal/openrouter/messages.go:43` — `chatMessages` encodes the transcript
  as chat messages.
- `internal/server/convert.go` — `update` converts turn events, and `replay`
  sends a saved transcript. A saved turn error is already shown as a tool call
  with no tool name, which is how the compaction is shown.
- `agents/experiments/compaction/compact.py` — the summary prompt this plan
  reuses.

## How the session looks

In this example, the trigger fires during the third turn, after a response that
made tool calls.

**Database (`transcript_entries` for one session)**

```
id  kind             data
1   turn_start       user 1 (+ model, effort, mode)
2   assistant_batch  assistant 1: tool calls + their outcomes
3   assistant_batch  assistant 2: answer, no tool calls
4   turn_start       user 2
5   assistant_batch  assistant 3: tool calls + their outcomes
6   assistant_batch  assistant 4: answer
7   turn_start       user 3
8   assistant_batch  assistant 5: tool calls + their outcomes
9   compaction       summary of entries 1–8 + usage of the summary request
10  assistant_batch  assistant 6: tool calls + their outcomes
11  assistant_batch  assistant 7: answer
```

**The model request just before compaction (the one compaction replaces)**

```
tools:     tool definitions            (rebuilt for every request)
messages:
  system     system prompt             (rebuilt for every request)
  user       user 1
  assistant  assistant 1 (text, tool calls)
  tool       result of assistant 1's call A
  tool       result of assistant 1's call B
  assistant  assistant 2
  user       user 2
  assistant  assistant 3 (tool calls)
  tool       result of assistant 3's call
  assistant  assistant 4
  user       user 3
  assistant  assistant 5 (tool calls)
  tool       result of assistant 5's call
```

**The summary request**

```
tools:     tool definitions
messages:
  ...        the same messages as above
  user       compaction prompt         (never saved)
```

**The model request right after compaction (produces assistant 6)**

```
tools:     tool definitions
messages:
  system     system prompt
  user       compaction summary
```

**The next request (produces assistant 7)**

```
tools:     tool definitions
messages:
  system     system prompt
  user       compaction summary
  assistant  assistant 6 (tool calls)
  tool       result of assistant 6's call
```

**ACP updates while the turn runs**

```
tool_call              "Context compaction", in progress
tool_call_update       completed, content = summary
usage_update           context used 0 until the next response; total cost
```

**ACP updates when the session is loaded (replay)**

```
user_message_chunk     user 1
agent_thought_chunk    assistant 1 reasoning   (only if it has any)
agent_message_chunk    assistant 1 text        (only if it has any)
tool_call              assistant 1 call A, completed, with its outcome
tool_call              assistant 1 call B, completed, with its outcome
agent_message_chunk    assistant 2 text
user_message_chunk     user 2
...                    assistants 3 and 4, same pattern
user_message_chunk     user 3
...                    assistant 5, same pattern
tool_call              "Context compaction", completed, content = summary
...                    assistants 6 and 7, same pattern
usage_update           context used (from assistant 7) and total cost
available_commands_update
```

## Decisions

- **One rule decides which entries a function reads.** What the model sees reads
  from the latest compaction onward: `chatMessages`, `needsCompaction`, the
  context tokens in `UsageSummary`, and the image check in `Start`. What
  happened in the session reads every entry: `replay`, the cost in
  `UsageSummary`, `LatestTurnStart`, and the session title.
  `transcript.SinceCompaction(entries)` returns the entries from the latest
  compaction onward, or all of them when there is none.
- **A compaction entry covers every entry before it.** It is saved where the
  trigger fired, which may be in the middle of a turn. No saved row is changed
  or deleted, so the schema does not change; `compaction` is a new
  `transcript_entries.kind`.
- **The compaction entry is `transcript.Compaction`** with `Summary string` and
  `Usage *Usage`. `Usage` is the summary request's reported usage, so its cost
  counts toward the session's cost. `Validate` rejects an empty summary.
- **The model reads the summary as a user message.** `Compaction.Message()`
  returns a `UserMessage` that says the earlier part of the session was replaced
  by the summary that follows, then the summary, the same way
  `SkillInvocation.Message` wraps skill instructions. `chatMessages` starts from
  `SinceCompaction` and sends that message. Continuation metadata of earlier
  responses is no longer sent.
- **The summary comes from the session's model and effort.** The summary request
  is the turn's request with one more user message: the prompt from
  `agents/experiments/compaction/compact.py`, held as `compactionPrompt` in
  `internal/agent`. It is appended as a `TurnStart` that exists only in that
  request and is never saved, so `chatMessages` needs no other change. The
  request keeps the same tools so the earlier tool calls encode as usual. The
  summary is the completion's text; tool calls in that completion are ignored.
- **The summary request is not streamed to the client.** `requestWithRetries`
  and `complete` take the request and a `forward bool`; the summary request
  passes false, so no `TextDelta` or `ReasoningDelta` is sent. Temporary
  failures are retried like any model request.
- **A summary fails unless the request finishes with nonempty text.** A token
  limit, a refusal, or empty text is a failure with the reason "compacting the
  session failed". Like any failure, it ends the turn and is saved as its turn
  error. Nothing is saved for a failed or cancelled compaction, so the next
  request checks again.
- **The client sees compaction as a tool call with no tool name.** Two new turn
  events: `CompactionStarted{ID}` becomes a `tool_call` titled "Context
  compaction" with status `in_progress`, and `CompactionFinished{ID, Outcome}`
  becomes a `tool_call_update` built with the existing `status` and
  `outputContent`. The outcome is `Completed(summary)`, `Failed(reason)`, or
  `Cancelled(...)`. The ID is `"compaction-" + rand.Text()`, as turn errors do.
  `replay` shows a saved compaction as the same tool call, completed, at its
  position. The terminal client needs no change.
- **After a compaction, the context tokens are 0 until the next response.** The
  summary request's input tokens describe the old context, and Ox does not
  estimate request size. `UsageSummary` sets the context tokens to 0 at a
  compaction and adds its cost; `needsCompaction` returns false when a
  compaction is newer than the latest response. The turn sends a `Usage` event
  after saving the compaction.

## Implementation plan

1. In `internal/transcript/transcript.go`, add `Compaction`, its `entry` method,
   `Validate`, and `Message`; handle it in `Validate`, `Encode`, and `Decode`
   with the kind `compaction`. Add `SinceCompaction`. Update `UsageSummary`.
2. In `internal/store/store.go`, add `AppendCompaction`, which validates the
   entry and appends it like `AppendBatch`.
3. In `internal/openrouter/messages.go`, make `chatMessages` encode
   `SinceCompaction(entries)` and send a `Compaction` as a user message with
   `Compaction.Message()`. Update the `Request.Transcript` comment.
4. In `internal/agent/agent.go`:
   - Add `compactionPrompt`, `CompactionStarted`, and `CompactionFinished`.
   - Give `requestWithRetries` and `complete` the request and `forward`
     parameters.
   - Add `Turn.compact`, which sends `CompactionStarted`, makes the summary
     request, saves the compaction with `AppendCompaction`, appends it to
     `t.request.Transcript`, and sends `CompactionFinished` and `Usage`. On a
     failure or cancellation it sends `CompactionFinished` with a failed or
     cancelled outcome and returns the error.
   - Replace the TODO in `Turn.loop` with a call to `compact`.
   - Make `needsCompaction` stop at a compaction, and make `Start` check images
     in `SinceCompaction(session.Transcript)`.
5. In `internal/server/convert.go`, convert `CompactionStarted` and
   `CompactionFinished` in `update`, and show a `Compaction` in `replay`.

## Test plan

- `internal/transcript`: a compaction encodes and decodes unchanged, and an
  empty summary fails validation. `SinceCompaction` returns every entry without
  a compaction and the entries from the latest one when there are two.
  `UsageSummary` reports 0 context tokens after a compaction, the latest
  response's tokens after a later response, and a cost that includes the
  compaction's.
- `internal/store`: `AppendCompaction` rejects an empty summary, updates
  activity, and the compaction reads back in position after reopening.
- `internal/openrouter`: a request whose transcript has a compaction sends the
  system prompt, the summary as one user message, and only the entries after it;
  continuation metadata from before the compaction is not sent.
- `internal/agent`:
  - When the latest response crosses the threshold, the next request is the
    summary request, the compaction is saved after that response, and the
    request after it carries only the summary and later entries.
  - The events are `CompactionStarted`, `CompactionFinished` with the summary,
    then `Usage`, and no `TextDelta` comes from the summary request.
  - After a compaction, `needsCompaction` is false until a new response crosses
    the threshold.
  - A summary that ends at the token limit or is empty saves a turn error and no
    compaction, and `CompactionFinished` reports the failure.
  - Cancelling during the summary request saves nothing and ends the turn as
    cancelled.
  - An image only before the latest compaction does not reject a text-only
    model.
- `internal/server`: a replayed session shows the compaction as a completed
  "Context compaction" tool call between the responses around it, and the usage
  update counts the compaction's cost.
