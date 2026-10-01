# Session transcript review: the multi-provider work run

## Scope and coverage

Reviewed the exported transcript of the `/ox-work` run that implemented
`agents/plans/2026-10-01-002-multi-provider-models.md`: 189 main entries, ten
subagents, three turns, two compactions, and 286 tool calls. The goal was to
find harness limitations, bugs, and rough edges, not to review the code the run
produced. Lenses: correctness, performance, observability, and usability. Each
symptom in the transcript was traced to the server or client code that produced
it.

Gaps: the transcript records no timestamps and no provider error text for the
main turn, so the cause of the main turn's failure is inferred from the
identical failure of a subagent in the same run. Live provider behavior, such as
cache routing, was not exercised.

## Findings

### High

#### Correctness

- **OX-0029 Compaction stops repeating the skill invocation once a later turn
  begins** (`crates/ox-server/src/compaction.rs:138`): `repeated_invocation`
  repeats the covered skill invocation only while it is the latest turn start.
  After the user's "Try again" message, the model never saw the `ox-work`
  instructions again. The final response ignored the required format and the
  commit step was skipped; the work is still uncommitted. Fix: keep repeating
  the latest covered skill invocation across plain user turns until a new skill
  invocation begins.
- **OX-0026 and OX-0027 A single provider error ended the main turn and a
  subagent, with no retry and no record**
  (`crates/ox-server/src/acp/prompt.rs:437`, `crates/ox/src/tui.rs:1162`): the
  main turn ended after entry 453, a successful grep, with nothing persisted.
  Subagent `1b9bf2ce` died on one OpenAI 503
  (`subscription_sharing_user_unavailable`) after 19 edits and reported nothing.
  The retry loop covers only OpenRouter timeouts, and the error reaches the user
  only as a transient terminal notice. Both issues are already open; this run
  shows their cost. Fix: retry 429 and 5xx with backoff for both providers, and
  persist a turn error entry so exports and resumes show why a turn stopped.

### Medium

#### Correctness

- **OX-0031 A compaction immediately before a subagent's final answer loses the
  report** (`crates/ox-server/src/compaction.rs:466`): subagent `dd00ce0f`
  compacted at 123K tokens while composing its report. The summary captured the
  detailed report, and the model then answered in one line: "Context restored.
  Step 5 is implemented". That line is all the parent received. Fix: when a
  final answer follows a compaction with no tool calls between them, forward the
  summary with it, or exempt a turn's final request from automatic compaction.
- **OX-0032 A failed main turn cancels busy subagents and drops their results**
  (`crates/ox-server/src/acp/prompt.rs:227`): subagent `ade27dc2` ran 29 batches
  auditing plan steps 1 through 5 and was still busy when the main turn failed.
  `run.stop_subagents()` ended it, its result never arrived, and the next turn's
  `wait` reported "No subagents exist". Fix: keep subagents alive across a
  failed turn, or deliver their pending messages at the start of the next turn
  in the same session.
- **OX-0035 Shell output truncation keeps only the tail**
  (`crates/ox-server/src/tools/shell.rs:487`): each stream is trimmed from the
  front to fit 16 KiB. Twenty outputs were truncated this way. For `cargo test`
  the first failures were cut, and for `git diff` the first files were cut. Fix:
  keep head and tail, as `tool_result_excerpt` in `compaction.rs:287` already
  does for summarizer material.
- **OX-0025 Enter on a partial slash command sends it as a prompt**
  (`crates/ox/src/tui.rs:924`): the user's "/mo" became a user message, and the
  model spent a 30-second `wait` on it. Only exact command names are matched.
  Already open. Fix: Enter with ghost text completes the command, as Tab does,
  or an unknown slash command is refused.

#### Performance

