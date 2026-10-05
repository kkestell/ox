# Work log: compaction trigger

## Plan

`agents/plans/2026-10-05-001-compaction-trigger.md`

## Checks run

- `go test ./internal/agent -run '^TestCompaction' -count=1` — Passed.
- `make check` — Passed.
- `git diff --check` — Passed.
- `make check-docs` — Failed on formatting in the unrelated, concurrently added
  `agents/plans/2026-10-05-002-hidden-tool-loop-transcript.md`. The work log's
  initial formatting failure was corrected.
- Focused `dprint check` on this plan and work log — Passed.
- `make e2e` — Not run; terminal behavior was not changed.
- Uncapped live tests initially failed on provider output reservations:
  Perceptron rejected 31,930 input tokens plus its default 8,192 output tokens
  against a 36,864-token context. Granite's provider rejected 68,420 input
  tokens plus 62,653 output tokens against a 131,072-token context.

## Manual verification

Ran two live scenarios against `perceptron/perceptron-mk1.5`, each with two
requests. A temporary Go test overlay exercised the real OpenRouter client,
stream parser, agent loop, and session store. Each second turn used a newly
constructed agent and the existing saved session. The harness set
`max_tokens=32` on live requests to avoid provider output defaults; production
request construction was not changed.

The overlay and runner are retained outside the checkout. The runner loads
`OPENROUTER_API_KEY` from the environment or the repository's `.env`:

```sh
python3 /var/folders/x3/s0dy9w311sncwf87160crpw80000gn/T/ox-compaction-live-oyucydu0/run.py
```

| Scenario      | Reported input tokens by turn | Compaction decision |
| ------------- | ----------------------------- | ------------------- |
| Short prompt  | 2,070; 2,090                  | False; false        |
| Padded prompt | 31,930; 31,950                | True; true          |

The padded prompt repeats a space followed by `a` 29,859 times. All four
requests returned `OK`. The model's actual catalog context limit was 36,864,
making the threshold 29,492. The second turns checked the saved usage before
making their next live request.

## Follow-up work

- Implement compaction at the existing TODO. This change only establishes its
  decision and position before the next model request.
- Account for provider output reservations when implementing compaction. A fixed
  20% reserve can be smaller than the provider's default output allowance,
  causing rejection before the 80% input threshold is reached.
