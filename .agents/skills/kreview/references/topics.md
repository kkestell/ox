# Review topics

Each section below serves both modes. In general mode, select the sections that
fit the corpus after inventorying it. In specific-topic mode, work only the
explicitly selected sections. In either mode, the relevant checklist is the
review and each applicable item deserves an answer.

For a diff, commit, or branch review, “the diff” and “changed” below mean that
change. For a directory or whole-codebase review, they mean the requested
corpus. Inventory that corpus first. In specific-topic mode, read every
production file in it and the relevant tests; then apply each selected checklist
to the matching code. A search identifies candidates to inspect. It does not
complete an exhaustive review.

## General

After inventorying the corpus and its contracts, select the topics that its
changed behavior, boundaries, and risks make material. For example, select
`unsafe` only when `unsafe`, `reflect`, or cgo is involved; select `api-design`
and `documentation` for exported or shared contract changes; and select
`dependencies` for module configuration changes. Do not select a topic solely
because it exists.

Apply every relevant checklist in each selected section to the requested corpus,
follow its call paths and invariants, and verify suspicions with focused
evidence. Record the selection and its reasons, then either confirmed findings
or a one-line "nothing to report" for each selected topic. Finish with the
selected-topic verdict list and any follow-up requiring evidence outside the
corpus.

## Resources

Review value and pointer semantics, aliasing of shared data, copying, and the
lifetime of files, connections, processes, and goroutine-held state.

Read the changed types' definitions, every construction site, and each caller
that passes data across the boundary.

- For each pointer in a signature, field, or slice element, name the reason it
  is a pointer: mutation, sharing, size, or nilability. Under this repository's
  rules, prefer values until mutation or sharing requires otherwise. A pointer
  that exists only out of habit is a finding.
- For each value type, check that copying it is safe and cheap. A struct
  containing a `sync.Mutex`, a `sync.WaitGroup`, or a `strings.Builder` must not
  be copied; check `go vet`'s copylocks result rather than assuming.
- Trace aliasing through slices and maps. Check whether a stored slice or map
  shares a backing array with the caller's, whether `append` may mutate a
  caller's array, and whether a constructor or getter hands out a mutable
  reference to internal state that the caller can change behind the type's back.
- Check every retained slice of a larger buffer for pinning memory the rest of
  the program no longer needs, and every `[]byte` derived from a reused read
  buffer for being stored past the next read.
- Check defensive copies: `slices.Clone`, `maps.Clone`, `bytes.Clone`, and
  `copy` in the diff each need a boundary they protect. A copy expressing the
  simplest correct boundary is correct; say so and move on.
- Check every acquired resource has exactly one release on every path: `Close`,
  `Unlock`, `cancel` from `context.WithCancel`, `Stop` on timers and tickers,
  and `Wait` on processes. Check `defer` runs in the right scope, not inside a
  loop that accumulates until the function returns.
- Check `Close` errors on writes and flushes are not silently dropped, and that
  a deferred `Close` on a written file does not hide a failed flush.
- Check string and byte conversions that copy in a hot path, and struct fields
  or interface boxing that force an allocation the caller did not expect.
- Check that indices, handles, ids, and keys cannot outlive or be used against
  the wrong container, and that the container's lifetime is clear where it is
  not obvious.

Verify with `go vet ./...`, `staticcheck ./...`, a `grep` for each type's
construction sites, and a test that mutates the caller's data after handing it
across the boundary.

## Error handling

Review whether fallible paths return errors consistently, preserve useful
context, and distinguish recoverable failures from violated invariants.

Read the boundary where errors reach the user, the package's existing error
values, and every caller of the changed fallible functions.

- Enumerate every `panic`, `log.Fatal`, `os.Exit`, type assertion without the
  comma-ok form, indexing, slicing, and integer division in the diff. For each,
  state the reason it cannot fire on a production path, or report it. Under this
  repository's rules a panic on a violated internal invariant is intended; a
  panic reachable from bad external input is a finding, because that input must
  be validated and reported at the boundary.
- Check every returned error for the context it carries. Ask whether the message
  reaching the user names the file, session, method, tool, or input that caused
  it. Check `fmt.Errorf` uses `%w` where a caller needs `errors.Is` or
  `errors.As`, and does not wrap where nothing unwraps.
- Check error strings against Go convention: lowercase, no trailing punctuation,
  no "failed to" prefix stacked on another one, and no duplicate context added
  at each level of the call stack.
