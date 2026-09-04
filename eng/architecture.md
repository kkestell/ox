# Ox Architecture

This document describes Ox's durable design: its process boundaries, component
responsibilities, dependency direction, state ownership, and the decisions each
implementation slice must preserve. `eng/roadmap.md` tracks what gets built and
in what order.

## System boundary

Ox is a long-running ACP v1 agent process. Its standard input and output carry a
line-delimited JSON-RPC connection to one client. Zed is the primary client, but
the protocol boundary remains client-independent. Standard output is reserved
for ACP traffic; logs and command diagnostics go to standard error.

```text
ACP client
    <-> JSON-RPC over stdin/stdout
Ox process
    <-> HTTPS and server-sent events
OpenRouter
```

Ox owns model interaction, protocol semantics, sessions, tool orchestration,
permissions, and durable conversation state. The client owns presentation,
editor integration, terminal presentation, context attachment, and process
supervision. Features that cross that boundary use ACP methods and negotiated
capabilities rather than client-specific side channels.

## Invariants

- External values from ACP, configuration files, the environment, the keyring,
  and OpenRouter are interpreted at their boundary before domain code uses them.
- Standard output contains only ACP JSON-RPC messages.
- A session has one canonical working directory and cannot access a path outside
  it, including through absolute paths, parent traversal, or symlinks.
- Configuration is resolved and frozen when a session is created.
- Each session admits at most one prompt turn at a time. Different sessions may
  run concurrently.
- Session history preserves exactly the conversation the model should see on the
  next turn, including streamed text retained after client cancellation and
  excluding a refused prompt.
- Provider-specific request, response, and streaming details do not leak into
  ACP wire types.
- Cancellation remains able to reach work that is waiting on the client or
  provider.

## Responsibilities

The current package layout assigns one owner to each boundary:

- `cmd/ox` owns process startup, environment capture, logging, the stdio
  transport, and top-level commands. It contains no session or provider
  semantics.
- `internal/acp` owns the ACP wire vocabulary and validation of client input. It
  does not own session state or provider translation.
- `internal/agent` owns ACP method semantics, capability negotiation,
  authentication flow, in-memory sessions, prompt translation, streaming
  updates, stop reasons, and cancellation.
- `internal/openrouter` owns the provider vocabulary, HTTP boundary, SSE parser,
  and assembly of a streamed provider response.
- `internal/config` owns model configuration files, validation, precedence, and
  the immutable configuration attached to a session.
- `internal/credentials` owns credential precedence and mutable access to the OS
  keyring. It exposes the currently resolved credential without exposing keyring
  mechanics to the agent or provider.
- `internal/workspace` owns canonical session roots and path values that remain
  confined to them.
- `internal/e2e` owns the black-box process harness and protocol-level tests. It
  is test-only and contributes nothing to the shipped binary.

The package list may change as responsibilities grow. The ownership and
dependency direction are the durable design. Split or merge packages when that
makes a responsibility clearer, then update this document.

## Dependency direction

Dependencies point from orchestration toward focused boundaries:

```text
cmd/ox
    -> agent
    -> config
    -> credentials
    -> openrouter

agent
    -> acp
    -> config
    -> credentials
    -> openrouter
    -> workspace
```

Boundary packages do not depend on `agent` or `cmd/ox`. Provider types do not
appear in ACP types, and ACP types do not define provider behavior. The command
package wires concrete components together rather than hiding them behind a
service registry.

## Protocol boundary

ACP requests and notifications enter through the method handlers in
`internal/agent`. `internal/acp` determines whether their wire representation is
valid; handlers determine whether an operation is valid for current agent and
session state. Invalid client input becomes a useful JSON-RPC error. A malformed
notification that cannot receive a response is logged when the client must know
about the lost operation through other state.

Session updates are written before the prompt response completes. Client and
agent capabilities are negotiated during initialization, and optional behavior
is enabled only when the advertised capability supports it.

## Session and turn state

The agent owns a collection of independent sessions. A session owns its
canonical workspace, frozen configuration, model-visible history, and at most
one active turn. State shared across sessions is limited to process-level
configuration inputs, credentials, provider transport, logging, and negotiated
client capabilities.

Prompt execution stays synchronous within its JSON-RPC handler so update order
and the final response share one lifecycle. Concurrency comes from independent
handlers and sessions rather than background turn managers. Cancellation marks
the active turn and cancels its context without waiting for that turn to finish.

Sessions are currently in memory. Durable session work will add persistence
behind the session boundary without making the ACP transport or provider own
conversation state.

## Provider boundary

The agent translates validated ACP prompt content into provider messages.
`internal/openrouter` knows only OpenRouter requests, responses, and streamed
deltas. It returns the completion assembled so far when a stream ends early so
the agent can apply ACP cancellation and history rules.

Provider transport failures remain ordinary Go errors. ACP-visible stop reasons
and refusal behavior are decided by the agent, where the client protocol and
session history meet.

## Configuration and credentials

Model configuration has three layers, in precedence order: `OX_MODEL`,
`<workspace>/.ox/config.json`, then the global configuration at
`$XDG_CONFIG_HOME/ox/config.json` or `$HOME/.config/ox/config.json`. The only
configuration-file key is `model`. Unknown keys and blank model values are
errors. `OX_LOG_LEVEL` and `OX_OPENROUTER_BASE_URL` remain process-only so a
workspace cannot redirect requests or control process logging.

OpenRouter credentials come first from `OPENROUTER_API_KEY`, then from the OS
keyring entry for service `ox` and account `openrouter`. `OX_KEYRING_DISABLED=1`
turns keyring access off. Credential mutation belongs to authentication and the
`login` command; provider code only reads the currently resolved key.

## Workspace boundary

Session creation resolves the requested working directory to one canonical,
listable directory. Every model-facing filesystem path is resolved relative to
that root and rejected if its best canonical location escapes. File operations
use root-confined operating-system handles so the confinement check and access
cannot be separated by a symlink race. Only regular files can be opened as model
context.

Future filesystem and shell tools remain owned by the agent's tool layer and
operate through this workspace boundary. When the client advertises ACP
filesystem or terminal capabilities, delegation changes who performs an
operation, not which paths and permissions Ox considers valid.

## Testing boundaries

Focused unit tests exercise ACP validation, session rules, configuration,
credentials, workspace confinement, and provider streaming at their package
boundaries. ACP-visible behavior is proved through `internal/e2e`, which builds
the real executable and drives its process, stdio, environment, working
directory, and provider connection.

The mock provider is the normal end-to-end boundary. A real OpenRouter check is
reserved for explicit provider interoperability work and does not replace the
deterministic suite.
