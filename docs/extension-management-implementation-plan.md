# Extension Management Implementation Plan

> Implements: [Extension Management](extension-management.md), [Extension System](extension-system.md), [Host & Session API](host-session-api.md), [TUI Chat Client](cli-tui.md), [ADR-0012](decisions/adr-0012-extension-enablement-state-and-lazy-activation.md)

## Goal

Implement extension listing and enable/disable management end to end without breaking the three-tier trust model. The key invariant is that disabled extensions are discoverable but not initialized, and workspace opt-ins remain per-user state outside the repo.

## Delivery Principles

- Ship from the library outward. The TUI should be the last major consumer, not the place where lifecycle rules are invented.
- Preserve a working system after every phase. No long-lived broken branch where startup, chat, or test fixtures stop working.
- Use tests as phase gates, not cleanup. Each phase must add the tests that prove its new invariant before the next phase starts.
- Prefer narrow vertical slices over giant rewrites. Internal refactors first, public API second, UI last.

## Out of Scope For This Plan

- Extension installation, update, or removal from a registry
- Per-workspace suppression of user/system extensions
- Showing malformed-manifest extensions in the TUI
- Mid-turn extension mutation semantics beyond "not allowed in v1"

## Phase 1: Internal Model Split

### Objective

Separate manifest discovery from live runtime activation inside the library without changing user-visible behavior yet.

### Scope

- Refactor the internal extension types so metadata and runtime state are not the same thing.
- Introduce stable extension identity (`<tier>:<name>`) as an internal concept first.
- Keep current startup behavior temporarily so the refactor is low-risk and testable in isolation.

### Likely Code Areas

- `Ur/Extensions/Extension.cs`
- `Ur/Extensions/ExtensionLoader.cs`
- New internal types under `Ur/Extensions/` for descriptor/runtime state
- Existing extension test helpers in `Ur.Tests/ExtensionSystemTests.cs`

### Work Items

1. Split manifest-derived fields from activation-derived fields.
2. Make activation/deactivation explicit operations over a discovered extension descriptor.
3. Add an internal status model that can later support `DesiredEnabled`, `IsActive`, and `LoadError`.
4. Keep the current public `UrHost.Extensions` behavior stable during this phase to avoid mixing refactor with feature change.

### Tests To Add Or Update

- Extend `Ur.Tests/ExtensionSystemTests.cs` with coverage for:
  - extension identity serialization/parsing
  - discovery order remaining stable across tiers
  - activation still registering tools for trusted extensions
  - deactivation unregistering tools cleanly

### Quality Gate

- `dotnet test Ur.Tests/Ur.Tests.csproj`
- Existing extension tests remain green with no user-visible behavior change.
- No public API changes yet.

## Phase 2: Override Store And Startup Semantics

### Objective

Make startup compute effective extension state from defaults plus persisted overrides, and stop initializing disabled extensions.

### Scope

- Implement the dedicated override store described in [extension-management.md](extension-management.md).
- Read global overrides from `~/.ur/extensions-state.json`.
- Read per-workspace overrides from `~/.ur/workspaces/<workspace-hash>/extensions-state.json`.
- Change host startup so only effective-enabled extensions are initialized.

### Likely Code Areas

- New `Ur/Extensions/ExtensionOverrideStore.cs` or equivalent
- `Ur/UrHost.cs`
- `Ur/Workspace.cs` only if a helper for workspace hashing/path identity is useful
- `Ur.Tests/ExtensionSystemTests.cs`
- `Ur.Tests/HostSessionApiTests.cs`

### Work Items

1. Define the override file shape and path strategy.
2. Implement load/merge/write/clear operations for override state.
3. Compute `DesiredEnabled` using:
   - system/user default: enabled
   - workspace default: disabled
   - persisted override if present
4. Apply that desired state during startup before any `main.lua` execution.
5. Keep malformed override files non-fatal: warn and fall back to defaults.

### Tests To Add Or Update

- New library tests for:
  - global override file disables a user extension on startup
  - workspace override file enables a workspace extension on startup
  - override files persist deltas only, not redundant defaults
  - malformed override file falls back to defaults without crashing host startup
  - disabled workspace extension does not execute `main.lua` during startup

