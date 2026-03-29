# Agent Loop

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Drives the core conversation cycle: receive user input, send to LLM, process responses, execute tool calls, run the middleware pipeline, and repeat. The agent loop is the heartbeat of Ur — everything else exists to support it.

### Non-Goals

- Does not manage UI rendering — the loop emits events; the UI layer renders them.
- Does not load or manage extensions — it consumes the middleware pipeline and tool registry that the [Extension System](extension-system.md) populates.
- Does not enforce permissions directly — tool execution goes through the [Permission System](permission-system.md).

## Context

### Dependencies

| Dependency          | What it provides                              | Interface                       |
| ------------------- | --------------------------------------------- | ------------------------------- |
| `IChatClient`       | LLM interaction (streaming responses)         | `GetStreamingResponseAsync`     |
| Tool Registry       | Available tool definitions and handlers       | Tool lookup + invocation        |
| Extension System    | Middleware pipeline                           | Ordered middleware chain         |
| Permission System   | Approval for sensitive tool operations        | Permission check callback       |

The agent loop does **not** depend on Session Storage or Configuration directly. The caller provides an `IChatClient` and a `List<ChatMessage>` — the loop doesn't know where they came from or where they'll be persisted.

### Dependents

| Dependent  | What it needs                                        | Interface              |
| ---------- | ---------------------------------------------------- | ---------------------- |
| UI Layer   | Streaming response chunks, tool call status, prompts | Event/callback model   |

## Interface

### Run Turn

- **Purpose:** Process one user message through the full agent cycle (possibly multiple LLM round-trips if tool calls are involved).
- **Inputs:** User message, mutable `List<ChatMessage>` (the in-memory conversation history).
- **Outputs:** `IAsyncEnumerable<AgentLoopEvent>`: response chunks, tool call starts/completions, permission requests, turn completion.
- **Errors:** LLM API error (retryable or fatal). Tool execution error (reported to LLM as tool result). Middleware error (skip failing middleware, log, continue).
- **Preconditions:** At least one provider is enabled with a valid API key.
- **Postconditions:** All messages (assistant responses, tool calls, tool results) are appended to the in-memory message list. The caller is responsible for persisting messages to the session store — this keeps the loop independent of storage concerns and lets the caller control when and how persistence happens.

### Compact Session

- **Purpose:** Summarize older messages to stay within model context limits.
- **Inputs:** Current session, model context length (from provider registry).
- **Outputs:** Compacted session (older messages replaced with a summary).
- **Preconditions:** Session token count exceeds a threshold relative to model context length.

## Internal Design

The loop operates on a mutable `List<ChatMessage>` owned by the caller. All messages are M.E.AI `ChatMessage` instances — the loop does not define its own message types. This means the message list can be sent directly to `IChatClient` and serialized directly to JSONL by the [Session Storage](session-storage.md) layer.

A single turn works as follows:

1. Caller appends the user's `ChatMessage` to the message list before calling `RunTurnAsync`.
2. Middleware pipeline runs (pre-LLM phase). Middleware can modify the messages, inject system prompts, add/remove tools, or short-circuit.
3. Messages + tool definitions are sent to the LLM via `IChatClient.GetStreamingResponseAsync`.
4. Response streams back. `ResponseChunk` events are emitted for each text chunk. The complete assistant `ChatMessage` (including all `FunctionCallContent` items and their `AdditionalProperties`) is accumulated and appended to the message list.
5. If the response contains tool calls:
   a. For each tool call, look up the handler in the tool registry.
   b. Check permissions (via the permission system).
   c. Execute the tool. If it fails, the error becomes the tool result.
   d. Build a tool `ChatMessage` with `FunctionResultContent` items, append to message list.
   e. Middleware pipeline runs (post-tool phase).
   f. Go to step 3 (send updated messages back to LLM).
6. If the response is a final text message (no tool calls), yield `TurnCompleted` and exit.

The middleware pipeline is modeled after ASP.NET Core: an ordered chain of functions, each receiving a context and a `next` delegate. A middleware can modify the context before calling `next`, modify the result after `next` returns, or skip `next` entirely to short-circuit.

## Error Handling and Failure Modes

