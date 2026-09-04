# Delegate Filesystem Tools to the ACP Client

## Sources

- `eng/roadmap.md#delegated-filesystem` — required capability selection,
  confinement, permission, error, fallback, and process-level acceptance gates.
- `eng/architecture.md#protocol-boundary` and
  `eng/architecture.md#workspace-boundary` — ownership of negotiated client
  capabilities, workspace safety, read evidence, and tool execution.
- `internal/agent/agent.go`, `internal/agent/loop.go`, and
  `internal/agent/tool.go` — initialization state, client callbacks, approval
  ordering, and the invocation seam used by every model-requested tool.
- `internal/tools/read.go`, `internal/tools/write.go`, `internal/tools/edit.go`,
  and `internal/workspace/workspace.go` — current paging, UTF-8,
  text-preservation, exact-edit, evidence, and confinement behavior that both
  executors must retain.
- `~/src/references/repos/third-party/protocol/agent-client-protocol@8e3eb8f2:docs/protocol/v1/file-system.mdx`
  and `agent-client-protocol-schema/src/v1/client.rs` — authoritative capability
  checks and filesystem request/response contract, including absolute paths and
  session IDs.
- `~/src/references/repos/third-party/protocol/acp-go-sdk:agent_gen.go`,
  `types_gen.go`, and `json_parity_test.go` — typed agent-to-client callback and
  cross-language wire examples. No personal reference implements this ACP
  delegation seam; retain Ox's stronger local tool rules around the canonical
  protocol calls.

## Goal

Use ACP filesystem callbacks for read, write, and exact-edit operations when the
client advertises the corresponding capability. Keep confinement, read-evidence,
approval, model-visible results, and durable tool history independent of whether
file contents came from the client or the local workspace.

## Implementation

- `internal/acp/types.go`, `internal/acp/validate.go`, and ACP type tests — add
  the two client method constants and the pinned v1 read/write request and
  response types. Validate outbound required fields while accepting an empty
  text-file response as valid file content.
- `internal/agent/agent.go` — retain the negotiated filesystem flags from
  `initialize` as connection state and construct cancellable
  `jrpc2.Server.Callback` functions for advertised methods during a prompt. Do
  not persist these flags in the session configuration; that is owned by
  `eng/roadmap.md#negotiated-capabilities-in-the-request-configuration`.
- `internal/agent/tool.go` and `internal/agent/loop.go` — pass the available
  client filesystem operations through `Invocation` without teaching the agent
  package file-format mechanics. Capability checks must be per method, and a
  client callback error must become the failed result for that tool call rather
  than aborting the turn.
- `internal/workspace/workspace.go` — expose the smallest resolver needed to
  turn a tool path into a confined absolute path. Preserve canonical handling of
  internal symlinks and reject workspace or spill escapes before an ACP
  filesystem callback.
- `internal/tools/read.go`, `internal/tools/write.go`, `internal/tools/edit.go`,
  and `internal/tools/text.go` — separate content acquisition/replacement from
  the existing pure paging and text transforms. Select the delegated operation
  only when its capability is present. Hash the complete client-provided read
  before applying Ox's output window. For mutations, enforce existing
  read-evidence and exact-match rules and compute the complete replacement in Ox
  before calling `fs/write_text_file`; refresh evidence only after success.
  Leave the current confined local executor as the nil-capability path.
- `internal/e2e/harness_test.go` — let process tests initialize with chosen
  capabilities and answer agent-to-client requests with either results or
  JSON-RPC errors.

## Tests

- Add focused ACP JSON parity tests for both filesystem methods, including
  omitted paging fields, absolute paths, session IDs, empty file content, and
  empty write results.
- In `integration/agent_loop_test.go`, drive delegated read, overwrite, and
  exact edit through callbacks. Assert the session ID and confined absolute
  path, unchanged local file contents, preserved paging/text rules and evidence,
  independent capability fallback, refusal before filesystem dispatch, and a
  client read error followed by a successful continued model turn.
- Add a real-process test in `internal/e2e` that advertises both capabilities,
  services read/write callbacks, and proves escape and unread-write failures
  issue no filesystem request. Keep the browser suite unchanged; its local-path
  comparison belongs to the executor-conformance slice.

## Decisions

- Delegation replaces file-content I/O, not Ox's tool contract. The client gets
  only a validated session ID, confined absolute path, optional read paging, and
  complete write content; it does not decide permission, evidence, or exact edit
  semantics.
