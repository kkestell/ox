# Move UrHost into Ur.Hosting to break 6-namespace cycle

## Goal

Eliminate the circular dependency cycle among Ur.Hosting, Ur.Skills, Ur.Sessions, Ur, Ur.Tools, and Ur.AgentLoop by relocating `UrHost` from the root `Ur` namespace into `Ur.Hosting`.

## Desired outcome

After this work:

- The root `Ur` namespace contains only foundation types (`Workspace`, `ToolContext`) with no upward dependencies.
- `Ur.Hosting` is the clear composition root: it holds `UrHost`, `UrStartupOptions`, and `ServiceCollectionExtensions`, and depends on everything below it but nothing depends back on it.
- The namespace dependency graph is a DAG with no cycles.
- All existing tests pass.

## How we got here

The previous plan (2026-04-11-001) broke four specific dependency issues: the Ur <-> Ur.Hosting cycle via `DefaultUserDataDirectory`, the ISubagentRunner placement, Providers <-> Configuration, and UrSession <-> UrHost intimacy. A subsequent static analysis (NOSE101) revealed a broader 6-namespace cycle that persists because `UrHost` still sits in the root `Ur` namespace while depending on `Ur.Hosting.UrStartupOptions`, `Ur.Sessions`, `Ur.Skills`, and `Ur.Tools` -- and lower namespaces (Sessions, AgentLoop) depend back on `Ur` for `Workspace`.

Moving UrHost into `Ur.Hosting` turns root `Ur` into a dependency-free foundation layer, which breaks every upward edge in the cycle in a single move.

## Approaches considered

### Option A -- Move UrHost to Ur.Hosting

- Summary: Change `UrHost.cs` from `namespace Ur;` to `namespace Ur.Hosting;`. Move the file physically into `src/Ur/Hosting/`. Update the two Ox consumer files to use `Ur.Hosting.UrHost`.
- Pros: Single file move breaks all 6 cycle edges. Root Ur becomes a pure foundation layer. No new namespaces. Hosting is already the composition root -- UrHost belongs there conceptually.
- Cons: External callers change from `using Ur;` to `using Ur.Hosting;` (only 2 files in Ox).
- Failure modes: If future types are added to root `Ur` with upward dependencies, cycles could return. Mitigated by the clear convention: root `Ur` is for foundation types only.

### Option B -- Move Workspace out of root Ur

- Summary: Move `Workspace` to a new `Ur.Core` or `Ur.Infrastructure` namespace so Sessions and AgentLoop don't depend on the root namespace.
- Pros: Breaks the Sessions -> Ur and AgentLoop -> Ur edges.
- Cons: Does not break the Ur -> Hosting, Ur -> Sessions, Ur -> Tools, or Ur -> Skills edges -- UrHost still depends on all of them. Requires a new namespace for a single type. Results in `Ur.Core.Workspace` verbosity. Needs additional moves to fully resolve the cycle.
- Failure modes: Solving only half the cycle invites more complex workarounds for the remaining edges.

## Recommended approach

Option A -- Move UrHost to Ur.Hosting.

- Why this approach: One namespace change eliminates every edge in the cycle. After the move, root `Ur` contains only `Workspace` (no dependencies) and `ToolContext` (depends only on `Ur.Todo`). Every other namespace depends downward on these foundation types. Hosting becomes the unambiguous top of the dependency tree.
- Key tradeoffs: Two Ox files must update their `using` directives. Tests resolve `UrHost` through the DI container via `Ur.Hosting` already, so no test changes are expected.

## Related code

