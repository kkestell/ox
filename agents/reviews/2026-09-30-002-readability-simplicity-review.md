# Readability and simplicity review

## Scope and coverage

Reviewed the whole codebase at `7e73600` with the `readability` and `simplicity`
lenses. Read all Rust production and test files in `crates/ox-server` and
`crates/ox`, both Python scripts and the benchmark tasks, the Makefile, the
Cargo manifests, the example settings and skills, the release workflow, and the
README. Work was split across five subagent reviews (server ACP boundary, server
support, sessions and subagents, tools and processes, client and support files);
the main reviewer re-verified every finding against the current code before
fixing, and resolved all tool output truncation.

A concurrent session committed `7e73600` (review
`2026-09-30-001-readability-and-simplicity-review.md`) while this review ran.
That review fixed the checkpoint re-read, the read.rs line reader, the search
stderr drain, and the client `loading` state. Those changes were checked against
the current code and are not repeated here.

The `make e2e` tmux tests were run. No live OpenRouter request, Docker benchmark
run, or external ACP client was exercised. Generated evaluation images and the
lockfile were treated as data, not reviewed.

## Fixed

- **The option-choice error was built three times**
  (`crates/ox-server/src/acp.rs:337`): `set_config_option` repeated the same
  `ok_or_else` closure for the model, effort, and mode arms, so the message and
  the "invalid choice means invalid params" decision had to change together in
  three places. One `not_a_choice` closure now serves all three.

- **Shutdown signalled every session's shell processes twice**
  (`crates/ox-server/src/acp.rs:548`): the `for` loop called `begin_shutdown` on
  each session, then `join_all` called `shutdown`, which signals before it
  waits; `join_all` polls every session before any completes, so the loop added
  a step and a misleading comment rather than an ordering guarantee. The loop is
  gone and the comment now says what provides the guarantee.

- **Session listing rejected a cursor no client can obtain**
  (`crates/ox-server/src/acp.rs:487`): the server returns every session in one
  page and never sets `next_cursor`, so the only way to send a cursor is to
  invent one. The check is removed; the test now pins that a cursor is ignored
  and the response has no `next_cursor`.

- **A failed commit sent updates over a known-broken connection**
  (`crates/ox-server/src/acp/prompt.rs:727`): `complete_interrupted` tested
  `outcome` for `AcpUpdate`, but the commit-failure branch replaced `outcome`
  with `Storage` first, so the remaining updates were sent anyway and their send
  error then hid the storage error. The connection failure is captured before
  the commit and the guard reads that value.

- **`Operation::Load` and `Operation::Delete` were indistinguishable**
  (`crates/ox-server/src/acp/operations.rs:16`): nothing stored or matched the
  difference, so a reader looked for a load-versus-delete distinction that does
  not exist. The variants are merged into `Operation::Session`; the two named
  entry points (`try_load`, `try_delete`) remain.

- **`CompletionStream::next` had two ways to say the stream ended early**
  (`crates/ox-server/src/openrouter.rs:638`): it returned `Ok(None)` when the
  body ended before a completion and `Err(UnexpectedEof)` when it ended
  mid-message, and both callers treated `None` as their own error. `next` now
  returns `StreamItem` and reports the end as one error, deleting the `Option`,
  the duplicate messages, and the test helper's `None` stop.

- **The summarizer output limit existed in two files**
  (`crates/ox-server/src/openrouter.rs:332`,
  `crates/ox-server/src/compaction.rs:21`): `SUMMARY_OUTPUT_TOKENS` reserved
  room for an output budget that the request body wrote as a literal
  `"max_tokens": 4096`. One `SUMMARIZER_MAX_TOKENS` constant now feeds both, so
  a change to the limit cannot leave compaction sizing pieces for the old one.

- **The summarized-prefix lookup was written three times**
  (`crates/ox-server/src/compaction.rs:92`): `candidates`, `ranked_cuts`, and
  `material` each opened with the same "length the latest checkpoint covers"
  expression. One `summarized_prefix` helper is used by all three.

- **`compact` had no doc comment** (`crates/ox-server/src/compaction.rs:418`):
  the most side-effecting function in the file now states that its `bool` means
  a checkpoint was committed, that rejected cuts still spend summarizer requests
  and count toward the saved cost, and that the caller's transcript is extended
  only after the store saves it.

- **Request sizes mixed tokens and bytes behind a bare `3`**
  (`crates/ox-server/src/compaction.rs:26`): `request_estimate` returned tokens
  while `estimated_bytes` and `message_bytes` returned bytes, and nothing said
  which unit a name or comparison used. Renamed `request_tokens`,
  `projected_tokens`, `body_bytes`, `entry_bytes`, and `to_tokens`, so the unit
  is visible at every use and a mixed comparison is visible as one.

