# Behavioral Evaluation System

## Goal

Build a behavioral evaluation system that drives the **Ox binary** through long-horizon,
multi-turn tasks in isolated Podman containers, then checks outcomes against explicit
validation rules. Evals test what matters — whether the real agent does the right thing
— not library internals. Results accumulate in SQLite so metric regressions (token
usage, tool error rate, turns to completion) are visible across models and over time.

## Desired outcome

- `make evals-run` executes all scenarios against their configured models in fresh
  Podman containers and produces a Markdown report.
- `make evals-run-quick` runs only `complexity: simple` scenarios for fast
  pre-merge checks.
- Each row in SQLite captures: turns, input/output tokens, tool call count and error
  rate, pass/fail, duration, and which validation rules failed.
- Adding a new scenario is a single YAML file in `evals/scenarios/`.
- `ox --headless --yolo --turn "..."` is a useful standalone feature, not just an
  eval artifact.

## How we got here

A survey of five eval systems (Gemini CLI, Aider, Roo Code, Cline, Goose) identified
the relevant patterns:

- **Drive the real binary** (Gemini CLI) — behavioral evals test whether the agent
  chooses correct actions, not whether library internals wire up correctly. Running the
  Ox binary end-to-end through all turns exercises the real code path.
- **Per-container isolation** (Roo Code, Aider) — each eval gets a fresh Podman
  container with a clean workspace, preventing state contamination between runs.
- **Declarative validation rules** (Goose) — YAML scenario files with structured
  assertions (file_exists, file_contains, command_succeeds) make pass/fail unambiguous
  without parsing agent output.
- **Metrics-over-time persistence** (Roo Code, Cline) — SQLite enables trend queries
  for regression detection.

Three approaches were considered. The user confirmed: drive the Ox binary directly,
not Ur as a library. This requires a headless/YOLO mode added to Ox itself.

## Approaches considered

### Option A — Drive Ox binary in headless mode (recommended)

- **Summary:** Add a `--headless --yolo --turn <msg>` mode to the Ox binary.
  EvalRunner spawns `podman run` for each `scenario × model` pair, passing turns
  as CLI args. Ox writes a metrics JSON file to a mounted volume before exiting.
  EvalRunner reads the file and runs validation checks.
- **Pros:** Evals test the real binary end-to-end. Headless mode is a useful general
  feature (scripting, CI). Multi-turn is native — scenarios just specify multiple
  `--turn` args. No separate EvalHost project.
- **Failure modes:** If Ox crashes mid-run, the metrics file may not be written.
  Mitigated by EvalRunner treating a missing/malformed metrics file as a hard failure.

### Option B — C# EvalHost library harness

- **Summary:** New C# project calls Ur as a library directly, bypassing the Ox binary.
- **Cons:** Does not test the real agent. Misses all Ox-layer behavior. Defeats the
  purpose of behavioral evals.

### Option C — boo PTY automation

- **Summary:** Extend boo Python tests to simulate keystrokes into the TUI.
- **Cons:** TUI output parsing is brittle, not suitable for metrics collection, and
  adds latency. boo already covers TUI smoke tests; evals should not duplicate that
  role.

## Related code

- `src/Ox/Program.cs` — Entry point. Parses `OxBootOptions` and starts TUI or (after
  this change) headless mode. Headless mode branches before any TUI initialization.
- `src/Ox/OxBootOptions.cs` — CLI flag parsing. Gains `--headless`, `--yolo`,
  `--turn <msg>` (repeatable), `--metrics-out <path>`.
- `src/Ur/Hosting/UrHost.cs` — `CreateSession(TurnCallbacks?)`. Null = auto-deny.
  Headless YOLO mode passes a TurnCallbacks that auto-grants everything.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — `TurnCompleted { InputTokens }` (needs
  `OutputTokens` added); `ToolCallStarted`; `ToolCallCompleted { IsError }`.
- `src/Ur/Permissions/PermissionResponse.cs` — `record PermissionResponse(bool, Scope)`.
  YOLO mode returns `new PermissionResponse(true, PermissionScope.Session)` for all.
