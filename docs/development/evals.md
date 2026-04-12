# Behavioral Evals

Ox uses behavioral evaluations to test whether the agent can complete realistic
coding tasks end-to-end. Each eval scenario describes a workspace (a real repo
or synthetic files), a sequence of prompts to send the agent, and a set of
validation rules that determine pass/fail after the agent finishes.

## How It Works

At a high level, the eval runner:

1. **Loads** YAML scenario definitions from `evals/scenarios/`.
2. **Expands** each scenario into scenario × model pairs (each scenario lists
   which models to test against).
3. **Builds a workspace** — either clones a Git repo at a pinned commit or
   writes synthetic files to a temp directory.
4. **Runs Ox inside a Podman container** with the workspace mounted, passing
   each turn as a `--turn` argument. The container runs Ox in headless mode
   with YOLO permissions (no approval prompts).
5. **Validates** the workspace state against the scenario's validation rules
   (file existence, file contents, command exit codes, etc.).
6. **Persists** results (pass/fail, token counts, tool call metrics, duration)
   to a SQLite database.
7. **Generates** a Markdown report summarizing results by scenario and model.

All of this runs sequentially to avoid API rate limits and keep output readable.

### Project Structure

```
evals/
├── Containerfile            # Multi-stage build: Ox binary + language toolchains
├── EvalRunner/              # CLI tool that orchestrates eval runs
│   ├── Program.cs           # Entry point, option parsing, run loop
│   ├── ContainerRunner.cs   # Podman invocation, artifact extraction, validation
│   ├── WorkspaceBuilder.cs  # Git clone or synthetic file creation
│   ├── ResultStore.cs       # SQLite persistence via Dapper
│   └── ReportGenerator.cs   # Markdown report writer
├── EvalShared/              # Shared types (no Ur dependency)
│   ├── ScenarioDefinition.cs
│   ├── ValidationRule.cs
│   ├── ScenarioLoader.cs    # YAML → typed scenario deserialization
│   ├── ValidationRunner.cs  # Evaluates rules against workspace
│   └── EvalResult.cs
├── EvalShared.Tests/        # Unit tests for shared components
└── scenarios/               # YAML scenario definitions
    ├── ripgrep-alternation-regression.yaml
    ├── attrs-optional-pipe.yaml
    └── ...
```

## Prerequisites

### 1. Podman

The eval runner uses Podman to run each scenario in an isolated container.
Install Podman before running evals:

```bash
# Fedora / RHEL
sudo dnf install podman

# Ubuntu / Debian
sudo apt install podman

# macOS
brew install podman
podman machine init
podman machine start
```

### 2. API Keys

API keys are passed to the container via environment variables. The naming
convention is `UR_API_KEY_{PROVIDER}` where the provider name is uppercased
and non-alphanumeric characters are replaced with underscores. For example:

| Provider in `providers.json` | Environment variable       |
| ---------------------------- | -------------------------- |
| `openai`                     | `UR_API_KEY_OPENAI`        |
| `google`                     | `UR_API_KEY_GOOGLE`        |
| `openrouter`                 | `UR_API_KEY_OPENROUTER`    |
| `zai-coding`                 | `UR_API_KEY_ZAI_CODING`    |

Set these in a `.env` file at the project root (the Makefile sources it
automatically):

```bash
# .env
UR_API_KEY_OPENAI=sk-...
UR_API_KEY_GOOGLE=AIza...
UR_API_KEY_OPENROUTER=sk-or-...
UR_API_KEY_ZAI_CODING=...
```

The eval runner passes **all** `UR_API_KEY_*` variables into the container, so
any provider you configure will have access to its key. You only need keys for
the providers whose models appear in the scenarios you're running.

### 3. Container Image

Build the eval container image before your first run (or after code changes):

```bash
make evals-build
```

This builds the `ox-eval` image, which contains:
- The Ox binary (published self-contained from source)
- Language toolchains (Rust, Python, Node.js, Go, Ruby) needed by scenarios
- Common test runners (`pytest`, `cargo`, etc.)

## Running Evals

### Run all scenarios

```bash
make evals-run
```

This builds the container image (if not already built), sources your `.env`
file, and runs every scenario against every model listed in the scenario's
`models` field.

If a run crashes or leaves containers running, stop them with `make evals-stop` (stops and removes any containers started from the `ox-eval` image).

