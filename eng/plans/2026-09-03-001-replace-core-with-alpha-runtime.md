# Replace the Ox Core With Alpha's Runtime

## Goal

Replace the small Ox prompt server with Alpha's proven Go runtime while keeping
Ox as the product and standard ACP as the public client boundary.

## Desired outcome

`ox` is a single Go ACP process with Alpha's model/tool loop, safe coding tools,
permissions, durable session lifecycle, subagents, provider resilience, and
runtime integration coverage. No VS Code extension or TypeScript enters the Ox
repository. Existing Ox protocol validation, multimodal prompt handling, request
cancellation, terminal login, and black-box process tests continue to work.

## Summary of approach

Use Alpha's `runtime/` as the implementation body rather than incrementally
recreating its subsystems. Rebrand its module, executable, environment, keyring,
filesystem paths, metadata namespace, and prompts as Ox. Merge Ox's stricter ACP
boundary and broader prompt vocabulary into the transplanted runtime. Remove
Amber's VS Code-specific trust, correlated-turn, and steering extensions; keep
portable subagent and replay metadata under an Ox namespace until standard ACP
can express it.

The first working version executes Alpha's tools locally and reports them over
ACP. Client-delegated filesystem and terminal execution remain separate work
because changing the executor while replacing the core would obscure failures in
the transplant.

## Related code

- `~/src/references/repos/personal/alpha/runtime/` - Runtime source and tests to
  transplant as a coherent unit.
- `~/src/references/repos/personal/alpha/docs/dev/{protocol,settings-and-sessions,tools}.md`
  - Behavioral invariants that must survive the transplant.
- `cmd/ox/`, `internal/acp/`, `internal/agent/`, and `internal/e2e/` - Existing
  Ox boundaries and black-box behavior to preserve.
- `eng/architecture.md` and `eng/roadmap.md` - Owners of the resulting design,
  status, and ACP coverage.

## Test plan

- Retain Ox's malformed-input recovery, stdout purity, full prompt-content,
  refusal, cancellation, authentication, configuration, and concurrent-session
  process tests.
- Port Alpha's focused tests for tool scheduling, approvals, read evidence,
  confined file operations, atomic edits, output spill, shell process groups,
  session folding, append-log repair, activation locking, provider retry, model
  catalog, and settings validation.
- Port Alpha's runtime integration cases for real tool turns, permission
  callbacks, deterministic parallel result order, subagents, durable restart,
  replay, interrupted-turn recovery, and session deletion.
- Add black-box Ox cases proving a session created by one process can be loaded
  and continued by another and that cancelling a permission wait or shell call
  leaves replayable state.
- Do not port extension-host, webview, Playwright, or chat-fixture UI tests.

## Implementation plan

- Replace Ox's Go implementation with Alpha's runtime packages and integration
  harness. Rewrite imports to `github.com/kkestell/ox`; keep the binary at
  `cmd/ox` and preserve the current unbounded handler concurrency needed for
  request cancellation and client callbacks.
- Rebrand every shipped Amber identifier. Use `OX_*`, `.ox/settings.json`, Ox
  XDG cache/data directories, keyring service `ox`, `.ox-tmp`, executable name
  `ox`, and `kkestell.ox/*` only for metadata that remains necessary.
- Build the ACP vocabulary by extending Ox's validated standard types with
  Alpha's session, permission, tool-update, usage, and replay types. Preserve
  client filesystem, terminal, and terminal-auth capabilities, every standard
  prompt-content variant, `$/cancel_request`, and useful JSON-RPC errors.
- Wire Alpha's settings, credential, catalog, provider, durable store, agent
  loop, tools, workspace, shell-rule, and subagent implementations into
  `cmd/ox`. Keep `ox login` and advertise it as ACP client terminal
  authentication.
- Remove workspace-trust input, `_amber/session/steer`, Amber turn correlation,
  and all assumptions that a companion extension supervises the process.
- Merge and rebrand the test suites. Delete superseded Ox implementations only
  after their public cases pass against the transplanted core. Run dependency
  cleanup so `go.mod` contains runtime libraries and tools actually used by Ox.
- Update the architecture, roadmap status and ACP coverage, codebase map,
  commands, settings documentation, and `AGENTS.md` to describe the resulting
  implementation rather than the source project.

## Impact assessment

- All runtime code paths and most Go tests change.
- The session format becomes Alpha's versioned append-only JSONL under the Ox
  data directory. There is no migration because Ox has no users or compatibility
  commitment.
- Ox gains direct dependencies for glob matching, Git ignore parsing, and POSIX
  shell parsing. It gains no JavaScript toolchain, extension process, UI, or
  non-ACP transport.