- Look for discarded errors: `_ =`, ignored results from writes, flushes,
  `Close`, and `Encode`, and empty error branches. Decide whether the fallback
  hides a real failure.
- Check sentinel errors and custom error types against this repository's rules:
  define one only where code actually branches on it. Confirm each has a caller
  using `errors.Is` or `errors.As`, and that callers do not match on strings.
- Check error classification: a user-input problem reported as an internal
  failure, or an internal bug reported as a user diagnostic, is a finding. At
  the ACP boundary, check the problem maps to the right JSON-RPC error and
  message.
- Trace failure atomicity. When a fallible step runs after a mutation, confirm
  the aborted operation leaves no partial state, half-written file, or dangling
  registration.
- Check that a failure reports once, at one place, with one exit status, rather
  than being logged deep and re-reported shallow.
- Check `context.Canceled` and `context.DeadlineExceeded` are distinguished from
  real failures wherever cancellation is expected.
- Check any `recover` for the narrow invariant it protects and confirm it does
  not convert a bug into a silently degraded result.

Verify by writing or running a test that forces each failure path, and by
reading the message it actually produces.

## API design

Review exported and shared interfaces for clear names, predictable behavior,
minimal surface, ergonomic signatures, and types that make invalid states hard
to express.

Read the package's existing API and this repository's Go design rules before
judging new surface.

- Check every newly exported identifier. Ask whether it can be unexported. Check
  that nothing exported returns an unexported type a caller cannot name, and
  that internal state is not exported merely to make tests convenient.
- Check names against the package's existing vocabulary and Go conventions: no
  stutter with the package path, `New` for constructors, `Get` omitted from
  getters, `-er` names for single-method interfaces, initialisms cased
  consistently. Do not invent a second name for a concept the codebase already
  names.
- Check interfaces are defined at the consumer, are small enough to explain in
  one sentence, and have a real second implementation or a test double that
  justifies them. Under this repository's rules, a concrete type is preferred
  until multiple real implementations exist.
- Check argument and return types for the encoding of invalid states: a named
  type over a bare `string` or `int`, an enumerated constant over a `bool` pair,
  a pointer or an explicit zero-value contract over a sentinel.
- Check parameter order, count, and any `bool` flag that reads as a mystery at
  the call site. Check that `ctx context.Context` is the first parameter and is
  not stored in a struct.
- Check the zero value is useful, or that the type documents why it must be
  constructed. Check `String`, `Error`, `MarshalJSON`, and `UnmarshalJSON`
  implementations for the semantics a caller expects.
- Check JSON and wire struct tags against the protocol types they encode: field
  names, `omitempty`, pointer versus value for optional fields, and unexported
  fields that silently drop from the encoding.
- Confirm every new option, functional option, generic type parameter, or
  extension point has a present caller. Speculative flexibility is a finding
  under this repository's rules.
- Check documentation on each exported item for the contract a caller needs.

Verify by writing the call site a real caller would write, and by checking every
existing caller still reads clearly.

## Performance

Review algorithmic complexity, repeated work, allocation and copying, hot loops,
I/O patterns, and data layout where they plausibly matter.

Read the call path from the ACP method or tool entry point to the changed code,
and establish whether this code runs once per session, per turn, per tool call,
per streamed event, or per byte.

- State the input size and call frequency before judging anything. A finding
  without that context is speculation.
- Compute the complexity of each changed loop and recursion, including the cost
  of the operations inside it. Look for a linear scan inside a loop, a quadratic
  containment check that a map makes linear, and repeated sorting or repeated
  traversal of the same structure.
- Look for work that is invariant across a loop, recomputed on each call, or
  computed eagerly and then discarded on most paths.
- Find allocations on the hot path: per-iteration slices and maps, `fmt.Sprintf`
  where concatenation or `strconv` suffices, string-to-`[]byte` round trips,
  interface boxing of large values, and intermediate slices a single pass
  removes. Check whether `make` with a known capacity or a reused buffer fits.
- Check copies of large structs through arguments, returns, range variables, and
  channel sends.
- Check I/O for unbuffered reads and writes, per-item syscalls, per-event flush,
  repeated `os.Stat` or directory walks, and files read more than once. Check
  streamed model output and streamed tool output are not accumulated whole when
  a bounded buffer is the contract.
