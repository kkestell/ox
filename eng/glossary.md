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

| Domain                      | ACP                  | OpenRouter                                      |
| --------------------------- | -------------------- | ----------------------------------------------- |
| continuation metadata       | —                    | `reasoning_details`                             |
| workspace path              | `cwd`                | —                                               |
| visible reasoning, request  | —                    | `reasoning`                                     |
| visible reasoning, response | `AgentThoughtChunk`  | `delta.reasoning`                               |
| effort level                | `effort` option      | `reasoning.effort` (omitted for Default)        |
| session mode                | `mode` option        | —                                               |
| tool name                   | tool kind            | `function.name`                                 |
| tool outcome `cancelled`    | tool status `failed` | —                                               |
| prompt outcome              | `stopReason`         | `finish_reason`                                 |
| system prompt               | —                    | first `system` message                          |
| context tokens              | `used`               | `usage.prompt_tokens + usage.completion_tokens` |
| context limit               | `size`               | `context_length`                                |
| session cost                | `cost`               | sum of `usage.cost`                             |

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
  ID. A main session is one an ACP client or headless run created.
- **Child session**: A saved session linked to its main session through
  `parent_session_id`, holding one subagent's conversation. Its ID is the
  `subagent_id` in coordination tools. It never appears in ACP listing and is
  deleted with its main session.
- **Subagent**: An agent owned by one main prompt run, running the shared loop
  against its own child session concurrently with the main agent. `Subagents`
  in `src/subagents.rs` is its prompt-owned guard and shared coordination
  state.
- **Agent turn**: One execution of the shared loop, for the main agent or a
  subagent. It is `AgentTurn` in code; the prompt run remains the overall
  operation.
- **Agent message**: A subagent final answer or failure published to the main
  agent, represented by `AgentMessage` and saved in an `AgentMessages`
  transcript entry of the main session.
- **ACP identity**: The main session ID and, for a subagent, its child session
  ID, used for ACP updates and permission requests. It is `AcpIdentity` in
  code; the session store uses each agent's own session ID instead.
- **Presentation**: The ACP connection, ACP identity, and choice to send or
  suppress updates that one agent turn uses. It is `Presentation` in code.
- **Tool context**: The workspace path, the agent session ID of the agent making
  the call, the session's shell processes, and, for the main agent, its
  subagents, passed to tool execution and permission classification. It is
  `ToolContext` in code.
- **Active session**: A session created or loaded in the current process. Its
  system prompt is assembled and its skill catalog loaded when it becomes
  active, and only an active session can be configured or prompted over ACP.
  It owns its shell processes.
- **System prompt**: Ox's built-in agent instructions followed, when present, by
  workspace instructions. It is assembled when a session becomes active and sent
  as the first message of every model request for that session.
- **Workspace instructions**: The text of `AGENTS.md` at the workspace root,
  appended to the system prompt when a session becomes active.
- **Session settings**: The model, effort level, and session mode in force for
  a turn.
- **ACP selections**: The latest session settings selected through ACP for a
  future turn. They are process state, not durable authority.
- **Saved settings**: The model, effort level, and session mode of the latest
  turn start. They are the durable authority for the session settings after
  load. An empty transcript has no saved settings.
- **Settings snapshot**: The session settings a prompt run captures at its turn
  boundary.
