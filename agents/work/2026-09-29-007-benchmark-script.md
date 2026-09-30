# Benchmark script

## Plan

`agents/plans/2026-09-29-007-benchmark-script.md`

## Summary

`ModelUsage` records cached and reasoning tokens from OpenRouter's usage
details. `scripts/bench.py run` builds a commit or the checkout, runs the eight
tasks in `scripts/bench/tasks.toml`, checks each, and writes a `result.json` per
repetition from the run's private `ox.db`. `scripts/bench.py compare` prints
per-task and total tables. The plan's goal is met.

## Departures from the plan

- The `tinyexpr-precedence` check runs `make -B smoke` instead of `make smoke`.
  The `smoke` target runs the test binary only when it rebuilds, so after Ox has
  already built it, `make smoke` exits 0 without running the tests.

## Decisions

- `--reps` defaults to 3.
- The dirty flag ignores untracked files, since they reach the build only
  through a tracked change, and untracked plans are common in this workflow.
- `compare` reads missing `cached_tokens` and `reasoning_tokens` as 0, so a
  build from before this change can serve as the base of a comparison.
- `repeated_calls` counts repeats within one session.
- `compare` prints a table of each label's commit, dirty flag, model, effort,
  and result count ahead of the task tables. Its totals table sums the task
  medians.
- The usage test in `openrouter.rs` now sends usage details; the prompt test in
  `acp/prompt.rs`, whose usage chunk has none, owns the 0 case.
- Local `ox.db` files must be recreated, because saved usage lacks the new
  fields.

## Automated checks

- `make check` — passed.
- `make check-docs` — passed.

## Manual verification

1. The smoke benchmark from the test plan, building the working tree.

   ```sh
   scripts/bench.py run --label smoke --reps 1 --task pi-digit \
     --model deepseek/deepseek-v4.1-flash --effort default
   scripts/bench.py compare smoke
   ```

   The run finished and passed with 9 requests, 8 tool calls (6 `shell`, 2
   `apply_patch`, 1 repeated), 67594 input tokens of which 56832 were cached,
   6191 output tokens of which 3781 were reasoning, and $0.0084 in 43 seconds.

2. A build from a ref, compared with the first run.

   ```sh
   scripts/bench.py run --label smoke-ref --ref HEAD --reps 1 --task pi-digit \
     --model deepseek/deepseek-v4.1-flash --effort default
   scripts/bench.py compare smoke smoke-ref
   ```

   The worktree build produced `bench/bin/ox-<hash>` and the worktree was
   removed. The run passed with 4 requests, and `compare` showed both labels
   with percentage changes from the first label. `make check-docs` still passes
   with the task workspaces' `README.md` files under `bench/`.

## Follow-up work

- A check command has no timeout, so a check that hangs stalls its repetition.
