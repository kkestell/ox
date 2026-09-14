# Session-update values and dead code

## Goal

The ACP session-update discriminator has constants for five of its eight
spellings. Producers mix the constants with bare string literals for the same
concept, so a reader cannot tell which spellings are owned by the protocol
package and which were typed at the call site.

Separately, small pieces of code across packages say something other than what
they do: a validated prompt message is computed and thrown away, a window result
carries four fields nobody reads, one stream attempt returns five positional
values, locals shadow `copy`, a replay parameter is nil at every call site, an
MCP wrapper holds one field, two different functions are both named for a tool
title, a cancelled prompt clears an error field to reach the success path, an
escape is recognised by a substring of an error message, and the same
cancellation sentence is spelled out seven times.

## Desired outcome

Every session-update spelling has one owner in `internal/acp`, and every
producer names it. Each of the local pieces above either disappears or states
its purpose directly.

## Summary of approach

Complete the constant set and replace the literals. Then take the dead and
misleading pieces one at a time, preferring deletion to rewriting: drop what is
unread, return what is used, and name the repeated string once.

Marshalling a value this package built cannot fail, so the panic that follows it
is an assertion. Give `internal/agent` one `mustMarshal` that names that
assertion instead of nine copies of it.

## Related code

- `internal/acp/types.go` - the session-update constant block.
- `internal/agent/adapter.go`, `state.go`, `config_options.go` - the producers.
- `internal/agent/agent.go` - the discarded prompt message and the cancelled
  prompt's error field.
- `internal/agent/loop.go` - the tool-title functions and the cancellation text.
- `internal/agent/mcp.go` - the range rebinding.
- `internal/mcp/mcp.go` - `discoveredTool`.
- `internal/openrouter/client.go` - `streamAttempt`.
- `internal/tools/read.go` - `window` and `windowed`.
- `internal/workspace/workspace.go` - `isEscape`.

## Current state

- Relevant existing behavior: `promptMessage` is called for its error alone; the
  turn builds its own history from the durable record.
- Existing patterns to follow: `mustDuration` in `internal/mcp` already names a
  start-up assertion the same way.
- Constraints from the current implementation: `ToolCallUpdate.SessionUpdate` is
  `omitempty` because the type is also embedded in a permission request, where
  the discriminator does not belong.

## Test plan

- **Key behaviors to verify:** the wire spellings are unchanged, and a prompt
  cancelled by the client still reports a cancelled stop reason with usage.
- **Test levels:** unit, in `internal/acp` and `internal/tools`.
- **Edge cases and failure modes:** a read whose window is empty or past the end
  of the file.
- **What not to test:** the constants themselves.

## Implementation plan

- Complete the session-update constant set and use it at every producer.
- Remove the discarded prompt message, the unread window fields, the always-nil
  replay metadata, the one-field MCP wrapper, and the redundant range rebinding.
- Give `streamAttempt` a named result.
- Rename the locals that shadow `copy` and the agent's tool-title method.
- Return the cancelled prompt's response instead of clearing its error.
- Name the tool-cancellation sentence once, add `mustMarshal`, and say why an
  escape is recognised by its message.

## Documentation updates

- Todo list item "Standardize session-update values and remove trivial dead or
  misleading code (F35, F39)".

## Impact assessment

- Code paths affected: session notifications, prompt cancellation, reads,
  OpenRouter streaming, MCP discovery. No behavior change.
- Data, protocol, or schema impact: none.
- Dependency or API impact: three new constants in `internal/acp`.

## Validation

- Tests to write and run: the whole suite.
- Static checks: `make check-go`.
