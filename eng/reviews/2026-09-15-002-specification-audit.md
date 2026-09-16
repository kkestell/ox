# Specification audit

- **Scope:** every claim in `docs/spec.md` (557 lines), verified against the Go
  and TypeScript sources, the focused tests, and the documents that share its
  subject (`docs/settings.md`, `docs/browser.md`, `docs/web.md`,
  `docs/installation.md`, `docs/acp-extensions.md`, `eng/architecture.md`,
  `eng/client-architecture.md`, `eng/todo.md`).
- **Revision audited:** working tree at commit `a005855` with `docs/spec.md` =
  `c7a8c00e2e7bccd930c583922376786caa4d38f3f8c06bc3e4dcf43ed5e5f6f0` (557 lines,
  uncommitted). All line numbers below refer to that revision and were
  re-checked after the audit.
- **Movement during the audit:** the workspace changed while this audit ran.
  `docs/spec.md` lines 68-79 were rewritten (cost, cache hit rate, collapsed
  reasoning and tool output) and `eng/todo.md` was replaced by a bare `# TODO`
  between the first read and the last. Findings below were re-verified against
  the pinned revision, not the earlier one.
- **Coverage:** all 18 sections, roughly 300 individual claims and claim groups
  verified line by line in eight passes. 21 findings: one implementation defect
  where the spec is right and must stay, six P2 problems, thirteen P3 wording or
  consistency problems, and one precision note. Every other claim checked out;
  the section table below records where the coverage landed.

## Method

Each section was verified claim by claim against the owning code, with
`file:line` evidence for every verdict, then the contradicting or unverifiable
claims were re-checked directly by the audit author. Quantitative claims
(deadlines, byte bounds, counts, ratios) were read from the constants, not
inferred. The three external links in the spec were fetched and read. Known
behavior that the spec does not state was recorded separately rather than
counted as an inaccuracy.

## Findings

### P1 — a client-cancelled turn acknowledges state that was never persisted

- **Spec:** 271-272 — "Failure to persist an accepted operation stops further
  work in that session and fails its owning request. It must not produce a
  successful acknowledgement for unrecorded state."
