# Known issues

## A resumed session whose first turn is interrupted or fails never parks

A Claude process started to resume a conversation counts as possibly holding
schedules until a Stop hook reports its schedule list. Claude sends no Stop
callback for a turn that ends by interrupt or error, so when the first turn
after a resume is stopped or fails, the idle session does not park until a later
turn completes or `oxd` restarts.

- **Worst cost:** one idle Claude process per affected session, kept until the
  next completed turn. No work is lost.
- **Found:** from the source and the Stop hook behavior recorded in
  [Claude Code](harnesses/claude-code.md#8-background-work-and-schedules), in
  the full-repository review of 2026-11-19. Not reproduced live.
- **Waits on:** a live reproduction of whether every job `--resume` restores,
  including a fixed-interval `/loop`, appears as a top-level `CronCreate` call
  in the session's history. If it does, a resumed process with no such call can
  start parkable. Otherwise it waits on Claude reporting restored jobs without a
  completed turn.

## A plan written to a file shows no plan in its tool call

When Claude calls `ExitPlanMode` with
[empty input](harnesses/claude-code.md#13-built-in-tool-names-21319), the
transcript's tool call has no plan to render. The approval card still shows it.

- **Worst cost:** after the approval is answered, the transcript no longer shows
  the plan the user approved or denied. Claude's reply usually restates it.
- **Found:** in a `make live` run of the ExitPlanMode approval test, on
  2026-11-20.
- **Waits on:** a live reproduction showing when Claude sends `ExitPlanMode`
  with empty input, and whether its stream-json output carries the plan anywhere
  besides the `can_use_tool` request.
