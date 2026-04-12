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
  rate, pass/fail, duration, and which validation rules failed. The full session JSONL
  and metrics JSON are stored as blobs for historical analysis.
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
  for regression detection, and stores full session artifacts for historical analysis.

Three approaches were considered. The user confirmed: drive the Ox binary directly,
not Ur as a library. This requires a headless/YOLO mode added to Ox itself.

## Approaches considered

### Option A — Drive Ox binary in headless mode (recommended)

- **Summary:** Add a `--headless --yolo --turn <msg>` mode to the Ox binary.
  EvalRunner spawns `podman run` for each `scenario × model` pair, passing turns
  as CLI args. Ur writes a metrics JSON file alongside the session file before exiting.
  EvalRunner reads both files from the mounted volume and stores them in SQLite.
- **Pros:** Evals test the real binary end-to-end. Headless mode is a useful general
  feature (scripting, CI). Multi-turn is native — scenarios just specify multiple
  `--turn` args. No separate EvalHost project. Metrics collection is generic — TUI
  sessions also produce metrics files.
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
  `--turn <msg>` (repeatable).
- `src/Ur/Hosting/UrHost.cs` — `CreateSession(TurnCallbacks?)`. Null = auto-deny.
  Headless YOLO mode passes a TurnCallbacks that auto-grants everything.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — `TurnCompleted { InputTokens }` (needs
  `OutputTokens` added); `ToolCallStarted`; `ToolCallCompleted { IsError }`.
- `src/Ur/Sessions/UrSession.cs` — Gains metrics accumulation across turns; writes
  `{sessionId}.metrics.json` alongside the session JSONL on session close.
- `src/Ur/Sessions/SessionStore.cs` — Gains `WriteMetricsAsync(Session, SessionMetrics)`.
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
- **`UrSession` does not accumulate metrics.** Token counts, tool call counts, errors,
  and duration are observable via events but not persisted anywhere outside the JSONL.
  The JSONL contains `UsageContent` for tokens and `FunctionCallContent`/
  `FunctionResultContent` for tool calls, but not structured error flags or timing.
- **API keys live in the OS keyring.** Inside a Podman container there is no keyring
  daemon. `UrStartupOptions.KeyringOverride` already exists as the injection point.

## Structural considerations

**Hierarchy:** Headless mode lives in `src/Ox/` (same binary, same DI setup, different
execution path after `UrHost` is resolved). `evals/` projects have no dependency on
`src/` — they treat Ox as an opaque binary. The only data exchange crossing the
host/container boundary is: scenario YAML (mounted read-only), workspace directory
(mounted read-write), and the `.ur/sessions/` directory within the workspace (mounted
read-write, contains both the session JSONL and metrics JSON written by Ur).

**Abstraction:** `HeadlessRunner` in `src/Ox/` sits at the same level as `OxApp` — it
uses `UrHost.CreateSession`, drives `RunTurnAsync`, and streams output to stdout. It
does not collect metrics; that is `UrSession`'s responsibility. `EnvironmentKeyring`
lives in `src/Ur/Configuration/Keyring/` alongside the `IKeyring` interface it
implements — Ur owns the abstraction, so the concrete implementation belongs there,
not in Ox.

**Modularization:** `evals/` is cleanly separated from `src/`. Two modules:
`EvalShared` (shared data types + validation), `EvalRunner` (orchestration + SQLite).
The Ox binary is the execution engine — eval infrastructure does not duplicate it.

**Encapsulation:** The SQLite schema is internal to `EvalRunner`. The metrics JSON
schema and session JSONL format are Ur's contracts, written generically for all runs.
EvalRunner treats them as opaque artifacts to capture, with the metrics JSON providing
the structured fields it needs for aggregate queries.

## Refactoring

### 1. Add `OutputTokens` to `TurnCompleted`

`TurnCompleted` currently carries only `InputTokens`. Extend it to also carry
`OutputTokens` from `UsageContent.OutputTokenCount`. Additive — existing callers
that ignore it are unaffected. Needed for metrics accumulation in `UrSession` and
useful for the TUI's context fill display.

### 2. Session metrics collection in Ur (`src/Ur/`)

