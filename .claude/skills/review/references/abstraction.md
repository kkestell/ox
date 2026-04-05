# Abstraction

## The Zen

An abstraction captures the essential nature of a thing while suppressing
irrelevant detail. A good abstraction lets you think about *what* something does
without knowing *how* it does it. When abstractions are right, the code reads
like a story told at the appropriate level of detail for the audience.

The key judgment call is *where to draw the line*. Too little abstraction and
every caller must understand internals. Too much and the abstraction becomes a
puzzle box that hides things the caller genuinely needs to know. The best
abstractions feel obvious in hindsight—they map naturally to the way people
already think about the problem.

## What Good Abstraction Looks Like

- **Names reveal intent, not mechanism.** `TransferFunds` instead of
  `UpdateTwoRowsInAccountsTable`. The name tells you what the operation means
  in the domain, not how it is implemented.
- **Interfaces are small and purposeful.** Each method on a public interface
  earns its place. There are no "just in case" methods that no external caller
  currently uses.
- **Return types and parameters live at the right level.** A business function
  returns a domain result, not an HTTP status code or a database cursor.
- **Mixed abstraction levels are absent within a single function.** A function
  should not orchestrate high-level workflow steps and also bit-shift a byte
  buffer in the same body.
- **Callers do not need to know the implementation to use it correctly.**
  If you must read the source of a function to know whether to call it, the
  abstraction is broken.

## What to Look For in a Review

### Leaky abstractions

- A function that returns raw SQL rows, ORM objects, or HTTP responses when the
  caller only needs domain data.
- Error types that expose transport or storage details (e.g., `PgError` leaking
  out of a repository interface).
- Parameters that force callers to know implementation details—passing a
  database connection into a business function, for instance.

### Wrong level of abstraction

- A high-level orchestration method that drops into low-level byte manipulation
  or string parsing mid-flow.
- A utility function that encodes domain-specific business logic.
- Test helpers that couple tests to implementation details rather than
  observable behavior.

### Missing abstraction

- The same multi-step procedure duplicated in several places, begging for a
  named operation.
- Raw primitive types used where a domain type would add clarity and prevent
  misuse (e.g., passing bare `string` for email, currency, or ID where a
  dedicated type would catch misuse at compile time).
- Magic numbers or string literals scattered through logic rather than named
  constants or configuration.

### Over-abstraction

- An interface with a single implementation and no realistic prospect of a
  second—abstraction without purpose.
- Layers of indirection where a function calls a function that calls a function,
  each adding no logic, only forwarding.
- "Framework-itis": wrapping a simple operation in a generic, configurable,
  extensible harness nobody asked for.

## Anti-Patterns

| Anti-Pattern | What Happens | Why It Hurts |
|---|---|---|
| **Leaky abstraction** | Internal details bleed through the interface | Callers couple to implementation; changes ripple everywhere |
| **Premature abstraction** | Interface extracted before the second use case exists | Guesses wrong about the axis of variation; refactoring tax |
| **Primitive obsession** | Domain concepts represented as raw strings/ints | No type safety; easy to confuse "user ID" with "order ID" |
| **Abstraction inversion** | Low-level code reimplements what a higher-level facility already provides | Duplication, bugs, and confusion about which version is canonical |
| **Mixed levels** | One function mixes high-level orchestration with low-level detail | Hard to read; changes at one level risk breaking the other |
| **Speculative generality** | Interfaces, factories, and config for one concrete case | Complexity without payoff; harder to understand than the direct version |
| **Header interface** | Interface mirrors every method of its sole concrete class | Illusion of abstraction; changes always touch both |

## Severity Guide

- **Critical:** An abstraction boundary that is so leaky it provides no real
  encapsulation—callers must understand internals to use it correctly, yet the
  boundary creates indirection that makes debugging harder.
- **High:** Mixed abstraction levels in a function that is called widely or is
  on a critical path; primitive obsession in a domain where type confusion can
  cause data corruption.
- **Medium:** A single-implementation interface that adds indirection without
  value; a function returning raw infrastructure types to a small number of
  internal callers.
- **Low:** A slightly imprecise name; minor mixing of adjacent abstraction
  levels in a small, self-contained helper.
