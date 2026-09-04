# Document ACP Client Development Setup

## Sources

- `eng/roadmap.md#current-milestone-client-interoperability-baseline` — owns the
  requirement to document launching Ox as a local ACP agent.
- `Makefile` — owns the supported local installation command and binary
  destination.
- `docs/settings.md` — owns Ox model, credential, and runtime settings.
- `~/src/references/repos/third-party/protocol/agent-client-protocol` —
  canonical ACP process and transport contract.

## Goal

Add a short end-user guide that lets a developer build Ox, register its binary
with an ACP client, provide Ox's required model and credential, and locate ACP
diagnostics when startup fails.

## Implementation

- Add `docs/acp-client.md` with prerequisites and an ordered setup path:
  - install the current checkout with `make install` and explain that rebuilding
    is required after code changes;
  - configure a model through the existing Ox settings guide and run `ox login`
    before starting the ACP client;
  - register Ox as a local ACP agent launched over standard input and output;
  - require an absolute path to the installed `ox` binary, no arguments, and no
    embedded credential;
  - explain how to start an Ox session and where to inspect ACP logs.
- Keep provider and model details in `docs/settings.md`; link to that guide
  instead of duplicating its settings schema or precedence rules.
- Mark only the development-setup item complete in `eng/roadmap.md`. The smoke
  checklist and interoperability verification remain separate slices.

## Tests

- Check that the guide launches the installed binary directly over standard
  input and output.
- Check every command and file link in the guide against the current repository
  and canonical ACP contract.
- Treat this as documentation-only work; no Go behavior or protocol test should
  change.

## Decisions

- Document pre-authentication with `ox login`. Do not depend on an ACP client
  exposing Ox's terminal authentication method; that behavior belongs to the
  capability and authentication interoperability checks.
- Use an absolute command path so ACP client startup does not depend on its
  inherited `PATH` or shell expansion.
