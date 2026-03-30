# Session Storage

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Persists conversation history as append-only JSONL files, one per session, scoped to a workspace. Manages session lifecycle (create, resume, list, read). The session format must preserve the full fidelity of LLM messages — including provider-specific metadata — so that conversations can be resumed without errors.

### Non-Goals

- Does not interpret or transform message content. The session layer stores and retrieves; the [Agent Loop](agent-loop.md) owns the conversation semantics.
- Does not handle session compaction (summarizing old messages to fit context limits). That is the agent loop's responsibility; it reads, compacts, and writes back.
- Does not provide cross-session search or indexing. Searching requires reading all files. If this becomes a real need, it belongs in a separate component.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Workspace | Sessions directory path (`$WORKSPACE/.ur/sessions/`) | `Workspace.SessionsDirectory` |
| Microsoft.Extensions.AI | `ChatMessage` type and its polymorphic content model | M.E.AI abstractions |

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| Agent Loop | Append messages during a turn; read full history to send to LLM | `AppendAsync`, `ReadAllAsync` |
| UI Layer | List sessions, display history | `List`, `Get`, `ReadAllAsync` |

## Interface

### Create

- **Purpose:** Start a new session in the workspace.
- **Inputs:** None (ID is generated from timestamp).
- **Outputs:** `Session` (id, file path, creation time).
- **Postconditions:** A JSONL file exists at `$WORKSPACE/.ur/sessions/{id}.jsonl`.

### List

- **Purpose:** Enumerate all sessions in the workspace.
- **Outputs:** Ordered list of `Session` objects.

### Get

- **Purpose:** Retrieve a session by ID.
- **Outputs:** `Session` or null if not found.

### AppendAsync

- **Purpose:** Append a `ChatMessage` to a session's JSONL file.
- **Inputs:** `Session`, `ChatMessage`.
- **Postconditions:** One JSON line appended to the file representing the full `ChatMessage`.

### ReadAllAsync

- **Purpose:** Read the complete message history from a session.
- **Outputs:** `IReadOnlyList<ChatMessage>` in append order.

## Data Structures

### Session

- **Purpose:** Identifies a conversation. Lightweight handle — no messages in memory.
- **Shape:** `{ Id: string, FilePath: string, CreatedAt: DateTimeOffset }`
- **Invariants:** ID is unique within a workspace. FilePath points to a `.jsonl` file in the workspace's sessions directory.

### JSONL Line Format

Each line in the session file is a **per-message envelope** wrapping a JSON-serialized `ChatMessage` with provenance metadata. The `ChatMessage` serialization uses M.E.AI's built-in polymorphic `$type` discriminator for `AIContent` subtypes (`text`, `functionCall`, `functionResult`, etc.).

- **Why an envelope:** Mid-session model switching is supported (see [ADR-0010](decisions/adr-0010-mid-session-model-switch.md)). Each message records which provider, model, and settings were active when it was produced. This enables the runtime translation layer to know what stripping is needed when sending history to a different provider, and supports future features like cost tracking and replay.
- **Why `ChatMessage` directly inside the envelope:** Provider-specific metadata in `AdditionalProperties` is preserved without the session layer needing to know about providers. The on-disk format is full-fidelity; any lossy transformation for cross-provider compatibility happens at runtime, in memory only.
- **Invariants:** Every line deserializes to a valid envelope containing a valid `ChatMessage`. Lines are append-only; partial writes (crash mid-append) are handled by skipping malformed trailing lines on read.
- **Human readability:** The envelope adds a thin wrapper but the `ChatMessage` inside remains inspectable with `cat` and `jq`, consistent with ADR-0003's rationale.
- **Migration:** Session files without envelope metadata (from before this format change) are treated as having unknown provenance. The session reader detects bare `ChatMessage` lines (no `provider` key at the root) and wraps them on read.

#### Example: tool-calling round-trip with provenance (3 lines)

