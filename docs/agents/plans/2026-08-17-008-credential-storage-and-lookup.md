# 2026-08-17-008. Credential storage and lookup

## Goal

Ox has exactly one place a credential can come from: `main.go` reads
`OPENROUTER_API_KEY` out of the process environment and copies it into
`openrouter.Client.APIKey`, once, at startup. `NewSession` refuses a session
when that string is empty.

Two things are wrong with that. An ACP agent is launched by an editor, not by a
shell, so the environment Ox inherits is the editor's environment, and a person
who wants Ox to work has to arrange for their editor to carry a secret. There is
no answer to "where do I put my key" that is not "restart your editor with the
right variable set."

The second problem is that the credential is a `string` copied at startup, so
nothing can change it while Ox runs. That is the shape that makes `authenticate`
impossible: a client that hands Ox an API key needs somewhere durable to put it
and needs the very next provider request to use it.

## Desired outcome

Ox resolves an OpenRouter credential from `OPENROUTER_API_KEY` or from the OS
keyring, and the environment wins when both have one. The keyring entry is
service `ox`, account `openrouter`, which is the macOS Keychain, the Linux
Secret Service, or the Windows Credential Manager depending on where Ox is
running.

A credential can be written to the keyring and deleted from it, so a key
supplied once outlives the process that received it. Deleting is refused while
`OPENROUTER_API_KEY` supplies the credential, because deleting a keyring entry
would not change which credential Ox is using and would report success for
something that did not happen.

The credential is resolved once and cached, and re-resolved on demand. Reading
it is safe from every concurrent turn. It is read per provider request rather
than frozen onto a session, so a credential that changes applies to the sessions
that already exist.

When no credential is available anywhere, `session/new` fails with ACP's
`auth_required` code, `-32000`, and a message that names both
`OPENROUTER_API_KEY` and the keyring entry Ox looked for.

`OX_KEYRING_DISABLED=1` turns off keyring access entirely, for a machine with no
usable keyring and for Ox's own end-to-end tests, which must never read or write
the developer's real keyring.

The credential never appears in a log line. Only the layer that supplied it is
logged.

## Summary of approach

A new `internal/credentials` package owns the credential: where it can come
from, which source wins, and how it is written and deleted. It exports a `Store`
holding the resolved key and its source behind a mutex, with `Key`, `Source`,
`Refresh`, `Set`, and `Clear`, plus a `Source` type whose values read as message
fragments and a `NoCredentialMessage` const that tells a user how to supply one.

The keyring is `github.com/zalando/go-keyring`, which reaches each platform's
native store without cgo and ships an in-memory provider for tests. On macOS it
shells out to `/usr/bin/security`, so a keyring read costs a subprocess. That is
why the `Store` caches: a read per provider request would be a process spawn per
provider request. The cache is re-resolved by `Refresh`, and by `Set` and
`Clear` after they change the keyring, so there is exactly one function that
decides which source wins and every path runs it.

The package never calls `os.Getenv`. `cmd/ox/main.go` reads `OPENROUTER_API_KEY`
and `OX_KEYRING_DISABLED` and passes them to `NewStore`, which is the rule
`internal/config` already follows. The two inputs are a `string` and a `bool`
rather than an `Environment` struct, because the reason `config.Environment`
exists is that its two values are both strings and could be passed in the wrong
order; these two cannot.

`openrouter.Client.APIKey` changes from `string` to `func() string` and the
command wires it to `store.Key`, so each request reads the credential that is
current when the request is built. The client sets the header from whatever the
function returns and does not judge the value; whether a credential exists at
all is the agent's gate, and one owner for that rule is enough. A nil function
is a wiring bug rather than a state, so the client panics on it.

`Agent` holds the store and `NewSession` gates on it, replacing the
`a.client.APIKey == ""` check. The gate returns `-32000` rather than the
`-32603` it returns today, because `auth_required` is what the protocol defines
for this and Zed maps that code to an authentication prompt carrying the agent's
message as its description. That is the right behavior even before
`authenticate` exists: the message is what tells a user to set the variable or
store a key.

The credential is deliberately not frozen onto the session the way the model is.
A session's model is frozen because two workspaces in one process legitimately
want different models. There is no such thing as a per-session credential: ACP
authenticates a connection, not a session, and a credential that arrives after a
session exists has to apply to it.

