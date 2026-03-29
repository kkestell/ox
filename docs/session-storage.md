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

Each line in the session file is a JSON-serialized `ChatMessage`. The serialization uses M.E.AI's built-in polymorphic `$type` discriminator for `AIContent` subtypes (`text`, `functionCall`, `functionResult`, etc.).

- **Why `ChatMessage` directly:** Different LLM providers include provider-specific metadata in the M.E.AI content model. For example, Gemini 2.5 attaches a `ThoughtSignature` to `FunctionCallContent.AdditionalProperties` that must be echoed back on the next request or the API returns an error. By storing the `ChatMessage` as-is — including `AdditionalProperties` at both the message and content level — we preserve this metadata without needing to know what each provider requires. The M.E.AI provider libraries handle the translation on the way out; we just need to not lose data on the way in.
- **Invariants:** Every line deserializes to a valid `ChatMessage`. Lines are append-only; partial writes (crash mid-append) are handled by skipping malformed trailing lines on read.
- **Human readability:** The `$type` discriminator and flat JSON structure remain inspectable with `cat` and `jq`, consistent with ADR-0003's rationale.

#### Example: tool-calling round-trip (3 lines)

```jsonl
{"Role":"user","Contents":[{"$type":"text","Text":"What's the weather in Tokyo?"}]}
{"Role":"assistant","Contents":[{"$type":"text","Text":"I'll check the weather."},{"$type":"functionCall","Name":"get_weather","CallId":"call_abc123","Arguments":{"city":"Tokyo"}}],"AdditionalProperties":null}
{"Role":"tool","Contents":[{"$type":"functionResult","CallId":"call_abc123","Result":"Sunny, 22°C"}]}
```

#### Example: Gemini with ThoughtSignature (preserved in AdditionalProperties)

```jsonl
{"Role":"assistant","Contents":[{"$type":"functionCall","Name":"get_weather","CallId":"get_weather","Arguments":{"city":"Tokyo"},"AdditionalProperties":{"ThoughtSignature":"CuwBAb4+9vs..."}}]}
```

## Internal Design

The `SessionStore` is a thin layer over the filesystem. No caching, no indexing, no in-memory state beyond what the caller passes in.

**Serialization:** Use `System.Text.Json` with `JsonSerializerOptions` configured for M.E.AI's polymorphic content types. M.E.AI provides `AIJsonUtilities.CreateDefaultOptions()` which registers the `$type` discriminators for `AIContent` subtypes. This is critical — without it, `FunctionCallContent` serializes as a base `AIContent` and loses its fields.

**Append:** Serialize `ChatMessage` → JSON string → append line to file. One message per line.

**Read:** Read all lines → skip empty/malformed → deserialize each to `ChatMessage`. Malformed trailing lines (from a crash mid-write) are silently skipped rather than failing the entire read.

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
