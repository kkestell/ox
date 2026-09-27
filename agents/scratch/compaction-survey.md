# Session Compaction Survey — 9 Agent Harnesses

Source: shallow clones in `~/projects/harness-research/` (opencode, codex,
gemini-cli, openhands, cline, goose, crush, qwen-code, grok-build).
Method: quick survey (targeted grep + 1–3 key files per repo), not deep reverse engineering.

Headline: every harness does LLM summarization plus a manual command. None
keeps raw history verbatim — all replace old turns with a summary message and
rebuild the system prompt fresh.

## Comparison

| Repo | Manual / Auto | Trigger | Summarizer model | What survives |
|---|---|---|---|---|
| opencode | Both | est. request > `context - max(out, 20k buffer)`; overflow retry; background prune | session model or `compaction` agent model, 4k output cap | summary checkpoint + verbatim recent tail (~25% usable, min 2k / max 15k tokens) |
| codex | Both | `model_auto_compact_token_limit`, post-turn % threshold, `/compact` | same turn model (or server-side compaction) | recent user-message text (≤20k tokens) + summary as last user message |
| gemini-cli | Both (`/compress`) | last prompt ≥ 0.5 × model limit (configurable) | dedicated `chat-compression-*` alias (e.g. 3-pro/flash) | fresh system/env + summary pseudo-turn + newest ~30% of turns |
| openhands | Both — note: this clone is frontend-only, backend not visible | event count: 240 events default (planning agent: 100) | same agent LLM | summary event at offset + `keep_first: 6` in planning preset; details in backend |
| cline | Both (`/compact`) + overflow recovery | est. tokens ≥ 0.9 × max input (default 128k budget) | current session model, thinking off, 8k output cap; `basic` mode = no LLM | `[summary + tail after safe cut]`; full transcript kept in sidecar file |
| goose | Both (`/compact`, `/summarize`) | usage > 0.8 × context limit | same active provider/model, no tools | summary (role=user) + continuation note; originals demoted to invisible |
| crush | Both (`summarize`, not `/compact`) | remaining ≤ 20% of window (or 20k if window >200k) | same large/session model | summary message only; everything before cut never sent (still in DB) |
| qwen-code | Both (`/compress`, `/compress-fast` no-AI) | 0.85 × window minus 13k buffer; also HTTP 413 + image-count overflow | dedicated compaction model (`/model --compaction`) else main model, ≤20k output | summary-as-user + re-read file attachments (≤5) + images (≤3); no verbatim tail |
| grok-build | Both (`/compact`) | 85% of window (80 for some models) | `grok-4.20` default, overridable | system verbatim + AGENTS.md + last query + in-flight tail + summary |

## Tool calls / outputs in compaction

- Args kept, outputs truncated to ~2k chars: opencode (2,000 chars +
  `[truncated]`), cline agentic (2,000 chars; images → `[image:type]`,
  thinking dropped).
- Dropped entirely from new context, survive only via summary text: codex
  (only user text survives), crush (no tail at all), qwen-code (media →
  placeholders; 413 path caps text at 4,000 chars), grok-build (old tool
  history dropped; only in-flight tail kept verbatim).
- Budgeted newest-first: gemini-cli (50k token budget for
  `functionResponse`s; over-budget older outputs → last 30 lines + spill to
  temp file; generic 40k-char tool cap), goose (middle-out drop of tool
  responses at 0/10/20/50/100% if the summarizer itself overflows).
- Deterministic non-LLM fallbacks: cline `basic` (drops tool pairs except
  newest fitting suffix, keeps last 3 assistant texts + `<SYSTEM_NOTICE>`),
  qwen-code `/compress-fast`, goose per-tool-pair summarization behind a
  flag, opencode background `prune` (clears old tool output past 40k-token
  protection window, keeps last 2 turns).

## Summarizer prompt shape (all similar)

Structured handoff note with sections like goal / state / files / errors /
next step. Notable variants:

- opencode: `Objective / Important Details / Work State (Completed, Active,
  Blocked) / Next Move / Relevant Files`; prior summary is discarded so
  "anything not carried forward is lost."
