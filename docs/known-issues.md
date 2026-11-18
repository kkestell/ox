# Known issues

These are confirmed defects that are not fixed yet. Each entry says how often
the defect occurs, what it costs at worst, and how it was confirmed. All were
checked against the code on 2026-11-18. "Live" means reproduced with real
`oxd` and Claude processes using Haiku. "Source" means traced in the code but
not reproduced.

| Issue | Occurs | Confirmed |
| --- | --- | --- |
| [Startup failures lose Claude's reason](#startup-failures-lose-claudes-reason) | Missing transcript | Live |
| [RPC bodies over 1 MiB never settle](#rpc-bodies-over-1-mib-never-settle) | Very large prompts | Live |
| [Process exit is detected at stdout EOF](#process-exit-is-detected-at-stdout-eof) | Fault injection only | Live, injected |
| [An unknown effort level is ignored](#an-unknown-effort-level-is-ignored) | Misspelled configuration | Live |
| [A failed configuration can leave settings the page does not show](#a-failed-configuration-can-leave-settings-the-page-does-not-show) | Refused effort after a model change, or a failed database write | Source |

## Startup failures lose Claude's reason

`initialize` in `internal/claude` reads only the `initialize` control
response and discards everything else. Claude's stderr goes only to the
daemon's stderr, and the exit status is dropped.

Startup fails, for example, when a session with a resume token has lost its
transcript. Claude then prints an error `result` with
`"errors":["No conversation found with session ID: …"]` to stdout, and the
same text to stderr, and exits with status 1. `session/send` reports only
`initialize Claude: read startup response: EOF`. Retrying fails the same way.

Unknown-model and not-logged-in failures have not been tested.

## RPC bodies over 1 MiB never settle

`ox` reads at most 1 MiB of an `/rpc` body (`io.LimitReader`), forwards the
truncated JSON to `oxd`, and answers `204`. `oxd` replies with a parse error
whose `id` is `null`. The page's connection drops any response that matches
no pending call. The call stays pending until the connection closes.

This happens when a prompt pushes the request body past 1 MiB, such as a
pasted log. JSON escaping and multibyte text count toward the limit. The send
silently does nothing: no prompt reaches Claude, no error appears, and the
text stays in the box. The rest of the page keeps working.

Confirmed live: a 1,048,685-byte `session/send` returned `204`, and the event
stream received `{"id":null,"error":{"code":-32700,"message":"Parse error"}}`.

## Process exit is detected at stdout EOF

`Process.read` in `internal/claude` calls `cmd.Wait` and reports the exit only
after stdout reaches EOF. Destroy, parking, and daemon shutdown all wait for
that exit on the session's goroutine.

If any process outlived Claude while holding its stdout open, these would
hang until that process exited, and every later request for that session
would wait behind them.

Ordinary background work does not cause this. A background Bash `sleep`
started by Haiku ran in its own process group and wrote to a task output
file, not to Claude's stdout. Claude stopped it on exit, and `session/destroy`
returned in 0.8 s. With an injected child that inherited Claude's stdout,
the exit was reported only when that child exited, 13 s after Claude
terminated.
MCP servers and abnormal Claude exits have not been tested.

## An unknown effort level is ignored

Claude accepts an effort level it does not know with a warning on stderr and
uses its default effort. A misspelled effort `id` in the configuration
therefore changes nothing and reports no error.

## A failed configuration can leave settings the page does not show

`session/configure` on a running session sends Claude one control request for
each changed setting, in the order model, effort, permission mode. It stores
and broadcasts the settings only once Claude has acknowledged them all and the
database write succeeds. If a later request fails after an earlier one
succeeded, or the database write fails after every acknowledgement, the
process keeps what it applied while the stored settings, and every page, show
the old ones. The request returns an error.

The web UI sends more than one setting only when a model change also changes
effort, because the new model does not offer the current effort. Every model
in the built-in configuration offers the same efforts, so through the web UI
this needs a configuration whose models differ in effort levels, and Claude
refusing the effort right after accepting the model. Which effort levels
Claude refuses through a control request has not been tested. The database
case needs a failed local SQLite write.

Until the session's next configuration or process start, prompts run on a
model other than the one shown, possibly a costlier one. In the database case
the permission mode can differ too.
