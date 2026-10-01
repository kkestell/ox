# Glossary

## Naming

- Use transcript for the durable conversation and transcript entry for one
  element. Do not introduce history, record, or event as domain synonyms.
- Use model request for one invocation of the selected model provider. Reserve
  completion for the validated result of that request.
- Qualify client as ACP, model, OpenRouter, OpenAI, or HTTP whenever the
  surrounding text does not make it obvious.
- Say session title or tool call title. Never write an unqualified title.

## Terms

- **Ox**: The program, `ox`.
- **Ox client**: The interactive terminal ACP client that `ox` runs.
- **Ox server**: The bundled ACP server that `ox acp` runs, also usable by other
  ACP clients.
- **ACP client**: The editor or application connected to an ACP server.
- **Main session**: A session an ACP client or a headless run created.
- **Child session**: A subagent's session, owned by a main session and hidden
  from the ACP client.
- **Subagent**: An agent the main agent starts during a prompt run, working in
  its own child session.
- **Subagent message**: A subagent's answer or failure, delivered to the main
  agent.
- **Active session**: A session created or loaded in the current process. Only
  an active session can be prompted.
- **Prompt run**: All the work caused by one prompt: model requests, tools,
  subagents, and saves.
- **Turn**: One pass of the loop for the main agent or a subagent, from its
  input to its end.
- **Turn start**: The transcript entry that begins a turn, with its input and
  the settings captured for it.
- **Turn error**: The transcript entry that records why a turn ended with an
  error.
- **Session settings**: The model, effort level, and session mode for a turn.
- **Session mode**: Ask, where shell actions need the client's permission, or
  Auto, where they do not.
- **Transcript**: The saved conversation, the source of both replay and model
  requests.
- **Replay**: Sending a loaded session's transcript back to the ACP client.
- **Assistant batch**: One model message and the outcome of each of its tool
  calls, saved together.
- **Tool outcome**: What Ox knows happened to a tool call: completed, failed, or
  cancelled, with the text the model reads.
- **Tool call content**: What the ACP client shows under a finished tool call,
  saved beside the outcome's text. Empty content means the client shows the
  text.
- **Provisional output**: Model output shown before it is validated. It is not
  saved if the request fails.
- **Visible reasoning**: Reasoning text shown to the ACP client.
- **Continuation metadata**: Opaque model state sent back on later requests,
  never shown as reasoning.
- **Session operation**: A prompt, load, close, or delete. At most one runs per
  session at a time.
- **Shell process**: A background command that outlives the tool call that
  started it, reachable only by the agent that started it. It ends when its
  active session closes.
- **Skill**: Instructions from a skills directory, invoked as a slash command.
- **Workspace instructions**: The workspace's `AGENTS.md`, added to the system
  prompt.
- **Model provider**: OpenRouter or OpenAI, selected by a qualified model ID.
- **Qualified model ID**: A model provider ID, a colon, and that provider's
  model ID.
- **Model client**: One model provider's HTTP client.
- **Model catalog**: The models from every model provider whose credentials were
  available when the process started.
- **Session cost**: What a main session and its child sessions have spent on
  model requests.
- **Transcript view**: The client's display of the session's transcript, the
  region above the composer.
- **Thinking**: The transcript view's label for visible reasoning.
- **Composer**: The client's bottom region: the input rows and the status line.
