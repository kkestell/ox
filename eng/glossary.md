# Glossary

## Naming

- Use transcript for the durable conversation and transcript entry for one
  element. Do not introduce history, record, or event as domain synonyms.
- Use model request for one OpenRouter invocation. Reserve completion for the
  validated result of that request.
- Qualify client as ACP, OpenRouter, or HTTP whenever the surrounding type does
  not make it obvious.
- Say session title or tool call title. Never write an unqualified title, which
  could mean either.

## Names across boundaries

| Domain                      | ACP                  | OpenRouter                               |
| --------------------------- | -------------------- | ---------------------------------------- |
| continuation metadata       | —                    | `reasoning_details`                      |
| workspace path              | `cwd`                | —                                        |
| visible reasoning, request  | —                    | `reasoning`                              |
| visible reasoning, response | `AgentThoughtChunk`  | `delta.reasoning`                        |
| effort level                | `effort` option      | `reasoning.effort` (omitted for Default) |
| session mode                | `mode` option        | —                                        |
| tool name                   | tool kind            | `function.name`                          |
| tool outcome `cancelled`    | tool status `failed` | —                                        |
| prompt outcome              | `stopReason`         | `finish_reason`                          |
| system prompt               | —                    | first `system` message                   |

## Terms

- **ACP**: Agent Client Protocol, the JSON-RPC interface between an editor or
  other client and Ox.
- **ACP client**: The editor or application connected to Ox. It is distinct from
  the OpenRouter and HTTP clients.
- **Agent**: Ox as presented through ACP.
- **ACP update**: A `session/update` notification that describes session
  metadata, model output, or tool state. Sending one does not confirm that the
  ACP client received or displayed it.
- **ACP boundary**: The component that translates ACP input and output, exposes
  session operations, and owns shared process state.
- **ACP tool status**: The client-facing state of a tool call: pending, in
  progress, completed, or failed. A cancelled Ox tool outcome is presented as
  failed because ACP has no separate cancelled tool status.
- **Session**: A saved conversation and its metadata, identified by a session
  ID. Its OpenRouter model is fixed when the first turn starts.
- **Active session**: A session created or loaded in the current process. Its
  system prompt is assembled when it becomes active, and only an active session
  can be configured or prompted over ACP.
- **System prompt**: Ox's built-in agent instructions followed, when present, by
  workspace instructions. It is assembled when a session becomes active and sent
  as the first message of every model request for that session.
- **Workspace instructions**: The text of `AGENTS.md` at the workspace root,
  appended to the system prompt when a session becomes active.
- **Session settings**: The model, effort level, and session mode in force for
  a turn.
- **ACP selections**: The latest session settings selected through ACP for a
  future turn. They are process state, not durable authority.
- **Saved settings**: The session settings rebuilt by folding a stored
  transcript. They are the durable authority for the session model and the
  fallback when there are no ACP selections.
- **Settings snapshot**: The session settings a prompt run captures at its turn
  boundary.
- **Effort level**: One of Ox's four reasoning levels: Default, Low, Medium, or
  High.
- **Effort mapping**: The per-model table that turns an effort level into an
  OpenRouter effort string, or into no reasoning parameter for Default.
- **Session mode**: The durable choice that controls shell authorization for a
  turn: Ask or Auto.
- **Ask mode**: The session mode that requests ACP client permission before
  each shell call. It is the default for a new ACP session.
- **Auto mode**: The session mode that runs shell calls without an ACP
  permission request. Headless prompts use Auto.
- **Session title**: The short label a session shows in a client, taken once
  from the first nonblank line of the first saved user message and shortened to
  80 characters.
- **Session summary**: A session's ID, workspace path, optional session title,
  and creation and activity timestamps, without its transcript.
- **Stored session**: A session summary paired with its validated transcript.
- **Session store**: The SQLite-backed component that creates, reads, lists,
  updates, and deletes sessions and their transcripts.
- **Workspace path**: The exact absolute path associated with a session. Ox does
  not normalize or resolve aliases.
