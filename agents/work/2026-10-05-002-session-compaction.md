# Work log: compact the session at the compaction trigger

## Plan

`agents/plans/2026-10-05-002-session-compaction.md`

## Summary

When the latest response reaches 80% of the context limit, the turn asks the
session's model for a summary and saves it as a `compaction` entry. Later
requests send only the summary and the entries after it. The client sees a
"Context compaction" tool call while the turn runs and on replay. The plan's
goal is met.

## Departures from the plan

- `compactionPrompt` adapts the experiment's prompt to a chat request: it refers
  to "the conversation above" instead of "the text above" and ends with "Do not
  call tools.", because the summary request still lists the tools and only the
  completion's text is used.

## Decisions

- A model request that fails during compaction keeps its existing reason, "the
  model request failed". Only a token limit, a refusal, or empty text uses
  "compacting the session failed".
- When a compaction fails and its `CompactionFinished` update also fails to
  send, the turn ends with the compaction's error, which is the one saved as the
  turn error.

## Checks run

- `make check` — Passed after `make format` rewrapped the plan.
- `make e2e` — Not run; the terminal client was not changed.

## Manual verification

None. No live model request was made; the tests use the scripted OpenRouter
server.

## Follow-up work

- Account for provider output reservations at the trigger, as noted in
  `agents/work/2026-10-05-001-compaction-trigger.md`.
- A live run against a small-context model would show whether summaries keep
  enough of the session to continue the work.
