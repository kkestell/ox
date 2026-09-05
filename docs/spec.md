# Ox Specification

This document defines Ox's observable product behavior. It is authoritative for
the ACP connection, sessions and turns, workspace access, authentication, and
failure behavior, including the target behavior of planned capabilities.
`eng/roadmap.md` identifies what remains to be implemented. User guides describe
only shipped behavior; `docs/settings.md` owns the shipped settings reference.

Ox is a coding agent that serves one ACP v1 client over standard input and
output and uses OpenRouter as its model provider. Ox validates request inputs
and prerequisites before accepting an operation. Invalid requests do not enter
conversation history. An accepted operation may fail after producing output or
effects; Ox records that outcome rather than claiming the operation was rolled
back.

## Connection

Ox reads line-delimited JSON-RPC from standard input and writes ACP messages to
standard output. Logs and command diagnostics never appear on standard output.
The process exits successfully when standard input closes.

The client initializes the connection before creating or loading a session. Ox
negotiates ACP v1 capabilities from the client's advertised capabilities. It
uses client filesystem and terminal methods only when the client advertises
them. Missing capabilities do not silently change an operation that requires the
client.

## Sessions and turns

Creating a session returns a new session identifier and binds the session to one
canonical, listable working directory. Activation resolves file-based
configuration, workspace instructions, skills, and executors. Explicit session
configuration changes follow the rules below; each running turn keeps its
original configuration.

A session accepts at most one prompt turn at a time. Separate sessions may run
turns concurrently. Prompt content reaches the model in client order, and Ox
streams reasoning and answer text as ACP updates before returning the prompt
response.

Loading a durable session restores the conversation the model would have seen
and replays its recorded ACP updates. Loading may continue a turn that was
durably waiting for permission. A refused prompt does not enter conversation
history.

Cancelling a session stops its active provider request, permission wait, and
running tools. Output already streamed to the client remains part of the
session. Cancelling an idle or unknown session has no effect.

## Workspace operations

Built-in file-tool paths are confined to the session workspace. Ox rejects
parent traversal, absolute paths, and symlink traversal when they would escape
the workspace. Model-facing reads open only regular files.

Ox requires evidence from an earlier read, glob, or search before a model may
change an existing file. File changes and shell commands require client
permission unless the session already holds a matching grant. Delegating an
operation to a capable ACP client preserves the same confinement, evidence,
permission, output, and cancellation behavior as local execution.

## Authentication

Ox requires an OpenRouter credential before it creates or loads a session.
`authenticate` verifies the selected authentication method and credential before
enabling session work. `ox login` verifies a credential before replacing the
stored key. Credential precedence and configuration locations are defined in
`docs/settings.md`.

Ox never deliberately includes resolved credentials in ACP output, logs, traces,
or session records. Arbitrary user or tool content can itself contain secrets;
durable conversation history is private data, not a sanitized trace.

## Diagnostic trace

Ox emits no diagnostic trace by default. Starting Ox with `--trace <path>`
enables a machine-readable trace at that path for the lifetime of the process.
Ox replaces an existing file at that path, so one trace contains one process
run. The trace never appears on standard output.

Ox refuses to start if it cannot create the requested trace. If a write fails
after startup, Ox reports the failure once on standard error, disables tracing,
and continues serving the client.

Each trace line is one complete JSON object. For every accepted turn, the trace
records the turn lifecycle, provider requests, tool calls, permission requests
and outcomes, and the final stop reason. Records identify the session and, when
applicable, the tool call. They include only event kinds, identifiers, timings,
sizes, and outcomes. They never include credentials, prompt text, model output,
file content, or shell output.

## Errors

Malformed JSON and invalid JSON-RPC requests receive the corresponding JSON-RPC
error when a response is possible. An unknown method receives a method-not-found
error. Invalid ACP parameters, unavailable sessions, invalid configuration,
unusable workspaces, missing credentials, provider failures, and unsupported
client operations produce errors that identify the rejected operation or source.

One failed request does not prevent the connection or unrelated sessions from
continuing. Ox sends no response to notifications, including cancellation.

## Session lifecycle and recovery

`session/list` returns durable sessions without activating them. `session/load`
activates and replays; `session/resume` activates without replay and does not
silently execute an interrupted turn. `session/close` cancels and waits for
active work, releases activation resources, and preserves history.
`session/delete` removes an inactive session's history and owned spill
artifacts; it never deletes workspace files or Git worktrees. Loading an already
active session and deleting an active session fail rather than racing its owner.

A second prompt to a busy session is rejected without accepting its content. To
redirect work, the client cancels, waits for the prompt response, and sends a
new prompt. Ox does not accept a hidden input queue, a steering extension, or a
prompt whose response means only that input was enqueued.