`UrSession` accumulates metrics across all turns by observing events from the agent
loop: token counts from `TurnCompleted`, tool call and error counts from
`ToolCallStarted` / `ToolCallCompleted`, and wall-clock duration from session start
to close. On session close (dispose), it writes a `SessionMetrics` record as
`{sessionId}.metrics.json` alongside the session JSONL. This is generic behavior —
TUI and headless sessions both produce a metrics file.

`SessionMetrics` is a flat record defined in `src/Ur/Sessions/`:
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
avoid dividing by zero when no tools were called.

### 3. Headless/YOLO mode in Ox (`src/Ox/`)

Add a new execution path in `Program.cs` that branches before TUI initialization
when `OxBootOptions.IsHeadless` is true. This path constructs a `HeadlessRunner`
and runs it, then exits. No TUI code is reached. This is the only structural change
to `src/Ox/` — `OxApp` and all TUI code are untouched.

## Implementation plan

### Phase 1 — Ur additions

- [ ] Add `long? OutputTokens { get; init; }` to `TurnCompleted` in
  `src/Ur/AgentLoop/AgentLoopEvent.cs`.
- [ ] Populate it from `UsageContent.OutputTokenCount` alongside `InputTokens` in
  the agent loop. (Find the `UsageContent` reading site in `AgentLoop.cs`.)
- [ ] Add `SessionMetrics.cs` in `src/Ur/Sessions/` — flat record with the fields
  above. JSON-serializable via `System.Text.Json`.
- [ ] Extend `UrSession` to accumulate metrics: track session start time on
  construction; subscribe to events from `RunTurnAsync` to accumulate `InputTokens`,
  `OutputTokens`, `ToolCallsTotal`, `ToolCallsErrored`, and turn count.
- [ ] Extend `SessionStore` with `WriteMetricsAsync(Session, SessionMetrics)` — writes
  `{sessionId}.metrics.json` in the same directory as the session JSONL.
- [ ] Call `WriteMetricsAsync` from `UrSession.DisposeAsync` (or equivalent close
  path), computing `DurationSeconds` as elapsed time since session start.
- [ ] Unit test: fake provider reports usage + tool calls → `SessionMetrics` written
  with correct counts and tokens.
- [ ] Unit test: `TurnCompleted` carries both `InputTokens` and `OutputTokens`.

### Phase 2 — Headless/YOLO mode in Ox

