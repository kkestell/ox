# ADR-0010: Model change starts a new session

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur uses one LLM model per agent loop turn. Users may want to switch models during a conversation (e.g. start with a fast model for exploration, switch to a stronger model for a complex task). The question is whether this switch can happen within a session or requires starting a new one.

Sessions are stored as JSONL files containing raw `ChatMessage` objects, which preserve provider-specific metadata via `AdditionalProperties` (see [ADR-0007](adr-0007-chatmessage-session-format.md)). Different providers attach different metadata — Gemini requires `ThoughtSignature` to be echoed back, for example. This metadata is opaque to Ur; the session layer stores it without interpretation.

See: [Provider Registry](../provider-registry.md), [Session Storage](../session-storage.md).

## Decision Drivers

- **Data integrity.** Provider-specific metadata in stored messages is incompatible across providers. Sending Gemini metadata to Claude (or vice versa) causes errors or silent data loss.
- **Simplicity.** One model per session means the agent loop, session storage, and compaction logic never need to reason about mixed-provider message histories.
- **User expectation.** Users switching models likely want a fresh start anyway — a model picking up another model's reasoning mid-stream is unreliable.

## Considered Options

### Option 1: Allow mid-session model switch

**Description:** The user changes models and the conversation continues in the same session. The agent loop starts using the new model's `IChatClient` for subsequent turns.

**Pros:**

- No friction — the user just switches.
- Preserves conversation context (the user doesn't lose history).

**Cons:**

- Provider-specific `AdditionalProperties` from earlier messages may cause API errors with the new provider.
- Would require either stripping metadata (lossy — can't resume the old model) or translating it (complex, provider-specific, fragile).
- Compaction would need to handle mixed-provider message formats.

**When this is the right choice:** If sessions used a normalized message format that stripped provider metadata. But ADR-0007 chose raw `ChatMessage` specifically to preserve that metadata.

### Option 2: Model change starts a new session

**Description:** Switching models ends the current session and begins a new one. The UI communicates this clearly: "Changing models will start a new session."

**Pros:**

- No mixed-provider metadata. Each session is internally consistent.
- Simple — no translation, no stripping, no mixed-format handling.
- Aligns with ADR-0007's design (raw `ChatMessage` storage depends on single-provider consistency).

**Cons:**

- User loses in-session conversation context when switching.
- Slightly more friction for users who switch models frequently.

**When this is the right choice:** When session integrity and simplicity outweigh the convenience of mid-session switching.

## Decision

We chose **Option 2** because session storage (ADR-0007) preserves raw provider metadata that is incompatible across providers. Mixing providers in one session would require metadata translation — complex, fragile, and contrary to the simplicity constraint.

## Consequences

### Positive

- Session storage remains simple: one provider per session, no mixed metadata.
- Agent loop never needs to handle provider transitions.
- Compaction operates on a homogeneous message history.

### Negative

- Users who want to switch models lose their conversation context. Mitigated by the UI making this explicit ("changing models will start a new session").

### Neutral

- Multi-model conversations (e.g. orchestrating models within a turn) are a separate concern for the future and are not precluded by this decision.

## Confirmation

- The UI prompts when the user attempts to change models mid-session.
- No code path exists to change the `IChatClient` within a session's lifetime.
