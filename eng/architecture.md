# Ox Architecture

This document describes Ox's durable design: its process boundaries, component
responsibilities, dependency direction, state ownership, and the decisions each
implementation slice must preserve. `eng/todo.md` tracks what gets built and in
what order. `docs/spec.md` owns observable behavior and limits. This design also
covers planned boundaries; the todo list identifies implementation status.

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
supervision. Ox targets Unix-like systems, so process groups, file ownership and
permission bits, and advisory locks are used directly rather than behind a
platform abstraction. Features that cross that boundary use ACP methods and
negotiated capabilities rather than client-specific side channels.

## Invariants

- External values from ACP, configuration files, the environment, the keyring,
  and OpenRouter are interpreted at their boundary before domain code uses them.
- Standard output contains only ACP JSON-RPC messages.
- A session has one canonical working directory. Built-in file tools cannot
  escape it through absolute paths, parent traversal, or symlinks. Host
  processes and external servers are not an OS sandbox.
- Activation inputs and each running turn are immutable. Explicit session
  selections are durable and affect subsequent turns.
- Each session admits at most one prompt turn at a time. Different sessions may
  run concurrently. A turn may own several live child loops.
- Session history preserves exactly the conversation the model should see on the
  next turn, including streamed text retained after client cancellation and
  excluding a refused prompt.
- Provider-specific request, response, and streaming details do not leak into
  ACP wire types.
- Cancellation remains able to reach work that is waiting on the client or
  provider.

## Responsibilities

The implemented package layout assigns one owner to each boundary:

- `cmd/ox` owns process startup, process inputs, logging, the stdio transport,
  and top-level commands. It resolves every per-user path once, under one rule
  that honors an XDG variable only when it is absolute, and hands concrete paths
  to the boundaries that use them; no lower package reads the environment. It
  contains no session or provider semantics.
- `internal/acp` owns the ACP wire vocabulary and validation of client input. It
  does not own session state or provider translation.
- `internal/agent` owns ACP method semantics, capability negotiation,
  authentication flow, durable sessions, prompt and tool orchestration,
  turn-scoped subagent lifecycles and messaging, permissions, replay, streaming
  updates, stop reasons, and cancellation.
- `internal/openrouter` owns the provider vocabulary, HTTP boundary, SSE parser,
  retry policy, model catalog, and assembly of streamed provider responses.
- `internal/mcp` owns MCP transport lifecycles, protocol negotiation, discovery,
  catalog identities, and bounded tool calls. It exposes no credentials through
  model-facing descriptors.
- `internal/lsp` owns language-server process lifecycles, JSON-RPC framing,
  query serialization, document synchronization, position translation, and
  confined query results.
- `internal/settings` owns global and workspace settings, precedence, and the
  validation of per-model request profiles and process-level language-server
  definitions. The agent combines those inputs with durable session selections
  to construct immutable turn configuration.
- `internal/skills` owns confined workspace-skill discovery, Agent Skills
  metadata validation, and activation-frozen body loading.
- `internal/credentials` owns credential precedence and mutable access to the OS
  keyring. It exposes the currently resolved credential without exposing keyring
  mechanics to the agent or provider.
- `internal/trace` owns the versioned, concurrency-safe JSONL diagnostic sink
  and its session, turn, provider, tool, and permission correlation scopes. It
  accepts only allowlisted metadata and never receives event content.
- `internal/tools` owns the model-facing file, search, edit, shell, todo,
  question, skill, memory, web-fetch, language-query, and subagent-coordination
  tool contracts and their implementations.
- `internal/shellrules` owns the command grammar used for reusable shell
  permissions.
- `internal/workspace` owns canonical session roots, confined file access,
  ignore-aware traversal, and bounded streamed output.
- `internal/e2e` owns the black-box process harness and protocol-level tests. It
  is test-only and contributes nothing to the shipped binary.
- `integration` owns runtime integration tests across agent, provider, tools,
  permissions, and durable state. It is also test-only.
- `evals` owns the external ACP evaluation client, versioned task fixtures,
  objective verifiers, run budgets, and evaluation artifacts. Production
  packages do not depend on it.

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
    -> trace

agent
    -> acp
    -> credentials
    -> lsp
    -> mcp
    -> openrouter
    -> settings
    -> skills
    -> trace
    -> workspace

tools
    -> agent
    -> lsp
    -> shellrules
    -> workspace

lsp
    -> workspace

settings
    -> lsp
    -> openrouter

skills
    -> workspace

