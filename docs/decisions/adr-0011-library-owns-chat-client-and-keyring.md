# ADR-0011: Library Owns Chat Client and Keyring Creation

- **Status:** accepted
- **Date:** 2026-03-29
- **Amended:** 2026-03-30

## Context and Problem Statement

Ur's architecture declares that the CLI is a thin consumer of the Ur class library ([ADR-0002](adr-0002-library-first.md)). The library owns the provider registry, model definitions, settings, and session storage. Yet early designs required the host application to create provider-specific objects and pass them in.

**Original problem (2026-03-29):** The host created an `IChatClientFactory` with provider SDKs and endpoint URLs. This pushed provider knowledge into the wrong layer.

**Refined problem (2026-03-30):** After moving chat client creation into the library, the host still created `IKeyring` instances (platform detection, `MacOSKeyring`/`LinuxKeyring`). Keyring creation is platform-specific but not UI-specific — the library can detect the platform itself. Additionally, `CreateChatClient` was public, exposing an internal operation to frontends. The agent loop (not the frontend) should be the consumer of chat clients.

## Decision Drivers

- **Library-first architecture.** Frontends should be thin. Provider wiring and platform detection are not UI concerns.
- **Simplicity.** Single developer. Duplicating provider or keyring setup across frontends is unnecessary complexity.
- **Encapsulation.** The library owns provider data (registry, endpoints, API keys) and knows which platforms it supports. Client creation and keyring creation are internal operations, not public API.

## Considered Options

### Option 1: Host provides IChatClientFactory (original status quo)

**Description:** The host creates an `IChatClientFactory` with provider-specific SDKs and endpoints.

**Rejected:** Every frontend duplicates factory code and SDK packages. The "thin client" promise of ADR-0002 is broken. See original ADR for full analysis.

### Option 2: Library owns client creation, host provides IKeyring (intermediate state)

**Description:** Library owns provider SDKs and client creation. `UrHost.Start()` accepts a required `IKeyring`.

**Pros:**

- Frontends don't touch provider SDKs or endpoints.
- `IKeyring` is a clean platform boundary.

**Cons:**

- Every frontend must still do platform detection and keyring construction — identical code in every host.
- `CreateChatClient` as public API invites frontends to manage chat clients directly, which is the agent loop's job.

**When this is the right choice:** When different frontends genuinely need different keyring implementations. Not this project — macOS uses Keychain, Linux uses libsecret, and `RuntimeInformation.IsOSPlatform` works everywhere including AoT.

### Option 3: Library owns everything, IKeyring is optional override

**Description:** Library does platform detection and creates the default keyring internally. `UrHost.Start()` accepts an optional `IKeyring?` for testing. `CreateChatClient` is internal — only the agent loop uses it.

**Pros:**

- Frontend is maximally thin: `UrHost.Start(cwd)`.
- Platform detection lives in one place (the library), not duplicated per frontend.
- `CreateChatClient` is internal, so frontends can't misuse it. Chat client lifecycle is fully library-managed.
- Still testable: pass a mock `IKeyring` in tests.

**Cons:**

- Library takes a hard dependency on knowing which platforms exist. Acceptable — Ur already declares macOS and Linux as the only supported platforms.
- If a frontend needs a truly exotic keyring (not macOS or Linux), it must be added to the library. Unlikely given the platform constraints.

**When this is the right choice:** A library-first project with known platform targets and a single developer.

## Decision

We chose **Option 3**. The progression from Option 1 → 2 → 3 followed the same principle to its conclusion: if the library owns the domain (providers, models, secrets), it should own the operations on that domain (client creation, keyring creation). Leaving keyring creation in the host was an arbitrary stop short of full encapsulation.

`CreateChatClient` is internal because frontends have no business creating chat clients — that is the agent loop's responsibility. Exposing it publicly would invite frontends to bypass the loop and manage conversations directly.

## Consequences

### Positive

- CLI becomes a true thin client: `UrHost.Start(cwd)`, run REPL.
- Provider endpoints, SDKs, client creation, and keyring creation are all encapsulated behind `UrHost`.
- Future frontends (GUI, IDE) get everything for free with zero provider or platform wiring.
- `CreateChatClient` being internal prevents frontends from bypassing the agent loop.

### Negative

- Ur library gains provider SDK dependencies (`Microsoft.Extensions.AI.OpenAI`). Acceptable — necessary for Ur to function, works under AoT.
- Library owns platform detection. If a third platform is added, the library must be updated. Acceptable given the explicit non-goal of Windows support.

### Neutral

- `UrHost.Start` signature: `Start(string workspacePath, IKeyring? keyring = null)`. The optional parameter is for testing only.
- `CreateChatClient` is internal. The agent loop is the sole consumer.
- `IKeyring` implementations (`MacOSKeyring`, `LinuxKeyring`) remain public types for test scenarios, but frontends don't need to reference them.

## Confirmation

- The CLI's `Program.cs` should have zero references to provider SDKs, endpoint URLs, `IKeyring`, `MacOSKeyring`, `LinuxKeyring`, or `CreateChatClient`.
- Any new frontend can start Ur with `UrHost.Start(path)` and nothing else.
- Tests can inject a mock keyring via `UrHost.Start(path, mockKeyring)`.
