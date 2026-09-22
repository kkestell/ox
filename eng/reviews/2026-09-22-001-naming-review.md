# Naming review — whole codebase

Date: 2026-09-22 Reviewer: kreview (`naming`) Status: Resolved

## Scope

All 14 files under `src/**/*.rs`, including their test modules, checked against
`eng/glossary.md`, `eng/architecture.md`, and the communication rules in
`AGENTS.md`. `eng/reviews/2026-09-21-001-naming-review.md` was read first so
that its resolved findings and rejected claims are not raised again.

The corpus is the whole codebase rather than one change, so findings are
ordered by what they cost a reader, not by the commit that introduced them.

## Coverage gaps

This was a static vocabulary and call-site review. The fixes were validated with
the full local test suite, Clippy, and `cargo fmt`, but no live model request
was made. `Makefile`, `scripts/run.py`, `src/system_prompt.md`, and the
engineering documents were read for vocabulary but were not themselves reviewed
for naming. The dated plans under `eng/plans/` keep the old names because they
record what was decided at the time.

## Checks run

- Read all 14 files under `src/**/*.rs`, `eng/glossary.md`,
  `eng/architecture.md`, `eng/code-style.md`, `eng/testing.md`, and `AGENTS.md`.
- Read `eng/reviews/2026-09-21-001-naming-review.md` in full and checked its
  seven resolutions and six rejections against the current source.
- Searched `src/` for every disputed term and traced each occurrence to the
  concept it names: `settings`, `selections`, `effort`, `run`, `serve`, `kind`,
  `chunk`, `instructions`, and `model_choice`.
- Compared `src/acp.rs` and `src/acp/prompt.rs` at `264364c` with the current
  files to establish which names changed after the previous review resolved
  them.
- Inspected `b29cd18` to identify the commit that reintroduced a resolved name.
- After applying the fixes, ran `cargo fmt`, `cargo clippy --all-targets`, and
  `cargo test`. All 83 tests pass and Clippy is clean.
- Repeated the vocabulary searches to confirm each replaced name survives only
  where it names a distinct concept or historical review and plan text.

## Verified findings and resolutions

All findings concerned readability and maintenance. None of them changed runtime
behavior, and each one is resolved in the current source.

### 1. [medium, resolved] `ActiveSession.settings` held the ACP selections

- Original issue: `src/acp.rs:96` names the field `settings` and documents it as
  "The latest client selections."
- Contract: The glossary defines ACP selections as the latest session settings
  selected for a future turn and states that they are process state, not
  durable authority. Finding 4 of the previous review resolved this same
  mismatch by renaming the map to `selections`.
- Consequence: `b29cd18` replaced the `selections` map with `active:
  HashMap<SessionId, ActiveSession>` and named the new field `settings`,
  carrying the old comment with it. The `selected_settings` accessor was
  removed at the same time, so `src/acp.rs:529` now reads `selected_settings:
  Some(active.settings)` and translates between two names for one value at the
  boundary. Inside `set_config_option`, `settings` at `src/acp.rs:155` is the
  ACP selections while `stored_settings` at `src/acp.rs:151` is a different
  value six lines above it.
- Resolution: The field is `selections` again, and its comment now says ACP
  selections. `src/acp.rs` passes `selected_settings: Some(active.selections)`
  at both prompt call sites, so the boundary no longer translates between two
  names. Inside `set_config_option` the ACP selections are `selections` and the
  transcript-derived value is `saved_settings`. A value copied at the prompt
  boundary stays `settings`, because it is the settings snapshot from that point
  on.

### 2. [medium, resolved] The settings rebuilt from the transcript had three names

- Original issue: `StoredSession::settings` (`src/sessions.rs:324`) returns the
  session settings in force after the last transcript entry. Callers name that
  result `stored_settings` (`src/acp.rs:151`), `saved_settings`
  (`src/acp/prompt.rs:162`), and `settings` (`src/acp.rs:212`).
- Contract: The glossary names session settings, ACP selections, and the
  settings snapshot, but has no term for this value, although
  `eng/architecture.md` describes producing it: "Folding the transcript
  reconstructs the session settings after load or restart."
- Consequence: `stored_settings` predates the previous review and
  `saved_settings` was added after `264364c`, so the set of names is still
  growing. A reader comparing `src/acp.rs` with `src/acp/prompt.rs` has to
  confirm that two differently named locals hold the same value, in the two
  places where the rule that locks the model depends on it.
- Resolution: The method is `StoredSession::saved_settings`, and every caller
  binds `saved_settings`: `src/acp.rs` in both `set_config_option` and
  `load_session`, and `src/acp/prompt.rs` in `PromptRun::open`.
  `eng/glossary.md` now defines saved settings as the session settings rebuilt
  by folding a stored transcript, so a third name has a rule to fail against.

