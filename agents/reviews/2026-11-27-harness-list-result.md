# Review: serve only the catalog from harness/list, and correct the protocol's configuration passages

## Scope and coverage

Base `1e7b9c1` (`git merge-base origin/main HEAD`, also `HEAD`). Reviewed the uncommitted working tree on branch `harness-list-result`: `internal/server/methods.go`, `internal/server/mcp.go`, `internal/server/methods_test.go`, the new `docs/configuration.md`, and the edits to `docs/protocol.md`, `docs/architecture.md`, `docs/functional-requirements.md`, and `README.md`. Checked against `agents/plans/2026-11-27-harness-list-result.md`. The `AGENTS.md` entries for `docs/configuration.md` are left out on purpose; the top-level agent lands them on `main` after the merge.

The change touches no state owner, persistence, or harness driving, and narrows one protocol result. It was reviewed in this session with no concern reviewers.

Gaps: none material.

## Summary

| ID | Severity | Category      | Action | Status | Title                                                      |
| -- | -------- | ------------- | ------ | ------ | ---------------------------------------------------------- |
| F1 | Low      | Documentation | Fix    | Resolved | The configuration doc drops which harness names oxd accepts |

## F1. The configuration doc drops which harness names oxd accepts

Low · Documentation · Fix · `docs/configuration.md:9`

The removed `## Configuration` section said "The daemon validates harness names at startup." `docs/configuration.md` does not carry that fact over. It never says that the keys under `harnesses` must be `claude` or `ur`, and its startup sentence ("`oxd` refuses to start when a default or titling choice is not configured, or when a Claude effort level is one Claude does not take") reads as the whole list of startup refusals. A user who writes their own file from this page and names a harness anything else gets `unknown harness` at startup with no doc that states the rule. The plan's outline for the file left the rule out, and the implementer followed the outline.

### Evidence

`cmd/oxd/main.go` `checkConfig` refuses any key in `cfg.Harnesses` with no matching harness (`unknown harness %q`); `newHarnesses` offers only `claude`, `fake`, and `ur`. `git diff -- docs/protocol.md` shows the sentence removed, and `grep -n 'harness names\|unknown harness' docs/` finds no replacement.

### Action

In `docs/configuration.md`, say that `harnesses` holds the harnesses by name, `claude` for Claude Code and `ur` for Ur, and add a harness other than those to the startup sentence's refusals. Documentation only; no test.

## Checks run

- `make test`: passed (Go packages and the Node suites).
- `dprint check`: passed.
- `TestHarnessListServesCatalog` with `harness/list` replying `s.config` again: fails, listing `max_child_depth`, `tailscale_ip`, and `titling`. Restored.
- `listModels` marshaling `s.config` again: `TestForwarder` in `cmd/ox-mcp` fails on the `list_models`/`harness/list` comparison. Restored.
- The JSON example in `docs/configuration.md` passed through `config.Load` in a temporary test: loads. Test removed.
- Requirement changes: "Session labels" and "Effort across models" match the plan's approved texts word for word.
- Doc claims traced to code: `harness.Effort` (first effort level, or empty), `labelFrom`/`leadingLine` (first nonblank line of the first prompt with text), `forkPoint` (merge base, or the empty tree), `server.New` (titling harness must have `Ask`). Anchors `protocol.md#session-updates`, `architecture.md#web-ui`, and `harnesses/claude-code.md#initialize-first` exist. No link to `#configuration` remains outside `agents/`.
- `make e2e` and `make live`: skipped; the page reads only `harnesses` and `defaults`, and Claude driving is unchanged, so neither can observe the change.

## Verification 1

Working tree on base `1e7b9c1`, with only `docs/configuration.md` changed since the review; the other files' diffs are unchanged. `dprint check` passed.

| ID | Status   | Evidence                                                                                                                                                |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `docs/configuration.md` names the harness keys `claude` and `ur`, and its startup sentence adds a refusal for any other harness, matching `checkConfig` |

No new findings.

## Verdict

Ready to commit.
