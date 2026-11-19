# Web client

A Solid 2.0 app built with Vite. `make web` builds it into `dist`, and
`make dev` serves it with the Vite dev server.

- Lint and format with Biome: `npm run lint` checks, `npm run fix` rewrites.
  `make test` runs the check.
- The user checks UI changes by eye. Report the views a change touches. Open
  a browser only when the user asks, and drive it with Playwright.

## Solid 2.0

> The short form for tools with a small context budget: the rules that differ from React and Solid 1.x, the 1.x to 2.0 corrections, and one line per `solid-js` export.

Index: https://docs.solidjs.com/llms.txt
Full corpus: https://docs.solidjs.com/llms-full.txt
Diagnostic codes: https://docs.solidjs.com/reference/solid-js/diagnostics.md (one page per code, linked from each console report)

Solid is not React, and 2.0 is not 1.x. Pattern-matching from either is the
dominant bug source. Every name below is a `solid-js` export unless noted;
DOM APIs (`render`, `hydrate`, `Portal`, `dynamic`) are in `@solidjs/web`.

### Three rules

1. **Props are read, not destructured; pass values, not accessors.**
   - Wrong: `function C({ name }) {}` and `<C value={count} />`
   - Right: `function C(props) { return <p>{props.name}</p>; }` and `<C value={count()} />`
2. **`createEffect` takes two functions: compute (tracks), then apply (side effects).**
   - Wrong: `createEffect(() => { el.title = count(); });`
   - Right: `createEffect(() => count(), v => { el.title = v; });`
3. **Stores are written through the draft.**
   - Wrong: `setStore("user", "name", "B")` or `setStore(produce(s => ...))`
   - Right: `setStore(s => { s.user.name = "B"; });`

### 1.x → 2.0 corrections

Nothing in the left column exists in 2.0.

| Wrong (1.x) | Right (2.0) |
| --- | --- |
| `import { createStore } from "solid-js/store"` | `import { createStore } from "solid-js"` |
| `import { render } from "solid-js/web"` | `import { render } from "@solidjs/web"` |
| `"jsxImportSource": "solid-js"` | `"jsxImportSource": "@solidjs/web"` |
| `createEffect(fn)` (one argument) | `createEffect(compute, apply)` |
| `onMount(fn)` | `onSettled(() => { setup(); return cleanup; })` |
| `createEffect(on(dep, fn))` | `createEffect(() => dep(), v => fn(v))` |
| `const [d] = createResource(id, fetchX)` | `const d = createMemo(() => fetchX(id()))` under `<Loading>` |
| `d.loading` / `d.error` | `<Loading>` or `isPending(() => d())` / `<Errored>` |
| `refetch()` | `refresh(d)` (`affects(d); refresh(d)` to read as pending) |
| `<Suspense>` / `<SuspenseList>` | `<Loading>` / `<Reveal>` |
| `<ErrorBoundary>` | `<Errored fallback={(err, reset) => ...}>` (heals automatically) |
| `<Index each={xs}>` | `<For each={xs} keyed={false}>` |
| `batch(fn)` | not needed: updates batch per microtask; `flush()` applies now |
| `startTransition` / `useTransition` | built-in transitions: `action(...)`, `isPending`, optimistic APIs |
| `createComputed` | `createMemo`, split `createEffect`, or `createSignal(fn)` |
| `mergeProps` / `splitProps` / `unwrap` | `merge` / `omit` / `snapshot` |
| `produce`, `createMutable`, `modifyMutable` | draft setters are the default in `createStore` |
| `createSelector` | `createProjection` (or `createStore(fn)`) |
| `<Ctx.Provider value={v}>` | `<Ctx value={v}>` |
| `classList={{ a: x() }}` | `class={{ a: x() }}` or `class={["base", { a: x() }]}` |
| `use:dir={x}` / `attr:` / `on:` / `tabIndex` | `ref={dir(x)}` / plain attributes / `onClick` / `tabindex` |
| `onError` / `catchError` | `<Errored>` or `createEffect(c, { effect, error })` |
| `from(source)` / `observable` | return an AsyncIterable from a computation |

Also:

- Reads lag writes: `setX(1); x()` still returns the old value until the
  microtask or `flush()`. In tests, `flush()` before asserting.
