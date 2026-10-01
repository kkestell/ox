# Retry temporary model request failures

## Plan

`agents/plans/2026-10-01-005-retry-temporary-model-request-failures.md`

## Summary

A model request that stalls or returns HTTP 429 or 5xx is now retried for both
model providers, after 2 and then 8 seconds, up to three attempts. Other
failures still end the turn on the first attempt. The plan's goal is met.

## Decisions

- The OpenAI test's stall uses a bare event prefix instead of the fixture's
  `sse`, because `sse` ends with `[DONE]`, which fails the stream instead of
  stalling it.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed.