- **`sessions.created_at` was written and returned but never read**
  (`crates/ox-server/src/sessions.rs:36`): the column, the `SessionSummary`
  field, and two SELECT lists carried a value no code consumed; the session
  insert wrote the same timestamp to `created_at` and `updated_at`. The column,
  field, SELECT mentions, and the assertion that pinned them are gone.

- **`write_result` decided "unchanged" twice**
  (`crates/ox-server/src/tools/file.rs:79`): it chose the word `"Unchanged"`,
  then compared that display label to decide whether to add the diff, while
  `old_text: Option<String>` meant both "the file did not exist" and "the
  previous text". The unchanged case returns early; otherwise `created` picks
  `Added`/`Modified` and the diff is always pushed.

- **The page-end match repeated its tail**
  (`crates/ox-server/src/tools/read.rs:84`): two of the three arms called
  `has_more` identically, burying the only difference, the truncation notice.
  The notice is appended first and the match has two arms.

- **Two path checks each enforced the same rule**
  (`crates/ox-server/src/tools/workspace.rs:49`): `normalize_path` and
  `resolve_allowing_link_target` each required "Normal or `.` components only"
  with their own copy of the error text. One `is_relative_path` predicate and
  one `invalid_relative_path` error now hold the rule; the two different
  empty-path messages are unchanged.

- **A filtered iterator was cloned only to be counted**
  (`crates/ox-server/src/shell_processes.rs:138`): the `retained` binding
  suggested later use, but only its count mattered. It is counted directly.

- **A closed session kept `cancelling` set** (`crates/ox/src/acp.rs:85`):
  `close` cleared seven per-session fields but not `cancelling`, the only other
  place it is cleared is `finished`, and `accepts` drops the old session's late
  `Finished` event. Cancelling a turn and then starting a new session before
  that event arrived made the new session answer its first permission request
  `Cancelled`, so the prompt silently produced nothing. `close` now clears the
  flag, with the new test
  `closing_a_cancelled_session_queues_the_next_permission_request`.

- **Startup threaded failures through three nested `Result`s**
  (`crates/ox/src/acp.rs:337`): `initialize` plus the new-session request inside
  a timeout ended in `Result<Result<..>>` with an `Ok(Err(error))` arm. The
  timeout flattens to one `Result` with `unwrap_or_else` and a single `?`.

- **The blank-row rule between transcript items was written per pass**
  (`crates/ox/src/tui/transcript.rs:300`): `visible_rows` counted rows and built
  the visible slice with the same `after_call && call` rule written twice, so
  the two passes had to be kept in agreement by hand. One `needs_blank_row`
  helper is used by both.

- **The approval doc did not state its contract** (`crates/ox/src/tui.rs:635`):
  it now says the request's tool call may carry only part of the call and the
  transcript's copy supplies the rest, which is why the view is a parameter.

- **The server crate repeated the workspace dependency declarations**
  (`crates/ox-server/Cargo.toml:10`): four dependencies were declared twice, and
  `futures` said `0.3.31` while the workspace and the lockfile say `0.3.34`. The
  crate now uses the workspace entries with its extra features.

- **The README claimed a size the repository does not have** (`README.md:5`):
  "under 10,000 lines of code" is false on any count. The claim is removed.

- **The chart's tick search was a doubly nested generator**
  (`scripts/bench.py:754`): the filtered ticks were bound inside the
  comprehension and `next(..., [1])` picked the first set that fits. A four-line
  loop with the same fallback has replaced it.

## Findings

### Low

#### Readability

- **OX-0014: `request_completion` repeats its cancellation select inside the
  stream loop** (`crates/ox-server/src/acp/prompt.rs:469`,
  `crates/ox-server/src/acp/prompt.rs:486`).

  Ending a model request correctly requires reading two cancellation sites (the
  request select and the per-item select) and a nested loop. Both selects carry
  the identical biased `cancelled()` arm, and the delta forwarding, the
  end-of-stream error, and the completion return all sit one level deeper than
  they need to.

  Evidence:
  `() = self.cancellation.cancelled() => return Err(PromptOutcome::Cancelled)`
  appears in both selects, and the inner `match item` handles only stream items.

  Suggested fix: move "project the transcript, request, forward deltas until the
  completion" into one `async fn` and wrap that call in a single `select!`
  against `self.cancellation.cancelled()`. Keep the context-overflow compaction
  retry inside the outer loop and apply it only to the request error, as today.