- **Effort level**: Default, which sends no reasoning parameter, or one of the
  OpenRouter efforts `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or
  `max`. A model's effort levels are Default plus the efforts OpenRouter lists
  for it.
- **Session mode**: The durable choice that controls shell authorization for a
  turn: Ask or Auto.
- **Ask mode**: The session mode that requests ACP client permission before
  each shell call, including a background start, and before each input sent to
  a shell process, including closing its stdin. It is the default for a new ACP
  session.
- **Auto mode**: The session mode that runs shell calls and sends input to
  shell processes without an ACP permission request. Headless prompts use
  Auto.
- **Session title**: The short label a session shows in a client, taken once
  from the first nonblank line of the first saved turn input and shortened to
  80 characters. A skill invocation contributes
  `/<name> <arguments>`; an image-only user message contributes `Image`.
- **Session summary**: A session's ID, workspace path, optional session title,
  and creation and activity timestamps, without its transcript.
- **Compaction summary**: Model-generated text carrying relevant older
  conversation into later model requests. It is separate from session metadata.
- **Compaction checkpoint**: A saved transcript entry with a compaction summary
  and the exclusive index of the completed prefix it covers, ending at an
  assistant batch.
- **Context limit**: The maximum token budget of one model request and output,
  taken from the model's OpenRouter `context_length` in the model catalog.
- **Request estimate**: Ox's heuristic token estimate for a serialized model
  request, using three bytes per token for text and a fixed allowance per image.
- **Stored session**: A session summary paired with its validated transcript.
- **Session store**: The SQLite-backed component that creates, reads, lists,
  updates, and deletes sessions and their transcripts.
- **Workspace path**: The exact absolute path associated with a session. Ox does
  not normalize or resolve aliases.
- **Session operation**: One prompt, load, or delete running for a session. At
  most one can run for the same session at a time. `/compact` arrives as a
  prompt request and runs as a prompt operation.
- **Operation guard**: A value that keeps one session busy for a session
  operation. Dropping it makes the session available.
- **Prompt request**: One ACP request containing user content for a session.
- **Slash command**: A named command advertised through an ACP session update
  and sent by the ACP client as prompt text beginning with `/`: the built-in
  `/compact` or a skill in the skill catalog. Ox recognizes a slash command
  before saving anything or making a model request.
- **Skill**: A `SKILL.md` definition and its directory in a skills directory.
  It has a name, description, optional argument hint, and instructions.
- **Skills directory**: One of the directories Ox loads skills from, highest
  priority first: `~/.config/ox/skills/`, `~/.agents/skills/`, and the
  workspace's `.agents/skills/`. Each holds `<name>/SKILL.md` entries.
- **Skill catalog**: The skills available to an active session, loaded when it
  becomes active.
- **Skill invocation**: A turn input holding a skill's name, arguments,
  instructions, and image attachments, saved in place of the user message for
  that turn.
- **Prompt run**: The work caused by one prompt request: save the turn start,
  request model output, run tools, run and stop its subagents, save results, and
  respond.
- **Final answer**: The text of the assistant message committed with the
  finished OpenRouter stop that ended a prompt run, carried only in a finished
  outcome.
- **Headless entry point**: The `ox run` mode, which creates a session, runs
  one prompt without an ACP client, and prints the final answer. A prompt run
  there is headless and never invokes a skill.
- **Prompt outcome**: The internal reason a prompt run stopped. It determines
  how unfinished tool calls are completed and whether Ox returns an ACP stop
  reason or an error.
- **Prompt cancellation**: A per-prompt signal that remains cancelled once
  triggered. It stops new work but does not roll back model or tool effects
  already observed.
- **User message**: Ordered text and images produced from the supported ACP
  content blocks and saved before the first model request. A subagent's
  assigned task and each follow-up message are also saved as user messages.
- **User message part**: One text or image element in a user message.
- **Image attachment**: A validated user-provided image with base64 data and a
  MIME type, saved in a user message or skill invocation.
- **Transcript**: The ordered, saved conversation used for both session replay
  and future model requests.
- **Transcript entry**: A turn start, assistant batch, compaction checkpoint,
  or, in a main session only, agent messages in the transcript.
- **Turn input**: The user message or skill invocation that starts a turn. It
  is `TurnInput` in code and `PromptInput.turn_input` in a prompt run's input.
- **Turn start**: A transcript entry holding a turn input with the model, effort
  level, and session mode captured for that turn. It is `TurnStart` in code and
  `turn_start` in the database.
- **OpenRouter client**: The concrete client that verifies the API key and sends
  model requests to OpenRouter's chat-completions endpoint.
- **Model catalog**: The OpenRouter models from `GET /models` that pass the
  catalog filter, fetched once at startup, each with its name, context limit,
  effort levels, and image input support.
- **Catalog filter**: The rules that admit an OpenRouter model to the model
  catalog: OpenRouter added it within the last 183 days, it is not a `:batch`
  variant, it accepts tools, takes text input, produces text output, and has a context limit above 8,000 tokens.
- **Default model**: The model named by `model` in the workspace settings file,
  else by `model` in the settings file, used for a new session and for `ox run`
  without `--model`. It is `Settings::default_model` in code.
- **Settings file**: `~/.config/ox/settings.json`, read once at process startup.
  It names the default model.
- **Workspace settings file**: `.ox/settings.json` in a session workspace. It
  uses the settings file format, and each key it sets replaces the same key from
  the settings file.
- **Model request**: One OpenRouter chat-completion HTTP request. A prompt run
  may make several.
- **Model request parameters**: The validated catalog model, effort level, and
  system prompt that every ordinary model request in a turn sends with the
  transcript. It is `ModelRequestParameters` in code.
- **Completion stream**: The reader for one streamed OpenRouter response after
  its HTTP request has succeeded. It yields output deltas followed by one
  completion.
- **OpenRouter stream item**: An answer-text delta, a reasoning delta, or the
  one completion yielded by a completion stream.
- **Assistant message**: The validated model output assembled from a completion
  stream: answer text, visible reasoning, tool calls, continuation metadata, and
  model usage. It is part of an assistant batch.
- **Model usage**: The input tokens, output tokens, and cost that OpenRouter
  reports for one model request. It is `ModelUsage` in code and is saved with
  the assistant message the request produced.
- **Summarizer cost**: The summed cost of the summarizer requests made by the
  compaction that committed a checkpoint, saved in that checkpoint.
- **Session cost**: The sum of every saved model usage cost and summarizer cost
  in a main session's transcript and in every child session of it, reported in
  US dollars.
- **Context tokens**: The number of tokens Ox reports as currently in a
  session's context: the latest assistant message's input plus output tokens
  when it reported usage and no checkpoint follows it, otherwise the request
  estimate.
- **Usage update**: The ACP update that reports context tokens, the context
  limit, and session cost. It is `SessionUpdate::UsageUpdate` in code and
  `usage_update` on the wire.
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
- **Tool result**: A tool call paired with its outcome: the call and the outcome
  at the same position in its assistant batch. Model requests send it as a
  tool-role message.
- **Assistant batch**: One assistant message plus exactly one final tool outcome
  for each call in the message, in call order. The store saves it as one
  transcript entry in one transaction.
- **Uncommitted assistant batch**: A validated assistant message whose tool
  outcomes are incomplete or have not yet been saved. The step of the prompt
  run that processes that message owns it until the save attempt.
- **Shell permission request**: An ACP request asking whether one shell call
  may run or one `shell_process` write may send its input. Approval or denial
  applies only to that call; approving a background start does not approve
  later input.
- **Background command**: A command started by `shell` with
  `background: true`, whose lifetime continues after that tool call returns.
- **Shell process**: One background command started by one agent of an active
  session, together with its process group, stdin, retained output, and
  current state. `ShellProcesses` in `src/shell_processes.rs` is the owner, and
  `shell_processes` is the field that carries it from the active session to
  the tools.
- **Agent session ID**: The session ID of the agent that started a shell
  process: the main session ID for the main agent, or the child session ID for
  a subagent. It is `ShellProcess::session_id` and `ToolContext::session_id` in
  code.
- **Shell process ID**: An opaque UUID identifying one shell process among the
  shell processes one agent started in one active session. It is `process_id`
  in tool arguments and results and is never an operating-system PID.
- **Replay**: Sending saved transcript content back to the ACP client when a
  session is loaded.
- **Commit**: A successful SQLite transaction. `AgentTurn::commit` takes one
  complete assistant batch, saves it, updates session activity, and only then
  extends the in-memory transcript.
- **Provisional output**: Answer text or visible reasoning sent to the ACP
  client before the assistant message is validated. It is not saved if the model
  request is interrupted.
