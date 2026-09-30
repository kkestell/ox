# Benchmark script

## Goal

Measure how a change to Ox affects headless prompt runs. `scripts/bench.py run`
builds one commit of `ox`, runs a fixed set of tasks against it several times
each, checks whether each task was done, and reads the run's transcript from its
own `ox.db` to count model requests, tool calls, failed tool calls, tokens, and
cost. `scripts/bench.py compare` puts two or more labeled runs side by side. Ox
itself changes only to record cached and reasoning token counts, which
OpenRouter already reports.

## Related code

- `scripts/run.py` — the live check the benchmark replaces for this purpose: API
  key from `.env`, temporary workspace, optional pinned repository commit,
  `ox run --dir`. Its prompts are the seed of the task set. It stays as is.
- `crates/ox-server/src/acp.rs:856` `run_headless` — opens `ox.db` at
  `sessions::database_path()`, so `OX_DATA_DIR` gives each benchmark run a
  private database. Runs in Auto mode. Skills are not invoked.
- `crates/ox-server/src/lib.rs:83` `run` — reads the global settings file for
  the default model and effort; `--model` and `--effort` override both.
- `crates/ox-server/src/sessions.rs:31` — the schema. `sessions` has
  `parent_session_id` for child sessions; `transcript_entries` has `kind` and
  JSON `data`.
- `crates/ox-server/src/sessions.rs:1007` `encode_entry` — the `kind` values:
  `turn_start`, `assistant_batch`, `compaction_checkpoint`, `subagent_messages`.
- `crates/ox-server/src/sessions.rs:397` `AssistantMessage`, `ToolCall`,
  `ToolOutcome`, `ToolStatus`, `ModelUsage`, `AssistantBatch` — the JSON of an
  assistant batch: `message.tool_calls[]` (`name`, `arguments`), `message.usage`
  (`input_tokens`, `output_tokens`, `cost`), and `outcomes[]` (`status`,
  `text`).
- `crates/ox-server/src/sessions.rs:258` `CompactionCheckpoint` —
  `summarizer_cost`.
- `crates/ox-server/src/openrouter.rs:835` `ApiUsage` and `:673` — where the
  usage chunk becomes a `ModelUsage`.
- `crates/ox-server/src/openrouter.rs:1246` `fixture::usage` — the usage chunk
  helper the tests use; `:1671` the test that checks the parsed usage.
- `crates/ox-server/src/tools.rs:46` — the tool names.
- `Makefile`, `Cargo.toml` `[profile.fast]` — the build the script uses.

## Decisions

- **The script builds a commit, not the working tree.** `bench.py run --ref`
  takes any git ref, adds a detached worktree under `bench/worktree/`, builds
  `ox` there with `cargo build --profile fast -p ox` and
  `CARGO_TARGET_DIR=target/bench` (so builds of different commits share
  dependencies), copies the binary to `bench/bin/ox-<full commit hash>`, and
  removes the worktree. An existing binary for that hash is reused. Without
  `--ref`, the script builds the current checkout the same way and names the
  binary `ox-<label>`. Either way the result records the commit hash and whether
  the tree was dirty.
- **Model and effort are required flags.** The run pins `--model` and `--effort`
  so the global settings file cannot skew a comparison. With both pinned and
  skills never invoked, nothing else under `~/.config/ox` reaches a headless
  run, so `HOME` is left alone and the workspace's tools (`cargo`, `gcc`) find
  their own files.
- **Each run is a directory.** `bench/runs/<label>/<task id>/<rep>/` holds
  `workspace/` (kept after the run, for inspection), `data/` (`ox.db`),
  `answer.txt`, `stderr.txt`, `check.txt`, and `result.json`. `bench/` is
  ignored by git. There is no separate results file: `compare` reads every
  `result.json` under a label.
- **Every task has a check command.** A task's `check` runs with `sh -c` in the
  workspace after Ox exits, regardless of how Ox exited. Exit 0 means the task
  passed. Fewer tool calls mean nothing when the task failed, so pass rate heads
  every comparison.
