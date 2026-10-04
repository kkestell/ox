# Benchmark on small-c task commits

## Goal

Replace the benchmark tasks with tasks taken from the Git history of small-c, a
Small-C 2.1 compiler written in Go that emits QBE intermediate language. Each
task is a tagged small-c commit that adds one feature and its tests. The agent
starts from the parent commit's tree and implements the feature; the runner
scores the result with the tests the commit adds, and counts any older tests it
breaks.

When the work is done, small-c exists as its own repository with a scaffold
commit and one task, `scripts/bench.py validate` confirms that the task is
sound, and `scripts/bench.py run` scores Ox on it with the new tests visible,
partly visible, or hidden. Token, tool-call, and cost metrics and the comparison
report work as before.

## Related code

- `scripts/bench.py` — the current runner. Keep `build`, `go_build`, the
  repetition loop and retries in `run` and `run_repetition`,
  `transcript_metrics`, `compare`, `chart_svg`, and the report helpers. Replace
  `load_tasks` and the workspace setup and check in `attempt_repetition` (clone,
  fixtures, `setup`, `check`).
- `scripts/bench/tasks.toml`, `scripts/bench/fixtures/` — the current tasks;
  deleted.
- `scripts/bench/Dockerfile` — the current Rust image; replaced.
- `AGENTS.md` — describes `scripts/bench.py` and `scripts/bench/`.
- `.gitignore` — `/bench/` already ignores the runner's results.

## small-c conventions

These rules are the contract between small-c and the runner. small-c's
`AGENTS.md` states them.

- small-c is a separate Git repository at `~/src/small-c`.
- A task commit holds one feature and its tests and is tagged `task/<id>`. Its
  first parent is the task's base.
- A test case is `tests/<group>/<name>.c` plus any sibling files with the same
  name and another extension. Its Go subtest is `TestCompiler/<group>/<name>`.
  - `programs`: compile with `smallc`, assemble with `qbe`, link with `cc`, run,
    and compare standard output followed by a final `exit: N` line against
    `<name>.out`.
  - `errors`: `smallc` must exit with a nonzero status.
- Test programs use only behavior that C defines: no overflow, division by zero,
  `INT_MIN / -1`, or dependence on evaluation order.
- Files directly in `tests/`, such as the driver `tests/compiler_test.go`, are
  the test harness, not test cases. Restoring `tests/` for scoring also restores
  the harness. A harness change must keep working with the base's test cases.
- A task's new tests are the test cases with a file that the task commit adds or
  modifies. Its old tests are all other test cases in the task commit's
  `tests/`.

## Decisions

- **Dialect.** `int` is 32 bits (QBE `w`), pointers are 64 bits (QBE `l`), and
  `char` is a signed 8-bit value. This matches `cc -std=c89 -fsigned-char` on
  64-bit targets, so `go test ./tests -update` writes each `.out` file by
  compiling the program with `cc -std=c89 -w -fsigned-char` and running it.
  Expected results come from the system compiler, never from `smallc`.
- **`smallc` emits QBE intermediate language only.** The test driver runs `qbe`
  and `cc`, as the original Small-C emitted assembly for an external assembler.
- **Task definitions live in ox.** Prompts and example lists are in
  `scripts/bench/tasks.toml`, so the agent never sees them and small-c stays an
  ordinary repository.
- **The workspace has no history.** The runner exports the base tree with
  `git archive` into `/workspace` and creates a fresh repository there with one
  commit, so the agent cannot reach the task commit through Git.
- **Test visibility.** `run --tests visible|examples|hidden`, default
  `examples`, chooses which new test case files the workspace starts with, on
  top of the base's test cases: all of them, those whose path without extension
  matches an entry in the task's `examples` (for example `programs/precedence`),
  or none. Every setting gets the task commit's harness: the runner removes the
  base's files directly in `tests/` and copies in the task commit's. The runner
  appends a fixed sentence per setting to the prompt so the agent knows whether
  tests are held back. The setting is part of a run's identity, like the model
  and effort.
