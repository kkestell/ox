# Hierarchy

## The Zen

Every non-trivial system is a hierarchy of smaller systems. Hierarchy tames
complexity by letting you reason at one level of detail at a time—zoom in to see
how a subsystem works, zoom out to see how subsystems collaborate. When
hierarchy is clean, each layer is a complete thought: it has a clear job, talks
only to its neighbors, and shields higher layers from lower-level noise.

Booch's insight is that hierarchy is not optional decoration—it is the primary
mechanism humans use to comprehend large systems. If the hierarchy is muddled,
the system is incomprehensible regardless of how clean each individual class is.

## What Good Hierarchy Looks Like

- **Layers flow one way.** Higher layers depend on lower layers, never the
  reverse. A domain model should not import an HTTP handler.
- **Each layer is a coherent abstraction level.** Reading a package should feel
  like reading a single chapter, not a grab bag of unrelated details.
- **Depth matches complexity.** A simple CLI tool does not need six layers; a
  distributed payment system probably does. The right number of layers is the
  fewest that keep each layer simple.
- **Containment is meaningful.** A parent package or class owns its children
  because they *belong* together, not because someone needed a folder.
- **Inheritance depth is shallow.** Deep inheritance trees (>3 levels) create
  fragile coupling. Prefer composition or interfaces unless there is a genuine
  is-a relationship.

## What to Look For in a Review

### Layering violations

- A "low-level" module importing from a "high-level" one (e.g., a database
  driver importing a business rule).
- Circular dependencies between packages or modules at different layers.
- A single file or class that mixes code from two or more abstraction levels—
  for example, SQL string construction next to business validation logic.

### Flat or missing hierarchy

- A project where every file lives in one directory with no grouping.
- A monolithic class or module that does everything, with no sub-components.
- Long files (>500 LOC) that could be decomposed into smaller, named pieces.

### Over-deep hierarchy

- Inheritance chains deeper than 3 levels where intermediate classes add little
  behavior.
- Directory nesting so deep that file paths are hard to read or remember.
- Wrapper-upon-wrapper patterns where each layer just delegates to the next
  with trivial transformation.

### Broken containment

- Child modules that reach into sibling or parent internals instead of using
  the parent's public interface.
- "Utility" or "helpers" packages that everything depends on—a sign that shared
  code was not given a proper home in the hierarchy.
- God packages that own unrelated children purely for organizational
  convenience.

## Anti-Patterns

| Anti-Pattern | What Happens | Why It Hurts |
|---|---|---|
| **Lasagna architecture** | Too many thin layers, each doing almost nothing | Every change touches N files; indirection without value |
| **Big ball of mud** | No discernible layers at all | Cannot reason about the system at any level |
| **Upward dependency** | Lower layer imports upper layer | Breaks independent deployability and testability |
| **Diamond inheritance** | Multiple inheritance paths converge | Ambiguous method resolution, fragile super calls |
| **Yo-yo inheritance** | Must bounce up and down the class tree to understand behavior | Defeats the purpose of hierarchy—comprehension |
| **Leaky layer** | Higher layer must understand lower-layer details to function | Abstraction boundary exists in name only |
| **Circular packages** | A → B → C → A dependency cycle | Cannot build, test, or understand any package in isolation |

## Severity Guide

- **Critical:** Circular dependency between layers, or a foundational module
  that depends on application-level code.
- **High:** Inheritance depth >3 with fragile super-call chains; a layer that
  routinely leaks implementation details upward.
- **Medium:** Flat structure where grouping would materially aid comprehension;
  a utility grab-bag that everything imports.
- **Low:** Slightly excessive nesting; a single file mixing two adjacent
  abstraction levels in a small, well-understood context.
