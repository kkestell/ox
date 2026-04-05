# Modularization

## The Zen

A module is a unit of code that can be understood, changed, tested, and reused
independently. Good modularization lets different people (or the same person at
different times) work on different parts of a system without stepping on each
other—and without needing to hold the whole system in their head.

The dual goals are **high cohesion** (everything inside a module belongs
together) and **low coupling** (modules interact through narrow, stable
interfaces). When both hold, you can change the internals of one module without
cascading changes through others. When either breaks, the module boundaries
become fiction—the system behaves as one big tangled piece regardless of how
many files it is split across.

## What Good Modularization Looks Like

- **Each module has a single, stateable purpose.** If you cannot describe what a
  module does in one sentence without "and", it likely does too much.
- **Public surfaces are narrow.** A module exports only what others need.
  Internal helpers, types, and constants stay private.
- **Dependencies are explicit and minimal.** Imports make the dependency graph
  visible. A module depends on what it uses, nothing more.
- **Changes are local.** A bug fix or feature addition to module A does not
  require touching modules B, C, and D.
- **The module is testable in isolation.** You can write meaningful tests for it
  without spinning up the entire application.

## What to Look For in a Review

### Low cohesion

- A module that groups code by technical layer (all controllers, all models)
  rather than by domain capability. Symptom: changing one feature touches many
  modules.
- A file with multiple unrelated classes or functions that happen to have been
  created around the same time.
- A "utils" or "helpers" module that grows unboundedly because there is no
  better home for miscellaneous code.

### High coupling

- A change to one module's internals forces signature or logic changes in
  several other modules.
- Modules that share mutable state—global variables, singletons with writable
  fields, or shared caches without clear ownership.
- Extensive use of another module's internal types, constants, or unexported
  details (via reflection, build tags, or naming conventions that bypass access
  control).

### Wrong module boundaries

- Two modules that are always changed together—they are really one module split
  across two directories.
- A module that re-exports most of another module's API, acting as a thin
  pass-through with no added value.
- Circular dependencies between modules, indicating the boundary was drawn in
  the wrong place.

### Missing module boundaries

- A single file or package that has grown past ~500 LOC with identifiable
  sub-responsibilities that could stand alone.
- Copy-pasted logic between features that could be extracted into a shared
  module with a clear contract.
- A "core" or "common" module that half the codebase imports—often a sign that
  real module boundaries are missing and shared code was dumped in one place.

### Over-modularization

- Dozens of tiny modules (one function each) connected by an intricate web of
  imports, making the dependency graph harder to follow than a single file would
  be.
- Splitting that was done for aesthetic reasons ("files should be short") rather
  than for cohesion or independence.
- Microservices that share a database or call each other synchronously for every
  operation—modules in name only.

## Anti-Patterns

| Anti-Pattern | What Happens | Why It Hurts |
|---|---|---|
| **God module** | One module owns most of the logic | Cannot change, test, or understand any part independently |
| **Shotgun surgery** | One logical change scatters across many modules | High cost per change; easy to miss a site |
| **Feature envy** | A module's methods mostly operate on another module's data | Wrong boundary; the logic belongs where the data lives |
| **Inappropriate intimacy** | Two modules access each other's internals | Coupled in fact, decoupled only in fiction |
| **Utility dumping ground** | Unrelated helpers accumulate in `utils/` | Unclear ownership; everything depends on it; nobody maintains it |
| **Circular dependency** | A → B → A | Cannot build, test, or deploy either independently |
| **Nano-modules** | Every function is its own module | Dependency graph explodes; cognitive overhead exceeds the benefit |

## Severity Guide

- **Critical:** Circular module dependencies; a God module whose size makes the
  system effectively unmodularized.
- **High:** Shotgun surgery pattern across core features; shared mutable state
  between modules with no synchronization or ownership contract.
- **Medium:** A growing utils dump; feature envy where logic clearly belongs
  elsewhere; a module with low cohesion that groups unrelated concerns.
- **Low:** Slightly broad public API that could be narrowed; a single file
  that would benefit from splitting but is still understandable.
