# Plan: trigger compaction before the next model request

## Goal

Add a local compaction decision in the server's model loop. Compaction is due
when the latest response's reported input tokens reach 80% of the selected
model's context limit. Establish the decision and its place in the loop;
implementing compaction itself is outside this change.

## Related code

- `internal/agent/agent.go` — `Turn.loop` makes model requests, runs tools, and
  saves each response through `processBatch`. `Start` loads the existing
  transcript for every new turn, including resumed sessions.
- `internal/openrouter/stream.go` — Reads the final usage chunk before returning
  the completion, exposing `prompt_tokens` as `Usage.InputTokens`.
- `internal/transcript/transcript.go` — Each saved `AssistantBatch` already
  retains its response's usage. `UsageSummary` combines input and output tokens
  for status reporting, so it is unsuitable for this decision.

## Decisions

- Put the check immediately before `requestWithRetries` in `Turn.loop`, after
  the existing cancellation check. Read the latest saved response from
  `t.request.Transcript`. After a response containing tool calls, `processBatch`
  saves that response and its tool outcomes before the next loop iteration.
- A final answer needs no further model request in that turn. The next user
  prompt, including after resume, checks the saved response before its first
  request. This requires no additional state or transcript helper.
- Add a private `Turn.needsCompaction() bool` method. Scan backward to the most
  recent `AssistantBatch` and use only its `Message.Usage.InputTokens`. An empty
  transcript, missing usage on that response, or a nonpositive context limit
  returns false. Do not substitute an older response when the latest lacks
  usage.
- Compare against the current model's limit. Use integer arithmetic with the
  threshold `limit - limit/5`, which rounds 80% up without multiplication
  overflow. Trigger at or above that threshold. Cached tokens are already
  included in input tokens; output and reasoning tokens do not enter the
  calculation.
- The true branch is an explicit TODO for future compaction and currently
  continues normally:

  ```go
  if t.needsCompaction() {
      // TODO: compact the transcript before the next model request.
  }
  completion, err := t.requestWithRetries(ctx)
  ```

  This establishes the integration point without an event, callback, stored
  flag, or client change.
- Reported input tokens describe the previous request. The 20% reserve leaves
  room for growth, but this check does not measure newly added messages or tool
  output. Until compaction is implemented, crossing the threshold has no runtime
  effect and cannot prevent context overflow.

## Implementation plan

1. In `internal/agent/agent.go`, add `Turn.needsCompaction` using existing
   transcript entries and the selected model's context limit. Add the local
   decision branch before the next model request in `Turn.loop`.
2. In `internal/agent/agent_test.go`, cover the decision using the existing
   fixture and saved response usage. Keep test observations inside the agent;
   the placeholder needs no test callback or notification.

## Test plan

- For a context limit of 1,000, input counts of 799, 800, and 801 return false,
  true, and true. For a limit of 101, 80 returns false and 81 returns true.
- Large output or reasoning counts do not trigger compaction when input is below
  the threshold. Cached input is counted once.
- Use the latest assistant response even when newer turn starts or errors follow
  it. Missing usage on that response returns false rather than using an earlier
  count. Empty transcripts and nonpositive limits return false.
- A new turn reads saved response usage and applies its selected model's context
  limit without restoring a separate flag.
- After a response containing tool calls, the saved batch supplies the next
  compaction decision. Existing turn tests continue to cover tool execution,
  answer completion, usage reporting, cancellation, and context overflow.
