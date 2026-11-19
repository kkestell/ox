# Behavior guarantees

These are the promises Ox keeps to the person using it. Each links to the
contract that defines its details. Reviews check changes against them.

1. **A prompt runs with the settings the page shows.** Every Claude process
   starts with the session's settings, and
   [`session/configure`](protocol.md#sessionconfigure) reserves the session
   until Claude acknowledges a change. While a page's own change is in
   flight, it shows the requested values marked pending and holds Send;
   once the change settles, it shows the session's settings, which after a
   partial or unstored change are the ones the process took. A change made
   from another page appears when its `session/configured` event arrives.
   On reconnect, Send waits for that session’s fresh settings. A
   mode change Claude makes itself is [not yet tracked](todo.md#check-first).

2. **Typed text stays until it is sent.** The composer clears only the text
   it sent, once `oxd` accepts it. Text typed while a send or a new
   session's creation is pending, and unsent text in a session the user
   leaves, survive navigation and reconnects. Reloading or closing the page
   discards unsent text, and a new session's text before it is submitted
   belongs to its workspace draft. A lost creation reply keeps the same
   session ID and draft text for manual submission.

3. **Only the user sends prompts.** A prompt is sent once, when the user
   submits it. Showing a view again, reconnecting, and reloading send
   nothing, and a failed send is not retried. A send cut off by a lost
   connection reports an error even if `oxd` received it; the transcript
   shows whether it did.

4. **Every request settles.** Each request from the page ends in its result
   or an error the page shows: `oxd` answers every request with an ID
   ([errors](protocol.md#errors)), the relay refuses what it cannot forward,
   and a lost event stream fails every pending request. A request waits
   behind its session's current operation: Claude's startup has a one-minute
   limit, an unacknowledged configuration stops the process after one
   minute, and parking or destroying waits for Claude to exit after SIGTERM
   and kills it after ten seconds.

5. **A failure is shown where it happened.** A session's request errors
   appear in that session's view, including the first send after the session
   is created. An error stays until the next request from that view starts,
   and a request that fails shows its error even if a later one started
   first. A start that fails carries the reason Claude printed on stdout or,
   failing that, the end of its stderr, and a process that exits without
   being stopped leaves an `exited` entry in the transcript.

6. **Sessions are independent.** A session starting, configuring, or
   running holds up only its own requests
   ([sessions](architecture.md#sessions)), and the page keeps each session's
   requests separate ([web UI](architecture.md#web-ui)).

7. **Approvals wait for an answer.** An approval stays pending until an
   answer is written to Claude, or Ox withdraws it when Claude cancels it,
   goes idle, or exits; every page is told which
   ([approval lifecycle](protocol.md#approval-lifecycle)). Allow sends the
   tool's input as the card shows it.

8. **Closing a page leaves work running.** Prompts, approvals, and Claude's
   own turns continue without a page ([connections](protocol.md#connections)),
   and a page that reconnects reloads every session in place.

9. **Claude's own turns run while its process does.** A session does not
   park while Claude reports a background task or a pending schedule
   ([parking](protocol.md#workspaces-and-sessions)). Turns Claude starts
   itself are reported without a prompt's `message_id`
   ([updates](protocol.md#session-updates)). A parked session runs no
   schedules until a prompt starts it again. A resumed process stays active
   until Claude reports which schedules it restored.

10. **Ox's bookkeeping stays in its own files.** `oxd` writes only its
    [database](protocol.md#workspaces-and-sessions) and
    [socket](protocol.md#connection-and-messages), and
    replaces a socket path only when it holds a stale socket. Destroying a
    session deletes its history in Ox and leaves Claude's transcript and the
    workspace alone. Claude writes in the workspace as its permission mode
    and the user's approvals allow.
