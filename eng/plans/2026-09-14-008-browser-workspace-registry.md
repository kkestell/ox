# Browser workspace registry

## Goal

Persist the browser host's validated server-local workspace roots and let the
browser register, select, and remove them through opaque identities. An empty
host must remain usable for registration, and neither snapshots nor any new HTTP
surface may expose general filesystem access.

## Related code

- `docs/spec.md#web-client` and `eng/client-architecture.md` — Observable
  multi-workspace behavior and the trusted-host boundary.
- `client/src/{host,protocol,browser,workspace-supervisor}.ts` — Current
  single-workspace startup, browser-safe state, UI, and root validation seams.
- `client/src/{host,protocol}.test.ts` and `client/e2e/smoke.spec.ts` — Focused
  protocol/host checks and the real browser-to-Ox workflow.
- `/Users/kyle/src/references/repos/personal/iota/crates/adapter-storage/src/lib.rs`
  — Prior atomic replacement pattern for small host-owned workspace state.

## Decisions

- Store a strict versioned registry at
  `$XDG_CONFIG_HOME/ox/workspaces.json`, falling back to
  `$HOME/.config/ox/workspaces.json` only from an absolute base. A missing file
  is an empty registry; malformed, unsupported, or unusable persisted entries
  fail startup instead of being discarded.
- Each entry contains a generated stable ID and its canonical root. Canonical
  roots are unique and must still be absolute, listable directories whenever
  the registry loads. Snapshots contain only the ID and basename-derived display
  name; the path crosses the browser boundary only in the bounded
  `register-workspace` command.
- Serialize registry mutations and atomically replace the owner-only file before
  publishing their state. The first registration becomes selected; later
  registrations preserve selection. Removing a workspace never changes its
  files or Ox session history, and removing the selected entry selects the first
  remaining entry or returns the host to its empty registration state.
- Keep one selected `WorkspaceSupervisor` in this slice and replace it when the
  selected registry entry changes. Retaining independently active supervisors,
  routing workspace-scoped commands between them, and preserving concurrent
  turns across selection belong to the next todo task.

## Test plan

- Unit-test registry path resolution, strict/versioned loading, canonical
  deduplication (including symlink aliases), atomic add/select/remove persistence,
  mutation failure, and removal without deleting workspace contents.
- Extend browser-protocol tests for bounded register/select/remove commands and
  browser-safe workspace catalogs that reject roots and arbitrary fields.
- Exercise an empty registry, invalid registration, two-root selection, selected
  removal, and page refresh through the real browser/host/Ox boundary; assert
  that canonical paths never appear in the rendered page or snapshots.

## Implementation plan

- Add a focused workspace-registry module that resolves the default path, loads
  and validates the versioned file, canonicalizes roots, assigns stable opaque
  IDs, serializes mutations, and persists by atomic replacement.
- Reshape the shared snapshot with a registered-workspace catalog and optional
  selected workspace state, and add the three validated workspace commands
  without adding filesystem HTTP routes or returning submitted paths.
- Change host startup to load the registry without requiring `--workspace`,
  orchestrate the currently selected supervisor, and publish one coherent
  revision after register/select/remove transitions and supervisor updates.
- Add an unstyled semantic workspace selector, absolute-path registration form,
  and removal action to `browser.tsx`, keeping paths and command errors in local
  form state rather than host snapshots.
- Update focused unit tests and the Playwright fixture to use isolated registry
  storage and cover the registry workflows without weakening the existing
  single-workspace ACP scenarios.

## Documentation updates

- Update `docs/browser.md` for registry-backed startup and browser-managed
  workspace paths, and check off this child task in `eng/todo.md` when it lands.