- [ ] Extend `OxBootOptions.cs`:
  - `bool IsHeadless { get; private init; }` — set by `--headless` flag.
  - `bool IsYolo { get; private init; }` — set by `--yolo` flag. Auto-grants all
    tool permissions without prompting. Meaningless outside headless mode.
  - `IReadOnlyList<string> Turns { get; private init; }` — accumulated from
    repeated `--turn <msg>` args. At least one turn required in headless mode.
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
  - Constructor: `UrHost host, IReadOnlyList<string> turns, bool yolo`.
  - `RunAsync(CancellationToken ct)`:
    - Build `TurnCallbacks`: if `yolo`, `RequestPermissionAsync` returns
      `new PermissionResponse(true, PermissionScope.Session)` for all requests.
      Otherwise null (auto-deny, for dry-run use).
    - Create a session: `host.CreateSession(callbacks)`.
    - For each turn in `turns`:
      - `await foreach (var ev in session.RunTurnAsync(turn, ct))`:
        - `ResponseChunk` → write text to stdout (the agent's response is visible).
        - `TurnError { IsFatal: true }` → write to stderr and break.
    - Metrics are written automatically by `UrSession.DisposeAsync` — `HeadlessRunner`
      does not collect or write them.
    - Exit 0 on success; 1 on fatal error.
- [ ] Extend `Program.cs`: after resolving `UrHost`, check
  `bootOptions.IsHeadless`. If true, construct and run `HeadlessRunner` and return
  its exit code — skip all TUI initialization (alternate screen, `TerminalInputSource`,
  `OxApp`).
- [ ] When `IsHeadless`, inject `new EnvironmentKeyring()` via
  `startupOptions.KeyringOverride` and set `SelectedModelOverride` from
  `bootOptions.ModelOverride` in the DI setup block.
- [ ] Validate in headless mode: if `turns` is empty, print usage to stderr and exit 1.
- [ ] Unit test: `OxBootOptions.Parse(["--headless", "--yolo", "--turn", "hello"])` →
  all fields set correctly.
- [ ] Integration test: headless mode with `--fake-provider` scenario → runs to
  completion, metrics file written alongside session JSONL, contains expected token counts.

### Phase 3 — EvalShared project

- [ ] Create `evals/EvalShared/EvalShared.csproj`. Target `net10.0`. Add `YamlDotNet`.
- [ ] `ScenarioDefinition.cs` — record: `Name`, `Description`, `Category`,
  `Complexity` (enum: `Simple` / `Medium` / `Complex`), `Models` (list of
  `"provider/model"`), `Turns` (list of strings — the sequence of user messages;
  single-turn scenarios use a one-element list), `Repository` (optional
  `RepositoryRef { string Url, string Commit }`), `WorkspaceFiles` (list of
  `WorkspaceFile { Path, Content }`), `ValidationRules` (list of `ValidationRule`),
  `TimeoutSeconds` (int, default 120).

  `Repository` and `WorkspaceFiles` are mutually exclusive. Real-repo scenarios
  specify `repository` — `WorkspaceBuilder` clones at the pinned commit and the
  workspace is exactly the repo at that state, failing tests and all. Synthetic
  scenarios specify `workspace_files` — `WorkspaceBuilder` writes those files
  directly. The pinned commit is what makes real-repo scenarios reproducible.
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
- [ ] `EvalResult.cs` — record combining `SessionMetrics` data + validation results:
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
    error         TEXT,
    session_jsonl TEXT NOT NULL,   -- full contents of the session JSONL file
    metrics_json  TEXT NOT NULL    -- full contents of the metrics JSON file
  );
  CREATE TABLE IF NOT EXISTS validation_failures (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id  INTEGER NOT NULL REFERENCES eval_runs(id),
    rule_type TEXT NOT NULL,
    message   TEXT NOT NULL
  );
  ```
  Methods: `SaveRunAsync(EvalResult, string sessionJsonl, string metricsJson)`,
  `LoadRecentAsync(int days)`.
- [ ] `WorkspaceBuilder.cs` — creates a temp dir under `/tmp/ox-eval-XXXX`. If
  `scenario.Repository` is set, runs `git clone --no-checkout {url} . && git checkout
  {commit}` to populate the workspace at the exact pinned state. If `Repository` is
  absent, writes `WorkspaceFiles` directly. EvalRunner deletes the temp dir after
  the container exits.
- [ ] `ContainerRunner.cs` — builds and runs a `podman run` command:
  - Mounts: workspace → `/workspace` (rw), `providers.json` → `/eval/providers.json`
    (ro), scenario YAML → `/eval/scenario.yaml` (ro).
  - Env vars: all `UR_API_KEY_*` vars from the host process environment.
  - Command: `ox --headless --yolo` plus one `--turn <msg>` per turn from the
    scenario, plus `--model <model>`.
  - Timeout: `scenario.TimeoutSeconds` (enforced via `podman run --timeout`).
  - After exit: locate the session JSONL and metrics JSON in
    `{workspace}/.ur/sessions/`. If either file is missing or unreadable (Ox crashed
    before writing them), return a synthetic failure with `Error` set. When the
    metrics file indicates a crash (`Error` is non-null), skip `ValidationRunner` and
    mark the run failed — the workspace state may be partial and running validation
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
    Install language toolchains needed by the scenario library: `git`, `bash`,
    `python3`, `python3-pip`, `python3-pytest`, `ruby`, `bundler`, `nodejs`, `npm`,
    `golang`, `rustup` (with stable toolchain). The .NET SDK is already present from
    stage 1 — copy it across or install the runtime. This is a fat image by design:
    scenarios span multiple languages and the image is built once and reused.
    `WORKDIR /workspace`. `ENTRYPOINT ["ox"]`.
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

Every scenario clones a real repo at a pinned pre-fix commit. The turns are derived
from the issue description. Validation runs the test suite added or modified by the
fix. The fix commit is recorded in each YAML as `fix_commit` for reference — it is
the ground truth, not used at runtime.

**Commit hashes below must be verified against GitHub before writing the YAML files.**
The agent that sourced these claims to have verified them, but spot-check before
treating them as canonical.

Each YAML has the structure:
```yaml
name: click-semver-default
complexity: simple  # or medium / complex
models:
  - google/gemini-3.1-flash-lite-preview
  - zai-coding/glm-4.5-air