### Run quick (simple scenarios only)

```bash
make evals-run-quick
```

Runs only scenarios with `complexity: simple` — useful for fast pre-merge checks.

### Run with filters

The eval runner supports several CLI flags for targeted runs:

```bash
# Run against specific models (overrides scenario model lists)
dotnet run --project evals/EvalRunner -- --models google/gemini-3.1-flash-lite-preview

# Filter by scenario name (substring match)
dotnet run --project evals/EvalRunner -- --filter ripgrep

# Filter by complexity
dotnet run --project evals/EvalRunner -- --complexity simple

# Skip the Markdown report
dotnet run --project evals/EvalRunner -- --no-report

# Custom SQLite database path (default: evals/results/evals.db)
dotnet run --project evals/EvalRunner -- --db /tmp/my-evals.db

# Custom providers.json path (default: providers.json)
dotnet run --project evals/EvalRunner -- --providers ./my-providers.json
```

### CLI Options Reference

| Flag                | Default               | Description                                        |
| ------------------- | --------------------- | -------------------------------------------------- |
| `--scenarios <dir>` | `evals/scenarios/`    | Directory containing YAML scenario files            |
| `--complexity <t>`  | *(all)*               | Filter by complexity: `simple`, `medium`, `complex` |
| `--filter <text>`   | *(all)*               | Filter by scenario name (substring match)           |
| `--models <m1,m2>`  | *(from scenario)*     | Override model list for all scenarios               |
| `--providers <path>`| `providers.json`      | Path to providers config                            |
| `--db <path>`       | `evals/results/evals.db` | SQLite database path                             |
| `--report`          | *(default: on)*       | Write Markdown report after run                     |
| `--no-report`       |                       | Skip report generation                              |

## Results

### SQLite Database

Results are persisted to `evals/results/evals.db`. The schema has two tables:

- **`eval_runs`** — one row per scenario × model run, with metrics (tokens,
  tool calls, duration) and pass/fail status.
- **`validation_failures`** — individual rule failures linked to a run.

You can query it directly:

```bash
sqlite3 evals/results/evals.db \
  "SELECT scenario_name, model, passed, duration_seconds FROM eval_runs ORDER BY timestamp DESC LIMIT 20;"
```

### Markdown Reports

After each run (unless `--no-report`), a Markdown report is written to
`evals/results/report-YYYY-MM-DD.md`. It includes:

- A results table grouped by scenario × model with pass rate, average turns,
  token counts, tool error rate, and duration.
- A per-model summary aggregating across all scenarios.
- A list of recent failures with error messages.

## Scenario Format

Scenarios are YAML files in `evals/scenarios/`. Here's an annotated example:

```yaml
# Human-readable identifier (required).
name: ripgrep-alternation-regression

# Complexity tier for filtering: simple, medium, complex (required).
complexity: complex

# Models to test against. Each model generates a separate eval run (required).
# Use the full "provider/model" format that Ox uses.
models:
  - google/gemini-3.1-flash-lite-preview
  - zai-coding/glm-4.5-air

# ── Workspace source (choose one) ──────────────────────────────────

# Option A: Clone a real repo at a pinned commit.
# The commit should point to a known-broken state so the eval is reproducible.
repository:
  url: https://github.com/BurntSushi/ripgrep
  commit: 6c5108ed17987531644518fac8c1659b0b202611
  fix_commit: 9d738ad0c009e6632d75fa3d36051e5ae7f7cce6  # optional, metadata only

# Option B: Write synthetic files (no git clone). Use for simple scenarios
# that don't need a full repo.
# workspace_files:
#   - path: src/main.py
#     content: |
#       def broken():
#           return 1 / 0

# ── Agent turns (required) ─────────────────────────────────────────

# Each turn is sent to the agent as a separate prompt, in order.
# The agent processes all turns sequentially within a single session.
turns:
  - "Case-insensitive alternation patterns like 'foo|bar' with -i produce
     false negatives. Investigate the regex crate's literal extraction and fix it."
  - "Run the regex crate tests to confirm the fix."

# ── Validation (required) ──────────────────────────────────────────

# Rules checked against the workspace after all turns complete.
# A scenario passes only if ALL rules pass.
validation_rules:
  - type: command_succeeds
    command: cargo test -p regex

# Maximum time for the container run, in seconds (default: 120).
timeout_seconds: 600
```

