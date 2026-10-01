# Review of October 1 changes

## Scope and coverage

Reviewed `540ece7..ad3d017`: today's 30 commits present at the start of the
review, including the OpenAI integration, provider selection, compaction
changes, retries, saved errors, subagents, tools, terminal changes, and scripts.
Read the changed production code, its relevant callers, tests, plans, work logs,
and previous reviews. Lenses: simplicity, architecture, correctness,
performance, error handling, concurrency, testing, Rust ownership, Rust idioms,
and Rust Cargo.

This is also a reconsideration of the approved designs. OX-0045 and OX-0047
describe intentional policies whose consequences merit another decision, not
failures to implement their plans. No production changes were made: the findings
require decisions about retained context, retry ownership, startup dependencies,
or which provider output is authoritative.

Gaps: no live provider requests, fresh paid benchmarks, or model-quality
experiments. Authentication was reviewed against the local implementation and
recorded acceptance results; provider documentation was not revalidated. The
existing benchmark reports do not exercise the new compaction and subagent
behavior. Temporary diagnostic tests used synthetic transcripts and scripted
responses; their estimates are not measured provider token counts.

## Findings

### Medium

#### Performance

- **OX-0045 Compaction permanently retains an ever-growing request prefix**
  (`crates/ox-server/src/compaction.rs:252`): every covered user message and
  every covered tool-call title returns in every later request. Summarizing
  again cannot reduce this part. Long pasted plans, code, or logs therefore
  accumulate across completed tasks; tool calls accumulate even within one task.
  Once this prefix exceeds the automatic threshold, compaction cannot bring the
  request below that threshold. New compactable batches trigger more summaries,
  including attempts that ultimately save nothing. Eventually even a short new
  prompt cannot fit. Starting a new session becomes necessary despite having a
  compaction feature and a complete durable transcript.

  Evidence: `covered_messages` always scans from entry zero; neither an older
  checkpoint nor completion of a task retires anything from this prefix.
  `request_completion` at `acp/prompt.rs:481` checks the threshold before every
  request, while `compact` at `compaction.rs:624` accepts any reduction that
  fits admission, even one still above the threshold. A diagnostic with twenty
  30,800-byte user messages and small answers, using the 272K OpenAI fixture and
  the default estimate, left **209,399 estimated tokens even with an empty
  summary**, above the 195,840 automatic threshold. Four more messages raised
  that lower bound to 250,563, above the 244,800 admission limit. Calibration
  changes when this happens, not whether the retained prefix grows.

  Recommendation: roll back permanent retention of all historical user text and
  the action log before adding more compaction machinery. Keep the active
  request, applicable skill instructions, a summary, and a recent suffix. The
  existing transcript remains the complete record. Keeping recent context and
  calibrating estimates address observed failures without requiring an
  additional permanent history. If full retention is still desired, compare its
  answer quality and total token cost on a long session before keeping it.

- **OX-0046 A temporary summary failure repeats successful summarizer requests**
  (`crates/ox-server/src/acp/prompt.rs:440`): the new retry loop wraps
  `request_completion`, which also performs automatic compaction. Compaction
  errors become `PromptOutcome::ModelRequest` at line 553, so a temporary
  summarizer failure retries the entire compaction. Intermediate summaries and
  their costs were local to the failed invocation and are discarded. A failure
  late in a multi-request summary can therefore repeat substantial paid work;
  the eventual checkpoint excludes the earlier successful requests' costs.

  Evidence: a diagnostic reused the existing large-material fixture pattern to
  require two summary requests. Scripted replies were a successful first
  summary, HTTP 503, two successful summaries, and the ordinary answer. The run
  finished with four summarizer requests, and the first and third request bodies
  were identical. `compaction.rs:603` initializes the summary and cost anew on
  each invocation. Manual `/compact` takes a different path and does not get
  this retry. The retry plan explicitly says summarizer requests stay unretried,
  so this interaction escaped both the plan and its tests.

  Recommendation: give a single request one retry owner. The smallest policy
  correction is to keep compaction failures outside ordinary-completion retries,
  as the plan intended. If summaries should retry too, retry the failing piece
  where the preceding summary is still available. Do not add durable recovery
  state or another retry layer around the whole turn.

#### Architecture

- **OX-0047 An unused authenticated provider can prevent Ox from starting**
  (`crates/ox-server/src/lib.rs:95`): saved credentials automatically activate a
  provider, and every activated catalog must load successfully before Ox
  resolves even an explicit headless `--model`. With both accounts configured,
  an OpenAI refresh or catalog failure prevents an OpenRouter run; an OpenRouter
  catalog outage prevents an OpenAI run. The user cannot choose the healthy
  provider in the picker because the server never starts. Authentication for
  occasional use has become a permanent startup dependency.

  Evidence: `discover_catalogs` at line 83 propagates either catalog error, and
  `run` resolves its requested model only after `load_settings_and_catalog`.
  `startup_requires_credentials_and_does_not_ignore_catalog_failures` explicitly
  verifies this coupling, including that an OpenRouter error prevents trying
  OpenAI. This is the multi-provider plan's chosen policy, not a missing retry.

  Recommendation: retain the shared catalog model and centralized client
  selection, but reconsider treating credentials as a requirement to contact
  every provider. An explicitly selected provider should be usable on its own.
  If the combined picker is needed, unavailable unused providers should not
  prevent startup; report their failure without silently changing the chosen
  model. If switching providers within one process is not used, restoring one
  provider per process is the smaller alternative. Avoid background discovery,
  catalog refresh, and automatic model fallback.