- **Session operation**: One prompt, load, or delete running for a session. At
  most one can run for the same session at a time.
- **Operation guard**: A value that keeps one session busy for a session
  operation. Dropping it makes the session available.
- **Prompt request**: One ACP request containing user content for a session.
- **Slash command**: A named command advertised through an ACP session update
  and sent by the ACP client as prompt text beginning with `/`. Ox recognizes a
  slash command before saving a user message or making a model request.
- **Prompt run**: The work caused by one prompt request: save the user message,
  request model output, run tools, save results, and respond.
- **Headless entry point**: The `ox run` mode, which creates a session and runs
  one prompt without an ACP client. A prompt run there is headless.
- **Prompt outcome**: The internal reason a prompt run stopped. It determines
  how unfinished tool calls are completed and whether Ox returns an ACP stop
  reason or an error.
- **Prompt cancellation**: A per-prompt signal that remains cancelled once
  triggered. It stops new work but does not roll back model or tool effects
  already observed.
- **User message**: Text produced from the supported ACP content blocks and
  saved before the first model request.
- **Transcript**: The ordered, saved conversation used for both session replay
  and future model requests.
- **Transcript entry**: A model entry, effort entry, mode entry, user message,
  assistant message, or tool result in the transcript.
- **Model entry**: The first transcript entry. It stores the OpenRouter model
  used for every model request in that session.
- **OpenRouter client**: The concrete client that verifies the API key and sends
  model requests to OpenRouter's chat-completions endpoint.
- **Model catalog**: The OpenRouter models Ox offers, paired with their effort
  mappings.
- **Model request**: One OpenRouter chat-completion HTTP request. A prompt run
  may make several.
- **Completion stream**: The reader for one streamed OpenRouter response after
  its HTTP request has succeeded. It yields output deltas followed by one
  completion.
- **OpenRouter stream item**: An answer-text delta, a reasoning delta, or the
  one completion yielded by a completion stream.
- **Assistant message**: The validated model output assembled from a completion
  stream: answer text, visible reasoning, tool calls, and continuation metadata.
- **Completion**: An assistant message paired with the normalized reason
  OpenRouter stopped generating.
- **OpenRouter stop**: The normalized OpenRouter stopping reason attached to a
  completion: finished, tool calls, token limit, or refusal.
- **Answer text**: The model's user-facing answer. Deltas may be sent live; the
  assembled text is saved only as part of a complete assistant batch.
- **Visible reasoning**: Reasoning text presented to the ACP client. It is
  distinct from opaque continuation metadata.
- **Continuation metadata**: Opaque data stored internally as
  `continuation_metadata` and encoded as OpenRouter `reasoning_details` on a
  later model request. It is not displayed as reasoning.
- **Tool boundary**: The component that defines the concrete tool set and
  executes one complete tool call.
- **Tool call**: A model-produced call ID, tool name, and raw argument string.
- **Tool call title**: The one-line description an ACP client shows for a tool
  call, built from the call's arguments and shortened to 80 characters. A call
  whose arguments are missing or malformed is titled by its tool name alone.
- **Tool kind**: The ACP category that tells a client which icon to show for a
  tool call: execute, read, search, edit, or other.
- **Tool outcome**: What Ox knows happened: completed, failed, or cancelled,
  with explanatory text.
- **Tool result**: A tool call's ID and name paired with its outcome.
- **Assistant batch**: One assistant message plus exactly one final tool result
  for each call in the message. The store saves it in one transaction.
- **Uncommitted assistant batch**: A validated assistant message whose tool
  outcomes are incomplete or have not yet been saved.
- **Shell permission request**: An ACP request asking whether one shell tool
  call may run. Approval or denial applies only to that call.
- **Replay**: Sending saved transcript content back to the ACP client when a
  session is loaded.
- **Commit**: A successful SQLite transaction. `PromptRun::commit` saves one
  complete assistant batch, updates session activity, and only then extends the
  in-memory transcript.
- **Provisional output**: Answer text or visible reasoning sent to the ACP
  client before the assistant message is validated. It is not saved if the model
  request is interrupted.
