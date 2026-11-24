# The page reopens its event stream itself

## Goal

On iOS, a home-screen Ox page that is switched away from for a while and then
switched back stays on "Reconnecting…" with an empty session list until the
user reloads it. After the change, the page reconnects as soon as it is shown
again.

The page leaves reconnection to `EventSource`'s own retry. When iOS suspends the
page, the stream errors with `readyState` `CONNECTING`, so the page shows
"Reconnecting…", and WebKit's retry can stall without ever firing again. Nothing
else reopens the stream: `rpc()` rejects with `Disconnected` while no connection
is held, so its fetch-failure path, the page's only other recovery, never runs,
and nothing reacts to the page becoming visible. The page will own the stream's
whole lifecycle in `connection.ts`: it closes a dropped stream, reopens it after
a second, and reopens it at once when the page becomes visible while it is not
connected.

## Related code

- `cmd/ox/web/src/connection.ts` — `connect()` opens the `EventSource`, holds the
  relay's connection ID, reports drops through `lose()`, and replaces the stream
  only when an `rpc()` POST cannot reach the relay. The whole fix lives here.
- `cmd/ox/web/src/ox.tsx:550` — the provider's `connect()` call. `onConnect`
  runs `reload()`; `onError` marks every session reloading and sets the page
  error. Unchanged: it already handles any number of drops and connects.
- `cmd/ox/web/src/App.tsx:154` — shows a `Disconnected` that is not `closed` as
  the "Reconnecting…" status, and a `closed` one as an alert with Reload.
  Unchanged.
- `cmd/ox/main.go:149` — the relay's `/events`: sends `event: connection` with
  the connection ID as soon as it dials `oxd`, and answers `502` when it cannot.
  The browser closes an `EventSource` on a non-200 answer, which is how the page
  tells "`oxd` still down" from a network drop. Unchanged.
- `cmd/ox/web/e2e/session.spec.ts:393` — "a reconnecting stream shows as a
  status, not an error" and "a closed stream offers a Reload button that
  reconnects", the existing drop and refusal cases. The new test sits beside
  them.

## Decisions

- **One owner of the stream.** `connect()` keeps a single `stream` value, a
  discriminated union:
  - `{ kind: "connecting"; source: EventSource }` — opened, no `connection`
    event yet.
  - `{ kind: "connected"; source: EventSource; connection: string }`
  - `{ kind: "waiting"; timer: ReturnType<typeof setTimeout> }` — dropped, the
    old source closed, reopening when the timer fires.
  - `{ kind: "closed" }` — the relay refused the stream, or `close()` ran. Never
    reopened.

  This replaces `source` and `connection`. The connection ID exists only while
  connected, and a pending reopen exists only while no source does.
