# ADR-0001: Lua for Extensions

- **Status:** accepted
- **Date:** 2026-03-28

## Context and Problem Statement

Ur needs an extension system that allows third parties to add tools, middleware, and behaviors. The extension runtime must be compatible with .NET AoT compilation, which rules out dynamic assembly loading. It must also support sandboxing, since extensions come from untrusted sources (especially workspace extensions).

Relevant docs: [Extension System](../extension-system.md), [Permission System](../permission-system.md)

## Decision Drivers

- AoT compatibility — no dynamic assembly loading, no runtime reflection
- Sandboxing — extensions must be isolatable with controlled access to system resources
- Simplicity — single developer, small codebase, low maintenance burden
- Developer experience — extension authors should find the system approachable
- Performance — extension calls should not add significant latency to the agent loop

## Considered Options

### Option 1: .NET Plugins (MEF, AssemblyLoadContext)

**Description:** Extensions are .NET assemblies loaded at runtime.

**Pros:**

- Extensions written in the same language as the host
- Full access to .NET ecosystem and type system
- Familiar to .NET developers

**Cons:**

- Incompatible with AoT compilation — this is a hard blocker
- Assembly loading is complex (dependency conflicts, version binding)
- Sandboxing .NET assemblies is effectively impossible without a separate process

**When this is the right choice:** When AoT is not a constraint and extensions need deep .NET integration.

### Option 2: WebAssembly (WASM)

**Description:** Extensions compiled to WASM and run in a WASM runtime (e.g. wasmtime, Extism).

**Pros:**

- Strong sandboxing by design
- Language-agnostic — extensions could be written in Rust, Go, C, etc.
- Growing ecosystem and tooling

**Cons:**

- Adds a WASM runtime dependency (larger binary, more complexity)
- .NET-to-WASM interop is less mature than Lua-CSharp interop
- Extension authors need a WASM-compatible toolchain
- Debugging is harder

**When this is the right choice:** When extensions need to be written in multiple languages, or when the strongest possible sandboxing is required.

### Option 3: Lua via Lua-CSharp

**Description:** Extensions are Lua scripts. The Lua runtime is provided by [Lua-CSharp](https://github.com/nuskey8/Lua-CSharp), which offers `LuaPlatform` for sandboxing.

**Pros:**

- AoT compatible — no dynamic loading, no reflection
- Built-in sandboxing via `LuaPlatform` (controls filesystem, I/O, OS access)
- Lightweight runtime, fast startup
- Lua is simple to learn and widely known
- Each extension gets its own `LuaState` — isolation without process boundaries

**Cons:**

- Extension authors must write Lua, not C#
- Lua-CSharp is a relatively young library — risk of bugs or missing features
- The C# API surface exposed to Lua must be carefully designed and maintained
- Lua's type system is minimal — no compile-time safety for extension code

**When this is the right choice:** When AoT is a hard constraint, sandboxing matters, and the extension surface is primarily scripting (registering tools, hooking middleware) rather than heavy computation.

### Option 4: Embedded Python

**Description:** Extensions are Python scripts, run via an embedded Python interpreter.

**Pros:**

- Very widely known language
- Huge ecosystem of libraries

**Cons:**

- Python embedding in .NET is complex and fragile
- Python runtime is heavy (large binary, slow startup)
- Sandboxing Python is extremely difficult
- AoT compatibility is uncertain

**When this is the right choice:** When the extension ecosystem needs access to Python's scientific/ML libraries.

## Decision

We chose **Lua via Lua-CSharp** because it is the only option that satisfies all three hard constraints simultaneously: AoT compatibility, sandboxing, and simplicity. The AoT constraint eliminates .NET plugins outright. WASM satisfies the constraints but adds significant complexity for a single-developer project. Python is too heavy and too hard to sandbox.

Lua is a pragmatic fit: the extension surface (registering tools, hooking middleware, light scripting) does not need a heavyweight language. Lua-CSharp's `LuaPlatform` gives us sandbox controls at the right granularity.

## Consequences

### Positive

- Extensions are lightweight and fast to load
- Sandbox is built into the runtime, not bolted on
- Each extension is isolated in its own `LuaState`
- AoT works without workarounds

### Negative

- Extension authors must learn Lua (though it is a small language)
- The Lua API surface becomes a compatibility commitment
- Lua-CSharp maturity is a risk — may need to contribute fixes upstream

### Neutral

- Inter-extension communication must go through Ur APIs, not shared state
- Extension testing requires a Lua test runner or Ur test harness

## Confirmation

- AoT publishing succeeds with Lua-CSharp included — test this early and continuously
- Extension authors can build useful tools without fighting the language or the API — validate with 3-5 real extensions
- Lua-CSharp bugs do not block progress — monitor upstream issues
