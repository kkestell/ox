# Document Zed Development Setup

## Sources

- `eng/roadmap.md#current-milestone-zed-interoperability-baseline` — owns the
  requirement to document launching Ox as a Zed ACP agent.
- `Makefile` — owns the supported local installation command and binary
  destination.
- `docs/settings.md` — owns Ox model, credential, and runtime settings.
- `~/src/references/repos/third-party/protocol/zed-acp:docs/src/ai/external-agents.md#custom-agents`
  — pinned Zed instructions and settings shape for custom ACP agents.
- `~/src/references/repos/third-party/protocol/zed-acp/crates/agent_servers/src/acp.rs`
  — pinned process-spawning and ACP logging behavior.

## Goal

Add a short end-user guide that lets a developer build Ox, register its binary
as a custom Zed agent, provide Ox's required model and credential, and locate
ACP diagnostics when startup fails.

## Implementation

- Add `docs/zed.md` with prerequisites and an ordered setup path:
  - install the current checkout with `make install` and explain that rebuilding
    is required after code changes;
  - configure a model through the existing Ox settings guide and run `ox login`
    before starting Zed;
  - add Ox through Zed's **Agent Settings → External Agents → Add Custom Agent**
    flow;
  - include one valid `agent_servers` JSON example using `type: "custom"`, an
    absolute path to the installed `ox` binary, no arguments, and no embedded
    credential;
  - explain how to start an Ox thread from Zed's agent selector and where to
    inspect ACP logs with `dev::OpenAcpLogs`.
- Keep provider and model details in `docs/settings.md`; link to that guide
  instead of duplicating its settings schema or precedence rules.
- Mark only the development-setup item complete in `eng/roadmap.md`. The smoke
  checklist and interoperability verification remain separate slices.

## Tests

- Check that the JSON example matches the pinned Zed custom-agent schema and
  launches the installed binary directly over stdio.
- Check every command, file link, menu name, and command-palette action in the
  guide against the current repository and pinned Zed snapshot.
- Treat this as documentation-only work; no Go behavior or protocol test should
  change.

## Decisions

- Document pre-authentication with `ox login`. Do not depend on Zed exposing
  Ox's terminal authentication method; that behavior belongs to the later
  capability and authentication interoperability checks.
- Use an absolute command path in the example so Zed startup does not depend on
  its inherited `PATH` or shell expansion.
