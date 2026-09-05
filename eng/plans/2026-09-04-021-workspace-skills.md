# Workspace Skills

## Sources

- `docs/spec.md#workspace-instructions-and-skills` — discovery root, Agent
  Skills validation, catalog and file bounds, activation lifetime, loading, and
  parent/child behavior
- `eng/roadmap.md#workspace-skills` — slice scope and completion gates
- `eng/architecture.md#configuration-and-credentials` and
  `eng/architecture.md#context-and-durable-projections` — immutable activation
  inputs and recovered-turn ownership
- [Agent Skills specification](https://agentskills.io/specification) — canonical
  `SKILL.md` directory, frontmatter, naming, and optional-field rules
- `~/src/references/repos/personal/eta/internal/skills/{skills,skills_test}.go`,
  `~/src/references/repos/personal/eta/internal/frontmatter/frontmatter.go`, and
  `~/src/references/repos/personal/eta/internal/agent/{prompt,prompt_test}.go` —
  metadata parsing, deterministic discovery, and catalog prompting to adapt
- `internal/agent/{prompt,agent,state,loop,tool}.go`, `internal/tools/tools.go`,
  and `internal/workspace/workspace.go` — current activation, durable request
  configuration, tool dispatch, and confined-read seams

## Goal

Discover valid workspace-local skills at activation, expose only their bounded
metadata in parent and child prompts, and load a selected skill's validated
instructions through a dedicated read-only tool. Freeze the catalog and file
identity with each activation so changed files cannot be paired with stale
metadata or recovered work.

## Implementation

- `internal/skills` and `go.mod` — port Eta's frontmatter split and metadata
  validation into a focused workspace-only loader, using `gopkg.in/yaml.v3`
  directly for the Agent Skills YAML contract. Discover only
  `.agents/skills/<name>/SKILL.md` below the canonical root with no symlink
  traversal; treat a missing catalog directory as empty, fail activation for an
  unreadable or symlinked catalog root, and return path-bearing warnings for
  malformed individual entries that are skipped. Read regular UTF-8 files with a
  64-KiB bound, sort valid skills by name, enforce the 128-skill limit, and
  retain a digest of the complete validated file plus its workspace-relative
  location.
- `internal/agent/prompt.go` and activation in `internal/agent/agent.go` —
  render the catalog's name, description, and relative `SKILL.md` location into
  a distinct bounded system-prompt block for both parent and child. Explain that
  the `skill` tool loads instructions, referenced files use confined file reads,
  and metadata or loaded text cannot grant tools or permission. Fail activation
  when the actual rendered block exceeds 64 KiB, and log skipped entries with
  their paths.
- `internal/agent/state.go` — include the ordered skill references and file
  digests in durable request configuration, cloning, equality, validation, and
  checkpoint projection. Bump the checkpoint version. This lets an open or
  recovered turn use its original catalog while a later reactivation captures
  workspace changes.
- `internal/tools` and `internal/agent/{tool,loop}.go` — register an
  approval-free, parallel-safe `skill` tool in code and plan modes for parents
  and children. Accept one catalog name, resolve it only through the turn's
  frozen references, safely reread the confined regular file, and compare its
  complete digest before returning the validated Markdown instructions. A
  missing, replaced, symlinked, or changed file returns a path-bearing error
  directing reactivation. Ordinary durable tool-result history then preserves
  successfully loaded content across restart and compaction without a second
  state record.
- `eng/architecture.md` — add the focused skills package to the responsibility
  and dependency maps without duplicating discovery rules owned by the
  specification.

## Tests

- Loader tests cover valid optional metadata, deterministic order, name and
  directory validation, malformed YAML/frontmatter, invalid UTF-8, the file,
  count, and rendered-catalog bounds, missing/unreadable roots, non-regular
  entries, and symlinks at every traversed level.
- Prompt, state, and tool tests prove metadata-only rendering, identical
  parent/child catalogs, exact-name loading, unknown names, digest change
  detection, configuration cloning/validation, checkpoint round trips, and
  plan-mode availability.
- Fake-model integration and process tests load a skill and a referenced file,
  show the same catalog to parent and child, preserve loaded tool history after
  restart and compaction, and prove an `allowed-tools` declaration cannot bypass
  shell permission or execute a script by discovery alone.
- Recovery tests change a skill after a durable wait and prove the original turn
  refuses the stale load while a reactivated subsequent turn sees the new
  metadata and body.

## Decisions

- Store only metadata, relative location, and a full-file digest in request
  configuration. The body remains workspace data until explicitly loaded, and
  the resulting tool history is the sole durable copy exposed to the model.
- Port Eta's small parser and validation seam, but omit its user-wide roots,
  implicit direct file loading, and permission exceptions.
