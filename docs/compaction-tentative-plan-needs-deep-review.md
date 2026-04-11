# Ur Multi-Stage Compaction Scheme

**Inspired by**: Claude Code's layered context management  
**Goal**: 80% of the benefit for 20% of the complexity  
**Date**: 2026-04-11

---

## Current State

Ur has **zero compaction** today. The message list grows unbounded until the API rejects the request with a context-too-long error. The system prompt already promises compression ("The system will automatically compress prior messages..."), but nothing implements it.

What Ur **does** have:
- `LastInputTokens` tracking (from `UsageContent` in assistant messages)
- Per-tool output truncation at write time (`ToolArgHelpers.TruncateOutput`: 100KB / 2000 lines)
- A simple message model: `List<ChatMessage>` with M.E.AI content types
- A transient system prompt rebuilt each turn (never persisted)

What Ur **does not** have (and doesn't need):
- Anthropic-specific prompt caching / cache-editing (multi-provider)
- Complex subagent zombie-message cleanup (single subagent tool only)
- Feature-flag-gated compaction variants
- Attachment / delta message system

---

## The 80/20 Analysis

Claude Code has 7 compaction mechanisms. Here's the ROI ranking for Ur:

| Mechanism                     | Complexity | Impact for Ur                                          | Verdict                 |
| ----------------------------- | ---------- | ------------------------------------------------------ | ----------------------- |
| **Tool result clearing**      | Low        | **Very high** — tool results are 60-80% of context     | ✅ **Stage 1**           |
| **Autocompact (summarize)**   | Medium     | **Very high** — the only thing preventing hard crashes | ✅ **Stage 2**           |
| **Reactive compact**          | Low        | **High** — safety net when estimation is wrong         | ✅ **Stage 3**           |
| Context collapse (granular)   | High       | Medium — nicer UX, not essential                       | ❌ Defer                 |
| Cached microcompact           | High       | Low — requires Anthropic cache-editing API             | ❌ Skip (multi-provider) |
| Time-based microcompact       | Low        | Low — marginal gain over simple clearing               | ❌ Defer                 |
| Snip compact (zombie removal) | Medium     | Low — Ur's subagent story is simple                    | ❌ Defer                 |
| Partial compact (user pivot)  | Medium     | Low — nice UX, not critical                            | ❌ Defer                 |

---

## Stage 1: Tool Result Clearing

**What**: Replace old tool result content with `[Tool result cleared]` before sending to the API.  
**When**: Every API call, before sending messages.  
**Trigger**: Tool results older than the most recent N assistant turns (configurable, default: 3).  
**Why this first**: Zero API cost. Pure local text manipulation. Eliminates 60-80% of context bloat.  

### Implementation

New file: `src/Ur/AgentLoop/Compaction/ToolResultClearer.cs`

```
Pipeline position: AgentLoop.BuildLlmMessages() — after the UsageContent filter
```

Logic:
1. Walk the message list backwards from the end
2. Count assistant messages (these are "turn boundaries")
3. For any `FunctionResultContent` in tool messages older than the threshold, replace content with `"[Tool result cleared]"`
4. This is a **projection** — mutate a copy for the API, not the persistent `_messages` list

Key design decisions:
- **Projection, not mutation**: Build a filtered view for the API call. The persistent `_messages` list stays intact (needed for session persistence and potential re-summarization).
- **Only clear "heavy" tools**: read_file, bash, grep, glob. Tools like todo_write have small results.
- **Keep the most recent N tool results intact**: The model needs recent context.

### Modified files
- `AgentLoop/AgentLoop.cs` — call `ToolResultClearer` inside `BuildLlmMessages()`
- New: `AgentLoop/Compaction/ToolResultClearer.cs`

### Estimated effort: ~1-2 hours

---

## Stage 2: Autocompact (Conversation Summarization)

**What**: When context approaches the limit, summarize the conversation and replace old messages with a compact summary.  
**When**: Before each API call, after tool result clearing.  
**Trigger**: `LastInputTokens > ContextWindow * 0.80` (80% threshold).  

### Implementation

New files:
- `src/Ur/AgentLoop/Compaction/Autocompactor.cs` — trigger logic + orchestration
- `src/Ur/AgentLoop/Compaction/ConversationSummarizer.cs` — LLM call for summarization
- `src/Ur/AgentLoop/Compaction/CompactPrompt.cs` — summarization prompt template

Logic:
1. Check if `LastInputTokens` exceeds the threshold
2. Build a summarization prompt (simpler than Claude Code's 9-section format — start with 5):
   - Primary Request and Intent
   - Key Files and Code Changes
   - Errors and Fixes
   - Current Work / Pending Tasks
   - All User Messages (bulleted)
3. Send the full conversation to the LLM with the summarization prompt, requesting text-only output (no tool calls)
4. Replace old messages with a single user message containing the summary, wrapped in a marker:
   ```
   <compact-summary>
   [summary content]
   </compact-summary>
   ```
5. Preserve the most recent K messages (minimum 5 messages with content, minimum 10K tokens worth)
6. Update `LastInputTokens` after compaction

Key design decisions:
- **Use the same model**: Unlike Claude Code's forked-agent approach, just make a separate API call. Simpler, works with all providers.
- **Mutate `_messages`**: Unlike Stage 1 (projection), this permanently rewrites the message list. Persist the compacted state to disk (rewrite the JSONL, or append the compact summary as a new message + mark old ones as superseded).
- **No session memory fast-path yet**: Claude Code has a path that skips the summarization API call if "session memory" was already extracted. That's an optimization — the base case is always "send to LLM, get summary."
- **Persist as append**: Append the compact-summary message to the JSONL file + a `compact_boundary` marker. On session reload, skip messages before the last boundary. This avoids rewriting the entire JSONL.

### Compact summary prompt

```
You are summarizing a conversation between a user and an AI coding assistant.
Produce a concise summary with these sections:

## Primary Request and Intent
What the user asked for and their goals.

## Key Files and Code Changes
Files that were read, modified, or created. Include important code snippets.

## Errors and Fixes
Any errors encountered and how they were resolved.

## Current Work
What was most recently being worked on. Include any pending tasks.

## User Messages
Brief summary of each user message in order.

Output ONLY the summary text, no tool calls.
```

### Modified files
- `AgentLoop/AgentLoop.cs` — add compaction check in the `while(true)` loop, before `BuildLlmMessages()`
- `Sessions/UrSession.cs` — expose context window info; handle compact-boundary persistence
- `Sessions/SessionStore.cs` — support compact-boundary marker in JSONL
- New: `AgentLoop/Compaction/Autocompactor.cs`
- New: `AgentLoop/Compaction/ConversationSummarizer.cs`
- New: `AgentLoop/Compaction/CompactPrompt.cs`

### Estimated effort: ~4-6 hours

---

## Stage 3: Reactive Compact (Safety Net)

**What**: When the API returns a context-too-long error, truncate oldest messages and retry.  
**When**: On API error (HTTP 413 / context length exceeded).  
**Trigger**: The LLM API rejects the request.

### Implementation

Logic:
1. Catch the context-too-long error in `StreamLlmAsync` (it already catches exceptions into `LlmErrorHolder`)
2. Instead of yielding a fatal `TurnError`, try recovery:
   a. Strip the oldest "round" of messages (assistant + tool result pair)
   b. Retry the API call
   c. If still too long, strip another round
   d. After 3 failed retries, give up with the fatal error
3. If recovery succeeds, the turn continues normally

Key design decisions:
- **Round-based truncation**: Remove complete assistant+tool-result pairs, never mid-conversation
- **Retry limit**: 3 attempts, then fail
- **No summarization**: This is emergency surgery, not elegant compaction. The summary comes from Stage 2's proactive compaction; this is the parachute.

### Modified files
- `AgentLoop/AgentLoop.cs` — add retry logic around `StreamLlmAsync` call

### Estimated effort: ~2-3 hours

---

## The Pipeline (Stages 1-3 Combined)

```
Each API call:
  1. [Stage 2] Autocompactor — check threshold, summarize if needed
  2. [Stage 1] BuildLlmMessages() — clear old tool results (projection)
  3. Send to API
  4. [Stage 3] On context-too-long → truncate oldest rounds → retry
```

---

## What We're Deferring (And Why)

| Deferred Feature                                  | Why                                                                                                                            |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| **Context Collapse** (granular segment summaries) | Complex state management. Autocompact gives 90% of the benefit. Revisit if users report jarring context loss after compaction. |
| **Cached Microcompact**                           | Requires Anthropic-specific cache-editing API. Ur is multi-provider.                                                           |
| **Time-based Microcompact**                       | Marginal benefit over Stage 1's turn-count-based clearing.                                                                     |
| **Snip Compact** (zombie removal)                 | Ur has one subagent tool, not a complex agent hierarchy.                                                                       |
| **Partial Compact** (user-selected pivot)         | Nice UX but not critical. Can add as a `/compact` slash command later.                                                         |
| **Session Memory fast-path**                      | Optimization on top of Stage 2. Add when we have session memory extraction.                                                    |
| **Tool result budget** (per-message byte limits)  | Ur already truncates at write time. Double-truncation is marginal.                                                             |

---

## Context Window Sizing

Ur is multi-provider, so context window sizes vary. Approach:

1. **Provider declares context window**: Add `int? ContextWindowTokens` to `IProvider` / `ProviderConfig`. Look up from a known model table (hardcoded for popular models, configurable for custom).
2. **Fallback**: If unknown, assume 128K tokens (safe for most modern models).
3. **Threshold**: `AutocompactThreshold = ContextWindow * 0.80`
4. **Buffer**: Reserve 8K tokens for output (configurable).

### Modified files
- `Providers/IProvider.cs` — add `GetContextWindowTokens(string model)`
- `Providers/ProviderConfig.cs` — model context window lookup table
- `Configuration/UrOptions.cs` — optional override setting

---

## File Structure After Implementation

```
src/Ur/
  AgentLoop/
    AgentLoop.cs              ← modified (pipeline integration)
    AgentLoopEvent.cs
    SubagentRunner.cs
    ToolInvoker.cs
    Compaction/               ← new directory
      ToolResultClearer.cs    ← Stage 1
      Autocompactor.cs        ← Stage 2 trigger + orchestration
      ConversationSummarizer.cs ← Stage 2 LLM call
      CompactPrompt.cs        ← Stage 2 prompt template
```

---

## Implementation Order

1. **Stage 1** (Tool Result Clearing) — immediate, no API cost, biggest single win
2. **Context window sizing** — needed for Stage 2's threshold
3. **Stage 2** (Autocompact) — prevents hard context limits
4. **Stage 3** (Reactive Compact) — safety net for edge cases
5. **System prompt update** — remove the "automatically compress" promise until Stage 2 ships, or keep it as forward-compatible

Total estimated effort: **~8-12 hours** for all three stages.