# 2026-08-17-009. Authenticate and logout

## Goal

Ox answers `initialize` with `authMethods: []` and implements neither
`authenticate` nor `logout`. A person whose only problem is that Ox cannot see
their key gets `-32000` from `session/new` and a message naming
`OPENROUTER_API_KEY` and the keyring entry, and then has no way to act on it:
the client has nothing to offer, so the fix is to quit the editor, arrange the
environment, and start over. That is the state the previous plan left behind on
purpose — it built a credential that can be written and deleted while Ox runs
and stopped short of the two methods that do it. `credentials.Store.Set` and
`Clear` have no caller outside their own tests.

There is a second gap the same work closes. Nothing establishes that a key is
good. A key with a typo resolves like any other, `session/new` succeeds, and the
first prompt fails with OpenRouter's 401 wrapped in `-32603`, which reads like
an outage rather than a wrong key.

## Desired outcome

`initialize` advertises what a client can do about authentication: a method for
using a credential Ox already has access to, a method for logging in through a
terminal when the client says it can run Ox in one, and
`agentCapabilities.auth.logout`.

`authenticate` with the stored-credential method re-reads the environment and
the keyring, then asks OpenRouter whether the credential works. A working
credential returns an empty result, and the very next `session/new` succeeds. No
credential anywhere returns `-32000` naming both sources. A credential
OpenRouter rejects returns `-32000` carrying the provider's reason, so a
mistyped key is reported as a wrong key. An unknown method ID is `-32602`.

Because `authenticate` re-resolves, a key stored by any means after Ox started —
`ox login` in a terminal, another editor window, `security add-generic-password`
— takes effect without restarting Ox.

`ox login` is how a key gets in. It prompts on the terminal without echoing what
is typed, verifies the key with OpenRouter, stores it in the keyring, and exits
zero. It exits nonzero with a human-readable problem when the key is blank,
rejected, or unstorable. That exit status is exactly what the terminal
authentication method needs, so a client that supports the method runs the login
flow for the user without leaving the editor.

`logout` deletes the keyring entry. Existing sessions stay, and the next prompt
in one fails with `-32000` rather than sending an empty bearer token to
OpenRouter. `logout` refuses while `OPENROUTER_API_KEY` supplies the credential,
because Ox cannot unset its client's environment and reporting success would be
a lie.

No credential ever crosses the ACP boundary. Ox does not accept a key in a
protocol message, and `authenticate` does not carry one.

## Summary of approach

A new `internal/agent/auth.go` owns the authentication surface: the two method
IDs, the `authMethods` list `Initialize` returns, the `Authenticate` and
`Logout` handlers, and the `authRequiredError` helper that the three places
needing `-32000` share.

Ox advertises two methods. The stored-credential method is the default `agent`
type, which the client completes by calling `authenticate`. The terminal method
carries `"type": "terminal"` and `"args": ["login"]`, and Ox advertises it only
when the client set `clientCapabilities.auth.terminal`, which the protocol
requires. A client that supports it reproduces its own configured Ox invocation
in an interactive terminal with `login` appended, waits for the process to exit,
and treats a zero status as success. The descriptor cannot name a command, which
is the point: the client already knows how it launches Ox.

`Authenticate` refuses the terminal method ID with `-32602`, because that method
is completed out of band and the protocol forbids passing it here. For the
stored-credential method it calls `Refresh`, gates on a credential existing, and
then verifies it.

Verification is one new method on the provider client,
`VerifyCredential(ctx, key)`, which issues `GET /key` with that key as the
bearer token. OpenRouter answers 200 for a usable key and 401 with
`{"error":{"message":"User not found."}}` for one it does not recognise. The key
is a parameter rather than the client's own accessor, because `ox login` has to
verify a key before it is stored and `authenticate` has to verify the one that
is resolved. A rejection wraps an exported `ErrCredentialRejected` so the one
caller that branches on it can tell "wrong key" from "cannot reach OpenRouter";
a transport failure or a 5xx stays a plain error and surfaces as `-32603`.