- `src/Ur/Hosting/UrStartupOptions.cs` — `KeyringOverride` injects `EnvironmentKeyring`
  for container operation (no OS keyring inside Podman).
- `src/Ur/Configuration/Keyring/IKeyring.cs` — Three-method interface. `EnvironmentKeyring`
  implementing it lives in `src/Ur/Configuration/Keyring/` (Ur owns the abstraction;
  container-specific impl belongs alongside the interface, not in Ox).

## Current state

- **No headless mode in Ox.** `Program.cs` always enters the TUI path after DI setup.
- **No `--yolo` mode.** Permissions are always interactive via `OxApp`'s permission
  prompt bridge.
- **`TurnCompleted` carries `InputTokens` but not `OutputTokens`.** Needed for cost
  tracking in eval metrics. Additive change to `AgentLoopEvent`.
- **API keys live in the OS keyring.** Inside a Podman container there is no keyring
  daemon. `UrStartupOptions.KeyringOverride` already exists as the injection point.

## Structural considerations

**Hierarchy:** Headless mode lives in `src/Ox/` (same binary, same DI setup, different
execution path after `UrHost` is resolved). `evals/` projects have no dependency on
`src/` — they treat Ox as an opaque binary. The only data exchange crossing the
host/container boundary is: scenario YAML (mounted read-only), workspace directory
(mounted read-write), metrics JSON (written by Ox, read by EvalRunner from the same
mount), and `providers.json` (mounted read-only).

**Abstraction:** `HeadlessRunner` in `src/Ox/` sits at the same level as `OxApp` — it
uses `UrHost.CreateSession`, drives `RunTurnAsync`, and collects events. It does not
reach into Ur internals. `EnvironmentKeyring` lives in `src/Ur/Configuration/Keyring/`
alongside the `IKeyring` interface it implements — Ur owns the abstraction, so the
concrete implementation belongs there, not in Ox.

**Modularization:** `evals/` is cleanly separated from `src/`. Three modules:
`EvalShared` (shared data types + validation), `EvalRunner` (orchestration + SQLite).
The Ox binary is the execution engine — eval infrastructure does not duplicate it.

**Encapsulation:** The SQLite schema is internal to `EvalRunner`. The metrics JSON
schema is the only contract between Ox's headless mode and the eval infrastructure.
Keeping it simple (one flat JSON object) avoids versioning friction.

## Refactoring

### 1. Add `OutputTokens` to `TurnCompleted`

`TurnCompleted` currently carries only `InputTokens`. Extend it to also carry
`OutputTokens` from `UsageContent.OutputTokenCount`. Additive — existing callers
that ignore it are unaffected. Needed for cost tracking in eval metrics and useful
for the TUI's context fill display.

### 2. Headless/YOLO mode in Ox (`src/Ox/`)

Add a new execution path in `Program.cs` that branches before TUI initialization
when `OxBootOptions.IsHeadless` is true. This path constructs a `HeadlessRunner`
and runs it, then exits. No TUI code is reached. This is the only structural change
to `src/Ox/` — `OxApp` and all TUI code are untouched.

## Implementation plan

### Phase 1 — Ur addition

- [ ] Add `long? OutputTokens { get; init; }` to `TurnCompleted` in
  `src/Ur/AgentLoop/AgentLoopEvent.cs`.
- [ ] Populate it from `UsageContent.OutputTokenCount` alongside `InputTokens` in
  the agent loop. (Find the `UsageContent` reading site in `AgentLoop.cs`.)
- [ ] Unit test: fake provider reports usage → `TurnCompleted` carries both tokens.

### Phase 2 — Headless/YOLO mode in Ox

- [ ] Extend `OxBootOptions.cs`:
  - `bool IsHeadless { get; private init; }` — set by `--headless` flag.
  - `bool IsYolo { get; private init; }` — set by `--yolo` flag. Auto-grants all
    tool permissions without prompting. Meaningless outside headless mode.
  - `IReadOnlyList<string> Turns { get; private init; }` — accumulated from
    repeated `--turn <msg>` args. At least one turn required in headless mode.
  - `string? MetricsOutPath { get; private init; }` — from `--metrics-out <path>`.
    When set, Ox writes a metrics JSON file here on exit.
  - `string? ModelOverride { get; private init; }` — from `--model <provider/model>`.
    Passed to `UrStartupOptions.SelectedModelOverride` so headless runs select the
    eval model without rewriting settings files.