- **OX-0030 Compaction fires at about half the usable context**
  (`crates/ox-server/src/compaction.rs:26`, `compaction.rs:44`): the token
  estimate is bytes divided by three, and the automatic threshold is 80 percent
  of admission, about 196K estimated tokens for a 272K model. Actual input at
  the two main compactions was 122K and 116K tokens, and 123K in subagent
  `dd00ce0f`. Each compaction forced a re-read of the docs and code costing
  about 60K tokens, and the first summary was weak. Fix: calibrate the estimate
  against the previous response's reported `input_tokens`, or raise the
  bytes-per-token ratio.
- **OX-0033 OpenAI requests set no prompt cache key and cache hits are erratic**
  (`crates/ox-server/src/openai.rs:314`): of 382 requests over 20K tokens, 84
  cached only the 1,664-token system prompt, and about half of the 27M input
  tokens were cached overall. `base_body` sends no `prompt_cache_key`. Fix: send
  the session id as `prompt_cache_key` and check whether anything in the request
  prefix changes between consecutive requests.
- **OX-0034 Every edit stores the whole file twice**
  (`crates/ox-server/src/sessions.rs:508`): `ToolContent::Diff` holds the full
  `old_text` and `new_text`. Thirty-nine edits of `acp.rs` stored about 12 MB,
  most of the 29 MB export. Providers only send the outcome text
  (`openai.rs:264`), so this costs storage and ACP traffic, not context. Fix:
  store a unified diff or the changed range.

#### Usability

- **OX-0036 The subagent limit counts idle subagents whose answers were
  delivered** (`crates/ox-server/src/subagents.rs:171`): four `start_subagent`
  calls failed against idle subagents, forcing two rounds of `stop_subagent`.
  Fix: evict delivered idle subagents when the limit is hit, or count only busy
  ones.

### Low

#### Correctness

- **OX-0037 Reasoning summary parts are joined without a separator**
  (`crates/ox-server/src/openai.rs:446`, `openai.rs:670`): 88 batches show
  reasoning like "**Planning...****Inspecting...**". Fix: join parts with a
  blank line.

#### Simplicity

- **OX-0038 `wait` with no subagents returns a redundant message**
  (`crates/ox-server/src/subagents.rs:244`): the outcome reads "No subagents
  exist.\n\nNo subagents." Fix: return one line.

#### Observability

- **OX-0039 Transcript entries carry no timestamps**
  (`crates/ox-server/src/sessions.rs:1054`): only the session's `updated_at` is
  stored, so an export cannot show how long a turn, a tool call, or a subagent
  took. Fix: store a timestamp per entry.
- **OX-0042 A failed subagent's failure message omits what it changed**
  (`crates/ox-server/src/subagents.rs:359`): subagent `1b9bf2ce` failed after 19
  edits, and the parent learned only the provider error. It launched another
  subagent to audit what the first had done. Fix: list the files the failed
  subagent edited in its failure message.

#### Usability

- **OX-0040 Parallel subagents in one workspace contend for the cargo build
  lock** (`crates/ox-server/src/prompts/subagent_prompt.md:12`): subagents
  waited on "Blocking waiting for file lock on build directory", compiled each
  other's half-finished edits, and fell back to `sleep 10; cargo check`. Fix:
  tell subagents in the subagent prompt to use a private `CARGO_TARGET_DIR`, or
  set one per subagent.

#### Documentation

- **OX-0041 The installed `ox-work` skill points at `docs/agents/` while this
  repo uses `agents/`** (`~/.claude/skills/ox-work/SKILL.md:13`): the model's
  first step was reconciling the two paths. The skill lives outside this
  repository. Fix: make the skills discover the agents directory from
  `AGENTS.md`, or move this repo's `agents/` under `docs/`.

## Checks run

- `make check-docs` on the review, `issues.csv`, and `todo.md`.

## Verdict

The run met the plan's goal but left the work uncommitted because compaction
dropped the skill instructions, and it lost two subagents' work to a single
unretried provider error. Fourteen findings: two high, eight medium, six low, of
which three were already open.