- **OX-0015: the picker's list-row count is stored in the transcript's
  `Layout`** (`crates/ox/src/tui.rs:146`, `crates/ox/src/tui.rs:410`,
  `crates/ox/src/tui.rs:763`).

  `Layout::height` normally describes the transcript region's height but holds
  the picker's list-row count while a picker is open. Because of that reuse,
  `mouse()` cannot trust its own bounds check and returns early for a picker
  (`crates/ox/src/tui.rs:1002`), and the picker's scroll amount is computed
  twice per frame (`run` at `crates/ox/src/tui.rs:1191`, and again in `draw`).

  Evidence: the picker branch of `draw` returns `Layout { height: rows, .. }`,
  and the picker key handling reads `let rows = ui.layout.height;`.

  Suggested fix: store the picker's visible row count on the picker (from the
  same `picker_rows` call `run` already makes) and stop overloading
  `Layout::height`.

#### Simplicity

- **OX-0016: `ranked_cuts` recomputes the request size beside
  `projected_tokens`** (`crates/ox-server/src/compaction.rs:193`).

  Ranking a cut weighs `base_bytes + suffix_bytes[..] + repeated_bytes` from
  `entry_bytes`, a second implementation of what `projected_tokens` measures by
  serializing the projected body. An encoding change that updates one silently
  mis-ranks cuts, which changes which cuts compaction tries and how much history
  it drops; a test exists only to pin the two in agreement.

  Evidence: the hand-rolled suffix scan, and
  `assert_eq!(base + suffix, body_bytes(openrouter::ordinary_body(...)))` in
  `ranked_cuts_follow_the_latest_checkpoint_smallest_request_first`.

  Suggested fix: rank with
  `projected_tokens(parameters, transcript, cut, &"x".repeat(SUMMARY_ALLOWANCE_BYTES))`
  and delete `entry_bytes` plus the suffix scan (about 25 lines). This costs one
  body serialization per candidate cut, so it needs the author's agreement.

- **OX-0017: session cost is summed twice, in Rust and in SQL JSON paths**
  (`crates/ox-server/src/sessions.rs:831`).

  `children_cost` extracts `$.message.usage.cost` and `$.summarizer_cost` and
  repeats the kind strings `'assistant_batch'` and `'compaction_checkpoint'`
  that `encode_entry` and `decode_entry` own. If a field moves or a kind is
  renamed, `json_extract` returns NULL and SQL `SUM` returns NULL, so the
  client's displayed session cost silently stops counting child sessions.

  Evidence: the `json_extract` query versus
  `TranscriptEntry::AssistantBatch(batch) => batch.message.usage...cost` in
  `transcript_cost`.

  Suggested fix: have `children_cost` select the child sessions' `kind, data`
  rows and reuse `decode_entry` and `transcript_cost`, deleting the JSON paths
  and the second copy of the kind names. The extra decode cost for the usage
  line needs the author's agreement.

- **OX-0018: shell `timeout_seconds` rejects an explicit null**
  (`crates/ox-server/src/tools/shell.rs:24`).

  `#[serde(default, deserialize_with = "present")]` maps an omitted field to
  `None` but fails `"timeout_seconds": null` as "invalid type: null, expected
  u64". A model writing null for an optional field loses a turn to a retry where
  omission would have used the default or the background path. The custom
  deserializer and two test rows exist only for that rejection.

  Evidence: `fn present(...) { u64::deserialize(deserializer).map(Some) }` and
  the comment "an explicit null is rejected like any non-integer", with both
  null cases asserted to fail.

  Suggested fix: use a plain `Option<u64>` and delete the two null test rows
  unless the rejection is wanted deliberately.

- **OX-0019: workspace settings are merged by a second hand-rolled builder**
  (`crates/ox-server/src/settings.rs:103`).

  `for_workspace` re-opens and re-parses `.ox/settings.json` for every new
  session and load and expresses three overrides as nested `Option` chains,
  apart from `load_from`'s near-identical construction. A reader has to compare
  two builders to learn which file wins.

  Evidence:
  `default_effort: file.effort.map(|value| effort(&path, Some(value))).transpose()?.unwrap_or(self.default_effort)`
  and the same pattern for `mode`.

  Suggested fix: parse a file into one `SettingsFile` and apply it with one
  `apply(file, fallback)` overlay used by both builders.