The end-to-end harness gains two things. `OX_KEYRING_DISABLED=1` joins its
default environment, so the test binary never touches the developer's keyring.
And the mock model records the `Authorization` header it received instead of
asserting a fixed value, which is what makes "the credential reached the
provider" observable at the process boundary the same way `requestFor` already
makes the model observable.

## Related code

- `~/src/references/repos/personal/alpha/runtime/internal/credentials/credentials.go:33-48`
  — the `Store`: a cached key and source behind a `sync.RWMutex`, resolved once
  by the constructor. `:109-137` is `resolve`, the one function that decides
  which source wins, with a keyring error degraded to no credential and logged
  as a warning rather than failing the process. `:68-87` is `Set`, which rejects
  a blank key, refuses when the keyring is disabled, and re-resolves after
  writing. `:89-107` is `Clear`, which refuses when the environment supplies the
  credential and treats a missing entry as already cleared. `:14-18` are the
  service, account, and disable-variable constants. Ox's store is this file with
  the environment reads lifted to the command.
- `~/src/references/repos/personal/alpha/runtime/cmd/amber-runtime/main.go:28` —
  `&openrouter.Client{APIKey: credentialStore.Key, ...}`, the whole mechanism
  that makes a credential change take effect without a restart. `:44-51` logs
  `credential_source` at startup and never the key.
- `~/src/references/repos/personal/alpha/runtime/internal/openrouter/client.go:195-206`
  — the client resolves the key per request through a nil-guarded accessor;
  `catalog_test.go:98` is the test that pins that behavior.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/agent.go:615-617`
  — the session-creation gate, `a.credentials.Key() == ""` yielding
  `authRequiredError()`, which `:783-785` defines as
  `jrpc2.Errorf(acp.ErrCodeAuthRequired, ...)`. `:207-233` is the `Authenticate`
  handler that calls `Set` and `Refresh`; it is the next roadmap item and is
  recorded here only to confirm this store is the shape it needs.
- `~/src/references/repos/personal/alpha/runtime/internal/credentials/credentials_test.go:117-135`
  — the test that a keyring failure degrades reads, fails writes, and does not
  leak the key into the log, using a mutex-guarded buffer as the log sink.
  `:137-159` hammers concurrent `Key`/`Source` reads against concurrent `Set`
  calls, which is what proves the lock under `-race`.
- `~/src/references/repos/personal/eta/internal/llm/keychain.go:14-31` — the
  narrower shape: `ErrNotFound` becomes `("", nil)` because a missing entry is
  not a failure, and any other keyring error is surfaced. `providers.go:56-68`
  resolves the environment first and ends with an error naming both the variable
  and the command that would fix it.
- `~/src/references/repos/personal/theta/cmd/ox/main.go:49-53` and
  `~/src/references/repos/personal/theta/internal/config/config.go:233-262` —
  the `.env` approach, a global `$XDG_CONFIG_HOME/ox/.env` plus the nearest
  `.env` at or above the current directory. Recorded as the option this plan
  does not take, and why: Theta is a terminal program the user starts inside a
  project, so its working directory is the workspace. Ox's working directory is
  whichever directory its client happened to launch it in, and Ox does not learn
  a workspace until `session/new`.
- `~/src/references/repos/third-party/coding-agents/crush/internal/config/config.go:100`
  — the other option not taken: an `api_key` field in the configuration file,
  holding a `$VAR` reference resolved at load. Ox's configuration file
  deliberately cannot name a credential.
- `~/src/references/repos/third-party/infrastructure/go-keyring/keyring.go:12-35`
  — the package surface: `Get`, `Set`, `Delete`, and `ErrNotFound`.
  `keyring_darwin.go:43-54` shells out to
  `/usr/bin/security
  find-generic-password`, which is why reads are cached and
  why a read is a subprocess. `keyring_mock.go` has `MockInit` and
  `MockInitWithError`, an in-memory provider swapped in by assignment to a
  package variable, which is a unit-test seam and not a cross-process one. That
  variable is never handed back to the platform provider, so a mock installed
  once holds for the rest of a test binary.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/agent-client-protocol-schema/src/v1/error.rs:179-183`
  — `AuthRequired` is `-32000`. `docs/protocol/v1/draft/session-setup.mdx:10`
  states that `session/new` may fail with it until the client authenticates, and
  `docs/protocol/v1/authentication.mdx:111` states that authentication is what
  stops it.
