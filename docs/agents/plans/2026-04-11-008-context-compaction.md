# Context Compaction — Multi-Stage Pipeline

## Goal

Prevent context-window overflow by introducing a two-stage compaction pipeline: tool-result clearing (projection) and conversation summarization (mutation). Today Ur has zero compaction — the message list grows until the API rejects it. Rather than adding reactive error-recovery for context-too-long API errors, we use conservative compaction thresholds to avoid hitting the limit in the first place.

## Desired outcome

Long sessions run without hitting context-window limits. The compaction pipeline works silently: the user sees no jarring loss of context, and the system prompt's promise of automatic compression is finally delivered.

## How we got here

The tentative design in `docs/compaction-tentative-plan-needs-deep-review.md` proposes three stages inspired by Claude Code's layered approach. During planning we resolved four design questions:

1. **Pipeline ordering**: Stage 1 (tool-result clearing) runs before Stage 2 (autocompact). Clearing is free and may avoid the expensive summarization call entirely.
2. **Tool-result scope**: Clear ALL tool results older than N turns, not just "heavy" tools. Simpler, avoids maintaining a tool-category list, and old results of any type are stale.
3. **Summarization model**: Same model, same provider. Simplest path — one extra API call per compaction.
4. **Compaction ownership**: Autocompact lives in UrSession (pre-turn), not AgentLoop. AgentLoop stays stateless with no persistence knowledge. Tool-result clearing stays in AgentLoop's `BuildLlmMessages` as a projection.

## Approaches considered

### Option 1 — All-in-AgentLoop (tentative doc's approach)

- Summary: Put all three stages in AgentLoop. Autocompactor mutates `_messages` and introduces persistence into AgentLoop.
- Pros: Compaction logic is co-located.
- Cons: Violates the current boundary — AgentLoop has zero knowledge of persistence today. Creates bidirectional coupling between AgentLoop and session storage.
- Failure modes: AgentLoop becomes a God class mixing LLM orchestration with session management.

### Option 2 — Split by responsibility (recommended)

- Summary: Each stage lives where its responsibility naturally belongs. Stage 1 (projection) in AgentLoop's `BuildLlmMessages`. Stage 2 (permanent mutation + persistence) in UrSession as a pre-turn step.
- Pros: Preserves existing boundaries. No new coupling. Each component can be tested in isolation.
- Cons: Compaction logic is distributed across two classes.
- Failure modes: Stage coordination requires UrSession to pass the right context-window info.

### Option 3 — Dedicated Compactor mediator

- Summary: New `Compactor` class sits between UrSession and AgentLoop, owning all three stages.
- Pros: Clean single-responsibility module.
- Cons: Over-engineered for a pre-turn check + a projection + a retry. Adds a new layer to a system that doesn't need one yet.
- Failure modes: Nano-module that just delegates to the components it wraps.

## Recommended approach

Option 2 — split by responsibility. Each stage stays at the abstraction level where its dependencies already exist. AgentLoop keeps its stateless projection model. UrSession extends its pre-turn setup (already does readiness checks, slash command expansion, model snapshots) with a compaction check. Stage 3 (reactive error recovery) is omitted — conservative compaction thresholds make it unnecessary, and it would require either fragile heuristic error-message parsing or plumbing provider-specific error classification into AgentLoop.

## Related code

- `src/Ur/AgentLoop/AgentLoop.cs` — The `RunTurnAsync` while loop and `BuildLlmMessages` projection. Stage 1 inserts into `BuildLlmMessages`.
- `src/Ur/Sessions/UrSession.cs` — Owns `_messages`, persistence via `PersistPendingMessagesAsync`, `LastInputTokens` tracking. Stage 2 lives here as a pre-turn step.
- `src/Ur/Sessions/SessionStore.cs` — JSONL append-only persistence. Needs compact-boundary marker support for Stage 2.
- `src/Ur/Providers/ProviderConfig.cs` — `GetContextWindow(provider, model)` provides the denominator for threshold math.
- `src/Ur/Hosting/UrHost.cs` — `ResolveContextWindow(modelId)` wraps ProviderConfig with ModelId parsing. Passes context window into session creation.
- `src/Ur/Tools/ToolArgHelpers.cs` — Existing per-tool output truncation at write time (`TruncateOutput`). Complementary to Stage 1.
- `tests/Ur.Tests/` — TestHostBuilder pattern for integration-style tests with real DI.

## Current state

