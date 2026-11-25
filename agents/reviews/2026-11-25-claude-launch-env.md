# Review: Keep Claude on the effort, provider, and account Ox chooses

## Scope and coverage

Reviewed the uncommitted changes on `claude-launch-env` against base
`cb36c7c7072395e416f4fbad6b2e5f3bd6d35a01`: `docs/harnesses/claude-code.md`,
`internal/claude/ask.go`, `internal/claude/ask_test.go`,
`internal/claude/claude.go`, and `internal/claude/claude_test.go`. Checked
against the plan
[2026-11-25-claude-launch-env](../plans/2026-11-25-claude-launch-env.md), which
resolves #105.

The change delivers the plan's Goal and keeps its Decisions. `launchEnv` is the
one rule for Claude's environment. `startProcess` and `Ask`, the only two places
that start `claude`, both use it. Names are compared before the first `=`, as
the plan's Matching decision requires. The plan makes no requirement changes,
and `docs/functional-requirements.md` is unchanged. **Settings shown are
settings used**, **Titling model**, and **Subscription billing** hold for every
start that inherits from `oxd`'s environment. The change adds no state, timer,
wait, or reply path.

Gaps:

- The provider and credential variables are checked against the Claude Code
  2.1.331 binary's strings, not observed live, as the plan states.
- `make live` was not rerun in this review. The implementer reports it passed.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                          |
| -- | -------- | ----------- | -------- | ------ | -------------------------------------------------------------- |
| F1 | Medium   | Settings    | Record   | Resolved | A settings file's `env` block overrides the effort Ox passes   |
| F2 | Low      | Testing     | Fix      | Resolved | No test guards the `ANTHROPIC_` prefix                         |
| F3 | Low      | Conventions | Fix      | Resolved | `TestAsk` doc comment is not rewrapped                         |
| F4 | Low      | Security    | Rejected | —      | Gateway file-descriptor and auth-snapshot credentials are kept |
| F5 | Low      | Correctness | Rejected | —      | Claude's tools lose every `ANTHROPIC_` variable                |

## F1. A settings file's `env` block overrides the effort Ox passes

Medium · Settings · Record · Resolved · `internal/claude/claude.go:124`

Claude Code applies the `env` block of its settings files to itself after it
starts. `CLAUDE_CODE_EFFORT_LEVEL` set in the workspace's `.claude/settings.json`
(and, by the same mechanism, user or local settings) overrides `--effort` and
the `apply_flag_settings` effort, so the session runs at an effort other than
the one the page shows. The same path can carry `CLAUDE_CODE_USE_BEDROCK` or an
API key. `launchEnv` cannot reach these variables, because they never pass
through `oxd`'s environment.

This is not new. The plan scopes #105 to `oxd`'s environment, and the change
meets that scope. The defect remains for **Settings shown are settings used**
and **Subscription billing**.

### Evidence

A live Haiku run (`claude -p --model claude-haiku-5-5 --effort high`,
`ANTHROPIC_*` and `CLAUDE_CODE_EFFORT_LEVEL` unset) in a temporary directory,
with Bash echoing `$CLAUDE_EFFORT`:

- With `.claude/settings.json` holding
  `{"env":{"CLAUDE_CODE_EFFORT_LEVEL":"low"}}`, it printed `EFFORT=low`.
- Without that file, it printed `EFFORT=high`.

### Action

Open a `bug` issue: "A settings file's `env` block can run Claude at an effort,
provider, or account other than Ox shows."

- **Worst cost:** a session runs at a different effort than shown, or bills
  another provider or an API key instead of the subscription.
- **How it was found:** the live probe above, while reviewing #105.
- **What it waits on:** a live probe of how Ox can override a settings file's
  `env` block. Candidates are an `env` entry in Ox's `--settings`, or
  restricting `--setting-sources`. Which one works is not established.

## F2. No test guards the `ANTHROPIC_` prefix

Low · Testing · Fix · Resolved · `internal/claude/claude_test.go:388`