- Check that a mutex is not held across I/O, and that a lock is not the
  bottleneck a per-item copy would avoid.
- Weigh each candidate against this repository's stated priority of simple,
  unsurprising code. Report a slowdown that matters; do not trade readable code
  for a micro-optimization without evidence.

Verify with a measurement: run a benchmark with `go test -bench`, count
allocations with `-benchmem`, or add a counter. Report the measured effect, or
state plainly that the finding is unmeasured.

## Testing

Review whether tests cover boundary values, failure paths, state transitions,
feature interactions, and regressions introduced by the change.

Read the existing tests for the changed packages, the fixtures they use, and
this repository's rules about where behavioral coverage belongs: the smallest
useful test at the lowest stable boundary, with end-to-end tests in
`internal/e2e` as the preferred proof of ACP-visible behavior.

- Map every behavior the diff adds or changes to the test that proves it. Name
  the untested ones. An unmapped behavior is the finding, not a missing test
  count.
- Check the test sits at the right boundary: a unit test where the rule lives, a
  test in `integration/` for runtime interaction, and an `internal/e2e` test for
  anything an ACP client observes. A protocol behavior proven only by a unit
  test on an internal helper is a finding.
- Check boundary values concretely: zero, one, many, empty input, maximum and
  minimum integers, off-by-one indices, first and last element, and the exact
  threshold in each comparison the diff introduces.
- Check that every error and diagnostic the change can produce has a test that
  triggers it and asserts the message or JSON-RPC error the client sees.
- Check interactions, not only features in isolation: the new behavior under
  cancellation, under a second concurrent session, after replay, and combined
  with permissions and existing tools.
- Check assertions prove behavior. A test asserting only that the code did not
  error, or asserting a field the implementation happens to set, is a weak test;
  say what it should assert instead.
- Check end-to-end tests follow the harness rules: streams composed with the
  `sse` and `ev*` builders, files seeded through harness options, queued
  responses named where concurrent turns must not depend on arrival order, and
  mid-turn cancellation modeled with `hold`.
- Check tests do not depend on the keyring, the network, real credentials, or
  wall-clock sleeps, and that every fixed bug gained a regression test.
- Check tests fail for the right reason: an assertion that passes on both old
  and new behavior proves nothing.

Verify by running `make check`, or the focused package with
`go test -race -count=1`, and by temporarily breaking the new code to confirm a
test catches it. Report which tests you ran and what they showed.

## Readability

Review whether names, control flow, and decomposition make the code easy to
reason about locally.

Read each changed function end to end and try to state its rule in one sentence.

- For each changed function, name its single job. A function you cannot describe
  without "and" is a finding; name the extraction.
- Count nesting levels and early exits. Look for conditions that invert into a
  guard clause, `else` branches that a `return` removes, and `switch` cases that
  collapse. Prefer the happy path at the left margin.
- Check every name against the thing it holds: no `data`, `info`, `tmp`, `res`,
  `helper`, `handleX`, or `doY` where the domain has a word. Check that the same
  concept uses the same word everywhere in the change, and that receiver names
  are short and consistent across a type's methods.
- Check each new helper removes or names a concept rather than merely moving
  lines. Under this repository's rules, a little duplication is preferred to a
  premature abstraction.
- Check that a reader can verify each block without holding distant state in
  mind. Flag invariants enforced far from where they are relied on, mutation
  through a wide-open pointer across many lines, and flags read much later than
  they are set.
- Check comments. Remove ones that narrate the code, and require one where a
  constraint, a reason, or a non-obvious choice is invisible. Under this
  repository's rules, comments never record history, corrections, or
  alternatives rejected.
- Check control flow for hidden side effects: a getter that mutates, a predicate
  that logs, a `String` method that computes or locks.
- Check that similar cases read similarly. Two branches doing the same thing in
  two shapes cost the reader twice.
- Check a direct conditional was not replaced by an indirect dispatch mechanism,
  a registry, or a table that a `switch` states more plainly.
- Suggest restructuring only when it makes the rule clearer, and show the
  clearer version rather than describing it.

Verify by reading each proposed rewrite back in place. If the rewrite is not
plainly easier to follow, drop the finding.

## Concurrency

Review shared-state ownership, synchronization, atomics, lock scope and
ordering, channel protocols, `context` cancellation, shutdown, and goroutine
lifetime.

