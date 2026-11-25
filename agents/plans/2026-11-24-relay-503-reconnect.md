# A relay 503 ends the page's stream

## Goal

A `503` from the relay means the page's `oxd` connection is gone, but the page
treats it as the failure of one request and keeps the stream selected. During
catch-up, `reload`'s `retry` stops on the `Disconnected` and nothing opens a
replacement stream until the browser happens to see the event stream end, so
Send and settings stay disabled. On a ready page, readiness and pending calls
stay on a stream the relay has said it cannot use.

After the change, a `503` on the current stream ends that stream exactly as a
POST that cannot reach the relay does: pending calls fail, the page shows
Reconnecting, and a replacement stream opens a second later and catches up. A
`503` on a stream that has already ended rejects only its own call.

Resolves [#84](https://github.com/kkestell/ox/issues/84), audit finding
[F1](../audits/2026-11-24-structural-audit-2026-11-24.md#f1-a-relay-503-can-leave-send-disabled-after-reconnect).

## Related code

- `cmd/ox/web/src/connection.ts` — `connect` owns the stream (`Stream` union),
  `drop` ends it, and `send` binds each POST to its stream ID. The `.then`
  branch rejects one call on `503`; the `.catch` branch calls `drop` when the
  stream is still the current one.
- `cmd/ox/main.go` — `relay.rpc` answers `503` when the stream ID is unknown or
  the write to `oxd` fails. Unchanged.
- `cmd/ox/web/src/ox.tsx` — `retry` returns `undefined` on `Disconnected`, and
  `onError` clears readiness and marks snapshots for reload. Both already rely
  on the connection having ended the stream; unchanged.
- `cmd/ox/web/src/poll.ts` — keeps the last answer on `Disconnected`.
  Unchanged.
- `docs/architecture.md` ("Connection and reload") — already states that a
  failed `/rpc`, including `503`, drops the connection like a stream error.

## Decisions

- **Construction:** `connect` is the single owner of ending the current
  stream. Inside `send`, a `503` and a rejected fetch are one case, the
  stream's loss, handled by one local function: if the stream is still the
  connected one with ID `sentOn`, `drop` it; otherwise reject only this call.
  The page never holds a `Disconnected` error while the stream that produced
  it is still selected, so `retry`, `onError`, and `createPoll` keep their
  current reading of `Disconnected`. A `current()` check
  (`stream.kind === "connected" && stream.connection === sentOn`) replaces the
  two copies of that condition in `send`.
- **Message:** the loss uses `"Lost the connection to ox. Reconnecting."`, the
  text a rejected fetch already uses. The relay's body (`unknown connection`
  or a socket write error) is not shown. Other non-OK statuses (`400`, `413`)
  keep rejecting only their call with the relay's text.
- **Requirements:**
  - **Lost connection.** A `503` now says so, fails pending requests, holds
    Send and settings, and reconnects after a second. If `oxd` is still down,
    the replacement stream is refused and the page shows Reload, as with any
    other drop.
  - **Send cut off.** A send whose POST gets a `503` fails with the lost
    connection message through `drop`'s `failPending`, and is not resent.
  - **File tree updates** and **File viewer updates:** `createPoll` keeps the
    last answer on the `Disconnected`, unchanged.
  - A `503` on a request from an ended stream, such as a stale reload, rejects
    that call and leaves the replacement alone, as the `.catch` branch does
    today.
- **Settling:** no new wait. The reconnect is `drop`'s existing one-second
  timer, cleared by `close` and by `onVisible`.
- **Sibling change (#86):** this plan does not touch `ox.tsx` or `poll.ts`.
  The clock in `ox.tsx` advances only while `ready()`, so a `503` on a ready
  page now stops the clock for about a second until the replacement stream
  catches up, the same as any other drop.

## Check your assumptions

None.

## Test plan

- A `503` on the replacement stream's `harness/list` during catch-up, with
  the event stream left open, ends in a reconnected page with Send enabled.
  New e2e test in `e2e/session.spec.ts`, beside "a reconnecting stream shows as
  a status, not an error": create a session, route `**/rpc*` to fulfill
  `harness/list` once with `503 unknown connection` and fall back otherwise,
  restart the relay, and expect Send enabled within 10 seconds. No existing
  test fails a catch-up request with the stream open. Fails before the change:
  `retry` stops and no stream replaces the open one.
- A `503` on `session/send` on a ready page, with the event stream open,
  shows the send's lost connection error, reconnects, re-enables Send, and
  posts the prompt once. New e2e test in `e2e/session.spec.ts` beside "a send
  cut off after acceptance shows an error without resending", using
  `rpcCalls` for the send count and asserting the main alert text
  `"Lost the connection to ox. Reconnecting."`. Fails before the change: the
  alert shows `unknown connection` and the stream is never replaced.
- "a lost connection keeps the last listing" in `e2e/workspace.spec.ts`
  replaces its two steps with one loop over both failures, a `503` and an
  aborted POST on the `src` listing. For each, with `**/events` aborted, the
  page shows Reconnecting, offers no Reload, keeps `a.txt` listed, and shows no
  file error; after both routes are removed and the clock runs a second,
  Reconnecting is gone and Send is enabled. The `503` case fails before the
  change: no Reconnecting status.
- "a request cut off after its connection was replaced leaves the new
  connection alone" covers the ended-stream path for a rejected fetch and
  stays as it is.

## Implementation plan

1. `cmd/ox/web/src/connection.ts`: in `send`, add `current()` and a `lost()`
   that calls `drop(new Disconnected("Lost the connection to ox.
   Reconnecting."))` when `current()` and otherwise `fail`s the call with the
   same error. Call `lost()` from the `503` branch of `.then` and from
   `.catch`. Use `current()` for the pre-send check. Update the comments to
   say a `503` and an unreachable relay both end the stream they were sent on.
2. `cmd/ox/web/e2e/session.spec.ts`: add the two tests above.
3. `cmd/ox/web/e2e/workspace.spec.ts`: rewrite "a lost connection keeps the
   last listing" as above.

## Requirement changes

None.