- **Scoring.** After Ox exits, the runner copies the workspace into the run
  directory, replaces `/workspace/tests` with the task commit's `tests/`, and
  runs `go test -json -count=1 ./tests`. Test files the agent wrote are
  discarded and edits to provided test files cannot change the score. A test
  case with no pass result, such as after a build failure, counts as failed.
- **Result fields.** `tests_passed` and `tests_total` count new tests;
  `regressions` counts old tests that failed; `passed` is true when every new
  test passes, `regressions` is 0, and `go test` exits with status 0; `solution`
  is the task commit's hash. Resuming a label requires each saved result's
  `solution` to match the current tag.
- **Validation rule.** At the base tree plus the task commit's `tests/`, every
  test case reports a result, every old test passes, and at least one new test
  fails. At the task commit's tree, every test passes and `go test` exits with
  status 0. New tests that already pass at the base, typically `errors` tests,
  still count toward `tests_passed`, so partial scores include them; `passed` is
  the headline result.
- **Validation runs in ox, in the benchmark image,** so it checks the tasks in
  the same environment that scores them, together with `tasks.toml`.
- **Repository source.** `tasks.toml` has a top-level `repo`, a path to the
  small-c repository relative to the ox root, initially `../small-c`. `run` and
  `validate` resolve each task's tag and first parent to commit hashes once per
  invocation and run `git archive` against that repository.
- **The first task, `expressions`, is part of this plan** so the runner and
  validation have a real task to work against. Later tasks are separate work.
- **The default timeout becomes 1800 seconds,** since compiler features take
  longer than the old tasks.

## Test plan

- small-c: `make check` passes at the scaffold commit and at `task/expressions`.
  `go test ./tests -update` leaves every `.out` file unchanged.
- `validate` reports `expressions` as valid.
- `validate` fails with a message naming the task and the problem when the tag
  is missing, when an `examples` entry matches no new test, and when every new
  test already passes at the base. Check the last case with a scratch tag on a
  commit that adds only a test its parent already passes, then delete the tag.
- `run --task expressions --reps 1` with a cheap model, once per `--tests`
  setting: `start.txt` lists the harness plus all, the examples, or none of the
  new test case files; `result.json` has the new fields; `check.txt` holds the
  `go test` output; and `workspace/` holds the agent's tree, not the restored
  tests.
- Running a second label with a different `--tests` setting into the first
  label's directory is refused.
- `compare` on two labels writes a report with the new metric rows and a Tests
  column, and renders the chart.

## Implementation plan

### small-c, in `~/src/small-c`

1. Run `git init` and write the scaffold:
   - `go.mod` — module `github.com/kkestell/small-c`, `go 1.27`, no
     dependencies.
   - `cmd/smallc/main.go` — `smallc [-o OUTPUT] INPUT`: compiles `INPUT` and
     writes QBE intermediate language to `OUTPUT`, or to standard output.
     Diagnostics go to standard error as `INPUT:LINE: message`, with exit
     status 1.
   - `internal/compiler/compiler.go` — `Compile` takes the input name and source
     and returns the QBE intermediate language or an error. The scaffold accepts
     only `main() { return INTEGER; }`, with any whitespace and `/* */`
     comments, and emits `export function w $main() {`, `@start`, `ret N`, `}`.
   - `tests/compiler_test.go` — `TestMain` builds `cmd/smallc` once into a
     temporary directory. `TestCompiler` runs one parallel subtest per test case
     as described in the conventions, each with a 10-second run limit. The
     `-update` flag rewrites the `.out` files as described in the decisions.
   - `tests/programs/return_integer.c` and `.out`;
     `tests/errors/missing_semicolon.c`.
   - `Makefile` — `check` runs `gofmt -l`, `go vet ./...`, and `go test ./...`;
     `format` runs `gofmt -w`.
   - `README.md` — what small-c is, requirements (Go 1.27, QBE 1.2, a C compiler
     as `cc`), usage, the test layout and `-update`, and the dialect.
   - `AGENTS.md` — short code-style rules in the style of ox's, `make check` as
     validation, and the conventions above.
   - `.gitignore`.

   Commit it as "Scaffold the Small-C compiler". This commit is not a task.
