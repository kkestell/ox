# Encapsulation

## The Zen

Encapsulation is the discipline of drawing a tight boundary around a component's
internals so that the rest of the system interacts with it only through a
deliberate, controlled interface. It is the enforcement arm of abstraction: where
abstraction decides *what* to hide, encapsulation decides *how* to enforce that
hiding.

When encapsulation holds, you can change a component's internals—data
structures, algorithms, storage format—without any caller knowing or caring.
When it breaks, the internals *are* the interface, and every change is a
negotiation with every consumer.

The practical test is simple: can you refactor this component's guts without
modifying any code outside it? If yes, it is well-encapsulated.

## What Good Encapsulation Looks Like

- **Data is private; behavior is public.** Fields are unexported. Callers
  interact through methods or functions that enforce invariants.
- **Invariants are maintained by the owner.** If an `Account` balance must
  never go negative, the `Account` type enforces that—callers cannot set the
  balance directly.
- **Construction is controlled.** Objects that require setup have constructors
  or factory functions; callers cannot create an invalid instance by filling
  fields manually.
- **Internal types stay internal.** Helper structs, implementation enums, and
  intermediate data formats are not exported.
- **The public API is the narrowest correct API.** Every exported symbol earns
  its place. "Might be useful later" is not a reason to export.

## What to Look For in a Review

### Exposed internals

- Public fields on a struct/class that callers read and write directly,
  bypassing any validation or invariant checking.
- Exported types that exist only to support the implementation (e.g., an
  internal cache entry type that leaks into the public API).
- Returning mutable references to internal collections—callers can modify the
  internals without going through the owner.

### Broken invariants

- State that can be put into an invalid configuration through the public API.
  For example, a `Range` where `start > end` is representable because both
  fields are public.
- Setter methods that accept any value without validation, deferring the check
  to usage time (or never).
- Construction paths that skip initialization—`new` without required fields,
  struct literals that leave critical fields at zero values.

### Accessor proliferation

- A class with a getter and setter for every field—encapsulation in syntax but
  not in spirit. If every field is read/write through accessors, the fields are
  effectively public.
- "Anemic domain models" where objects are pure data bags and all logic lives
  in external service classes that reach in to read and write fields.

### Encapsulation bypassed

- Reflection or serialization used to access private fields from outside the
  owning module.
- "Friend" patterns or package-level access used broadly rather than
  surgically—sign that the boundary is in the wrong place.
- Tests that reach into private state instead of asserting on observable
  behavior.

### Under-encapsulation at module level

- A package that exports everything because "someone might need it."
- Internal implementation details documented in public API docs, tying the
  contract to the current implementation.
- Configuration objects with dozens of public fields and no builder or
  validation, so callers must know which combinations are valid.

## Anti-Patterns

| Anti-Pattern | What Happens | Why It Hurts |
|---|---|---|
| **Public field free-for-all** | All fields exported, no methods guard them | Invariants live in callers' heads, not in code; any caller can corrupt state |
| **Getter/setter facade** | Every field gets trivial get/set methods | Ceremony without protection; same coupling as public fields |
| **Mutable return** | Method returns a reference to internal state | External code modifies internals; owner loses control of its own data |
| **Anemic model** | Objects hold data; separate services hold all logic | Logic and data are split; encapsulation exists nowhere |
| **Promiscuous export** | Everything is public "just in case" | Huge public surface locks in implementation details; hard to evolve |
| **Reflective intrusion** | External code accesses private members via reflection | Fragile, invisible coupling that breaks on rename or restructure |
| **Initialization bypass** | Callers create objects without using constructors | Invalid or partially initialized objects enter the system |

## Severity Guide

- **Critical:** Invariant-bearing state with no protection—callers can put the
  system into an inconsistent state through the public API, leading to data
  corruption or security holes.
- **High:** Mutable references to internal collections returned publicly;
  anemic models in a domain-heavy system where invariant violations cause
  business logic errors.
- **Medium:** Getter/setter facades that provide no real protection; overly
  broad exports that complicate API evolution.
- **Low:** A few fields that could be private but are unlikely to cause harm
  in practice; tests that peek at internal state for convenience.