`Logout` calls `Clear` and reports its refusal as `-32603`. It deliberately does
not touch sessions. ACP leaves the fate of running sessions to the agent, and
the honest minimum is to keep them and gate what they do next, so `Prompt` gains
the same credential gate `NewSession` already has, placed after the session
lookup so an unknown session is still reported as an unknown session.

The gate answers one question — why the resolved credential cannot be used — and
returns that reason as the `-32000` message, so what a client shows matches what
is actually wrong. No credential is the sentence naming both sources; a
credential `authenticate` saw OpenRouter reject is the provider's own reason,
which is why the agent remembers the key that was rejected and the message that
came with it. A successful `authenticate` and a `logout` both forget it. The
gate never verifies: verification is a round trip, and paying it per turn to
re-answer a question `authenticate` already answered would be waste.

`ox login` makes `cmd/ox` a program with two modes. `main` dispatches on the
argument: no arguments serves ACP over stdio as it does today, `login` runs the
login flow, and anything else is a usage error with exit status 2 — a
misconfigured client must not get a silent ACP server. The flow lives in
`cmd/ox/login.go` as a function taking the input file, the output writer, the
store, and the client, so it is unit-testable with a pipe and an
`httptest.Server`. It reads the key without echo through
`golang.org/x/term.ReadPassword` when its input is a terminal and as a plain
line when it is not, which is both what a piped key needs and what makes the
flow testable. `ReadPassword` turns echo back on with a deferred call that the
default handling of an interrupt skips, so the terminal read restores the
terminal itself when a signal arrives and leaves no shell unable to echo. It
refuses before prompting when the keyring is off, so it never asks for a secret
it cannot keep, and it reports that `OPENROUTER_API_KEY` still wins when the key
it just stored is not the one Ox will use.

The credential store keeps owning both of those facts, so the login flow asks it
rather than reading the environment a second time. The sentence about
`OX_KEYRING_DISABLED` becomes an exported `KeyringDisabledMessage` that both
`Set` and the login flow use, so the wording has one owner, the way
`NoCredentialMessage` already does; `KeyringDisabled` reports whether storing is
possible at all; and the resolved `Source` after a successful `Set` is what says
whether the environment still wins.

The end-to-end harness gains three things. Its default `OX_OPENROUTER_BASE_URL`
points at a refused local address, so a test that forgets to start a mock model
fails with a connection error instead of quietly reaching openrouter.ai — which
matters now that a method other than `session/prompt` talks to the provider. The
mock model routes `GET /key` alongside the chat completions path, records the
bearer token each check presented, and can be scripted to reject one. And the
harness can run the binary as a one-shot command with piped input, which is how
`ox login` is exercised at the process boundary.

## Related code

- `~/src/references/repos/personal/alpha/runtime/internal/agent/agent.go:178-182`
  — the `authMethods` list on the `initialize` response, one method with an ID,
  name, and description. `:174-176` advertises `auth.logout` as `{}`. `:197-234`
  is `Authenticate`: unknown method ID becomes `InvalidParams`, then `Set` or
  `Refresh`, then a final gate that returns `auth_required` when the credential
  is still missing — the shape Ox's handler takes, minus the `_meta` key path.
  `:236-247` is `Logout`, which is `Clear` plus a log line. `:783-785` is
  `authRequiredError`, the one place the `-32000` code is written.