The plan decides that every variable starting with `ANTHROPIC_` is dropped, so
future variables in those families stay out without a list to maintain.
`TestClaudeStartsWithOxsChoices` sets only real names. An exact-name list would
satisfy it equally. The old assertion, `strings.Contains(data, "ANTHROPIC_")`,
did catch any `ANTHROPIC_` variable. The per-name rewrite lost that.

### Evidence

I replaced `strings.HasPrefix(name, "ANTHROPIC_")` with
`slices.Contains([]string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"}, name)`.
`go test ./internal/claude/ -run 'TestClaudeStartsWithOxsChoices|TestAsk$'`
still passed. The edit was reverted.

### Action

In `TestClaudeStartsWithOxsChoices`, add to `dropped` an `ANTHROPIC_` name that
no list would contain, such as `ANTHROPIC_OX_TEST`. The test then fails when
the prefix rule is replaced by a list.

## F3. `TestAsk` doc comment is not rewrapped

Low · Conventions · Fix · Resolved · `internal/claude/ask_test.go:13`

The first line of the comment grew to 94 characters. The rest of the package
wraps comments at 80.

### Evidence

`awk 'length>80' internal/claude/ask_test.go` reports line 13 at 94 characters.
The line after it is 72.

### Action

Rewrap the comment at 80 columns.

## F4. Gateway file-descriptor and auth-snapshot credentials are kept

Low · Security · Rejected · `internal/claude/claude.go:168`

Claude Code 2.1.331 also reads `CLAUDE_CODE_GATEWAY_TOKEN_FILE_DESCRIPTOR` and
`CLAUDE_BG_AUTH_SNAPSHOT_PATH` as credential sources, and `overrides` keeps
both.

### Action

Rejected as speculative. Claude Code withholds both from its own child
processes, and neither is set by any launcher `oxd` runs under in normal use.
An inherited descriptor variable is also useless without the open descriptor
itself.

## F5. Claude's tools lose every `ANTHROPIC_` variable

Low · Correctness · Rejected · `internal/claude/claude.go:183`

Claude's Bash children inherit Claude's environment, so a project's own scripts
no longer see `ANTHROPIC_BASE_URL` and similar variables when they run in an Ox
session.

### Action

Rejected: this is intended behavior. The plan's Trust boundaries decision
accepts it.

## Checks run

- `go test ./internal/claude/ -run 'TestClaudeStartsWithOxsChoices|TestAsk$'`
  passes on the change.
- Mutation: an exact `ANTHROPIC_` list in place of the prefix still passes
  (F2).
- Mutations reported by the construction reviewer were not repeated here:
  - Restoring the old two-credential rule fails both tests.
  - Matching `overrides` by prefix fails on `CLAUDE_CODE_EFFORT_LEVEL_X`.
  - Removing `CLAUDE_CODE_EFFORT_LEVEL` fails both tests.
  - Passing `Ask` the raw environment fails `TestAsk`.
- `dprint check docs/harnesses/claude-code.md` passes.
- Live Haiku probe of a project settings `env` block (F1).
- From the implementer: `make test` and `make live` pass.
  - One earlier `make live` run failed `TestLive/interrupt` after 185s and
    passed on rerun.
  - The change does not touch interrupt handling or anything that test waits
    on, so this review raises no finding for that failure.
- `make e2e` is not required: the page is unchanged.

## Verification 1

Checked the working tree on `claude-launch-env` at base `cb36c7c`, after the
fixes. The `.claude/skills/ox-review/SKILL.md` edit in the tree belongs to a
separate change and is outside this review.

| ID | Status   | Evidence                                                                                                                         |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | #118 records it, with its worst cost, the live probe, and what it waits on                                                       |
| F2 | Resolved | `ANTHROPIC_OX_TEST` is in `dropped`; with an exact `ANTHROPIC_` list in place of the prefix, `TestClaudeStartsWithOxsChoices` fails |
| F3 | Resolved | The `TestAsk` comment is wrapped at 80 columns                                                                                   |

No new findings.

Checks: `go test -race ./internal/claude/` passes. The mutation was reverted.
`make live` was not rerun, at the parent session's direction. The fixes touch
only tests, so `make live` cannot observe them.

## Verdict

`Ready to commit`. F2 and F3 are fixed, and #118 records F1.