| Failure Mode              | Detection              | Recovery                          | Impact on Dependents    |
| ------------------------- | ---------------------- | --------------------------------- | ----------------------- |
| LLM API error (transient) | HTTP error / timeout   | Retry with backoff (limited)      | Delayed response        |
| LLM API error (fatal)     | Auth error, bad model  | Abort turn, report to user        | Turn fails              |
| Tool execution error      | Exception from handler | Return error as tool result to LLM| LLM sees error, adapts  |
| Middleware error           | Exception in pipeline  | Skip failing middleware, log, continue | Degraded but functional |
| Permission denied          | User says no           | Return denial as tool result to LLM | LLM sees denial, adapts |

## Design Decisions

### Report tool errors to LLM (no automatic retry)

- **Context:** LLMs sometimes request invalid tool calls or tools can fail.
- **Options considered:** Automatic retry, abort turn, report error to LLM.
- **Choice:** Report the error back as a tool result.
- **Rationale:** LLMs are good at recovering from errors when given the error message. Automatic retry risks infinite loops. Aborting wastes the conversation context.
- **Consequences:** The LLM sees error messages and can adjust. Extension authors should return clear, actionable error messages.

### Event-based UI contract

- **Context:** The agent loop needs to communicate streaming responses, tool status, and permission prompts to arbitrary UI layers. See [UI Contract](ui-contract.md) for the full crosscutting concern.
- **Options considered:** Direct callbacks, event stream, reactive observables.
- **Choice:** `IAsyncEnumerable<AgentLoopEvent>` — the loop yields events, the UI layer consumes the stream. Implemented in [`Ur/AgentLoop/AgentLoopEvent.cs`](../Ur/AgentLoop/AgentLoopEvent.cs).
- **Implemented events:** `ResponseChunk(Text)`, `ToolCallStarted(CallId, ToolName)`, `ToolCallCompleted(CallId, ToolName, Result, IsError)`, `TurnCompleted`, `Error(Message, IsFatal)`.
- **Rationale:** Decouples the loop from any specific UI. Works for CLI (print to stdout), GUI (update widgets), and IDE (send to extension host). The library defines event types; each UI implements rendering.
- **Consequences:** The event type hierarchy is part of the library's public API. Adding new event types is non-breaking (consumers can ignore unknown subtypes); removing or changing existing ones is breaking.

## Open Questions

- **Question:** What is the middleware API signature in Lua?
  **Context:** This is the core extensibility hook. Needs to be simple for extension authors but powerful enough for real use cases.
  **Current thinking:** `ur.middleware.add(function(context, next) ... end)` where `context` exposes messages, tools, metadata. `next()` calls the next middleware (or the LLM). Middleware can modify context before/after `next`, or skip `next` to short-circuit.

- **Question:** How does session compaction work?
  **Context:** Long conversations exceed model context limits. Need to summarize older messages.
  **Current thinking:** When token count exceeds a threshold (configurable, relative to model's max context), summarize the oldest N messages into a single summary message. The summary itself is generated by the LLM. This is a well-known pattern but the details (what to summarize, how to preserve important context) need design.

- **Question:** Should the loop emit permission events (`PermissionRequested`, `PermissionResolved`)?
  **Context:** The current `AgentLoopEvent` hierarchy ([`Ur/AgentLoop/AgentLoopEvent.cs`](../Ur/AgentLoop/AgentLoopEvent.cs)) defines: `ResponseChunk`, `ToolCallStarted`, `ToolCallCompleted`, `TurnCompleted`, `Error`. The original speculation included `PermissionRequested` and `PermissionResolved`, but the permission system currently uses its own callback contract (`PermissionRequest`/`PermissionResponse`). Should permission interactions also surface as loop events so the UI has a single event stream, or is the separate callback sufficient?
  **Current thinking:** The separate callback works for the permission decision itself. But the UI may want to know *that* a permission prompt happened (for logging, status display). A `PermissionRequested`/`PermissionResolved` event pair as informational notifications (not decision points) could serve that need without duplicating the callback contract.

- **Question:** Should the middleware pipeline have named phases (pre-LLM, post-tool, etc.) or is it a single pipeline?
  **Context:** ASP.NET Core uses a single pipeline. Some systems have distinct pre/post hooks.
  **Current thinking:** Single pipeline is simpler. Middleware that only cares about pre-LLM can just not inspect the response. But if real use cases demand phase-specific hooks, we can add them.
