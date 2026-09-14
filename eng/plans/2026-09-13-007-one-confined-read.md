# One confined read

## Goal

Three packages implement the same security-sensitive read. Workspace file
reading, root-instruction loading, and skill loading each reject a symbolic
link, open non-blocking so a raced FIFO cannot hang the caller, re-check that
the opened handle is a regular file, and bound what they materialize. A change
to any of those rules has to be made three times to hold.

## Desired outcome

The confined read exists once, at the workspace boundary. Each caller keeps the
policy that is genuinely its own: which byte bound applies, whether the content
must be UTF-8, how a missing file reads, and how an error is phrased.

## Summary of approach

Add a confined read to the workspace package that takes a caller's root, a name,
and a byte bound, and returns the bytes. Point skill loading and
root-instruction loading at it. Workspace reads keep their own path because they
deliberately follow a workspace-internal symlink after resolving it, but the
bound itself becomes shared.

## Related code

- `internal/workspace/platform.go` - `openRegularFile`, which already performs
  the non-blocking open and the opened-handle check.
- `internal/workspace/workspace.go` - `ReadFile`, whose confinement resolves a
  path rather than refusing links.
- `internal/skills/skills.go` - `readBoundedRegular`.
- `internal/agent/prompt.go` - `loadRootInstructions`.

## Current state

- Relevant existing behavior: `ReadFile` accepts a symlink that resolves inside
  the workspace, and a test covers that, so the strict rule cannot simply be
  applied everywhere.
- Existing patterns to follow: `openRegularFile` already owns the part that is
  identical in all three.
- Constraints from the current implementation: root-instruction loading treats a
  missing file as "no instructions", so the read must surface a missing-file
  error the caller can recognize.

## Test plan

- **Key behaviors to verify:** a symlink, a directory, and a device are refused;
  a file over the bound is refused; a missing file reports a missing-file error;
  existing skill and instruction behavior is unchanged.
- **Test levels:** unit, in `internal/workspace`; the existing skill and prompt
  tests already cover the callers.
- **Edge cases and failure modes:** a file exactly at the bound.
- **What not to test:** each caller's error wording.

## Implementation plan

- Add the confined read and a shared bounded read to the workspace package.
- Use them from skill loading and root-instruction loading.
- Use the shared bound in `Workspace.ReadFile`.
- Add the workspace tests and record the new dependency edge.

## Documentation updates

- `eng/architecture.md` gains the `skills -> workspace` edge.
- Todo list item "Centralize confined regular-file reads at the workspace
  boundary (F21)".

## Impact assessment

- Code paths affected: skill loading, root-instruction loading, workspace reads.
- Data, protocol, or schema impact: none.
- Dependency or API impact: `internal/skills` gains a dependency on
  `internal/workspace`.

## Validation

- Tests to write and run: the workspace tests, then the skills, agent, tools,
  integration, and end-to-end suites.
- Static checks: `make check-go`.