- gemini-cli / qwen-code / goose / grok-build: XML/JSON `<state_snapshot>`
  with ~9 fixed sections (intent, concepts, files+snippets, errors quoted
  verbatim, all user messages, pending tasks, next step); scratchpad /
  `<analysis>` stripped before persisting. Gemini adds a second probe pass
  ("did you omit file paths?") and aborts on inflated/empty output.
- codex: "CONTEXT CHECKPOINT … handoff summary for another LLM"; installed
  with a `SUMMARY_PREFIX` wrapper.
- crush: "This summary will be the ONLY context available… Err on too much
  detail" + todo list.

## Per-repo details

### opencode

- Auto pre-turn budget check + one-shot overflow recovery + background
  deterministic `prune` of old tool outputs. Manual
  `POST /api/session/:sessionID/compact`.
- Auto when estimated request (system+messages+tools) >
  `context - max(outputTokens, compaction.buffer)`. Defaults: `auto: true`,
  `buffer: 20_000`, `keep.tokens: 8_000`. Overflow: provider
  `context_overflow` → single `compactAfterOverflow`, else terminal fail.
- Summarizer: `compaction` agent model if configured, else session model,
  tools disabled, 4,096 output cap. Base instruction plus prior-summary
  update instructions and a fixed template (`Objective / Important Details /
  Work State / Next Move / Relevant Files`).
- Full transcript stays durable; active context replaced by one hidden
  checkpoint `{summary, recent}`. Prior summaries roll forward (update, not
  append). Recent tail kept verbatim (~25% of usable, 2k–15k tokens).
  System prompt rebuilt fresh, not summarized.
- Tool calls serialized as `[Assistant tool call]: name(args-json)` +
  `[Tool result]`; args fully kept, outputs truncated to 2,000 chars.
  `prune` clears old tool output past a 40k-token protection window (last 2
  turns and `skill` outputs exempt).
- Key files: `packages/core/src/session/compaction.ts`,
  `packages/opencode/src/session/compaction.ts`,
  `packages/opencode/src/session/overflow.ts`,
  `packages/opencode/src/agent/prompt/compaction.txt`.

### codex

- Manual `/compact` / `thread/compact/start`; auto pre-turn, mid-turn, and
  post-turn. Three implementations: inline task, server-side Responses API
  `compaction` item, token-budget reset path.
- Triggers: `model_auto_compact_token_limit`,
  `model_post_turn_compact_threshold_percent`, `compact_prompt` override,
  `features.token_budget` fallback path.
- Summarizer: same turn model (remote-v2 delegates to server). Default
  prompt: "CONTEXT CHECKPOINT COMPACTION. Create a handoff summary…
  progress, decisions, constraints, next steps." Installed with a
  `SUMMARY_PREFIX` wrapper.
- Replacement history = re-injected initial context + selected recent user
  messages (≤20,000 tokens) + summary as last user message. Only
  user-message text survives; assistant/tool outputs exist only via the
  summary.
- Key files: `codex-rs/core/src/compact.rs`, `compact_remote_v2.rs`,
  `compact_token_budget.rs`, `codex-rs/core/src/session/turn.rs`,
  `codex-rs/prompts/templates/compact/`.

### gemini-cli

- `/compress` command + automatic `tryCompressChat`. Trigger: last prompt
  tokens ≥ `compressionThreshold` × model limit (default 0.5,
  configurable); absolute `context.limit` also supported. `force=true`
  bypasses threshold.