mcp
    -> acp

evals
    -> acp
    -> ox subprocess
```

The ACP, credential, provider, settings, skill, shell-rule, and workspace
boundaries do not depend on `agent` or `cmd/ox`. Provider types do not appear in
ACP types, and ACP types do not define provider behavior. The command package
wires concrete components together rather than hiding them behind a service
registry.

The optional diagnostic trace is a lossy view of live execution rather than a
durable record. It receives identifiers, event kinds, timings, sizes, and
outcomes at orchestration boundaries. It does not receive prompts, model text,
tool arguments or output, workspace content, credentials, raw errors, or
configuration. Trace failure disables the sink without changing ACP or durable
session behavior.

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
canonical workspace, immutable activation inputs and turn configuration,
model-visible history, permission grants, usage, and at most one active turn.
Process configuration inputs, credentials, provider transport, logging, and the
session store are shared across sessions. Explicit workspace memory is shared
only by sessions with the same canonical root and has its own serialized owner.
MCP and language-server connections belong to individual activations. Each
language-server manager serializes its activation's whole queries, because each
one updates the server's view of open documents; separate sessions hold separate
managers and stay independent.

Each session is an owner-only, versioned JSONL log in the Ox data directory.
Records are appended and synced before live state or ACP-visible outcomes
advance. Activation holds an operating-system file lock, repairs only a torn
final record, and rejects concurrent ownership. Loading folds the log back into
model history and replays the recorded ACP transcript. Compaction records
replace only the provider-facing middle of that history with a model-produced
summary. The earlier user, assistant, and tool records remain authoritative for
ACP replay. Loading validates and applies every record to restore the exact
compacted provider history; there is no separate checkpoint projection.

Session listing briefly probes each log's activation lock without retaining it
and publishes an advisory namespaced metadata flag when another runtime owns the
session. Sessions already active in the listing runtime remain available in its
own list even though that runtime holds their locks.

The active turn owns an in-memory subagent group. Each child has a private
conversation, inbox, report stream, and cancellation scope while sharing the
turn's immutable provider configuration, activation resources, and permission
grants. Coordination tools mutate only this group. Child loops are not sessions
and do not own logs, recovery, configuration, or nested child groups. Ending the
turn cancels and joins the group before the durable turn outcome is committed.

An unfinished turn is closed as interrupted unless it has a durable pending
permission request. `session/load` reissues such a request with the same
tool-call identity and a new generation, ignores answers for older generations,
and stays open while Ox continues the turn. The recovered turn's stop reason is
persisted but not sent to the client because ACP exposes it only through the
original, now-lost `session/prompt` response. The successful `session/load`
response is the completion signal.

Turn execution stays within a JSON-RPC request lifecycle: ordinary turns run
under `session/prompt`, and recovered turns run under `session/load`. The model
and independent tools or children may run concurrently where their contracts
allow it, while session mutation stays serialized. Each loop dispatches its own
conflicting tool calls one at a time; a child's tool call never waits on the
primary agent or on another child. Cancellation reaches provider streams, child
loops, permission callbacks, and whole shell process groups. The resulting
primary terminal state is persisted before the owning request returns.

Each claimed live turn has one diagnostic scope when tracing is enabled.
Recovered work uses the original durable turn identifier, while replay of
already recorded history emits no trace events.

## Provider boundary

The agent translates validated ACP prompt content into provider messages.
`internal/openrouter` knows only OpenRouter requests, responses, and streamed
deltas. It returns the completion assembled so far when a stream ends early so
the agent can apply ACP cancellation and history rules.

The provider boundary caches and validates OpenRouter's model catalog. A cached
catalog is served while it is fresh and refetched on the spot once it is not, in
memory as well as on disk, so a long-running process sees current context
windows and parameters. It falls back to the stale entries only when the
provider is unreachable, and then bounds how soon it tries again so one outage
cannot cost a fetch per request; no background refresh owns that work. It
retries transient failures within a bounded budget, but only until part of the
answer has been streamed to the client, because ACP cannot unsend it. Message
text and tool calls are the answer; a failure during reasoning alone stays
retryable. Raw reasoning details and usage survive provider translation when
they are needed for continued requests or accounting.

The agent admits every provider request against the selected model's context
window. It budgets the complete messages and tool declarations plus the
configured output reserve before sending. Summarization uses the owning turn's
frozen model and provider settings. That request has its own system prompt and
no tools. The ordinary system prompt, tool declarations, first user message, and
complete recent message groups remain outside the summary. Context occupancy is
the last measured provider prompt size, or the estimated prompt size immediately
after a durable compaction; token and cost totals remain cumulative.

Primary and subagent loops have no model-request or tool-loop iteration limit.
The primary loop durably records a monotonically increasing request count for
recovery and diagnostics. Subagent response usage advances cumulative session
totals through a content-free record, while its private conversation remains in
memory only.

Provider transport failures remain ordinary Go errors. ACP-visible stop reasons,
refusal behavior, and durable history are decided by the agent, where the client
protocol and session state meet.

## Configuration and credentials

Activation resolves the configured model profiles, root instructions, skill
metadata, client capabilities, and tool definitions. A session freezes the whole
resolved profile map beside the OpenRouter catalog entry backing each one, so
the models a client may choose from and the request settings each carries are
fixed for that activation. Process inputs cannot come from the workspace.
`docs/settings.md` owns shipped configuration fields and precedence;
`docs/spec.md#process-configuration-transition` owns the planned transition.
This separation keeps process authority out of project-controlled files without
duplicating the settings reference here.

