# Abstraction opportunities review

## Scope and coverage

Reviewed all production code in `src/` at `145092e` (about 8,000 of the
15,200 lines) to find places where one new type, method, or helper would
reduce total lines or make the code easier to follow. For test modules
(about 7,200 lines), I checked only the helpers and harnesses. I did not
read every test body.

Lenses: `architecture`, `readability`, and `rust-idioms`. I did not
review correctness, security, or performance except where they limit a
suggestion.

Line savings are estimates from reading the code. I did not implement or
measure any of them.

I also looked at these patterns and chose not to report them:

- The `id`, `name`, `from_id`, and `ALL` boilerplate on `SessionMode`,
  `EffortLevel`, and `HookKind`. A macro would save about 40 lines but would
  be harder to read than the plain matches it replaces.
- The fake OpenRouter `Server` and the prompt `Harness`. Test setup is already
  shared, and only a few small builders, such as `call(id, command)`, repeat
  across test modules.
- The path resolver in `apply_patch`, which rejects links, and the one in
  `Workspace`, which follows them. They look alike but enforce different
  rules, and merging them would weaken a security boundary.

## Findings

### Medium

#### Architecture

- **A validated model is looked up again and again as if it could be missing**
  (`src/openrouter.rs:253`, `src/openrouter.rs:277`, `src/compaction.rs:42`,
  `src/compaction.rs:54`, `src/compaction.rs:194`, `src/acp/convert.rs:115`,
  `src/acp/prompt.rs:272`, `src/acp/prompt.rs:675`):
  every session model passes `validate_settings` (`src/acp.rs:51`) before any
  request is estimated or built, and the catalog is installed once per process
  (`src/openrouter.rs:158`). Even so, `ordinary_body`, `summarizer_body`,
  `budget`, and `usage_update` each look the model up by ID again and return an
  `io::Result` with their own "unknown model" error. That error spreads upward:
  `request_estimate`, `projected_estimate`, `input_fits`, and `ranked_cuts` are
  fallible only because of it. Their callers then map a failure that cannot
  happen into `PromptOutcome::OpenRouter` (`src/acp/prompt.rs:679`, `:686`,
  `:696`, `:654`, `:779`) or `Error::into_internal_error` (`src/acp.rs:272`,
  `:409`, `src/acp/prompt.rs:278`). In other places the same lookup ends in
  `.expect` with four different messages (`src/acp.rs:72`, `:348`,
  `src/acp/prompt.rs:254`, `src/compaction.rs:377`, `src/main.rs:111`).
  A reader has to work out that none of these failures can happen, and that
  a failed size estimate is not really a model request failure.
  Separately, the three values `(model, effort, system_prompt)` are passed
  together to `request_estimate` (five calls, each wrapped over five or six
  lines), `input_fits`, `projected_estimate`, `ranked_cuts`, `ordinary_body`,
  and `stream_completion`.
  **Fix:** make `validate_settings` return the `&'static CatalogModel`. Add a
  small request type, for example
  `openrouter::ModelRequest<'a> { model: &'static CatalogModel, effort, system_prompt: &'a str }`.
  `PromptRun` builds it once in `open`, and `compact_session` and
  `load_session` build it after they validate settings. Then give it
  infallible methods: `body(&[TranscriptEntry]) -> Value`, `budget()`,
  `estimate(&[TranscriptEntry]) -> usize`, and `fits(&[TranscriptEntry]) -> bool`.
  Make `usage_update` take the request. `stream_completion` stays fallible
  because of the network. This retires four unknown-model errors, five
  `.expect` messages, about a dozen `map_err` calls, and the `io::Result` on six
  functions. I estimate 50–70 fewer lines.

- **Transcript entries are encoded and appended in several hand-written
  copies** (`src/sessions.rs:868`, `src/sessions.rs:913`, `src/sessions.rs:926`,
  `src/sessions.rs:937`, `src/acp/prompt.rs:261`, `src/acp/prompt.rs:286`):
  `decode_entry` (`src/sessions.rs:1061`) maps each kind string to a variant.
  There is no matching encoder, so the four `append_*` methods write the kind
  strings again by hand. Each also repeats the same sequence: `now()`, lock,
  open a transaction, update activity, insert, commit. The settings change
  becomes up to three transcript entries in three places: the input size
  check in `save_turn_start` (`src/acp/prompt.rs:262–270`), the in-memory
  transcript after the save (`:287–295`), and the store insert
  (`src/sessions.rs:886–894`). This is why the prompt run copies the transcript
  and later uses `take()` on each field. The row query and decode loop also
  appear twice (`src/sessions.rs:819–836` and `:946–964`).
  **Fix:** add `encode_entry(&TranscriptEntry) -> (&'static str, String)` next
  to `decode_entry`, plus one private
  `append(&self, id, title, entries: &[TranscriptEntry])` that runs the shared
  transaction. `append_batch` and `append_hook_feedback` become one-line
  wrappers, or disappear. `PromptRun::open` builds the settings entries once
  as a `Vec<TranscriptEntry>`, which replaces `SessionSettingsChange`.
  `save_turn_start` then checks the size of `transcript + entries`, saves
  `entries`, and extends the transcript with them. A shared `read_transcript(tx, id)`
  serves both `read` and `append_checkpoint`. I estimate 60–80 fewer lines, and
  the kind strings would be defined in one place.

