# Fix headless terminology: --prompt, --max-iterations, single-prompt evals

## Goal

Fix two intertwined misunderstandings in the headless/eval system:

1. **`maxTurns` caps the wrong thing.** It slices the scripted `turns` list with `.Take()`, which limits how many user messages are sent. The intent was always to cap the agent's ReAct loop iterations — how many times the `AgentLoop.RunTurnAsync` `while (true)` iterates before being cut off. Rename to `--max-iterations` and wire it to `AgentLoop`.

2. **Headless mode accepts multiple `--turn` args.** The eval use case is: one task, one prompt, the agent works until done (or until the iteration cap fires). Multiple user messages per session is a TUI concept. Headless eval sessions have exactly one user message. Rename `--turn` → `--prompt` and enforce exactly one.

3. **Eval scenarios have a multi-value `turns:` list and a `complexity:` field.** Same misunderstanding: each scenario is one task (one prompt) that the agent works on autonomously. `complexity` is subjective and unused at runtime. Replace `turns: [...]` with `prompt: "..."` and remove `complexity:`.

## Desired outcome

- `ox --headless --prompt "Fix the bug." --model foo/bar` — single prompt, agent loops until done.
- `ox --headless --prompt "Fix the bug." --max-iterations 20 --model foo/bar` — same but the agent loop is capped at 20 ReAct iterations (LLM calls within the one turn).
- Eval YAML uses `prompt: "..."` (scalar string) instead of `turns: [...]`.
- `complexity:` key is gone from all YAML files.
- Tests reflect actual semantics: iteration cap tests use the `tool-call` fake scenario (2 LLM calls) to exercise the cap.

## How we got here

The original implementation of `maxTurns` used `.Take(n)` on the scripted turns list, interpreting "turns" as "user messages". But in eval scenarios the whole point is that the agent works autonomously on one task — there's no multi-turn user dialogue. Similarly, `complexity` was added as metadata but was never used at runtime. The user clarified:

- **Turn** = from one user message to the next (includes user message, multiple agent loop iterations, and agent response).
- **Iteration** = one ReAct loop iteration (one LLM call inside `AgentLoop.RunTurnAsync`).
- Headless mode always has exactly one turn (one `--prompt`). `--max-iterations` caps how many ReAct iterations that single turn can make.

## Approaches considered

### Option 1 — Per-turn iteration cap (AgentLoop)

Add `int? maxIterations` to `AgentLoop`. At the top of each `while (true)` loop iteration, increment a counter; yield `TurnError { IsFatal = true }` and break when the cap is exceeded.

- **Pros**: Directly addresses the "spinning" problem. Lives exactly where spinning happens. Natural fit.
- **Cons**: Requires threading the cap through `UrSession` → `UrHost.CreateSession` → `AgentLoop`.

### Option 2 — Pre-loop check in UrSession

Track iteration count in `UrSession` by counting `TurnCompleted` events (LLM calls per session). Check before starting a new agent loop.

- **Pros**: Keeps `AgentLoop` clean.
- **Cons**: `TurnCompleted` fires at the end of a successful loop exit (no tool calls), not once per LLM call. Counting LLM calls at the session level would require a new event type or a mutable counter shared via callback. More coupling for less benefit.

## Recommended approach

**Option 1.** The iteration counter belongs in `AgentLoop` because that's where iteration happens. The threading cost (4 files: `AgentLoop` → `UrSession` → `UrHost` → `HeadlessRunner`) is straightforward and keeps each layer responsible for what it directly controls. `UrSession` stores the cap as a field and passes it to the `AgentLoop` constructor, which is already parameterized for similar concerns (`turnsToKeepToolResults`).

## Related code

