# Retry temporary model request failures review

## Scope and coverage

Reviewed commit `b3eff12`, which retries model requests after a stall or an HTTP
429 or 5xx status for both model providers. Read the change against
`agents/plans/2026-10-01-005-retry-temporary-model-request-failures.md` and
traced the retry loop in `run_model_step`, the status error paths in both model
clients, the compaction errors that reach the same loop, and the switched tests.
Lenses: correctness, error-handling, api-design, naming, testing, and
documentation.

## Fixed findings

- **`Temporary` was public with a public field**
  (`crates/ox-server/src/model.rs:302`): only `model.rs` builds or reads it, so
  the public type added surface no caller uses. Fix: made the type and its field
  private.
- **The work log failed `make check`**
  (`agents/work/2026-10-01-005-retry-temporary-model-request-failures.md:23`): a
  trailing blank line failed `dprint check`, so `make check` stopped at the
  Markdown check. Fix: formatted the file.

## Findings

### Low

#### Naming

- **OX-0043: Two functions named `status_error` meet at one call**
  (`crates/ox-server/src/openrouter.rs:318`): the OpenRouter client calls
  `model::status_error(status, io::Error::other(status_error(status, &detail)))`.
  The local `status_error` builds the message and the `model` one marks the
  error temporary, so the same name means two different jobs in one expression.
  Fix: rename one of them, such as `model::status_error` to a name that says it
  marks the error temporary.

## Checks run

- `make check` — first failed at `dprint check` on the work log; passed after
  formatting it and after making `Temporary` private.

## Verdict

The retry change meets the plan's goal. Two small issues were fixed and one
naming issue of low severity is left open.
