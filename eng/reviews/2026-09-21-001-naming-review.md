# Naming review — whole codebase

Date: 2026-09-21 Reviewer: kreview (`naming`) Status: Resolved

## Scope

All 13 files under `src/**/*.rs`, checked against the naming rules and glossary
in `AGENTS.md`. This verification also traced the session-settings design in
`eng/plans/2026-09-21-001-session-settings.md` where it settles the distinction
between a client selection and settings in force for a turn.

## Coverage gaps

The verification was a static vocabulary and call-site review. The fixes were
validated with the full local test suite and Clippy, but no live model request
was made. `Makefile`, `scripts/run.py`, and documentation outside the owning
instructions and affected design documents were not reviewed for naming.

## Checks run

- Read all `src/**/*.rs`, `src/tools/patch-guide.txt`, `AGENTS.md`, and the
  session-settings plan.
- Traced every source location and remedy in the original review through its
  current callers.
- Searched `src/` for the glossary terms and discouraged alternatives, including
  `history`, `record`, `event`, `terminal`, `pending`, `accepted`, `client`,
  `title`, `summary`, `root`, and `settings`.
- Inspected commits `6accf0d` and `6651214` to distinguish intentional
  vocabulary from accidental aliases.
- After applying the fixes, ran `cargo fmt --all`, `cargo test`, and
  `cargo clippy --all-targets -- -D warnings`.
- Repeated the vocabulary searches to confirm the replaced names remain only
  where they describe distinct external concepts or historical review text.

## Verified findings and resolutions

All findings concerned readability and maintenance rather than runtime behavior.
Each one is resolved in the current source.

### 1. [medium, resolved] The `events` table renamed transcript entries

- Original issue: The schema, SQL, comments, and tests called stored transcript
  entries `events` and the index `events_by_session`.
- Contract: `AGENTS.md` says to use _transcript_ and _transcript entry_ for this
  domain and not introduce _event_ as a synonym.
- Consequence: The schema and its tests said `event`, while the Rust types,
  validation, and errors said `TranscriptEntry`. A reader had to translate
  between two names for the same stored value.
- Resolution: The table is now `transcript_entries` and its index is
  `transcript_entries_by_session` throughout the schema, SQL, tests, and
  architecture design. No migration was added; the repository explicitly permits
  recreating `ox.db`.

### 2. [medium, resolved] Session titles and tool call titles used unqualified `title`

- Original issue: Internal identifiers included `TITLE_LIMIT`, `tools::title`,
  `SessionSummary.title`, `MAX_TITLE_CHARS`, and `title_from_prompt`.
- Contract: `AGENTS.md` says to write _session title_ or _tool call title_ and
  never an unqualified _title_ because both concepts occur in the codebase.
- Consequence: Call sites such as `tools::title(call)` and `summary.title` made
  the reader recover the kind of title from surrounding types and modules.
- Resolution: Internal code now uses `tool_call_title`, `session_title`,
  `session_title_from_prompt`, `MAX_TOOL_CALL_TITLE_CHARS`, and
  `MAX_SESSION_TITLE_CHARS`. External ACP fields and builders remain `title`,
  and the `sessions.title` SQL column remains qualified by its table.

### 3. [medium, resolved] `record` obscured the saved/uncommitted distinction

- Original issue: `record_outcome` and its comment described adding an outcome
  to `UncommittedAssistantBatch`, while a session-store comment used _recorded_
  for a durable save.
- Contract: `AGENTS.md` reserves direct state terms such as _saved_ and
  _uncommitted_ and says not to introduce _record_ as a domain synonym.
- Consequence: The same word could mean either placement in an uncommitted batch
  or a committed transcript entry, precisely where cancellation and ACP-update
  failures make that distinction important.
- Resolution: The method is now `set_outcome`; its comment says the outcome
  enters the uncommitted batch, and the session-store comment says settings are
  saved. Ordinary fixture usage such as recording HTTP requests remains because
  it describes a different concept.

### 4. [low, resolved] `ServerState.settings` contained client selections

- Original issue: The field comment described “latest client selections,” but
  the map was named `settings`. A selection may change during a turn and apply
  only to the next turn.
