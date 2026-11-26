# Keep Claude on the effort, provider, and account Ox chooses

## Goal

Ox starts Claude with its own environment minus `ANTHROPIC_API_KEY` and
`ANTHROPIC_AUTH_TOKEN`. Claude Code reads other variables that override what Ox
passes: an inherited `CLAUDE_CODE_EFFORT_LEVEL` beats `--effort` and the
`apply_flag_settings` effort change, and `CLAUDE_CODE_USE_BEDROCK`, `_VERTEX`,
`_FOUNDRY`, and their siblings send Claude to another provider's billing. Titling
starts Claude with the same environment.

When the change is done, neither a session's Claude nor titling's Claude sees
any variable from Ox's environment that changes the effort, model, provider, or
account Ox chooses, and the user's own subscription login (`CLAUDE_CONFIG_DIR`,
`CLAUDE_CODE_OAUTH_TOKEN`) still reaches Claude.

Resolves #105, from
[F1](../audits/2026-11-25-ox-sweep.md#f1-claude-inherits-settings-that-override-the-effort-and-billing-ox-shows).

## Related code

- `internal/claude/claude.go:168` — `subscriptionEnv`, the one function that
  builds Claude's environment, used by `startProcess` (`:126`) and `Ask`.
- `internal/claude/claude.go:110` — `--effort`, which the inherited variable
  overrides.
- `internal/claude/configure.go:32` — the `apply_flag_settings` effort change,
  which the inherited variable also overrides ("CLAUDE_CODE_EFFORT_LEVEL
  overrides effort for this session" in Claude Code 2.1.331).
- `internal/claude/ask.go:30` — titling's environment.
- `internal/claude/claude_test.go:386` — `TestClaudeStartsWithoutAnAPIKey`.
- `internal/claude/ask_test.go:15` — `TestAsk`, which checks the API key is
  unset.
- `internal/server/live_test.go:461` — the live test reads `CLAUDE_CONFIG_DIR`
  to find Claude's transcripts, so Claude must keep inheriting it.

## Decisions

- **One rule, one place.** Rename `subscriptionEnv` to `launchEnv` and make it
  the single definition of what Claude inherits. It drops:
  - every variable whose name starts with `ANTHROPIC_`. Each one Claude Code
    2.1.331 reads is a credential (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
    `ANTHROPIC_UNIX_SOCKET`, `ANTHROPIC_FOUNDRY_API_KEY`), an endpoint
    (`ANTHROPIC_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`, …), a provider's
    configuration (`ANTHROPIC_VERTEX_PROJECT_ID`, …), or a model choice
    (`ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_*_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`).
    A prefix keeps future variables of the same families out without a list
    to maintain.
  - a named list, `overrides`, of the `CLAUDE_*` variables that change Ox's
    choices:
    - effort: `CLAUDE_CODE_EFFORT_LEVEL`;
    - provider: `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`,
      `CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_USE_MANTLE`,
      `CLAUDE_CODE_USE_ANTHROPIC_AWS`, `CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD`,
      `CLAUDE_CODE_USE_GATEWAY`, `CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST`,
      `CLAUDE_CODE_API_BASE_URL`;
    - account: `CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR` (an API key, billed
      instead of the subscription).

  `CLAUDE_*` is not dropped wholesale: `CLAUDE_CONFIG_DIR` and
  `CLAUDE_CODE_OAUTH_TOKEN` choose which subscription login Claude uses, and the
  user's other Claude Code tuning (retries, timeouts, proxies) does not touch
  what Ox shows. `CLAUDE_CODE_USE_` is not a prefix either, since
  `CLAUDE_CODE_USE_POWERSHELL_TOOL` and others share it.
- **Matching.** `launchEnv` compares the variable's name, the text before the
  first `=`, so `CLAUDE_CODE_EFFORT_LEVEL_X` is not caught by
  `CLAUDE_CODE_EFFORT_LEVEL`.
- **Requirements:** **Settings shown are settings used**, **Titling model**, and
  **Subscription billing** hold for every start: new, resumed, restarted after
  parking, and every titling run, since all go through `launchEnv`. Effort
  changes applied with `apply_flag_settings` take effect because nothing in
  Claude's environment overrides them.
- **Settings:** with no `--effort` (Ox's effort empty), Claude runs at its
  default instead of an inherited level, which is what an empty effort means.
- **Construction:** the invariant "Claude's environment holds nothing that
  overrides Ox's choices" has one source, `launchEnv`; `startProcess` and `Ask`
  take its result and add only Ox's own variables.
- **Claude Code:** `CLAUDE_CODE_EFFORT_LEVEL` overriding `--effort` was observed
  live on 2.1.331 with Haiku: with `CLAUDE_CODE_EFFORT_LEVEL=low` and
  `--effort=high`, Bash's `CLAUDE_EFFORT` (Claude's active effort) read `low`;
  without the variable it read `high`. The provider and credential variables
  rest on Claude Code 2.1.331's own strings (its auth-source list and provider
  checks) and are not observed live. Both go into
  `docs/harnesses/claude-code.md` ("Environment").
- **Trust boundaries:** the change only narrows what Claude inherits from
  `oxd`'s environment. Claude's tools inherit Claude's environment, so they
  also stop seeing these variables.

## Check your assumptions

None.

## Test plan

- Claude harness: extend `TestClaudeStartsWithoutAnAPIKey` in
  `internal/claude/claude_test.go`, renamed `TestClaudeStartsWithOxsChoices`. It
  sets `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`,
  `ANTHROPIC_MODEL`, `CLAUDE_CODE_EFFORT_LEVEL`, `CLAUDE_CODE_USE_BEDROCK`,
  `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_FOUNDRY`, and
  `CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR`, each named literally in the test, and
  checks the stand-in's `env` output holds none of them. It also sets
  `CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_OAUTH_TOKEN`, and `OX_TEST_KEPT` and checks
  each arrives unchanged. Fails today on every variable but the two credential
  ones.
- Claude harness: in `TestAsk` (`internal/claude/ask_test.go`), set
  `CLAUDE_CODE_EFFORT_LEVEL=high` beside the API key, have the script record it
  as it records `ANTHROPIC_API_KEY`, and check it is unset. This keeps titling
  on the same environment as sessions. Fails today.

## Implementation plan

1. `internal/claude/claude.go`: add `overrides`, the list above, grouped by
   effort, provider, and account with a one-line comment each. Replace
   `subscriptionEnv` with `launchEnv(cmd *exec.Cmd) []string`, which deletes
   from `cmd.Environ()` each variable whose name starts with `ANTHROPIC_` or is
   in `overrides`. Its doc comment states the rule: Claude inherits nothing that
   would run it with another effort, model, provider, or account than Ox
   chooses. Call it from `startProcess`.
2. `internal/claude/ask.go`: call `launchEnv`.
3. The two tests above.
4. `docs/harnesses/claude-code.md`, "Environment": replace the
   `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` bullet with what Claude does with
   inherited variables: in `-p` mode an inherited API key or token is always
   used; `CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR` and `ANTHROPIC_UNIX_SOCKET` are
   other credential sources; `CLAUDE_CODE_EFFORT_LEVEL` overrides `--effort` and
   `apply_flag_settings` effort (observed, 2.1.331); `CLAUDE_CODE_USE_*`
   provider switches and `ANTHROPIC_*` endpoints and model variables route
   Claude to another provider or model (2.1.331 strings);
   `CLAUDE_CODE_OAUTH_TOKEN` is a subscription login.

## Requirement changes

None.

## Documentation updates

- `docs/harnesses/claude-code.md` ("Environment"): the variables Claude Code
  reads that override effort, provider, model, or account, as in task 4.