- `~/src/references/repos/personal/alpha/runtime/internal/acp/types.go:9-10` and
  `agent.go:211-225` — the private `_meta` extension that lets Alpha's own VS
  Code client hand the runtime an API key inside `authenticate`, and `:766-774`,
  which reports the credential source back through `_meta` on `initialize`,
  `authenticate`, and `logout`. Recorded as the option this plan does not take:
  `_meta` is a channel between a matched client and agent, Ox has no client of
  its own, and a key in an ACP message is a secret in the client's logs and
  process memory for no gain over the keyring.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/protocol/v1/authentication.mdx:37-63`
  — `authMethods` on the `initialize` response and
  `agentCapabilities.auth.logout`, where `{}` means supported and an omitted
  value means the client must not call `logout`. `:67-77` states that a method
  with no `type` is an `agent` method. `:101-111` fixes the empty result and
  states that authentication stops the `auth_required` error. `:138-142` leaves
  the fate of running sessions to the agent and tells clients to expect
  authentication errors on existing sessions after a logout, which is the
  licence for keeping sessions and gating prompts.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/rfds/auth-methods.mdx:49-99`
  — the terminal method: `args` and `env` are appended to the client's own
  configured invocation, and the descriptor cannot name a command. `:101-136` is
  the `clientCapabilities.auth.terminal` opt-in and the rule that an agent may
  advertise the method only when the client advertised the capability.
  `:138-162` is the flow — launch, wait for exit, zero means success, reconnect
  and retry — and the rule that the client must not pass a terminal method to
  `authenticate`. `:170-174` records that the `env_var` method, which would have
  asked the client to collect a key and restart the agent with it, was removed
  in July; it is not an option. `:180-182` names structured credential
  collection through elicitation as the alternative that keeps user input inside
  the agent's own flow.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/docs/protocol/v1/elicitation.mdx:105-111`
  — form mode must not be used for API keys, and an agent must not fall back to
  it when the client lacks URL mode. This is why there is no in-band prompt for
  a key: the only compliant structured flow is URL mode, which needs Ox to host
  an HTTP endpoint.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/agent-client-protocol-schema/src/v1/agent.rs:568-590`
  — `AuthMethod` is a `type`-tagged union whose untagged default is `agent`.
  `:647-667` is `AuthMethodAgent` (`id`, `name`, optional `description`);
  `:718-749` is `AuthMethodTerminal`, which adds `args` and, in v1, an `env`
  map. `:465-473` is `AgentAuthCapabilities` and `:520-531`
  `LogoutCapabilities`. `:296-310` and `:386-397` are the `authenticate` and
  `logout` request bodies: a `methodId` and nothing else.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/agent-client-protocol-schema/src/v1/client.rs:2130-2139`
  — `clientCapabilities.auth.terminal` is a bool in v1.
- `~/src/references/repos/third-party/protocol/zed-acp/crates/agent_servers/src/acp.rs:767-793`
  — the interoperability oracle. Zed initializes at v1 (`:993`) and advertises
  `auth.terminal: true`, so the terminal method is offered to it. `:1887-1922`
  runs a typed terminal method behind a beta feature flag and otherwise falls
  back to `meta_terminal_auth_task`. `:1555-1585` is that fallback: a
  `_meta["terminal-auth"]` object of `{label, command, args, env}` on any
  method, which Zed runs today without the flag. Recorded as the option not
  taken — it is a pre-stabilization shim keyed to a Zed-internal name, and it
  requires Ox to hand a client the path to its own binary, which the typed
  descriptor deliberately forbids.
- `internal/credentials/credentials.go:76-113` — `Set` and `Clear`, which this
  plan finally gives callers. `Set` rejects a blank key, refuses when the
  keyring is off, and re-resolves; `Clear` refuses when the environment supplies
  the credential and treats a missing entry as already cleared. `:70-74` is
  `Refresh`, which `authenticate` calls so a key stored by another process is
  seen.
- `internal/agent/session.go:56-62` — the existing `-32000` gate, whose inline
  `jrpc2.Errorf` becomes a call to the shared helper.
- `internal/openrouter/client.go:31-101` — `Stream`, whose request construction,
  status handling, and bounded error-body read `VerifyCredential` follows.
  `stream.go:25-28` is the `apiError` type that decodes OpenRouter's
  `{"error":{"code","message"}}` envelope, reused for the rejection message.