- **No compaction exists.** Messages grow unbounded until API rejection.
- **`LastInputTokens`** is tracked per-turn from `UsageContent` in assistant messages. Available as the threshold signal for Stage 2.
- **`BuildLlmMessages` already projects.** It strips `UsageContent` before sending to the LLM. Stage 1 extends this projection with tool-result clearing.
- **Session persistence is append-only JSONL.** `SessionStore.AppendAsync` writes one JSON line per message. `ReadAllAsync` deserializes all lines. No boundary/marker concept exists yet.
- **`_messages` is a mutable `List<ChatMessage>` shared between UrSession and AgentLoop.** Stage 2 will mutate this list (replacing old messages with a summary), then persist the compacted state.

## Structural considerations

**Hierarchy**: Stage 1 (projection) → AgentLoop. Stage 2 (mutation + persistence) → UrSession. Dependencies flow downward: UrSession → AgentLoop → IChatClient. No new upward dependencies introduced.

**Abstraction**: `BuildLlmMessages` is already a filtering/projection step — adding tool-result clearing is at the same abstraction level. UrSession's `RunTurnAsync` already performs pre-turn setup — adding a compaction check is at the same abstraction level. The summarization LLM call is encapsulated in a dedicated helper so UrSession doesn't drop into LLM-call plumbing.

**Modularization**: New `src/Ur/Compaction/` namespace holds the compaction components. This avoids bloating AgentLoop or Sessions with compaction details. Each class has a single purpose:
- `ToolResultClearer` — pure projection function, no state
- `Autocompactor` — orchestrates threshold check + summarization + message replacement
- `SummaryPrompt` — prompt template, isolated for testability

**Encapsulation**: Autocompactor receives its inputs as parameters (messages, chat client, context window, last input tokens) rather than reaching into UrSession. SessionStore's compact-boundary support is an internal implementation detail — the public API stays `AppendAsync` + `ReadAllAsync`.

## Implementation plan

### Stage 1 — Tool Result Clearing (projection)

- [ ] Add `TurnsToKeepToolResults` to `UrOptions` (int, default 3). Bound from `"ur:turnsToKeepToolResults"` in settings.json. Controls how many recent assistant turns' tool results are preserved verbatim; older results are replaced with `"[Tool result cleared]"` during projection.
- [ ] Create `src/Ur/Compaction/ToolResultClearer.cs`. Static method: `IEnumerable<ChatMessage> ClearOldToolResults(IEnumerable<ChatMessage> messages, int turnsToKeep)`. Walks backwards counting assistant messages as turn boundaries. For any `FunctionResultContent` in messages older than `turnsToKeep` turns, replaces content with `"[Tool result cleared]"`. Returns a new sequence — never mutates the input.
- [ ] Wire `ToolResultClearer` into `AgentLoop.BuildLlmMessages()`. Pass `turnsToKeep` from the configuration. Insert after the existing `UsageContent` filtering step. The projection chain becomes: strip UsageContent → clear old tool results → yield to caller.
- [ ] Unit test: messages with tool results spanning 5 turns → only last 3 turns' results are intact. Earlier results replaced with `"[Tool result cleared]"`.
- [ ] Unit test: messages with no tool results → pass through unchanged.
- [ ] Unit test: exactly N turns of tool results → nothing cleared (boundary condition).
- [ ] Unit test: verify original message list is not mutated (projection safety).

### Stage 2 — Autocompact (conversation summarization)

- [ ] Add `ResolveContextWindow` to `UrSession` constructor parameters so the context window is available for threshold math. `UrHost` already has `ResolveContextWindow(modelId)` — pass the resolved `int?` when creating sessions. Store as `_contextWindow`.
- [ ] Create `src/Ur/Compaction/SummaryPrompt.cs`. Static method returns the summarization system prompt. Five sections: Primary Request and Intent, Key Files and Code Changes, Errors and Fixes, Current Work / Pending Tasks, User Messages. Output must be text only — no tool calls.
- [ ] Create `src/Ur/Compaction/Autocompactor.cs`. Public method: `async Task<bool> TryCompactAsync(List<ChatMessage> messages, IChatClient client, int contextWindow, long lastInputTokens, CancellationToken ct)`. Returns true if compaction occurred.
  - Threshold: `lastInputTokens > contextWindow * 0.60`. Conservative threshold — leaves ample headroom so reactive error recovery is unnecessary.
  - Build summarization messages: system prompt (from `SummaryPrompt`) + the full conversation as a single user message.
  - Call `client.GetResponseAsync` (non-streaming, no tools) to get the summary.
  - Determine the cut point: preserve the most recent messages that contain at least 5 content-bearing messages (skip empty tool scaffolding). Walk backward from the end.
  - Replace `messages[0..cutPoint]` with a single user message: `<context-summary>\n{summary}\n</context-summary>`. User role is chosen deliberately: (a) System role would conflict with the transient system prompt that `BuildLlmMessages` already prepends each turn, (b) M.E.AI's `ChatRole` has no custom/summary variant, (c) the `<context-summary>` tag makes it machine-recognizable for future compaction passes and session replay tools.
  - Return true.
