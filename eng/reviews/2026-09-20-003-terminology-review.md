# Terminology review

Date: 2026-09-20

## Scope

This review covers all tracked Rust implementation and test code under `src/`.
ACP and OpenRouter field and enum names are treated as external vocabulary and
are not renamed at their protocol boundaries. The ignored `research/` working
directory is not part of the committed implementation and was not changed.

The review began from commit `4f8bdec`, which preserves the working tree before
the terminology changes.

## Outcome

The implementation has one fairly small workflow:

```text
receive an ACP prompt request
save its user message
make a model request
send live answer and reasoning updates while the response streams
validate the complete assistant message
run any tool calls and record their outcomes
save the assistant message and tool results together
return the final ACP response
```

Cancellation or failure enters the same final step: give every unstarted tool
call an explicit outcome, save the complete batch when possible, send any
remaining updates, and return the appropriate response.

The code previously described this workflow with overlapping terms including
acceptance, admission, ownership, pending state, closed batches, settlement,
delivery, history, and model calls. The implementation now names the concrete
data or action instead.

## Changes made

| Previous name or wording                                                 | Current name or wording                                                                                  | Reason                                                                                                    |
| ------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `src/model.rs`                                                           | `src/openrouter.rs`                                                                                      | The module is the concrete OpenRouter adapter, not a provider-neutral model layer.                        |
| `ModelClient`                                                            | `openrouter::Client`                                                                                     | The concrete adapter is identified by its module at use sites.                                            |
| `MODEL`                                                                  | `openrouter::DEFAULT_MODEL`                                                                              | The value applies when creating a session; an existing session keeps its recorded model.                  |
| `ModelRequest`                                                           | `openrouter::CompletionStream`                                                                           | The HTTP request has already completed; the value reads and assembles its streamed response.              |
| `ModelCompletion` / `ModelStop`                                          | `openrouter::Completion` / `openrouter::Stop`                                                            | Module qualification identifies the boundary without repeating `Model` in every type.                     |
| `TranscriptEvent`                                                        | `TranscriptEntry`                                                                                        | An entry is durable conversation data, not a live event.                                                  |
| prompt `history`                                                         | `transcript`                                                                                             | It is the same ordered transcript loaded from the store.                                                  |
| internal `cwd`                                                           | `workspace_path`                                                                                         | It is the path associated with a session, not the process working directory.                              |
| `Prompt`                                                                 | `PromptRun`                                                                                              | The type represents execution state, not prompt text.                                                     |
| `accept`                                                                 | `save_user_message`                                                                                      | The method writes the message and updates the session title.                                              |
| `converse`                                                               | `run_model_loop`                                                                                         | The method repeatedly requests model output and runs tools.                                               |
| `settle`                                                                 | `finish`                                                                                                 | The method completes missing outcomes, saves, updates the client, and responds.                           |
| `Exit`                                                                   | `PromptOutcome`                                                                                          | The enum describes why one prompt run stopped.                                                            |
| model call                                                               | model request                                                                                            | Each count corresponds to one HTTP request to OpenRouter.                                                 |
| `MAX_MODEL_CALLS`                                                        | `MAX_MODEL_REQUESTS`                                                                                     | Matches the counted operation.                                                                            |
| `PendingBatch`                                                           | `UncommittedAssistantBatch`                                                                              | The message is validated but has not been saved with all tool results.                                    |
| `closed()`                                                               | `complete()`                                                                                             | The method requires one outcome per call; it does not close a resource.                                   |
| prompt `pending` field                                                   | `uncommitted_batch`                                                                                      | Avoids confusion with ACP's pending tool status.                                                          |
| model `pending` queue                                                    | `buffered_items`                                                                                         | It contains already parsed stream items waiting to be returned.                                           |
| `ModelEvent`                                                             | `openrouter::StreamItem`                                                                                 | Distinguishes normalized OpenRouter stream output from transcript entries and raw SSE events.             |
| `deliver` / `Delivery`                                                   | `send_update` / `AcpUpdate`                                                                              | The operation sends an ACP update; it does not prove client receipt.                                      |
| `PromptOutcome::Provider`                                                | `PromptOutcome::OpenRouter`                                                                              | The failure now identifies the concrete external boundary.                                                |
| `convert::prompt_text`, `agent_text`, `tool_result`, and similar helpers | `prompt_to_user_message`, `agent_message_chunk`, `finished_tool_call_update`, and other ACP-shaped names | Conversion names now make their direction or ACP result explicit instead of mixing Ox and ACP vocabulary. |
| claim/admission/membership wording                                       | acquire/drop an operation guard                                                                          | Describes the actual mechanism directly.                                                                  |
| internal `reasoning_details`                                             | `continuation_metadata`                                                                                  | The values are opaque data retained for a later model request.                                            |

The terminology changes were followed by a storage simplification. Transcript
types now derive their stored JSON, including bare strings for model and user
entries, `continuation_metadata`, and a nested tool `outcome`. That changes the
private row format, so an existing database from the earlier format must be
deleted and recreated.

