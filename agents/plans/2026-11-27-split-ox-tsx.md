# Split ox.tsx into snapshot projection, session control, and daemon store

## Goal

`cmd/ox/web/src/ox.tsx` (938 lines) holds three layers: a pure projection of `Update`s into a `SessionSnapshot`, the per-session controller `createSessionControl` (~380 lines), and the daemon store `createOx`. Split them into three modules with no change in behavior, and make the two cleanups the issue names. Resolves [#201](https://github.com/kkestell/ox/issues/201).

## Related code

- `cmd/ox/web/src/ox.tsx` — all three layers today.
- `cmd/ox/web/src/{App,ApprovalRequest,Attachments,FileTree,SessionView,Sidebar,TabArea,TabPicker,Workspace}.tsx`, `poll.ts` — import types and `createAttempt` from `./ox`.
- `cmd/ox/web/e2e/session.spec.ts` — covers the attach errors ("Attach PNG…", "at most 10 MiB"); the rest of the e2e specs cover send, create, configure, Stop, approvals, and reconnect through the controller.

## Decisions

- **Construction:** The new modules form a one-way import chain: `snapshot.ts` and `attempt.ts` import nothing local except `protocol`; `sessionControl.ts` imports them, `protocol`, `connection`, `id`, `tabs`; `ox.tsx` imports all. `sessionControl.ts` does not import `Session` from `ox.tsx`. `createSessionControl` takes `snapshot: () => SessionSnapshot | undefined` (the live snapshot) and `state: () => SessionState | undefined` instead of `session: () => Session | undefined`. It reads only those two: `live()` is `snapshot()`, `canStop` and `canConfigure` read `state()`, and `recoverable` is `state() !== undefined`, which holds exactly when `session() !== undefined` did. `ox.tsx` builds both from `stored(id)`. This narrows what the controller sees without a cycle.
- **`snapshot.ts`:** `Entry`, `Approval`, `SessionSnapshot`, `settingsOf`, `stopTools`, `apply` (exported), and `snapshotOf(result: Methods["session/get"][1])`, which holds the snapshot-building code now inline in `load` (settings, history applied, approvals, queued). `load` keeps the `reconcile` merge into the store's previous snapshot, because that is store writing.
- **`attempt.ts`:** `createAttempt` moves here so `Sidebar` and `sessionControl.ts` share it without a cycle. It returns `[error, attempt, clear, fail]`; `fail(message)` sets the error synchronously.
- **`sessionControl.ts`:** `createSessionControl`, `SessionControl`, `Outgoing`, `Request`, `Attention`, `imageTypes`, `maxImageBytes`, `base64`, `CardDraft`, `QuestionAnswer`, `FormDraft`, `Draft`. Moved verbatim apart from the changes below.
- **`ox.tsx` keeps:** `Workspace`, `Session`, `directoryName`, `ListRequest`, `createOx`, `OxProvider`, `useOx`.
- **`attach()`:** the two validation refusals call a local `refuse(message)` = `setNotice(undefined); fail(message)`, replacing `start(Promise.reject(new Error(...)))`. The error shows in the same tick instead of a microtask later.
- **`post()`:** `settle(mine)` runs once on each path and the trailing `.finally` is removed: the success handler settles before `reconcilePrompt` (which reads `submitting()`, derived from the request slot), and the failure handler runs `restore(prompt)`, `settle(mine)`, then rethrows. Same order as before; the success-path comment says why settle precedes reconcile.
- **Non-chat prop narrowing:** not done. `FileTree`, `TabArea`, `Attachments`, and `ApprovalRequest` keep taking `SessionControl`; narrowing them is not a by-product of the split and would touch #207's files.
- **Requirements:** none touched; no behavior changes. Reconnect, in-flight requests, and drafts are carried by the same code.
- Ownership, settings, settling, process I/O, Claude Code, and trust boundaries: untouched. No new state, timer, or stored field.

## Check your assumptions

None.

## Test plan

- No new behavior. The existing e2e specs observe the move: attach refusals (`session.spec.ts`), send/create/configure/Stop and approval drafts, reconnect. `npm run lint` (Biome and `tsc`) checks the new imports; `make test` and `make e2e` run.
- No new test: the `fail` setter and `post()` restructure are not observable by a user beyond what `session.spec.ts` already asserts.

## Implementation plan

1. Create `src/snapshot.ts` with the items above; export `apply` and `snapshotOf`.
2. Create `src/attempt.ts` with `createAttempt` and the `fail` setter.
3. Create `src/sessionControl.ts` with the controller and its types; swap `session` for the `snapshot` and `state` accessors; apply the `attach()` and `post()` changes.
4. Reduce `src/ox.tsx` to the daemon store: import from the new modules, pass the two accessors to `createSessionControl`, use `snapshotOf` in `load`. Re-export nothing.
5. Update imports in `App`, `ApprovalRequest`, `Attachments`, `FileTree`, `SessionView`, `Sidebar`, `TabArea`, `TabPicker`, `Workspace`, and `poll` to the module that now owns each name.
6. Run `npm run lint`, `make test`, `make e2e`.

## Requirement changes

None.