### 3. [medium, resolved] `instructions::load` returned the system prompt

- Original issue: `src/instructions.rs` is named for the workspace half of what
  it builds, and `load` (`src/instructions.rs:15`) returns the complete system
  prompt.
- Contract: The glossary defines the system prompt as Ox's built-in
  instructions followed by workspace instructions, and defines workspace
  instructions as the `AGENTS.md` text alone. `AGENTS.md` says never to mix
  definitions or overload terms.
- Consequence: `src/acp.rs:218` calls `instructions::load` inside
  `load_session`, next to `SessionOperations::try_load`, so `load` means both
  reading a file and starting a session operation within one function. The
  module comment at `src/instructions.rs:1` introduces a third word, "workspace
  guidance", for what the glossary and the emitted heading both call workspace
  instructions. `src/acp.rs:911` binds a test variable named `instructions` to
  the system prompt. The production callers were already corrected to
  `system_prompt`, so only the module, its function, and these two places still
  carry the older word.
- Resolution: The module is `src/system_prompt.rs` and the function is
  `for_workspace`, so all three callers read
  `system_prompt::for_workspace(path)` and `load` no longer competes with
  `SessionOperations::try_load`. `assemble` was avoided because
  `openrouter::Assembly` already uses it for a different operation. The module
  comment and the `read_workspace` comment now say workspace instructions, the
  test binding in `src/acp.rs` is `prompt`, and its test name says system
  prompt. The file map in `AGENTS.md` points at the new path.

### 4. [medium, resolved] `effort` named the effort level and the OpenRouter string

- Original issue: `src/openrouter.rs:147` reads `if let Some(effort) =
  choice.effort(effort)`. The outer `effort` is the `EffortLevel` parameter at
  `src/openrouter.rs:128`; the new binding is the OpenRouter effort string
  written to `body["reasoning"]["effort"]` on the next line.
- Contract: The glossary separates the effort level, one of Ox's four reasoning
  levels, from the effort mapping, which "turns an effort level into an
  OpenRouter effort string, or into no reasoning parameter for Default". Its
  table of names across boundaries lists the ACP `effort` option and
  OpenRouter's `reasoning.effort` as different names.
- Consequence: One identifier holds both sides of that mapping within a single
  statement, and the `efforts` field (`src/openrouter.rs:20`) and `effort`
  method (`src/openrouter.rs:24`) repeat the collision. A reader has to use the
  type to tell which side is meant.
- Resolution: The field is `openrouter_efforts` and the method is
  `openrouter_effort`, so `src/openrouter.rs` reads `if let
  Some(openrouter_effort) = catalog_model.openrouter_effort(effort)`. The
  parameter stays `effort`, because it is the effort level.

### 5. [low, resolved] `MODEL_CHOICES` contradicted the model catalog in the errors

- Original issue: `src/openrouter.rs:16-58` defines `ModelChoice`,
  `MODEL_CHOICES`, and `model_choice`.
- Contract: The glossary calls this the model catalog: the OpenRouter models Ox
  offers, paired with their effort mappings.
- Consequence: Three messages in the code already use the glossary term —
  `src/acp.rs:49` and `src/acp/prompt.rs:175` report that a model "is not in the
  model catalog", and `src/acp.rs:59` expects that "a session model comes from
  the model catalog" — so a reader is told one name and shown another.
  "Choice" is also the configuration-option concept in the same area:
  `src/acp.rs:170` rejects a value that "is not a choice of configuration
  option". A
  `ModelChoice` is a model with an id, a display name, and an effort mapping,
  which is why `src/acp.rs:61` already iterates it as `|model|`.
- Resolution: The type is `CatalogModel`, the table is `MODEL_CATALOG`, and the
  lookup is `catalog_model`, which now iterates `|model|` as well. The errors
  that already said model catalog match what a reader is shown, and "choice" is
  left to the ACP configuration option.

### 6. [low, resolved] `acp::run` served the mode that `Command::Run` does not name

- Original issue: `src/main.rs:85` dispatches `Command::Serve` to `acp::run`,
  and `src/main.rs:86` dispatches `Command::Run` to `acp::run_headless`. The
  function named `run` is the one the `run` subcommand never reaches.
- Contract: The glossary reserves the headless entry point for `ox run`, and
  `eng/architecture.md` says the normal process "serves one ACP connection".
