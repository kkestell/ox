# Security Model Documentation

## Goal

Write a single, comprehensive, end-user-facing document that describes how Ur handles permissions, security, sandboxing, and extensions. The audience is programmers; the tone is descriptive and direct — no selling, no comparisons, no code references. Just "here is how it works."

## Desired outcome

A well-structured document at `docs/security.md` that a new user can read to understand the full security model, from the workspace boundary through to extension sandboxing, without referencing source code.

## How we got here

The user wanted documentation for Ur's security/permissions/sandboxing/extension behavior. The codebase has comprehensive security infrastructure (permission matrix, extension sandboxing, trust tiers, subagent isolation, keyring-based secrets) but zero end-user documentation for any of it.

We brainstormed three organizational approaches:
- **Layer-driven** (workspace → permissions → extensions → sandbox → subagents → secrets) — builds understanding incrementally, maps to the architecture, no repetition
- **Threat-model driven** — adversarial framing didn't match the "just describe how it works" brief
- **User-action driven** — repetitive, since many actions share underlying mechanisms

The user chose **layer-driven**. We also confirmed: single page, at `docs/security.md`, concepts only (no CLI syntax).

## Recommended approach

Write a layer-driven document organized from the broadest trust boundary inward. Each section builds on the previous, so a reader who understands workspace containment will naturally understand why the permission matrix is structured the way it is.

## Related code

These files define the behavior being documented. They are **not** referenced in the document itself, but the plan author must read them to ensure accuracy.

- `src/Ur/Workspace.cs` — workspace containment check (the foundational boundary)
- `src/Ur/Permissions/OperationType.cs` — Read/Write/Execute classification
- `src/Ur/Permissions/PermissionScope.cs` — Once/Session/Workspace/Always grant durations
- `src/Ur/Permissions/PermissionPolicy.cs` — the prompt/no-prompt decision matrix
- `src/Ur/Permissions/PermissionGrantStore.cs` — grant persistence, prefix matching, lazy loading
- `src/Ur/Permissions/PermissionGrant.cs` — persisted grant record structure
- `src/Ur/Extensions/ExtensionLoader.cs` — extension sandbox (`CreateSandboxedState`), tool registration API
- `src/Ur/Extensions/ExtensionTier.cs` — System/User/Workspace trust tiers
- `src/Ur/Extensions/ExtensionOverrideStore.cs` — enable/disable override persistence
- `src/Ur/Extensions/ExtensionCatalog.cs` — runtime enable/disable/reset API
- `src/Ur/AgentLoop/ToolInvoker.cs` — permission enforcement during tool invocation
- `src/Ur/AgentLoop/SubagentRunner.cs` — subagent isolation (fresh history, no recursion)
- `src/Ur/Tools/BuiltinToolFactories.cs` — permission metadata for each builtin tool
- `src/Ur/Tools/BashTool.cs` — process execution controls (timeout, truncation, working directory)
- `src/Ur/Configuration/Keyring/` — OS-native keyring for API key storage
- `src/Ur/Sessions/UrSession.cs` — permission callback wiring
- `src/Ur/UrHost.cs` — startup orchestration, tool registry assembly

## Implementation plan

### Section outline

The document should follow this structure. Each section is a task.

- [ ] **Title and opening** — Set the scope: this document describes Ur's security model. One paragraph.
- [ ] **The workspace boundary** — What a workspace is. The distinction between in-workspace and outside-workspace paths. Why this matters: it is the primary trust boundary that determines how aggressively Ur prompts for approval.
- [ ] **Operation types** — The three classifications: Read, Write, Execute. Explain that every tool operation falls into one of these categories. Briefly note which builtin tools map to which type.
- [ ] **The permission matrix** — The core behavior table: in-workspace reads are auto-allowed; everything else prompts. Execute operations are always treated as outside-workspace (always prompt, Once only). Explain the reasoning: commands can reach anything regardless of working directory.
- [ ] **Permission scopes** — Once, Session, Workspace, Always. What each means in terms of persistence and reach. Where grants are stored (`.ur/permissions.jsonl` for workspace scope, `~/.ur/permissions.jsonl` for always scope). Prefix matching: a grant for a directory covers everything under it.
- [ ] **Conservative defaults** — Tools without explicit permission metadata default to Write (always prompt). Extension tools default to Write. If no permission callback is configured (headless mode), operations are denied. This section ties together the "why" behind the previous sections.
- [ ] **Extensions** — What extensions are: Lua-based tool providers. The trust tier system (System > User > Workspace). Default enablement (system and user are on by default; workspace extensions must be explicitly enabled). Name collision resolution (higher-trust tier wins). How extensions register tools through the `ur.tool.register` API.
- [ ] **Extension sandboxing** — What the Lua sandbox provides: extensions run with no filesystem access, no OS access, no I/O, no module loading, no debug library. Only basic Lua, string, table, and math libraries are available. Extensions can only register tools — they cannot escape the sandbox.
- [ ] **Extension tools and permissions** — Extension tools inherit the full permission system. They default to Write classification (require approval). Extensions cannot self-declare their operation type. The permission prompt identifies which extension is requesting access.
- [ ] **Subagent isolation** — Subagents run with fresh message histories (no access to parent conversation). They cannot spawn further subagents (self-recursion excluded from their tool registry). They share the parent's permission grants.
- [ ] **Command execution controls** — Shell commands run via `bash -c` with the workspace as working directory. Timeout enforced (default two minutes). Process tree killed on timeout. Output truncated at 2000 lines / 100 KB.
- [ ] **Secret management** — API keys stored in OS-native keyrings (macOS Keychain, Linux libsecret via P/Invoke). Never written to plain-text files.

### Writing tasks

- [ ] Write the document at `docs/security.md` following the outline above
- [ ] Ensure no source code references, file paths from the codebase, or implementation details (e.g., C# type names) leak into the prose
- [ ] Ensure the tone is descriptive, direct, and programmer-appropriate — no marketing language, no comparisons to other tools
- [ ] Run `make format-docs` to apply Prettier formatting

## Validation

- [ ] Manual read-through: does a programmer who has never seen the codebase understand the full security model after reading this document?
- [ ] Accuracy check: every behavior described matches the actual implementation in the related code files
- [ ] No code references: verify no C# type names, method names, or source file paths appear in the document
- [ ] `make format-docs` passes

## Gaps and follow-up

- This document covers concepts only. A separate CLI reference (covering `ur extensions list/enable/disable/reset/settings` and the permission prompt syntax) would be a natural follow-up.
- The document does not cover the settings system (two-layer merge, schema validation) or the skills system (prompt templates) — these are peer subsystems that may warrant their own documentation.
