# Web Client Architecture

This document defines the durable architecture of Ox's first-party web client.
It covers process boundaries, ownership, state, concurrency, and test seams.
`docs/spec.md#web-client` owns observable behavior, and `eng/todo.md` owns the
build order and implementation status. Individual implementation tasks get their
own plans in `eng/plans/`.

The client is an ordinary ACP v1 client. It must not add a private control path
into Ox or make Ox behavior depend on this particular client.

## System boundary

The application has a trusted Bun host process and an untrusted React browser
surface:

```text
phone or desktop browser
    <-> same-origin HTTP and WebSocket
Bun host
    <-> ACP v1 JSON-RPC over stdin/stdout
workspace-scoped Ox process
    <-> OpenRouter, MCP servers, and language servers
```

The Bun host serves the compiled React application, owns all host access, and
stays alive when no browser is connected. The browser never starts processes,
opens arbitrary host paths, handles ACP directly, or becomes authoritative for
live session state. A browser refresh or a second device attaches to state the
host already owns.

The host persists browser-managed canonical roots behind opaque workspace
identities. A workspace supervisor owns one Ox process and one ACP connection
for each active canonical root. Expanding which registry entries remain active
does not change session or transport ownership.

The server listens on loopback by default and accepts an explicit bind address
for use on a trusted network such as Tailscale. It does not provide user
authentication or TLS. Binding it beyond a trusted network is outside the
security model.

## Invariants

- The Bun host is the authority for workspace identity, Ox process state, active
  sessions, pending client callbacks, and the transcript projection. React
  renders host snapshots and submits commands.
- Browser messages name opaque workspace and session identifiers. Every command
  that reaches a supervisor names its workspace, so a concurrent selection
  change cannot redirect it to another Ox process. Only the host maps a
  workspace identifier to a canonical server-local path.
- One Ox process serves all active sessions in one workspace. Different sessions
  may run turns concurrently; mutations within one session are serialized where
  ACP requires it.
- A session belongs to exactly one workspace supervisor for its entire active
  lifetime. Filesystem and terminal callbacks are routed through that same
  workspace.
- The client advertises only capabilities whose complete callback lifecycle it
  implements. Every permission, elicitation, filesystem, and terminal request
  receives one response or a cancellation caused by its owning request or
  connection.
- Multiple browsers observe the same pending permission or elicitation. The
  first valid answer wins; later answers receive a stale-interaction result and
  cannot resolve the ACP callback twice.
- A browser disconnect never cancels a turn. Host shutdown, Ox exit, or an
  explicit user command may cancel work, and the resulting state is shown
  honestly rather than presented as a transparent continuation.
- ACP validation and capability negotiation use the official TypeScript SDK. The
  browser protocol remains a smaller application protocol and does not expose
  raw process handles or arbitrary filesystem operations. Secret inputs are
  write-only commands and are never echoed in results or snapshots.
- Credentials and secret MCP fields remain in the Bun process only as long as
  their operation or activation requires. They are not included in browser
  snapshots, logs, URLs, transcript entries, or ACP metadata.
- No stylesheet, inline style, CSS-in-JS rule, utility CSS framework, or visual
  component library is introduced until the functional v1 gate is complete.
  Feature work uses semantic HTML and native controls so behavior and
  accessibility do not depend on styling.
- Protocol completeness is a host and test responsibility, not the browser's
  information architecture. An ACP method, identifier, capability, or process
  state does not receive a primary control merely because it is implemented.

## Ownership and dependency direction

The client is one Bun package under `client/`, with these coarse boundaries:

- The **host boundary** owns startup configuration, HTTP and WebSocket
  lifecycles, browser command validation, workspace registration, and graceful
  shutdown.
- The **workspace supervisor** owns the Ox child process, its stderr status, the
  initialized ACP connection, negotiated agent capabilities, session routing,
  the active turn for each session, and workspace-scoped client executors.
- The **session controller** owns one session's configuration options, pending
  interactions, and transcript projection. It reduces both live and replayed ACP
  updates through the same path.
- The **filesystem executor** owns confined client-side reads and writes. The
  **terminal executor** owns command processes, output bounds, exit status,
  cancellation, and release. Neither has presentation responsibilities.
- The **shared browser protocol** defines validated commands, results,
  snapshots, and changes. It contains browser-safe values only and depends on no
  Bun, React, process, or ACP transport implementation.
- The **React application** owns navigation, forms, native file selection, and
  rendering. It does not import server modules or derive domain state that the
  host would lose on refresh.
