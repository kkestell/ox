# Research inherited from Hypercube

Ox succeeds Hypercube. These notes preserve harness investigations and integration
lessons, not Ox's current architecture, supported features, or implementation plans.
For current behavior, see [Ox's architecture](../architecture.md) and
[ACP v2 support](../acp-v2.md).

Imported on 2026-11-12 from Hypercube commit
[`c094fa6`](https://github.com/kkestell/hypercube/tree/c094fa68f409005f1ba4155fc7aa84fde5ce5c23).
Keep the original observation dates, tested versions, evidence labels, and unknowns:
these are historical findings, not guarantees about newer harness versions. Paths
and implementation descriptions in the imported notes refer to Hypercube unless
explicitly stated otherwise. Recheck the installed harness before relying on a
finding in Ox.

## Harnesses

Copied from Hypercube's `.agents/research/harnesses/`:

- [Claude Code](harnesses/claude-code.md): configuration, persistent control,
  permissions, background work, skills, and command discovery.
- [OpenCode V2](harnesses/opencode.md): server ownership, configuration, event
  streams, permissions, native skill attachments, and catalogs.
- [Pi](harnesses/pi.md): RPC, steering, settled idle, resource loading, and skills.
- [Codex CLI](harnesses/codex.md): app-server protocol, threads, approvals, and
  native history. Retained as background, not a commitment to support Codex.

The surveys mention `sources/` snapshots and `sources/urls.json`. Those files were
absent from Hypercube's research directory at import and are not included here;
the public URLs in the notes remain the available references. Scratch captures
mentioned in the utility-model experiments are also not included.

## Experiments and lessons

- [Utility-model experiments](utility-model.md), copied from
  `.agents/research/utility-model.md`: live tests of lightweight calls using
  existing harness credentials, including startup side effects and cancellation.
- [Why Hypercube dropped Codex](codex-retirement.md), copied from
  `docs/dev/codex-retirement.md`: historical costs and failures of the adapter and
  private-storage workaround. Links to unimported records point back to Hypercube.
- [Selected Linux lessons](linux.md), extracted from `.agents/research/linux.md`:
  process ownership, environment, portability, and test-environment findings.
  Go/Electron-specific build and desktop guidance was deliberately omitted.