## Findings after the cleanup

### Resolved: the same transcript had several names

`StoredSession::transcript`, the prompt runner's `history`, "conversation
records," and `TranscriptEvent` all described the same ordered conversation. The
domain type is now `TranscriptEntry`, and both the store and prompt runner call
the collection a transcript.

The SQLite table is still named `events`. That is an existing storage detail,
not a second domain term. Renaming it would require a schema change and provides
little benefit by itself.

### Resolved: workspace and `cwd` were mixed internally

ACP supplies a field named `cwd`, but Ox stores it as the session's workspace.
Internal state and function arguments now use `workspace_path`. `request.cwd`
remains at the ACP boundary because it is imposed by the protocol schema.

### Resolved: one model request had several names

The implementation used model call, model request, completion request, and ACP's
turn-request terminology for one OpenRouter HTTP request. Internal code now uses
model request. ACP's `MaxTurnRequests` remains only where Ox constructs the
protocol response.

### Resolved: `accepted` described several unrelated boundaries

The previous comments used accepted for a saved user message, a validated model
message, an uncommitted assistant batch, and a committed transcript entry. Those
states are now named directly: saved, validated, uncommitted, and saved in the
transcript.

### Resolved: `pending` described unrelated queues and states

ACP's `Pending` remains the protocol status for an announced tool call that has
not started. The prompt runner now holds an `uncommitted_batch`, and the
completion stream holds `buffered_items`.

### Resolved: session locking used organizational jargon

The session-operation module previously described one mechanism as admission,
claiming, ownership, registry membership, and release. The code now says that an
operation acquires a guard, the guard keeps the session busy, and dropping the
guard makes the session available.

### Resolved: boundary adapters mixed source and destination vocabulary

The OpenRouter implementation lived in `model.rs`, its streamed response was
called `ModelRequest`, and prompt failures used the generic variants `Provider`
and `Update`. The module is now `openrouter`, the response reader is a
`CompletionStream`, and failures name the OpenRouter or ACP-update boundary.

Likewise, the ACP conversion helpers now either state their direction
(`prompt_to_user_message`) or name the ACP value they construct
(`agent_message_chunk`, `agent_thought_chunk`, and the tool-call update
helpers).

### Intentional: external vocabulary maps to Ox vocabulary

External APIs and Ox deliberately use different names at these boundaries:

| OpenRouter or ACP name         | Ox meaning                                                                      |
| ------------------------------ | ------------------------------------------------------------------------------- |
| OpenRouter assistant role      | The model-produced message stored as `AssistantMessage`.                        |
| ACP agent message              | The same model output presented by Ox to the ACP client.                        |
| ACP `AgentThoughtChunk`        | Visible `reasoning` text.                                                       |
| OpenRouter `reasoning_details` | Internal `continuation_metadata`.                                               |
| OpenRouter `finish_reason`     | `openrouter::Stop`, then an internal `PromptOutcome`, then an ACP `StopReason`. |
| ACP `cwd`                      | Internal `workspace_path`.                                                      |
| ACP `Pending`                  | An announced tool call that has not begun execution.                            |

These translations should remain localized to `openrouter.rs`, `acp.rs`, and
`acp/convert.rs` rather than leaking into the conversation and persistence
types. They are required translations, not unresolved naming inconsistencies.

### Intentional: stop reasons use a translation table

The same stopping condition has different provider and protocol names. This is a
required API translation rather than inconsistent internal terminology:

| OpenRouter       | `openrouter::Stop` | `PromptOutcome`     | ACP               |
| ---------------- | ------------------ | ------------------- | ----------------- |
| `stop`           | `Finished`         | `Finished`          | `EndTurn`         |
| `length`         | `TokenLimit`       | `TokenLimit`        | `MaxTokens`       |
| `content_filter` | `Refused`          | `Refused`           | `Refusal`         |
| `tool_calls`     | `ToolCalls`        | Continue running    | No final stop yet |
| —                | —                  | `ModelRequestLimit` | `MaxTurnRequests` |
| —                | —                  | `Cancelled`         | `Cancelled`       |

### Resolved: follow-up terminology drift

A follow-up pass replaced remaining prompt-outcome `exit` locals, operation
guard `claims`, and acceptance and settlement wording in test comments. The
apply-patch design now says that the tool uses the session's workspace path;
`cwd` remains only at the ACP boundary.

## Glossary

