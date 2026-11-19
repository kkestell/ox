# Known issues

| Issue | Frequency in real use | Confirmed |
| --- | --- | --- |
| [RPC bodies over 1 MiB never settle](#rpc-bodies-over-1-mib-never-settle) | Unknown | Live |
| [Process exit is detected at stdout EOF](#process-exit-is-detected-at-stdout-eof) | Unknown | Injected fault |
| [An unknown effort level is ignored](#an-unknown-effort-level-is-ignored) | Unknown | Live |
| [A failed configuration can leave settings the page does not show](#a-failed-configuration-can-leave-settings-the-page-does-not-show) | Unknown | Source |
| [An unexpected exit can look like parking](#an-unexpected-exit-can-look-like-parking) | No occurrence | Source |
| [A failed approval answer leaves a stale approval](#a-failed-approval-answer-leaves-a-stale-approval) | No occurrence | Source |
| [A configuration that omits a list breaks the web UI](#a-configuration-that-omits-a-list-breaks-the-web-ui) | No occurrence | Source |

## RPC bodies over 1 MiB never settle

`ox` reads at most 1 MiB of an `/rpc` body and forwards the truncated JSON. In
a live test, a 1,048,685-byte `session/send` returned `204`, and the event
stream received `{"id":null,"error":{"code":-32700,"message":"Parse error"}}`.
The page matches replies by ID, so the send remains pending until the
connection closes. The prompt stays in the box without an error or a response.

## Process exit is detected at stdout EOF

`Process.read` waits for stdout EOF before reporting exit. A child that keeps
the descriptor open can block parking, destruction, and later requests for
that session indefinitely. Daemon shutdown also waits for the session.

An injected child inherited stdout and delayed the exit report by 13 seconds.
In a live comparison, a background Bash `sleep` used a task output file in
its own process group, and `session/destroy` returned in 0.8 seconds.

## An unknown effort level is ignored

Claude starts with a misspelled effort from the configuration and runs at
its [default effort](claude-code.md#initialize-first) without an error. The
page shows the configured value, so prompts can run at a different effort and
cost than shown. Confirmed live with the `--effort` launch flag.

## A failed configuration can leave settings the page does not show

While applying [`session/configure`](protocol.md#sessionconfigure), Claude
can accept a change before refusing a later one. A database write can fail
after Claude accepts all changes. In either case the RPC returns an error
and the page shows the old settings while the running process keeps the
changes it accepted. The web form sends two settings together only when a
model change also changes effort, and the built-in models all offer the same
efforts. Which efforts Claude refuses through a control request is untested.

Until a successful configuration reconciles the settings or the process
restarts, prompts can use a different effort, a costlier model, or (after a
failed database write) a permission mode other than the one shown.

## An unexpected exit can look like parking

`Agent.exit` includes the exit reason in an update only when a client prompt
is queued or active. In other phases it publishes the same `parked` update
as normal parking. The client loses the crash diagnostic and can show an
incomplete harness-initiated reply without explaining why it stopped.

## A failed approval answer leaves a stale approval

`Agent.Approve` removes the approval before writing the answer to Claude. If
the write fails, it returns the error without publishing `approval_answered`
or `approval_cancelled`, and the process's exit cannot withdraw an approval
that is no longer pending. Every page keeps the approval until it reloads;
answering it again returns "approval request is no longer pending".

## A configuration that omits a list breaks the web UI

`config.Load` accepts a harness or model that omits `models`,
`permission_modes`, or `efforts`, and `harness/list` sends `null` for it.
`ModelPicker`, `defaultSettings`, and the `Select` in `SettingsFields.tsx`
index those lists without a check. The new-session form lists every harness's
models, so one harness without `models` breaks it for all of them; a harness
without `permission_modes` breaks it when chosen. Either omission also breaks
its sessions' views. The built-in
configuration lists all three everywhere.