- The **browser harness** owns deterministic fake-provider fixtures and launches
  the compiled application, the real Bun host, and the real Ox binary in
  temporary private directories.

Dependencies point inward from adapters to application state:

```text
React -> shared browser protocol
host -> shared browser protocol -> workspace supervisor
workspace supervisor -> session controller -> ACP SDK
workspace supervisor -> filesystem and terminal executors
browser harness -> public host and browser surfaces -> Ox subprocess
```

React components do not call the ACP SDK. Executors do not publish browser
messages. The transcript reducer does not know whether an update was live,
replayed after `session/load`, or restored into a newly connected browser.

## Host and browser protocol

HTTP serves immutable frontend assets and a small health surface. One
same-origin WebSocket carries browser commands and host state. This keeps prompt
results, streamed updates, permissions, and elicitation on one ordered,
full-duplex connection.

The host rejects WebSocket upgrades whose browser `Origin` does not match the
served application. HTTP has no state-changing route, so an unrelated web page
cannot use the user's browser as a bridge into the trusted host merely because
it can address the Tailscale or loopback endpoint.

On connection, the host sends a complete browser-safe snapshot. Later changes
carry a monotonically increasing host revision. Commands carry request
identifiers and receive one success or error result. A missing revision,
reconnect, or host restart is repaired with another complete snapshot rather
than a browser-authored merge.

The shared protocol uses discriminated JSON unions and validates inbound values
before dispatch. It represents product concepts such as sessions, transcript
entries, pending interactions, and process health. ACP-specific extensibility
and metadata remain at the ACP boundary unless a field has deliberate product
meaning.

Browser commands express product intent. Opening a conversation selects it when
the host already owns its controller and otherwise loads it with replay. The
browser never asks the user to choose between ACP load and resume. Refresh and
pagination are history mechanics rather than peer session operations.

## Workspace and process lifecycle

Startup exposes every registered entry. A root that stopped being usable since
it was registered makes that one workspace unavailable rather than failing
startup, because the browser is where the user removes or restarts it. A
workspace supervisor then launches the configured Ox executable with its
configured arguments, connects the official ACP client over newline-delimited
standard I/O, advertises the supported client capabilities, and initializes the
connection once. Standard error is captured as bounded operational diagnostics;
it is never parsed as protocol state.

The supervisor owns the configured Ox invocation so it can also execute a
terminal authentication method by appending the method's advertised arguments.
After initialization, it automatically attempts Ox's configured
stored-credential authentication method. The browser asks the user to connect
only when no stored credential is available or that automatic attempt fails. The
browser collects a credential with a password control; the host sends it to the
login process on standard input and immediately drops it. The credential never
crosses the ACP connection. A successful login is followed by ordinary ACP
authentication on the current or a replacement connection.

An unexpected Ox exit makes the workspace unavailable and fails outstanding
browser commands and callbacks. The host may start a fresh connection, but it
does not claim that interrupted turns survived. Durable sessions are recovered
only through the standard list, load, and resume methods.

A workspace that has no usable process, because its launch failed or its Ox
exited, is recovered by an explicit restart that replaces the supervisor for the
same registered root. The replacement carries forward the diagnostics that
explain why the previous process stopped, and it recovers conversations only
through that ordinary durable path. Logging out leaves the process running and
clears the stored credential, so it is not a restart case.

The registry stores canonical roots and nonsecret launch configuration. Every
registered entry is active and has an independent supervisor that exists from
registration until removal, including while it is starting and stopping, which
is what makes those two statuses observable. One process failure cannot corrupt
routing or cancel work in another workspace. Selecting a workspace changes which
one the browser sees, not which processes run. Removing an entry stops its
supervisor. Browser-added roots are server-local absolute paths that the host
canonicalizes and validates before registration.

## Session state and concurrency

The host maintains a controller for every active session. A controller folds
updates into stable transcript entries keyed by ACP message and tool-call
identities. Text and thought chunks append in order; tool-call updates merge;
plans and usage replace their current projections; configuration updates replace
the complete option list. Replaying a session uses the same reducer and must not
duplicate entries already present in a host snapshot.

At most one prompt request is active per session, and the host refuses a second
one before it reaches Ox. Different sessions on the same Ox connection prompt
concurrently. Closing, deleting, switching, refreshing, and browser disconnects
do not share implicit cancellation semantics: each invokes only the
corresponding explicit ACP or host operation.