Read every producer and consumer of the shared state, not only the changed side.

- Enumerate the shared state the change touches and name its protection for each
  field: a mutex, an atomic, a channel, or single-goroutine ownership.
  Unprotected shared mutability is a finding. Under this repository's rules,
  concurrency needs real independent work or cancellation to justify it at all.
- Check each critical section for the invariant it maintains, and for a
  check-then-act split across two acquisitions.
- Check lock scope: work, allocation, I/O, logging, a channel send, or a
  callback held under a lock, and a deferred unlock covering a whole function
  that needed the lock for one line.
- Establish a lock order for every path that takes two mutexes and confirm all
  paths agree. Check for re-entrant acquisition through a callback or a nested
  helper; Go mutexes are not reentrant and this deadlocks.
- Check each `sync/atomic` use against what it synchronizes, and prefer the
  typed `atomic.Bool`, `atomic.Int64`, and `atomic.Pointer` forms over loose
  functions on plain fields.
- Check every goroutine the change starts for a defined exit: who cancels it,
  what closes its channels, and who waits for it. A goroutine that outlives the
  session, turn, or request that started it is a finding.
- Check `context` propagation: every blocking call takes a context, cancellation
  reaches the model stream, subprocesses, and MCP calls, and no path ignores
  `ctx.Done()` in a select.
- Check channel protocols: who closes each channel and exactly once, sends that
  can block after the receiver returned, unbuffered sends in a cancelable path
  without a `select` on `ctx.Done()`, and a nil channel that blocks forever.
- Check for a `WaitGroup` whose `Add` races its `Wait`, a `time.Timer` or
  `Ticker` never stopped, and a `sync.Once` guarding state that other paths also
  initialize.
- Check loop variables and closures captured by goroutines, and shared maps
  written from more than one goroutine.
- Check cancellation at every point that can block: what state is left behind,
  and whether a partially applied mutation survives.

Verify with `go test -race -count=1` on the affected packages, a stress run with
`-count` repetition, and a test that exercises both interleavings. Say which you
ran.

## Security

Identify the change's trust boundaries before judging it, then review validation
and normalization of untrusted input, authorization, injection and path
handling, resource limits, sensitive logging, secret storage, and disclosure.

Read where each input enters the process and what it is trusted to be.

- Name every trust boundary the change crosses: ACP requests from the client,
  model output and tool arguments, MCP server responses, workspace file
  contents, subprocess output, environment variables, and network data. State
  what the code assumes about each. Model-chosen tool arguments are untrusted
  input.
- Trace each untrusted value to its use. Check validation happens at the
  protocol boundary, before use, once, on the value actually used rather than a
  copy checked earlier.
- Check every path built from input against the workspace confinement rules:
  traversal, symlink following, absolute path injection, and escape from the
  session root. Check the path is resolved before it is checked, and that the
  checked path is the one opened.
- Check every command, query, template, or format string built from input for
  injection. Prefer an argument vector over a shell string, and check parsed
  shell permission rules against the command actually executed.
- Check permission prompts cannot be bypassed: a tool path that executes before
  the request, a cached grant applied to a different command, or a rule matched
  loosely.
- Check resource limits: unbounded recursion, unbounded reads into memory,
  unbounded allocation from an attacker-chosen size, unbounded tool output, and
  loops driven by an input-controlled count.
- Check arithmetic on sizes, lengths, offsets, and capacities for overflow that
  turns into an out-of-range slice or an under-allocation.
- Check what is logged, traced, printed in diagnostics, or written to durable
  session state: API keys, tokens, absolute paths, environment contents, and
  file contents. Confirm trace output stays sanitized.
- Check secret handling: hardcoded values, credentials in process arguments,
  keys read from the environment leaking into errors, and keyring entries stored
  under the wrong account.
- Check file creation for permissions, predictable temporary names, and
  time-of-check-to-time-of-use races.
- Tie each finding to a realistic threat and a concrete attacker input. Drop the
  checklist items that do not apply to this program's threat model.

Verify by constructing the malicious input and running it. Report what happened.

## Correctness

Review stated and implicit invariants, boundary conditions, arithmetic and
conversion behavior, state transitions, ordering, cleanup, and failure
atomicity.

Read the contract that owns the behavior first: `docs/spec.md`,
`eng/architecture.md`, the ACP schema, or the type's own documentation.