- `internal/e2e/model_test.go:156-172` — the mock model's single-path handler
  and its bearer-token check, which gains a second route. `:71-99` is
  `startModel` and `withModel`, the option that points Ox at it.
- `internal/e2e/harness_test.go:90-163` — `start`, whose scratch directory,
  environment, and seed-file setup the new one-shot command runner shares.
- `~/src/references/repos/third-party/infrastructure/go-keyring/keyring_mock.go`
  — `MockInit`, the in-process provider that makes a store with a real,
  changeable keyring available to unit tests, including tests in
  `internal/agent`.
- OpenRouter's `GET /api/v1/key`, confirmed against the live endpoint: 200 with
  a `{"data":{...}}` body for a usable key, and 401 with
  `{"error":{"message":"User not found.","code":401}}` for one it does not
  recognise. No request body, and the only thing Ox needs from the response is
  the status.

## Current state

- Relevant existing behavior: `Initialize` returns an empty `authMethods` array
  and no `auth` capability. `Methods()` registers five methods, none of them
  `authenticate`. `NewSession` gates on `a.credentials.Key() == ""` with
  `-32000`; `Prompt` has no such gate. `cmd/ox/main.go` takes no arguments and
  always starts the ACP server. `openrouter.Client` has one method, `Stream`.
- Existing patterns to follow: handlers validate their request, map validation
  to `-32602`, and log an outcome; `session.go` and `prompt.go` show one concern
  per file. `internal/credentials` owns the prose that names its own layers and
  variables. `cmd/ox` reads the environment and passes values in.
- Constraints from the current implementation: `InitializeResponse.AuthMethods`
  is `[]json.RawMessage`, a placeholder that has to become a typed list.
  `acp.ClientCapabilities` has no `auth` field, so the terminal method's gate
  has nothing to read yet. The store caches, so `authenticate` must call
  `Refresh` before deciding.
- The end-to-end harness sets `OX_KEYRING_DISABLED=1`, so nothing driven through
  the binary can store or delete a keyring entry. A successful `logout`, and
  therefore the prompt gate it enables, is reachable only where go-keyring's
  in-memory provider can be installed — in unit tests.
- The harness leaves `OX_OPENROUTER_BASE_URL` unset unless a test passes
  `withModel`. Every test that prompts passes it today, so nothing reaches
  openrouter.ai; `authenticate` adds a second way to reach the provider and
  makes that reliance on convention worth removing.
- `internal/acp/types_test.go:49-82` pins the whole `initialize` response shape,
  and `internal/e2e/ox_test.go:47-49` asserts `authMethods` is an empty array.
  Both change with the advertisement.

## Structural considerations

- **Hierarchy:** `cmd/ox` owns what the process does when it starts and
  continues to be the only reader of the environment. `internal/agent` owns the
  ACP surface: which methods exist, which error codes they return, and when the
  credential is consulted. `internal/credentials` keeps owning where a
  credential lives and which source wins, and learns nothing about ACP.
  `internal/openrouter` gains one request and no knowledge of who calls it.
- **Abstraction:** the store still returns a key and a source. Deciding that a
  missing credential is `-32000` stays in the agent, and deciding that a
  rejected credential is also `-32000` joins it there. The provider client
  reports what OpenRouter said and does not translate it into a protocol code.
- **Modularization:** one new file per concern — `internal/agent/auth.go` for
  the authentication surface and `cmd/ox/login.go` for the second process mode.
  No new package: the login flow is terminal input and output wired to the
  store, which is what a command is for, and a package holding one function that
  reads a line would be a nano-module.
- **Encapsulation:** the key stays inside the process that owns it. The ACP
  surface exposes only whether authentication succeeded, never the credential or
  the key material behind it, and the keyring entry names stay unexported behind
  `NoCredentialMessage`. `VerifyCredential` takes the key it should test rather
  than reaching for the store, so the provider client keeps having no opinion
  about where credentials come from.