| Term                        | Definition                                                                                                                                                                                |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| ACP                         | Agent Client Protocol, the JSON-RPC interface between an editor or other client and Ox.                                                                                                   |
| ACP client                  | The editor or application connected to Ox. It is distinct from the OpenRouter and HTTP clients.                                                                                           |
| Agent                       | Ox as presented through ACP.                                                                                                                                                              |
| ACP update                  | A `session/update` notification that describes session metadata, model output, or tool state. Sending one does not confirm that the ACP client received or displayed it.                  |
| ACP tool status             | The client-facing state of a tool call: pending, in progress, completed, or failed. A cancelled Ox tool outcome is presented as failed because ACP has no separate cancelled tool status. |
| Session                     | A saved conversation and its metadata, identified by a session ID. Its OpenRouter model is fixed when the session is created.                                                             |
| Session summary             | A session's ID, workspace path, optional title, and creation and activity timestamps, without its transcript.                                                                             |
| Stored session              | A session summary paired with its validated transcript.                                                                                                                                   |
| Session store               | The SQLite-backed component that creates, reads, lists, updates, and deletes sessions and their transcripts.                                                                              |
| Workspace path              | The exact absolute path associated with a session. Ox does not normalize or resolve aliases.                                                                                              |
| Session operation           | One prompt, load, or delete running for a session. At most one can run for the same session at a time.                                                                                    |
| Operation guard             | A value that keeps one session busy for a session operation. Dropping it makes the session available.                                                                                     |
| Prompt request              | One ACP request containing user content for a session.                                                                                                                                    |
| Prompt run                  | The work caused by one prompt request: save the user message, request model output, run tools, save results, and respond.                                                                 |
| Prompt outcome              | The internal reason a prompt run stopped. It determines how unfinished tool calls are completed and whether Ox returns an ACP stop reason or an error.                                    |
| Prompt cancellation         | A per-prompt signal that remains cancelled once triggered. It stops new work but does not roll back model or tool effects already observed.                                               |
| User message                | Text produced from the supported ACP content blocks and saved before the first model request.                                                                                             |
| Transcript                  | The ordered, saved conversation used for both session replay and future model requests.                                                                                                   |
| Transcript entry            | A model entry, user message, assistant message, or tool result in the transcript.                                                                                                         |
| Model entry                 | The first transcript entry. It stores the OpenRouter model used for every model request in that session.                                                                                  |
| OpenRouter client           | The concrete client that verifies the API key and sends model requests to OpenRouter's chat-completions endpoint.                                                                         |
| Model request               | One OpenRouter chat-completion HTTP request. A prompt run may make several.                                                                                                               |
| Completion stream           | The reader for one streamed OpenRouter response after its HTTP request has succeeded. It yields output deltas followed by one completion.                                                 |
| OpenRouter stream item      | An answer-text delta, a reasoning delta, or the one completion yielded by a completion stream.                                                                                            |
| Assistant message           | The validated model output assembled from a completion stream: answer text, visible reasoning, tool calls, and continuation metadata.                                                     |
| Completion                  | An assistant message paired with the normalized reason OpenRouter stopped generating.                                                                                                     |
| OpenRouter stop             | The normalized OpenRouter stopping reason attached to a completion: finished, tool calls, token limit, or refusal.                                                                        |
| Answer text                 | The model's user-facing answer. Deltas may be sent live; the assembled text is saved only as part of a complete assistant batch.                                                          |
| Visible reasoning           | Reasoning text presented to the ACP client. It is distinct from opaque continuation metadata.                                                                                             |
| Continuation metadata       | Opaque data stored internally as `continuation_metadata` and encoded as OpenRouter `reasoning_details` on a later model request. It is not displayed as reasoning.                        |
| Tool call                   | A model-produced call ID, tool name, and raw argument string.                                                                                                                             |
| Tool outcome                | What Ox knows happened: completed, failed, or cancelled, with explanatory text.                                                                                                           |
| Tool result                 | A tool call's ID and name paired with its outcome.                                                                                                                                        |
| Assistant batch             | One assistant message plus exactly one final tool result for each call in the message. The store saves it in one transaction.                                                             |
| Uncommitted assistant batch | A validated assistant message whose tool outcomes are incomplete or have not yet been saved.                                                                                              |
| Shell permission request    | An ACP request asking whether one shell tool call may run. Approval or denial applies only to that call.                                                                                  |
| Replay                      | Sending saved transcript content back to the ACP client when a session is loaded.                                                                                                         |
| Commit                      | A successful SQLite transaction. `PromptRun::commit` saves one complete assistant batch, updates session activity, and only then extends the in-memory transcript.                        |
| Provisional output          | Answer text or visible reasoning sent to the ACP client before the assistant message is validated. It is not saved if the model request is interrupted.                                   |

## Naming rules going forward

- Use transcript for the durable conversation and transcript entry for one
  element. Do not introduce history, record, or event as domain synonyms.
- Use model request for one OpenRouter invocation. Reserve completion for the
  validated result of that request.
- Qualify client as ACP, OpenRouter, or HTTP whenever the surrounding type does
  not make it obvious.
- Describe state changes directly: saved, validated, running, completed,
  uncommitted, or cancelled. Avoid unqualified accepted, pending, and terminal.
- Say send an ACP update. Do not imply confirmed delivery or receipt.
- Say acquire or drop an operation guard. Avoid admission, claim, ownership,
  membership, and release for this mechanism.
- Keep external names such as `cwd`, `reasoning_details`, `AgentThoughtChunk`,
  and `MaxTurnRequests` at their protocol boundaries.
