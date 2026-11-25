# Live reproductions for #5, #6 and #34

Claude Code 2.1.331, `claude-haiku-5-5`, 2026-11-24.

## Method

A Python driver (`/tmp/ccprobe/bin/drive.py`, not kept) ran one process per
probe:

```
cd <probe dir> && claude -p --input-format stream-json --output-format stream-json \
  --verbose --include-partial-messages --replay-user-messages \
  --permission-mode <mode> --permission-prompt-tool stdio --model claude-haiku-5-5 \
  (--session-id <uuid> | --resume <uuid>) [--settings <json>]
```

with `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and no `ANTHROPIC_API_KEY`. The
driver writes each stdin and stdout line with seconds since spawn, declares
the Stop hook in `initialize`
(`"hooks":{"Stop":[{"hookCallbackIds":["stop"]}]}`), answers every
`hook_callback` with success, and answers `can_use_tool` with allow (input
echoed), deny (`DENY_TOOLS`), or no answer (`HOLD_TOOLS`). A step script decides
when it sends `initialize`, prompts and interrupts. The first plan probes ran
without `--include-partial-messages`, and later ones with it.

Raw lines below are trimmed; `uuid` fields are removed.

## #5: restored jobs and their `CronCreate` calls

### A fixed-interval `/loop` is a top-level `CronCreate` call

New session, prompt `/loop 5m say hi`:

```
1.97 << {"type":"user","message":{"role":"user","content":"<command-message>loop</command-message>\n<command-name>/loop</command-name>\n<command-args>5m say hi</command-args>"},...,"isReplay":true}
2.14 << assistant tool_use ToolSearch {"query": "select:CronCreate", "max_results": 1} parent=None
3.12 << assistant tool_use CronCreate {"cron": "*/5 * * * *", "prompt": "say hi", "recurring": true} parent=None
3.16 << tool_result "Scheduled recurring job 68544d28 (Every 5 minutes). Next run: Nov 24 9:35pm. Session-only (not written to disk, dies when Claude exits). Auto-expires after 30 days. Use CronDelete to cancel sooner."
3.16 <<   tool_use_result {"id": "68544d28", "humanSchedule": "Every 5 minutes", "recurring": true, "durable": false, "nextFireAt": 1795577713337, "maxAgeMs": 2592000000}
4.00 << Stop hook_callback: session_crons=[{"id":"68544d28","schedule":"*/5 * * * *","recurring":true,"prompt":"say hi"}]
```

In the transcript, the only entries that name the job are that `tool_use` and
its `tool_result`. Claude writes no separate restore record.

### Resume restores it under the same ID, and reports it only at a Stop

`--resume`, `initialize` sent 3 s after spawn, prompt `Reply with just: ok` 5 s
later:

```
0.30 << {"type":"system","subtype":"hook_started","hook_name":"SessionStart:resume",...}
0.39 << {"type":"system","subtype":"hook_response","hook_name":"SessionStart:resume",...,"outcome":"success"}
3.02 << control_response initialize success {...}        # no job ID anywhere in it
8.07 << system init ...
8.42 << assistant text ok
8.43 << Stop hook_callback: session_crons=[{"id":"68544d28","schedule":"*/5 * * * *","recurring":true,"prompt":"say hi"}]
```

The job ID appears in no line before the Stop callback. The `initialize`
response has the keys `commands, agents, output_style, available_output_styles,
user_output_styles_dir, models, account, pid, claude_code_version,
current_permission_mode, hooks_applied, hook_on_failure_supported,
hooks_when_unanswered_kept, other_hook_ask_outranks_host, feedback_mode,
analytics_disabled, sandbox_enabled, remote_control_*, ide_rc_auto_enable_gate,
fast_mode_state, fast_mode_disabled_reason, session_state, capabilities`. None
of them lists schedules. A second resume with no `CronCreate` in between
restored `68544d28` again.

### What resume does not restore

- A job a foreground subagent created (`b9621db3`, `parent_tool_use_id` set):
  listed in the creating process's Stop, absent after resume.
- `CronCreate` with `durable: true` returned `"durable": false` and wrote no
  `.claude/scheduled_tasks.json`. The job came back on resume like any other
  main-thread job.
- A `ScheduleWakeup` wakeup: listed before, `session_crons=[]` after resume.
- A one-shot job whose fire time passed while no process ran (`137bac6f`, due
  21:33, resumed 21:34): not restored. `CronDelete` for it then failed with
  `No scheduled job with id '137bac6f'`.

In these runs every job a resume restored was a top-level `CronCreate` call in
the session's history. Claude reported restored jobs only through the Stop hook
of a completed turn, with nothing at startup.

**Status:** settled. **Fix it allows:** a process started with `--resume` can
start parkable when the session's history has no top-level `CronCreate` call.
Only a session that has one has to wait for a Stop report. Subtracting
`CronDelete` IDs, expired recurring jobs (`maxAgeMs`) and past one-shots
(`nextFireAt`) would narrow that further, but it would copy Claude's restore
rule into Ox.

## #6: recovering the plan for an `ExitPlanMode {}` tool call

Ten plan-mode sessions: three with a free-form planning prompt (allowed), seven
with the live test's prompt `Load and call the ExitPlanMode tool now with this
plan, as a Markdown list of exactly two items: first step, second step.`
(denied). In all ten, Haiku wrote the plan file with `Write` and then called
`ExitPlanMode`. The model's own input was empty:

```
4.36 {"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01MV3qjPEbd7FdssKDfWkFtV","name":"ExitPlanMode","input":{},"caller":{"type":"direct"}}}}
4.36 {"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}}
4.38 assistant tool_use ExitPlanMode {"plan": "- first step\n- second step\n", "planFilePath": "/Users/kyle/.claude/plans/load-and-call-the-soft-llama.md"}
```

Claude filled `plan` (the plan file's content) and `planFilePath` into the
`assistant` line, the transcript entry, and the `can_use_tool` input:

```
<< {"type":"control_request",...,"request":{"subtype":"can_use_tool","tool_name":"ExitPlanMode",...,"input":{"plan":"- first step\n- second step\n","planFilePath":"/Users/kyle/.claude/plans/load-and-call-the-mossy-cosmos.md"},...,"requires_user_interaction":...}}
```

The results:

- Allowed: `tool_result` text `User has approved your plan. ... Your plan has
  been saved to: <path> ... ## Approved Plan:\n<plan>`, and `tool_use_result`
  `{"plan", "isAgent": false, "filePath"}`.
- Denied: `tool_result` `Permission denied by client` (`is_error: true`),
  `tool_use_result` `"Error: Permission denied by client"`. No plan.

Through Ox:
`OX_LIVE=1 npm --prefix cmd/ox/web run e2e -- approval -g "ExitPlanMode plan renders" --repeat-each=4`
passed 4 of 4. In each run's transcript, Haiku had written the plan file (under
the workspace's `.claude/plans/`) before the call, and the `ExitPlanMode`
`tool_use` input carried `plan` and `planFilePath`.

**Status:** shown not to exist on 2.1.331. The `assistant` `tool_use` that Ox
publishes as the transcript tool call now carries the plan, so the defect seen
on 2.1.329 no longer happens. **Fix it allows:** none needed. The issue can
close.

## #34: a turn before `initialize`, and Stop in that window

### A resumed process runs scheduled turns before `initialize`

Session with `CronCreate` `* * * * *` "say tick", resumed with `initialize`
withheld for 150 s:

```
  0.22 << hook_started SessionStart:resume
  0.28 << hook_response SessionStart:resume
 81.47 << {"type":"system","subtype":"session_state_changed","state":"running",...}
 81.48 << {"type":"command_lifecycle","command_uuid":"858b1b49-...","state":"started","wakeup_source":"schedule_wakeup",...}
 81.64 << system init ...
 82.15 << assistant text tick
 82.32 << {"type":"result",...,"stop_reason":"end_turn",...}
 82.33 << command_lifecycle completed / session_state_changed idle
141.50 ... the same again ...
150.01 >> initialize
150.02 << control_response initialize success
```

Those turns get no Stop callback, because no hook has been declared yet.

### A turn before `initialize` asks for approval, and an interrupt stops it

The job's prompt changed to "Use the Write tool to create note.txt ..., then run
`python3 -c 'import time; time.sleep(40)'`". Resumed with `initialize`
withheld, the `Write` approval held, and an interrupt sent 3 s after the
request:

```
66.39 << {"type":"system","subtype":"session_state_changed","state":"running"}
66.39 << {"type":"command_lifecycle","command_uuid":"13152cea-...","state":"started","wakeup_source":"schedule_wakeup"}
67.85 << assistant tool_use Write {"file_path": "/private/tmp/ccprobe/s34/note.txt", "content": "hi\n"}
67.90 << {"type":"system","subtype":"session_state_changed","state":"requires_action"}
67.90 << {"type":"control_request","request_id":"7fd63cc1-...","request":{"subtype":"can_use_tool","tool_name":"Write",...}}
70.90 >> {"type":"control_request","request_id":"int-9a535711","request":{"subtype":"interrupt"}}
70.91 << {"type":"control_cancel_request","request_id":"7fd63cc1-..."}
70.91 << {"type":"control_response","response":{"subtype":"success","request_id":"int-9a535711","response":{"still_queued":[]}}}
70.91 << tool_result is_error=true "The user doesn't want to proceed with this tool use. ..."
70.92 << user text "[Request interrupted by user for tool use]"
70.92 << result subtype=error_during_execution is_error=true terminal_reason=aborted_tools
70.92 << {"type":"command_lifecycle","command_uuid":"13152cea-...","state":"cancelled"}
70.92 << {"type":"system","subtype":"session_state_changed","state":"idle"}
80.92 << control_response initialize success
```

With the approval allowed, an interrupt 4 s into the running foreground Bash
call, still before `initialize`, ended it the same way: a
`task_notification` with `"status":"stopped"`, the error `tool_result`,
`[Request interrupted by user for tool use]`, `result` `aborted_tools`,
`command_lifecycle cancelled`, `idle`. An interrupt sent before `initialize`
while no turn ran was answered `{"still_queued":[]}` and changed nothing.

### `initialize` during a turn is answered at once

With `initialize` sent while that Bash call ran, the response came within 10 ms,
with `"session_state":"running"`. A later interrupt stopped the turn as above.

### With `initialize` already sent, as Ox does, no turn came first

Spawn and `initialize` together, with a SessionStart hook `sleep 75` in
`--settings` and the every-minute job restored:

```
  0.00 >> initialize
  0.26 << hook_started SessionStart:resume  (twice: user hook and the sleep 75 hook)
 75.28 << hook_response SessionStart:resume  (the sleep 75 hook)
 75.33 << control_response initialize success
118.36 << session_state_changed running / command_lifecycle started wakeup_source=schedule_wakeup
121.11 << Stop hook_callback
```

Claude answered `initialize` only after the SessionStart hooks finished. No
scheduled turn ran while they ran, and the minute boundary missed during the
hook was not caught up. In every run where `initialize` was on stdin from spawn,
Claude answered it within 0.7 s of spawn, before any turn.

**Status:** Claude's behavior is settled. A resumed Claude does start turns,
including approval requests, before it answers `initialize`, and it honors an
interrupt in that window exactly as after `initialize`. The defect as stated
(Stop ignored while `starting`) was not reproduced through Ox's own ordering. With
`initialize` written at spawn, Claude answered it before any scheduled turn
in these runs, even behind a 75 s SessionStart hook. **Fix it allows:**
`Interrupt` can treat `starting` as it treats `available`, writing the
interrupt whenever the tracker is not idle, because Claude answers and acts on
an interrupt before `initialize`. That removes the ignored-Stop state without
depending on timing.

## A failed resume reports no state before its result

`initialize` written at spawn, then `--resume` of a conversation that does not
exist, with `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`:

```
claude -p --input-format stream-json --output-format stream-json --verbose \
  --model haiku --resume 11111111-1111-4111-8111-111111111111
```

```
stderr: No conversation found with session ID: 11111111-1111-4111-8111-111111111111
<< {"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":0,"stop_reason":null,"session_id":"11111111-...",...}
exit 1
```

No `session_state_changed` line and no `initialize` response came before the
`result`. A startup turn's `result` follows its `running` report (#34 above),
so Claude's last reported state tells the two apart: `idle` before a startup
failure, `running` or `requires_action` before a startup turn's `result`.

## Other observations

- `CronCreate` results carry `maxAgeMs: 2592000000`, and the result text says
  recurring jobs auto-expire after 30 days.
- Claude writes each `control_response` the host sends back onto stdout
  unchanged. It was seen with `--replay-user-messages`; the cause was not
  isolated. Ox ignores it, since the IDs are Claude's own.
- The user's own `PreToolUse` hook blocked a foreground `sleep 30`, so the
  long-running command in the #34 probes used `python3 -c`.
