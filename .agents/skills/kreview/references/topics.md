# Review topics

Use the requested topics to investigate real behavior and risks, not to impose a
checklist of new guarantees. Trace relevant callers and concrete reachable
values. Existing documentation describes the starting design; the user's agreed
scope can revise it. Propose less machinery where ownership or a simpler
representation removes the problem.

Choose verification in proportion to the suspected consequence, following
`AGENTS.md`. A source trace may settle a structural finding; a behavior defect
may need a reproduction or focused Cargo test. Record actual coverage and
uncertainty.

## General

Select topics touched by the change or needed to understand its consequences.
Follow the relevant code beyond the diff when necessary. Do not add unrelated
lenses or invent a finding for a topic with no defect.

## Resources

- Establish who owns mutable values, what is borrowed, and whether callers share
  backing storage. Clones, `Arc`, and interior mutability should protect a real
  boundary; values containing synchronization state should not be duplicated
  without understanding their semantics.
- Follow acquired files, processes, connections, tasks, guards, and cancellation
  scopes to their cleanup. Flag concrete leaks or lifetimes that exceed their
  owner.
- Judge references, owned values, `Cow`, and allocation choices by their use,
  rather than requiring an explanation for every clone or optimizing harmless
  allocations.

## Error handling

- External failures should reach the caller as useful `Result` errors, with
  validation at the owning boundary. Panics, `unwrap`, and `expect` should
  expose programming errors rather than bad external input; duplicated checks
  may indicate unclear ownership.
- Check discarded, flattened, or overly broad errors for a real lost failure or
  lost context. Custom error types and classifications need callers that use the
  distinction.
- Trace cancellation and partial failure according to the operation's actual
  contract. External effects need not roll back unless the product requires it.

## API design

- Read real call sites. Keep shared surface small, names clear, and argument
  meaning apparent. Types, lifetimes, and trait bounds should express the
  operation rather than add conversions and wrappers without benefit.
- Traits, generics, and dynamic dispatch need multiple real implementations
  that make the abstraction clearer than a concrete type. Check whether they
  improve the actual API or only spread complexity.
- Check protocol encoding where field presence, default values, or optional
  values change observable behavior. Context and extension storage is a
  lifetime and ownership question, not an automatic defect.

## Performance

- Establish actual input size and call frequency before judging cost.
- Investigate repeated traversal, cloning, allocation, I/O, serialization, or
  retained output when it can materially affect a session. Prefer eliminating
  work over clever local tuning.
- Support performance claims with measurements when needed, and label
  unmeasured concerns. Avoid complexity that buys an irrelevant micro-optimization.

## Testing

- Check important retained and changed behavior at a stable boundary, including
  failure paths and interactions that have a concrete regression risk.
- Assertions should distinguish correct behavior from the reported defect and
  survive implementation changes. Tests for retired mechanisms may also retire.
- Prefer existing deterministic Cargo test harness facilities. Do not demand a
  separate test for every error message, every comparison, or every internal
  helper. Real provider checks require explicit authorization.

## Readability

- Follow the operation as a reader: state changes, ownership, and authority
  decisions should be visible near their use. Multiple representations or
  scattered flags may make local reasoning harder than a longer coherent
  function does.
- Helpers, traits, and macros should name concepts or remove complexity. Prefer
  direct control flow when indirection hides a simple decision.
- Comments explain a non-obvious reason or contract. Avoid narration and blanket
  documentation demands for self-explanatory code.

## Concurrency

- Establish the owner and synchronization of shared mutable state. Trace actual
  interleavings that could race, deadlock, block an executor, or apply a stale
  decision. Check `Send`/`Sync` bounds and `Arc`/mutex use against the real
  ownership model.
- Follow threads, async tasks, channels, callbacks, and subprocesses through
  cancellation and shutdown. Their lifetime should agree with the work that
  owns them.
- Check that cancellation can reach relevant waits and that live policy changes
  have the behavior required by the session contract. Use focused concurrency
  or interleaving tests when they would settle a concrete concern.

## Security

- Identify actual trust boundaries and model-chosen inputs. Tie findings to a
  realistic threat and a concrete input; do not invent a broader threat model.
- Trace permissions and workspace confinement through the operation actually
  executed, including symlink races, command construction, FFI boundaries, and
  delegated work.
- Check secret storage, diagnostic output, and tracing against their intended
  disclosure rules. Durable conversations and tool results intentionally carry
  content; they are not sanitized traces.
- Resource limits should address a concrete boundary risk. Unlimited
  conversation history is not automatically a defect when the product
  explicitly permits it.

## Correctness

- Check required behavior and explicitly agreed changes against the owning
  contract. Separate a regression from an intentional redesign.
- Trace reachable state transitions, ordering, encoding, and boundary values
  that could change the result. Numeric, `None`, and `Result` cases matter when
  reachable, rather than as a reason to add checks around every operation.
- Judge partial effects and recovery by the product contract. Identify a simpler
  state or ownership model when several fields must remain synchronized.

## Unsafe

- For `unsafe` blocks and FFI, establish the actual layout, aliasing, lifetime,
  ownership, thread-safety, and language constraints required for soundness.
- Check safe callers cannot violate those assumptions. Non-obvious safety
  obligations need an explanation where maintained, and the smallest safe
  abstraction should own the proof.
- For raw pointers, transmutation, pinning, reflection-like macros, or manual
  `Send`/`Sync` implementations, trace the kinds and values actually supplied.
  Verify unsafe assumptions with appropriate targeted tooling when useful.

## Architecture

- Name responsibilities, state owners, and dependency direction. For an
  authorized redesign, judge the proposed model rather than demanding the old
  module map survive.
- Look for duplicate implementations, redundant wrappers, cloned
  representations, trait layers, and serialization boundaries without a useful
  process or trust boundary.
- Keep dependencies few: do not recommend a crate merely to abstract a
  capability used once. Integration should retire the old path rather than
  recreate it.
- For claimed simplification, identify retired concepts and verify net source
  change including replacements. Update owning documents when decisions change.

## Dependencies

- Identify the implementation and maintenance the crate removes. Compare the
  whole local alternative, not the number of dependency calls.
- Assess actual integration, transitive, feature, toolchain, portability, and
  maintenance costs. An intentional version or edition change is a decision to
  assess, rather than an automatic defect. Local scratch replacements do not
  establish a shippable Cargo configuration.
- Check `Cargo.toml` and `Cargo.lock` match real use and that replacing a
  capability removes the old implementation.

## Documentation

- Document caller obligations that are not clear from names, types, and code. Do
  not require a ceremonial comment on every public item.
- Check changed documentation describes actual behavior and keep durable design
  decisions in their owning documents. Local implementation changes need no
  architecture rewrite.
- Guides describe shipped usage. Avoid duplicating requirements, test setup, and
  implementation mechanics across documents.