2. Implement task `expressions`: decimal integer literals, unary `-`, binary
   `*`, `/`, `%`, `+`, and `-` with C precedence and left associativity, and
   parentheses, in the `return` expression. Add `tests/programs/precedence.c`,
   `associativity.c`, `unary_minus.c`, `division.c` (positive and negative
   operands), and `parentheses.c`, plus `tests/errors/unbalanced_parens.c`.
   Write the `.out` files with `-update`. Commit as "Compile integer
   expressions" and tag `task/expressions`.

### ox

3. The ox working tree has uncommitted edits to `scripts/bench/tasks.toml` and
   `scripts/bench/fixtures/`. Ask the user to commit or discard them, then
   delete `scripts/bench/fixtures/`.
4. Replace `scripts/bench/Dockerfile`: start from `golang:1.27-bookworm`, whose
   `gcc` provides `cc`; install `procps`, `python3`, `ripgrep`, and `xz-utils`;
   download the QBE 1.2 release tarball from `c9x.me` and build and install it.
5. Rewrite `scripts/bench/tasks.toml`: a header comment describing the format,
   `repo = "../small-c"`, and one `[[task]]` with `id = "expressions"`, a
   `prompt` describing the feature without naming test files, and
   `examples = ["programs/precedence", "programs/division"]`.
6. Change `scripts/bench.py`:
   - Update the header comment's usage lines for `run --tests` and `validate`.
     Remove `FIXTURES`; add `TEST_NOTES`, the prompt sentence for each `--tests`
     setting.
   - `load_tasks` returns the repository path and the tasks, and rejects
     duplicate ids.
   - `task_commits(repo, task)` resolves `task/<id>` and its first parent to
     hashes. `new_test_files(repo, base, solution)` lists the paths added or
     modified under `tests/`. `test_ids(paths)` maps paths to subtest names,
     ignoring files directly in `tests/`.
   - `start_container(name, label)` holds the `docker run` call shared by `run`
     and `validate`. `copy_tree(container, repo, commit, paths)` pipes
     `git archive` of the paths at the commit into
     `docker cp - <container>:/workspace`.
   - `prepare_workspace` copies the base tree, replaces the files directly in
     `tests/` with the task commit's, and copies the visible new test case
     files. It then creates the one-commit repository, with `user.name` and
     `user.email` passed through `git -c`, and writes `git ls-files` to
     `start.txt`.
   - `score` restores `tests/`, runs the tests under `timeout`, writes the
     output to `check.txt`, and computes the result fields from the exit status
     and the `go test -json` events.
   - `attempt_repetition` uses these in place of the clone, fixture, setup, and
     check code and drops `CARGO_TARGET_DIR`. It copies `/workspace` to the run
     directory before `score`.
   - `run` adds `--tests` and sets `--timeout` to 1800 by default; the identity
     includes `tests`. The resume check also compares each saved result's
     `solution` with the current tag.
   - Add the `validate` subcommand with `--task`. For each task it checks that
     the tag and its parent exist and that every `examples` entry matches a new
     test file, then applies the validation rule, using one container for the
     base tree and another for the task commit's tree. It prints one line per
     task and exits with status 1 if any task is invalid.
   - Add `tests_passed`, `tests_total`, and `regressions` to `METRICS`, and a
     Tests column to the Runs table in `compare`.

## Documentation updates

- `AGENTS.md` — `scripts/bench.py` benchmarks a build of `ox-server` on the
  small-c tasks, validates the tasks, and compares benchmark runs;
  `scripts/bench/` holds the task list and the benchmark image.
