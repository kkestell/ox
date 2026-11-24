# Bind each reload to the stream that started it

## Goal

A reload that is waiting to repeat a failed request can outlive its event
stream. When it wakes after a new stream has connected, it sends its requests
through whichever stream is current, installs a mix of the old and new
stream's results, loads every session, and enables Send while the new
stream's own reload is still waiting for its catalog. This breaks **Lost
connection**.

After the change, a reload's requests, including its repeats, go only to the
stream that started it, and fail without a request once that stream has
ended. Only the current stream's reload can install the catalog, mark the page
caught up, or load sessions.

Resolves [#72](https://github.com/kkestell/ox/issues/72), from
[audit finding F2](../audits/2026-11-24-audit-2026-11-24.md#f2-a-retry-from-a-closed-stream-enables-actions-early).

## Related code

- `cmd/ox/web/src/connection.ts` — owns the event stream's lifecycle (`Stream`
  union), the pending calls, and `rpc`, which sends on whichever stream is
  connected when it is called. Each stream has its own relay connection ID
  (`rand.Text()` in `cmd/ox/main.go`'s `events`), which every POST already
  carries as `sentOn`.
- `cmd/ox/web/src/ox.tsx` `retry` — repeats a request each second until it is
  answered; exits only on `Disconnected`.
- `cmd/ox/web/src/ox.tsx` `load` — loads one session's snapshot through
  `retry(() => connection.rpc("session/get", …))`.
- `cmd/ox/web/src/ox.tsx` `reload` — reads `harness/list` and `workspace/list`
  through the global `connection.rpc`, installs both, sets `ready`, and loads
  every session.
- `cmd/ox/web/src/ox.tsx` `receive` — `session/created` starts a `load` when
  `ready()`.
- `cmd/ox/web/src/ox.tsx` `onSettled` → `connect` — `onConnect` starts
  `reload`; `onError` clears `ready` and marks sessions reloading.
- `cmd/ox/web/src/SessionView.tsx:98`, `cmd/ox/web/src/Sidebar.tsx:330,343`,
  the clock interval in `ox.tsx`, and `createSessionControl` read `ready` as a
  boolean.
- `cmd/ox/web/e2e/session.spec.ts` — "a reconnect repeats a failed request
  until the page catches up" covers repeats within one stream; "a stalled
  reconnect resumes when the page is shown" covers reopening on
  `visibilitychange`.
- `cmd/ox/web/e2e/workspace.spec.ts` — `clocked`, the fixture that pauses the
  page's clock once it has loaded.

## Decisions

- **Construction:** the stream owns its requests' lifetime. `connect` hands
  `onConnect` an `Rpc` bound to that stream's connection ID. A call through it
  rejects with `Disconnected` unless the stream is still connected under that
  same ID, so a request from an ended stream's reload cannot reach a new
  stream. `reload` and `load` take that `Rpc` as a parameter and use nothing
  else, so every attempt of one reload, before and after a retry's sleep, uses
  one stream. The global `connection.rpc` remains only for the user's own
  actions, which belong to whatever stream is current when the user acts.
- **Construction:** the page's readiness holds the stream it belongs to.
  `ready`, a boolean signal today, becomes `live`, a signal holding
  `{ rpc: Rpc }` for the stream whose reload has installed the catalog and
  workspace list, or `undefined`. `ready` becomes the derived
  `() => live() !== undefined`, so its readers do not change. `reload` sets
  `live` with its own `rpc`; `onError` clears it. `session/created` loads the
  new session through `live()?.rpc`, so that load also belongs to a stream and
  ends with it. The value is wrapped in an object because a Solid setter
  treats a function argument as an updater.
- **Construction:** a reload that outlives its stream cannot publish. Its next
  request rejects with `Disconnected`, and `retry` returns `undefined` on
  `Disconnected` without calling `setError`. A reply already in hand when the
  stream drops cannot interleave, because the reply's continuation runs as a
  microtask of the stream's `message` event, before the stream's `error` event
  is dispatched.
- **Settling:** no wait is added. A retry's sleep still ends by its one-second
  bound; its next attempt on an ended stream rejects at once without a POST,
  and the reload or load returns.
- **Requirements:** **Lost connection** holds through each case. A drop clears
  `live`, failing pending calls as before. A new stream's reload enables Send
  only once that stream's `harness/list` and `workspace/list` have answered.
  A repeat after a failure on an open stream still repeats on that stream
  until answered, as "a reconnect repeats a failed request until the page
  catches up" checks. A refused (closed) stream leaves every old reload's next
  attempt failing with `Disconnected`, and the Reload button reloads the page
  as before. A second page has its own connection and is unaffected.
  **Work outlives the page** is untouched: no request reaches `oxd` that did
  not before, and fewer do.
- **Ownership:** no new state, timer, retry, or lock. `live` replaces `ready`
  as the same fact, now carrying the stream it was established on.

## Check your assumptions

None.

## Test plan

- **e2e, new test in `session.spec.ts`:** "a retry from a dropped stream does
  not use the next stream". It needs a paused clock to hold an old retry
  asleep across a reconnect, which no existing reconnect test has, so it is a
  new test run under `clocked`, which moves from `workspace.spec.ts` to
  `ui.ts` and is exported. Steps: create a session; hold `workspace/list`;
  `services.restartRelay()`; once it arrives, fail it, which puts the reload's
  retry to sleep on the paused clock. Hold the next `harness/list`. Start
  `rpcCalls(page, "workspace/list")`. `services.restartRelay()` again, then
  `showPage(page)` to reopen at once while the stream's reopen timer is
  paused; wait for the held `harness/list` to arrive. `page.clock.runFor(1000)`
  wakes the old retry. Expect Send disabled. Release `harness/list`, expect
  Send enabled, and expect exactly one `workspace/list` posted since the
  second drop. On the current code the old retry posts a second
  `workspace/list` through the new stream, so the count is two. The
  implementer confirms the test fails before the change.
- The existing "a reconnect repeats a failed request until the page catches
  up", "a stalled reconnect resumes when the page is shown", "on reconnect
  each session waits for its own settings", and "a reconnecting stream shows
  as a status, not an error" keep their assertions and must pass unchanged.
- Headless Chrome through `make dev` in this worktree: drop the relay
  (restart `ox`) with a session open, and confirm the page reconnects, reloads
  its sessions, and Send returns.

## Implementation plan

1. `cmd/ox/web/src/connection.ts`: export
   `type Rpc = <M extends keyof Methods>(method: M, params: Methods[M][0]) => Promise<Methods[M][1]>`
   and use it in `Connection`. Move the body of `rpc` into a function
   `send(sentOn: string): Rpc` that first rejects with
   `new Disconnected("Not connected to ox")` unless
   `stream.kind === "connected" && stream.connection === sentOn`, then posts as
   today. `Connection.rpc` rejects as today when not connected, otherwise
   calls `send(stream.connection)`. Change `onConnect()` to
   `onConnect(rpc: Rpc)` and call it with `send(event.data)` in the
   `connection` listener. Comment `onConnect`'s argument: it sends on the
   stream that connected and fails once that stream ends.
2. `cmd/ox/web/src/ox.tsx`: replace the `ready` signal with
   `const [live, setLive] = createSignal<{ rpc: Rpc }>()` and
   `const ready = () => live() !== undefined`, with a comment naming `live` as
   the stream whose reload has installed the catalog and workspace list.
   Give `load(id, rpc)` and `reload(rpc)` an `Rpc` parameter and send their
   requests only through it. `reload` sets `setLive({ rpc })` where it set
   `ready`, and loads sessions with `load(id, rpc)`. In `receive`,
   `session/created` loads through `live()`'s `rpc` when set. `onConnect`
   becomes `(rpc) => void reload(rpc)`; `onError` calls `setLive(undefined)`.
   Update the comments on `retry` and `reload` to say a reload belongs to the
   stream that started it.
3. `cmd/ox/web/e2e/ui.ts`: move `clocked` and its `start` date here from
   `workspace.spec.ts` and export it; import it in `workspace.spec.ts`.
4. `cmd/ox/web/e2e/session.spec.ts`: add the test above beside "a reconnect
   repeats a failed request until the page catches up".
5. `docs/architecture.md`: update the reconnect paragraph as below.

## Requirement changes

None.

## Documentation updates

- `docs/architecture.md`, the paragraph after the web client's file table:
  replace "A reload request that fails while the stream is connected repeats
  each second, and its failure shows as the page error until it is answered."
  with "Each stream's reload sends its requests, and their repeats, only on
  that stream, and ends when the stream drops. A reload request that fails
  while its stream is connected repeats each second, and its failure shows as
  the page error until it is answered. Only the current stream's reload marks
  the page caught up."