- Consequence: `acp::run` (`src/acp.rs:353`) is a short wrapper around `serve`
  (`src/acp.rs:423`), which is the name matching both the command and the
  architecture document. `run` separately means one prompt run
  (`src/acp/prompt.rs:38`, a glossary term) and running a subprocess
  (`src/tools/search.rs:86`, `src/tools/shell.rs:282`).
- Resolution: The entry point for the `Serve` arm is `acp::serve_stdio`, which
  opens the store and serves over stdio. The private `serve(state, transport)`
  keeps its name and its test call sites, which supply their own state.
  `run_headless` and `prompt::run` already matched their glossary terms and did
  not change.

### 7. [low, resolved] Neither workspace path check said it constrains the path

- Original issue: `existing_path` (`src/tools.rs:103`) and `resolve`
  (`src/tools/patch.rs:173`) take the same arguments and do the same job:
  resolve a workspace-relative path and refuse one that reaches outside the
  workspace. They apply deliberately different symbolic-link rules.
- Contract: `eng/architecture.md` states both rules: `read_file` and `grep`
  "accept a directly named symbolic link to a file only when its target remains
  inside the workspace", while "patch operations reject a symbolic link as
  their target".
- Consequence: Neither name states that the path is constrained to the
  workspace, and nothing distinguishes the two rules. This is the check that
  keeps `read_file`, `grep`, `glob`, and `apply_patch` inside the workspace, so
  a reader adding a tool cannot tell from the names which one is correct or
  that picking the wrong one widens the boundary. This is not the workspace
  path claim the previous review rejected: that claim was about the `root` and
  `name` parameters, which remain correct.
- Resolution: The checks are named for the rule each applies:
  `tools::workspace_path_allowing_link_target` and
  `patch::workspace_path_rejecting_links`. The first also gained a comment
  stating that it resolves a workspace-relative path and refuses one that leaves
  the workspace.

### 8. [low, resolved] `kind` named four concepts, one of them a bare function

- Original issue: `src/acp/convert.rs:73` defines a function named `kind` that
  returns the ACP `ToolKind` for a call.
- Contract: The glossary defines tool kind as the ACP category that tells a
  client which icon to show.
- Consequence: `kind` elsewhere means the transcript entry kind, which is both a
  column and a parameter (`src/sessions.rs:567`, `:580`), the patch operation
  kind (`src/tools/patch.rs:11`), and the type of a reasoning detail
  (`src/openrouter.rs:376`). The bare function is the only one of the four that
  can be read without its surrounding type.
- Resolution: The function is `convert::tool_kind`, called at its three sites.
  The other three uses of `kind` are qualified by their structures and modules
  and did not change.

## Previously settled, not raised again

- `Assembly`, `PartialCall`, `Capture`, and `Observed`: the previous review
  rejected renaming these as churn without a real ambiguity, and that reasoning
  still holds at their current call sites. The adjacent point that
  `Assembly::tool_call` and `Assembly::reasoning_detail` are named like
  accessors while they modify the assembly falls inside the same rejection and
  is not worth reopening on its own.
- `openrouter::Stop`: the previous review resolved this with no finding because
  callers qualify it. Current callers still do.
- Workspace path names, and `summary` in two modules: both rejected previously,
  and the current source matches the reasoning given.

## Observations that do not support a finding

- `execute_other` (`src/tools.rs:339`) means the tools that do not handle
  cancellation themselves, not the tools other than shell, but the distinction
  is visible in the `select!` above it at `src/tools.rs:330`.
- `Chunk` names a stream chunk (`src/openrouter.rs:455`) and a patch chunk
  (`src/tools/patch.rs:23`). The patch sense is fixed by the `apply_patch`
  description the model reads, and both types are private to their modules.
- `pending_tool_call` and `replayed_tool_call` omit the `_update` suffix that
  `in_progress_tool_call_update` and `finished_tool_call_update` carry. The
  suffixes follow the ACP variant names, which is defensible.

## What holds up

The title vocabulary resolved by the previous review has stayed correct:
`session_title`, `session_title_from_prompt`, `MAX_SESSION_TITLE_CHARS`,
`tool_call_title`, and `MAX_TOOL_CALL_TITLE_CHARS`, with no unqualified `title`
outside ACP and the `sessions` table. `transcript` and `TranscriptEntry`,
`continuation_metadata` against `reasoning`, `AssistantBatch` against
`UncommittedAssistantBatch`, and `OperationGuard` with `PromptCancellation` each
have one name and match their definitions.

## Verdict

All eight naming findings are resolved. They were vocabulary rather than
behavior: local renames, two comment corrections, one new glossary term, and one
path update in `AGENTS.md`. No redesign was needed, and the test suite and
Clippy are clean.