- [ ] Add compact-boundary support to `SessionStore`:
  - Define a sentinel line format: `{"__compact_boundary":true}` — a JSON object that won't deserialize as a valid `ChatMessage`.
  - New method `AppendCompactBoundaryAsync(Session, CancellationToken)` writes the sentinel line.
  - Update `ReadAllAsync`: walk the lines array backward to find the index of the last compact-boundary sentinel. If found, slice to only the lines after it before entering the deserialization loop. This must happen before the `JsonSerializer.Deserialize` / `JsonException` catch block — boundary markers must be detected as intentional delimiters, not confused with crash-corrupted lines that the existing error-recovery path silently skips. Log a debug message noting how many pre-boundary messages were skipped.
- [ ] Add pre-turn compaction check in `UrSession.RunTurnAsync`, after the readiness gate and slash-command interception, before appending the user message:
  - If `_contextWindow` is null or `LastInputTokens` is null → skip (no data to check). Log a debug message when skipping due to null `_contextWindow` so operators can diagnose why proactive compaction isn't running.
  - Call `ToolResultClearer` to estimate post-clearing token savings? No — `LastInputTokens` is from the previous API call and already reflects what the LLM saw. The threshold check uses raw `LastInputTokens` vs `_contextWindow * 0.60`.
  - If threshold exceeded: create a chat client for the active model, call `Autocompactor.TryCompactAsync`, then persist by writing a compact-boundary + the new messages (summary + preserved tail) via `SessionStore`.
  - Update `LastInputTokens` to null after compaction (will be refreshed on the next LLM call).
  - Yield a `Compacted` event (new `AgentLoopEvent` subtype) so the UI can display a system message like "Context compacted — older messages summarized." This keeps the user informed about why early context may be vaguer.
- [ ] Persistence flow for compaction: after `TryCompactAsync` returns true, call `_sessions.AppendCompactBoundaryAsync`, then append each message in the now-compacted `_messages` list via `_sessions.AppendAsync`. After all compacted messages are persisted, set `persistedCount = _messages.Count` explicitly — this is the critical invariant: persistedCount must always equal the number of messages written to disk. The user message added after compaction will then be the next un-persisted message. Comment the reset in code so future maintainers understand the invariant.
- [ ] Unit test: `Autocompactor.TryCompactAsync` with messages at 85% capacity → compaction occurs, messages list is shorter, first message contains `<context-summary>`.
- [ ] Unit test: messages at 50% capacity → no compaction, messages unchanged.
- [ ] Unit test: `SummaryPrompt` returns expected prompt structure.
- [ ] Unit test: `SessionStore.ReadAllAsync` with a compact-boundary in the JSONL → only post-boundary messages returned.
- [ ] Unit test: `SessionStore.ReadAllAsync` with multiple boundaries → uses the last one.
- [ ] Unit test: `SessionStore.ReadAllAsync` with no boundary → returns all messages (backwards compatible).
- [ ] Integration test: UrSession pre-turn compaction triggers when `LastInputTokens` exceeds threshold. Use a fake chat client that returns a canned summary.
- [ ] Define a new `AgentLoopEvent` subtype: `Compacted` with a summary message string. Yield from `UrSession.RunTurnAsync` after successful compaction so the UI can render a system message.
- [ ] Unit test: UrSession yields `Compacted` event when autocompact fires.

## Impact assessment

- **Code paths affected**: `AgentLoop.BuildLlmMessages` (Stage 1 projection), `UrSession.RunTurnAsync` pre-turn path (Stage 2), `SessionStore` read/write (compact boundary).
- **Data impact**: JSONL session files gain compact-boundary sentinel lines. Old sessions without boundaries load normally (backwards compatible). Post-compaction, pre-boundary messages remain on disk but are ignored on reload.
- **Dependency / API impact**: `Autocompactor` depends on `IChatClient` (already available in UrSession). No new external dependencies. `UrSession` constructor gains a `contextWindow` parameter — existing callers (`UrHost`) already have this data.

## Validation

- **Tests**: Unit tests for each stage as listed above. Integration test for the full pipeline: fake client with token tracking → verify compaction triggers at threshold, tool results cleared in projection.
- **Manual verification**: Start a long session (many tool calls), observe context fill in the status bar. Verify compaction triggers before reaching ~65%. Verify the "Context compacted" system message appears. Resume a compacted session — verify the summary loads correctly and the conversation continues coherently.
- **Edge cases to verify**: Session with zero `LastInputTokens` (new session, no prior turn). Model switch mid-session (context window changes). Session resume after compaction. Multiple compactions in a single session.