- `src/Ur/AgentLoop/AgentLoop.cs` — The `while (true)` loop at line 70; add the counter and cap check here.
- `src/Ur/Sessions/UrSession.cs` — Constructs `AgentLoop` at line 312; thread `_maxIterations` through.
- `src/Ur/Hosting/UrHost.cs` — `CreateSession` at line 169; add `maxIterations` parameter.
- `src/Ox/HeadlessRunner.cs` — Signature changes: `turns: IReadOnlyList<string>` → `prompt: string`, `maxTurns` → `maxIterations`; remove `.Take()` slicing; pass `maxIterations` to `CreateSession`.
- `src/Ox/OxBootOptions.cs` — Rename `Turns` → `Prompt` (scalar string), `MaxTurns` → `MaxIterations`; update CLI parsing (`--turn` → `--prompt`, `--max-turns` → `--max-iterations`).
- `src/Ox/Program.cs` — Validation: `bootOptions.Turns.Count == 0` → `string.IsNullOrEmpty(bootOptions.Prompt)`; update `HeadlessRunner` construction.
- `evals/EvalShared/ScenarioDefinition.cs` — Replace `List<string> Turns` with `string Prompt`; remove `ScenarioComplexity` enum and `Complexity` field; rename `MaxTurns` → `MaxIterations`.
- `evals/EvalShared/ScenarioLoader.cs` — Update raw DTO and mapping; remove `ParseComplexity`; validate `prompt` is non-empty.
- `evals/EvalRunner/ContainerRunner.cs` — Replace `foreach (var turn in scenario.Turns)` loop with a single `--prompt scenario.Prompt` arg; rename `--max-turns` → `--max-iterations`.
- `evals/scenarios/*.yaml` (15 files) — Replace `turns: [...]` with `prompt: "..."` (collapse multi-item lists into a single comprehensive task description); remove `complexity:`.
- `tests/Ur.Tests/HeadlessRunnerTests.cs` — Rewrite tests: remove the "stops after N turns" tests (which tested wrong behavior); add iteration-cap tests using the `tool-call` fake scenario.
- `tests/Ur.Tests/OxBootOptionsTests.cs` — Rename test methods and update YAML snippets for `--prompt` / `--max-iterations`.
- `evals/EvalShared.Tests/ScenarioLoaderTests.cs` — Update all inline YAML snippets; remove complexity-variant tests; add `prompt` field tests.

## Current state

- `AgentLoop.RunTurnAsync` loops unboundedly (`while (true)`) until the LLM stops calling tools or an error occurs. No iteration cap exists.
- `HeadlessRunner` takes `IReadOnlyList<string> turns` and `int? maxTurns`, slices the turns list with `.Take(maxTurns.Value)`.
- `OxBootOptions` has `IReadOnlyList<string> Turns` (from `--turn` repeated args) and `int? MaxTurns` (from `--max-turns`).
- `UrHost.CreateSession` has no iteration cap parameter.
- Eval YAML files have `turns: [list of strings]` and `complexity: simple/medium/complex`.
- `ScenarioDefinition` has `List<string> Turns`, `ScenarioComplexity Complexity`, and `int? MaxTurns`.

## Structural considerations

The change respects existing layer dependencies:
- `AgentLoop` → no new dependencies; adds a constructor parameter, same as `turnsToKeepToolResults`.
- `UrSession` → passes cap to `AgentLoop`; no new dependencies.
- `UrHost` → adds an optional parameter to `CreateSession`; still calls `new UrSession(...)`.
- `HeadlessRunner` → dependency on `UrHost` and the Ur event types is unchanged; signature narrows (list → scalar).
- Eval shared types (`ScenarioDefinition`, `ScenarioLoader`) → rename fields; no new dependencies.

No layer boundaries are violated. The cap flows downward: CLI → boot options → `HeadlessRunner` → `CreateSession` → `UrSession` → `AgentLoop`. This is the natural dependency direction.

## Implementation plan

### Core: iteration cap in AgentLoop

- [ ] **`AgentLoop.cs`**: Add `int? maxIterations = null` to the primary constructor parameter list (after `turnsToKeepToolResults`). Store as `private readonly int? _maxIterations`. At the very top of the `while (true)` block in `RunTurnAsync`, add:
  ```csharp
  if (_maxIterations.HasValue && ++_iterationCount > _maxIterations.Value)
  {
      yield return new TurnError
      {
          Message = $"Stopped: agent iteration limit ({_maxIterations.Value}) reached.",
          IsFatal = true
      };
      yield break;
  }
  ```
  Declare `int iterationCount = 0;` as a local before the loop (not a field — `AgentLoop` is constructed once per user turn, so a field would also work, but a local is cleaner and avoids the misleading `_` prefix that this codebase reserves for instance fields). Update the class-level XML doc to document the parameter.

