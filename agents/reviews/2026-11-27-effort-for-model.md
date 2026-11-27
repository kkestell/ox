# Review: check each Claude model's effort levels against Claude's model list

## Scope and coverage

The uncommitted changes in the `effort-for-model` worktree on base `23214bf`
(`git merge-base origin/main HEAD`), against
`agents/plans/2026-11-27-effort-for-model.md` (issue #152). The user answered
the plan's two product questions as it recommends: `oxd` refuses to start when
the configuration offers Claude and Claude Code cannot be run to read its model
list, and refuses a configured Claude model Claude's list does not name.

The change touches harness driving and `oxd` startup but adds no asynchronous
edge beyond one bounded `claude` run, so it was reviewed in this session
without concern reviewers. `docs/functional-requirements.md` is unchanged, as
the plan's Requirement changes say; the new refusals fall under the existing
**Configuration checks** requirement.

Gaps:

- `/ox-work`'s final response, with its Tests proved and Checks, was not passed
  to this review. The checks below were run here instead.

## Summary

| ID | Severity | Category | Action   | Status   | Title                                                                   |
| -- | -------- | -------- | -------- | -------- | ----------------------------------------------------------------------- |
| F1 | High     | Testing  | Fix      | Resolved | The e2e suite restarts `oxd` with Claude configured, which CI cannot run |
| F2 | Medium   | Security | Fix      | Resolved | Discovery runs hooks from the shared temporary directory's settings     |
| F3 | Low      | Testing  | Rejected | —        | No test protects the `supportsEffort` gate                              |

## F1. The e2e suite restarts `oxd` with Claude configured, which CI cannot run

High · Testing · Fix · Resolved · `cmd/ox/web/e2e/session.spec.ts:1531`

"a session whose harness is no longer offered shows its settings" restarts
`oxd` with `haikuConfig`, which offers Claude. `oxd` now runs `claude` at
startup to read its model list and refuses to start without it. The CI e2e job
runs on `ubuntu-latest`, which has no `claude`, so this test fails in CI and
blocks the pull request. It passes on a machine with Claude Code installed,
which is why a local run misses it.

### Evidence

Run with `/Users/kyle/.local/bin` (where `claude` lives) removed from `PATH`:

```
npx playwright test e2e/session.spec.ts -g "harness is no longer offered"
  Error: .../ox-e2e-bin-IHOSS2/oxd exited with status 1
  1 failed
```

`haikuConfig`'s only other user, `approval.spec.ts`, runs only under
`make live`. The Go suite passes without `claude` on `PATH`.

### Action

The test needs a configuration whose harness is not `fake`, not one that needs
Claude. Restart `oxd` with a configuration that offers only Ur (a model with
efforts, permission mode `manual`, defaults and titling on Ur), which the e2e
`oxd` can start because `ur` sits beside it. Keep the test's assertions. It
stays the test that guards the behavior.

## F2. Discovery runs hooks from the shared temporary directory's settings

Medium · Security · Fix · Resolved · `internal/claude/claude.go:146`

`discoverModels` runs `claude -p` in `os.TempDir()` without `--safe-mode`.
Claude reads project settings from its working directory and, in `-p` mode,
runs their hooks without a trust prompt. On Linux, `os.TempDir()` is `/tmp`,
which every local account can write. Any other account on the machine can
create `/tmp/.claude/settings.json` with a `SessionStart` hook, and `oxd` runs
it as the user on every start. `Ask`, which runs in the same directory, passes
`--safe-mode` for this reason. Discovery also runs the user's own
`SessionStart` hooks on every `oxd` start, which `--safe-mode` skips.

### Evidence

In a scratch directory holding `.claude/settings.json` with a `SessionStart`
hook that touches a file, the discovery command ran the hook; the same command
with `--safe-mode` did not. With Claude Code 2.1.332, the `models` list is
identical with and without `--safe-mode` (every `value`, `resolvedModel`, and
`supportedEffortLevels` compared).

### Action

Add `--safe-mode` to discovery's arguments, as `Ask` does. Update "Model
discovery alone" in `docs/harnesses/claude-code.md` to the command with
`--safe-mode`, and replace "SessionStart hooks run first" with the observed
fact that `--safe-mode` lists the same models and runs no hooks (2.1.332). No
new test: a stand-in cannot observe which settings real Claude reads, and the
argument list is implementation.

## F3. No test protects the `supportsEffort` gate

Low · Testing · Rejected · `internal/claude/claude.go:189`

Removing `if model.SupportsEffort` changes no test result, because every fixture
model without `supportsEffort` also has no `supportedEffortLevels`.

### Action

Rejected. The gate implements the documented field contract
(`supportedEffortLevels` lists the choices for models with `supportsEffort`),
and Claude 2.1.332 sends neither field for Haiku 4.5. A test would need a
response Claude has not been seen to send.

## Checks run

- `make test` (lint, Go with the race detector, Node suites): passed.
- `go test -race -count=1 ./...` with `claude` removed from `PATH`, as CI runs:
  passed.
- The F1 e2e test with `claude` removed from `PATH`: failed, as above.
- `make live`: `TestLive`, `TestLiveUr`, and the approval specs passed.
- Spot-checked removals:
  - The `Start` closure's check: `TestClaudeModelList` fails ("a session at an
    effort its model does not take started").
  - The `resolvedModel` map entry: `TestClaudeModelList` and
    `TestConfigOffersOnlyWhatHarnessesTake` fail.
  - The `supportsEffort` gate: no test fails (F3).
- Live probes with Claude Code 2.1.332: the model list carries every built-in
  ID as an alias's `resolvedModel`, Haiku 4.5 with neither effort field, and
  Opus 4.6 and Sonnet 4.6 with `low` through `max` without `xhigh`.

## Verification 1

Checked the uncommitted changes on base `23214bf` after `/ox-work fix`.

| ID | Status   | Evidence                                                                                                                                                                                                                       |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| F1 | Resolved | The test restarts `oxd` with an Ur-only configuration; with `claude` off `PATH` and no `OPENROUTER_API_KEY` it passes, and it failed the same way before the fix.                                                                 |
| F2 | Resolved | Discovery passes `--safe-mode` (`internal/claude/claude.go:146`); the earlier probe shows `--safe-mode` skips project hooks with the same model list; "Model discovery alone" in `docs/harnesses/claude-code.md` states the command. |

No new findings. Checks run: `make test` passed; `go test -race` for
`internal/claude`, `cmd/oxd`, and `internal/server` with `claude` off `PATH`
passed; `make live` passed (`TestLive`, `TestLiveUr`, the approval specs).

## Verdict

`Ready to commit`: F1 and F2 are resolved.
