# Load only the selected session

## Goal

Reloading the page takes 3–5 s before the selected session shows. `reload()` in `cmd/ox/web/src/ox.tsx` sends `session/get` for every listed session (374 on the user's Ox, ~68 MB of history over the one event stream, parsed and replayed on the main thread), and the selected session's response queues among them.

Only `SessionView` reads a snapshot, and only for the selected session. The sidebar reads `state`, which `workspace/list` returns and `state` and `parked` updates keep current. After the change the page sends `session/get` only for the session it shows: on selecting it, and on reconnect or reload for the shown session alone.

## Related code

- `cmd/ox/web/src/ox.tsx` — `reload`, `load`, `receive` (`session/created` loads every new session), `onError` (marks every session `reloading`), `createSessionControl` (`live()`, `reconcilePrompt`, `reconcileApprovals`, `recoverable`).
- `cmd/ox/web/src/App.tsx` — `Layout` owns the selection; its `Match` renders `SessionView` once `session.load.snapshot` exists; its recovery effect and `finish` reattach a draft to its session.
- `cmd/ox/web/e2e/session.spec.ts` — specs that hold `session/get` (`hold`, `rpcCalls` in `e2e/ui.ts`).

## Decisions

- **Owner of "shown":** `createOx` holds `shown`, the ID of the session the page shows, set by `ox.show(id | undefined)`. `Layout` calls it from a `createEffect` on the selected session's ID (undefined for a draft or nothing). The selection stays owned by `Layout`; `ox` only learns it.
- **Construction:** a snapshot exists only for the shown session. `Session.load` becomes `{ kind: "unloaded" } | { kind: "loading"; snapshot?: SessionSnapshot } | { kind: "live"; snapshot: SessionSnapshot }`. `unloaded` replaces the old `reloading` with no snapshot, and is where every listed session starts; `loading` is a `session/get` in flight, or the shown session after a disconnect with its old snapshot kept, as `reloading` was.
- **Switching away drops the snapshot** (`show` sets the previous shown session to `unloaded`). Updates for unshown sessions are already ignored by `receive` when `load.kind !== "live"`, and keeping a snapshot would need updates applied to sessions nobody sees, and a reconnect policy for them. The view is keyed per session, so its open tool details and scroll are already lost on switching; coming back shows "Loading session…" for one `session/get` (18 ms in oxd for the largest session).
- **`show(id)`:** records `shown`, drops the old one, and if connected and the session is listed sets `loading` and calls `load(id, rpc)`. `reload` calls `load` for `shown` only, after `setLive`. A hash naming no listed session loads nothing.
- **`load` result is installed only if `id === shown`** when the response arrives, and the retry loop stops (returns undefined) once `id !== shown`. A switch A→B→A with two gets in flight installs both in order; the second reconciles over the first, and updates apply only after a response installs a live snapshot, as today.
- **`onError`:** `live` becomes `loading` with its snapshot; `unloaded` stays.
- **`session/created`:** no `load`. A session created by this page is selected by `finish`, which calls `show`; a child created by an agent stays unloaded until opened.
- **Controls (Requirements: Unsent text, Attached images, Only the user sends):** controls outlive loads, so text, images, answers, expanded folders and open file survive switching. An outgoing prompt for a session switched away from stays `sent`; `reconcilePrompt` and `reconcileApprovals` already run on each `load`, so returning to the session resolves it exactly as a reconnect does (taken, or returned to the composer if dropped). Prompt and approval updates while unshown are ignored, and the next snapshot carries their result. `busy` reads the session list, not the snapshot, and is unaffected.
- **Lost creation reply (Requirements: Lost creation reply):** `recoverable` required `live() !== undefined`, which was true only because every created session loaded. It becomes "unconfirmed and the session is listed" (`session() !== undefined`); the recovery effect in `App.tsx` then calls `recover()` and `finish` selects the session, which loads it and shows its current settings. The `d === draft() || session()?.id === d.id` guard stays.
- **Settling:** the one `session/get` per selection uses the existing `retry`, which ends with the stream or now with a switch away.
- **Requirements:** Session status ("as soon as the session list shows it") still holds, since it reads `state`. No requirement states that switching or reconnecting loads every session. Only the user sends: `session/get` is a read, nothing sends.
- Protocol, `oxd`, and docs are unchanged.

## Check your assumptions

None.

## Test plan

- e2e, `session.spec.ts`, new test "reloading loads only the selected session": create two sessions, collect `rpcCalls(page, "session/get")`, reload; expect exactly the selected session's ID, both rows still show their status, and the transcript shows. Then select the other session and expect one more `session/get` for it. Fails today (a call per session). No existing test can carry it: none counts `session/get` by session.
- e2e, same file, new test "a reconnect reloads only the shown session": with two sessions, `restartRelay`, expect `session/get` only for the shown one.
- Existing specs that hold `session/get` with no filter (`session list membership and order don't wait for transcripts`, the reconnect spec near line 1394, `a retried creation recovers the session without sending`) stay and must pass; adjust a `hold` only if it assumed a get for an unshown session, passing `session_id` to it.
- Switching away and back: new test "a prompt sent before switching away resolves when its session is shown again": hold the `session/send` reply, switch away, release, return, and expect the transcript to hold the prompt and the composer to be empty. Guards the `reconcilePrompt` path without a live snapshot.

## Implementation plan

1. `ox.tsx`: change `Session.load` to the three-kind union; update the comment above `Session` (`state` follows the session whether or not it is loaded).
2. `ox.tsx`: add `shown`, `show(id)`, the `id === shown` guards in `load`; `reload` loads `shown` only and creates sessions `unloaded`; `session/created` stops loading; `onError` maps `live` to `loading`; `recoverable` uses `session()`; return `show` from `createOx`.
3. `App.tsx`: `createEffect` on the selected session's ID calling `ox.show`; the `Show` fallback keeps `load.snapshot`.
4. Add the tests above to `session.spec.ts`; run `make test` and `make e2e`.

## Requirement changes

None.