- **Verdict:** the spec is correct; the implementation violates it.
- **Evidence:** `internal/agent/agent.go:1541-1560`. The `switch` tests
  `cancelledByClient` before `result.err`, so a run that failed with a
  persistence error still returns a normal `session/prompt` response whenever
  the client had cancelled. The comment on that branch ("whatever the run
  reported is replaced rather than returned") explains replacing the stop
  reason, not discarding an error.
- **Reproduction:** a throwaway overlay test outside the repository
  (`go test -overlay /tmp/oxaudit/overlay.json -run TestAudit... -v ./internal/agent`)
  makes the session log unwritable during a cancelled turn. Result:
  `session/prompt error = <nil>` with `StopReason:"cancelled"` although the
  terminal record was never written. The identical failure without a cancel
  returns
  `-32098 persist model exchange: append session record: ... file
  already closed`.
  The turn is then indistinguishable from a durable cancellation, and a later
  load sees an interrupted turn with no recorded outcome.
- **Fix:** in the `cancelledByClient` case, check `result.err` first (or return
  a cancellation stop reason only when `result.err == nil`), and cover it with a
  test that cancels while the store is failing. The spec wording can stay as it
  is; if the deliberate behavior is to keep the response successful, the spec
  must instead state that exception, which contradicts the persistence rule
  above it.

### P2 — reads are not confined to the session workspace

- **Spec:** 163-164 — "Built-in file-tool paths are confined to the session
  workspace."
- **Verdict:** inaccurate. Reads and discovery also reach the session spill
  directory, which lives outside the workspace.
- **Evidence:** `internal/tools/read.go:56`, `write.go:49`, `edit.go:65`,
  `glob.go:51`, and `grep.go:83` all build
  `workspace.NewWorkspace(root).WithReadable(invocation.SpillDir)`;
  `internal/workspace/workspace.go:278-280` adds those roots to every read and
  walk; `internal/agent/store.go:243-244` places the spill directory at
  `<data root>/<session id>.spill`. Spill footers hand the model the absolute
  path (`internal/workspace/spill.go:52`), which is how `docs/web.md` tells
  users to read large results back with `read_file`. Mutation stays
  workspace-only because `Workspace.Key` (used by `write_file`/`edit_file`)
  refuses any path inside a readable root
  (`internal/workspace/workspace.go:237-261`).
- **Fix (spec):** "Built-in file-tool reads are confined to the session
  workspace and the session's own spill directory, whose absolute paths appear
  in truncated tool output; mutations are confined to the workspace. Ox rejects
  parent traversal, absolute paths, and symlink traversal when they would escape
  those roots."

### P2 — the MCP permission rule contradicts the mode rules

- **Spec:** 453-455 — "Every MCP tool call requires permission unless a session
  grant covers that specific server, tool, and unchanged definition."
- **Verdict:** inaccurate. It omits `auto` mode, and read literally it
  contradicts 297-298 ("`auto` ... runs every one of its calls, including a
  child agent's, without a permission request").
- **Evidence:** `internal/agent/approval.go:26-33` — `authorized` returns true
  for `mode == modeAuto` before any grant lookup; MCP tools are registered with
  `Approval: ApprovalAsk` (`internal/agent/mcp.go:40-46`), so `auto` is exactly
  the case that skips the prompt. Pinned by
  `internal/agent/approval_test.go:305-349`. (Plan mode never reaches this rule
  at all: no MCP tool is offered there, see the P3 finding on 301-302.)
- **Fix (spec):** "Every MCP tool call requires permission unless the turn's
  mode authorizes it, as `auto` does for every call, or a session grant covers
  that specific server, tool, and unchanged definition."

### P2 — an MCP activation failure does not always name the server

- **Spec:** 443-444 — "A failure names the server and closes all resources
  started by that attempt."
- **Verdict:** inaccurate for the naming half; the cleanup half is correct.
- **Evidence:** connection and discovery failures do name the server
  (`internal/mcp/mcp.go:252-255`, `:257-261`), and resources are closed on every
  failure path (`internal/mcp/mcp.go:136-155`, proven by
  `internal/e2e/mcp_test.go:527-575`). The combined-catalog failures return
  `"MCP catalog exceeds 256 tools"` (`:144-147`),
  `"MCP provider tool name %q is
  not unique"` (`:148-151`), and the 256 KiB
  size error (`:152-155`, `:286-288`) with no server attached, and definition
  validation names only the item index (`internal/acp/mcp.go:168-169`).
- **Fix (spec):** "A failure closes all resources started by that attempt and
  identifies the server for connection, discovery, and per-server definition
  failures; a combined-catalog limit violation names the limit instead."
  Alternatively, wrap the catalog errors with the owning server name in
  `internal/mcp/mcp.go` and keep the sentence.

### P2 — cancelling does not cancel the outstanding form request

- **Spec:** 427 — "Cancelling ends the outstanding form request."
- **Verdict:** inaccurate. Ox stops waiting; it sends the client nothing.
- **Evidence:** elicitation is a server-to-client request
  (`internal/agent/agent.go:594-607`). `jrpc2`'s `waitCallback` only fails the
  local wait when the context ends
  (`github.com/creachadair/jrpc2@v1.3.5/server.go:440-472`); Ox's only
  `$/cancel_request` code is the inbound handler (`internal/agent/agent.go:189`,
  `:1739-1747`), and its only outbound notifications are `session/update`
  (`:447`, `:492`, `:1503`). The question call then ends as the generic failed
  result `tool call cancelled` (`internal/agent/loop.go:1227-1233`), not as the
  tool's own `{"outcome":"cancelled"}`. The ACP elicitation page defines
  `accept`, `decline`, and `cancel` responses but no agent-side request
  cancellation (fetched from
  https://agentclientprotocol.com/protocol/v1/elicitation); the ACP cancellation
  page offers the optional `$/cancel_request` notification for exactly this
  purpose (https://agentclientprotocol.com/protocol/v1/cancellation), which Ox
  does not send.
- **Fix (choose one):** send `$/cancel_request` for the elicitation request ID,
  or reword to "Cancelling stops Ox waiting on the outstanding form request and
  ends that question call; the client's own control dismisses the form it is
  showing."

### P2 — the web client does not list conversations for a workspace that has no process

- **Spec:** 55-56 — "The client shows recent conversations for every registered
  workspace, not only the one it is displaying."
- **Verdict:** inaccurate for a workspace that is not ready.
- **Evidence:** `client/src/components/sidebar.tsx:133` renders the conversation
  list only when `workspace.status === "ready"`; otherwise `:186-199` renders
  "Ox is starting…" / "Ox is not running." with a Restart button. Asserted by
  `client/e2e/smoke.spec.ts:643-644` and stated in
  `eng/client-architecture.md:265-266` ("A workspace with no usable process
  offers its restart there instead of its conversations").
- **Fix (spec):** "The client shows recent conversations for every registered
  workspace that has a running Ox process, not only the one it is displaying; a
  workspace whose process is not running offers its restart in the same place."

### P2 — history refresh contradicts the spec's own support-details rule

- **Spec:** 57-58 — "History refresh and pagination are part of the displayed
  workspace's conversation list", against 92-93 — "Process status, stderr, host
  revisions, raw session identifiers, and manual refresh are support details
  rather than primary workflow."
- **Verdict:** the two sentences describe the same control differently. Only
  pagination is in the list.
- **Evidence:** "Show older conversations" is rendered inside the conversation
  list (`client/src/components/sidebar.tsx:180-185`); "Refresh conversation
  history" is a button in support details
  (`client/src/components/support-details.tsx:56`, wired at
  `client/src/components/app.tsx:251-255`).
- **Fix (spec):** "Pagination belongs to the displayed workspace's conversation
  list, while refresh, close, and delete are secondary actions."
  `eng/client-architecture.md` is consistent with that wording.

### P3 — the trace record enumeration is incomplete

- **Spec:** 232-233 — "They include only event kinds, identifiers, timings,
  sizes, and outcomes."
- **Verdict:** inaccurate as an exhaustive list. The record schema also carries
  `version`, `provider_kind`, `tool_name`, `request_count`, and
  `input_tokens`/`output_tokens`/`reasoning_tokens`
  (`internal/trace/trace.go:219-239`, populated at `:150-170`; asserted by
  `internal/e2e/trace_test.go:98-99`). The privacy guarantee in the following
  sentence is accurate: no content field exists.
- **Fix (spec):** "They include only event kinds, identifiers, classifications,
  timings, sizes, token counts, and outcomes."

### P3 — the trace has no stop reason for failed or interrupted turns

- **Spec:** 231 — "For every accepted turn, the trace records ... the final stop
  reason."
- **Verdict:** imprecise. Failed and interrupted turns pass an empty stop reason
  (`internal/agent/loop.go:435-459`, `:478`), so the record carries the outcome
  but no stop reason for them.
- **Fix (spec):** "...and the turn outcome, with the ACP stop reason when the
  turn has one."

### P3 — "In either mode" with three modes defined

- **Spec:** 129 — "In either mode, the primary agent may start a child..."
- **Verdict:** stale wording. Subagent coordination tools are `PlanMode: true`
  with no plan exclusion (`internal/tools/subagent.go:101-137`), so children can
  start in `code`, `auto`, and `plan`; a child under `plan` is re-constrained to
  the plan tool set (`internal/agent/subagent.go:92-96`).
- **Fix (spec):** "In every mode, the primary agent may start a child on a
  complete standalone task..."

### P3 — plan excludes every MCP tool, not a subset

- **Spec:** 301-302 — "It excludes shell, file mutations, memory writes, and MCP
  tools whose effects Ox cannot enforce."
- **Verdict:** the qualifier implies a filter that does not exist. Every MCP
  tool is `Kind: acp.ToolKindOther` with no `PlanMode`
  (`internal/agent/mcp.go:40-46`), and the plan set keeps only tools that are
  `PlanMode` or read/search kinds (`internal/agent/agent.go:1133-1140`), so a
  server annotation cannot make one available
  (`internal/e2e/mcp_test.go:162-171`). `memory_search` is in the plan set but
  is not named among the permitted tools; the exclusion sentence covers it.
- **Fix (spec):** "It excludes shell, file mutations, memory writes, and every
  MCP tool."

### P3 — `authenticate` is not what gates session work

- **Spec:** 208-209 — "`authenticate` verifies the selected authentication
  method and credential before enabling session work."
- **Verdict:** imprecise. Session creation and loading are gated on a resolved,
  non-rejected credential (`internal/agent/agent.go:1420-1445`, `:942-951`), not
  on an `authenticate` call; `authenticate` refreshes and verifies the
  credential and clears a recorded provider rejection (`:268-318`). A client
  that never calls `authenticate` can create sessions.
- **Fix (spec):** "Ox requires a resolved, non-rejected credential before it
  creates or loads a session. `authenticate` verifies the selected method and
  the resolved credential and clears a recorded provider rejection."

### P3 — nothing enforces initialization before session creation

- **Spec:** 29 — "The client initializes the connection before creating or
  loading a session."
- **Verdict:** unenforced as a server rule. `NewSession`, `LoadSession`, and
  `ResumeSession` never check that `initialize` ran
  (`internal/agent/agent.go:337-379`, `:611-632`, `:638-652`), and a session
  created before initialization silently receives no client filesystem or
  terminal executors.
- **Fix:** either reject session methods before `initialize`, or reword to "The
  client initializes the connection before creating or loading a session, which
  is when Ox learns the client capabilities that activation uses."

### P3 — activation failure and replayed option state are described as client-visible

- **Spec:** 318-320 — "incompatible saved selections fail activation with the
  responsible option named. Replayed option history is followed by the current
  complete state."
- **Verdict:** both are true in effect but not in the form the sentence
  suggests. Activation failures wrap the settings error, which names the model
  or selection but not the ACP option id; only `session/set_config_option`
  prefixes the option id (`internal/agent/config_options.go:104-106` against
  `internal/agent/agent.go:716-721`). Replay emits the recorded per-change
  updates and stops (`internal/agent/state.go:1549-1708`); the current complete
  state arrives in the `session/load`/`session/resume` response
  (`internal/agent/agent.go:433-462`, `:612-629`), not as a trailing
  notification.
- **Fix (spec):** "Files are reread on activation; incompatible saved selections
  fail activation with the selection and model named, and a setter rejects them
  by option id. Replay sends the recorded option updates and the load response
  carries the current complete state."

### P3 — a Markdown image with a non-HTTP source renders nothing

- **Spec:** 77-78 — "an image displays its alt text or source URL instead of
  loading".
- **Verdict:** true for HTTP(S) sources. `urlTransform` drops anything that is
  not `http:`/`https:` (`client/src/components/markdown.tsx:26-31`), so a
  relative or `data:` image with no alt text renders an empty span (`:13`),
  showing neither alt text nor URL.
- **Fix (spec):** "an image displays its alt text, or its URL when the source is
  HTTP or HTTPS, instead of loading."

### P3 — "the complete session lifecycle" overstates the browser workflows

- **Spec:** 88-91 — the enumerated feature list includes "the complete session
  lifecycle".
- **Verdict:** `session/resume` is implemented in the host but has no product
  workflow: the only caller outside tests is
  `client/src/workspace-supervisor.ts:256-257`, and nothing in
  `client/src/protocol.ts` or a component invokes it, so opening history always
  loads. The spec's own next sentence ("users do not choose between ACP load and
  resume") is accurate.
- **Fix (spec):** "the complete session lifecycle except ACP resume, which the
  host supports but no browser workflow selects because opening history always
  loads".

### P3 — MCP identity in ACP details is lossy

- **Spec:** 447-448 — "Tool names are deterministic and namespaced; the original
  server and tool identity remain visible in ACP details."
- **Verdict:** partially accurate. The provider name is `mcp__<server>__<tool>`
  after sanitizing and truncation (`internal/mcp/mcp.go:381-404`), and that
  generated string is the ACP `name` (`internal/agent/loop.go:964`). The
  readable server and original tool appear only when the server supplies no
  title, through the fallback title (`internal/agent/mcp.go:82-87`). Long or
  non-alphanumeric identities are mangled, so the ACP details do not always
  carry the original identity.
- **Fix (spec):** "Tool names are deterministic and namespaced from the server
  and tool identity. The generated provider name sanitizes and truncates the
  identity, so ACP details carry the readable identity only through the activity
  title and, when the server supplies no title, the fallback title."

### P3 — the process settings reference is duplicated

- **Spec:** 331-340 restates the `process` fields, defaults, and CLI overrides
  that `docs/settings.md` owns, while 6-7 delegates "the shipped settings
  reference" to that file.
- **Verdict:** two homes for the same facts; a default changed in one place can
  silently disagree with the other. The architecture already treats the
  transition as a spec fact and defers the shipped reference
  (`eng/architecture.md:295-299`).
- **Fix (spec):** keep the transition's behavior (workspace files cannot set
  `process`, the shipped process reads no `OX_*`/`OPENROUTER_*` variables,
  credential-file and keyring rules) and reference `docs/settings.md` for the
  field list, defaults, and flag spellings.

### P3 — the introduction's scope list is narrower than the document

- **Spec:** 3-5 — "It is authoritative for the ACP connection, sessions and
  turns, workspace access, authentication, and failure behavior..."
- **Verdict:** the list omits the web client, process configuration, context
  continuity, workspace instructions and skills, todo and questions, MCP tools,
  language intelligence, web access, and isolation and memory — all of which the
  document defines.
- **Fix (spec):** "It is authoritative for Ox's ACP-visible behavior and the
  agent-side rules for configuration, context, tools, web access, and memory..."

### P3 — "exiting the process" cancels and waits only on orderly shutdown

- **Spec:** 156-158 — "Finishing or cancelling the primary turn, closing the
  session, losing the ACP request, or exiting the process cancels and waits for
  every live child."
- **Verdict:** accurate for the orderly path (stdin close → `Agent.Close` →
  per-session close joins the group, `internal/agent/agent.go:923-938`) and for
  the other four triggers. The server installs no signal handler (`os/signal`
  appears only in `cmd/ox/login.go:70`), so a signal terminates the process
  without the cancel-and-wait step.
- **Fix (spec):** "or an orderly process shutdown cancels and waits for every
  live child."

### Note — occupancy estimates are published as a definite `used`

- **Spec:** 362-366 — "uses conservative estimates and does not claim exact
  occupancy. Occupancy is always a token count: Ox derives one from the
  serialized request at a fixed, deliberately low bytes-per-token ratio, and
  replaces it with the provider's reported prompt tokens..."
- **Verdict:** accurate; the compaction path records an estimate that a client
  then renders as `used`. Worth one clause so a client author knows a `used`
  value may be an estimate rather than a provider measurement.

## Related documents with the same defect

These are outside `docs/spec.md` but were found while cross-checking it:

- `docs/web.md:5-6` — "Every fetch requires permission unless the session
  already has a matching grant." False in `auto` mode for the same reason as the
  MCP sentence above (`internal/agent/approval.go:26-33`). Worth the same mode
  exception.
- `docs/settings.md:128` — "Put flags before the optional `login` command."
  `ox --trace <path> login` parses `--trace` and then returns from the login
  branch before the tracer is opened (`cmd/ox/main.go:112-129`), so the trace is
  silently never created. Either document that process flags other than the
  credential settings do not apply to `login`, or reject them there.
- `docs/acp-extensions.md` (untracked) and the spec both state the
  `kkestell.ox/toolDisplayName` / `toolDisplayArguments` contract. The extension
  page is the natural home for key-level detail; the spec sentence can point at
  it.
- `eng/todo.md` was emptied during this audit. With no roadmap entries, nothing
  in the repository distinguishes shipped behavior from planned behavior for a
  reader of the spec. The audit independently confirmed the spec's Markdown
  claim now matches the client, so the emptiness is not currently a false claim,
  but it removes the check the introduction relies on.

## Implemented behavior the spec does not state

Recorded from the same verification, roughly ordered by how likely a reader is
to be surprised. None of these contradicts the spec; each is either a bound, a
durable behavior, or a limitation that only the code documents.

- A child compacts its own conversation privately and unrecoverably; no
  compaction record is written and the child's private history stays in memory
  (`internal/agent/subagent.go:384-403`).
- Child usage is reported as the session's context occupancy in live and
  replayed usage updates (`internal/agent/subagent.go:431-435`,
  `internal/agent/state.go:1601-1610`).
- `edit_file` and `write_file` read the current file without the read tool's
  size bound (`internal/workspace/workspace.go:217`).
- Session permission grants live in memory only, so they do not survive close,
  load, or resume (`internal/agent/agent.go:1807`).
- Children run their tools strictly one at a time, and a `stopping` child still
  occupies one of the four concurrency slots
  (`internal/agent/subagent.go:127-129`, `:534-607`).
- Children may elicit a client form directly, and the shared system prompt still
  describes subagent tools a child does not have (`internal/agent/loop.go:1189`,
  `internal/agent/prompt.go:56-73`).
- Subagent bounds (80-byte name, 64 KiB task, 16 KiB message, 32 reports and 64
  KiB total, 64 KiB result) and the compaction trigger and retention constants
  are model-visible or behavior-defining but unstated
  (`internal/agent/subagent.go:20-24`, `internal/agent/compact.go`).
- `session/resume` refuses a session with a durable pending permission and names
  `session/load` instead, and load or resume requires the requested working
  directory to canonicalize to the session's bound root
  (`internal/agent/agent.go:611-652`).
- Replay reconstructs the recorded updates as a projection rather than the
  byte-exact live stream (`internal/agent/state.go:1549-1708`).

## Verified accurate

| Section                           | Claims checked (rounded) | Result                                                                                                                                                                                                                                                                                                            |
| --------------------------------- | ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Connection                        | 8                        | `--version` output, exit status, and its freedom from configuration, credentials, and stdin; stdin-close exit 0; stdout purity; capability negotiation and the client-method gate                                                                                                                                 |
| Web client                        | 30                       | host lifecycle, bind and TLS posture, workspace registry and isolation, credential bootstrap, open/new/history behavior, navigation, transcript and disclosure controls, attachments, Markdown inertness, workspace settings and secret handling, ACP feature coverage, reconnect and restart                     |
| Sessions and turns                | 12                       | canonical cwd binding, activation resolution, one turn per session, client-order prompt content, streaming before the response, load/refusal rules                                                                                                                                                                |
| Concurrent subagents              | 20                       | 8 per turn and 4 concurrent, name uniqueness, inherited and private state, serialization rules, messaging and continuation, terminal states, child tool exclusion, durability rules, cancellation and no redispatch                                                                                               |
| Workspace operations              | 23                       | confinement against traversal and symlinks, read window and long-line reporting, ignore and dot-component rules, search diagnostics, exact-match edit and byte preservation, mode-aware permission, shell-grant derivation, filesystem delegation pairing, tool title and display metadata, provider-facing names |
| Authentication                    | 6                        | credential requirement, login verification and standard-input sourcing, credential-file immutability, no deliberate credential disclosure                                                                                                                                                                         |
| Diagnostic trace                  | 10                       | off by default, path replacement, create refusal, write-failure disable, one object per line, content-free records                                                                                                                                                                                                |
| Errors                            | 6                        | JSON-RPC error mapping, unknown method, per-request isolation, silence for notifications                                                                                                                                                                                                                          |
| Session lifecycle and recovery    | 12                       | list/load/resume/close/delete semantics, active-session refusal, advisory lock metadata, busy-session rejection, replay never executing tools, pending-permission dispatch limits, per-session storage isolation                                                                                                  |
| Session configuration             | 22                       | option table (ids, values, defaults, order), model profile replacement and reasoning reset, mode tool sets and permissions, setter serialization and no-op, persistence across close/load/resume, auto not answering a recovered request, absence of legacy `modes` and `session/set_mode`                        |
| Process configuration transition  | 15                       | process fields and defaults, CLI overrides, workspace exclusion, no `OX_*`/`OPENROUTER_*` reads, credential-file and keyring rules, no `.env` loading by Ox                                                                                                                                                       |
| Context continuity                | 14                       | budgeting before every request, estimate sources, provider-token replacement, protected prefix and recent groups, tool-call/result integrity, durable summaries, actionable limit errors, recovery at the same boundary                                                                                           |
| Workspace instructions and skills | 21                       | single root `AGENTS.md`, all failure modes naming the path, discovery location and validation, symlink exclusion, 128-skill and 64 KiB bounds, metadata-only catalog, digest-bound loads, durable loaded content                                                                                                  |
| Todo and questions                | 22                       | full-list replacement, entry validation, single in-progress, persist-then-one-update, retention across compaction and restart, form-only elicitation, schema validation, distinct outcomes, no credentials, restart interruption                                                                                  |
| MCP tools                         | 24                       | client-supplied servers only, transport set and capability advertisement, HTTPS and redirect rules, activation validation and cleanup, deadlines, catalog bounds, frozen schemas, revalidation, cancellation per transport, no retry, payload bounds, secret handling, unsupported features                       |
| Language intelligence             | 15                       | tool set, `process.language_servers` shape, PATH resolution, lazy start and deadlines, no auto-restart, confined results and position translation, client-authoritative content, diagnostics version handling                                                                                                     |
| Web access                        | 16                       | permission gate, credential and nonpublic-destination rejection, no ambient headers, five-redirect/30-second/2 MiB bounds, content types, untrusted marking, inline spill, MCP-only search                                                                                                                        |
| Isolation and memory              | 20                       | client cwd, worktree guidance and its limits, host privileges, typed facts with source and expiry, permission-gated mutation, canonical-root scoping, 256/2 KiB/30-day bounds, reads not extending expiry, all-terms matching and newest-first results, session deletion removing facts                           |

Reference checks: all three external links resolve and document what the spec
cites — the ACP session config options page
(https://agentclientprotocol.com/protocol/v1/session-config-options), the Agent
Skills specification (https://agentskills.io/specification), and the MCP
2026-07-28 transports page
(https://modelcontextprotocol.io/specification/2026-07-28/basic/transports). The
spec's statement that Ox uses configuration options as its only mode interface
is accurate as implemented (`AgentCapabilities` carries no `modes`,
`internal/agent/agent.go:237`), and the ACP page does recommend dual advertising
with `modes` during the transition; the spec's choice is a deliberate deviation
worth keeping explicit rather than reading as ACP's recommendation.

## What could not be verified

- That the ACP elicitation request is cancelled by a client when it is on
  screen; no repository test simulates an abandoned form, and the finding above
  rests on what Ox sends.
- Rollback behavior for the P1 finding under a real disk failure; the
  reproduction closes the log deliberately.
- `-32600` handling and load or resume of a nonexistent session; no test covers
  either.
- Real keyring behavior end to end. The suites always pass `--no-keyring`.
- Whether a signal-terminated process leaves children mid-effect; there is no
  signal handler to test.
- The eight-child and four-concurrent limits and duplicate-name rejection have
  no test at any level; those verdicts rest on reading
  `internal/agent/subagent.go:106-141`.
- Web client Playwright coverage was not run, and no test proves replay after a
  host restart. The client unit suite passed (103 tests).
- The subagent and MCP halves of cancellation (`docs/spec.md:123-124`) rest on
  code reading; no test cancels a turn while a subagent streams or an MCP call
  is in flight.

## Checks run

- `make check-docs` (`dprint check`) — passed at the pinned revision.
- Three specification links fetched and read in full.
- `go test -overlay /tmp/oxaudit/overlay.json -count=1 -v -run TestAudit...
  ./internal/agent`
  — reproduced the P1 finding; the overlay test lives outside the repository and
  no repository file was modified.
- `go test -count=1 ./internal/tools/ ./internal/agent/` — passed (verification
  pass).
- `cd client && bun run test` — passed, 103 tests, 0 failures (verification
  pass).
- No full suite was run: this audit changed no source, and the workspace was
  being edited concurrently, so `make check-all` would not have measured a fixed
  revision.
