# Thinking and Interleaved Narration Across All Providers

## Goal

Surface extended thinking/reasoning traces and interleaved narration (model text between tool calls)
from all supported providers in the conversation UI.

## Desired outcome

- When a model produces a thinking/reasoning trace (DeepSeek-R1, Gemini thinking mode, OpenAI reasoning
  models, Ollama with reasoning models), that trace appears in the conversation stream as a visually
  distinct "thinking" block.
- When a model narrates between tool calls in a normal response ("I found the bug — fixing it now"),
  that text appears inline in the conversation before the tool call entry.
- Both features work uniformly across OpenAI-compatible, Google, and Ollama providers.
- The implementation is gated by experiments: each provider is probed first; wrappers are only written
  where the MEAI adapter does not surface `TextReasoningContent` natively.

## How we got here

The user wanted to enable "thinking / agent responses / status updates" across all providers. Research
into the current state of .NET LLM SDKs revealed that MEAI v10.4.1 already ships a `TextReasoningContent`
type specifically for reasoning traces. If provider adapters emit it, the feature lands with minimal code.
If they don't, thin `IChatClient` decorator wrappers per provider fill the gap. An experiment phase gates
the implementation to avoid writing wrappers that turn out to be unnecessary.

## Approaches considered

### Option A: Experiment-first (chosen)

Write integration test probes before implementing wrappers. If `TextReasoningContent` flows through a
provider's MEAI adapter natively, only AgentLoop and UI changes are needed for that provider. If not,
write a thin `IChatClient` decorator that configures thinking and translates provider-specific reasoning
data into `TextReasoningContent`.

- Pros: Zero extra code where adapters already work; experiments are explicitly requested; thinking
  configuration (ThinkingConfig, reasoningEffort) belongs inside the wrapper where the provider lives,
  not in AgentLoop.
- Cons: Scope is uncertain until experiments run; may end up writing wrappers for all providers anyway.
- Failure mode: Experiments show every adapter needs a wrapper and we've spent time on probing first.

### Option B: Wrapper-first (defensive)