#### Readability

- **A skill invocation is converted to user content separately in each module**
  (`src/openrouter.rs:468`, `src/compaction.rs:295–317`,
  `src/compaction.rs:117`, `src/acp/prompt.rs:247`, `src/acp/prompt.rs:471`,
  `src/sessions.rs:603`, `src/sessions.rs:636`): the model sees a skill
  invocation as a user message with the invocation text followed by its images.
  `chat_messages` rebuilds that text and image array next to `user_content`.
  `material` has one branch for user message parts and another for invocation
  text and images, and both write the same `[image: …]` label. `save_turn_start`
  checks for images in two ways. The check for a turn start, a user message or
  a skill invocation, is written out at four sites. The search for the latest
  turn start is implemented twice, in `repeated_invocation` and
  `hook_invocation`. **Fix:** add `SkillInvocation::user_message(&self) -> UserMessage`,
  whose parts are the invocation text and then the images. Use it in
  `chat_messages`, `material`, and the image check. Add
  `TranscriptEntry::is_turn_start()` and one `latest_turn_start(transcript)`
  in `sessions.rs`. Converting to a `UserMessage` copies the image data once
  more for each encoding. `chat_messages` already copies it into JSON, so the
  added cost is similar. I estimate 30–40 fewer lines, and all code would use
  the model's view of an invocation.

### Low

#### Readability

- **ACP handlers repeat the same session read and settings validation**
  (`src/acp.rs:244–255`, `src/acp.rs:314–321`, `src/acp.rs:382–397`,
  `src/acp/prompt.rs:200–212`): four callers read the store, turn `None` into
  `not_found`, fold `saved_settings(&default_settings())`, validate the result,
  and compute `model_locked = !transcript.is_empty()`. `set_config_option`
  also builds the same "is not a choice of configuration option" error three
  times (`src/acp.rs:336`, `:352`, `:360`). **Fix:** add one
  `read_session(store, id) -> Result<(StoredSession, SessionSettings, &'static CatalogModel)>`
  in `acp.rs` for all four callers, plus a `not_a_choice(request)` helper. I
  estimate about 25 fewer lines, and the rule that saved settings must be in
  the catalog would be enforced in one place.

- **`PromptRun` repeats update sending and hook-run reporting**
  (`src/acp/prompt.rs:319`, `:363`, `:429–444`, `:580–595`, `:733–737`,
  `:781`, `:798`, `:820`): `(self.send_update)(…).map_err(PromptOutcome::AcpUpdate)?`
  appears 10 times, and `std::result::Result<_, PromptOutcome>` appears in 15
  signatures. `run_hook` and `run_after_run` both send the same updates:
  pending, in progress, then finished. The rule "an `Interrupted` error means
  cancelled" is written three times (`src/acp/prompt.rs:435`, `:764`,
  `src/acp.rs:282`). **Fix:** add `fn send(&mut self, update) -> RunResult<()>`,
  a `type RunResult<T> = std::result::Result<T, PromptOutcome>`, and one helper
  that starts a hook run and returns its call ID and context, for both hook
  paths. I estimate 25–35 fewer lines. The main benefit is less noise in the
  code that controls the prompt run.

- **Compaction material uses an anonymous tuple and repeats small calculations**
  (`src/compaction.rs:289`, `src/compaction.rs:391`, `src/compaction.rs:427–428`,
  `src/compaction.rs:159`, `:230`, `:290`, `:77`, `:271`, `:213`, `:233`):
  summarizer fields are `(String, String, usize)`, so `next_piece` edits them as
  `field.1.drain(..)` and `field.2 += 1`, and each of the 7 `push_back` calls
  ends in a bare `1`. The start of the uncovered transcript,
  `latest(transcript).map_or(0, |c| c.covered_prefix)`, is computed three times.
  The fixed image size, `images * IMAGE_ESTIMATE_TOKENS * 3`, is computed twice,
  and so is the summary placeholder `"x".repeat(SUMMARY_ALLOWANCE_BYTES)`.
  **Fix:** add a `Field { label, text, part }` struct with a `Field::new(label, text)`
  constructor, and add `uncovered_start(transcript)`, `image_allowance(images)`,
  and a `SUMMARY_PLACEHOLDER` value. The line count stays about the same, but
  the splitting loop in `next_piece` becomes much easier to read.

