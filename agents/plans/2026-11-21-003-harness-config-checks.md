# Harness configuration checks on `agent.Harness`

## Goal

`checkConfig` in `cmd/oxd` decides which configuration rules apply to which harness by switching on its name: Claude refuses the `default` alias and checks efforts with `claude.CheckEffort`, Ur checks permission modes and efforts with its own functions, and every harness but `ur` refuses a model with `providers`. Each rule belongs to its harness, but `cmd/oxd` chooses it. From audit `agents/audits/2026-11-20-003-since-first-audit.md` F3.

When done, `agent.Harness` carries each harness's configuration checks beside `CheckPrompt`, each harness value supplies its own, and `checkConfig` runs them without naming any harness. What the daemon accepts and refuses at startup is unchanged.

## Related code

- `cmd/oxd/main.go:117-156` — `checkConfig`, with the name switch and the providers rule.
- `cmd/oxd/main.go:46-50` — the harness map; Ur receives its provider orders from `cfg.Harnesses["ur"].Providers()`.
- `internal/agent/agent.go:59-65` — `Harness`, with `Start` and `CheckPrompt`.
- `internal/claude/prompt.go:12` — `claude.Harness`.
- `internal/claude/claude.go:110-130` — Claude's `efforts`, `CheckEffort`, and its call in `startProcess`.
- `internal/ur/ur.go:22-56` — Ur's `efforts`, `CheckEffort`, `CheckPermissionMode`, and `Harness`.
- `internal/config/config.go:38-39`, `:161-169` — `Model.Providers` and `Harness.Providers`, unchanged.
- `cmd/oxd/main_test.go` — `TestConfigOffersOnlyWhatHarnessesTake` and `TestConfigRefusesClaudesDefaultPermissionModeAlias`.

## Decisions

- **Shape of the checks.** `agent.Harness` gains three fields:
  - `CheckEffort func(string) error` — reports whether the harness takes an effort level; nil takes every level.
  - `CheckPermissionMode func(string) error` — reports whether the harness takes a permission mode; nil takes every mode.
  - `TakesProviders bool` — whether a model may set a provider order.

  They take strings rather than a `config.Harness`, so `internal/agent` gains no import of `internal/config`, and nil follows `CheckPrompt`'s existing meaning. `checkConfig` is their one reader.
- **Claude's alias rule moves into Claude's permission mode check.** `claude.Harness.CheckPermissionMode` refuses `default` with `use permission mode "manual" instead of "default" for Claude` and takes every other mode, as the switch does today.
- **The providers rule stays generic.** `checkConfig` refuses a model with `providers` on any harness whose `TakesProviders` is false, with `model %q: harness %q takes no providers`. Only `ur.Harness` sets it. The fake harness keeps nil checks and `TakesProviders` false, so it refuses providers as today. Ur still receives its provider orders through `ur.Harness(endpoint, providers)`.
- **Check functions become unexported.** With `cmd/oxd` no longer calling them, `claude.CheckEffort` becomes `checkEffort` (still called by `startProcess`), and `ur.CheckEffort` and `ur.CheckPermissionMode` become `checkEffort` and `checkPermissionMode`.
- **Error wrapping.** `checkConfig` wraps a permission mode error as `harness %q: %w` and an effort error as `model %q: %w`, as today for efforts. `main` already prefixes the configuration path.
- **Parallel plans.** F1 edits the `Agent` side of `internal/agent` and the claude, fake, and ur agents; this plan edits only the `Harness` struct in `internal/agent/agent.go`, `claude.Harness` and the effort check in `internal/claude`, and the check functions and `Harness` constructor at the top of `internal/ur/ur.go`. Where F1 has already changed a nearby line, keep its change.
- **Requirements:** none touched. The checks run once at startup, before any page connects.
- **Settings:** each harness still refuses at startup an effort level or permission mode it would silently ignore or default. The rules move unchanged.
- **Construction:** the rule "harness X takes these choices" has one source, X's `agent.Harness` value. `cmd/oxd` holds no harness-specific rule, so adding a harness with rules adds no case to `checkConfig`.
- **Claude Code:** no new assumption. Claude's effort levels and its `default` alias for manual are already in the code, and they move as they are.

## Check your assumptions

None.

## Test plan

- A harness's own checks run without `checkConfig` naming the harness. `cmd/oxd` test: extend `TestConfigOffersOnlyWhatHarnessesTake` with a harness `"picky"` in its harness map, `{Start: fake.Start, CheckEffort: refuse, CheckPermissionMode: refuse}`, where `refuse` returns an error. Add two cases: a `picky` model offering any effort, and `picky` offering any permission mode. Both are refused. Both fail on the current code, which applies no rules to an unnamed harness.
- The existing cases keep covering each harness's rules through its value: a Claude effort Claude does not take (fails if `claude.Harness` drops `CheckEffort`), an Ur effort and an Ur permission mode (fail if `ur.Harness` drops either check), a provider order on a Claude model (fails if `checkConfig` drops the `TakesProviders` rule), and the built-in configuration accepted (fails if `ur.Harness` drops `TakesProviders: true`, since the built-in Ur models list providers).
- `TestConfigRefusesClaudesDefaultPermissionModeAlias` keeps covering the alias, now through `claude.Harness.CheckPermissionMode`.

## Implementation plan

1. `internal/agent/agent.go`: add `CheckEffort`, `CheckPermissionMode`, and `TakesProviders` to `Harness`, each with a one-line comment as above. Update the type's comment to say a harness may check user prompts and the configuration offered for it.
2. `internal/claude/claude.go`: rename `CheckEffort` to `checkEffort` and update its call in `startProcess`. Add `checkPermissionMode`, which refuses `default` with the error above.
3. `internal/claude/prompt.go`: set `CheckEffort: checkEffort` and `CheckPermissionMode: checkPermissionMode` on `claude.Harness`.
4. `internal/ur/ur.go`: rename `CheckEffort` and `CheckPermissionMode` to `checkEffort` and `checkPermissionMode`. Set them and `TakesProviders: true` on the `agent.Harness` that `Harness` returns.
5. `cmd/oxd/main.go`: rewrite `checkConfig` to loop over each configured harness, refusing an unknown harness, then running its `CheckPermissionMode` on each permission mode, the `TakesProviders` rule on each model, and its `CheckEffort` on each effort, skipping nil checks. Remove the `claude` and `ur` imports if nothing else uses them (`claude.WaitAll` and `claude.KillAll` keep `claude`; `ur.Harness` and `ur.Endpoint` keep `ur`). Update the doc comment: it refuses a harness oxd does not have, or a choice its harness does not take.
6. `cmd/oxd/main_test.go`: add the `picky` harness and its two cases.
7. `docs/architecture.md`: edit the two passages below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`: the opening says `cmd/oxd` gives the server each harness "with its `Start` function and any prompt validation"; it becomes "with its `Start` function and its prompt and configuration checks". The `agent` row adds the `Harness` checks: "The `Agent` interface a harness implements, its `Start` function, prompt and configuration checks, and options, and the updates, snapshots, settings, and errors they share."
