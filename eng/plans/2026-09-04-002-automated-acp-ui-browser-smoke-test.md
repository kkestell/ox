# Automated ACP UI Browser Smoke Test

## Goal

Replace the unautomatable Zed checklist with a real client interoperability
test. The test must exercise Ox through independently maintained client code,
not an ACP client implemented inside this repository.

## Desired outcome

`make test-client` launches the real Ox binary behind the upstream
`@rebornix/stdio-to-ws` bridge, serves the upstream ACP UI web application, and
drives its visible controls with Playwright. The suite proves initialization,
use of the configured OpenRouter credential, session creation, incremental
prompt rendering, cancellation propagation, and process cleanup without
contacting OpenRouter.

## Summary of approach

Pin the published ACP UI site as an unmodified git submodule and pin
`@rebornix/stdio-to-ws` plus Playwright in a test-only npm lockfile. A small
Node harness will build Ox into a temporary directory, provide a deterministic
fake OpenRouter server, serve the static client, start the bridge with an
isolated Ox environment, and own cleanup. Playwright will configure the remote
agent in ACP UI browser storage, then perform the lifecycle through the UI. The
harness will not parse, construct, proxy, or assert ACP messages.

## Related code

- `internal/e2e/harness_test.go` and `internal/e2e/model_test.go` - Existing
  process isolation, provider routes, queued SSE, and held-response behavior to
  reproduce in the browser harness.
- `internal/e2e/auth_test.go` and `internal/e2e/prompt_test.go` - Existing
  protocol-level expectations that the independent-client smoke test must
  observe through UI and provider effects.
- `formulahendry/acp-ui@e6e36d05` - Upstream client source for authentication
  and session flow in `src/stores/session.ts`, cancellation in
  `src/lib/acp-bridge.ts`, and browser transport in
  `src/lib/transport/websocket.ts`. Its published artifact is
  `acp-ui/acp-ui.github.io@4482f93a`.
- `personal/delta/web/e2e/harness.ts` and `web/playwright.config.ts` - Prior art
  for browser-process orchestration and failure diagnostics, not client code.

## Structural considerations

Keep the suite under `internal/e2e/browser`; it remains test-only and talks to
Ox only through the shipped process boundary. ACP UI owns client semantics, the
upstream bridge owns transport adaptation, and Ox's harness owns only fixtures
and lifecycle. No production package or protocol API changes.

## Test plan

- Start each test with fresh browser storage, filesystem roots, ports, provider,
  bridge, and Ox process.
- From ACP UI, create a session with an isolated configured OpenRouter API key
  and assert the fake provider receives it before the session becomes usable.
  Explicit `authenticate` remains covered at the protocol boundary: ACP UI only
  opens its auth picker after a missing-key error, when Ox has no key to verify.
- Send a prompt whose response is released in two stages. Assert the first text
  is visible while the provider request remains open, then release it and assert
  the completed answer.
- Hold a second prompt after one chunk, click ACP UI's Cancel control, and
  assert the provider request is cancelled and the composer becomes usable
  again.
- Click Disconnect and assert the bridge leaves no Ox process behind. On
  failure, retain browser screenshots and include Ox/bridge stderr.
- Do not duplicate malformed-message, wire-shape, or provider retry cases
  already covered by Go tests.

## Implementation plan

- Add `.gitmodules` and `internal/e2e/browser/acp-ui/` pinned to the published
  upstream ACP UI artifact. Add the test-only npm manifest and lockfile with
  exact bridge and Playwright versions.
- Add `internal/e2e/browser/harness.ts` for temporary roots, free ports, static
  hosting, the three fake OpenRouter endpoints, Ox build/environment setup,
  bridge startup, PID tracking, and bounded cleanup.
- Add `internal/e2e/browser/lifecycle.spec.ts` and `playwright.config.ts` with
  the authentication/streaming and cancellation/shutdown cases above.
- Add `test-client` to `Makefile`. It initializes the pinned submodule, installs
  locked npm dependencies and Chromium, and runs the browser suite.

## Documentation updates

- Add `make test-client` to `AGENTS.md` when the command exists.
- Mark the automated browser-client smoke-test item complete in `eng/roadmap.md`
  and record the exercised ACP UI, bridge, and schema pins.

## Validation

- Run `make test-client` twice to catch leaked ports, processes, or browser
  state.
- Run `make check` to preserve the existing Go and documentation gates.