- **Tool schemas and argument parsing repeat the same code**
  (`src/tools/shell.rs:29`, `src/tools/read.rs:26`, `src/tools/search.rs:38`,
  `src/tools/search.rs:59`, `src/tools/patch.rs:58`, `src/tools/read.rs:47`,
  `src/tools/search.rs:83`, `:87`, `src/tools.rs:185`): every schema repeats
  the same JSON wrapper around its name, description, properties, and
  required fields. Every tool parses its arguments with the same
  `serde_json::from_str(…).map_err(|e| format!("arguments: {e}"))`.
  **Fix:** add `tools::function_schema(name, description, properties, required)`
  and `tools::parse_arguments::<T>(&str) -> Result<T, String>`. I estimate about
  30 fewer lines.

- **Each module has its own copy of the same `io::Error` helpers**
  (`src/skills.rs:134`, `src/sessions.rs:1084`, `src/openrouter.rs:524`,
  `src/main.rs:130`, `src/skills.rs:48–57`, `src/skills.rs:69`,
  `src/settings.rs:71`, `src/system_prompt.rs:34`, `src/main.rs:153`): four
  modules define the same one-line error helper under the names `invalid`,
  `invalid_data`, `malformed`, and `invalid_input`. Six sites add a path or name
  to an error with `io::Error::new(error.kind(), format!("{context}: {error}"))`.
  **Fix:** add one shared `with_context(error, context)` helper, and optionally
  one `invalid_data` helper. The savings are small, but prefixing errors would
  work the same way everywhere.

#### Rust idioms

- **Tool names are matched as strings in six places** (`src/tools.rs:79`,
  `src/tools.rs:92`, `src/tools.rs:167`, `src/tools.rs:179`,
  `src/acp/convert.rs:166`, `src/acp/prompt.rs:827`, `src/tools/search.rs:82`,
  `src/tools/search.rs:100`): the five tool names are `&str` constants.
  The default title, description, execution, ACP tool kind, and approval check
  each match on the name and each handle unknown names separately.
  `search::execute` takes the name and checks `name == GLOB` twice.
  `eng/code-style.md` asks for enums and exhaustive matching for closed data.
  **Fix:** add `enum Tool { Shell, ReadFile, Glob, Grep, ApplyPatch }` with
  `from_name` and `name`, and parse the name once per call. Each match
  then covers every tool, and an unknown name is handled once. Split
  `search::execute` into glob and grep entry points that share a helper. The
  line count is about the same, but adding a tool becomes a compile error until
  every match handles it. `ToolOutcome` could get a `status_id()` method at the
  same time, replacing the matching `"completed" | "failed" | "cancelled"`
  matches in `src/hooks.rs:154` and `src/compaction.rs:356`.

## Checks run

- Read all production code in the 22 Rust source files, plus
  `eng/code-style.md`, the start of `eng/architecture.md`, and the earlier
  simplification review.
- Counted repeated code with `grep` over production code only (the part of each
  file before its `#[cfg(test)]`): 10 `map_err(PromptOutcome::AcpUpdate)` and
  15 `Result<_, PromptOutcome>` signatures in `src/acp/prompt.rs`; 16
  `into_internal_error` calls in `src/acp.rs`; 22 `map_err(io::Error::other)`
  calls in `src/sessions.rs`; 13 `catalog_model(` calls across 6 files; and the
  sites listed above that check for a turn start, an `Interrupted` error, or a
  tool name.
- Traced every call path that reaches `ordinary_body`, `budget`, and
  `usage_update` back to `validate_settings` or to `resolve_model` in
  `main.rs`. This confirms the unknown-model errors cannot occur in production.
- No `cargo` commands were run. The review changed no code.

## Verdict

No correctness problems were found. The three medium findings give the
largest savings:

1. A validated model and request type.
2. One encoder and one append path for transcript entries.
3. A single conversion from a skill invocation to a user message.

Together they would remove about 150 lines and several error paths that
cannot occur, without changing behavior. The low findings are smaller
cleanups that can be done alongside related work.