- `~/src/references/repos/third-party/protocol/zed-acp/crates/agent_servers/src/acp.rs:2074-2082`
  — the interoperability oracle: Zed turns `-32000` into an `AuthRequired` error
  and attaches the agent's message as its description, so a client that has no
  `authMethods` to offer still shows a user what Ox said.

## Current state

- Relevant existing behavior: `cmd/ox/main.go:45` reads `OPENROUTER_API_KEY`
  into `openrouter.Client.APIKey`. `client.go:57` sets `Authorization` from that
  field on every request. `session.go:55-60` rejects `session/new` with `-32603`
  and `"no API key is configured: set OPENROUTER_API_KEY"` when the field is
  empty. Those three sites are every use of the credential.
- Existing patterns to follow: `internal/config` is the model for this package.
  It owns one question, takes environment values as arguments instead of reading
  them, returns a value rather than a policy, and leaves the JSON-RPC error code
  to the agent. Its `Source` type exists so a log line and an error message can
  say which layer won.
- Constraints from the current implementation: `Agent` is constructed with
  positional parameters and gains a sixth. The list stays readable at six
  because every type is distinct; a `Config` struct is what to write when tools
  and a session store arrive, not now.
- The mock model asserts `Authorization == "Bearer test-key"` at
  `model_test.go:166-170` and fails the test otherwise. Any end-to-end test that
  varies the credential has to change that assertion into a recording.
- The harness already sets `OPENROUTER_API_KEY=test-key` in its default
  environment, so the keyring would never be consulted in most tests — but the
  tests that clear the variable to check the failure path would consult it, and
  on a machine where a developer has stored a real Ox key those tests would find
  it and pass for the wrong reason, or fail.
- `TestDebugLoggingStaysOffStdout` in `internal/e2e/ox_test.go` establishes how
  to assert on Ox's log output: stop the child first, then read `child.stderr`.
  Reading that buffer while the process runs would race with the goroutine
  `exec` uses to fill it.

## Structural considerations

- **Hierarchy:** `cmd/ox` owns the knowledge that `OPENROUTER_API_KEY` and
  `OX_KEYRING_DISABLED` exist and wires the store to the client.
  `internal/credentials` owns the keyring entry, the precedence rule, and the
  cache, and depends on nothing of Ox's. `internal/agent` owns when the gate
  runs and what a missing credential looks like on the wire.
  `internal/openrouter` gains a dependency on a function type, not on the
  credentials package.
- **Abstraction:** the store returns a key and a source. It does not decide an
  error code, does not know a session exists, and does not know an ACP method
  will call `Set`. The one piece of user-facing prose it owns is
  `NoCredentialMessage`, for the same reason `config` owns the wording of its
  not-configured error: the package that knows the layers knows how to name
  them.
- **Modularization:** one new package with one file. The keyring is not wrapped
  in an interface of Ox's own, because go-keyring already has the seam its tests
  need and a second indirection would exist only to be mocked.
- **Encapsulation:** the key and its source are unexported fields behind a
  mutex, reachable only through methods, so no caller can read a half-updated
  pair or install a key without going through resolution. The service and
  account names stay unexported; `NoCredentialMessage` is the one place they
  reach a user.
- **Testability:** `NewStore` takes the environment values as arguments, so unit
  tests set no process environment and the only global state in play is
  go-keyring's provider, swapped by `MockInit` and restored by `t.Cleanup`.
  Everything a user sees — the error code and message from `session/new`, the
  bearer token the provider received, the absence of the key from the logs — is
  observable at the process boundary.

## Refactoring

- `openrouter.Client.APIKey` becomes `func() string`. This is a prerequisite for
  the credential being mutable state rather than a startup constant, and it is
  the only change to the client.
- `agent.New` gains a `credentials *credentials.Store` parameter and `Agent`
  gains the matching field. `testAgent` in `internal/agent/agent_test.go`
  constructs a store with no credential and a disabled keyring, which is what
  its existing assertions need and which keeps unit tests off the keyring.

## Test plan

