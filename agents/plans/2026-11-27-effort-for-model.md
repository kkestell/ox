# Check each Claude model's effort levels against Claude's own model list

## Goal

`oxd` checks a configured Claude effort level only against one global list of
names, so a configuration can offer High for Claude Haiku 4.5, which takes no
effort, or XHigh for Opus 4.6, which takes only low through max. Ox shows and
saves the effort, and Claude runs without it. When this change is done, `oxd`
reads Claude's model list once at startup and refuses a configuration that
offers a Claude model Claude does not list, or an effort level Claude does not
list for that model. A stored session whose model or effort Claude does not
take fails to start with the reason.

Resolves [#152](https://github.com/kkestell/ox/issues/152), audit finding
[F10](../audits/2026-11-26-main.md#f10-ox-can-offer-effort-levels-that-the-configured-claude-model-ignores).

## Related code

- `cmd/oxd/main.go` — `checkConfig` checks each model's efforts with the
  harness's `CheckEffort(effortID)`, without the model.
- `internal/agent/agent.go:95` — `Harness.CheckEffort func(string) error`.
- `internal/claude/claude.go:34`–`:46` — the global `efforts` list and
  `checkEffort`; `startProcess` calls `checkEffort` at launch (`:69`) and
  passes `--effort` (`:103`). `launchEnv` strips the variables that would run
  Claude on another provider or account.
- `internal/claude/prompt.go:14` — `claude.Harness`, which builds the Claude
  `agent.Harness`.
- `internal/claude/ask.go` — the one-shot `claude -p` run whose shape
  discovery follows: `exec.CommandContext`, `os.TempDir()`, `launchEnv`, stdin
  from a reader, stdout and stderr collected.
- `internal/claude/messages.go:77` — `controlReply`, the parsed
  `control_response`.
- `internal/ur/harness.go:18`–`:47` — Ur's `checkEffort` and `Harness`.
- `internal/config/config.go:208` — `config.Harness.CheckModel(model, effort)`,
  the catalog check every `session/create`, `session/configure`, MCP
  `create_session`, default, and titling choice goes through. It stays as is:
  once the catalog is checked against Claude, every request it admits is one
  Claude takes.
- `cmd/oxd/main_test.go` — `TestConfigOffersOnlyWhatHarnessesTake`,
  `TestConfigRefusesPermissionModesClaudeRefuses`,
  `TestConfiguredThinkingSummariesReachClaude`,
  `TestDaemonOwnsSocketAndDatabaseUntilShutdown`, `TestRefusesToStartWithoutUr`.
- `internal/claude/claude_test.go:518` — `TestUnknownEffortFailsToStart`, the
  test of the global check; `standIn` and `script` put a stand-in `claude` on
  `PATH`.
- `internal/server/live_test.go:303` — `TestLive`.

## Decisions

- **Claude's model list is the one source.** `claude.Harness` holds
  `models := sync.OnceValues(discoverModels)`. Both the configuration check and
  `Start` read it; nothing else in Ox names Claude's effort levels. The global
  `efforts` list and `checkEffort` in `internal/claude/claude.go` go away.
- **The harness check takes the model.** `agent.Harness.CheckEffort
  func(string) error` becomes `CheckEfforts func(model string, efforts
  []string) error`, "reports whether the harness runs model at each of
  efforts; nil means the harness takes every model and effort level".
  `checkConfig` calls it once for every configured model, with its effort IDs,
  including a model with none, so an unknown Claude model is refused even when
  it offers no effort. Ur's `CheckEfforts` checks each effort against its
  OpenRouter list and ignores the model, as today. The name stays distinct from
  `config.Harness.CheckModel`, which checks a choice against the catalog.
- **Discovery.** `discoverModels() (map[string][]string, error)` in
  `internal/claude/claude.go` runs `claude -p --input-format=stream-json
  --output-format=stream-json --verbose` in `os.TempDir()`, with `launchEnv`,
  under `exec.CommandContext` bounded by `startupLimit`, with
  `cmd.WaitDelay = time.Second`. Its stdin is the one line
  `{"type":"control_request","request_id":"initialize","request":{"subtype":"initialize"}}`
  from a `strings.Reader`; Claude answers and exits 0 at EOF. It reads stdout
  lines for the `control_response` whose `request_id` is `initialize`.
  `controlReply` gains `Response json.RawMessage` (the field named `response`),
  decoded only here into `models[]{value, resolvedModel, supportedEffortLevels}`.
  The map has an entry under both `value` and `resolvedModel` for every model:
  the built-in IDs such as `claude-opus-5-5` appear only as an alias's
  `resolvedModel`. A model without `supportsEffort` maps to an empty list.
- **Checks.** `checkEfforts(models)(model, efforts)` refuses, in Go error
  style:
  - discovery failure: `read Claude's models: <reason>`, where the reason is
    the exec error (Claude not on `PATH`), the error `control_response`'s
    `error`, Claude's plain-text stdout, its stderr, `timed out after 1m0s`, or
    `claude printed no model list`, in that order, as `Ask` picks its reason;
  - `Claude does not offer model "x"`;
  - `Claude takes no effort level "xhigh" with model "claude-opus-4-6"`.
- **Start re-checks.** `claude.Harness`'s `Start` closure calls the same check
  with the session's model and its effort (none when empty) before
  `claude.Start`, so a session stored under an older configuration, or before
  this change, with a model or effort Claude does not take fails to start with
  that error instead of running without it. `startProcess` loses its
  `checkEffort` call.
- **Requirements:** **Configuration checks** — `oxd` refuses to start, naming
  the model and level, when the configuration offers what Claude does not
  take, and when it offers Claude and Claude cannot be run to read its list.
  **Settings shown are settings used** — every Claude model and effort the page
  can offer or a session can launch with is one Claude's list takes, so the
  effort shown is the effort Claude runs at. **Built-in catalog** and
  **Titling model** — the built-in IDs resolve through Claude's aliases, so the
  built-in configuration starts, logged in or out. No page request changes: the
  check runs before `oxd` serves, and requests are still checked against the
  catalog.
- **Settings:** discovery runs with `launchEnv`, the environment sessions get,
  so it reads the model list of the account and provider sessions run on. It
  passes no `--model`, `--effort`, `--settings`, or MCP configuration.
- **Settling:** discovery's one wait ends when Claude exits, or at
  `startupLimit` (one minute), when the context kills it; `WaitDelay` bounds
  the wait on pipes a child of Claude holds. Every path returns the list or an
  error, which `oxd` prints as it refuses to start, or `Start` returns.
- **Process I/O:** stdout is read whole after exit, so the final line counts
  even without a newline. The failure reason comes from the error response,
  plain-text stdout, or stderr, as above.
- **Construction:** a configured Claude model and effort that Claude does not
  take cannot reach a session: `checkConfig` refuses them before serving, every
  request is checked against that catalog, and `Start` refuses a stored one.
  One map, read once, decides all three.
- **Ownership:** the discovered list. Owner: the Claude `agent.Harness` value
  `newHarnesses` builds, through its `sync.OnceValues`. Needed so the checks
  refuse effort Claude ignores. Races: `Start` runs on many session goroutines;
  `OnceValues` makes them share one discovery, which `checkConfig` normally
  already ran before serving. A Claude upgrade that changes the list is seen
  when `oxd` restarts. Cleanup: none; it lives as long as `oxd`. When the
  configuration offers no Claude harness, discovery runs only if a stored
  Claude session starts. The screenshots build replaces the Claude harness with
  the scene player, so it never discovers. The commit body repeats this.
- **Claude Code:** [Initialize first](../../docs/harnesses/claude-code.md#initialize-first)
  records the `models` list, `supportedEffortLevels` for models with
  `supportsEffort`, and the discovery recipe. Confirmed live with 2.1.332 for
  this plan, and added to that section by this change: logged in, `models`
  lists the aliases and full model IDs, among them
  `claude-haiku-4-5-20251001` without `supportsEffort` and `claude-opus-4-6`
  and `claude-sonnet-4-6` with only `low`, `medium`, `high`, `max`; logged out,
  `initialize` still succeeds and lists only the aliases, each with its
  `resolvedModel`; stdin closed right after `initialize` still gets the answer,
  then Claude exits 0, in about a second; SessionStart hooks run first. The
  audit's live turn showed Claude given `--effort high` with Haiku 4.5 runs
  without effort and reports none.

## Check your assumptions

1. **Ox needs Claude Code to start whenever the configuration offers Claude.**
   - **Plan:** when `claude` is missing, crashes, or does not answer within a
     minute, Ox refuses to start and says why, as it does when `ur` is
     missing. A user without Claude Code removes `claude` from their
     configuration. Being logged out does not stop Ox: Claude still lists its
     models, and sessions fail at their first prompt as today.
   - **Alternative:** start anyway, with a warning, and offer the configured
     efforts unchecked. Ox stays usable without Claude Code, but in exactly
     that case it can again show an effort Claude ignores.
2. **A Claude model Claude does not list is refused.**
   - **Plan:** a configured model must be one Claude lists, by alias or full
     ID. A typo is caught at startup instead of at a session's first prompt.
     The cost: logged out, Claude lists only its aliases, so a configuration
     with an older model such as Opus 4.6 starts only once the user logs in;
     the built-in models always start.
   - **Alternative:** accept a model Claude does not list, offering it with no
     effort levels. Such a model always starts, but a typo surfaces only when
     a session sends its first prompt.

## Test plan

- Discovery and its checks, Claude harness: new `TestClaudeModelList` in
  `internal/claude/claude_test.go`, with a `script` stand-in that answers
  `initialize` with a `models` list holding an alias resolving to a five-level
  model, a four-level model, and a model without `supportsEffort`, and appends
  a line to a file on each run. Through `Harness(false).CheckEfforts` it
  accepts the alias and the resolved ID with all five levels, refuses `xhigh`
  on the four-level model, accepts the no-effort model with no levels and
  refuses it with `low`, refuses an unlisted model, and ran Claude once. It
  replaces `TestUnknownEffortFailsToStart`, whose global check goes away.
- Discovery failures, Claude harness: in the same test, each with its own
  `script`: no `claude` on `PATH`; plain text `Not logged in` and exit 1; an
  error `control_response`; a script that sleeps, with `startupLimit` set short
  as `TestStartupLimit` does. Each error names its reason.
- A stored session's refused settings, Claude harness: in the same test,
  `Harness(false).Start` with the no-effort model at `low` returns the effort
  error and starts no session process (the stand-in's run count stays at one).
- `checkConfig`, oxd: `TestConfigOffersOnlyWhatHarnessesTake` and
  `TestConfigRefusesPermissionModesClaudeRefuses` put a stand-in `claude` on
  `PATH` answering with the built-in IDs' aliases plus
  `claude-haiku-4-5-20251001` and `claude-opus-4-6`. The built-in configuration
  is still accepted; the refused cases gain Haiku 4.5 with `low`, Opus 4.6 with
  `xhigh`, and an unlisted Claude model with no efforts, and lose the misspelled
  `hgih` case, which the unlisted-level case covers. The `picky` harness's
  check refuses through `CheckEfforts`.
- `oxd` refuses to start without Claude, oxd: new `TestRefusesToStartWithoutClaude`
  in `cmd/oxd/main_test.go`, built like `TestRefusesToStartWithoutUr` with
  `ox-mcp` and `ur` beside `oxd` and a `PATH` without `claude`; the output names
  Claude's models and no socket is left. No existing test starts `oxd` with a
  Claude harness and no `claude`.
- Stand-ins that now meet discovery: `TestConfiguredThinkingSummariesReachClaude`
  and `TestDaemonOwnsSocketAndDatabaseUntilShutdown` start Claude through the
  harness; their stand-ins answer `initialize` with the built-in models when run
  without `--model`, and write their argv or pid files only on a session's run.
- Real Claude, live: `TestLive` checks, before it serves, that
  `claude.Harness(true).CheckEfforts` accepts every model in the built-in
  configuration with its efforts and refuses `claude-haiku-4-5-20251001` with
  `low`.

## Implementation plan

1. `internal/agent/agent.go`: replace `CheckEffort` with `CheckEfforts`.
2. `internal/claude/messages.go`: add `Response json.RawMessage` to
   `controlReply`.
3. `internal/claude/claude.go`: delete `efforts` and `checkEffort` and the
   launch check in `startProcess`; add `discoverModels` and `checkEfforts`.
4. `internal/claude/prompt.go`: `Harness` builds `models :=
   sync.OnceValues(discoverModels)`, sets `CheckEfforts`, and checks the
   session's model and effort in its `Start` closure.
5. `internal/ur/harness.go`: `checkEffort` becomes `checkEfforts(model,
   efforts)`, set as `CheckEfforts`.
6. `cmd/oxd/main.go`: `checkConfig` calls `CheckEfforts(model.ID, ids)` once per
   model, wrapping its error with the model as today.
7. Tests as in the test plan.
8. Documentation as below; `make docs`.

## Requirement changes

None.

## Documentation updates

- `docs/configuration.md`: the refusal paragraph says `oxd` runs Claude at
  startup to read its model list when the configuration offers Claude, and
  refuses a Claude model Claude does not list, an effort level Claude does not
  list for that model, and a Claude it cannot run.
- `docs/protocol.md` (`harness/list`): a session stored with a Claude model, or
  an effort level for its model, that Claude does not take fails to start.
- `docs/harnesses/claude-code.md` ("Initialize first"): the facts confirmed
  live under Claude Code above.
