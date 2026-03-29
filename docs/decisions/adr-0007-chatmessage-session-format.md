# ADR-0007: Store ChatMessage Directly for Session Persistence

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

The session layer persists conversation history as JSONL. The question is what type to serialize: a custom `SessionMessage` struct, raw provider JSON, or M.E.AI's `ChatMessage` directly. The format must preserve provider-specific metadata (e.g. Gemini's `ThoughtSignature`) needed for correct conversation resumption.

See [Session Storage](../session-storage.md).

## Decision Drivers

- **Provider metadata fidelity.** Some providers attach metadata to messages that must be echoed back on the next request. Losing this data breaks conversation resumption.
- **Simplicity.** One type to serialize and deserialize, no translation layer.
- **AoT compatibility.** Serialization must work under ahead-of-time compilation.
- **The session layer should not know about providers.** Provider-specific handling belongs in M.E.AI, not in Ur's storage layer.

## Considered Options

### Option 1: Evolve a custom `SessionMessage`

**Description:** Define `SessionMessage { Role, Content, ToolCallId?, ToolName? }` and add fields as new M.E.AI content types emerge.

**Pros:**

- Full control over the serialized shape.
- Decoupled from M.E.AI's serialization format.

**Cons:**

- Cannot represent the full M.E.AI vocabulary: multi-part content, `FunctionCallContent`, `TextReasoningContent`, `AdditionalProperties`.
- Grows with every new M.E.AI feature — fragile maintenance burden.
- Loses provider-specific metadata that lives in `AdditionalProperties`.

**When this is the right choice:** When you need long-term format stability independent of the LLM abstraction layer.

### Option 2: Store raw provider JSON

**Description:** Capture the raw HTTP response body from the LLM API and store it verbatim.

**Pros:**

- Preserves everything — no data loss possible.

**Cons:**

- The agent loop would need to parse provider-specific formats to extract text and tool calls. Defeats the purpose of M.E.AI as an abstraction.
- Different providers have different JSON shapes. The session layer becomes provider-aware.
- No common type for the agent loop to operate on.

**When this is the right choice:** When the abstraction layer is unreliable or the storage layer needs to outlive it.

### Option 3: Store `ChatMessage` via M.E.AI's serialization

**Description:** Use `AIJsonUtilities.CreateDefaultOptions()` to serialize `ChatMessage` with polymorphic `$type` discriminators for all `AIContent` subtypes.

**Pros:**

- Round-trips correctly for all tested providers (OpenAI, Claude via OpenRouter, Gemini 2.5 including `ThoughtSignature`).
- The session layer stores and retrieves one type — no translation.
- Provider-specific metadata survives via `AdditionalProperties` on both `ChatMessage` and `AIContent` items.
- Empirically verified: Gemini's `ThoughtSignature` in `FunctionCallContent.AdditionalProperties` survives serialize → deserialize → send.

**Cons:**

- Coupled to M.E.AI's serialization format. If `$type` discriminators change, old sessions may not deserialize.
- Requires `AIJsonUtilities.CreateDefaultOptions()` to be AoT-compatible (expected but needs verification).

**When this is the right choice:** When the LLM abstraction is trusted and sessions are ephemeral conversations, not long-term archives.

## Decision

We chose **Option 3: store `ChatMessage` directly** because it preserves provider-specific metadata without the session layer needing to know about providers, and empirical testing confirmed correct round-tripping across multiple providers. Sessions are ephemeral conversations, not archives, so coupling to M.E.AI's current serialization format is acceptable.

## Consequences

### Positive

- Zero translation between the agent loop's in-memory type and the persisted format.
- Provider metadata (like Gemini's `ThoughtSignature`) preserved automatically.
- Session layer remains provider-agnostic.

### Negative

- M.E.AI serialization format changes could break old session files. Mitigation: sessions are ephemeral; a migration tool can re-serialize if needed.
- AoT compatibility of `AIJsonUtilities.CreateDefaultOptions()` must be verified.

### Neutral

- JSONL format remains human-readable with `cat` and `jq`, consistent with ADR-0003.

## Confirmation

- Conversation resumption works correctly across providers, including those with mandatory echo-back metadata.
- No provider-specific code exists in the session storage layer.
