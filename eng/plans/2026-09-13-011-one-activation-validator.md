# One activation validator

## Goal

`internal/acp` owns validation of client input, and `NewSessionRequest.Validate`
checks the working directory, the MCP server list, and the additional
directories Ox does not support. The agent's `validateActivation` then checks
the same three things again, because `session/load` and `session/resume` have no
boundary validator at all and rely on it.

A reader cannot tell which package owns a rule, and the two copies can drift:
the agent's working-directory message already differs from the boundary's.

## Desired outcome

Every activation request is validated the same way, at the ACP boundary. What
remains in the agent depends on state the boundary cannot see: whether the
working directory resolves on this filesystem, and whether the process has a
credential.

## Summary of approach

Give the load and resume requests the `Validate` method they lack, expressing
each one's own rule about MCP servers: `session/load` resupplies them, so the
field is required, while `session/resume` accepts their absence. Call it from
both methods and reduce the agent's activation check to the state-dependent
part.

## Related code

- `internal/acp/validate.go` - `NewSessionRequest.Validate`, the shape the new
  validators follow.
- `internal/agent/agent.go` - `validateActivation`, `NewSession`, `LoadSession`,
  `ResumeSession`.

## Current state

- Relevant existing behavior: the agent canonicalizes the working directory,
  which touches the filesystem and so cannot move to the boundary.
- Existing patterns to follow: every other request type validates itself, and
  each method wraps the failure as invalid parameters.
- Constraints from the current implementation: the session identifier's hex
  format is a storage rule, so it stays with the store.

## Test plan

- **Key behaviors to verify:** a load or resume request with a relative working
  directory, an unsupported additional directory, or an invalid MCP server is
  refused; load refuses an absent server list and resume accepts one.
- **Test levels:** unit, in `internal/acp`.
- **Edge cases and failure modes:** a missing session identifier.
- **What not to test:** MCP server validation itself, already covered.

## Implementation plan

- Add `Validate` to the load and resume requests.
- Call it from `LoadSession` and `ResumeSession`.
- Reduce `validateActivation` to canonicalization and the credential check.
- Add the boundary tests.

## Documentation updates

- Todo list item "Remove duplicate ACP activation validation (F30)".

## Impact assessment

- Code paths affected: session activation. Load and resume now refuse the same
  malformed input `session/new` already refused.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the boundary tests, then the whole suite.
- Static checks: `make check-go`.