- **A run has a timeout.** Ox has no limit on model requests, so the script
  kills a run after `--timeout` seconds (default 900): `SIGTERM`, which lets Ox
  cancel and save, then `SIGKILL` after 30 more seconds. A timed-out run is
  recorded with status `timeout` and its check still runs.
- **Runs are concurrent.** Each run has its own workspace and database, so
  `--jobs` runs (default 4) proceed at once through a thread pool. The build
  happens once, before any run starts.
- **Metrics come from `ox.db`, summed over the main session and its child
  sessions.** The script reads the database with `sqlite3` and `json` from the
  standard library after Ox exits. Python 3.14 supplies `tomllib` for the task
  file too, so the script needs no dependencies.
- **Model requests are the unit of work, not turns.** A headless run is one
  turn, so the count that matters is assistant batches.
- **Ox records cached and reasoning tokens.** `ModelUsage` gains `cached_tokens`
  and `reasoning_tokens`, filled from OpenRouter's
  `prompt_tokens_details.cached_tokens` and
  `completion_tokens_details.reasoning_tokens`, each 0 when the chunk omits it.
  Cache hits dominate the cost of a long run, so a comparison without them
  misattributes cost changes. This is the only production code change. Existing
  `ox.db` files must be recreated.
- **Compare shows medians.** For each task and metric, `compare` prints the
  median and the min–max range across repetitions for each label, and the
  percentage change of the median from the first label to each later one. A
  totals table across all tasks follows. Pass rate is shown as `k/n`.
- **The task set is the eight prompts from `run.py`, made checkable.** Each
  prompt names its files and commands so a check can find them. The
  `apply_patch` stress prompt is left out.

## Naming

- `benchmark run` — one execution of `bench.py run`: one built binary, one
  label, every task repeated `--reps` times.
- `label` — the name the user gives a benchmark run, and its directory under
  `bench/runs/`.
- `task` — one entry in `scripts/bench/tasks.toml`: `id`, `prompt`, `check`, and
  optional `repo` (a GitHub commit URL, as in `run.py`).
- `check command` — a task's `check`: a shell command that exits 0 when the task
  was done.
- `repetition` — one run of one task within a benchmark run, numbered from 1.
- `result` — the `result.json` of one repetition.
- `passed` — whether the check command exited 0.
- `status` — how Ox exited: `finished` (exit 0), `failed` (nonzero), or
  `timeout`.
- Model request, assistant batch, tool outcome, child session, subagent,
  compaction checkpoint, and session cost as defined in `agents/glossary.md`.

Metrics in a result and in `compare`, in this order:

- `passed`, `status`, `seconds`
- `requests` — assistant batches
- `tool_calls`, `failed_calls`, `cancelled_calls`
- `calls_by_tool` — `{name: {"calls": n, "failed": n}}`
- `repeated_calls` — calls whose name and arguments match an earlier call
- `input_tokens`, `cached_tokens`, `output_tokens`, `reasoning_tokens`, `cost`
- `max_input_tokens` — the largest single request's `input_tokens`
- `tool_output_chars` — summed length of outcome text
- `compactions`, `summarizer_cost`
- `subagents` — child sessions

Every result also records `label`, `task`, `rep`, `commit`, `dirty`, `model`,
`effort`, and `started_at`.

## Test plan

- `crates/ox-server/src/openrouter.rs` — the usage test at `:1671` sends a usage
  chunk with `prompt_tokens_details.cached_tokens` and
  `completion_tokens_details.reasoning_tokens` and expects them in `ModelUsage`.
  A second case, or the existing chunk without the details, expects 0 for both.
  No other guarantee changes; the other `ModelUsage` literals in tests gain the
  two fields.
- The script has no automated tests. After writing it, run
  `scripts/bench.py run --label smoke --reps 1 --task pi-digit --model <model>
  --effort <effort>`
  and `scripts/bench.py compare smoke`, and report the table.