- Contract: The glossary defines session settings as the model and effort in
  force for a turn.
- Consequence: While a prompt was running, the map could contain the next turn's
  selection rather than the settings in force for the current turn.
- Resolution: The map and its mutex messages now use `selections`. Values copied
  at the prompt boundary remain `settings` because they are then the turn's
  settings snapshot.

### 5. [low, resolved] `touch` hid session-title adoption

- Original issue: `touch` updated session activity, adopted the first available
  session title with `COALESCE`, and reported an absent session.
- Consequence: `touch` conventionally suggested a timestamp update, so the
  one-time session-title state change was invisible at its call site.
- Resolution: The function is now `update_activity_and_adopt_session_title` at
  both call sites.

### 6. [low, resolved] A test called completed or failed ACP tool updates `terminal`

- Original issue: `terminal_update_for` identified a tool call update whose
  status was no longer in progress.
- Contract: `AGENTS.md` says to avoid unqualified _terminal_ for state and to
  use the actual completed, failed, or cancelled state.
- Consequence: The helper reused _terminal_, which elsewhere in `src/acp.rs`
  correctly meant the ACP terminal-authentication capability.
- Resolution: The test helper is now `finished_tool_call_update_for`, matching
  the production helper it observes. `terminal_auth_method` and other uses of
  ACP's actual terminal capability remain unchanged.

### 7. [low, resolved] Patch preparation exposed jargon and unexplained shorthand

- Original issue: Patch validation and preparation were called `preflight`,
  model-visible results used `A`, `D`, `R`, `M`, and `N`, and the unchanged case
  was named `Noop`.
- Contract: `AGENTS.md` says not to use jargon or unexplained shorthand.
- Consequence: A model reading a tool result had to infer version-control-style
  status letters, and `preflight` did not say that Ox was validating and
  preparing every operation before changing files.
- Resolution: The code and error phase now use `prepare`, the enum variant is
  `Unchanged`, and result lines say `Added`, `Deleted`, `Moved`, `Modified`, or
  `Unchanged`. Tests and the owning patch design use the same wording. The
  private `Prepared` and `Change` types remain because their module context is
  sufficient.

## Rejected or narrowed original claims

- **Workspace path has five names:** rejected. The claim combined different
  concepts. `cwd` and `--dir` are required protocol and CLI boundary names;
  `root` becomes a canonical confinement root inside file tools; and `name` in
  `search::execute` is the tool name, not a path. `workspace_path` already names
  the exact path stored with a session.
- **`summary` means two different things:** rejected. `SessionSummary` is a
  domain type, while the patch module returns an ordinary textual summary. Their
  module and type contexts distinguish them, and they are not competing names
  for one concept.
- **`terminal` is overloaded:** narrowed to finding 6. ACP terminal
  authentication is the external concept's correct name.
- **Patch workflow containers are vague:** narrowed to finding 7. `Prepared` and
  `Change` are clear within the private patch module; the actual issues were
  `preflight`, `Noop`, and the model-visible status letters, all of which are
  resolved.
- **Vague assembler names plus `entry`/`client` reuse:** rejected. `Assembly`,
  `PartialCall`, `Capture`, and `Observed` have clear variants, fields, and
  module context. `auth::entry` returns `keyring::Entry`; the fixture clients
  are distinguished by their modules and return types. Renaming them would be
  stylistic churn without removing a real ambiguity.
- **`openrouter::Stop` suspicion:** resolved with no finding. Callers qualify it
  as `openrouter::Stop`, which cleanly distinguishes it from `PromptOutcome` and
  ACP's `StopReason` and matches the glossary's OpenRouter stop concept.
- **`SessionSummary.title` suspicion:** confirmed as part of finding 2. Its
  internal field and call sites now use `session_title`; ACP's `title` remains
  unchanged at the mapping boundary.

## Verdict

All seven verified naming issues are resolved. The original workspace-path,
summary, and catch-all vagueness findings remain rejected because their call
sites do not support them. The fixes were local vocabulary and wording changes;
no redesign was needed.