- Two LLM calls with a dedicated `chat-compression-*` model alias
  (UTILITY_COMPRESSOR role). System prompt asks for a dense XML
  `<state_snapshot>` (overall_goal, active_constraints, key_knowledge,
  artifact_trail, file_system_state, recent_actions, task_state) with a
  private scratchpad. Phase-3 probe ("did you omit file paths/tool
  results?") verifies; aborts on inflated/empty output.
- Split: compress oldest ~70%, keep newest ~30% at a user turn without
  `functionResponse`. New history = fresh system/env + summary pseudo-turn
  + kept tail. Full pre-compress history persisted to disk for resume.
- Tool outputs: 50k-token newest-first budget for `functionResponse`s;
  over-budget older outputs → last 30 lines + spill to temp file; generic
  40k-char cap; opt-in per-tool summarization (currently only
  `run_shell_command`).
- Key files: `packages/core/src/context/chatCompressionService.ts`,
  `packages/core/src/prompts/snippets.ts`,
  `packages/core/src/config/defaultModelConfigs.ts`,
  `packages/cli/src/ui/commands/compressCommand.ts`.

### openhands (partial — frontend clone only)

- Automatic `enable_default_condenser: true` by default; manual "Compact"
  CTA → `POST /api/conversations/{id}/condense`.
- Trigger is event-count based: `condenser_max_size` default 240 (planning
  agent override: `LLMSummarizingCondenser { max_size: 100, keep_first: 6 }`).
- LLM summarization via `LLMSummarizingCondenser`; same agent LLM in this
  repo. Prompt lives in the backend (not in this clone).
- Backend emits `Condensation { forgotten_event_ids, summary?,
  summary_offset? }`; forgotten events leave the LLM view, summary inserted
  at offset. Tool handling not visible from the frontend.
- Key files: `src/services/settings.ts`, `src/types/settings.ts`,
  `src/routes/condenser-settings.tsx`,
  `src/hooks/mutation/use-condense-conversation.ts`,
  `src/api/agent-server-adapter.ts:1450-1462`.

### cline

- Automatic (default strategy `agentic`) + manual `/compact` + forced
  deterministic compaction on `ContextLengthExceeded` with one retry.
- Auto when estimated request tokens ≥ 0.9 × max input (default 128k
  budget). Estimate ≈ 3 chars/token, floored by provider-reported prior
  input tokens. Target ~0.7 × trigger; `preserveRecentTokens` default
  20,000. Pluggable `compact()` + message-builder hooks.
- Agentic summarizer: current session model, thinking disabled, 8k output
  cap. System: "Summarize the provided coding session into a concise
  continuation note with detailed next steps." `basic` mode is
  deterministic, no LLM.
- Canonical transcript kept full-fidelity; working context in a
  hash-validated sidecar (`${sessionId}.compaction.json`). Agentic result:
  summary message + tail after a safe cut (never orphans tool_result).
  System prompt untouched.
- Agentic input truncates tool results / file content to 2,000 chars,
  images → placeholders, thinking dropped. Basic drops tool pairs except
  the newest fitting suffix, keeps last 3 assistant texts plus a
  `<SYSTEM_NOTICE>`.
- Key files:
  `sdk/packages/core/src/extensions/context/{compaction,compaction-shared,agentic-compaction,basic-compaction}.ts`,
  `sdk/packages/core/src/session/models/session-compaction.ts`.

### goose

- Automatic `check_if_compaction_needed` + manual `/compact` (alias
  `/summarize`). Optional per-tool-pair summarization behind
  `GOOSE_TOOL_PAIR_SUMMARIZATION`; `CompactingProvider` retries
  `ContextLengthExceeded` against compacted history.
- Trigger: usage > 0.8 × model context limit (`GOOSE_AUTO_COMPACT_THRESHOLD`;
  ≤0/≥1 disables). Token source: session `total_tokens` or local estimator.
- Summarizer: same active provider/model, no tools. Request: "Please
  summarize the conversation history provided in the system prompt."
  User-overridable `compaction.md` asks for one JSON block (`user_intent,
  technical_concepts, files, errors_and_fixes, problem_solving,
  user_messages, pending_tasks, current_work, next_step`); rendered via
  `compaction_summary.md`.
- Originals demoted to `user_visible + agent_invisible`; new context =
  summary (role=user) + continuation note ("do not mention… continue
  naturally") + (auto only) last text-only user message. System prompt and
  tool schemas are outside the compacted list.
- Tool calls formatted in full (`tool_request(name): {args JSON}`,
  `tool_response: {text}`); images/docs → placeholders, thinking dropped.
  Summarizer overflow retries dropping tool responses middle-out at
  0/10/20/50/100%.
- Key files: `crates/goose-context-management/src/{lib,summarize,format}.rs`,
  `crates/goose-context-management/src/prompts/compaction.md`,
  `crates/goose/src/context_mgmt/mod.rs`.

### crush

- Manual `summarize` command (note: `compact_mode` here is only a TUI
  layout flag) + automatic `StopWhen` hook.
- Auto when `remaining = window - (prompt + completion)` ≤ 20% of window
  (or 20k if window > 200k). Skipped if window unknown; `disable_auto_summarize`
  config.
- Summarizer: same large/session model. System prompt (`summary.md`):
  "This summary will be the ONLY context available… Err on too much
  detail" (Current State / Files & Changes / Technical Context / Strategy /
  Exact Next Steps). User prompt: "Provide a detailed summary of our
  conversation above." + todo list.
- Full replace by pointer: summary stored as assistant message with
  `IsSummaryMessage=true`; next turn loads only messages after it.
  Everything before is never sent (still in DB). Summary re-roled to User
  in the prompt; system prompt re-added fresh.
- Prior tool calls/results dropped entirely — only their description inside
  the summary survives. No truncation or verbatim tail.
- Key files: `internal/agent/agent.go` (thresholds ~55, `StopWhen` ~1038,
  `Summarize` ~1335, `getSessionMessages` ~1686), `internal/agent/templates/summary.md`,
  `internal/db/sql/messages.sql`.

### qwen-code

- Manual `/compress` (+ focus text, `/compress-fast` no-AI variant) +
  automatic pre-turn gate + reactive overflow recovery.
- Three-tier ladder: auto at min(0.85 × window, window − 13k buffer), warn
  and hard thresholds derived; also HTTP 413 payload overflow and
  screenshot-count overflow triggers; 3-strikes failure breaker.
- Dedicated compaction model (`/model --compaction`), else main model;
  falls back if the compaction model's window is too small. Cap 20k output
  tokens, thinking disabled. Prompt asks for a dense `<state_snapshot>`
  with 9 fixed sections; `<analysis>` stripped before persisting.
  PreCompact hook text appended as `Additional Instructions:`.
- Old history replaced by summary-as-user + file attachments re-read from
  disk (≤5 files, ≤5k tokens each, ≤50k total) + images (≤3) + state /
  plan-mode / subagent blocks. No verbatim message tail.
- Side-query input slimmed: media → placeholders; 413-recovery caps text
  parts at 4,000 chars; dangling trailing function calls stripped.
- Key files: `packages/core/src/services/chatCompressionService.ts`,
  `packages/core/src/services/postCompactAttachments.ts`,
  `packages/core/src/services/compactionInputSlimming.ts`,
  `packages/core/src/core/prompts.ts:919`.

### grok-build

- Manual `/compact` (+ focus text); automatic token-percentage gate.
  Library/host split: engine in `xai-grok-compaction`, triggers in hosts.
  Three styles: whole-session full-replace, per-step intra (tail-keep),
  chunked inter (between-turn).
- Auto at 85% of window by default (80 for some models); intra target 50%.
- Default compaction model `grok-4.20`, overridable (agent config >
  harness YAML > default). Structured prompt: faithful summary so a
  successor seeing only original query + summary can continue; at most a
  few thousand words; 9 numbered sections (request, concepts,
  files+snippets, errors+fixes, problem solving, all user messages, pending
  tasks, current work, single next step with verbatim quote); no tools.
- Assembly: system verbatim + user_info prefix + AGENTS.md + last query +
  in-flight tail + summary + reminder. Everything else discarded.
  Degenerate summaries (<500 chars) retried. Turn selection is
  tool-pair-safe (no orphan tool results).
- Old tool calls/results dropped; only the working-tail in-flight messages
  kept verbatim.
- Key files:
  `crates/common/xai-grok-compaction/src/code_compaction/{compact,assemble,summary,prompt}.rs`,
  `crates/common/xai-grok-compaction/src/code_compaction/templates/full_replace_summary_prompt.txt`,
  `crates/common/xai-grok-compaction/src/intra_compaction/`.

## Caveat

openhands findings are partial: the clone is the frontend repo, so
trigger/config and event shapes are confirmed but the summarizer prompt and
tool-handling live server-side.
