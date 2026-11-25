# Review: park resumed sessions without schedules, and stop startup turns

## Scope and coverage

Reviewed the uncommitted changes on `claude-bug-repro` against base `566ec77`
(HEAD, `git merge-base origin/main HEAD`), with the working tree as `/ox-work`
left it: 13 modified files, plus the untracked plan
`agents/plans/2026-11-24-claude-bug-repro.md` and notes
`agents/scratch/2026-11-24-claude-bug-repros.md`. Checked against the plan,
issues #5, #6, and #34, "Database failures", and the harness doc sections the
change cites (4 "When a turn ends", `initialize`, "Startup failures", 8, 13).

The `docs/harnesses/claude-code.md` edits were checked line by line against the
scratch notes. Every claim has a matching observation in the notes except the
failed-resume line (F2), which a live probe here confirmed.

Issue #6: the notes support closing it. On 2.1.331 the `assistant` `tool_use`
that Ox publishes as the transcript tool call carries `{plan, planFilePath}`
even when the model's own input was `{}` (stream event shown in the notes), and
the live spec "an ExitPlanMode plan renders as Markdown", which asserts the
transcript tool call's list items after the approval is answered, passed 4 of 4
with Haiku writing the plan file first in each run. Ox publishes tool calls from
the `assistant` line only (`internal/claude/agent.go:231`), so the filled input
reaches the transcript and the history.

Issues #5 and #34: the change resolves both. #5 by deriving
`mayHaveSchedules` from a `CronCreate` call in the history, with the live
"restored schedules" case as its reproduction; #34 by writing the interrupt in
`starting`, which the notes show Claude honors before `initialize`.

Gaps:

- #34's ignored Stop was not reproduced through Ox's own ordering, as the notes
  say; the fix rests on the unit test driving recorded lines.

## Summary

| ID | Severity | Category      | Action | Status   | Title                                                   |
| -- | -------- | ------------- | ------ | -------- | ------------------------------------------------------- |
| F1 | Low      | Documentation | Fix    | Resolved | Protocol still says interrupt writes nothing at startup |
| F2 | Low      | Documentation | Fix    | Resolved | Notes lack the failed-resume probe the docs cite        |

## F1. Protocol still says interrupt writes nothing at startup

Low · Documentation · Fix · Resolved · `docs/protocol.md:443`

`session/interrupt` says success means the request was written "or, while
Claude is starting or a configuration runs, that nothing needed writing and the
state settles once that ends". After the change, `Interrupt` writes Claude's
interrupt in `starting` whenever the published state is neither `idle` nor
`background` (`internal/claude/prompt.go:101`), and `docs/architecture.md` was
updated to say so. The protocol now contradicts the code and the architecture.

### Evidence

Traced `Interrupt`: `*starting` joins `available` and `*prompting`, and only
`*configuring`, `timedOut`, and `exited` return without writing.

### Action

Change the sentence to "or, while a configuration runs, that nothing needed
writing and the state settles once it ends". No test: documentation only.

## F2. Notes lack the failed-resume probe the docs cite

Low · Documentation · Fix · Resolved · `agents/scratch/2026-11-24-claude-bug-repros.md`

The harness doc's new "Startup failures" line, "No `session_state_changed`
precedes it (2.1.331)", and the plan's "probed live on 2.1.331" have no record in
the notes that land with them. That observation is what `startupOutput` relies
on to tell a startup failure (`Reported() == idle`) from a startup turn's
result, so its evidence belongs with the other reproductions.

### Evidence

The notes cover #5, #6, #34, and other observations, with no failed `--resume`
run. Reproduced here on 2.1.331: `claude -p ... --resume
11111111-1111-4111-8111-111111111111` with `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`
printed the stderr line and then the `error_during_execution` `result`, with no
`session_state_changed` line before it.

### Action

Add a short section to the notes with the command and the trimmed output: the
`result` line and the absence of any `session_state_changed` before it. No test:
`TestStartupFailureKeepsClaudesReason` and the new failed-startup-turn case in
`TestInputWaitsForStartup` already guard the behavior.

## Checks run

- `make test`: passed on the reviewed tree.
- `dprint check`: passed.
- Spot checks, each reverted afterward:
  - `*starting` moved back to the no-write case in `Interrupt`:
    `TestInputWaitsForStartup` fails.
  - `Reported() == idle` condition removed from `startupOutput`:
    `TestInputWaitsForStartup` fails (no `Interrupted` result).
  - `mayHaveSchedules` set for every resume: `TestResumeHoldsSchedulesOnlyAfterCronCreate`
    fails.
  - `session.start` passing no tool names: `TestStartPassesRecordedToolNames`
    fails.
- Live probe of a failed `--resume` on Claude Code 2.1.331 (F2).
- From `/ox-work`'s final response: `make test` and `make live` passed (all of
  `TestLive`, `TestLiveUr`, and the approval specs); each new test, including
  `TestLive/restored_schedules`, failed on the base. `make e2e` was skipped
  because the page is unchanged and e2e drives the fake harness, which cannot
  produce a startup turn.

## Verification 1

Checked the working tree on base `566ec77` after `/ox-work fix`. Since the
review, only `docs/protocol.md` and the scratch notes changed; no code or tests.
`dprint check` passed. No other suite was re-run, since nothing it observes
changed.

| ID | Status   | Evidence                                                                                                                         |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `session/interrupt` now names only a running configuration as the case where nothing is written, matching `Interrupt`.          |
| F2 | Resolved | The notes gain "A failed resume reports no state before its result", with the command and output, matching the probe in review. |

No new findings.

## Verdict

`Ready to commit`: F1 and F2 are resolved.