- State the rule the change is supposed to implement, quoting the owning
  document, then check the code against it clause by clause. Contract drift is
  the finding this topic exists to catch.
- Trace at least one success path, one boundary path, and one failure path with
  concrete values. Write the values down.
- Check every comparison for its boundary: `<` versus `<=`, inclusive versus
  exclusive ranges, the empty case, the single-element case, and the last
  iteration.
- Check every arithmetic operation for overflow, underflow, and division by
  zero, and every conversion between integer widths or between `int` and
  `int64`, `uint`, or `float64` for a value it silently changes.
- Check every `switch` for a missing case, a `default` that should be an error,
  and a new protocol variant that will silently fall through. Check every type
  switch and type assertion for the case it does not handle.
- Check `nil` handling distinctly from empty: a nil map written to, a nil slice
  versus an empty slice in JSON output, a nil pointer inside a non-nil
  interface, and a nil error value compared against a typed nil.
- Check state machines for reachable illegal transitions, states that never
  clear, and two fields that must agree but are set separately.
- Check ordering assumptions: map iteration order, evaluation order,
  short-circuit dependence, `defer` execution order, and sequence dependence
  between two calls.
- Check JSON round trips: a field that does not survive marshal and unmarshal,
  `omitempty` dropping a meaningful zero, and a durable session record that
  cannot be replayed into the same state.
- Check time handling: monotonic versus wall clock, timezone assumptions, and
  durations derived from user input.
- Check cleanup and failure atomicity: an aborted operation must not leave
  partial state, and the same failure must not be handled twice.
- Check the diff for behavior it changes without meaning to: a moved early
  return, a widened condition, a removed check another path relied on.

Verify by running the case you traced as a real test, and by re-reading the
owning document rather than trusting your memory of it.

## Unsafe

Review every `unsafe`, `reflect`, and cgo boundary for a documented, maintained
invariant.

Read the type's full package, since soundness is a property of the whole
abstraction rather than one expression.

- For each use of `unsafe`, write out the invariant it requires and the reason
  it holds here. An undocumented `unsafe` use is a finding regardless of whether
  it is currently correct. Under this repository's rules it also needs a reason
  the safe form was insufficient.
- Check each `unsafe.Pointer` conversion against the valid patterns in the
  `unsafe` package documentation. A `uintptr` held in a variable across
  statements is invalid; the garbage collector may move or free the object.
- Check `unsafe.Slice`, `unsafe.String`, and `unsafe.SliceData` uses for length,
  capacity, and the lifetime of the backing memory, and confirm no caller can
  mutate a string produced this way.
- Check that safe exported code cannot violate the invariant. If any exported
  path reaches an inconsistent state, the abstraction is broken, not the
  expression.
- Check layout assumptions: `unsafe.Sizeof`, `Alignof`, and `Offsetof` results
  the code depends on, struct field order assumptions, and any conversion
  between struct types whose layout is not guaranteed.
- Check `reflect` use for a simpler direct alternative, for kind checks before
  each call that panics, for settability before `Set`, and for exported versus
  unexported field access. Check `reflect.Value` is not retained past the
  lifetime of what it points at.
- Check every cgo boundary: parameter and return types against the C
  declaration, nullability, ownership and who frees, string encoding and NUL
  termination, errno conventions, and the pointer-passing rules that forbid
  storing a Go pointer in C memory.
- Check that no Go panic crosses a cgo boundary and no C call blocks the runtime
  in a way the code does not account for.
- Check build tags and `//go:linkname`, `//go:noescape`, or assembly for
  assumptions about a runtime detail that upgrades will break.

Verify with `go vet ./...` for its `unsafeptr` check, `go test -race -count=1`
whose `checkptr` instrumentation catches invalid pointer arithmetic, and a test
that exercises the boundary. State what you were unable to check.

## Architecture

Review whether responsibilities sit at clear boundaries and each behavior has
one implementation path.

Read `eng/architecture.md` and the codebase map in `AGENTS.md`, then locate the
change against them before judging its placement.

- Name the fact or rule the change implements and the package that owns it.
  Logic placed outside its owner, or split across two owners, is a finding.
- Check dependency direction against `eng/architecture.md`: protocol types below
  the agent, the agent above tools and providers, and no package reaching back
  into its caller. Import cycles broken by an interface defined in the wrong
  place are a finding.
