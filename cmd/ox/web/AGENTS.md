# Web client

Use `make dev` to work on the [web client](../../../docs/architecture.md#web-ui).

- Lint and format with Biome: `npm run lint` checks, `npm run fix` rewrites.
  `make test` runs the check.
- The user checks UI changes by eye. Report the views a change touches. Open
  a browser only when the user asks, and drive it with Playwright.

## Solid 2.0

Import reactive primitives and flow components from `solid-js`. Import DOM
APIs (`render`, `hydrate`, `Portal`, `dynamic`) from `@solidjs/web`, and use
`@solidjs/web` as `jsxImportSource`.

- Read props through the props object (`props.name`); pass values (`<C value={count()} />`).
- Read reactive values in JSX or a tracking computation. Read flow callback
  accessors in JSX. Use `untrack` for an untracked read.
- Write from event handlers, `onSettled`, or actions.
- State writes batch in a microtask. In tests, use [`flush`](#signals-and-memos)
  before asserting the new value.
- Pass a stable event handler and choose which action to take inside it.
- See [Effects and lifecycle](#effects-and-lifecycle) for `createEffect` and
  [Stores](#stores) for draft writes.

### Pointers

- Reference: `node_modules/solid-js/CHEATSHEET.md`, the [docs index](https://docs.solidjs.com/llms.txt),
  and the [full docs](https://docs.solidjs.com/llms-full.txt).
- Dev diagnostics identify the code and owner. Use the
  [diagnostic reference](https://docs.solidjs.com/reference/solid-js/diagnostics.md)
  linked from the console to address the cause.

### solid-js exports

One line per public export of `solid-js`; the reference pages in the index carry the signatures.

#### Signals and memos

| Export           | Purpose                                                                     |
| ---------------- | --------------------------------------------------------------------------- |
| `createSignal`   | `[get, set]` reactive value; `createSignal(fn)` is a writable derived value |
| `createMemo`     | read-only derived value; a returned Promise is pending until it lands       |
| `createDeferred` | async memo that keeps its last value while a refetch is in flight           |
| `untrack`        | run `fn` without subscribing to the reads inside it                         |
| `flush`          | apply queued writes now instead of on the next microtask                    |
| `isEqual`        | default equality of signals and memos                                       |

#### Effects and lifecycle

| Export                | Purpose                                                                         |
| --------------------- | ------------------------------------------------------------------------------- |
| `createEffect`        | `(compute, apply)`: compute tracks, apply runs effects and may return a cleanup |
| `onSettled`           | run once after the current activity settles; return a cleanup                   |
| `onCleanup`           | cleanup tied to a reactive run or owner disposal; for primitive authors         |
| `createRenderEffect`  | effect that runs during render, for DOM bindings; app code uses `createEffect`  |
| `createTrackedEffect` | single-callback effect that tracks and applies in one scope; rare               |
| `createReaction`      | one-shot tracked callback: `track(() => dep())` runs `fn` on the next change    |

#### Stores

| Export             | Purpose                                                                         |
| ------------------ | ------------------------------------------------------------------------------- |
| `createStore`      | deep reactive object; `set(draft => { ... })` writes through the draft          |
| `createProjection` | read-only derived store; `{ key }` gives keyed reconciliation                   |
| `reconcile`        | `reconcile(data, key)(draft.part)` merges new data into a store sub-tree by key |
| `deep`             | track every nested level of a store (default tracking is per property)          |
| `snapshot`         | plain untracked copy of a store for JSON, storage and tests                     |
| `isWrappable`      | whether a value would become a store proxy                                      |
| `merge`            | merge props-like objects into one reactive proxy                                |
| `omit`             | reactive proxy of props without the listed keys                                 |

#### Async, actions and transitions

| Export                  | Purpose                                                                                  |
| ----------------------- | ---------------------------------------------------------------------------------------- |
| `action`                | generator mutation: optimistic writes, `yield` async work, `refresh`; reverts on failure |
| `createOptimistic`      | signal whose writes inside an action show now and revert when it settles                 |
| `createOptimisticStore` | store form of `createOptimistic`                                                         |
| `isPending`             | true while a change to the values read inside `fn` is in flight                          |
| `latest`                | read the in-flight value during a transition instead of the committed one                |
| `refresh`               | re-run one source after a server write; quiet unless `affects` was declared              |
| `affects`               | mark a source or store slot as pending while in-flight work will change it               |
| `until`                 | Promise for the first time a reactive predicate turns truthy; `yield` it in an action    |
| `resolve`               | Promise for the first settled value of a reactive expression (tests)                     |
| `NotReadyError`         | thrown by a read of an async source before its first value; boundaries handle it         |
| `TimeoutError`          | rejection from `until` when its `timeout` passes                                         |
| `enableExternalSource`  | adapter so Solid tracks reads of another reactive system (MobX, Vue)                     |

#### Components and context

| Export             | Purpose                                                                                       |
| ------------------ | --------------------------------------------------------------------------------------------- |
| `children`         | resolve `props.children` once; `.toArray()` helper                                            |
| `flatten`          | resolve nested accessors and arrays in a children tree (`children` memoizes it)               |
| `createContext`    | context for a subtree; `<Ctx value={v}>` is the provider; no default means no provider throws |
| `useContext`       | read the nearest context value                                                                |
| `lazy`             | code-split component; pending through `<Loading>` while its chunk loads                       |
| `lazyModule`       | cached dynamic import of a non-component module; `.preload()` and `.peek()`                   |
| `dynamicComponent` | component that re-resolves `fn` to a component; the factory behind `dynamic`                  |
| `createUniqueId`   | stable id that matches between server and client (`for`, `aria-labelledby`)                   |

#### Flow components

| Export     | Purpose                                                                          |
| ---------- | -------------------------------------------------------------------------------- |
| `For`      | keyed list; callback gets `(item, i())`, or `(item(), i)` with `keyed={false}`   |
| `Repeat`   | render `count` times; `i` is a plain number                                      |
| `Show`     | render children when `when` is truthy; a function child gets a narrowed accessor |
| `Switch`   | render the first `<Match>` whose `when` is truthy                                |
| `Match`    | one branch of `<Switch>`                                                         |
| `Loading`  | async boundary; `on={deps}` re-arms it                                           |
| `Errored`  | error boundary; `fallback={(err, reset) => ...}`                                 |
| `Reveal`   | reveal order of sibling `<Loading>` boundaries                                   |
| `mapArray` | reactive array map behind `<For>`; reuses entries for unchanged items            |
| `repeat`   | reactive count-based render behind `<Repeat>`                                    |

#### Owners and roots

| Export         | Purpose                                                               |
| -------------- | --------------------------------------------------------------------- |
| `createRoot`   | owned reactive scope with `dispose()`; tests and library entry points |
| `getOwner`     | current owner, to capture across an async gap in library code         |
| `runWithOwner` | run `fn` under an owner; `runWithOwner(null, fn)` detaches a root     |
| `getObserver`  | the currently tracking computation, or `null`                         |
| `isDisposed`   | whether an owner has been disposed, to ignore late work               |

#### Hydration (advanced)

| Export           | Purpose                                                               |
| ---------------- | --------------------------------------------------------------------- |
| `NoHydration`    | opt a subtree out of client hydration                                 |
| `Hydration`      | opt a subtree back in under `<NoHydration>`                           |
| `isHydrating`    | whether code runs while claiming server-rendered DOM                  |
| `isHydratable`   | whether the calling owner sits where hydration applies                |
| `onHydrationEnd` | run once after the root pass and every streamed boundary have resumed |

#### Dev and observability

| Export                  | Purpose                                                                           |
| ----------------------- | --------------------------------------------------------------------------------- |
| `DEV`                   | dev tier: devtools hooks, graph traversal, diagnostics reporting; dev builds only |
| `OBSERVE`               | observe tier: records channel, diagnostics channel, attribution slot              |
| `configureClientErrors` | hook called once per error when a boundary renders its fallback                   |
