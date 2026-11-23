# Claude harness parking, exit reasons, and permission mode checks

## Goal

Fix three defects in `internal/claude` that issue #30 groups, findings F10, F11, and F14 of the deslop audit (`agents/audits/2026-11-22-deslop-audit.md`, landing through #26):

- A `CronDelete` call marks the session as possibly holding schedules, so a fresh session whose turn then ends without a Stop callback never parks.
- A plain-text failure Claude prints on stdout after `initialize`, such as `API Error: Connection refused`, is dropped, and the transcript shows only `Claude exited: exit status 1`.
- Configuration accepts any Claude permission mode except `default`, so a typo such as `acceptEdit` passes `oxd`'s startup check and fails only when Claude starts.

When done, only `CronCreate` and `ScheduleWakeup` hold off parking, an unexpected exit after startup reports Claude's last plain-text line, and `oxd` refuses to start with a permission mode Claude does not accept.

## Related code

- `internal/claude/agent.go:221` — the tool names that set `mayHaveSchedules`.
- `internal/claude/agent.go:144` — `Handle` keeps a `plainText` line only in `starting`.
- `internal/claude/agent.go:338` — `exit` builds the reason for an exit after startup from the exit status and stderr only.
- `internal/claude/claude.go:124` — `checkPermissionMode`, which refuses only `default`; `cmd/oxd/main.go` `checkConfig` calls it for every offered mode.

## Decisions

- **Requirements:** **Parking** holds for a fresh session after a `CronDelete`. **Unexpected exits** gets Claude's own reason in every phase. **Configuration checks** refuses a permission mode Claude does not take.
- **Process I/O:** the reader already delivers each non-JSON stdout line as `plainText` before `processExit`, so the last line is known when the exit is handled. The exit reason after startup is the exit status, then the last plain-text line, then the stderr tail, each on its own line and omitted when empty.
- **Construction:** `starting.reason` stays the reason a start fails and is unchanged. A new `Agent` field `plainText`, Claude's last plain-text line on stdout after startup, is set by `Handle` in every phase except `starting`. The two never hold the same line: `starting` ends before the agent field is written, and a start that fails ends the agent. The set of permission modes Claude accepts lives in one list beside `efforts` in `claude.go`.
- **Ownership:** `plainText` is owned by the agent and written only from `Handle` on the session goroutine. It is needed for **Unexpected exits**. It is read once, by `exit`, and goes with the agent; no timer or race.
- **Claude Code:** plain-text failures on stdout and the allowed permission modes (`acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan`, with `default` an alias for `manual`) are recorded in [Startup failures](../../docs/harnesses/claude-code.md#startup-failures) and [Control requests to Claude](../../docs/harnesses/claude-code.md#control-requests-to-claude). `CronDelete` creates no schedule, per [Background work and schedules](../../docs/harnesses/claude-code.md#8-background-work-and-schedules). `default` stays refused with its existing message, so one mode has one ID.

## Check your assumptions

None.

## Test plan

- A `CronDelete` in a failed turn leaves a fresh agent parkable: Claude harness, a third case in `TestTurnWithoutStopHookKeepsSchedules` (`park_test.go`), the same shape as its `ScheduleWakeup` case.
- An exit after startup reports the plain-text line before it: Claude harness, a case in `TestUnexpectedExitIsReported` (`prompt_test.go`) that sends a prompt, handles `plainText("API Error: Connection refused")`, then `processExit{err: exit status 1}`, and checks the `exited` update's `Error` holds both `exit status 1` and the API error and names the prompt's message ID.
- `oxd` refuses a misspelled Claude permission mode: `cmd/oxd`, extend `TestConfigRefusesClaudesDefaultPermissionModeAlias` into `TestConfigRefusesPermissionModesClaudeRefuses`, keeping the `default` case and adding `acceptEdit`, and checking that the built-in configuration, loaded with `config.Load` from a missing path in a temporary directory, passes `checkConfig` with `claude.Harness`.

## Implementation plan

1. `internal/claude/agent.go`: drop `"CronDelete"` from the scheduling tool names.
2. `internal/claude/agent.go`: add `plainText string` to `Agent`; in `Handle`, keep `plainText` lines in `starting.reason` as now and otherwise in `a.plainText`; in `exit`'s default case, add `a.plainText` between the exit status and the stderr tail.
3. `internal/claude/claude.go`: add `permissionModes`, the six modes Claude accepts other than `default`; `checkPermissionMode` keeps its `default` refusal and refuses any other mode not in the list with `Claude has no permission mode %q`.
4. Tests as above, in `internal/claude/park_test.go`, `internal/claude/prompt_test.go`, and `cmd/oxd/main_test.go`.

## Requirement changes

None.
