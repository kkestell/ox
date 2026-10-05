# Benchmark on small-c task commits

## Plan

`agents/plans/2026-10-04-004-small-c-benchmark.md`

## Summary

small-c exists at `~/src/small-c` with the commits "Scaffold the Small-C
compiler" (`e930c36`) and "Compile integer expressions" (`4e266f2`, tagged
`task/expressions`). `evals/bench.py` loads tasks from small-c's tags, builds
each workspace from the base tree without history, scores with the task commit's
`tests/`, and gains `run --tests` and `validate`. The plan's goal is met:
`validate` reports `expressions` as valid, and Ox passed it under all three
`--tests` settings.

## Departures from the plan

- `build_image()` and `remove_containers()` were extracted from `build` and
  `run` so that `validate` shares the image build and the interrupt cleanup
  stays one call.
- `start_container` also creates `/workspace`, which both `run` and `validate`
  need.

## Decisions

- `TaskError` carries task problems. `validate` prints them as
  `<id>: invalid: <problem>`; `run` turns them into a usage error.
- `resolve_task` stores the commits, new test files, and new and old test names
  in the task dict once per invocation.
- `validate` writes the `go test` output of each tree to
  `evals/validate/<id>/base.txt` and `solution.txt`, and names the file in its
  failure messages.
- `check.txt` holds the `Output` text of the `go test -json` events, not the raw
  JSON.
- Scoring runs under `timeout 600` (`SCORE_TIMEOUT`).
- The resume check now reads every saved result, not only the first, since it
  also compares each result's `solution`.
- small-c's compiler rejects integer literals with a leading zero and literals
  above 2147483647. The test driver treats an unknown group as a failure.
- New code in `bench.py` follows `ruff format`; six existing hunks that
  `ruff format` would also change were left as they were.

## Checks run

- small-c `make check` — Passed at `e930c36` and at `task/expressions`.
- small-c `go test ./tests -update` — Left every `.out` file unchanged.
- ox `make check` — Passed.
- `ruff check evals/bench.py` — Same findings as before the change, plus
  `PLW1510` on the two `subprocess.run` calls whose exit status is read.

## Manual verification

1. Validated the task.

   ```sh
   python3 evals/bench.py validate
   ```

   `expressions: valid`. In the image, the base failed the five new `programs`
   tests and passed `errors/unbalanced_parens` and both old tests; the task
   commit passed all eight.

2. Checked validation failures with temporary `[[task]]` entries in
   `tasks.toml`: `missing` with no tag, `badexample` tagged at
   `task/expressions` with `examples = ["programs/nope"]`, and `scratch` tagged
   on a commit after `task/expressions` that adds only `tests/errors/scratch.c`.
   The entries, tags, and scratch commit were removed afterwards.

   ```
   missing: invalid: the tag task/missing does not exist in /Users/kyle/src/small-c
   badexample: invalid: the example programs/nope matches no new test
   scratch: invalid: every new test already passes at the base; see .../evals/validate/scratch/base.txt
   ```

   The command exited with status 1.

3. Ran the task once per `--tests` setting.

   ```sh
   for t in visible examples hidden; do
     python3 evals/bench.py run --label smallc-$t-20261004 \
       --model openrouter:deepseek/deepseek-v4.1-flash --effort default \
       --task expressions --reps 1 --tests $t
   done
   ```

   All three passed 6/6 tests with 0 regressions. `start.txt` listed the harness
   plus all eleven new test case files, the four example files, or none.
   `result.json` had `tests`, `solution`, `tests_passed`, `tests_total`, and
   `regressions`; `check.txt` held the `go test` output; `workspace/tests` held
   the agent's own test files (for `hidden`, files such as
   `programs/expression_precedence.c`).

4. Ran the `examples` label again with `--tests hidden`; it was refused with
   "holds results for a different commit, model, effort, tests setting, or
   providers".

5. Compared two labels.

   ```sh
   python3 evals/bench.py compare smallc-examples-20261004 smallc-hidden-20261004
   ```

   The report had a Tests column in the Runs table and `tests_passed`,
   `tests_total`, and `regressions` rows; the SVG and PNG chart rendered. The
   report files were deleted afterwards.

## Follow-up work

- Add the later small-c tasks.
