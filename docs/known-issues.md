# Known issues

These are confirmed defects that are not fixed yet. Each entry says how often
the defect occurs, what it costs at worst, and how it was confirmed. All were
checked against the code on 2026-11-18. "Live" means reproduced with real
`oxd` and Claude processes using Haiku. "Source" means traced in the code but
not reproduced.

| Issue | Occurs | Confirmed |
| --- | --- | --- |
| [One request at a time per connection](#one-request-at-a-time-per-connection) | Ordinary use | Live |
| [Startup failures lose Claude's reason](#startup-failures-lose-claudes-reason) | Missing transcript | Live |
| [RPC bodies over 1 MiB never settle](#rpc-bodies-over-1-mib-never-settle) | Very large prompts | Live |
| [A failed save leaves memory changed](#a-failed-save-leaves-memory-changed) | State file unwritable | Live |
| [Page load can keep stale settings](#page-load-can-keep-stale-settings) | Two pages, millisecond window | Source |
| [Configuration races start and destroy](#configuration-races-start-and-destroy) | Two pages, millisecond window | Source |
| [Settings form replaces values not in its menus](#settings-form-replaces-values-not-in-its-menus) | Settings set outside the UI | Source |
| [Process exit is detected at stdout EOF](#process-exit-is-detected-at-stdout-eof) | Fault injection only | Live, injected |

## One request at a time per connection

`Server.serveConn` handles each request inline on the connection's read loop.
`session/send` holds the session lock while it starts a parked session's
Claude process. `workspace/list` takes every session's lock.

The web UI uses one `oxd` connection per page. So whenever a page sends to a
parked session, every other request from that page waits until Claude has
started, including approvals and requests for other sessions. A page that
loads during that time waits in `workspace/list`.

In a live run, `session/get` for an unrelated session and `workspace/list`
both waited 489 ms behind a send that started Haiku. The baseline was 0.07 ms.
If Claude stalls during startup, the wait lasts up to the one-minute startup
timeout in `claude.Start`.

## Startup failures lose Claude's reason

`initialize` in `internal/claude` reads only the `initialize` control
response and discards everything else. Claude's stderr goes only to the
daemon's stderr, and the exit status is dropped.

Startup fails, for example, when a session marked resumable has lost its
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

## A failed save leaves memory changed

`destroySession` removes the session from memory and from the stored state,
then saves. If the save fails, it returns before stopping the Claude process.
`configureSession` changes the stored record, then saves, and does not undo
the change if the save fails.

A save fails when the state directory is not writable, the disk is full, or
the filesystem is read-only. After a failed destroy:

- The session disappears from listings.
- Its Claude process keeps running and using tokens, but no request can
  reach it.
- `Server.Close` does not stop it.

After a failed configure, the request reports an error while memory holds the
new settings. The next successful save writes those settings to disk.

Confirmed live with a read-only state directory. A configure returned
`permission denied`, yet `workspace/list` showed the new effort. A destroy then
returned the same error, and the session was gone from `session/get` and
`workspace/list`. The surviving Claude process is traced in the source; that
run destroyed a parked session.

## Page load can keep stale settings

On load, the page takes each session's settings from `workspace/list`, and
installs the session only once `session/get` returns. `session/get` does not
return settings. A `session/configured` that arrives before the session is
installed is dropped.

This happens when one page changes a session's settings while another page
is loading or reconnecting. The window is milliseconds on a healthy local
instance, and longer when `workspace/list` waits on session locks (see
[One request at a time per connection](#one-request-at-a-time-per-connection)).
The loading page shows the old settings until it reloads. Its settings form
submits all three fields, so a change to effort alone can write back the old
model or a more permissive permission mode.

## Configuration races start and destroy

`configureSession` releases the session lock before it waits for Claude's
acknowledgements and before it stores the new settings.

- **Configure versus send on a parked session.** A send can start Claude with
  the old settings, and configure then stores and broadcasts the new ones.
  The UI then shows settings the process is not using, possibly including the
  permission mode. The gap is microseconds, so a person cannot hit it.
- **Configure versus destroy on a running session.** A destroy during the
  acknowledgement round-trip stops the process. Configure treats the exit as
  success and broadcasts `session/configured` after `session/destroyed`. The
  window is about 10–14 ms on an idle Haiku process. The web UI ignores
  `session/configured` for sessions it no longer has, so the destroyed
  session does not reappear.

## Settings form replaces values not in its menus

The server accepts any non-blank model and permission mode. Examples are a
full model ID, or the `default` permission mode. The settings form offers only
the fixed lists in `protocol.ts` and selects an option by exact match.

The web UI cannot create such values; they come only from other API clients
or a hand-edited state file. The form then displays the first option in the
menu. Since it submits all three fields on any change, changing effort alone
silently replaces the model or permission mode with that first option.

## Process exit is detected at stdout EOF

`Process.read` in `internal/claude` calls `cmd.Wait` and closes `Output` only
after stdout reaches EOF. The agent finishes only when `Output` closes, and
destroy, sends to a parking session, and daemon shutdown all wait for that.

If any process outlived Claude while holding its stdout open, these would
hang until that process exited, and `workspace/list` would hang behind the
session lock.

Ordinary background work does not cause this. A background Bash `sleep`
started by Haiku ran in its own process group and wrote to a task output
file, not to Claude's stdout. Claude stopped it on exit, and `session/destroy`
returned in 0.8 s. With an injected child that inherited Claude's stdout,
`Output` closed only when that child exited, 13 s after Claude terminated.
MCP servers and abnormal Claude exits have not been tested.
