# UI Contract

> Crosscutting concern of: [Ur Architecture](index.md)

## Overview

Ur is a class library; every UI (CLI, GUI, IDE extension) is a separate consumer. The library communicates with UIs through two mechanisms:

1. **Event streams** — `IAsyncEnumerable<AgentLoopEvent>` yielded by the agent loop. The UI consumes events and renders them however it wants. This is the primary channel for streaming responses and tool status.
2. **Callbacks** — Synchronous decision points where the library needs a user answer before proceeding. Permission prompts are the main example.

Both mechanisms follow the same principle: the library defines the data contract (event types, request/response records); the UI implements the rendering and interaction. The library never assumes terminal I/O, widget toolkits, or any specific presentation layer.

First-run readiness flows such as entering an API key or selecting a model are not callbacks. They are explicit UI flows built on top of `UrConfiguration`.

## Event Stream

The agent loop's `RunTurnAsync` returns `IAsyncEnumerable<AgentLoopEvent>`. The current event hierarchy (defined in [`Ur/AgentLoop/AgentLoopEvent.cs`](../Ur/AgentLoop/AgentLoopEvent.cs)):

| Event | Data | Purpose |
|---|---|---|
| `ResponseChunk` | `Text` | Streaming text fragment from the LLM |
| `ToolCallStarted` | `CallId`, `ToolName` | Tool execution beginning |
| `ToolCallCompleted` | `CallId`, `ToolName`, `Result`, `IsError` | Tool execution finished |
| `TurnCompleted` | — | Turn is done, no more tool calls |
| `Error` | `Message`, `IsFatal` | Something went wrong |

The event set is part of the library's public API. Adding new event subtypes is non-breaking (consumers ignore unknown types via the abstract base class). Removing or changing existing events is breaking.

## Callbacks

### Permission Prompts

When a gated operation needs user approval, the [Permission System](permission-system.md) invokes a callback:

- **Request:** `PermissionRequest(OperationType, Target, RequestingExtension, AllowedScopes)` — defined in [`Ur/Permissions/PermissionRequest.cs`](../Ur/Permissions/PermissionRequest.cs).
- **Response:** `PermissionResponse(Granted, Scope)` — defined in [`Ur/Permissions/PermissionResponse.cs`](../Ur/Permissions/PermissionResponse.cs).

The UI renders the prompt (terminal dialog, modal window, etc.), collects the user's choice, and returns the response. The callback is synchronous from the library's perspective — the agent loop blocks until the UI returns an answer.

The callback contract is defined ahead of full runtime use. The current host/session implementation focuses first on readiness, configuration, and turn orchestration; permission prompting will be wired when gated tool execution actually needs approval.

### Provider Management

Enabling a provider requires entering an API key, which is inherently interactive. The [Provider Registry](provider-registry.md) defines a management interface that UIs implement: list providers, enable/disable, enter/delete API keys. This is a UI flow, not a setting — see the design decision in provider-registry.md.

## Responsibilities by Layer

| Responsibility | Library | UI |
|---|---|---|
| Define event types | Yes | — |
| Yield events | Yes (agent loop) | — |
| Render events | — | Yes |
| Define permission request/response | Yes | — |
| Present permission prompt | — | Yes |
| Collect user input | — | Yes |
| Provider management flow | Define interface | Implement UI |

## Open Questions

- **Question:** Should permission interactions also surface as events in the `AgentLoopEvent` stream?
  **Context:** Currently permissions use a separate callback. The UI gets the decision point via callback, but doesn't get a notification in the event stream that a permission prompt happened. For logging, status bars, or activity feeds, the UI might want both.
  **Current thinking:** See [Agent Loop](agent-loop.md) open question on permission events.

- **Question:** What other callbacks will the library need beyond permissions and provider management?
  **Context:** Possible candidates: user confirmation for destructive operations, input prompts from extensions, authentication flows. Each new callback is a contract that every UI must implement.
  **Current thinking:** Keep the callback surface minimal. Permissions and provider management are the known requirements. Add others only when concrete use cases demand them.