### Validation Rule Types

| Type                       | Fields              | Description                                           |
| -------------------------- | ------------------- | ----------------------------------------------------- |
| `file_exists`              | `path`              | Asserts the file exists in the workspace               |
| `file_not_exists`          | `path`              | Asserts the file does not exist                        |
| `file_contains`            | `path`, `content`   | Asserts the file contains the exact string             |
| `file_matches`             | `path`, `pattern`   | Asserts the file contents match a regex pattern        |
| `command_succeeds`         | `command`           | Asserts the shell command exits with code 0            |
| `command_output_contains`  | `command`, `output` | Asserts a successful command's stdout contains a string |

File paths are relative to the workspace root. Path traversal (e.g., `../../etc/passwd`)
is rejected.

Commands run via `bash -c "{command}"` with a 15-second timeout, CWD set to the
workspace root.

## Adding New Evals

### 1. Find or Create a Bug

The best eval scenarios come from real bugs — either from the project's issue
tracker or from your own experience. Good candidates:

- **Library bugs** where a specific input produces incorrect output (e.g.,
  `rack` returns wrong content-type for nil, `requests` strips proxy auth).
- **Regression bugs** where a previously working feature breaks.
- **Edge cases** in parsing, encoding, or type handling.

### 2. Pin the Broken State

For repo-based scenarios, find the exact commit where the bug exists. This is
typically the commit *before* the fix was applied. If a fix commit exists,
record it as `fix_commit` for reference (it's metadata only — never used at
runtime).

### 3. Write the Scenario YAML

Create a new `.yaml` file in `evals/scenarios/`. Name it descriptively:
`{project}-{brief-issue-description}.yaml`.

Choose the right workspace mode:

- **`repository`** — for scenarios that need a real project with build system,
  dependencies, and test suite. Most common for medium/complex scenarios.
- **`workspace_files`** — for simple scenarios that only need a few files. Avoids
  the overhead of cloning a full repo.

### 4. Choose Models

List the models that should be tested. Use the full `provider/model` format.
Consider which models are cost-effective enough for regular eval runs — cheap/fast
models for `simple` scenarios, more capable models for `complex` ones.

### 5. Write Validation Rules

Design rules that confirm the bug is actually fixed:

- **`command_succeeds`** is the gold standard — if the project's own test suite
  passes, the fix is correct. This is the most reliable validation.
- **`file_contains`** / **`file_matches`** are useful for checking that specific
  code changes were made (e.g., a null check was added).
- **`file_exists`** / **`file_not_exists`** for checking that files were created
  or cleaned up.
- Combine multiple rules for thorough validation — all rules must pass.

### 6. Set a Realistic Timeout

- `simple` scenarios: 60–120 seconds
- `medium` scenarios: 120–300 seconds
- `complex` scenarios: 300–600 seconds

The timeout is enforced by Podman — the container is killed if it exceeds the
limit. The default is 120 seconds.

### 7. Test Your Scenario

Run your new scenario in isolation to verify it works:

```bash
dotnet run --project evals/EvalRunner -- --filter your-scenario-name --models provider/model
```

Check the output for pass/fail status and review any validation failures.
Iterate on the turns and rules until the scenario passes reliably with a
capable model.

### 8. Update the Container Image (if needed)

If your scenario requires a language toolchain or test runner not already in the
container image, add it to `evals/Containerfile` and rebuild:

```bash
make evals-build
```

The current image includes: Rust (via rustup), Python 3 + pytest, Node.js + npm,
Go, Ruby + Bundler, and standard C/C++ build tools.

## Tips

- **Reproducibility** is critical. Always pin repo scenarios to an exact commit.
  Synthetic scenarios with `workspace_files` are inherently reproducible.
- **Keep turns focused.** Each turn should have a clear, single objective.
  Avoid vague or overly broad prompts.
- **Prefer `command_succeeds`** over file content checks when possible — passing
  the project's own test suite is the strongest signal of a correct fix.
- **Run evals regularly** to catch regressions in Ox's agent behavior across
  code changes.
- **Cost awareness:** evals make real API calls. Use `--filter` and `--models`
  to run only what you need during development. Full runs are best suited for
  CI or periodic manual runs.