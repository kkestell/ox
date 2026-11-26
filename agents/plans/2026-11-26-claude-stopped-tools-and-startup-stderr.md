# Show a tool Stop ended as stopped, and keep Claude's stderr in a startup timeout

## Goal

Resolves [#109](https://github.com/kkestell/ox/issues/109), findings F5 and F12 of `agents/audits/2026-11-25-ox-sweep.md`.

- **F5.** When Stop interrupts a Claude Bash call, the tool shows as failed. Claude answers the running tool with an error `tool_result` ("The user doesn't want to proceed with this tool use…") before the interrupted `result`, the agent publishes it, and the page shows any tool with an error result as failed. Ur's interrupted tools show stopped, because Ur publishes no `tool_result` for them. After the change, both harnesses follow one rule: a tool call that Stop interrupted gets no `tool_result`, and the page shows a call without one at its turn's end as stopped, as it does today.
- **F12.** When Claude prints why it is stuck on stderr and keeps running, the startup timeout ends the start with the timeout text and the stdout reason alone. Claude's stderr reaches the agent only with the process exit. After the change, the startup timeout asks Claude to exit and ends the start at the exit, with the stderr tail included in the reason.

## Related code

- `internal/claude/agent.go`: `handleOutput`'s `"user"` case publishes every top-level `tool_result`. `Handle`'s `startupTimeout` case gives up the start at once. `exit` and `end` switch over the phases.
- `internal/claude/prompt.go`: `Interrupt` appends to `a.stops`, and `settleStops` clears it when the stopped turn ends (`idle`, the next input taken, or the agent's end). `Send` and `Interrupt` switch over the phases.
- `internal/claude/configure.go`: `controlTimedOut` and the `timedOut` phase follow the pattern the startup timeout adopts: terminate, then end at the exit.
- `internal/process/process.go`: `logStderr` keeps the stderr tail and hands it to `Exit` only after the pipe closes.
- `internal/ur/engine/engine.go`: `interrupt` ends the turn with `Interrupted` and publishes no `tool_result` for the running call, and `TestStaleOutput` covers dropping the command's late outcome. Ur needs no change.
- `cmd/ox/web/src/ox.tsx` (`stopTools`) and `cmd/ox/web/src/SessionView.tsx` (`ToolCall`): mark a tool stopped when its turn's `result`, `exited`, or `parked` arrives before its `tool_result`. Unchanged.

## Decisions

- **One rule for a stopped tool.** A tool call that an interrupt stopped has no `tool_result`. The page already decides stopped from that, so the decision stays in one place and needs no protocol field. The Claude agent conforms by dropping each top-level `tool_result` with `is_error` while an interrupt waits in `a.stops`. `a.stops` is Ox's own record that it interrupted the running turn. It holds from the interrupt's write until the turn's end, which covers the interrupt sequence's error results. Matching Claude's result text is not needed. A tool that fails on its own while an interrupt waits also shows stopped. That outcome is valid because the user stopped the turn. A tool that finishes successfully while an interrupt waits still shows done.
- **Startup timeout waits for the exit.** On `startupTimeout` the agent terminates the process (`Terminate(killAfter)`, as today) and enters a new phase, `startTimedOut`, which holds the `*starting`. The process exit ends the start with `no startup response within one minute`, then Claude's stdout reason if any, then the stderr tail if any, each on its own line. A lost conversation still becomes `lostConversation` through `startFailed`. The stderr tail stays where it is today: it reaches the agent only through `process.Exit`. No new I/O path, lock, or event type is added.
- **Requirements.** *Tool calls*: a stopped Claude tool shows stopped, live and after reload, because history records no `tool_result` for it. *Stop*: unchanged. The turn still ends with `Interrupted`. *Failed startup*: the reason now includes Claude's stderr. A start ends when Claude exits after SIGTERM, which is at once in practice and at most `killAfter` later for a Claude that ignores it. The prompt is still dropped and returned to its composer through `end`. A Stop during `startTimedOut` ends the agent at once, as in `starting`.
- **Settling.** The wait added in `startTimedOut` ends at the process exit, which `Terminate`'s kill bounds at `killAfter`, plus the pipe read deadline in `process.read`.
- **Process I/O.** Claude's stderr reason now reaches the failed start through `Exit.Stderr`.
- **Construction.** Only the `starting` phase holds `startupOutput`, so a late `initialize` response after the timeout cannot make the terminating process available. `startTimedOut` is not `*starting`. No flag inside `starting` needs a check. In `startTimedOut`, a late `initialize` falls to `controlResponse`, which ignores it, and late plain text goes to `a.plainText`, which the exit does not read in this phase.
- **Ownership.** `startTimedOut` is owned by the session goroutine through the agent, like every phase. It exists so that the start's failure can carry the stderr tail. It is cleaned up by the exit or by Stop, through `end`. It adds no timer. The kill timer is `Terminate`'s existing one. The new phase must appear in each exhaustive phase switch (`//sumtype:decl`): `exit`, both switches in `end`, `Send`, and `Interrupt`.
- **Claude Code.** The interrupt sequence is recorded in `docs/harnesses/claude-code.md` ("Interrupt"): the interrupt's `control_response`, then an error `tool_result`, then the interrupt text, then the `aborted_tools` result, then `idle`. The two prompt variants recorded there produce no `tool_result`. The dropped-result rule ends when `settleStops` runs: at `idle`, at the next input taken, or at the agent's end. SIGTERM exits Claude (143), as "Startup failures" records.

## Check your assumptions

None.

## Test plan

- Interrupted tool, Claude harness: extend `TestTopLevelToolCallsArePublished` (`internal/claude/prompt_test.go`). After its existing assertions, publish a `tool_use` `t3`, call `interrupt(a, updates)`, then feed the recorded sequence: the interrupt's `control_response`, the error `tool_result` for `t3` with "The user doesn't want to proceed with this tool use…", the `user` text `[Request interrupted by user for tool use]`, the `aborted_tools` result, and `idle`. Assert that no `tool_result` was published and that the `result` is `Interrupted`. Then, in the next turn, an error `tool_result` is published again. This fails before the change.
- Startup timeout reason, Claude harness: in `TestStartupFailureKeepsClaudesReason` (`internal/claude/claude_test.go`), the two `startupTimeout` cases gain a following `process.Exit` with `Stderr: "stuck"`. "startup timeout after plain text" expects `no startup response within one minute\nNot logged in · Please run /login\nstuck`. "startup timeout after lost conversation" still expects `lostConversation`. Add the case "initialize response after startup timeout": `startupTimeout{}`, `initialized`, then the exit, expecting `no startup response within one minute\nstuck`.
- Startup timeout through a real process: extend `TestStartupLimit`. The stand-in prints `stuck on login` to stderr, then `exec sleep 60`. Expect `no startup response within one minute\nstuck on login`. This fails before the change. Drop the `children.WaitAll` block, because the agent now ends only once the process has exited and been reaped.
- Live: in `TestLive`'s "interrupt" section (`internal/server/live_test.go`), add a Bash case. Have Haiku run `sleep 30`, approving any approval, and wait for its `tool_use`. Then run `l.stop`, check that the `result` is `Interrupted`, and check that no `tool_result` for that `tool_use_id` was received. This is the live reproduction of the inferred stop.

## Implementation plan

1. `internal/claude/agent.go`, `"user"` case: skip a top-level `tool_result` with `IsError` while `len(a.stops) > 0`. Add a comment: an interrupt answers each running tool with an error result, and a stopped call has no `tool_result`, as for Ur.
2. `internal/claude/agent.go`: add the phase `startTimedOut struct{ start *starting }` with its `isPhase`, documented as "Ox gave up on the start and asked the process to exit; the exit ends the start". In `Handle`'s `startupTimeout` case, call `a.proc.Terminate(killAfter)` and set `a.phase = startTimedOut{s}`. In `exit`, add `case startTimedOut`: build the reason from the timeout text, `p.start.reason`, and `exit.Stderr`, joining the nonempty ones with `\n`, and call `a.startFailed(p.start, reason)`. In `end`, name the latest queued input for `startTimedOut` as for `*starting`, and list it among the phases with no configuration.
3. `internal/claude/prompt.go`: `Send` holds input in `startTimedOut`, like `*starting`. `Interrupt` answers at once in `startTimedOut`, like `timedOut`.
4. Tests as in the test plan.
5. Documentation updates below.

Leave `Start` and `startProcess` unchanged. Worktree `gNTBWf2h8Cm` changes their signatures and the `Start(options)` call in `TestStartupLimit`.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, the Claude agent's phase diagram and table: add `startTimedOut`. Its transitions are `starting --> startTimedOut: startup limit` and `startTimedOut --> exited: process exits / Stop`. Its row reads "Claude did not answer `initialize` in time and Ox asked the process to exit; the exit ends the start with its stderr tail" and leaves on "Process exits or `Stop`". Remove "startup limit" from `starting`'s exits.
- `docs/protocol.md`, the `tool_result` row: add "A call an interrupt stopped gets none." The `exited` row: the stderr tail is included for an unexpected exit and for a start that timed out.