- Search for a second implementation of the same rule. A fallback path, a legacy
  path, or a compatibility shim for a rule that already has an owner is a
  finding; the project has no users and no backward compatibility constraints.
  Name both paths and say which one should survive.
- Look for duplicated logic that should become one small helper, and for a
  helper that moved lines without naming a concept.
- Check coupling: a package reaching into another's internals, a type known in
  more layers than it should be, and bidirectional dependencies.
- Look for hidden global state, package-level variables, `init` functions,
  environment reads outside the settings layer, and caches that make behavior
  depend on history.
- Check every new interface, generic parameter, option, flag, and layer for a
  present caller. Speculative flexibility is a finding here.
- Check whether a removed special case left behind structure that is now
  redundant: newly identical branches, a one-variant type, a wrapper that only
  forwards, a helper with one caller that no longer earns its name.
- Check no new catch-all package appeared, and that a new package has a
  responsibility stated in one sentence.
- Check the change respects state ownership: durable session state, permissions,
  and workspace confinement each have one owner.
- Check that a new dependency, abstraction, subsystem, protocol, storage format,
  background process, or configuration surface was actually agreed, since this
  repository requires consulting the user first.
- Check facts are not restated across documents or across packages. One home per
  fact, and `eng/architecture.md` updated when a responsibility or boundary
  moves.

Verify by grepping for the rule's other implementations and by reading the
owning document rather than inferring the intended structure.

## Dependencies

Review whether each added or changed module is necessary and narrowly used.

Read the `go.mod` diff, the `go.sum` diff, and what the module is actually used
for.

- For each added dependency, name the code that uses it and how much of it.
  Under this repository's rules, a module pulled in for one function that the
  standard library or a small local solution covers is a finding.
- Check the transitive addition in `go.sum` and `go mod graph`: its size, any
  module you would not want in this build, and whether it drags in a second
  logging, HTTP, or serialization stack.
- Check the version requirement is an exact released version, not a pseudo
  version pinned to an unreviewed commit, and that no `replace` directive
  targets a local path.
- Check maintenance signals: last release, open advisories, maintainer count,
  whether the module is a thin wrapper, and whether it contains `unsafe`, cgo,
  or code generation.
- Check `go.mod`'s Go directive and toolchain line against the version this
  project builds with, and that a dependency does not raise the required
  version.
- Check portability: build constraints the dependency forces, cgo it requires,
  and platforms it does not support.
- Check whether the dependency's types appear in this repository's exported API
  or in durable state and protocol payloads, which makes its version part of the
  contract.
- Check test-only dependencies are not reachable from production code, and that
  tooling used only by `make` is not a module requirement.
- Check the dependency does not duplicate a capability the project already has.

Verify with `go mod tidy` producing no diff, `go mod why` for each new module,
`govulncheck ./...` where available, and a clean `make check`.

## Documentation

Review exported API documentation and explanations of non-obvious invariants.

Read the doc comments in the diff, the items they describe, and the repository
documents that own the same facts.

- Check every exported item for a doc comment that starts with its name and
  states its contract: what it does, what the caller must guarantee, and what
  the return value means.
- Check failure cases, panics, blocking behavior, goroutine safety, and platform
  behavior are documented wherever they apply. An exported function whose
  failure cases or concurrency requirements are undocumented is a finding.
- Check non-obvious invariants are recorded where a maintainer will see them: on
  the type or field that must uphold them, not in a distant package.
- Check examples compile and show the intended use. Verify with
  `go test ./... -run Example`.
- Check documentation against the code it describes. A comment that was true
  before the diff and is now wrong is worse than no comment.
- Check that a fact documented here is not also owned by `docs/spec.md`,
  `eng/architecture.md`, `eng/todo.md`, or `AGENTS.md`. Reference the owner
  rather than restating it, and confirm the owning document actually says what
  the reference claims.
- Check `eng/architecture.md` was updated when a responsibility, boundary, or
  durable decision changed, and left alone when the change only implements a
  design it already covers.
- Check comments explain constraints and reasons rather than narrating the code,
  and remove narration the diff added.
- Check that no comment or document records a correction, a migration, a
  superseded design, or history, and that planned work is described directly
  rather than by its position in the roadmap.
- Check spelling, terminology, and the project's vocabulary. The same concept
  gets the same word in code and prose.

Verify by reading each doc comment as a caller who has not seen the
implementation, by running `go doc` on the changed package, and by running
`make check-docs`.