### Quality Gate

- `dotnet test Ur.Tests/Ur.Tests.csproj --filter "FullyQualifiedName~ExtensionSystemTests|FullyQualifiedName~HostSessionApiTests"`
- The ADR-0012 security invariant is proven by tests: disabled workspace extensions are discovered but not initialized.
- Startup remains non-interactive and chat readiness behavior is unchanged.

## Phase 3: Public Library Management API

### Objective

Expose a real workspace-scoped extension management surface for frontends.

### Scope

- Add `ExtensionCatalog` and `ExtensionInfo`.
- Move callers away from raw runtime objects as the primary management interface.
- Support list, enable/disable, and reset-to-default operations.

### Likely Code Areas

- `Ur/UrHost.cs`
- New public types under `Ur/`
- Internal extension-management implementation under `Ur/Extensions/`
- `Ur.Tests/HostSessionApiTests.cs`

### Work Items

1. Add `UrHost.Extensions` as a catalog/service rather than a raw list.
2. Define `ExtensionInfo` fields:
   - `Id`
   - `Name`
   - `Tier`
   - `Description`
   - `Version`
   - `DefaultEnabled`
   - `DesiredEnabled`
   - `IsActive`
   - `HasOverride`
   - `LoadError`
3. Implement:
   - `List()`
   - `SetEnabledAsync(extensionId, enabled, ct)`
   - `ResetAsync(extensionId, ct)`
4. Decide and implement failure semantics:
   - storage failure is an operation failure
   - activation failure returns updated info with `LoadError`
5. Ensure enable/disable updates the live tool registry immediately.

### Tests To Add Or Update

- Add focused API tests for:
  - listing includes disabled and active extensions in stable order
  - enabling a workspace extension writes per-workspace override state and activates its tool
  - disabling a user extension writes global override state and removes its tool
  - resetting clears the override and restores tier default behavior
  - activation failure surfaces `LoadError` without crashing the host
  - unknown extension ID fails cleanly

### Quality Gate

- `dotnet test Ur.Tests/Ur.Tests.csproj --filter "FullyQualifiedName~HostSessionApiTests|FullyQualifiedName~ExtensionSystemTests"`
- A headless test can fully manage extensions without any TUI code.
- Raw runtime internals are no longer required by frontends to inspect or toggle extension state.

## Phase 4: TUI Backend Contract And Modal Logic

### Objective

Prepare the TUI to consume the new library API in a testable way before adding user-visible keyboard flows.

### Scope

- Extend the TUI backend seam to include extension listing and mutation.
- Add the modal state model and filtering/sorting behavior.
- Keep rendering and slash-command wiring for the next phase.

### Likely Code Areas

- `Ur.Tui/IChatBackend.cs`
- `Ur.Tui/ChatBackend.cs`
- `Ur.Tui.Tests/TestChatBackend.cs`
- New modal/view-model code under `Ur.Tui/Components/`
- New tests in `Ur.Tui.Tests/`

### Work Items

1. Extend the backend seam with extension-management operations.
2. Add fake backend support for:
   - extension list snapshots
   - enable/disable/reset behavior
   - injected activation failures
3. Implement modal-local behaviors:
   - stable tier/status sorting
   - text filtering
   - selection movement
   - disabled-toggling guard while a turn is active

### Tests To Add Or Update

- New `Ur.Tui.Tests/ExtensionManagerModalTests.cs` covering:
  - filtering by name
  - tier/status rendering metadata
  - selection movement
  - reset action availability
  - read-only/blocking behavior during active turns
- Update `TestChatBackend.cs` to simulate extension operations and failure modes.

### Quality Gate

- `dotnet test Ur.Tui.Tests/Ur.Tui.Tests.csproj --filter "FullyQualifiedName~ExtensionManagerModalTests"`
- The TUI can model extension-management state entirely against a fake backend.
- No keyboard wiring or slash command changes yet.

## Phase 5: TUI Command And Interaction Flow

