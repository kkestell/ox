# Ox architecture

Ox is an interactive ACP client. It launches an ACP server for a workspace and
shows its output in the terminal. The bundled `ox-acp` server sends model
requests to OpenRouter, runs tools with the user's operating-system permissions,
and saves sessions in a local SQLite database. One server process serves one ACP
connection. A headless entry point runs one prompt through the same parts and
prints the answer.

## Client boundary

The client launches one configured ACP server and creates one session, then can
close it and load another saved session in the workspace. It owns terminal
input, output, permission responses, and cancellation. The server owns saved
sessions. The client can launch `ox-acp` or another compatible ACP server.

## Components

- **ACP boundary**: owns the connection, translates ACP input and output, and
  runs session operations.
- **Prompt run**: runs one turn: model requests, tool execution, and transcript
  commits. The same loop runs the main agent and its subagents.
- **Compaction**: summarizes older transcript to keep model requests within the
  context limit, both automatically and on request.
- **OpenRouter client**: encodes model requests and turns a streamed response
  into one validated completion.
- **Tools**: the fixed tool set. A tool executes one call and neither sends ACP
  updates nor saves the transcript.
- **Session store**: validates and saves sessions and transcripts. It knows
  neither OpenRouter's formats nor ACP's.

Dependencies point from the ACP boundary, prompt run, and compaction toward the
OpenRouter client, tools, and session store. Settings, skills, credentials, and
process execution support these components but take no part in running a turn.

## External boundaries

The ACP client, OpenRouter, the workspace, settings and skill files, child
processes, the keyring, and SQLite are outside `ox-acp`. Their input is
untrusted and is validated or translated before it becomes server state.
Credentials never enter a session or a child process.

## Sources of authority

- **The transcript is the only durable record of a conversation.** Replay to the
  ACP client and future model requests are both derived from it. A compaction
  summary changes what model requests contain but never replaces the saved
  entries.
- **Each turn records its own settings.** The model, effort, and mode are
  captured when a turn starts and saved with it, so a loaded session resumes
  from its transcript.
- **ACP updates are projections.** Output streamed before validation is
  provisional and absent from replay if the request fails.
- **The system prompt is not part of the transcript.** It is assembled when a
  session becomes active and stays fixed for that session in the process.

## Lifecycles

- **Turn**: the input is saved before the first model request. Model output is
  provisional until it becomes a validated completion, and tools run only from
  that completion.
- **Assistant batch**: a model message and one outcome per tool call are saved
  together, atomically. Every exit from a batch, including cancellation, gives
  each call an explicit outcome before saving. The prompt run advances only
  after the save commits.
- **Subagents**: belong to one main prompt run. Each has its own child session
  and reports back to the main agent, which alone saves those reports. When the
  main turn ends, its subagents are stopped.
- **Shell processes**: background commands that outlive a tool call. Each
  belongs to the agent that started it, within one active session, and only that
  agent can reach it. They end when the session is closed or deleted, the
  connection shuts down, or their subagent ends.

## Concurrency and cancellation

At most one prompt, load, or delete runs for a session at a time. Different
sessions run concurrently. No lock or database transaction is held across an
asynchronous wait. Cancellation stops new work but never claims to undo saved
state or external effects. A failure in one request does not affect other
sessions or end the connection.

## Trust

File tools are confined to the session workspace. The shell runs with the user's
permissions and can reach anything they can. In Ask mode, shell actions need the
client's permission; in Auto mode they do not. The mode captured at the start of
a turn governs the whole turn.

## Deliberate constraints

One model provider, one tool set, one SQLite connection, sequential tool
execution, and process-local coordination. There is no provider fallback, prompt
queue, cross-process coordination, or database migration. Changing any of these
means revisiting the boundaries that depend on it.