- `src/Ur/UrHost.cs` -- The type being relocated. Currently `namespace Ur;`, depends on Hosting, Sessions, Skills, Tools, Configuration, Providers, Settings.
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` -- Already creates UrHost and registers it in DI. UrHost moves to the same namespace.
- `src/Ur/Hosting/UrStartupOptions.cs` -- UrHost constructor parameter. Currently referenced as `Hosting.UrStartupOptions`; after move, just `UrStartupOptions`.
- `src/Ur/Workspace.cs` -- Stays in root `Ur`. Becomes the primary resident of the foundation layer.
- `src/Ur/ToolContext.cs` -- Stays in root `Ur`. References `Ur.Todo` only.
- `src/Ox/OxApp.cs` -- Uses `UrHost` with `using Ur;`. Needs `using Ur.Hosting;`.
- `src/Ox/Program.cs` -- Resolves `UrHost` from DI with `using Ur;`. Needs `using Ur.Hosting;`.
- `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` -- Already has `using Ur.Hosting;`. Resolves `UrHost` from DI. Should compile without changes.

## Current state

- UrHost is a `public sealed` class in `namespace Ur;` with an `internal` constructor.
- UrHost depends on: `Ur.Configuration`, `Ur.Permissions`, `Ur.Settings`, `Ur.Providers`, `Ur.Sessions`, `Ur.Skills`, `Ur.Todo`, `Ur.Tools`, and `Ur.Hosting` (for `UrStartupOptions`).
- Root `Ur` namespace contains only 3 files: `UrHost.cs`, `Workspace.cs`, `ToolContext.cs`.
- TestHostBuilder already uses `using Ur.Hosting;` and resolves UrHost via `host.Services.GetRequiredService<UrHost>()`. The type name `UrHost` is found from `Ur.Tests.TestSupport` via C# parent-namespace resolution through `Ur`.
- Ox consumers (`OxApp.cs`, `Program.cs`) import `using Ur;` and reference `UrHost` directly.

## Structural considerations

**Hierarchy**: The root `Ur` namespace currently violates hierarchy by depending upward on `Ur.Hosting`, `Ur.Sessions`, `Ur.Skills`, and `Ur.Tools`. After the move, root `Ur` is the bottom layer with zero upward dependencies, and `Ur.Hosting` is the top layer (composition root) -- clean hierarchy.

**Modularization**: UrHost is the composition root's primary artifact. It belongs with the other composition root types (ServiceCollectionExtensions, UrStartupOptions) in Hosting.

**Encapsulation**: UrHost's constructor is `internal`, so moving it within the same assembly preserves visibility. The `public` class visibility is unchanged.

## Implementation plan

- [x] Move `src/Ur/UrHost.cs` to `src/Ur/Hosting/UrHost.cs` (physical file relocation).
- [x] Change the namespace declaration from `namespace Ur;` to `namespace Ur.Hosting;`.
- [x] Remove the `Hosting.` prefix from `UrStartupOptions` in the UrHost constructor signature (line 77). It's now in the same namespace.
- [x] Remove `using` directives in UrHost.cs that are now unnecessary (e.g., if any referenced `Ur.Hosting` explicitly). Add `using Ur;` since UrHost still needs `Workspace`.
- [x] In `src/Ox/OxApp.cs`: add `using Ur.Hosting;`. Remove `using Ur;` if no other root-namespace types are referenced.
- [x] In `src/Ox/Program.cs`: add `using Ur.Hosting;`. Remove `using Ur;` if no other root-namespace types are referenced.
- [x] In `ServiceCollectionExtensions.cs`: remove the `Ur` using if it was only needed for `UrHost` (check -- it may still need it for `Workspace`). The UrHost type is now in-namespace.
- [x] Run `dotnet build` from the repo root -- verify green.
- [x] Run `dotnet test` from the repo root -- verify green.
- [x] Verify the cycle is broken: `grep -rn "^namespace Ur;" src/Ur/` should show only `Workspace.cs` and `ToolContext.cs`.

## Validation

- **Build**: `dotnet build` from repo root must succeed with no new warnings.
- **Tests**: `dotnet test tests/Ur.Tests/` and `dotnet test tests/Ur.IntegrationTests/` must pass.
- **Namespace audit**: After the move, confirm root `Ur` has no upward dependencies:
  - `grep -rn "^using Ur\." src/Ur/Workspace.cs` -- should show no matches.
  - `grep -rn "^using Ur\." src/Ur/ToolContext.cs` -- should show only `using Ur.Todo;`.
- **Cycle check**: Re-run the NOSE101 inspection. The 6-namespace cycle should be gone.
