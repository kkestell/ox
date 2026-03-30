# ADR-0011: Library Owns Chat Client Creation

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur's architecture declares that the CLI is a thin consumer of the Ur class library ([ADR-0002](adr-0002-library-first.md)). The library owns the provider registry, model definitions, settings, and keyring. Yet the current design requires the host application (CLI, GUI, etc.) to create an `IChatClientFactory` implementation with provider SDK packages and endpoint URLs, then pass it into `UrHost.Start()`.

This means the CLI must know about provider endpoints, carry provider-specific SDK dependencies (`Microsoft.Extensions.AI.OpenAI`, `Google_GenerativeAI.Microsoft`), and construct the factory — all of which are provider concerns, not UI concerns. Every future frontend would duplicate this wiring. The library has all the data it needs (registry, keyring, endpoints) to create chat clients itself.

## Decision Drivers

- **Library-first architecture.** Frontends should be thin. Provider wiring is not a UI concern.
- **Simplicity.** Single developer. Duplicating provider setup across frontends is unnecessary complexity.
- **Encapsulation.** The library owns provider data (registry, endpoints, API keys). Client creation is the natural next step — splitting it across the library boundary leaks internals.

## Considered Options

### Option 1: Keep IChatClientFactory as host-provided (status quo)

**Description:** The host application creates an `IChatClientFactory` with provider-specific SDKs and endpoint configuration, passes it to `UrHost.Start()`.

**Pros:**
- Ur library has no dependency on provider SDKs (only `Microsoft.Extensions.AI` abstractions).
- Frontends could theoretically use different provider implementations.

**Cons:**
- Every frontend duplicates the same factory code and carries the same SDK packages.
- The CLI must know about provider endpoints — data that belongs in the registry.
- Breaks the "thin client" promise of ADR-0002.
- The "different implementations" scenario is hypothetical. The OpenAI SDK covers all OpenAI-compatible APIs. There is no realistic case where a CLI and a GUI need different client factories.

**When this is the right choice:** A multi-team project where different frontends genuinely need different provider implementations. Not this project.

### Option 2: Library owns client creation, accepts IKeyring

**Description:** Move provider SDK packages and client creation logic into the Ur library. Add API endpoint URLs to `ProviderDefinition`. `UrHost.Start()` accepts an `IKeyring` (platform-specific) instead of `IChatClientFactory`. `UrHost` resolves API keys from the keyring and creates `IChatClient` instances internally.

**Pros:**
- Frontends become truly thin — `UrHost.Start(cwd, keyring)` and go.
- Provider data (endpoints, SDKs, client creation) is fully encapsulated in the library.
- `IKeyring` is the right injection point — it's platform-specific (Linux libsecret, macOS Keychain) and the only thing the host needs to provide.
- Testable: inject a mock `IKeyring` for tests.

**Cons:**
- Ur library now depends on `Microsoft.Extensions.AI.OpenAI` and `Google_GenerativeAI.Microsoft`.
- Adding a new provider SDK means updating the library, not just the host.

**When this is the right choice:** A library-first project where provider wiring is a core responsibility, not a host responsibility.

## Decision

We chose **Option 2** because provider client creation is a provider concern, and the library owns providers. The `IChatClientFactory` abstraction pushed provider knowledge into the wrong layer. `IKeyring` is the correct boundary — it represents a platform capability (secure secret storage), not domain logic.

## Consequences

### Positive

- CLI becomes a true thin client: start host, run REPL.
- Provider endpoints, SDKs, and client creation are encapsulated behind `UrHost`.
- Future frontends (GUI, IDE) get client creation for free.

### Negative

- Ur library gains two package dependencies (`Microsoft.Extensions.AI.OpenAI`, `Google_GenerativeAI.Microsoft`). Acceptable — these are necessary for Ur to function, and they work under AoT.

### Neutral

- `IChatClientFactory` is removed from the public API. An internal version may exist if useful for organizing client creation code.
- `ProviderDefinition` gains an `Endpoint` field (API base URL).
- `UrHost.CreateChatClient` no longer requires the caller to supply an API key.

## Confirmation

- The CLI's `Program.cs` should have zero references to provider SDKs, endpoint URLs, or `IChatClientFactory`.
- Any new frontend can start Ur with `UrHost.Start(path, keyring)` and create clients without additional provider setup.