repository:
  url: https://github.com/pallets/click
  commit: 04ef3a6f473deb2499721a8d11f92a7d2c0912f2  # pre-fix
  fix_commit: 1458800409ed12076f18451889b0857db36aa522  # reference only
turns:
  - "Using a semver.Version instance as a Click option default raises an exception
     when generating help text. Investigate and fix it."
  - "Run the tests to confirm the fix is complete."
validation_rules:
  - type: command_succeeds
    command: pytest tests/test_options.py
timeout_seconds: 300
```

**Simple** — contained fix, 2 turns, one module:

- [ ] `nodatime-localtime-seconds.yaml` — nodatime/nodatime #807. C#. `LocalTime`
  constructor silently wraps seconds ≥ 60 instead of throwing. Before:
  `c323a3b2f92d937c4fe81f6d33a43562b4c6d49b`. Fix: `2d48b2998434d8c4fc478a923f7f5281a6d5bfe2`.
  Validate: `dotnet test src/NodaTime.Test`.
- [ ] `sinatra-content-type-integer.yaml` — sinatra/sinatra #2077. Ruby.
  `content_type` crashes when a param value is an integer. Before:
  `7b50a1bbb5324838908dfaa00ec53ad322673a29`. Fix: `c4b7c04e6d23ef8e17404d64cc731bece268acea`.
  Validate: `bundle exec rake test`.
- [ ] `rack-nil-accept-header.yaml` — rack/rack #2225. Ruby. Blank `Accept` header
  raises `NoMethodError`. Before: `39a53608ed37c8c75479393eef024ca7b208c8f1`. Fix:
  `7222c0a789550540e70c126664f8424923c10808`. Validate: `bundle exec rspec`.
- [ ] `gh-release-limit-zero.yaml` — cli/cli #13078. Go. `gh release list --limit 0`
  loops infinitely. Before: `5d3c2ba5691f4cb8388710c578eeeadf216eec96`. Fix:
  `d0558fcbaad794c343bdfd3efd75d13777c2d42a`. Validate:
  `go test ./pkg/cmd/release/list/...`.
- [ ] `bubbletea-init-panic-deadlock.yaml` — charmbracelet/bubbletea #924. Go. Panic
  in `Init()` deadlocks instead of shutting down cleanly. Before:
  `6b98c9ced38bd1f5dbd59bffea58ae0c53c4dbee`. Fix:
  `1c6e74daab28ebb6985f8ff480d61117d6da3fba`. Validate: `go test ./...`.

**Medium** — requires navigating multiple files or understanding a subtle interaction,
2–3 turns, 3 models (add `zai-coding/glm-5-turbo`):

- [ ] `click-semver-default.yaml` — pallets/click #3298. Python. `semver.Version`
  as option default crashes help text generation. Before:
  `04ef3a6f473deb2499721a8d11f92a7d2c0912f2`. Fix:
  `1458800409ed12076f18451889b0857db36aa522`. Validate: `pytest tests/test_options.py`.
- [ ] `requests-proxy-auth-stripped.yaml` — psf/requests #5888. Python. Manually set
  `Proxy-Authorization` header stripped by `rebuild_proxies()`. Before:
  `590350f8d094c216051510ed1dd18fe871b53b72`. Fix:
  `99b3b492418d0751ca960178d274f89805095e4c`. Validate: `pytest tests/test_requests.py`.
- [ ] `attrs-cached-property-slots.yaml` — python-attrs/attrs #1230. Python.
  `AttributeError` inside `cached_property` swallowed on `slots=True` class. Before:
  `82a14627fddbd0b2d802fbc574fa3b1ef010a801`. Fix:
  `88e2896ca9351cd48711bd320571a832ae122cd5`. Validate: `pytest tests/test_slots.py`.
- [ ] `attrs-optional-pipe.yaml` — python-attrs/attrs #1348. Python.
  `converters.optional(converters.pipe(...))` raises because `optional` doesn't accept
  `Converter` instances. Before: `ee0f19b696c60064c58cdc08b3265aef56d49ff8`. Fix:
  `e21793e90a25c7ea47a9c0369150067cc8322de0`. Validate: `pytest tests/test_converters.py`.
- [ ] `fastify-uint8array-view.yaml` — fastify/fastify #5118. JavaScript. `reply.send`
  with a `Uint8Array` view sends the whole `ArrayBuffer` instead of just the view's
  bytes. Before: `9b8a7825dc033887d293549e40284284bf27c5a5`. Fix:
  `bc5df037c51ee0e414654a7285342a16207293e0`. Validate: `npm test`.

**Complex** — cross-module, algorithmic, or macro internals; 3–4 turns; 3 models.
`TimeoutSeconds: 600`.

- [ ] `ripgrep-alternation-regression.yaml` — BurntSushi/ripgrep #2884. Rust.
  Case-insensitive alternation patterns produce false negatives due to a regression in
  inner literal extraction. Before: `6c5108ed17987531644518fac8c1659b0b202611`. Fix:
  `9d738ad0c009e6632d75fa3d36051e5ae7f7cce6`. Validate:
  `cargo test -p regex`.
- [ ] `serde-flatten-enum-variants.yaml` — serde-rs/serde #2565. Rust.
  `#[serde(flatten)]` in one enum variant propagates `has_flatten` to all variants.
  Before: `9b868ef831c95f50dd4bde51a7eb52e3b9ee265a`. Fix:
  `fc55ac70d34221b38672b1583e496011fbae92aa`. Validate: `cargo test -p serde_derive`.