- **OX-0020: model and effort validation is written in three places**
  (`crates/ox-server/src/lib.rs:28`, `crates/ox-server/src/settings.rs:133`,
  `crates/ox-server/src/openrouter.rs:279`).

  `ModelRequestParameters::new` already rejects a model missing from the catalog
  and an unsupported effort; `resolve_model` and `check_effort` repeat both
  checks for the CLI, and `Settings::validate` repeats them a third time, with
  three sets of message text. A reader cannot tell whether a model id is
  validated once or twice.

  Evidence: `catalog_model(model_id).ok_or_else(...)` and
  `if !model.supports(effort)` in each of the three places.

  Suggested fix: have the CLI checks go through `ModelRequestParameters::new`
  (discarding the struct) and have `Settings::validate` share one error helper,
  keeping one wording per condition.

- **OX-0021: client and server each implement the atomic settings write**
  (`crates/ox/src/config.rs:103`, `crates/ox-server/src/settings.rs:190`).

  Both read the JSON (or `{}`), take the object, insert fields, pretty-print
  with a trailing newline, create the directory, write a temp file, and rename;
  they differ only in the inserted keys and the temp-file name. A fix to one
  copy does not reach the other.

  Evidence: `fields.insert("favorites".into(), ...)` and
  `std::fs::rename(&temporary, path)` versus the `model`/`effort`/`mode` writer
  with `.settings-<uuid>.tmp`.

  Suggested fix: expose the server's settings writer for a caller-supplied field
  value and delete the client's copy.

- **OX-0022: the 80-character truncation rule is implemented twice**
  (`crates/ox-server/src/tools.rs:225`,
  `crates/ox-server/src/sessions.rs:1053`).

  `shorten` and `session_title_from_prompt` both cap text at 80 characters
  including the ellipsis, but the tool-call title trims trailing whitespace
  before the ellipsis and the session title does not. One idea has two
  behaviors, and a change must be made twice.

  Evidence: `format!("{}…", kept.trim_end())` versus `format!("{kept}…")`.

  Suggested fix: one `ellipsize(text, limit)` helper used by both; keep the
  first-nonblank-line part in `session_title_from_prompt`.

- **OX-0023: the 16 KiB limits are repeated in model-facing text**
  (`crates/ox-server/src/tools.rs:56`, `crates/ox-server/src/tools/shell.rs:20`,
  `crates/ox-server/src/tools/shell.rs:108`,
  `crates/ox-server/src/tools/subagent.rs:54`,
  `crates/ox-server/src/tools/read.rs:32`,
  `crates/ox-server/src/tools/search.rs:45`).

  `OUTPUT_LIMIT`, `MAX_INPUT_BYTES`, and `MAX_MESSAGE_BYTES` are `16 * 1024`,
  but the tool descriptions and errors spell "16 KiB" in string literals.
  Changing a limit leaves the model told the old size, and each site must be
  found by hand.

  Evidence: `const OUTPUT_LIMIT: usize = 16 * 1024;` versus
  `"description": "... at most 16 KiB ..."` and
  `Err("arguments: text exceeds 16 KiB".to_owned())`.

  Suggested fix: build those strings with `format!` from the constant that
  applies, or from one shared KiB constant.

- **OX-0024: the scripts carry two copies of the commit-URL parse and the `.env`
  key scan** (`scripts/run.py:64`, `scripts/run.py:74`, `scripts/bench.py:114`,
  `scripts/bench.py:184`).

  The same GitHub commit URL regular expression, owner/name/commit split, and
  `OPENROUTER_API_KEY` scan are written in `run.py` and again in `bench.py`,
  with different error handling. A change to the URL form or the key location
  must be made twice.

  Evidence: identical
  `re.fullmatch(r"https://github\.com/([^/]+)/([^/]+)/commit/([0-9a-fA-F]+)", ....rstrip("/"))`
  and identical
  `next(line.split("=", 1)[1] for line in (ROOT / ".env").read_text().splitlines() ...)`.

  Suggested fix: put both helpers in `scripts/support.py` and import them from
  each script (importing one script from the other would run its `main`).

## Checks run

- `make check` passed: Markdown and Rust formatting, 169 server library tests,
  68 client library tests, the integration tests, the workspace build, and
  Clippy with warnings denied.
- `make e2e` passed: all 12 tmux tests.
- `python3 -m py_compile scripts/run.py scripts/bench.py` passed.
- `scripts/bench.py compare deepseek-main deepseek-simpler` ran against the
  committed runs to exercise the chart change; the regenerated evaluation files
  were reverted.
- No live OpenRouter calls, Docker benchmark runs, or external ACP clients were
  exercised.

## Verdict

The codebase is in good shape for these lenses: 22 readability and simplicity
fixes were applied, including the closed-session cancellation leak in the
client. Eleven low-severity findings remain open; none needs urgent planning.
