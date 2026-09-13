# Process paths

## Goal

Four places decide where Ox keeps something under a user's home, and they do not
agree. Settings and the model cache require an absolute XDG value and fall back
to a conventional directory under `HOME`, refusing to guess when neither is
absolute. Sessions and memory accept any non-empty XDG value, so a relative one
silently anchors durable session storage to whatever directory Ox happened to
start in, and an empty `HOME` yields a relative path rather than no path.

The provider also reads the environment itself to find its cache, so a boundary
that should be handed a path resolves process configuration instead.

## Desired outcome

One XDG policy, applied once at startup. Lower boundaries receive concrete paths
and never read the environment.

## Summary of approach

Give `cmd/ox` one resolver: an XDG variable is used only when it is absolute,
otherwise the conventional directory under an absolute `HOME`, otherwise no
path, which each store already handles by falling back to a temporary directory.
Resolve the settings, session, memory, and model-cache paths there and pass them
down. The provider takes its cache path as configuration.

## Related code

- `cmd/ox/main.go` - startup, where the paths are already partly resolved.
- `internal/settings/settings.go` - `GlobalPath`.
- `internal/agent/store.go` and `memory.go` - `SessionPath`, `MemoryPath`.
- `internal/openrouter/cache.go` - `resolvedCachePath` and its environment read.

## Current state

- Relevant existing behavior: an empty store path already means "use a temporary
  directory", so returning no path is a supported answer.
- Existing patterns to follow: the agent already takes `SettingsPath`,
  `SessionDir`, and `MemoryDir` as configuration.
- Constraints from the current implementation: the provider client's cache path
  is currently a test-only field, which becomes the ordinary way to set it.

## Test plan

- **Key behaviors to verify:** an absolute XDG value wins; a relative one is
  ignored in favour of `HOME`; a relative `HOME` yields no path; each of the
  four paths lands in its conventional place.
- **Test levels:** unit, in `cmd/ox`.
- **Edge cases and failure modes:** both values empty.
- **What not to test:** the stores' temporary-directory fallback.

## Implementation plan

- Add the resolver and its test to `cmd/ox`.
- Resolve all four paths at startup and pass them down.
- Remove the per-package path helpers and the provider's environment read.

## Documentation updates

- Todo list item "Centralize process path resolution and XDG validation (F29)".

## Impact assessment

- Code paths affected: startup path resolution. A relative `XDG_DATA_HOME` no
  longer anchors session and memory storage to the working directory.
- Data, protocol, or schema impact: none.
- Dependency or API impact: `settings.GlobalPath`, `agent.SessionPath`, and
  `agent.MemoryPath` are removed; the provider gains a cache path field.

## Validation

- Tests to write and run: the resolver test, then the whole suite.
- Static checks: `make check-go`.