## Implementation plan

1. `crates/ox-server/src/openrouter.rs`: add `prompt_tokens_details` and
   `completion_tokens_details` to `ApiUsage` as optional nested structs with
   optional `cached_tokens` and `reasoning_tokens`. Fill the two new
   `ModelUsage` fields at `:673`. Extend the usage test.
2. `crates/ox-server/src/sessions.rs`: add `cached_tokens: u64` and
   `reasoning_tokens: u64` to `ModelUsage`. Update every literal in
   `sessions.rs`, `acp.rs`, `acp/prompt.rs`, and `subagents.rs` tests.
3. `.gitignore`: add `/bench/`.
4. `scripts/bench/tasks.toml`: the eight tasks.
   - `pi-digit`: C program in `pi.c`, Makefile default target builds `pi` with
     strict warnings, README; prints only the 100th decimal digit of pi. Check:
     `make && test "$(./pi)" = 9`.
   - `todo-cli`: `todo.py` with `add`, `list`, `done`, persisted in `todo.json`;
     `unittest` tests in `test_todo.py`; README. Check:
     `python3 -m unittest -q && python3 todo.py add "buy milk" && python3
     todo.py list | grep -q "buy milk"`.
   - `coffee-site`: `index.html`, `styles.css`, `logo.svg`; start a local HTTP
     server in the background, fetch every file with `curl`, stop the server.
     Check: the three files exist and `index.html` references the other two.
   - `csv-stats`: `csvstats.c` reads a CSV path argument of integer rows and
     prints count, min, max, mean, median; exits 1 on malformed input;
     `valid.csv`, `malformed.csv`, Makefile building `csvstats`, README. Check:
     `make && ./csvstats valid.csv | grep -qi median && ! ./csvstats
     malformed.csv`.
   - `temp-cli`: a Cargo package `temp` where `temp <value> <C|F>` prints the
     other unit, validates arguments, has unit tests and a README. Check:
     `cargo test -q && cargo run -q -- 100 C | grep -q 212 && ! cargo run -q
     -- abc C`.
   - `tinyexpr-precedence` (`codeplea/tinyexpr` at `c3b2f32`): add an operator
     precedence regression test to `smoke.c`, run `make smoke`. Check:
     `make smoke && ! git diff --quiet -- smoke.c`.
   - `itoa-boundaries` (`dtolnay/itoa` at `1577ed9`): add signed boundary tests
     to `tests/test.rs`, run `cargo test`. Check:
     `cargo test -q && ! git diff --quiet -- tests/test.rs`.
   - `inih-quoted` (`benhoyt/inih` at `577ae2d`): add a quoted-value edge case
     test to `tests/unittest.c` with a matching `.ini` fixture, run
     `tests/unittest.sh` from `tests/`. Check:
     `cd tests && ./unittest.sh && ! git diff --quiet -- unittest.c`.
5. `scripts/bench.py`:
   - `run --label L --model M --effort E [--ref REF] [--reps N] [--jobs J]
     [--timeout S] [--task ID ...]`.
     Refuses a label whose directory exists. Builds the binary as decided. For
     each task and repetition: create the run directory, clone and check out the
     repository if the task has one, run
     `<binary> run --dir workspace --model M --effort E <prompt>` with
     `OPENROUTER_API_KEY` from `.env` and `OX_DATA_DIR` set to `data/`, apply
     the timeout, run the check command, compute the metrics from `data/ox.db`,
     and write `result.json`. Print one line per finished repetition: task, rep,
     status, passed, requests, tool calls, cost, seconds.
   - `compare LABEL [LABEL ...]`: load every result under each label, print one
     table per task and a totals table.
6. Run the smoke benchmark from the test plan.

## Documentation updates

- `AGENTS.md`: add `scripts/bench.py` and `scripts/bench/tasks.toml` to the
  directory map.
- `agents/testing.md`: under Live checks, one line that `scripts/bench.py` runs
  the benchmark tasks against a built commit and compares labeled runs.