- **Key behaviors to verify:**
  - Precedence. A key in the keyring alone is used and reported as
    `SourceKeyring`; `OPENROUTER_API_KEY` overrides it and is reported as
    `SourceEnvironment`; neither present is `SourceNone` and an empty key.
  - Blank values. A blank or whitespace-only `OPENROUTER_API_KEY` is not a
    credential and falls through to the keyring, matching how an unset variable
    behaves. A whitespace-padded key from either source is trimmed.
  - The cache. A keyring entry changed behind the store's back does not change
    what `Key` returns until `Refresh` runs. This is the behavior that makes a
    key read per provider request affordable.
  - Storing. `Set` writes the keyring, re-resolves, and reports `SourceKeyring`.
    `Set` with a blank or whitespace-only key is refused and writes nothing.
    `Set` while the environment supplies the credential still writes the keyring
    but leaves the resolved credential and source alone, because the environment
    still wins.
  - Deleting. `Clear` deletes the entry and resolves to no credential. `Clear`
    with nothing stored succeeds. `Clear` while `OPENROUTER_API_KEY` supplies
    the credential fails, leaves the keyring untouched, and leaves the resolved
    credential in place.
  - Keyring disabled. With `OX_KEYRING_DISABLED`, the keyring is never read, an
    environment credential still resolves, `Set` fails with a message naming the
    variable, and `Clear` succeeds without touching the keyring.
  - Keyring failure. A keyring that errors on every operation resolves to no
    credential rather than failing construction, surfaces the error from `Set`,
    and never writes the key into a log line.
  - Concurrency. Concurrent `Key` and `Source` reads against concurrent `Set`
    calls are race-free.
  - `session/new` with no credential fails `-32000` with a message naming
    `OPENROUTER_API_KEY`, the keyring service, and the keyring account, and Ox
    keeps answering afterwards.
  - The credential reaches the provider. A prompt sends `Authorization: Bearer`
    followed by the configured key, asserted from what the mock model recorded
    rather than from a fixed expectation.
  - The credential does not reach the logs. With debug logging on, a session and
    a prompt driven to completion, Ox's stderr does not contain the key.
- **Test levels:** everything about precedence, caching, writing, deleting, and
  keyring failure is a unit test in `internal/credentials` against
  `keyring.MockInit`, because the keyring is the thing being decided about and
  its in-memory provider is an in-process seam. The error code and message, the
  bearer token, and the log contents are end-to-end, because they are what a
  client and a provider actually see.
- **Edge cases and failure modes:** a keyring entry holding only whitespace,
  which is not a credential; `Set` followed immediately by `Key` from another
  goroutine; `Clear` when the entry was already deleted out of band; a keyring
  error on `Get` while a credential is already cached, which must leave the
  store with no credential rather than a stale one.
- **What not to test:** that go-keyring talks to each platform's store, which is
  its own test suite's job; that the real keyring is consulted from the
  end-to-end binary, since that would read whatever the developer running the
  tests has stored; the wording of log lines, beyond the absence of the key from
  them.

## Implementation plan

1. Add the dependency by running `go get github.com/zalando/go-keyring` so the
   resolved version is written by the tool.
2. Add `internal/credentials/credentials.go`: the package doc stating the two
   sources and their order; the unexported `service` and `account` constants;
   the exported `NoCredentialMessage`; `Source` with `SourceNone`,
   `SourceEnvironment`, and `SourceKeyring` whose values read as message
   fragments; and `Store` with the logger, the mutex, the cached key, the cached
   source, the environment key, and the disabled flag.
3. Add
   `NewStore(apiKey string, keyringDisabled bool, logger *slog.Logger)
   *Store`,
   resolving once before returning, and `Key`, `Source`, and `Refresh`.
4. Add the unexported `resolve`, which is the only place precedence lives: the
   trimmed environment key wins; a disabled keyring is no credential; a keyring
   entry is trimmed and used; `ErrNotFound` and a blank entry are no credential;
   any other keyring error is no credential plus a warning that names the error
   and not the key.
5. Add `Set`, rejecting a blank key, refusing when the keyring is disabled,
   writing through `keyring.Set`, then re-resolving. Add `Clear`, refusing when
   the environment supplies the credential, tolerating `ErrNotFound` from
   `keyring.Delete`, then re-resolving.
6. Add `internal/credentials/credentials_test.go` covering the unit-level items
   in the test plan. Every test installs the in-memory provider with
   `keyring.MockInit` or `keyring.MockInitWithError` before touching the
   keyring, so no test in the package reaches a real keyring. The no-leak
   assertion uses a mutex-guarded buffer as the log sink.
7. Change `openrouter.Client.APIKey` to `func() string`, panicking when it is
   nil, and set the header from its result.
8. Add the `credentials` parameter to `agent.New` and the field to `Agent`, and
   update `testAgent`.