### Threading the cap

- [ ] **`UrSession.cs`**: Add `int? maxIterations = null` parameter to the internal constructor (after `additionalTools`). Store as `private readonly int? _maxIterations`. Pass `_maxIterations` as the `maxIterations` argument when constructing `AgentLoop` at line 312.

- [ ] **`UrHost.cs`**: Add `int? maxIterations = null` to `CreateSession`. Pass it through to `new UrSession(...)`.

### HeadlessRunner and OxBootOptions

- [ ] **`HeadlessRunner.cs`**: Replace the constructor parameter `IReadOnlyList<string> turns` with `string prompt` and `int? maxTurns` with `int? maxIterations`. Remove the `.Take()` slicing logic. Call `host.CreateSession(callbacks, maxIterations: maxIterations)`. Update `RunAsync` to call `session.RunTurnAsync(prompt, ct)` directly (no foreach loop). Update all XML doc comments.

- [ ] **`OxBootOptions.cs`**: 
  - Rename `IReadOnlyList<string> Turns` → `string? Prompt` (scalar, not a list).
  - Rename `int? MaxTurns` → `int? MaxIterations`.
  - Replace `--turn` case (which accumulated into a list) with `--prompt` case (sets a single string; second occurrence overwrites silently, or could be an error — pick last value to keep parsing simple).
  - Replace `--max-turns` case with `--max-iterations`.
  - Update XML doc comments to reflect the new semantics.

- [ ] **`Program.cs`**: Update the early validation to check `string.IsNullOrWhiteSpace(bootOptions.Prompt)` instead of `bootOptions.Turns.Count == 0`. Update the error message. Update `HeadlessRunner` construction to pass `bootOptions.Prompt!` and `bootOptions.MaxIterations`.

### Eval shared types

- [ ] **`ScenarioDefinition.cs`**:
  - Replace `public required List<string> Turns { get; init; }` with `public required string Prompt { get; init; }`.
  - Remove `public required ScenarioComplexity Complexity { get; init; }`.
  - Remove the `ScenarioComplexity` enum.
  - Rename `public int? MaxTurns { get; init; }` → `public int? MaxIterations { get; init; }`.
  - Update the XML doc comment on `MaxIterations` to reflect "agent loop iteration cap".

- [ ] **`ScenarioLoader.cs`**:
  - In `RawScenario`: replace `public List<string>? Turns { get; set; }` with `public string? Prompt { get; set; }`. Remove `public string? Complexity { get; set; }`. Rename `MaxTurns` → `MaxIterations`.
  - In `MapToDefinition`: replace `Turns = raw.Turns ?? throw ...` with `Prompt = raw.Prompt ?? throw new InvalidOperationException("Scenario 'prompt' is required")`. Remove the `Complexity = ParseComplexity(raw.Complexity)` line. Rename `MaxTurns = raw.MaxTurns` → `MaxIterations = raw.MaxIterations`.
  - Remove the `ParseComplexity` private method entirely.

- [ ] **`ContainerRunner.cs`**:
  - Replace the `foreach (var turn in scenario.Turns)` loop with a single:
    ```csharp
    psi.ArgumentList.Add("--prompt");
    psi.ArgumentList.Add(scenario.Prompt);
    ```
  - Replace `--max-turns` with `--max-iterations` in the `MaxTurns`/`MaxIterations` block.
  - Rename the field reference `scenario.MaxTurns` → `scenario.MaxIterations`.
  - Update the comment above the block.

### YAML scenario files (all 15)

For each file in `evals/scenarios/`:

- [ ] Remove the `complexity:` line.
- [ ] Replace the `turns:` block (a YAML list) with a single `prompt:` scalar. Where the original list had multiple items ("fix X", "run tests", "check for Y"), collapse them into one comprehensive instruction. The agent is expected to do all of it autonomously — no hand-holding needed. For example:
  - `serde-flatten-enum-variants.yaml`: merge the three turns into a single prompt that asks to fix the derive macro, run the tests, and check for related issues.
  - Scenarios that had only 2 turns ("do X" + "run tests"): fold "run the tests" into the main prompt as "then run the tests to confirm."
