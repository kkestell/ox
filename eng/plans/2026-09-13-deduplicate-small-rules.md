# Deduplicate small rules

## Goal

Four small rules are written more than once, and every copy is a place the rule
can drift.

Identifier validation appears in the session store and in the memory tool as
byte-identical functions, although both identifiers come from one generator.
Directory syncing appears in the workspace boundary and in the agent as the same
open, sync, and joined close. Localhost detection appears in ACP server
validation and in the MCP transport's redirect check as the same four lines. The
MCP catalog measures its JSON size in two places, and the subtle part — the
comma between array elements and the brackets around them — is restated in both.

## Desired outcome

Each of those rules has one owner, and only genuinely identical copies are
removed.

## Summary of approach

Give each rule to the package that owns the thing it describes: the agent
generates both identifiers, so it owns their shape; the workspace owns
filesystem durability; the ACP boundary owns what a server address may be; and
the MCP package keeps both catalog measurements but shares the accumulator that
gets the delimiters right. Leave the two catalog loops alone, because a server's
own catalog and the combined catalog are different limits.

## Related code

- `internal/agent/store.go` and `internal/tools/memory.go` - the identifier
  validators.
- `internal/agent/lock.go` and `internal/workspace/platform.go` - the directory
  sync.
- `internal/acp/mcp.go` and `internal/mcp/transport.go` - localhost detection.
- `internal/mcp/mcp.go` - the two catalog size loops.

## Current state

- Relevant existing behavior: `internal/tools` already depends on
  `internal/agent`, and `internal/mcp` already depends on `internal/acp`, so no
  new dependency edge appears.
- Existing patterns to follow: the workspace already keeps its directory sync
  behind a package-level value its own tests replace.
- Constraints from the current implementation: the two catalog loops enforce
  different limits and must stay separate.

## Test plan

- **Key behaviors to verify:** the shared accumulator counts the same bytes both
  loops counted, and the existing identifier, sync, redirect, and catalog tests
  still pass.
- **Test levels:** unit, in `internal/mcp`.
- **Edge cases and failure modes:** an empty catalog and a single-tool catalog,
  where the delimiter accounting differs.
- **What not to test:** the rules themselves, already covered by their owners'
  tests.

## Implementation plan

- Export the identifier check from the agent and use it from the memory tool.
- Export the directory sync from the workspace and use it from the agent.
- Export localhost detection from the ACP boundary and use it from the MCP
  transport.
- Share one catalog size accumulator between the two loops.

## Documentation updates

- Todo list item "Deduplicate small safety and protocol rules (F32)".

## Impact assessment

- Code paths affected: identifier validation, directory sync, redirect checks,
  MCP catalog sizing. No behavior change.
- Data, protocol, or schema impact: none.
- Dependency or API impact: three internal packages gain one exported helper
  each.

## Validation

- Tests to write and run: the catalog size test, then the whole suite.
- Static checks: `make check-go`.
