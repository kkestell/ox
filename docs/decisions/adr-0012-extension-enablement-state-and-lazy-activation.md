# ADR-0012: Extension Enablement Uses Dedicated State and Lazy Activation

- **Status:** accepted
- **Date:** 2026-03-31
- **Decision makers:** Ur architecture
- **Consulted:** Current extension-system and TUI design

## Context and Problem Statement

Ur's trust model depends on workspace extensions being disabled by default, but the current architecture had no durable enable/disable model for frontends to use. At the same time, the implementation had started executing `main.lua` for all discovered extensions during startup, even when a workspace extension was considered disabled. That is survivable while the sandbox only exposes registration APIs, but it is the wrong architectural shape once extensions gain richer capabilities.

We need a design that lets the TUI and future frontends list extensions and toggle them on or off without breaking the security guarantee behind three-tier loading. Relevant docs: [Extension System](../extension-system.md), [Extension Management](../extension-management.md), [TUI Chat Client](../cli-tui.md).

## Decision Drivers

- Security — "disabled" must mean code has not been executed
- Usability — enable/disable decisions must survive restart and be visible in the UI
- Simplicity — the persistence rules should be easy to explain
- Library-first design — UIs should consume a public management API, not internal runtime objects

## Considered Options

### Option 1: Store enablement in settings files and initialize all discovered extensions

**Description:** Persist extension enabled state in the existing user/workspace settings files and keep eagerly running `main.lua` for every discovered extension.

**Pros:**
- Reuses existing file and mutation infrastructure
- Minimal new architecture on paper

**Cons:**
- Workspace settings are repo-scoped, so a project could try to opt users into workspace extensions
- A disabled extension would still execute code during startup
- Blurs configuration data with trust/lifecycle state

**When this is the right choice:** When extensions are fully trusted and "disabled" only means "hide capabilities from the UI."

### Option 2: Dedicated state store but initialize all discovered extensions

**Description:** Move enablement out of settings, but keep eager runtime initialization during startup.

**Pros:**
- Preserves the trust boundary around persistence
- Smaller implementation change than full lazy activation

**Cons:**
- Still violates the semantic meaning of "disabled"
- Keeps future capability expansion risky because disabled extensions already ran code

**When this is the right choice:** When startup-time registration side effects are harmless and disabling is only a registry toggle.

### Option 3: Dedicated state store and initialize only effective-enabled extensions

**Description:** Persist overrides outside repo settings, compute desired state from tier defaults plus overrides, and initialize only extensions whose effective state is enabled.

**Pros:**
- Preserves the security guarantee behind workspace-default-disabled
- Gives UIs a truthful model: discovered vs enabled vs faulted
- Cleanly separates configuration from lifecycle state

**Cons:**
- Requires a new management component and persistence path
- Enabling an extension becomes an on-demand activation path rather than a trivial flag flip

**When this is the right choice:** When trust defaults matter and frontends need a first-class extension management experience.

## Decision

We chose **Option 3** because it is the only option that keeps the trust model intact while also giving the TUI a durable management surface. Workspace opt-in is a per-user trust decision, so it cannot live in repo-scoped settings, and a disabled extension cannot honestly be called disabled if its code already ran during startup.

## Consequences

### Positive

- Workspace extension opt-ins persist per user without being shareable through the repo
- Frontends can list all discovered extensions and show accurate status
- Future extension APIs can expand without reopening the "disabled code still executed" hole

### Negative

- More moving parts: management API, override store, activation/deactivation flow
- Toggle operations now need error handling for runtime activation failures

### Neutral

- The existing three-tier defaults stay the same: system/user enabled, workspace disabled
- CLI commands and TUI modals can both sit on top of the same public management API

## Confirmation

- Users can enable a workspace extension once and see it stay enabled only for them in that workspace
- A disabled workspace extension never executes `main.lua` during startup
- The TUI can list extensions and surface activation failures without inspecting internal runtime objects
