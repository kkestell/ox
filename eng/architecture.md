# Ox architecture

This document describes the architecture implemented by Ox. It defines the
system boundaries, sources of authority, lifecycle boundaries, and invariants.
Source code remains the authority for local control flow and protocol details.

## System boundary

Ox is a local ACP agent. An ACP client supplies prompt requests and receives ACP
updates. Ox sends model requests to OpenRouter, runs tools with the user's
operating-system permissions, and saves sessions in a local SQLite database.

The normal process serves one ACP connection. The headless entry point creates a
session and runs one prompt through the same prompt run, OpenRouter client,
tools, and session store.

```text
ACP client
    |
    v
ACP boundary --> prompt run --> OpenRouter
                    |
                    +--------> tools --> workspace and child processes
                    |
                    +--------> session store --> SQLite
```

OpenRouter, the ACP client, the workspace, child processes, the operating-system
keyring, and SQLite are external boundaries. Their input is validated or
translated before it becomes Ox domain state.

## Components and dependencies

Ox has five architectural components:

- The **ACP boundary** owns the connection, translates ACP input, sends ACP
  updates, exposes session operations, and holds shared process state.
- The **prompt run** coordinates one turn. It is the only component that
  sequences model requests, tool execution, transcript commits, and the final
  response.
- The **OpenRouter client** encodes model requests and turns one streamed
  response into provisional output followed by one validated completion.
- The **tool boundary** defines the concrete tool set and executes one complete
  tool call. It does not send ACP updates or save the transcript.
- The **session store** validates and persists sessions and transcripts. It does
  not know OpenRouter wire formats or construct ACP updates.

The process entry and credential code support these components but do not
participate in prompt orchestration. Dependencies point from the ACP boundary
and prompt run toward the concrete OpenRouter client, tool boundary, and session
store. None of those lower components can start or continue a prompt run.

## Sources of authority

### Transcript

The transcript is the single durable conversation. Both replay and future model
requests are projections of the same saved transcript; neither has a separate
authoritative representation.

A transcript may be empty. Every nonempty transcript begins with one model
entry. Effort entries appear only immediately before the user message where the
new effort level takes effect. An assistant message is followed immediately by
one tool result for each tool call it contains, in call order.

The model entry fixes the OpenRouter model when the first turn starts. The model
does not change within that session. The current effort level may change between
turns. Folding the transcript reconstructs the session settings after load or
restart.

An assistant message keeps answer text, visible reasoning, tool calls, and
continuation metadata together. Continuation metadata is durable model context,
not visible reasoning, and is not included in replay.

### Session metadata

A session summary holds the session ID, exact workspace path, optional session
title, and creation and activity timestamps. The stored workspace path is the
tool context and the identity used when loading a session. Ox requires an
absolute path and compares it exactly without resolving aliases.

The first saved user message supplies the session title from its first nonblank
line. Later turns do not replace it.

### Process state

Process state is either live coordination state or a cache of reconstructible
state:

- operation guards and prompt cancellation coordinate active session operations;
- ACP selections hold the latest session settings chosen for a future turn;
- the OpenRouter client cache holds credentials and reusable HTTP state; and
- the session store holds one SQLite connection.

ACP selections are not a second durable settings store. A prompt run takes a
settings snapshot at its turn boundary. The transcript remains authoritative for
the fixed model and last saved effort level.

## Lifecycle boundaries

### Turn boundary

One prompt run owns the state for one turn: its settings snapshot, saved
transcript copy, cancellation signal, active completion stream, and any
uncommitted assistant batch.

The user message and any session-setting changes are committed before the first
model request. Model output remains provisional until the OpenRouter client
yields a validated completion. Tools run only from that completion.

When a completion contains tool calls, the prompt run executes them in order and
builds one assistant batch. The batch is committed before another model request
begins. A successful final response follows the same commit boundary.

### Assistant-batch boundary

An assistant batch contains one validated assistant message and exactly one
final tool result for every tool call in that message. The session store saves
the whole batch and updates session activity in one SQLite transaction.

The prompt run holds incomplete results in an uncommitted assistant batch. Every
observed tool outcome enters it before a finished ACP update is sent or a later
tool begins. The prompt run extends its transcript copy only after the database
commit succeeds.

If the turn stops after validation, every call without an observed outcome gets
an explicit failed or cancelled tool outcome before the batch is saved. Ox does
not infer success or claim that cancellation reversed effects.

### Projection boundary

ACP updates are projections, not authoritative state. Live answer text and
visible reasoning may be sent before validation; this provisional output is
absent from replay if the model request fails or is cancelled. Replay contains
only saved, displayable transcript content and final tool states.

Sending an ACP update does not confirm that the ACP client received or displayed
it. An ACP update failure stops new work, but the prompt run still attempts to
save a validated uncommitted assistant batch.

## Concurrency and cancellation

Prompt, load, and delete are session operations. Each must acquire an operation
guard, so at most one of them runs for a session at a time. Different sessions
may run concurrently. Listing and changing ACP selections do not acquire an
operation guard.

The operation guard remains held through cleanup, save attempts, replay, and
response sending. The SQLite mutex is separate and covers only a synchronous
store operation; it is never held across an asynchronous wait.

Prompt cancellation is latched and scoped to the active prompt. It prevents new
model requests and tool work but does not roll back a saved user message,
observed tool effects, or committed transcript entries. Each running tool owns
the cleanup boundary for its resources. Connection shutdown cancels active
prompts and waits for their operation guards to drop.

## Capability and trust boundaries

Text and descriptive resource links are the supported prompt input. Resource
links contribute text and are not fetched. OpenRouter streams, tool calls,
stored transcript entries, and ACP input are all treated as untrusted at their
boundaries.

Read, search, and patch operations are constrained to the session workspace.
Shell starts there but may access other paths and the network with Ox's
permissions. Over ACP, every shell call requires a shell permission request;
headless prompts approve it automatically. Tool effects are not transactional
and may remain after failure or cancellation.

Credentials come from `OPENROUTER_API_KEY` or the operating-system keyring and
are not part of a session or transcript. The OpenRouter client is loaded lazily
for ACP work and cached. Child shell processes do not inherit
`OPENROUTER_API_KEY`.

## Invariants

The implementation enforces these properties:

1. A session has at most one active prompt, load, or delete operation in the
   process.
2. The saved user message is durable before its turn's first model request.
3. A nonempty transcript begins with exactly one model entry, and every model
   request in the session uses that model.
4. An effort entry appears only immediately before the user message where it
   takes effect.
5. Tool execution begins only from a validated completion.
6. A saved assistant message has exactly one matching final tool result for each
   tool call, in call order.
7. An observed tool outcome enters the uncommitted assistant batch before its
   finished ACP update is sent.
8. A prompt run advances its transcript copy only after the corresponding
   database commit succeeds.
9. Replay and future model requests derive from the same saved transcript.
10. Cancellation stops new work without claiming to undo saved state or external
    effects.
11. No SQLite transaction or mutex guard crosses an asynchronous wait.
12. A request-level failure does not poison unrelated sessions or terminate a
    healthy ACP connection.

## Deliberate constraints

The implemented architecture has one OpenRouter provider, one concrete tool set,
one SQLite connection, sequential tool execution, whole-transcript reads, and
process-local operation guards. It has no provider fallback, automatic retry,
prompt queue, context compaction, durable provisional output, background
continuation, cross-process coordination, or database migration path.

These are current system constraints, not unimplemented abstractions. Changing
one requires revisiting the authority or lifecycle boundary that depends on it.
