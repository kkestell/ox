# Browser client authentication

## Goal

Let the browser client authenticate an Ox process using an already stored
credential, run the advertised terminal login flow without placing a credential
on ACP, and log out through the same supervised connection.

## Desired outcome

The browser shows whether its workspace requires authentication, can verify a
stored OpenRouter credential, can submit a password to a short-lived `ox login`
child, and can log out when Ox advertises that capability. Credential values are
never put in browser snapshots, result payloads, URLs, diagnostics, or ACP
metadata. A failed operation leaves the honest authentication state visible.

## Summary of approach

Extend the workspace supervisor's initialized ACP state with browser-safe
authentication methods and state. It will advertise terminal-auth support,
invoke ordinary ACP authentication for an agent method, and execute an
advertised terminal method by appending its arguments to the configured Ox
invocation, writing the browser password to that child's stdin, and then
re-authenticating the existing ACP connection. The host exposes these operations
as validated write-only browser commands; React renders semantic credential and
logout controls from snapshots.

## Related code

- `eng/client-architecture.md` — owns host authentication, secret handling, and
  the configured-invocation terminal-login rule.
- `docs/spec.md#authentication` — owns the OpenRouter authentication contract.
- `client/src/workspace-supervisor.ts` — owns the Ox process and ACP connection.
- `cmd/ox/login.go` — defines the piped credential behavior of `ox login`.
- `references/repos/personal/alpha/extension/src/runtime/client.ts` — prior art
  for driving advertised ACP authentication and logout, without reusing its
  in-band credential extension.

## Structural considerations

- **Hierarchy:** The workspace supervisor remains the sole owner of ACP and
  child processes; the host dispatches browser commands and React renders state.
- **Abstraction:** Authentication is workspace state, not a browser-side ACP
  adapter or a separate credential service.
- **Modularization:** Keep the small terminal-login process lifecycle beside the
  supervisor's existing configured invocation rather than adding a general
  terminal subsystem before its dedicated task.
- **Encapsulation:** Browser protocol values contain method labels, state, and
  generic failures only. The password command is write-only and dropped once
  stdin closes.
- **Testability:** Unit tests use a controllable ACP/login fixture process;
  Playwright proves the full browser-to-host-to-process path.

## Test plan

- Validate authentication, terminal-login, and logout browser commands while
  rejecting malformed, unknown, or blank credential input.
- Verify initialization advertises terminal-auth support, agent authentication
  success and failure transitions, logout, terminal command argument and stdin
  handling, nonzero login exits, and that captured state/diagnostics omit the
  supplied secret.
- In Playwright, exercise stored-credential authentication, a successful and a
  failing browser password login, and logout through real child-process
  boundaries using deterministic fixtures.
- Do not add filesystem or general terminal callback support in this task.

## Implementation plan

- Extend browser protocol snapshots and commands with browser-safe auth state,
  advertised method descriptors, and correlated write-only actions.
- Have the supervisor initialize with terminal-auth capability, retain supported
  agent/terminal methods and logout capability, serialize auth operations, and
  publish state transitions.
- Implement stored credential authentication and logout over ACP, plus terminal
  login using the exact configured command, inherited launch environment plus
  advertised terminal environment, and a closed credential stdin pipe.
- Route the commands through the host without logging their inputs and render
  unstyled semantic authentication controls in the browser shell.
- Add focused Bun and Playwright coverage, then mark the roadmap child complete.

## Documentation updates

- Mark the completed browser-client authentication task in `eng/todo.md`.

## Impact assessment

- Code paths affected: browser protocol and shell, host dispatch, workspace
  supervisor, focused client tests, and the roadmap.
- Data, protocol, or schema impact: browser snapshots gain safe authentication
  state and browser commands gain write-only auth actions; ACP stays unchanged.
- Dependency or API impact: no new dependency or Ox API.

## Validation

- Run `bun run check`, `bun test`, and `bun run test:e2e` from `client/`.
- Run `make check-docs` and inspect the focused diff.