Skip experiments — write `IChatClient` decorators for every provider preemptively based on the known
state of MEAI.OpenAI (issue dotnet/extensions#6311, `TextReasoningContent` not yet wired in v10.4.1)
and GeminiDotnet (unknown status for thinking in the MEAI adapter).

- Pros: Known implementation scope from day one.
- Cons: May be redundant if adapters already work; pre-emptive work.
- Failure mode: Wrappers introduce subtle bugs or fight the adapters.

### Option C: Dormant path (wait for upstream)

Add `TextReasoningContent` handling in AgentLoop and a `ThinkingEntry` UI type now; leave them inert until
upstream adapter improvements land. No provider-layer changes.

- Pros: Almost no work today.
- Cons: Thinking won't appear for any provider; doesn't solve the "how to enable thinking mode" problem.

## Recommended approach

Option A. The experiments are small and fast; their outcomes directly determine what code to write. If
they show adapters work, the implementation collapses to three files. If they show wrappers are needed,
the wrappers are the right abstraction layer anyway (provider encapsulates provider-specific configuration).

## Related code

- `src/Ur/AgentLoop/AgentLoop.cs` — content-type switch where `TextReasoningContent` case gets added;
  `BuildLlmMessages()` where reasoning content must be stripped before replay to LLM
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — new `ThinkingChunk` event
- `src/Ox/Conversation/ConversationEntry.cs` — new `ThinkingEntry`
- `src/Ox/OxApp.cs` — `DrainAgentEvents()` switch for `ThinkingChunk`
- `src/Ox/Views/ConversationView.cs` — rendering logic for `ThinkingEntry`
- `src/Ox/Views/ConversationEntryView.cs` — style constants for thinking blocks
- `src/Ur/Providers/OpenAiCompatibleProvider.cs` — may receive a thinking decorator
- `src/Ur/Providers/GoogleProvider.cs` — may receive a thinking decorator + ThinkingConfig injection
- `tests/Ur.IntegrationTests/ProviderCompatTests.cs` — experiment tests live alongside this file

## Current state

- MEAI 10.4.1 ships `TextReasoningContent` (namespace `Microsoft.Extensions.AI`) — a first-class type
  distinct from `TextContent`, carrying `Text` and optional `ProtectedData`.
- `AgentLoop.RunTurnAsync()` handles only `TextContent`, `FunctionCallContent`, and `UsageContent`.
  `TextReasoningContent` would pass through the switch silently today.
- `BuildLlmMessages()` already strips `UsageContent` from messages before sending to the LLM.
  `TextReasoningContent` needs the same treatment — providers do not want to receive prior reasoning
  traces as input (unlike Anthropic extended thinking with multi-turn signatures, which we do not support).
- Interleaved narration (text before tool calls in a single response) already mostly works: text
  arrives as `ResponseChunk` events and accumulates in `AssistantTextEntry`; when `ToolCallStarted` fires
  (after the stream ends), `_currentAssistantEntry` is nulled so subsequent tool calls don't append to
  the same text entry. The experiment should confirm this works end-to-end.
- Enabling thinking requires provider-specific request options:
  - Gemini: `ThinkingConfig { IncludeThoughts = true, ThinkingBudget = N }` — GeminiDotnet-specific
  - OpenAI o-series: `ChatOptions` `AdditionalProperties` or SDK-level `ReasoningOptions`
  - DeepSeek-R1 (via OpenRouter): thinking is on by default, no opt-in needed
  - Ollama: model-dependent; DeepSeek-R1-local and QwQ emit reasoning blocks automatically

## Structural considerations

The four concerns — enabling thinking, surfacing content, event routing, and rendering — are kept in
separate layers:

1. **Provider layer** owns enabling thinking. If a decorator is needed, `IProvider.CreateChatClient()`
   returns the wrapped client. AgentLoop never knows about `ThinkingConfig` or `reasoningEffort`.
2. **AgentLoop** owns content-type routing. The new `TextReasoningContent` case fits naturally into the
   existing switch alongside `TextContent` and `FunctionCallContent`.
3. **AgentLoopEvent** owns the event contract. `ThinkingChunk` mirrors `ResponseChunk` in shape.
4. **OxApp / ConversationEntry / ConversationView** own rendering. `ThinkingEntry` is parallel to
   `AssistantTextEntry` — both accumulate streaming text, but with different visual styles.

`BuildLlmMessages()` is the designated projection layer between storage and the LLM. Stripping
`TextReasoningContent` there (alongside the existing `UsageContent` strip) is architecturally correct.

**ThinkingEntry as a first-class conversation entry (not a sub-part of AssistantTextEntry)** is the
right call: thinking has distinct rendering, may precede text in the same response, and is conceptually
a different kind of content. Merging it into `AssistantTextEntry` would conflate two different concerns.

## Research

### Repo findings

- `ProviderCompatTests.cs` already demonstrates the integration test pattern (probe a real API, gate on
  env var, log content types). New experiment tests follow this pattern exactly.
- `BuildLlmMessages()` already filters `UsageContent` — the stripping pattern for `TextReasoningContent` is identical.
- The content-type switch in `RunTurnAsync` lines 119–133 is the single place to add the new case.

### External research

- MEAI v10.4.0 release notes: "advances AI abstractions with new hosted file, web search, and
  **reasoning content types**" — confirms `TextReasoningContent` is shipping in our current package.
- `dotnet/extensions` issue #6311: "Use TextReasoningContent in OpenAIResponseClient's IChatClient" — WIP
  as of April 2026; `Microsoft.Extensions.AI.OpenAI`'s adapter may not yet emit it.
- Gemini 3.1 supports three thinking levels (LOW, MEDIUM, HIGH); configured via `ThinkingConfig`.
- DeepSeek-R1 via OpenRouter: `reasoning_content` field is on by default; the OpenAI-compat wrapper
  may or may not surface it as `TextReasoningContent` vs a raw `AdditionalProperties` entry.

## Implementation plan

### Phase 1 — Experiments (gates all provider-specific work)

- [ ] Add `ThinkingExperimentTests.cs` to `tests/Ur.IntegrationTests/`, gated by `UR_RUN_THINKING_EXPERIMENTS=1`.
  Write one test per provider that calls a reasoning-capable model and logs every content type in the
  streaming response. Specifically check for `TextReasoningContent` items.
  - OpenAI o3 or o4-mini (via `OPENAI_API_KEY`) — with `reasoningEffort` set
  - DeepSeek-R1 via OpenRouter (via `OPENROUTER_API_KEY`) — no special config needed
  - Gemini 3.x Flash or Pro (via `GOOGLE_API_KEY`) — with `ThinkingConfig.IncludeThoughts = true`
  - Ollama with a reasoning model (DeepSeek-R1-local or QwQ) if available
  - Interleaved narration probe: prompt any provider to narrate then call a tool; verify text arrives
    before `FunctionCallContent` in the stream
- [ ] Run experiments, record results per provider: "native TextReasoningContent ✓", "no thinking output",
  or "thinking in AdditionalProperties only"

### Phase 2 — Provider wrappers (only for providers that need them per Phase 1)

For each provider where `TextReasoningContent` does not flow through natively:

- [ ] Write `ThinkingChatClient : IChatClient` decorator in `src/Ur/Providers/`.
  The decorator wraps an inner `IChatClient` and:
  1. On every streaming call, injects provider-specific thinking options into `ChatOptions`
     (e.g., `ThinkingConfig` for Gemini, `ReasoningOptions` for OpenAI). These options are baked in at
     construction time so AgentLoop never needs to know about them.
  2. Iterates the inner stream and translates provider-specific reasoning data (e.g.,
     `AdditionalProperties["reasoning_content"]`, Gemini thought parts) into `TextReasoningContent` items
     before yielding the update upstream.
- [ ] Wire each decorator into the corresponding `IProvider.CreateChatClient()` return value.
- [ ] Add thinking-level configuration to `providers.json` model entries (e.g., `"thinking": "high"`)
  and read it in the provider to parameterize the decorator (budget, effort level).

### Phase 3 — AgentLoop: route TextReasoningContent

- [ ] In `AgentLoop.RunTurnAsync()`, add a `TextReasoningContent` case to the content-type switch:
  ```csharp
  case TextReasoningContent trc:
      yield return new ThinkingChunk { Text = trc.Text };
      break;
  ```
- [ ] In `BuildLlmMessages()`, extend the `UsageContent` filter to also strip `TextReasoningContent`:
  ```csharp
  msg.Contents.Where(c => c is not UsageContent and not TextReasoningContent)
  ```
  This prevents prior reasoning traces from being replayed to the LLM as input.

### Phase 4 — New event type

- [ ] Add `ThinkingChunk` to `AgentLoopEvent.cs`:
  ```csharp
  public sealed class ThinkingChunk : AgentLoopEvent
  {
      public required string Text { get; init; }
  }
  ```

### Phase 5 — Conversation entries and UI

- [ ] Add `ThinkingEntry` to `ConversationEntry.cs` — mirrors `AssistantTextEntry` with a `StringBuilder`
  and `Append()` method. Kept separate because thinking has distinct rendering and different lifecycle
  semantics from assistant response text.
- [ ] Handle `ThinkingChunk` in `OxApp.DrainAgentEvents()`:
  - If `_currentThinkingEntry` is null, create one and add to conversation view.
  - Append the chunk text to it.
  - `_currentThinkingEntry` must be nulled in all five places `_currentAssistantEntry` is already nulled,
    to prevent a turn-boundary bug where thinking text from the next turn would append to a stale entry:
    - When `ResponseChunk` arrives (text after thinking starts a new turn within the response)
    - When `ToolCallStarted` arrives
    - When `TurnCompleted` arrives (line ~653 alongside `_currentAssistantEntry = null`)
    - When `TurnError` with `IsFatal = true` arrives (line ~695)
    - In `CancelTurn()` (line ~579)
- [ ] Render `ThinkingEntry` in `ConversationView` / `ConversationEntryView` with a visually distinct style:
  - Hollow circle prefix (`○`) instead of the filled `●` used for assistant text
  - Muted/dimmed color to signal this is internal reasoning, not the model's response
  - No markdown rendering (thinking traces are unstructured prose)

### Phase 6 — Validation

- [ ] Run `UR_RUN_THINKING_EXPERIMENTS=1 dotnet test tests/Ur.IntegrationTests` — all experiment tests pass,
  output shows `TextReasoningContent` items for each provider.
- [ ] Run `dotnet test tests/Ur.Tests` — all existing unit tests pass.
- [ ] Run `dotnet build` — no warnings.
- [ ] Manual: start ox with a reasoning-capable model, submit a prompt that requires thinking. Verify
  thinking block appears in the TUI with hollow-circle style before or alongside the response text.
- [ ] Manual: submit a prompt that causes the model to narrate before calling a tool. Verify narration
  text appears as an `AssistantTextEntry` above the tool call entry.

## Impact assessment

- **Code paths affected**: AgentLoop content switch, BuildLlmMessages filter, AgentLoopEvent, OxApp
  DrainAgentEvents, ConversationEntry, ConversationView, 1–3 provider files.
- **Data / schema**: `TextReasoningContent` items will be persisted in JSONL session files via MEAI's
  existing polymorphic serialization — no schema migration needed. They are stripped on replay.
- **Dependency impact**: No new NuGet packages. GeminiDotnet.Extensions.AI 0.23.0 is already referenced;
  if its adapter already emits `TextReasoningContent`, no additional Gemini work is needed.

## Open questions

- Does `GeminiDotnet.Extensions.AI` 0.23.0 require any special `ChatOptions` extension to pass `ThinkingConfig`,
  or does it accept it via `AdditionalProperties`? The experiment will answer this.
- What thinking levels / budgets should be the defaults in `providers.json`? Defer to user after
  experiments validate that thinking works end-to-end.