- [ ] Add `EnvironmentKeyring.cs` in `src/Ur/Configuration/Keyring/` — implements
  `IKeyring`. Reads `UR_API_KEY_{ACCOUNT_UPPER}` env vars (uppercase, hyphens and
  dots replaced with underscores); `SetSecret`/`DeleteSecret` are no-ops. Comment
  explains why: containers are ephemeral, keys come in via env, writes would be lost.
  Registered via `KeyringOverride` in `UrStartupOptions` only when `IsHeadless` is
  true — no effect on the TUI path.
- [ ] Add `HeadlessRunner.cs` in `src/Ox/`:
  - Constructor: `UrHost host, IReadOnlyList<string> turns, bool yolo,
    string? metricsOutPath`.
  - `RunAsync(CancellationToken ct)`:
    - Build `TurnCallbacks`: if `yolo`, `RequestPermissionAsync` returns
      `new PermissionResponse(true, PermissionScope.Session)` for all requests.
      Otherwise null (auto-deny, for dry-run use).
    - Create a session: `host.CreateSession(callbacks)`.
    - For each turn in `turns`:
      - `await foreach (var ev in session.RunTurnAsync(turn, ct))`:
        - `ResponseChunk` → write text to stdout (the agent's response is visible).
        - `TurnCompleted` → accumulate `InputTokens`, `OutputTokens`; increment
          turn counter.
        - `ToolCallStarted` → increment `ToolCallsTotal`.
        - `ToolCallCompleted { IsError: true }` → increment `ToolCallsErrored`.
        - `TurnError { IsFatal: true }` → write to stderr, record error, break.
    - Note: `TurnCompleted` fires exactly once per `RunTurnAsync` call — when the
      inner agent loop exits with no pending tool calls. It is the natural signal to
      finalize per-turn metrics and increment the turn counter.
    - Build `HeadlessMetrics` record (defined here, serialized to the output file):
      ```json
      {
        "turns": 3,
        "input_tokens": 12400,
        "output_tokens": 980,
        "tool_calls_total": 7,
        "tool_calls_errored": 1,
        "duration_seconds": 34.2,
        "error": null
      }
      ```
      All fields are required. `error` is null on success. `tool_error_rate` is
      intentionally absent — EvalRunner computes it as
      `tool_calls_errored / (double)tool_calls_total` when inserting to SQLite, to
      avoid dividing by zero in the binary when no tools were called.
    - If `metricsOutPath` is set, serialize `HeadlessMetrics` to JSON and write to
      that path (creating parent directories if needed).
    - Exit 0 on success; 1 on fatal error.
- [ ] Extend `Program.cs`: after resolving `UrHost`, check
  `bootOptions.IsHeadless`. If true, construct and run `HeadlessRunner` and return
  its exit code — skip all TUI initialization (alternate screen, `TerminalInputSource`,
  `OxApp`).
- [ ] When `IsHeadless`, inject `new EnvironmentKeyring()` via
  `startupOptions.KeyringOverride` and set `SelectedModelOverride` from
  `bootOptions.ModelOverride` in the DI setup block.
- [ ] Validate in headless mode: if `turns` is empty, print usage to stderr and exit 1.
- [ ] Unit test: `OxBootOptions.Parse(["--headless", "--yolo", "--turn", "hello",
  "--metrics-out", "/tmp/m.json"])` → all fields set correctly.
- [ ] Integration test: headless mode with `--fake-provider` scenario → runs to
  completion, metrics file written, contains expected token counts.

### Phase 3 — EvalShared project

- [ ] Create `evals/EvalShared/EvalShared.csproj`. Target `net10.0`. Add `YamlDotNet`.
- [ ] `ScenarioDefinition.cs` — record: `Name`, `Description`, `Category`,
  `Complexity` (enum: `Simple` / `Medium` / `Complex`), `Models` (list of
  `"provider/model"`), `Turns` (list of strings — the sequence of user messages;
  single-turn scenarios use a one-element list), `WorkspaceFiles` (list of
  `WorkspaceFile { Path, Content }`), `ValidationRules` (list of `ValidationRule`),
  `TimeoutSeconds` (int, default 120).
- [ ] `ValidationRule.cs` — abstract base record. Concrete subtypes:
  - `FileExistsRule { string Path }`
  - `FileNotExistsRule { string Path }`
  - `FileContainsRule { string Path, string Content }`
  - `FileMatchesRule { string Path, string Pattern }` (regex)
  - `CommandSucceedsRule { string Command }` — runs in workspace, asserts exit 0.
  - `CommandOutputContainsRule { string Command, string Output }` — checks stdout.
- [ ] `ValidationRunner.cs` — lives in `EvalShared` (no Ur dependency). `RunAsync`
  evaluates each rule against the workspace directory. File rules use `System.IO`.
  Command rules spawn `bash -c "{command}"` with CWD = workspace, 15s per-command
  timeout, capture stdout + stderr. Returns `List<ValidationFailure>`.
- [ ] `EvalResult.cs` — record combining `HeadlessMetrics` data + validation results:
  `ScenarioName`, `Model`, `Passed` (bool), `Turns` (int), `InputTokens` (long),
  `OutputTokens` (long), `ToolCallsTotal` (int), `ToolCallsErrored` (int),
  `DurationSeconds` (double), `ValidationFailures` (list of `{ RuleType, Message }`),
  `Error` (string?). This is EvalRunner's view of a completed run.
- [ ] `ScenarioLoader.cs` — deserializes YAML to `ScenarioDefinition` using YamlDotNet
  with `UnderscoredNamingConvention`. The `type` field on each validation rule entry
  discriminates the concrete subtype.
- [ ] Unit tests: `ScenarioLoader` round-trips YAML; `ValidationRunner` evaluates each
  rule type against a temp directory.

### Phase 4 — EvalRunner project

- [ ] Create `evals/EvalRunner/EvalRunner.csproj` referencing `EvalShared`. Target
  `net10.0`. Add `Microsoft.Data.Sqlite` and `Dapper`.
- [ ] `ResultStore.cs` — creates SQLite DB on first open:
  ```sql
  CREATE TABLE IF NOT EXISTS eval_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scenario_name TEXT NOT NULL,
    model         TEXT NOT NULL,
    timestamp     TEXT NOT NULL,   -- ISO 8601
    passed        INTEGER NOT NULL,
    turns         INTEGER NOT NULL,
    input_tokens  INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    tool_calls_total   INTEGER NOT NULL,
    tool_calls_errored INTEGER NOT NULL,
    tool_error_rate    REAL NOT NULL,
    duration_seconds   REAL NOT NULL,
    error         TEXT
  );
  CREATE TABLE IF NOT EXISTS validation_failures (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id  INTEGER NOT NULL REFERENCES eval_runs(id),
    rule_type TEXT NOT NULL,
    message   TEXT NOT NULL
  );
  ```
  Methods: `SaveRunAsync(EvalResult)`, `LoadRecentAsync(int days)`.
- [ ] `WorkspaceBuilder.cs` — creates a temp dir under `/tmp/ox-eval-XXXX`, populates
  it from `ScenarioDefinition.WorkspaceFiles`. EvalRunner deletes it after the
  container exits.
- [ ] `ContainerRunner.cs` — builds and runs a `podman run` command:
  - Mounts: workspace → `/workspace` (rw), `providers.json` → `/eval/providers.json`
    (ro), scenario YAML → `/eval/scenario.yaml` (ro), temp eval dir →
    `/eval/out` (rw, for metrics output).
  - Env vars: all `UR_API_KEY_*` vars from the host process environment.
  - Command: `ox --headless --yolo --metrics-out /eval/out/metrics.json` plus one
    `--turn <msg>` per turn from the scenario, plus `--model <model>`.
  - Timeout: `scenario.TimeoutSeconds` (enforced via `podman run --timeout`).
  - After exit: read `/eval/out/metrics.json`. If the file is missing or
    unreadable (Ox crashed before writing it), return a synthetic `HeadlessMetrics`
    with `Error` set and all numeric fields zero. When the metrics file indicates a
    crash (`Error` is non-null), `ContainerRunner` skips `ValidationRunner` and
    marks the run failed — the workspace state may be partial and running validation
    against it would produce misleading failures.
- [ ] `ReportGenerator.cs` — reads recent runs from `ResultStore`, generates a Markdown
  table grouped by scenario × model: pass rate, avg turns, avg tokens (in/out), avg
  tool error rate, avg duration. Writes to `evals/results/report-{date}.md`.
- [ ] `Program.cs` — CLI flags:
  - `--scenarios <dir>` (default `evals/scenarios/`)
  - `--complexity <simple|medium|complex>` (filter by complexity tier)
  - `--filter <glob>` (filter by scenario name)
  - `--models <m1,m2>` (override model list for all scenarios)
  - `--providers <path>` (default `~/.ur/providers.json`)
  - `--db <path>` (default `evals/results/evals.db`)
  - `--report` (write report after run, default true)
  - Loads scenarios, applies filters, expands to `scenario × model` pairs.
  - For each pair: `WorkspaceBuilder` → `ContainerRunner` → `ValidationRunner`
    (only when `ContainerRunner` succeeds without a crash) → `ResultStore.SaveRunAsync`.
  - Prints summary: total pass rate, per-model breakdown, regressions vs previous run.

### Phase 5 — Container infrastructure

- [ ] `evals/Containerfile` — multi-stage build:
  - Stage 1 (`mcr.microsoft.com/dotnet/sdk:10.0`): `dotnet publish src/Ox` as
    linux-x64 self-contained binary.
  - Stage 2 (`mcr.microsoft.com/dotnet/runtime-deps:10.0`): copy published binary.
    Install `git`, `bash`, `python3`, `python3-pytest` (needed by eval scenarios that
    run Python tests). `WORKDIR /workspace`. `ENTRYPOINT ["ox"]`.
  - Building this way means `make evals-build` does not need a pre-published binary.
    Layer caching means rebuilds are fast when only non-Ox files change.
- [ ] `Makefile` additions:
  ```makefile
  evals-build:
  	podman build -f evals/Containerfile -t ox-eval .

  evals-run: evals-build
  	dotnet run --project evals/EvalRunner -- --scenarios evals/scenarios/

  evals-run-quick: evals-build
  	dotnet run --project evals/EvalRunner -- --scenarios evals/scenarios/ --complexity simple
  ```
- [ ] Add `evals/results/` to `.gitignore`. Add `evals/results/.gitkeep` so the
  directory exists on checkout.

### Phase 6 — Initial scenario library

Write 15 scenarios in `evals/scenarios/`. Default models for all scenarios:
`google/gemini-3.1-flash-lite-preview`, `zai-coding/glm-4.5-air`.

**Simple** — single-turn, deterministic, verifiable by file state:

- [ ] `create-file.yaml` — Turn: "Create hello.txt containing exactly `Hello, World!`".
  Validate: `file_exists`, `file_contains`.
- [ ] `append-to-file.yaml` — Workspace: `log.txt` with one line. Turn: "Append the
  line `Done.` to log.txt". Validate: `file_contains: Done.`.
- [ ] `fix-syntax-error.yaml` — Workspace: Python file with broken syntax. Turn: "Fix
  the syntax error in greet.py so `python greet.py` runs without errors." Validate:
  `command_succeeds: python greet.py`.
- [ ] `rename-variable.yaml` — Workspace: Python file with `foo = 1`. Turn: "Rename
  `foo` to `bar` in main.py." Validate: `file_contains: bar = 1`,
  `command_output_contains: grep -c "foo = " main.py → 0`.
- [ ] `create-from-spec.yaml` — Workspace: `spec.md` describing a function. Turn:
  "Implement the function described in spec.md in a new file utils.py." Validate:
  `file_exists: utils.py`, `command_succeeds: python utils.py`.

**Medium** — multi-step single-turn or 2-turn:

- [ ] `add-function.yaml` — Workspace: `math_utils.py`. Turn: "Add a `multiply(a, b)`
  function." Validate: `file_contains: def multiply`,
  `command_output_contains: python -c "from math_utils import multiply; print(multiply(3,4))" → 12`.
- [ ] `git-commit.yaml` — Empty workspace. Turns: ["Initialize a git repo and create
  README.md with `# My Project`", "Commit it with message `Initial commit`"]. Validate:
  `command_succeeds: git log --oneline`, `file_contains: # My Project`.
- [ ] `refactor-constant.yaml` — Workspace: `config.py` with `TIMEOUT = 30`,
  `server.py` importing it. Turn: "Change the timeout to 60 everywhere." Validate:
  `file_contains: TIMEOUT = 60` (config.py), `file_contains: 60` (server.py).
- [ ] `multi-file-search.yaml` — Workspace: 5 Python files, one with a TODO. Turn:
  "Find and report every TODO comment in the codebase." Validate:
  `command_output_contains: grep -rn "TODO" . → TODO`.
- [ ] `add-tests.yaml` — Workspace: `calculator.py` with functions, no tests. Turn:
  "Write tests for the calculator functions in test_calculator.py." Validate:
  `file_exists: test_calculator.py`, `command_succeeds: python -m pytest test_calculator.py`.

**Complex** — long-horizon, multiple turns, 3 models (add `zai-coding/glm-5-turbo`):

- [ ] `implement-and-test.yaml` — Workspace: stub `calculator.py` + test file. Turn:
  "Implement the calculator functions so all tests pass." Validate:
  `command_succeeds: python -m pytest test_calculator.py`.
- [ ] `debug-failing-test.yaml` — Workspace: Python module + failing test (off-by-one
  bug). Turn: "The tests are failing. Diagnose and fix the bug." Validate:
  `command_succeeds: python -m pytest`.
- [ ] `create-cli-tool.yaml` — Empty workspace. Turns: ["Write a Python CLI tool
  word_count.py that accepts a filename and prints the word count", "Add a --help flag
  and a --verbose mode that prints each word"]. Validate:
  `command_succeeds: python word_count.py --help`,
  `command_output_contains: python word_count.py fixture.txt`.
- [ ] `extract-and-refactor.yaml` — Workspace: large Python file with two classes. Turn:
  "Split this into two files, one per class. Update all imports." Validate:
  `file_exists: class_a.py`, `file_exists: class_b.py`,
  `command_succeeds: python -c "from class_a import ClassA; from class_b import ClassB"`.
- [ ] `migrate-data-format.yaml` — Workspace: `data.json` (old format), `schema_v2.json`
  (new schema spec), `validate.py` (exits 0 if output is valid). Turn: "Transform
  data.json to match schema_v2.json and save as data_v2.json." Validate:
  `command_succeeds: python validate.py data_v2.json`.

## Impact assessment

- **`src/Ur/AgentLoop/AgentLoopEvent.cs`** — `TurnCompleted` gains `OutputTokens`.
  Additive; no existing caller breaks.
- **`src/Ox/OxBootOptions.cs`** — gains `IsHeadless`, `IsYolo`, `Turns`,
  `MetricsOutPath`.
- **`src/Ox/Program.cs`** — branches into `HeadlessRunner` before any TUI init when
  `IsHeadless` is true.
- **New in `src/Ur/Configuration/Keyring/`**: `EnvironmentKeyring.cs`.
- **New in `src/Ox/`**: `HeadlessRunner.cs`.
- **New in `evals/`**: `EvalShared/`, `EvalRunner/`, `scenarios/`, `results/`,
  `Containerfile`. `Ox.slnx` gains two entries.
- **No changes to `OxApp` or any TUI code.**

## Validation

- **Unit tests:** `OxBootOptions` parses all headless flags; `EnvironmentKeyring` reads
  env vars; `ScenarioLoader` YAML round-trips; `ValidationRunner` evaluates all rule
  types; `ResultStore` insert + query.
- **Headless smoke test:** `ox --headless --fake-provider hello --turn "hello"` runs to
  completion, exits 0, writes expected metrics JSON.
- **End-to-end:** `make evals-build` succeeds. `make evals-run-quick` runs 5 simple
  scenarios, at least one model passes `create-file.yaml`, report is written.

## Open questions

None — all design decisions resolved.