- No writes inside a memo, effect compute, or component body (dev throws).
  Write from event handlers, `onSettled`, or actions.
- No reactive reads at the top of a component body; read in JSX, a memo, or
  `untrack`. Flow callbacks (`<For>`, `<Show>`, `<Match>`) are owners, not
  tracking scopes: read their accessor arguments in JSX, not at the top.
- `<For>`: default gives a raw item and an index accessor; `keyed={false}`
  gives an item accessor and a plain number. `<Repeat>`: `i` is a number.
- `onClick={cond() ? a : b}` binds once; decide inside a stable handler.

### Primitives to reach for

| Primitive | Use |
| --- | --- |
| `createSignal(0)` / `createSignal(() => fetchUser(id()))` | plain signal / writable derived value, sync or async; read by calling it |
| `createMemo(() => fetchX(id()))` | read-only derived value; a Promise result is pending until it lands |
| `createProjection(async () => api.list(), [], { key: "id" })` | read-only derived store with keyed reconciliation |
| `createOptimisticStore(() => api.list(), [])` | store whose optimistic writes revert when the transition settles |
| `action(function* (x) { write(); yield api.put(x); refresh(d); })` | mutation with transition coordination; optimistic writes go inside |
| `until(() => confirmed(), { timeout })` | Promise that resolves when a predicate turns truthy; `yield` it in an action |
| `isPending(() => d())` | true while a change to `d` is in flight (input changed or `affects()` declared) |
| `latest(d)` | peek at the in-flight value during a transition |
| `refresh(d)` | re-ask the source after a server write; quiet unless preceded by `affects(d)` |
| `<Loading on={id()}>` `<Errored>` `<Reveal>` `<Repeat>` `<For>` | async boundary (`on` re-arms it), error boundary, sibling order, range list, keyed list |

### Pointers

- Full reference: `node_modules/solid-js/CHEATSHEET.md`; the same text is
  the `solid-2.0-cheatsheet` skill in `node_modules/solid-js/skills/`.
- Diagnostics: dev builds print `[CODE] message` plus an `in <App> › …`
  owner line. The first report of each code ends with a footer naming
  `node_modules/solid-js/skills/reactivity-diagnostics/SKILL.md`, one section
  per code (`STRICT_READ_UNTRACKED`, `REACTIVE_WRITE_IN_OWNED_SCOPE`,
  `LOADING_ON_OUTSIDE_HOLD`, …). Fix the cause; never suppress a code.
- Docs: https://docs.solidjs.com (Solid 2.0 docs; `/llms.txt` and
  `/llms-full.txt` for agents). Migration guide: `documentation/solid-2.0/MIGRATION.md`
  in the repository.

### solid-js exports

One line per public export of `solid-js`; the reference pages in the index carry the signatures.

#### Signals and memos

| Export | Purpose |
| --- | --- |
| `createSignal` | `[get, set]` reactive value; `createSignal(fn)` is a writable derived value |
| `createMemo` | read-only derived value; a returned Promise is pending until it lands |
| `createDeferred` | async memo that keeps its last value while a refetch is in flight |
| `untrack` | run `fn` without subscribing to the reads inside it |
| `flush` | apply queued writes now instead of on the next microtask |
| `isEqual` | default equality of signals and memos |

#### Effects and lifecycle

| Export | Purpose |
| --- | --- |
| `createEffect` | `(compute, apply)`: compute tracks, apply runs effects and may return a cleanup |
| `onSettled` | run once after the current activity settles; return a cleanup (replaces `onMount`) |
| `onCleanup` | cleanup tied to a reactive run or owner disposal; for primitive authors |
| `createRenderEffect` | effect that runs during render, for DOM bindings; app code uses `createEffect` |
| `createTrackedEffect` | single-callback effect that tracks and applies in one scope; rare |
| `createReaction` | one-shot tracked callback: `track(() => dep())` runs `fn` on the next change |

#### Stores