- **Testability:** every decision is observable at a stable boundary. The
  advertised methods, the error codes, and the provider's rejection are visible
  over stdio to the end-to-end client; the login flow's input and output are
  parameters; the credential-removal path is reachable in unit tests through
  go-keyring's in-memory provider without touching the developer's keyring.

## Refactoring

- Extract the scratch directory, environment, and seed-file setup from `start`
  in the end-to-end harness so both the ACP server and a one-shot command run
  against identical surroundings. Sequence this before the `ox login` tests.
- Split `cmd/ox/main.go` into argument dispatch and a `serve` function holding
  what `main` does today, so the login mode is a sibling rather than a branch
  inside the server path.
- Replace `InitializeResponse.AuthMethods []json.RawMessage` with a typed
  `[]AuthMethod`. The placeholder exists only because there was nothing to put
  in it.
- Move the `OX_KEYRING_DISABLED` sentence out of `Store.Set` into an exported
  `credentials.KeyringDisabledMessage` that `Set` and the login flow both use,
  and add `Store.KeyringDisabled` so the login flow asks the store whether it
  can store a key rather than reading the environment itself.

## Test plan

- **Key behaviors to verify:**
  - Advertisement. `initialize` returns the stored-credential method and
    `agentCapabilities.auth.logout`. A client that sets
    `clientCapabilities.auth.terminal` also gets the terminal method, carrying
    `"type": "terminal"` and `"args": ["login"]`; a client that does not, or
    that sends no capabilities at all, gets only the stored-credential method.
  - `authenticate` succeeds. With a credential the provider accepts, the result
    is an empty object, the bearer token the provider saw is the configured key,
    and the following `session/new` succeeds.
  - `authenticate` with no credential returns `-32000` naming
    `OPENROUTER_API_KEY`, the keyring service, and the keyring account, and no
    credential check reaches the provider.
  - `authenticate` with a rejected credential returns `-32000` carrying the
    provider's message, and a later `session/new` fails with the same message
    rather than one telling a person who has set `OPENROUTER_API_KEY` to set it.
  - `authenticate` with a provider that fails for another reason returns
    `-32603` and does not claim authentication is required.
  - `authenticate` rejects an unknown method ID and the terminal method ID with
    `-32602`, and the terminal refusal says the method is completed in a
    terminal. A request with no `methodId`, and one with no params at all, are
    also `-32602`.
  - `authenticate` picks up a key stored after Ox started, resolved through
    `Refresh` rather than the cache.
  - `logout` deletes the keyring entry and reports success; a second `logout`
    with nothing stored also succeeds.
  - `logout` refuses with `-32603` naming `OPENROUTER_API_KEY` while the
    environment supplies the credential, and the keyring entry survives.
  - After a successful `logout`, an existing session still exists, and both
    `session/prompt` in it and a new `session/new` return `-32000`. No request
    reaches the provider.
  - `ox login` reads a key from its input, verifies it, stores it in the
    keyring, reports where it went without printing the key, and exits zero.
  - `ox login` refuses a blank or whitespace-only key, a key the provider
    rejects, a provider that fails for another reason, and an unreachable
    provider, exits nonzero, and leaves the keyring unchanged.
  - `ox login` refuses before prompting when keyring access is off, so nothing
    is read from its input.
  - `ox login` reports that `OPENROUTER_API_KEY` still takes precedence over the
    key it just stored.
  - `ox` with an unrecognised argument exits 2 with a usage message and does not
    serve ACP.
  - `VerifyCredential` sends `GET /key` with the key it was given as the bearer
    token, works with no `APIKey` accessor configured, returns
    `ErrCredentialRejected` wrapping the provider's message for 401 and 403, and
    returns a plain error naming the status for anything else.