- [ ] `clap-bash-completion-double-underscore.yaml` — clap-rs/clap #6339. Rust. Bash
  completion panics when a subcommand name contains `__`. Before:
  `ddc008bbbc1924fbda5d6f2c66bcf4d165984977`. Fix:
  `f88c94e53d40c2427450ed65ec025951906eb1d4`. Validate: `cargo test -p clap_complete`.
- [ ] `effect-stream-decode-text.yaml` — Effect-TS/effect #6039. TypeScript.
  `Stream.decodeText` corrupts multi-byte characters split across chunks. Before:
  `904e055143ad74b1e4cd25429f44e7a3e86db5dc`. Fix:
  `a8c436f7004cc2a8ce2daec589ea7256b91c324f`. Validate: `pnpm test` in
  `packages/effect`.
- [ ] `effect-match-tag-nullish.yaml` — Effect-TS/effect #6017. TypeScript.
  `Match.tag` crashes when the union includes `null` or `undefined`. Before:
  `7b8165f45779380fea8ac8e09badef898b63eb41`. Fix:
  `e71889f35b081d13b7da2c04d2f81d6933056b49`. Validate: `pnpm test`.

## Impact assessment

- **`src/Ur/AgentLoop/AgentLoopEvent.cs`** — `TurnCompleted` gains `OutputTokens`.
  Additive; no existing caller breaks.
- **`src/Ur/Sessions/UrSession.cs`** — gains metrics accumulation across turns;
  writes `{sessionId}.metrics.json` on close.
- **`src/Ur/Sessions/SessionStore.cs`** — gains `WriteMetricsAsync`.
- **New in `src/Ur/Sessions/`**: `SessionMetrics.cs`.
- **`src/Ox/OxBootOptions.cs`** — gains `IsHeadless`, `IsYolo`, `Turns`,
  `ModelOverride`.
- **`src/Ox/Program.cs`** — branches into `HeadlessRunner` before any TUI init when
  `IsHeadless` is true.
- **New in `src/Ur/Configuration/Keyring/`**: `EnvironmentKeyring.cs`.
- **New in `src/Ox/`**: `HeadlessRunner.cs`.
- **New in `evals/`**: `EvalShared/`, `EvalRunner/`, `scenarios/`, `results/`,
  `Containerfile`. `Ox.slnx` gains two entries.
- **No changes to `OxApp` or any TUI code.**

## Validation

- **Unit tests:** `OxBootOptions` parses all headless flags; `EnvironmentKeyring` reads
  env vars; `UrSession` writes correct metrics JSON; `ScenarioLoader` YAML round-trips
  (both synthetic and repository-based scenarios); `WorkspaceBuilder` clones and
  applies overlay correctly; `ValidationRunner` evaluates all rule types; `ResultStore`
  insert + query.
- **Headless smoke test:** `ox --headless --fake-provider hello --turn "hello"` runs to
  completion, exits 0, metrics JSON written alongside session JSONL.
- **End-to-end:** `make evals-build` succeeds. `make evals-run-quick` runs 5 simple
  scenarios, at least one model passes `create-file.yaml`, report is written.

## Open questions

None — all design decisions resolved.
