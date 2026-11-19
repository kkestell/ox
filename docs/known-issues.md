# Known issues

| Issue | Frequency in real use | Confirmed |
| --- | --- | --- |
| [Startup errors on stderr reach only the daemon's log](#startup-errors-on-stderr-reach-only-the-daemons-log) | No occurrence | Live |
| [A resumed session can park with restored schedules](#a-resumed-session-can-park-with-restored-schedules) | Unknown | Source |
| [Allow rounds large integers in a tool's input](#allow-rounds-large-integers-in-a-tools-input) | No occurrence | Source |
| [A lost session/create response leaves an empty session](#a-lost-sessioncreate-response-leaves-an-empty-session) | No occurrence | Source |
| [make dev can use a daemon that is already running](#make-dev-can-use-a-daemon-that-is-already-running) | Unknown | Source |
| [An invalid escape in the URL fragment blanks the page](#an-invalid-escape-in-the-url-fragment-blanks-the-page) | No occurrence | Source |
| [The root directory's workspace has a blank label](#the-root-directorys-workspace-has-a-blank-label) | No occurrence | Source |

## Startup errors on stderr reach only the daemon's log

Claude reports some startup failures only on stderr, which goes to `oxd`'s
log. A permission mode Claude does not accept, such as a misspelled one in
the configuration, makes Claude exit with its allowed choices on stderr, and
`session/send` fails with `initialize Claude: read startup response: EOF`.
The failure is visible; its reason is in the log. Confirmed live with an
empty `--permission-mode` (Claude 2.1.328).

## A resumed session can park with restored schedules

A resumed Claude restores its recurring and future one-shot jobs but
[reports them](claude-code.md#8-background-work-and-schedules) only when its
first turn ends with a Stop hook. A new process counts no schedules, so if
that turn ends by interrupt or error, the session can park with jobs
pending, and they do not run until a prompt starts the process again. Worst
cost: missed scheduled runs. From `Parkable` in `internal/claude/park.go`.

## Allow rounds large integers in a tool's input

The page parses each approval with `JSON.parse` and sends the parsed input
back as `updatedInput`, so an integer beyond 2^53 reaches Claude rounded,
where [Claude expects the input byte for byte](claude-code.md#can_use_tool).
The card shows the same rounded value. `oxd` forwards the page's answer
unchanged.

## A lost session/create response leaves an empty session

If the connection drops after `oxd` creates a session but before the page
reads the response, the page shows the error and keeps the draft, and the
new session appears in the sidebar without a prompt. Sending again creates
a second session. Worst cost: an extra empty session to delete.

## make dev can use a daemon that is already running

`make dev` starts `bin/oxd` in the background and continues when it fails
because another `oxd` serves the socket. The relay and the UI then reach the
older daemon, so changes to `oxd` appear to have no effect. The terminal
shows the conflict.

## An invalid escape in the URL fragment blanks the page

`selectionFromHash` in `App.tsx` decodes the fragment with
`decodeURIComponent`, which throws on an invalid percent escape, so the page
does not mount until the fragment is removed.

## The root directory's workspace has a blank label

The sidebar labels a workspace with the last segment of its path, which is
empty for `/`.