- **Test levels:** the advertised methods, every error code and message a client
  sees, and the exit statuses and streams of `ox login` are end to end, because
  they are the contract. `VerifyCredential` is a unit test against an
  `httptest.Server`. The login flow is a unit test in `cmd/ox` driven through a
  pipe and an `httptest.Server`, with go-keyring's in-memory provider installed,
  because a terminal cannot be manufactured and the shipped binary must not
  write a real keyring. Everything that needs a credential to disappear while Ox
  runs — a successful `logout`, the prompt gate it enables, and `authenticate`
  seeing a key stored out of band — is a unit test in `internal/agent` built on
  the same in-memory provider.
- **Edge cases and failure modes:** `authenticate` while a turn is running in
  another session, which must not disturb it; `logout` while a turn is running,
  after which the turn finishes normally because its request already carries the
  credential; a whitespace-padded key typed into `ox login`, which is trimmed
  before it is verified and stored; a `login` argument arriving after
  client-configured arguments, which is what a client appends to.
- **What not to test:** that go-keyring reaches each platform's store; that the
  real keyring is consulted from the shipped binary; that a real client runs the
  terminal flow and that an interrupt at the key prompt leaves the terminal
  echoing, both of which need a real terminal and are manual verification; the
  wording of log lines beyond the absence of the key from them.

## Implementation plan

1. Add the dependency by running `go get golang.org/x/term` so the tool records
   the resolved version.
2. In `internal/acp/types.go`, add `AuthMethod` with `ID`, `Type` (omitted when
   empty), `Name`, `Description`, and `Args`; `AgentAuthCapabilities` with a
   `*LogoutCapabilities`; `ClientAuthCapabilities` with `Terminal`;
   `AuthenticateRequest` with `MethodID`; and empty `AuthenticateResponse`,
   `LogoutRequest`, and `LogoutResponse` structs. Add `Auth` to
   `AgentCapabilities` and to `ClientCapabilities`, and retype
   `InitializeResponse.AuthMethods`.
3. In `internal/acp/validate.go`, add `AuthenticateRequest.Validate`, requiring
   `methodId`. Update `internal/acp/types_test.go` for the new response shape,
   including the two capability objects.
4. Add `internal/agent/auth.go`: the two method-ID constants, `authMethods`
   taking the client's capabilities and returning the advertised list,
   `Authenticate`, `Logout`, and `authRequiredError`. Register `authenticate`
   and `logout` in `Methods()`, and have `Initialize` call `authMethods` and
   advertise `auth.logout`.
5. Replace the inline `-32000` construction in `NewSession` with
   `authRequiredError`, and add the same gate to `Prompt` after the session
   lookup.
6. Add `openrouter.ErrCredentialRejected` and
   `Client.VerifyCredential(ctx, key)` in `internal/openrouter/client.go`,
   issuing `GET /key`, treating 2xx as success, mapping 401 and 403 to a wrapped
   rejection carrying the decoded provider message, and returning a plain error
   for anything else. Add its unit tests.
7. Export `credentials.KeyringDisabledMessage` and use it from `Set`.
8. Add `cmd/ox/login.go`: the login flow taking the input file, the output
   writer, the store, and the client; refusing before prompting when the keyring
   is off; reading without echo when the input is a terminal and as a line when
   it is not, restoring the terminal on an interrupt; trimming; verifying;
   storing; and reporting where the key went and whether the environment still
   wins.
9. Split `cmd/ox/main.go` into argument dispatch and `serve`, wiring `login` and
   exiting 2 on an unrecognised argument.
10. Add `cmd/ox/login_test.go` covering the login-flow items in the test plan
    against go-keyring's in-memory provider and an `httptest.Server`.
11. Add the `internal/agent` unit tests for the credential-removal path,
    advertisement gating, and the handlers' error mapping, building a store on
    the in-memory provider.
12. In the harness, extract the shared scratch and environment setup, default
    `OX_OPENROUTER_BASE_URL` to a refused local address with a comment recording
    why, and add a one-shot command runner returning the exit status, standard
    output, and standard error.
