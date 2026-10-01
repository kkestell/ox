# Testing

`make check` runs the tests and the other checks.

## Where tests go

- **Automated tests**: in-module `#[cfg(test)]` tests. Model requests go to a
  scripted OpenRouter or OpenAI test fixture, so tests need no account, API key,
  or external network. OpenAI authorization tests use a local scripted service.
- **Live checks**: `scripts/run.py '<prompt>'` runs one headless prompt in a
  temporary workspace and requires an OpenRouter key in `.env`. Use
  `ox run --dir <workspace> '<prompt>'` for OpenAI with the global provider set
  to `openai`. OpenAI checks require explicit sign-in and a completed inference
  request; sign-in or catalog discovery alone does not verify plan access. For
  behavior only an ACP client shows, connect a client to a local build.
- **Client tests**: `crates/ox/` has in-process ACP and terminal logic tests.
  `make e2e` runs the isolated tmux tests against the Ox server and scripted
  OpenRouter test fixture.

## Test discipline

The test suite is curated code. Each test owns one durable, observable
guarantee, and the suite changes as the guarantees change.

- Give each guarantee one owning test, named for that guarantee. A test that
  checks unrelated guarantees is split so each failure names what broke.
- A change adds a test for each new guarantee and for each reproduced
  regression, at the closest stable boundary. It rewrites or deletes the tests
  for guarantees it changes or removes, in the same change.
- Test a guarantee once. Unit, orchestration, ACP, and end-to-end tests repeat
  an assertion only when those layers have distinct failure modes.
- Use table-driven cases for one behavior over varied inputs. Each case states
  its input and expected result, and a failure message identifies the case.
- Keep setup proportional to the guarantee. Shared fixtures and helpers hold
  setup that several tests need, so each test body reads as its guarantee.
- Test observable behavior. Internals change freely while the guarantees they
  serve hold.
- Before finishing any change that touches tests, inspect the complete test diff
  and report which guarantees gained, lost, or moved their owning tests.