| Export | Purpose |
| --- | --- |
| `createStore` | deep reactive object; `set(draft => { ... })` writes through the draft |
| `createProjection` | read-only derived store; `{ key }` gives keyed reconciliation |
| `reconcile` | `reconcile(data, key)(draft.part)` merges new data into a store sub-tree by key |
| `deep` | track every nested level of a store (default tracking is per property) |
| `snapshot` | plain untracked copy of a store for JSON, storage and tests (replaces `unwrap`) |
| `isWrappable` | whether a value would become a store proxy |
| `merge` | merge props-like objects into one reactive proxy (replaces `mergeProps`) |
| `omit` | reactive proxy of props without the listed keys (replaces `splitProps`) |

#### Async, actions and transitions

| Export | Purpose |
| --- | --- |
| `action` | generator mutation: optimistic writes, `yield` async work, `refresh`; reverts on failure |
| `createOptimistic` | signal whose writes inside an action show now and revert when it settles |
| `createOptimisticStore` | store form of `createOptimistic` |
| `isPending` | true while a change to the values read inside `fn` is in flight |
| `latest` | read the in-flight value during a transition instead of the committed one |
| `refresh` | re-run one source after a server write; quiet unless `affects` was declared |
| `affects` | mark a source or store slot as pending while in-flight work will change it |
| `until` | Promise for the first time a reactive predicate turns truthy; `yield` it in an action |
| `resolve` | Promise for the first settled value of a reactive expression (tests) |
| `NotReadyError` | thrown by a read of an async source before its first value; boundaries handle it |
| `TimeoutError` | rejection from `until` when its `timeout` passes |
| `enableExternalSource` | adapter so Solid tracks reads of another reactive system (MobX, Vue) |

#### Components and context

| Export | Purpose |
| --- | --- |
| `children` | resolve `props.children` once; `.toArray()` helper |
| `flatten` | resolve nested accessors and arrays in a children tree (`children` memoizes it) |
| `createContext` | context for a subtree; `<Ctx value={v}>` is the provider; no default means no provider throws |
| `useContext` | read the nearest context value |
| `lazy` | code-split component; pending through `<Loading>` while its chunk loads |
| `lazyModule` | cached dynamic import of a non-component module; `.preload()` and `.peek()` |
| `dynamicComponent` | component that re-resolves `fn` to a component; the factory behind `dynamic` |
| `createUniqueId` | stable id that matches between server and client (`for`, `aria-labelledby`) |

#### Flow components

| Export | Purpose |
| --- | --- |
| `For` | keyed list; callback gets `(item, i())`, or `(item(), i)` with `keyed={false}` |
| `Repeat` | render `count` times; `i` is a plain number |
| `Show` | render children when `when` is truthy; a function child gets a narrowed accessor |
| `Switch` | render the first `<Match>` whose `when` is truthy |
| `Match` | one branch of `<Switch>` |
| `Loading` | async boundary (replaces `Suspense`); `on={deps}` re-arms it |
| `Errored` | error boundary (replaces `ErrorBoundary`); `fallback={(err, reset) => ...}` |
| `Reveal` | reveal order of sibling `<Loading>` boundaries (replaces `SuspenseList`) |
| `mapArray` | reactive array map behind `<For>`; reuses entries for unchanged items |
| `repeat` | reactive count-based render behind `<Repeat>` |

#### Owners and roots

| Export | Purpose |
| --- | --- |
| `createRoot` | owned reactive scope with `dispose()`; tests and library entry points |
| `getOwner` | current owner, to capture across an async gap in library code |
| `runWithOwner` | run `fn` under an owner; `runWithOwner(null, fn)` detaches a root |
| `getObserver` | the currently tracking computation, or `null` |
| `isDisposed` | whether an owner has been disposed, to ignore late work |

#### Hydration (advanced)

| Export | Purpose |
| --- | --- |
| `NoHydration` | opt a subtree out of client hydration |
| `Hydration` | opt a subtree back in under `<NoHydration>` |
| `isHydrating` | whether code runs while claiming server-rendered DOM |
| `isHydratable` | whether the calling owner sits where hydration applies |
| `onHydrationEnd` | run once after the root pass and every streamed boundary have resumed |

#### Dev and observability

| Export | Purpose |
| --- | --- |
| `DEV` | dev tier: devtools hooks, graph traversal, diagnostics reporting; dev builds only |
| `OBSERVE` | observe tier: records channel, diagnostics channel, attribution slot |
| `configureClientErrors` | hook called once per error when a boundary renders its fallback |