13. In the mock model, route `GET /key` beside the chat completions path, share
    the bearer-token check between them, record the token each credential check
    presented, and add a way to script a rejection.
14. Add `internal/e2e/auth_test.go` with the end-to-end items in the test plan,
    and update the `authMethods` assertion in `internal/e2e/ox_test.go`.

## Documentation updates

- `AGENTS.md`: check off the `authenticate` roadmap item and name it
  `authenticate` and `logout`, since both land together.
- `AGENTS.md` ACP method coverage: check `authenticate` and add a checked
  `logout` to the requests Ox accepts.
- `AGENTS.md` Configuration section: state that `ox login` stores a key in the
  keyring, that `authenticate` re-reads both sources and verifies the credential
  with OpenRouter, and that `logout` deletes the keyring entry and is refused
  while `OPENROUTER_API_KEY` supplies the credential.
- `AGENTS.md` Tests section: note that the end-to-end harness points Ox at a
  refused provider address unless a test starts the mock model.

## Impact assessment

- Code paths affected: `internal/acp` types and validation, `internal/agent`'s
  method table and both credential gates, one new provider request, `cmd/ox`'s
  startup, one extracted constant in `internal/credentials`, and the end-to-end
  harness and mock model. The prompt turn's streaming, the session store, and
  the configuration package are untouched.
- Data, protocol, or schema impact: two new methods Ox accepts, `authenticate`
  and `logout`. The `initialize` response gains `authMethods` entries and
  `agentCapabilities.auth.logout`, and Ox begins reading
  `clientCapabilities.auth.terminal`. `session/prompt` gains `-32000` as a
  possible failure. One new provider endpoint, `GET /key`. Ox becomes a program
  with a subcommand, so an unrecognised argument now exits 2 where it previously
  started a server.
- Dependency or API impact: one direct dependency, `golang.org/x/term`, for
  reading a key without echoing it and for recognising a terminal.
  `golang.org/x/sys`, which it needs, is already in the module graph.
- Deliberately deferred or excluded, and none of it half-built here: accepting a
  key inside an ACP message through a private `_meta` extension, which no client
  of Ox's would send and which would put a secret in a client's logs; Zed's
  pre-stabilization `_meta["terminal-auth"]` descriptor, which needs Ox to hand
  a client the path to its own binary; elicitation, which the protocol forbids
  for API keys in form mode and which would need Ox to host an HTTP endpoint in
  URL mode; re-resolving the credential inside `session/new`, since
  `authenticate` is the method for that and a keyring read is a subprocess;
  verifying the credential per prompt; cancelling running turns on `logout`; and
  more than one provider or account, which would turn the keyring account into a
  key rather than a constant.

## Validation

- Tests to write and run: `go test -race -count=1 ./...`. The end-to-end binary
  is built with `-race` and `GORACE=halt_on_error=1`, so `authenticate` and
  `logout` racing against running turns are checked as well.
- Static checks: `make check`, which runs `gofmt`, `go vet ./...`,
  `staticcheck ./...`, and `dprint check`.
- Manual verification: with `OPENROUTER_API_KEY` unset, `gpt-5.6-luna`
  configured, and no keyring entry, drive `initialize` and confirm both methods
  are advertised when the client sends `clientCapabilities.auth.terminal` and
  only one when it does not; confirm `session/new` fails `-32000` and
  `authenticate` fails the same way. Run `ox login` in a terminal, confirm the
  typed key is not echoed, interrupt it at the prompt and confirm the shell
  echoes again, confirm a deliberately mistyped key is refused with OpenRouter's
  reason and not stored, then store the real key. Without restarting Ox, call
  `authenticate`, confirm it succeeds, and confirm `session/new` and
  `session/prompt` then stream an answer. Call `logout`, confirm the keychain
  entry is gone, and confirm the existing session's next prompt fails `-32000`.
  Export `OPENROUTER_API_KEY` and confirm `logout` refuses while naming it.
