# ADR-0010: Mid-Session Model Switching

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur uses one LLM model per agent loop turn. Users may want to switch models during a conversation (e.g. start with a fast model for exploration, switch to a stronger model for a complex task). The question is whether this switch can happen within a session or requires starting a new one.

Sessions are stored as JSONL files containing raw `ChatMessage` objects, which preserve provider-specific metadata via `AdditionalProperties` (see [ADR-0007](adr-0007-chatmessage-session-format.md)). The original concern was that different providers attach incompatible metadata that would break cross-provider message feeding.

**Empirical testing disproved this for most providers.** Cross-provider message feeding was tested between OpenAI (gpt-5-nano), Google/Gemini (gemini-3-flash-preview), and OpenRouter (qwen3.5-flash). Results:

- **OpenAI ↔ OpenRouter:** Messages fully interchangeable in both directions, as-is, no stripping needed.
- **Any → Google:** Fails due to a bug in the `GenerativeAIChatClient` adapter, not message format incompatibility.
- **Google → Any:** Works (Google's responses were plain text in testing).
- **AdditionalProperties:** None of the tested providers populated `AdditionalProperties`. The `createdAt`, `messageId`, and `informationalOnly` fields on messages are first-class M.E.AI properties, not provider extensions.

The Gemini `ThoughtSignature` case was not observed — the Google adapter did not produce tool calls. This remains a theoretical concern.

See: [Provider Registry](../provider-registry.md), [Session Storage](../session-storage.md).

## Decision Drivers

- **Empirical evidence.** Cross-provider message feeding works for the OpenAI-compatible ecosystem (most providers).
- **User experience.** Forcing a new session on model change loses conversation context. Switching between cheap/fast and expensive/capable models within a task is a common workflow.
- **Full fidelity on disk.** The session file preserves everything. Any lossy transformation for cross-provider compatibility happens at runtime, in memory only.

## Considered Options

### Option 1: Model change starts a new session

**Description:** Switching models ends the current session and begins a new one.

**Pros:**

- Zero risk of cross-provider message incompatibility.
- Simple — no translation layer, no per-message provenance tracking.

**Cons:**

- Forces users to lose conversation context when switching models.
- Overly conservative given empirical evidence.

**When this is the right choice:** When provider-specific message metadata is genuinely incompatible and lossy transformation is unacceptable.

### Option 2: Allow mid-session model switching with runtime translation

**Description:** The model can change mid-session. The session JSONL stores full-fidelity messages wrapped in a per-message envelope that captures provenance (provider, model, settings). When sending a conversation to a model on a different provider, a runtime translation layer strips incompatible metadata from the in-memory messages before sending. The on-disk session is never modified.

**Pros:**

- Users keep conversation context when switching models.
- On-disk session remains full-fidelity — no data loss.
- For OpenAI-compatible providers, no translation is needed at all.
- Per-message provenance enables future features (cost tracking, audit, replay).

**Cons:**

- Session JSONL format changes from bare `ChatMessage` to an envelope. Breaking change.
- Runtime translation layer needed for cross-provider switching (even if it's a no-op today for OpenAI-compatible providers).
- Quality degradation possible when switching providers (e.g., losing reasoning context). Must be communicated to the user.
- Google adapter is currently broken for receiving cross-provider messages. Adapter bug, not a format problem.

**When this is the right choice:** When empirical evidence shows messages are interchangeable across target providers, and user experience cost of forced new sessions outweighs implementation cost.

## Decision

We chose **Option 2** because empirical testing showed messages are interchangeable across OpenAI-compatible providers with zero transformation. The user experience cost of losing conversation context on every model switch is significant. The translation layer handles edge cases at runtime without compromising on-disk fidelity.

### Session envelope format

Each JSONL line becomes a per-message envelope:

```jsonl
{"provider":"openai","model":"gpt-5-nano","settings":{"temperature":0.7},"message":{"role":"assistant","contents":[...]}}
```

User and tool messages use the envelope too, recording the model that was active when they were produced. Every line is self-describing — no stateful parsing needed.

## Consequences

### Positive

- Users can switch models mid-conversation without losing context.
- Session files capture complete provenance per message.
- No data loss: on-disk format stores everything, runtime stripping only affects what's sent to the API.

### Negative

- Breaking change to JSONL session format. Existing sessions won't have provenance. Migration: treat provenance-less messages as belonging to an unknown model.
- Quality degradation on cross-provider switches is real but hard to quantify. UI warns "Switching providers may affect response quality."
- Google cross-provider feeding broken (adapter bug). Known limitation.

### Neutral

- The runtime translation layer starts as a simple `AdditionalProperties` stripper. It can grow to handle provider-specific transformations as needed.
- Multi-model orchestration within a single turn is a separate future concern not addressed here.

## Confirmation

- Integration tests demonstrate cross-provider message feeding (`Ur.IntegrationTests/ProviderCompatTests.cs`).
- Session files contain per-message provenance that survives round-trip serialization.
- UI communicates model switches and quality implications.