### Objective

Expose extension management to users through the TUI.

### Scope

- Add `/extensions`.
- Render the modal.
- Support toggle and reset flows.
- Add an explicit confirmation step before enabling a workspace extension.

### Likely Code Areas

- `Ur.Tui/ChatApp.cs`
- New `Ur.Tui/Components/ExtensionManagerModal.cs`
- `Ur.Tui.Tests/ChatAppTests.cs`
- `Ur.Tui.Tests/ExtensionManagerModalTests.cs`

### Work Items

1. Add `/extensions` to the slash command table.
2. Open and close the modal cleanly from chat mode.
3. Wire toggle/reset actions through the backend seam.
4. Show success and failure feedback in a user-visible way.
5. Require an explicit confirmation step before enabling a workspace extension.
6. Block toggles while a turn is running; listing may remain available read-only if desired, but mutation is not allowed in v1.

### Tests To Add Or Update

- Extend `Ur.Tui.Tests/ChatAppTests.cs` with:
  - `/extensions` opens the modal
  - Esc dismisses the modal
  - toggling a user/system extension updates visible state
  - enabling a workspace extension requires confirmation
  - activation failure surfaces an error message or error state
  - toggling while a turn is running is blocked
- Keep `/model`, `/quit`, and chat flow tests green to catch regressions in slash routing.

### Quality Gate

- `dotnet test Ur.Tui.Tests/Ur.Tui.Tests.csproj`
- Keyboard-driven extension management works end to end against the fake backend.
- Existing chat, first-run, and model-picker behavior still passes unchanged.

## Phase 6: End-To-End Hardening

### Objective

Prove the feature across real library + TUI seams and finish the feature as a shippable slice.

### Scope

- Run the full unit/integration suite.
- Add one or two real-world smoke scenarios with temp workspaces and sample extensions.
- Update docs from "planned" to "implemented" where needed.

### Likely Code Areas

- `Ur.Tests/`
- `Ur.Tui.Tests/`
- Possibly `Ur.IntegrationTests/` if an end-to-end host scenario belongs there
- Architecture docs already touched in this design pass

### Work Items

1. Add at least one real host-level smoke test covering:
   - workspace extension disabled by default
   - enable via API
   - tool available immediately
   - restart host
   - extension still enabled for that workspace/user
2. Add a complementary disable/reset smoke test.
3. Decide whether a lightweight TUI integration test is worth adding now or whether unit/UI tests already cover enough.
4. Reconcile docs with actual type names and file paths after implementation settles.

### Tests To Add Or Update

- Prefer a new smoke-style library test if `Ur.IntegrationTests` is too provider-specific today.
- Run the full local gate:
  - `dotnet test Ur.Tests/Ur.Tests.csproj`
  - `dotnet test Ur.Tui.Tests/Ur.Tui.Tests.csproj`
  - `dotnet test Ur.IntegrationTests/Ur.IntegrationTests.csproj` only if the environment is already configured and the suite is expected to pass in CI

### Quality Gate

- The full intended test matrix passes.
- Manual smoke check confirms:
  - `/extensions` lists tiers clearly
  - enabling a workspace extension persists across restart
  - disabling removes the tool and keeps it removed after restart
  - no disabled workspace extension executes during startup

## Recommended Stop Points

- After Phase 2: the security model is corrected even before the TUI ships.
- After Phase 3: the library API is usable by non-TUI frontends and scriptable tests.
- After Phase 5: the feature is user-visible and functionally complete.
- After Phase 6: the feature is ready to ship with confidence.

## Suggested Test Commands By Phase

- Library-focused phases:
  - `dotnet test Ur.Tests/Ur.Tests.csproj`
- TUI-focused phases:
  - `dotnet test Ur.Tui.Tests/Ur.Tui.Tests.csproj`
- Final regression:
  - `dotnet test Ur.Tests/Ur.Tests.csproj`
  - `dotnet test Ur.Tui.Tests/Ur.Tui.Tests.csproj`
  - `dotnet test Ur.IntegrationTests/Ur.IntegrationTests.csproj`
