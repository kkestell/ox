# Ox simplification review

Reviewed commit: `264364c` on `rust`, after pulling `origin/rust`. This report
replaces the earlier review of the stale checkout.

Rechecked after another fetch: `HEAD` and `origin/rust` still both point to
`264364c`. All three findings below remain applicable.

## 1. Store workspace paths directly on sessions

Locations: `src/sessions.rs:31`, `src/sessions.rs:382`,
`src/sessions.rs:445`, and `src/sessions.rs:530`.

The `workspaces` table contains only an internal ID and a path. No caller uses
the ID, and there is no workspace metadata or independent workspace operation.
The separate table requires an insert and a subquery during session creation,
plus joins when listing sessions and reading their summaries.

Store `workspace_path` directly on `sessions`. Session creation then becomes
one insert; because new sessions now have empty transcripts, its explicit
multi-statement transaction can also disappear. This removes a database entity
and its associated query machinery without changing the session API.

Preserve exact path-string comparison, absolute-path validation, activity
ordering, and the foreign key that cascades session deletion to transcript
entries. Recreate the disposable database for the schema change, following
repository policy. The architecture document's separate workspace rows are an
implementation choice to update, not a user-facing requirement.

## 2. Store observed tool results as an ordered prefix

Locations: `src/acp/prompt.rs:108`, `src/acp/prompt.rs:331`,
`src/acp/prompt.rs:397`, and `src/acp/prompt.rs:414`.

`UncommittedAssistantBatch` allocates an optional outcome slot for every tool
call. It supports indexed insertion, checks whether individual slots are
filled, scans for holes during finishing, and reconstructs `ToolResult` values
when completing the batch.

Execution is strictly sequential. `execute` awaits each call and stores its
outcome before advancing. Every early exit therefore leaves an ordered prefix
of observed results; there can be no hole followed by a completed call.

Replace `Vec<Option<ToolOutcome>>` with `Vec<ToolResult>`. Append each observed
result, and construct not-started results only for the remaining suffix of
calls. This removes optional-slot handling and repeated result construction.
Keep complete-batch validation at the save boundary.

Preserve recording an outcome before sending its ACP update, retaining the
uncommitted batch until the transaction succeeds, and extending the saved
in-memory transcript only after that success. Cancellation must retain an
observed shell or patch result and give later calls explicit outcomes. The
existing ordered-results, cancellation, update-failure, and failed-save tests
cover these requirements. Parallel tool execution would require revisiting
the prefix representation; it is not current behavior.

## 3. Resolve prompt settings using the session read already required

Locations: `src/acp.rs:247`, `src/acp.rs:496`, and
`src/acp/prompt.rs:170`.

When a session has no cached selections, `ServerState::session_settings` reads
and validates its entire transcript, folds its settings, validates the model,
and inserts the result into the selections map. Immediately afterward,
`PromptRun::open` reads and validates the same transcript, folds the same
settings, and validates the effective model again.

This duplicate read occurs only when selections are absent. Normal
`session/new` and `session/load` calls populate the map, so their subsequent
prompts already perform only one session read. The benefit is removing a
redundant fallback path, not reducing database reads on every turn.

Pass an optional snapshot of the current selections into synchronous prompt
startup. When the snapshot is absent, use the settings folded from the
`StoredSession` that startup already reads. This removes the separate
read-and-cache fallback in `session_settings`; it does not require another
cache or store API. New-session and load handlers can continue seeding
selections, and configuration requests can retain their existing fallback.

Preserve the distinction between selected settings and settings already saved
in the transcript. An existing transcript must supply the fixed model; an
explicit selection supplies the next turn's effort. Keep the settings snapshot
and user-message save synchronous in the ordered ACP handler, before spawning
the model loop. Headless execution must still explicitly select its defaults.
Unknown stored models and missing sessions must remain errors.

The current tests cover model locking, rejected unknown models, effort changes
during a running prompt, and effort encoding. They do not directly cover a
prompt for an existing session absent from the selections map; add that focused
case when implementing this simplification.

## Coverage and checks

Read production code in all 13 Rust source files, the current repository
instructions, README, architecture contracts relevant to these findings, and
the implemented session-settings plan. Traced the proposed changes through
callers and relevant tests. No implementation or roadmap changes were made.

The following checks passed on `264364c`:

- `cargo test acp::prompt::tests::` — 16 tests.
- `cargo test sessions::tests::` — 7 tests.
- `cargo test effort` — 3 tests, one overlapping the prompt suite.
- `cargo test acp::tests::model_selection_locks_after_the_first_user_message`
- `cargo test acp::tests::loading_a_session_with_a_model_outside_the_catalog_fails_before_replay`
- `cargo test acp::tests::load_and_list_use_the_same_exact_workspace_path`

That is 28 distinct passing tests. These establish the existing behavior; the
proposed refactors have not been implemented or tested. No live OpenRouter or
keyring checks, full-suite run, or Linux process-cleanup checks were performed.
Test-code inspection was focused rather than exhaustive. No line savings were
measured.

The recheck traced all workspace-table references, both callers that fill
tool outcomes, and selection initialization and lookup paths. Source code
remained unchanged, so the previously passing tests were not rerun.

## Summary

The strongest reductions are removing the separate workspace entity and
representing sequential tool results directly. Prompt settings can also lose a
duplicate session-read path while retaining the new turn-boundary behavior.