The agent owns durable session selections independently of activation inputs. A
selection records a model ID, not a copy of its settings, so every activation
resolves it against current files. A setter loads the chosen profile, validates
the resulting complete configuration, commits it, then publishes it. At turn
admission the agent constructs one immutable configuration used by the provider,
dispatcher, and children; a running or recovered turn never reads a later model
or reasoning selection.

Mode is deliberately outside that configuration. It is the session's current
execution policy, read afresh at every authorization decision and every provider
request, so choosing it reaches the turn already running rather than the next
one. The declared tool set a request carries is derived from the frozen
configuration under the current mode, which is what lets a withdrawn tool be
refused by name instead of reported as unknown. The dispatcher enforces tool
exclusion; prompt wording and server annotations are not policy enforcement.

Credentials are resolved by the credential boundary and passed to transports in
memory. Authentication and login own mutation. Durable configuration contains no
resolved credentials, MCP header values, or server environment values.
Reactivation obtains those inputs afresh. Nonsecret server/tool identity and
schema evidence bind saved permission grants and recovered dispatch to the
intended operation. Raw secret-bearing client input must not pass through
generic request logging or persistence.

## Context and durable projections

The append-only session log remains authoritative for the transcript and
session-owned state. Todo, configuration selections, and compaction boundaries
belong in that log. Their live state advances through the same commit path,
rather than through independently saved sidecar files.

Context admission occurs only at complete model/tool boundaries. The original
transcript and the provider-facing compacted projection have separate purposes;
ACP replay never substitutes summaries for recorded output. Static instructions
and tool catalogs form a stable prefix; transient state is explicit context that
trails history, so rewriting it leaves that prefix intact. Prompt caching is an
optimization and cannot affect history or request correctness.

The log cannot commit an external effect atomically. For the primary loop,
dispatch intent precedes execution, completion follows it, and a missing
completion means the outcome may be unknown. Recovery may continue a
never-dispatched permission wait but must not blindly retry a dispatched
operation. Child conversations and tool activity are deliberately live-only: the
log records their content-free provider usage but never reconstructs or
redispatches them. A storage failure stops further session dispatch.

## Extension boundaries