Replay never executes a tool. Pending permission recovery may dispatch only
calls whose execution has not begun. A call interrupted after dispatch has an
unknown outcome until inspected; Ox never automatically repeats a shell, MCP, or
file mutation merely because its completion record is absent. External side
effects and the session log cannot be committed atomically.

Failure to persist an accepted operation stops further work in that session and
fails its owning request. It must not produce a successful acknowledgement for
unrecorded state. Other sessions continue when their storage remains usable.

## Session configuration

Ox exposes select-valued ACP configuration options in this order:

| Identifier  | Values and default                                                       | Effect                                                                  |
| ----------- | ------------------------------------------------------------------------ | ----------------------------------------------------------------------- |
| `mode`      | `code` (default), `plan`                                                 | Chooses the available tool set.                                         |
| `model`     | Validated OpenRouter model IDs; activation's configured model by default | Chooses the model for subsequent turns.                                 |
| `reasoning` | `default`, plus efforts supported by the selected model                  | `default` uses the model's provider default without an explicit effort. |

A configured explicit reasoning effort is the initial selection. Models without
selectable reasoning omit that option. A model change resets reasoning to
`default` and rejects incompatible explicit sampling, output-limit, tool, or
modality settings rather than silently dropping them. Retained conversation
content must be usable by the new model; otherwise the change is rejected.

`code` uses the ordinary permission-gated tools. `plan` permits file discovery
and reads, instructions and skills, todo, form questions, and read-only LSP
queries. It excludes shell, file mutations, delegation, memory writes, and MCP
tools whose effects Ox cannot enforce. Web fetch retains its permission gate.
Changing mode never grants permissions or widens an existing grant.

`session/set_config_option` validates and persists the entire resulting state
before responding with the complete option list and emitting
`config_option_update`. Changes accepted during a turn take effect on the next
turn. Selecting `plan` does not stop an already running `code` turn;
cancellation is required for that. Concurrent setters are serialized in accepted
order. Selecting the current value is a no-op.

Explicit selections persist across close, load, and resume and override
activation defaults. Files are reread on activation; incompatible saved
selections fail activation with the responsible option named. Replayed option
history is followed by the current complete state. Tools, instructions, and
model settings in a recovered permission wait remain those of its original turn;
a changed required tool definition rejects recovery before dispatch.

