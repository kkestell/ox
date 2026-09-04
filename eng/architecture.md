# Ox Architecture

This document describes Ox's durable design: its process boundaries, component
responsibilities, dependency direction, state ownership, and the decisions each
implementation slice must preserve. `eng/roadmap.md` tracks what gets built and
in what order.

## System boundary

Ox is a long-running ACP v1 agent process. Its standard input and output carry a
line-delimited JSON-RPC connection to one ACP client. The protocol boundary is
client-independent. Standard output is reserved for ACP traffic; logs and
command diagnostics go to standard error.

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
- Configuration is resolved and frozen for each session activation.
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
  authentication flow, durable sessions, prompt and tool orchestration,
  permissions, subagents, replay, streaming updates, stop reasons, and
  cancellation.
- `internal/openrouter` owns the provider vocabulary, HTTP boundary, SSE parser,
  retry policy, model catalog, and assembly of streamed provider responses.
- `internal/settings` owns global and workspace settings, validation,
  precedence, and the immutable request configuration attached to a session.
- `internal/credentials` owns credential precedence and mutable access to the OS
  keyring. It exposes the currently resolved credential without exposing keyring
  mechanics to the agent or provider.
- `internal/tools` owns the model-facing task, file, search, edit, and shell
  tool contracts and their implementations.
- `internal/shellrules` owns the command grammar used for reusable shell
  permissions.
- `internal/workspace` owns canonical session roots, confined file access,
  ignore-aware traversal, and bounded streamed output.
- `internal/e2e` owns the black-box process harness and protocol-level tests. It
  is test-only and contributes nothing to the shipped binary.
- `integration` owns runtime integration tests across agent, provider, tools,
  permissions, subagents, and durable state. It is also test-only.

The package list may change as responsibilities grow. The ownership and
dependency direction are the durable design. Split or merge packages when that
makes a responsibility clearer, then update this document.

## Dependency direction

Dependencies point from orchestration toward focused boundaries:

```text
cmd/ox
    -> agent
    -> credentials
    -> openrouter
    -> settings
    -> tools

agent
    -> acp
    -> credentials
    -> openrouter
    -> settings
    -> workspace

tools
    -> agent
    -> shellrules
    -> workspace
```

The ACP, credential, provider, settings, shell-rule, and workspace boundaries do
not depend on `agent` or `cmd/ox`. Provider types do not appear in ACP types,
and ACP types do not define provider behavior. The command package wires
concrete components together rather than hiding them behind a service registry.

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

The agent owns a collection of independent active sessions. A session owns its
canonical workspace, activation-frozen request configuration, model-visible
history, permission grants, read evidence, usage, and at most one active turn.
State shared across sessions is limited to process-level configuration inputs,
credentials, provider transport, logging, and the session store.

Each session is an owner-only, versioned JSONL log in the Ox data directory.
Records are appended and synced before live state or ACP-visible outcomes
advance. Activation holds an operating-system file lock, repairs only a torn
final record, and rejects concurrent ownership. Loading folds the log back into
model history and replays the recorded ACP transcript. An unfinished turn is
closed as interrupted before the session accepts more work.

Prompt execution stays within its JSON-RPC request lifecycle. The model and
independent tools may run concurrently where their contracts allow it, while
session mutation and conflicting tool calls remain serialized. Cancellation
reaches provider streams, permission callbacks, subagents, and whole shell
process groups. The resulting terminal state is persisted before the prompt
returns.

## Provider boundary

The agent translates validated ACP prompt content into provider messages.
`internal/openrouter` knows only OpenRouter requests, responses, and streamed
deltas. It returns the completion assembled so far when a stream ends early so
the agent can apply ACP cancellation and history rules.

The provider boundary caches and validates OpenRouter's model catalog. It
retries transient failures within a bounded budget only before response content
has been observed. Raw reasoning details and usage survive provider translation
when they are needed for continued requests or accounting.

Provider transport failures remain ordinary Go errors. ACP-visible stop reasons,
refusal behavior, and durable history are decided by the agent, where the client
protocol and session state meet.

## Configuration and credentials

Model settings have three layers, in precedence order: `OX_MODEL`,
`<workspace>/.ox/settings.json`, then the global settings at
`$XDG_CONFIG_HOME/ox/settings.json` or `$HOME/.config/ox/settings.json`.
Workspace settings override individual global fields. Unknown keys, invalid
values, and model options that conflict with catalog capabilities are errors.
`OX_LOG_LEVEL` and `OX_OPENROUTER_BASE_URL` remain process-only so a workspace
cannot redirect requests or control process logging.

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

Read, glob, and grep operations establish session-scoped evidence. Writes and
exact edits require that evidence, preserve the existing file's text format and
mode, and replace files atomically. Discovery follows Git ignore rules. Large
tool and shell output is bounded in the conversation and spills to a confined
session directory.

Mutating file tools and shell commands require ACP permission unless a previous
session grant covers the operation. Reusable shell grants are derived from a
parsed command rather than string prefixes. Shell commands run from the session
root with a sanitized environment, and cancellation kills their process group.
Read, write, and exact-edit file content uses ACP filesystem callbacks when the
client advertises the corresponding method and otherwise uses the local
executor. Client delegation does not change Ox's workspace, read-evidence,
text-preservation, or permission rules. Terminal execution remains local until
ACP terminal delegation is implemented.

## Testing boundaries

Focused unit tests exercise ACP validation, session folding and storage,
settings, credentials, tool scheduling, workspace confinement, shell process
handling, and provider behavior at their package boundaries. Runtime integration
tests cover tools, permissions, subagents, replay, and recovery. Protocol-level
ACP behavior is proved through `internal/e2e`, which builds the real executable
and drives its process, stdio, environment, working directory, and provider
connection.

Client interoperability is proved separately through `internal/e2e/browser`.
Playwright drives a pinned, unmodified ACP UI web release, which connects to the
real Ox executable through a pinned upstream stdio-to-WebSocket bridge. The
harness owns browser and process orchestration plus the fake provider; it does
not implement, translate, or assert ACP messages itself. The canonical ACP
schema remains the oracle for capabilities that the browser client does not
exercise.

The mock provider is the normal end-to-end boundary. A real OpenRouter check is
reserved for explicit provider interoperability work and does not replace the
deterministic suite.
