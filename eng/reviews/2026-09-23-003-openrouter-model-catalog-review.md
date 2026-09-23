# OpenRouter model catalog review

## Scope and coverage

Reviewed the current working tree diff for catalog loading, settings, model and
effort selection, request encoding, related tests, and associated documentation.
Read the draft plan as context. Used correctness, error handling, resources,
API design, testing, documentation, and Rust idioms lenses. I checked the public
OpenRouter model response and API documentation, but did not run a live Ox prompt
or simulate a stalled OpenRouter connection.

## Findings

### Medium

#### Error handling

- **A stalled catalog response can prevent Ox from starting**
  (`src/openrouter.rs:132`): `fetch_catalog` creates an async reqwest client
  without a timeout, then awaits both the response headers and the complete
  response body. Both ACP serving and `ox run` await this function before doing
  any work (`src/main.rs:173`). A peer that accepts a connection but stops
  sending data can therefore leave startup waiting indefinitely. Reqwest's
  [async client documentation](https://docs.rs/reqwest/latest/reqwest/struct.ClientBuilder.html#method.timeout)
  confirms that its default is no timeout. Set a total deadline on this catalog
  request so startup returns an actionable error when it stalls.

## Follow-up

The user confirmed the 183 day cutoff is intentional. The plan now records it.
The catalog request now has a 15-second total timeout; the finding above records
the behavior at review time.

## Checks run

- `cargo fmt --all -- --check` — passed.
- `cargo test --all-targets --all-features` — passed (96 tests).
- `cargo build --all-features` — passed.
- `cargo clippy --all-targets --all-features -- -D warnings` — passed.
- Both example unittest commands from `eng/testing.md` — passed (1 goal test,
  4 careful tests).
- `git diff --check` — passed.
- Read OpenRouter's [current public model response](https://openrouter.ai/api/v1/models)
  and [model list documentation](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties).

## Verdict

The startup deadline finding was fixed after the review. The age cutoff is an
intentional catalog rule.
