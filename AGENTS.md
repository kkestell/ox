Keep this document accurate and short.

## Code style

- Do less. Keep correct code simple, ordinary, and cheap to change.
- Write idiomatic Go. Prefer plain data and direct functions.
- Give each concern a clear boundary and each lifecycle one owner.
- Let real needs earn abstractions, dependencies, and configuration. Measure
  before optimizing; harden against observed failures.
- Return clear errors for bad input; fail loudly on broken invariants. Test
  observable behavior; write comments that explain why.

## Code

- `cmd/ox-server/` — the Ox server: ACP, turns, tools, OpenRouter, and session
  store.
- `cmd/ox/` — the terminal client and its terminal tests.
- `internal/` — one package per concern, shared by both commands.
- `internal/sysprompt/` — the built-in system prompt.
- `examples/` — the example settings file.
- `scripts/run.py` — runs one headless prompt in a temporary workspace.
- `evals/` — the small-c evaluation runner, tasks, image, and results.
- `scripts/export_session.py` — exports a session to JSON.
- `.github/workflows/` — the release build.
- `research/` — research notes and reports.
- `agents/` — plans, reviews, and work logs.

## Validation

- `make check` runs every check. Run it after changing code.
- `make check-docs` checks the Markdown. Run it after changing only docs or
  comments.
- `make e2e` runs the isolated tmux tests. Run it after changing terminal
  behavior.
- `make format` formats the code and the Markdown.

Report any check that fails or is skipped.

## Evaluation tasks

The evaluation tasks are commits in small-c, at `../small-c`, listed in
`evals/tasks.toml`. The agent under evaluation works in a copy of small-c's
tree, so instructions for adding tasks live here and not in small-c.

To add a task:

1. Take the first unchecked task below.
2. In small-c, starting from `main`, implement the feature and its test cases in
   one commit, following small-c's `AGENTS.md`. Write the `.out` files with
   `go test ./tests -update`, run `make check`, and tag the commit `task/<id>`.
3. Add a `[[task]]` to `evals/tasks.toml` with the id, a prompt that describes
   the feature without naming test files, and `examples` naming two of the new
   `programs` test cases.
4. Run `python3 evals/bench.py validate --task <id>`. A task is valid when at
   least one new test fails at its base, every old test passes there, and every
   test passes at the tagged commit.
5. Check the task below, and commit `evals/tasks.toml` and this file.

Each task builds on the one before it:

1. [x] `expressions` — integer literals, unary `-`, `* / % + -`, and parentheses
       in the `return` expression.
2. [x] `control-flow` — the comparison operators and nested `if`/`else`, with
       one `return` in each branch.
3. [x] `locals` — local `int` variables, assignment, and bodies with several
       statements.
4. [ ] `loops` — `while`, `break`, and `continue`.
5. [ ] `functions` — functions with parameters, and calls.
6. [ ] `externals` — calls to external functions such as `putchar`.
7. [ ] `globals` — global variables.
8. [ ] `logical` — `!`, `&&`, and `||`.
9. [ ] `pointers` — `&`, `*`, and pointer arithmetic.
10. [ ] `arrays` — arrays.
11. [ ] `chars` — `char`, character constants, and string literals.
12. [ ] `assignment-operators` — compound assignment, `++`, and `--`.
13. [ ] `bitwise` — bitwise operators and shifts.
14. [ ] `conditional` — the `?:` operator.
15. [ ] `switch` — `switch`.
16. [ ] `preprocessor` — `#define` and `#include`.

## Documentation

Use only `README.md`, `AGENTS.md`, the code and its comments, plans, reviews,
work logs, and Git history. Never create new documentation files.

`README.md` tells users how to install, configure, and use Ox. Update it only
when those instructions change. `AGENTS.md` gives instructions to agents and
should rarely change.

The code describes the implementation. Comments explain non-obvious reasons or
external rules; they never restate code. Plans, reviews, and work logs record
work in `agents/`. Git history records changes.

A fact lives in one place. Do not duplicate it across these sources. Most
changes need no documentation or comment edits. Correct or delete inaccurate
text without expanding it.

## Backwards Compatibility

Currently, there is none. Recreate `ox.db`, `ox.db-shm`, and `ox.db-wal` instead
of adding migrations or versions. They live in `$OX_DATA_DIR`, else
`$XDG_DATA_HOME/ox`, else `~/.local/share/ox`.

## Communication

- Always describe things directly, clearly, and plainly
- Follow big idea up front and progressive disclosure
- Never use jargon, invented terms, or shorthand
- Never mix definitions or overload terms