MCP and LSP integrations use focused protocol adapters wired by `cmd/ox` into
agent-owned activation resources and the existing tool dispatcher. Transport
adapters own framing, negotiated versions, deadlines, and resource cleanup; they
do not own permission decisions or durable history. MCP uses the
[official Go SDK](https://github.com/modelcontextprotocol/go-sdk) behind this
adapter, initially pinned to stable `v1.7.0`, with explicit framing/output
bounds and Ox's stricter retry policy. Do not take a prerelease merely to obtain
new features. Protocol revision follows the embedded SDK's negotiation; enabled
capabilities remain deliberately restricted to the specification. LSP uses the
pinned jrpc2 client behind bounded framing and lifecycle adapters. Settings
validates its process definitions once; the manager consumes those immutable
definitions without reinterpreting configuration. Neither adapter introduces a
general plugin runtime or a second agent framework.

Workspace context loaders own bounded instruction/skill discovery. They use the
workspace boundary and return immutable content or metadata to the agent.
Language tools translate positions and synchronize the selected filesystem's
content; they cannot treat the local disk as authoritative when the client owns
an unsaved document. MCP tools retain server identity and are effectful by
default. Changes in server metadata cannot expand an active turn's authority,
and never reach the model-facing tool set or system prompt, which an activation
freezes so the provider's prompt cache survives a server changing underneath a
session. Because MCP can only list a server's whole catalog, a dispatch
validates the selected definition against a bounded-age listing rather than
paying for the catalog on every call.

Web fetch owns public-address HTTP retrieval and text extraction under the
ordinary tool permission path. MCP supplies search, so Ox does not own a search
provider abstraction or additional search credentials. Retrieved web, MCP, and
memory content remains source data; it is never promoted to system authority.

Explicit workspace memory has one private, versioned, atomically replaced store
per canonical root in the Ox data directory, protected by an OS lock for each
read/modify/write operation. Its bounded size permits direct text search. There
is no secondary vector store or asynchronous extractor. Source-session deletion
removes corresponding facts before deleting the session; an interrupted delete
can safely repeat cleanup. Retrieval content enters the session log as a tool
result so later memory changes cannot alter replay.

Git worktrees are workspace inputs supplied by the client. Ox neither retargets
an activated session nor owns merge, rollback, or worktree deletion. This keeps
editor filesystem and terminal callbacks attached to the same root throughout
the session. Worktree acceptance tests cover ordinary working-file separation,
not an unsupported claim of process isolation.

## Workspace boundary

Session creation resolves the requested working directory to one canonical,
listable directory. Every model-facing filesystem path is resolved relative to
that root and rejected if its best canonical location escapes. File operations
use root-confined operating-system handles so the confinement check and access
cannot be separated by a symlink race. Only regular files can be opened as model
context.

The write tool creates or replaces whole text files. Exact edit reads current
content and requires an exact literal match before atomically replacing the
file, preserving its mode and every byte outside the replaced text. File
mutations do not depend on session history. Discovery follows Git ignore rules.
Large tool and shell output is bounded in the conversation and spills to a
confined session directory.

Mutating file tools and shell commands require ACP permission unless the
session's mode authorizes the whole tool set or a previous session grant covers
the operation. Mode is the session's execution policy: it decides whether a call
proceeds, prompts, or is unavailable before any approval mechanics run, and
primary and child loops share that decision. Authorization a mode confers is not
a session grant and outlives nothing but the call it admitted.

Reusable shell grants are derived from a parsed command rather than string
prefixes. Shell commands run from the session root with a sanitized environment,
and cancellation kills their process group. Read, write, and exact-edit file
content uses ACP filesystem callbacks when the client advertises the filesystem
methods and otherwise uses the local executor. Those methods are one capability:
an exact edit must read current content from the same filesystem that receives
its replacement, so a client advertising one without the other is refused at
initialization. Shell commands similarly use ACP terminal callbacks when the
client advertises terminal support and otherwise use Ox's local process-group
runner. Client delegation preserves Ox's validation, permission, output, and
cancellation requirements. The client owns actual execution; ACP callbacks are
not an OS sandbox or a cross-filesystem transaction. Local root-confined handles
cannot prove that a remote client implements its side correctly. Approved shell
commands and external server processes retain host privileges even when launched
from the confined workspace root.

## Testing boundaries

Focused unit tests exercise ACP validation, session folding and storage,
settings, credentials, tool scheduling, workspace confinement, shell process
handling, and provider behavior at their package boundaries. Runtime integration
tests cover tools, permissions, replay, and recovery. Protocol-level ACP
behavior is proved through `internal/e2e`, which builds the real executable and
drives its process, stdio, environment, working directory, and provider
connection.

The mock provider is the normal end-to-end boundary. A real OpenRouter check is
reserved for explicit provider interoperability work and does not replace the
deterministic suite.

## Evaluation boundary

The evaluation adapter is an external ACP client of the shipped executable. Task
fixtures and artifacts belong to the evaluation harness, not production session
semantics. Deterministic process tests prove contracts; on-demand model runs
compare task outcomes under fixed budgets. Neither a benchmark score nor a
reference implementation substitutes for confinement and recovery tests.

The evaluation client uses jrpc2 for calls and callbacks while its channel
records raw events and captures updates in wire order before exposing results.
Its run deadline requests ACP cancellation and permits a bounded grace for the
prompt result rather than cancelling that RPC immediately.

Every run gets private home, configuration, cache, data, and workspace
directories. A local provider gateway applies the run's request budget to all Ox
traffic, including retries and child requests, before forwarding it. The runner
records only nonsecret provider settings and keeps credentials in the child
process environment. Task verifiers, rather than answer text, decide success.