- **Every drop takes one path.** A function `drop(error: Disconnected)` closes
  the current source if there is one, fails pending calls and calls
  `handlers.onError` (today's `lose()`), and sets `stream` to `waiting` with a
  one-second timer that calls `open()`, or to `closed` when `error.closed`. It
  is called by the source's `onerror` and by `rpc()`'s fetch failure.
- **`onerror` closes the source.** On error the source's own `readyState`
  decides: `CLOSED` means the relay refused it, so `drop` with a closed
  `Disconnected` ("Lost the connection to ox."); otherwise `drop` with "Lost the
  connection to ox. Reconnecting." and the page's timer reopens it, so the
  browser's retry never runs. Each handler refers to the `EventSource` it was
  attached to, not to `stream`.
- **Visible again reopens at once.** A `visibilitychange` listener on `document`
  checks `document.visibilityState === "visible"`, and when `stream` is
  `connecting` or `waiting`, it closes the source or clears the timer and calls
  `open()`. A stream that is `connected` or `closed` is left alone. This
  replaces a reconnect attempt iOS suspended mid-request, which would otherwise
  never answer.
- **`rpc()` reads `stream`.** It rejects with `Disconnected("Not connected to
  ox")` unless `stream.kind === "connected"`, and sends on `stream.connection`.
  Its fetch failure calls `drop` only when `stream` is still `connected` with
  the ID it sent on; otherwise it fails just that call, as today. The 503 path
  is unchanged.
- **`close()`** closes the source or clears the timer, removes the
  `visibilitychange` listener, sets `closed`, and fails pending calls.
- **`Disconnected.closed`** now means the relay refused the stream, so the page
  stops reconnecting and only a reload restores it. Its comment says so.
- **A stream left open on a dead socket** is not addressed. The screenshot
  shows "Reconnecting…", which only an `onerror` produces, so the observed
  defect is the stalled retry. A stream iOS keeps open without data is not
  reproduced.
- **Requirements:** "Lost connection" holds as written: a drop shows the status
  and fails pending requests through `onError` as today; Send and settings stay
  held until `reload()` finishes; the page reconnects by itself, now including
  after iOS resumes it; and a relay that answers `502` because `oxd` is down
  still shows Reload. "Every request settles": a request made while not
  connected rejects at once with `Disconnected`, and one in flight on a dropped
  stream is failed by `drop`. "Every page in sync": each reconnect reaches
  `onConnect`, which reloads everything the page missed.
- **Settling:** the reconnect timer ends by firing or by being cleared on
  visibility or `close()`. A reconnect attempt ends by its `connection` event,
  its error, or the page becoming visible again.
- **Construction:** the connection ID is present only in the `connected`
  variant, a pending reopen only in `waiting`, and a refused or closed stream
  has neither, so no code can send on a stale ID or reopen a refused stream.
- **Ownership:** the reconnect timer is owned by `connect()`'s `stream`. It is
  needed so the page reconnects by itself after a drop now that the browser's
  retry no longer runs. Races: a replaced source's late events — a closed
  `EventSource` dispatches none, and every replacement closes the old source
  first; a visibility change during `waiting` — it clears the timer before
  opening. Cleanup: `close()` clears it.

## Check your assumptions

None.

## Test plan

- A reconnect that stalls recovers when the page becomes visible, e2e, new test
  in `session.spec.ts` after "a closed stream offers a Reload button that
  reconnects": "a stalled reconnect resumes when the page is shown". Create a
  session, route `**/events` with a handler that keeps each route unanswered
  (store it in an array, neither fulfill nor continue it), restart the relay,
  and expect the "Reconnecting…" status. Unroute, then dispatch
  `visibilitychange` on `document` from `page.evaluate` (headless Chrome reports
  `visible`). Expect the status to go and Send to be enabled. Without the fix
  the browser's retry sits on the unanswered request and nothing reopens the
  stream, so the status stays. No existing test stalls a reconnect, and the
  existing drop tests recover through the browser's retry, so they pass on the
  old code.
- The existing "a reconnecting stream shows as a status, not an error", "a
  closed stream offers a Reload button that reconnects", and the reconnect cases
  that route `**/events` to `abort()` cover the page's own timer reopening after
  a drop and the `502` refusal, and keep passing.

## Implementation plan

1. `cmd/ox/web/src/connection.ts`: replace `source` and `connection` with the
   `stream` union; replace `lose()` with `drop()`; make `open()` attach handlers
   to its own `EventSource` and set `stream` to `connecting`; set `connected` in
   the `connection` listener; add the `visibilitychange` listener; update
   `rpc()`, its fetch failure, and `close()`; update the `Disconnected` comment.
2. `cmd/ox/web/e2e/session.spec.ts`: add the test above.
3. `docs/architecture.md`: update the `connection.ts` row and the paragraph on
   stream errors.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md` — the `connection.ts` row becomes "Opening the event
  stream and reopening it after a drop, sending requests, matching replies, and
  reporting stream errors." The paragraph after the table opens: "The page
  reopens the event stream a second after it drops, and at once when the page
  becomes visible while it is not connected. It shows stream errors while
  reconnecting and asks for a reload once `ox` refuses the stream." The rest of
  the paragraph stands.
