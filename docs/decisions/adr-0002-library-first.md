# ADR-0002: Library-First Architecture

- **Status:** accepted
- **Date:** 2026-03-28

## Context and Problem Statement

Ur needs a UI — initially a CLI/REPL, but with stated intent to support GUI and IDE frontends in the future. The question is whether the CLI is the application (with the core embedded) or whether the core is a library that the CLI consumes as one of several possible frontends.

Relevant docs: [Ur Architecture](../index.md)

## Decision Drivers

- Future frontend support — GUI, IDE extensions, and potentially headless/API modes
- Simplicity — avoid premature abstraction, but also avoid a rewrite when a second frontend arrives
- Testability — the core should be testable without terminal I/O
- Single developer — cannot afford to maintain two divergent codebases

## Considered Options

### Option 1: CLI-First (monolithic)

**Description:** The CLI application is the project. Core logic lives in the same project as the terminal UI.

**Pros:**

- Simpler to start — one project, no abstraction boundaries to design
- No risk of over-engineering the library API

**Cons:**

- Adding a second frontend later requires extracting the core — effectively a rewrite
- Terminal I/O leaks into core logic (e.g. permission prompts via stdin/stdout)
- Harder to test core logic in isolation

**When this is the right choice:** When there will genuinely only ever be one frontend, or when the project is a prototype with no longevity expectations.

### Option 2: Library-First

**Description:** Ur is a .NET class library. The CLI is a separate, thin project that depends on it. The library defines interfaces/callbacks for UI interactions (streaming output, permission prompts, provider management).

**Pros:**

- Clean separation from day one — no extraction needed when a second frontend arrives
- Core logic is testable without terminal I/O
- Forces explicit design of the UI contract (events, callbacks)
- Multiple frontends share the same core, reducing divergence

**Cons:**

- Must design the UI contract upfront — risk of getting it wrong before real use cases validate it
- Slightly more project structure overhead (two projects in the solution)

**When this is the right choice:** When multiple frontends are planned and the core logic is substantial enough to justify the separation.

## Decision

We chose **library-first** because the intent to support CLI, GUI, and IDE frontends is a stated goal, not a hypothetical. Designing the separation now — while the codebase is tiny — is cheaper than extracting it later when terminal assumptions have permeated the core. The overhead is minimal (one extra project in the solution), and the forcing function of having to define the UI contract as events/callbacks leads to a better architecture.

## Consequences

### Positive

- The agent loop, extension system, permission system, and configuration are all UI-agnostic from the start
- Permission prompts, streaming output, and provider management are defined as contracts that any frontend can implement
- Core is unit-testable with mock UI layers

### Negative

- The UI contract must be designed before the first frontend is fully built — some of it will be speculative
- Two projects to maintain instead of one (but they are different sizes: the library is large, the CLI is thin)

### Neutral

- The CLI project becomes a thin adapter: wire up stdin/stdout/stderr to the library's event model, implement the permission prompt callback, run the REPL loop

## Confirmation

- The CLI project stays thin (< 500 lines for a long time)
- When a second frontend is attempted, it can consume the library without modification to the library itself
- Permission prompts, streaming, and provider management all work through the defined contracts without terminal assumptions leaking through