#### Simplicity

- **OX-0048 OpenAI keeps redundant copies of output only to cross-check them**
  (`crates/ox-server/src/openai.rs:559`): the stream retains added-item
  snapshots, completed items, text deltas, argument deltas, and reasoning text,
  then reconciles those representations at completion. The added snapshots and
  the text, argument, and reasoning buffers do not supply the saved answer or
  executed calls: `finish` reads those from completed output items. They serve
  only to reject disagreements between intermediate and final provider output.
  This makes the new parser materially larger and gives it several parallel
  representations to keep consistent.

  Evidence: `process` at lines 443, 470, 473, and 484 fills these buffers;
  `finish` at lines 618, 644, 656, 676, and 710 reads them for equality or
  presence checks. Final tool arguments come directly from the completed item's
  `arguments`. The recorded live integration failure established a need to
  collect `output_item.done` when the terminal output array is empty; it did not
  establish a need for these additional cross-checks.

  Recommendation: choose completed items as the source of the completion and
  emit text/reasoning deltas as provisional display output. Retire the
  added-item map, accumulated argument/text maps, accumulated reasoning string,
  and their reconciliation branches. Keep the completed-item map, reasoning
  paragraph position, successful terminal-event requirement, and validation of
  the final message and tool calls. This is a narrow simplification of the
  OpenAI parser, not a reason to introduce a provider framework or weaken
  authentication.

## What earns its place

The overall direction is sound. The largest addition, OpenAI support, provides
an actually used second inference route. Its authorization flow, rotating-token
lock, and completed-item assembly have concrete purposes. I would keep them and
the concrete provider dispatch. A generic provider trait, plug-in registry, or
shared universal response representation would add concepts without solving a
current problem. Likewise, the providers' different completion rules do not
justify merging their parsers just because both read streamed events.

Keep the small fixes: rejecting command typos, saving turn errors, retrying
ordinary temporary failures, preserving both ends of shell output, counting busy
subagents, and separating reasoning paragraphs. They address observed failures
with local changes. Bash, workspace-path normalization, and filtering globs
after ignore rules also have direct purposes. The last uses a small adapter to
the existing matching library rather than inventing a glob language.

Keep the calibrated estimate for now. The previous review measured premature
compaction, and the replacement derives its ratio from existing usage instead of
introducing another saved setting. Rebuilding requests may later deserve
measurement, but this review found no evidence that it warrants another cache.
Keeping recent context is also reasonable. The absolute newest-batch rule is
less settled: it deliberately gives up compacting a single oversized batch, and
its tests prove retention, not that a real subagent produces a useful final
report. OX-0031 should be treated as needing outcome verification before further
special cases are built around it.

## Evidence and priorities

The benchmark evidence is narrower than the day's changes. The two broad
`deepseek-fixes` comparisons show 36/36 passes on both sides, with total cost
20% and 23% higher for the dirty candidates. Those candidates are not precise
identities for today's final code. The clean `deepseek-head` comparison covers
only `mini-redis-docs`, at `d8223d2`, before the OpenAI and compaction work.
These reports contain zero compactions and zero subagents. They cannot establish
that permanent context retention, latest-batch preservation, or the new cache
key improved outcomes. They also do not isolate the larger default file-read
page, so that tuning change has no established cost benefit yet.

The first rollback candidate is permanent compaction history. The first parser
simplification is redundant OpenAI stream state. The first correction is retry
ownership. Startup coupling needs a deliberate product choice. For the other
tuning changes, measure a representative long session before changing more
policy: total input and summarizer usage, useful final reports, repeated reads,
and completion of the original task matter more than preserving a particular
request shape in a fixture. Sending a cache key is cheap enough to keep while
its effect is measured.

Existing OX-0034 already covers storing entire old and new files for every edit;
it remains a concrete storage simplification and is not duplicated here. The
concurrent tracking cleanup, including closing OX-0040 as won't fix, was
preserved. Automatically allocating a build directory per subagent would trade
build-lock waits for duplicate compilation and disk use. Coordinate who runs
checks first, unless measurement shows separate builds are worth that cost.

## Checks run

- `make check` passed: Markdown, Rust formatting, 282 workspace tests, build,
  and Clippy. Its twelve ignored terminal tests were run separately.
- `make e2e` passed all twelve terminal tests.
- `make check-docs` and `git diff --check` passed after recording this review
  and its issues.
- `cargo test -p ox-server review_ -- --nocapture` passed the two temporary
  diagnostics described in OX-0045 and OX-0046. They were removed afterward;
  `git diff --exit-code` verified both source files returned exactly to HEAD. No
  permanent test guarantee gained, lost, or moved its owning test.
- Live inference, paid benchmarks, and empirical answer-quality comparisons were
  not run.

## Verdict

Keep the concrete fixes and the OpenAI integration. Reconsider the policies that
retain work forever or couple unrelated operations, and remove redundant stream
state before adding abstractions. Four medium findings remain open; none was
fixed during this review. The passing suite establishes implementation behavior,
but the latest compaction choices still need evidence that they improve actual
work at an acceptable cost.