```jsonl
{"provider":"openai","model":"gpt-5-nano","settings":{},"message":{"role":"user","contents":[{"$type":"text","text":"What's the weather in Tokyo?"}]}}
{"provider":"openai","model":"gpt-5-nano","settings":{},"message":{"role":"assistant","contents":[{"$type":"text","text":"I'll check the weather."},{"$type":"functionCall","name":"get_weather","callId":"call_abc123","arguments":{"city":"Tokyo"}}]}}
{"provider":"openai","model":"gpt-5-nano","settings":{},"message":{"role":"tool","contents":[{"$type":"functionResult","callId":"call_abc123","result":"Sunny, 22°C"}]}}
```

#### Example: mid-session model switch

```jsonl
{"provider":"openai","model":"gpt-5-nano","settings":{},"message":{"role":"user","contents":[{"$type":"text","text":"Summarize this file."}]}}
{"provider":"openai","model":"gpt-5-nano","settings":{},"message":{"role":"assistant","contents":[{"$type":"text","text":"Here's a summary..."}]}}
{"provider":"openrouter","model":"anthropic/claude-sonnet-4","settings":{"temperature":0.7},"message":{"role":"user","contents":[{"$type":"text","text":"Now refactor it."}]}}
{"provider":"openrouter","model":"anthropic/claude-sonnet-4","settings":{"temperature":0.7},"message":{"role":"assistant","contents":[{"$type":"text","text":"Here's the refactored version..."}]}}
```

## Internal Design

The `SessionStore` is a thin layer over the filesystem. No caching, no indexing, no in-memory state beyond what the caller passes in.

**Serialization:** Use `System.Text.Json` with `JsonSerializerOptions` configured for M.E.AI's polymorphic content types. M.E.AI provides `AIJsonUtilities.CreateDefaultOptions()` which registers the `$type` discriminators for `AIContent` subtypes. This is critical — without it, `FunctionCallContent` serializes as a base `AIContent` and loses its fields.

**Append:** Build envelope (`{ provider, model, settings, message }`) → serialize to JSON string → append line to file. One envelope per line.

**Read:** Read all lines → skip empty/malformed → deserialize each. If a line has a `provider` key at the root, it's an envelope — extract the `ChatMessage` from `message`. If it's a bare `ChatMessage` (legacy format), wrap it with unknown provenance. Malformed trailing lines (from a crash mid-write) are silently skipped.

## Error Handling and Failure Modes

| Failure Mode | Detection | Recovery | Impact on Dependents |
|---|---|---|---|
| Corrupt trailing line (crash during write) | JSON parse failure on last line | Skip the malformed line, return all valid messages | Agent loop sees slightly truncated history — acceptable |
| Missing session file | `File.Exists` check | Return null / empty list | Caller handles "session not found" |
| Disk full | IOException on append | Propagate to caller | Agent loop surfaces error to user |

## Design Decisions

### Store `ChatMessage` directly, not a custom `SessionMessage`

See [ADR-0007](decisions/adr-0007-chatmessage-session-format.md) for full analysis.

- **Choice:** Store `ChatMessage` via M.E.AI's own serialization using `AIJsonUtilities.CreateDefaultOptions()` for polymorphic `$type` support.
- **Rationale:** Empirically verified that M.E.AI's `ChatMessage` preserves provider-specific metadata through `AdditionalProperties`. Gemini's `ThoughtSignature` survives the serialize → deserialize → send round-trip. The session layer doesn't need to know anything about providers.
- **Consequences:** Session files are coupled to M.E.AI's serialization format. Acceptable — sessions are ephemeral conversations, not long-term archives.

## Open Questions

- **Question:** Should `AppendAsync` serialize with indented JSON for human readability, or compact JSON for smaller files?
  **Current thinking:** Compact. JSONL convention is one object per line; indented JSON breaks that. Use `jq` for pretty-printing when inspecting manually.

- **Question:** AoT compatibility of `AIJsonUtilities.CreateDefaultOptions()`.
  **Context:** This method may use reflection to discover `AIContent` subtypes. Needs verification under AoT publishing. If it doesn't work, we'll need to manually register the polymorphic type hierarchy in the serializer options.
  **Current thinking:** Test early. M.E.AI is designed to work with AoT (.NET 10 target), so this likely works, but must be confirmed.