- [ ] Rename `max_turns:` → `max_iterations:` if present (none of the current files have it, but apply for future correctness).

Files to update:
  `attrs-cached-property-slots.yaml`, `attrs-optional-pipe.yaml`, `bubbletea-init-panic-deadlock.yaml`, `clap-bash-completion-double-underscore.yaml`, `click-semver-default.yaml`, `effect-match-tag-nullish.yaml`, `effect-stream-decode-text.yaml`, `fastify-uint8array-view.yaml`, `gh-release-limit-zero.yaml`, `nodatime-localtime-seconds.yaml`, `rack-nil-accept-header.yaml`, `requests-proxy-auth-stripped.yaml`, `ripgrep-alternation-regression.yaml`, `serde-flatten-enum-variants.yaml`, `sinatra-content-type-integer.yaml`.

### Tests

- [ ] **`OxBootOptionsTests.cs`**:
  - Rename all `MaxTurns` references to `MaxIterations`.
  - Rename `--max-turns` to `--max-iterations` in all test inputs.
  - Rename `Parse_MaxTurnsFlag_ParsesPositiveInteger` → `Parse_MaxIterationsFlag_ParsesPositiveInteger`, etc.
  - Replace `--turn` with `--prompt` in all test inputs. Rename `Turns` → `Prompt` in assertions.
  - Update `Parse_FullArgs_AllFlagsPresent` and similar tests.

- [ ] **`HeadlessRunnerTests.cs`**: Rewrite entirely.
  - Remove all tests that tested the `.Take()` slicing behavior (`RunAsync_MaxTurnsBelowCount_StopsAfterLimit`, `RunAsync_MaxTurnsOne_ProcessesExactlyOneTurn`, `RunAsync_MaxTurnsExceedsCount_ProcessesAll`).
  - Keep and update `RunAsync_NoMaxIterations_SinglePromptCompletes` (was `NoMaxTurns_ProcessesAllProvidedTurns`): use `hello` scenario (1 LLM call), `maxIterations: null`, expect `metrics.Turns == 1`.
  - Drop `RunAsync_EmptyPrompt_RunsNothing` (was `MaxTurnsWithEmptyTurnsList_RunsNothing`). Empty-prompt validation moves to `Program.cs`, which rejects blank input before constructing `HeadlessRunner`. `HeadlessRunner` itself should never receive an empty prompt in production, and passing `""` in a unit test would call `session.RunTurnAsync("")` — not a safe no-op. This edge case is already covered by the updated `OxBootOptionsTests` validation path.
  - Add `RunAsync_MaxIterationsNotReached_Completes`: use `tool-call` scenario (2 LLM calls), `maxIterations: 3`, expect `metrics.Turns == 1` (TurnCompleted fires once).
  - Add `RunAsync_MaxIterationsExceeded_ReturnsError`: use `tool-call` scenario (2 LLM calls), `maxIterations: 1`, expect exit code 1 (fatal TurnError after 1st LLM call).

- [ ] **`ScenarioLoaderTests.cs`**:
  - Update all inline YAML: replace `turns: [...]` with `prompt: "..."` and remove `complexity:` lines.
  - Remove `Load_ComplexityVariants_AllParse` test (complexity no longer exists).
  - Rename `Load_MaxTurnsField_ParsesValue` → `Load_MaxIterationsField_ParsesValue`; update YAML to use `max_iterations:`.
  - Rename `Load_NoMaxTurnsField_DefaultsToNull` → `Load_NoMaxIterationsField_DefaultsToNull`.
  - Update `Load_MissingRequiredField_Throws` to use YAML without `prompt:` (not `turns:`).

## Validation

- Run `dotnet test` from the repo root — all tests must pass.
- Manually check that `ox --headless --prompt "hello" --model fake/hello` runs and exits 0.
- Verify a scenario YAML loads correctly: `dotnet run --project evals/EvalRunner -- --dry-run evals/scenarios/sinatra-content-type-integer.yaml` (or equivalent).