Ox uses configuration options as its only mode interface. It does not advertise
legacy `modes` or implement `session/set_mode`. This follows ACP's preferred
[configuration interface](https://agentclientprotocol.com/protocol/v1/session-config-options)
and avoids two writable representations of the same state.

## Process configuration transition

Process settings belong in the global settings file's `process` object. Its
fields are `log_level` (default `info`), `openrouter_base_url` (default the
OpenRouter API), and optional `trace`. Workspace files cannot set this object.
Language-server process fields are defined under language intelligence below.
The corresponding CLI overrides are `--log-level`, `--openrouter-base-url`, and
`--trace`. `--model` overrides the activation's file-based model default;
explicit durable session selections still take precedence. Invalid flags or
process settings fail startup. A process endpoint override does not travel in a
workspace file or become a model-facing setting.

The shipped process stops reading `OX_*` and `OPENROUTER_*` variables. Standard
home, XDG, PATH, and platform environment variables retain their usual purpose.
Credentials come from the OS keyring unless `--credential-file <path>` supplies
an explicit, owner-only regular file with one nonempty credential. That file is
read at startup, never recorded, and its credential cannot be replaced by
`login` or cleared by `logout`. `--no-keyring` disables keyring access and
leaves credential-file authentication available. `login` reads the credential
from standard input and verifies it before storing it; credentials are never
command arguments or ordinary JSON settings.

The process and browser harnesses use these same public flags and temporary
credential files. An explicitly requested live test reads `.env` in the harness
and uses the existing repository-mandated model. Ox itself does not load `.env`.

## Context continuity

Before each provider request, including tool continuations and child requests,
Ox budgets the full request plus the requested output against the selected
model's context window. Estimates include the pending prompt, instructions, tool
schemas, and multimodal content. When exact token counts are unavailable, Ox
uses conservative estimates and does not claim exact occupancy.

Compaction preserves the system instructions, the first user request, and
complete recent assistant/tool groups. It never separates a tool call from its
result or starts while a tool batch is unresolved. Todo state and configuration
remain explicit. The original transcript remains available for ACP replay. A
summary becomes effective only after durable persistence; failure or an empty
summary preserves the previous context. An oversized protected prefix or
irreducible recent group produces an actionable context-limit error without an
unbounded retry loop. Recovery reconstructs the same provider context at the
same boundary, including within a long turn or delegation.

## Workspace instructions and skills

Ox loads only the session root's `AGENTS.md` automatically. A missing file adds
nothing; an unreadable, non-regular, invalid UTF-8, symlinked, or larger than 64
KiB file fails activation with its path. Parent and nested instruction files are
not automatically discovered. Root instructions apply to parent and child agents
and cannot expand permissions or override the user's current request. Their
bytes are fixed until reactivation and retained for reproducible history.

Skills are discovered in `<workspace>/.agents/skills/<name>/SKILL.md` only.
User-wide skill roots are intentionally excluded to preserve the workspace
boundary. Discovery follows the
[Agent Skills format](https://agentskills.io/specification), validates required
metadata, sorts by name, and excludes symlink traversal. Malformed individual
skills are reported by path and skipped. An unreadable skills directory fails
activation rather than looking empty.

At most 128 valid skills and 64 KiB of rendered catalog metadata are accepted;
an oversized catalog fails activation. Each skill file is limited to 64 KiB.
Only name, description, and workspace-relative location enter the initial
prompt. The model loads a body by name through a skill tool; referenced files
are read separately through confined file tools. `allowed-tools` metadata is
advisory and never grants execution permission. Skill scripts run only through
the ordinary shell permission path. Skills cannot silently install packages.

The catalog and file identity are fixed at activation. A changed body fails a
load with a reactivation message rather than pairing old metadata with new
instructions. Loaded content enters durable tool history. Compaction may
summarize it; loading it again restores the same validated body. Skill content
has the same instruction priority as workspace instructions.

## Todo and questions

The todo tool atomically replaces the full ordered list. Each entry has nonempty
content, a priority (`high`, `medium`, or `low`, default `medium`), and status
(`pending`, `in_progress`, or `completed`). At most one entry is in progress; an
empty list clears it. Invalid input leaves the old list unchanged. Each accepted
change is persisted before one ACP `plan` update, and the current list is
retained across compaction, replay, and restart. Child agents cannot replace the
parent's plan. A todo list describes progress; it does not schedule work.

A model-facing question tool uses ACP form elicitation only when the client
advertises form support. It asks one question per call, accepting a string or a
single selection from labeled choices. Accepted values are schema-validated;
declined and cancelled answers remain distinct tool outcomes. A default is not
an answer. Answers are persisted before model continuation. Questions never
request credentials and do not stand in for execution permission.

Without form support the tool is absent; the agent can ask in its final text and
wait for another prompt. Cancelling ends the outstanding form request; restart
marks an unanswered question interrupted and does not reissue it. URL
elicitation and `elicitation/complete` are outside this product scope.

## MCP tools

Ox accepts only client-supplied `mcpServers` on activation. It supports stdio
and Streamable HTTP using protocol revision `2026-07-28`, advertises only HTTP
in `mcpCapabilities`, and rejects older revisions and legacy HTTP+SSE. These are
the selected
[MCP transports](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports).
Workspace files cannot start additional servers. Client-configured HTTP servers
may be local; remote endpoints require HTTPS and credentials are never forwarded
across a redirect to another origin.

Activation validates every definition, starts or connects to the servers, uses
`server/discover`, and lists their tools before returning success. A failure
names the server and closes all resources started by that attempt.
Initialization and discovery have a 30-second deadline per server. Duplicate
server names, tool-name collisions, invalid schemas, and a combined catalog
above 256 tools or 256 KiB fail activation. Tool names are deterministic and
namespaced; the original server and tool identity remain visible in ACP details.

Every MCP tool call requires permission unless a session grant covers that
specific server, tool, and unchanged definition. Server annotations are hints,
not authority to bypass permissions or enable a tool in `plan` mode. Calls are
serialized with other potentially mutating work in the same session. External
servers are not sandboxed by the workspace root.

Tool schemas are frozen for an activation. Ox does not subscribe to catalog
changes; unavailable tools fail by name and new definitions require
reactivation. Before dispatch Ox refreshes the selected definition and rejects a
change rather than executing it under an old grant. Calls have a 120-second
deadline. Cancellation sends a notification on stdio and closes the request
response stream on HTTP, following the selected transport. These checks cannot
prove that an external server implements the behavior its schema describes.
Transport failure fails the tool without retrying a possibly executed call. Text
and structured JSON results are bounded to a 1 MiB accepted payload and 64 KiB
inline, with truncation explicit and accepted overflow stored as a spill.
Unsupported content types produce a useful tool error.

Server command environments and HTTP headers remain in activation memory; secret
values are never persisted or exposed to the model. Restart requires fresh
server definitions and credentials from the client. Ox stores only the nonsecret
identity and schema evidence needed to validate recovery. Changing credentials
does not itself invalidate a tool grant; changing the destination, command, or
schema does. Server shutdown follows session close and process exit. MCP
sampling, resources, prompts, roots, OAuth flows, and multi-round-trip user
input are not enabled. A tool requiring an unsupported input interaction fails
clearly; Ox does not replay its request to fake a response.

## Language intelligence

Language-server tools provide definitions, references, document/workspace
symbols, and diagnostics. Servers are explicitly configured in global settings
under `process.language_servers`: a map of names to `command`, `args`, and
`extensions`. Commands are resolved from the process PATH and run in the session
root. Workspace configuration cannot select executables, and Ox never downloads
or installs a language server automatically.

Servers start lazily per activation with a 30-second initialization deadline;
queries time out after 10 seconds. A missing or crashed server fails the tool
and does not restart automatically during that activation. Server resources
close with the session. Results use confined workspace-relative paths and
one-based model-facing positions, with protocol position encoding translated
correctly. Out-of-root locations are omitted with an explicit omission count.

Queries synchronize the current target file before use. Client-backed file
content is authoritative when that executor is selected. Diagnostics identify
the observed document version; stale, timed-out, or unavailable diagnostics are
never reported as a clean result. Ox does not assert that an empty push means a
server has finished analyzing the file. Diagnostics are requested explicitly,
not automatically after every edit. Renames, formatting, code actions, and IDE
side channels are excluded.

## Web access

Built-in web fetch accepts public HTTP(S) URLs and asks permission before
network access. It rejects URL credentials and nonpublic destinations, including
on redirects and DNS resolution at connection time. It sends no workspace
cookies, provider credentials, or ambient authorization headers. Redirects are
limited to five, requests to 30 seconds, and decoded response bodies to 2 MiB;
exceeding a limit fails explicitly. HTML, plain text, and JSON are supported;
binary formats and JavaScript execution are not.

A result identifies requested and final URLs and includes extracted text marked
as untrusted source content. Inline output is capped at 64 KiB; accepted
overflow spills through the ordinary output path. Source text never authorizes
tools or becomes workspace instructions. Answers based on fetched material link
to the source URL, distinguish inference from quoted evidence, and never
describe an unfetched search snippet as a verified page.

Web search is supplied through a client-configured MCP search server. Ox does
not embed a search vendor, scrape search result pages, or acquire another API
credential. Without a search server, fetch remains available and the agent
reports that search is unavailable when needed.

## Isolation, memory, and delegated work

An ACP session uses exactly the working directory supplied by the client.
Isolation is provided by creating a Git worktree before `session/new` and
passing that path as `cwd`. Ox does not silently create or switch worktrees,
merge branches, roll back a checkout, or discard it on session deletion. The
client or user owns that lifecycle. File tools enforce confinement; approved
shells, language servers, and MCP servers run with host privileges. Ox is not an
OS sandbox, and worktrees do not isolate network access or shared Git metadata.

The exact-edit tool remains the single edit primitive. Anchored editing is an
evaluation candidate and replaces exact editing only after the roadmap's
comparative gate passes. It never weakens stale-read rejection, confinement,
permission previews, or preservation of file format.

Workspace memory is explicit and opt-in through memory tools. It stores typed
facts (`preference`, `decision`, `finding`) with source session, creation time,
expiry, and optional superseded identity. Writes, deletion, and supersession
require permission. Memory is scoped to the canonical workspace root; separate
worktrees do not silently share it. Stored memories are data and do not override
user requests or workspace instructions. There is no automatic transcript
extraction or remote embedding request.

Retention is at most 256 active facts, 2 KiB per fact, and 30 days from
creation. Reads do not extend expiry. Supersession and expiry remove facts from
retrieval; at capacity an insertion fails until facts are removed. A model must
explicitly search memory. Results match all whitespace-separated query terms
case-insensitively, sort newest first with stable identity as the tie-breaker,
and return at most ten facts and 8 KiB. Retrieved content is recorded in durable
tool history. An empty query lists the newest active facts. Deleting a source
session removes its facts from future retrieval; previous transcripts are
unchanged. Semantic retrieval is an evaluation candidate, not a dependency of
this behavior.

Task decomposition uses an explicit durable queue under an active parent turn.
The parent may add, inspect, cancel, and explicitly retry tasks. Tasks have
stable IDs, a description, and states `pending`, `running`, `completed`,
`failed`, `cancelled`, or `interrupted`. At most 32 tasks are retained and one
child runs at a time. The child has no delegation or queue-management tools; its
activity is nested under the parent's ACP tool call. Parent and child share the
turn's permission policy, context limits, and request budget.

A dispatch is durable before execution and completion is durable before being
reported. Completed tasks are never redispatched. Restart marks running tasks
interrupted; retry requires an explicit user request and creates a new attempt
whose earlier effects remain visible. Cancellation stops the active child and
leaves pending work paused. Nothing runs after the owning ACP request ends; a
new prompt may explicitly resume pending tasks. The queue promises no
exactly-once execution of external effects.