Browser-authored prompt blocks are bounded and checked against the agent's
negotiated prompt capabilities, so an attachment the agent cannot accept is
refused rather than forwarded. Configuration changes are applied through
`session/set_config_option`, and the response replaces the option projection.

Pending permission and elicitation requests belong to their session and tool
call. They remain visible across browser refreshes because the host owns their
resolvers, and the workspace catalog and conversation list report which
workspace and conversation is waiting, so an interaction raised outside the
displayed conversation is still reachable and answerable where it belongs.
Cancellation removes the interaction and lets the ACP request finish with the
cancellation it received. The host never invents an approval or form answer
because a browser disappeared.

Ox owns durable conversation history. The web client persists no competing
transcript. After a Bun restart, the session catalog comes from `session/list`,
and opening a session uses `session/load` to reconstruct its display from Ox's
replay. Browser preferences may be persisted independently, but they cannot be
treated as session truth.

After authentication, the host opens the most recently updated conversation. A
workspace with no durable history gets one new selected conversation. New always
creates and selects a distinct empty Ox session. Choosing history opens the
conversation with replay when inactive and selects it when already active.
History refreshes after lifecycle changes, older pages extend the same list, and
close or delete are secondary actions of the selected conversation.

The Bun host owns the workspace's current client-supplied MCP server set and
uses it for later new and load activations. The browser edits a draft set and
replaces the host set only through an explicit valid action. An incomplete MCP
form therefore cannot affect conversation navigation. Secret header and
environment values remain host-only and never appear in snapshots.

## Product surface

The primary surface is the selected conversation: its transcript, composer,
pending permission or question, history access, and new-conversation action.
Authentication replaces that surface only while user action is required, and a
connection failure appears as an actionable problem rather than a permanent
status dashboard.

The conversation header contains the session's advertised model, mode, and
reasoning controls plus read-only context usage. Attachments and resource links
open from an add-context disclosure in the composer. Plans render only when
present. Tool details and raw output are disclosed beneath their useful activity
title. Resource links render as ordinary safe links.

Workspace settings contain MCP servers and support details. MCP is always named
as MCP; the client introduces no generic integration concept. Host revision, raw
session identifiers, Ox process state, stderr diagnostics, manual history
refresh, and authentication management are support information. Resume has no
user-facing meaning. None of these compete with the transcript or composer.

The client follows negotiated capabilities rather than assuming every ACP agent
matches Ox. Real-process browser scenarios prove the host covers the complete
ACP surface even when a capability has no permanent control. A general editor,
interactive terminal emulator, Git interface, and worktree manager remain
outside this client boundary.

## Filesystem and terminal executors

Ox validates its tool operation before delegating it, but the Bun host still
validates the callback as hostile input. Filesystem requests must carry the
expected active session, use an absolute path within that session's canonical
workspace, and refuse traversal through symbolic links. Reads honor ACP line and
limit semantics; writes create or replace files only within the root and refuse
non-regular targets.

Terminal creation executes the exact program and argument vector without an
extra shell. It validates the working directory against the session workspace,
tracks each process by an unguessable terminal identifier, retains bounded
output according to the request, and keeps exit, kill, output, wait, and release
idempotent where the ACP method permits. Cancellation reaches the whole process
group. Releasing a terminal removes all retained process and output state.

The executors are session-scoped adapters, not a browser-accessible file API or
standalone terminal service. Their behavior is tested through Ox tool calls so
the capability advertisement and the callback implementation cannot drift.

## Testing boundary

Pure Bun tests cover browser-protocol validation, transcript reduction,
controller transitions, callback races, path confinement, and terminal state.
They use direct values and injected process or filesystem seams only where an
operating-system boundary requires one. React behavior is tested primarily
through accessible browser roles and names rather than component internals.

Playwright tests launch a real browser against the real Bun host and a real Ox
binary. A deterministic fake OpenRouter endpoint scripts streamed text,
reasoning, tool calls, usage, failures, and held responses. Each test gets a
temporary workspace, settings tree, session store, credential source, and
process group. The harness waits on observable events instead of wall-clock
delays and records useful browser, host, Ox stderr, and provider-request
artifacts on failure.

The functional v1 gate requires type checking, pure tests, and a real-process
browser matrix covering Ox's complete ACP surface through the product workflows,
including refresh during a live turn, concurrent sessions, cancellation, denied
interactions, replay, and Ox failure. Only after that gate passes may visual CSS
work begin.