9. In `NewSession`, replace the `a.client.APIKey` check with
   `a.credentials.Key() == ""` returning
   `jrpc2.Errorf(acp.ErrCodeAuthRequired, "%s", credentials.NoCredentialMessage)`,
   and add `acp.ErrCodeAuthRequired = -32000` to `internal/acp/types.go` beside
   `ErrCodeRequestCancelled`.
10. In `cmd/ox/main.go`, build the store from `os.Getenv("OPENROUTER_API_KEY")`
    and `os.Getenv("OX_KEYRING_DISABLED") == "1"`, pass `store.Key` as the
    client's `APIKey`, pass the store to `agent.New`, and add
    `credential_source` to the startup log line.
11. In the harness, add `OX_KEYRING_DISABLED=1` to the default environment with
    a comment recording that the test binary must never read or write the
    developer's keyring.
12. In the mock model, record each request's `Authorization` header on the
    recorded request instead of asserting a fixed value, keeping a check that
    the header is present and bearer-shaped, and expose it the way `Model` is
    exposed.
13. Add `internal/e2e/credentials_test.go` with the end-to-end items in the test
    plan, and split `TestNewSessionRequiresAModelAndAnAPIKey` into a model case
    that still expects `-32603` and a credential case that expects `-32000`.

## Documentation updates

- `AGENTS.md`: check off the credential storage and lookup roadmap item.
- `AGENTS.md` Configuration section: state that the OpenRouter credential comes
  from `OPENROUTER_API_KEY` or from the OS keyring under service `ox` and
  account `openrouter`, that the environment wins, that a configuration file
  still cannot carry a credential, and that `OX_KEYRING_DISABLED=1` turns
  keyring access off.
- `AGENTS.md` Tests section: note that the end-to-end harness disables keyring
  access, so credential behavior in the shipped binary is exercised through the
  environment and the keyring itself is covered by unit tests.

## Impact assessment

- Code paths affected: `cmd/ox/main.go`, `openrouter.Client`'s one field and the
  header it sets, `Agent`'s fields and constructor, `NewSession`'s gate, the
  harness environment, and the mock model's request recording. The prompt turn,
  the session's locking, the configuration package, and the ACP types are
  otherwise untouched.
- Data, protocol, or schema impact: no ACP message changes shape and no method
  is added. `session/new` changes the error code it returns for a missing
  credential from `-32603` to `-32000`, which is what a client needs to
  distinguish "authenticate" from "something broke". A new out-of-process store
  appears: one keyring entry under service `ox`, account `openrouter`, which Ox
  reads, writes, and deletes.
- Dependency or API impact: one direct dependency,
  `github.com/zalando/go-keyring`, which brings `github.com/godbus/dbus/v5` for
  the Linux Secret Service, `github.com/danieljoos/wincred` for the Windows
  Credential Manager, and `golang.org/x/sys` indirectly. No cgo. On macOS the
  keyring is reached by running `/usr/bin/security`.
- Deliberately deferred, and none of it half-built here: `authenticate` and
  `logout`, which are the ACP methods that call `Set` and `Clear` and which need
  `authMethods` advertised from `initialize`; a credential gate on
  `session/prompt`, which has nothing to guard against until a credential can be
  removed mid-connection; `.env` loading, which this plan rejects rather than
  defers, because a workspace `.env` would make the credential per-session while
  ACP authenticates a connection, and a global one would be a second way to set
  an environment variable; more than one provider or account, which would turn
  the keyring account into a key rather than a constant; and validating a key
  against OpenRouter before storing it, which needs a live request from a
  handler and belongs with `authenticate`.

## Validation

- Tests to write and run: `go test -race -count=1 ./...`. The credentials unit
  tests include a concurrent read/write case, and the end-to-end binary is built
  with `-race` and `GORACE=halt_on_error=1`, so a credential read from several
  concurrent turns is checked as well.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: with a real key stored in the login keychain via
  `security add-generic-password -U -s ox -a openrouter -w "$OPENROUTER_API_KEY"`,
  `OPENROUTER_API_KEY` unset, and `gpt-5.6-luna` configured, drive `initialize`,
  `session/new`, and `session/prompt` by hand and confirm the answer streams and
  that the startup log reports `credential_source=keyring`. Then export
  `OPENROUTER_API_KEY` and confirm the same run reports
  `credential_source=environment`. Then delete the keychain entry, unset the
  variable, and confirm `session/new` fails with `-32000` and a message naming
  both. Reading an entry created by `security` needs no keychain prompt, because
  Ox reaches the keychain by running `security` itself.
