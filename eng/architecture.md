# Ox architecture

This document describes the architecture implemented by Ox. It defines the
system boundaries, sources of authority, lifecycle boundaries, and invariants.
Source code remains the authority for local control flow and protocol details.

## System boundary

Ox is a local ACP agent. An ACP client supplies prompt requests and receives ACP
updates. Ox sends model requests to OpenRouter, runs tools and skill hooks with
the user's operating-system permissions, and saves sessions in a local SQLite
database.

The normal process serves one ACP connection. The headless entry point creates a
session, runs one prompt through the same prompt run, OpenRouter client, tools,
and session store, and prints the final answer.

```text
ACP client
    |
    v
ACP boundary --> prompt run --> OpenRouter
                    |
                    +--------> tools --> workspace and child processes
                    |
                    +--------> hooks --> child processes
                    |
                    +--------> session store --> SQLite
```

OpenRouter, the ACP client, the workspace, workspace skill definitions, child
processes, the operating-system keyring, and SQLite are external boundaries. Their input is validated or
translated before it becomes Ox domain state.

## Components and dependencies

Ox has five architectural components:

- The **ACP boundary** owns the connection, translates ACP input, sends ACP
  updates, exposes session operations, and holds shared process state.
- The **prompt run** coordinates one turn. It is the only component that
  sequences model requests, tool execution, hook runs, transcript commits, and
  the final response.
- The **OpenRouter client** encodes model requests and turns one streamed
  response into provisional output followed by one validated completion.
- The **tool boundary** defines the concrete tool set and executes one complete
  tool call. It does not send ACP updates or save the transcript.
- The **session store** validates and persists sessions and transcripts. It does
  not know OpenRouter wire formats or construct ACP updates.

The process entry, credential code, skill catalog loading, hook protocol, and
child-process execution support these components but do not participate in
prompt orchestration. Dependencies point from the ACP boundary
and prompt run toward the concrete OpenRouter client, tool boundary, and session
store. None of those lower components can start or continue a prompt run.

## Sources of authority

### Transcript

The transcript is the single durable conversation. Both replay and future model
requests are projections of the same saved transcript; neither has a separate
authoritative representation.

A transcript may be empty. Every nonempty transcript begins with one model
entry. A turn starts with a user message or a skill invocation. Effort and mode
entries form a contiguous settings block immediately before the turn start where
those settings take effect; a block contains at most one of each. An assistant
message is followed immediately by one tool result for each tool call it
contains, in call order. Hook feedback follows an assistant message with no tool
calls.

A skill invocation stores the skill's name, its literal arguments, and the
instructions copied from its definition when it was invoked, so later changes to
the definition do not change saved model context. Hook feedback stores the
skill, the hook decision, and the hook's message. Model requests send each as a
labeled user-role message. Hook feedback is neither a user message nor a tool
result.

The model entry fixes the OpenRouter model when the first turn starts. The model
does not change within that session. The current effort level and session mode
may change between turns. Folding the transcript reconstructs all session
settings after load or restart, including Auto mode before another command can
run.

An assistant message keeps answer text, visible reasoning, tool calls, and
continuation metadata together. Continuation metadata is durable model context,
not visible reasoning, and is not included in replay.

A compaction checkpoint stores a nonempty summary and the exclusive index of a
completed transcript prefix. The latest checkpoint controls model requests;
earlier checkpoints and all covered entries remain saved for replay. Checkpoints
are omitted from replay. A model request uses the latest summary as a labeled
user-role message, followed by entries after its covered prefix. When the
latest turn start is a skill invocation inside the covered prefix, its message
is repeated right after the summary, so a long hook-driven run keeps its
instructions and arguments. Once a later turn starts, the summary alone carries
it. Request estimates, input admission, and compaction cut sizes use the same
projection.

The system prompt is not a transcript entry. It is neither stored nor replayed.

### Slash commands and skills

Ox advertises its slash commands through an ACP session update after a session
is created or loaded: the built-in `/compact` and one command for each skill in
the session's skill catalog. A skill's argument hint becomes the command's
input hint. A recognized command is handled at the ACP boundary before anything
is saved or a model request begins. `/compact` runs a guarded, cancellable
compaction without saving a user message.

A skill is `.agents/skills/<name>/SKILL.md` directly under the session
workspace: YAML frontmatter with a name, description, optional argument hint,
and optional `before_stop` hook command, followed by Markdown instructions. Ox
has no built-in skills. The skill catalog is loaded when a session becomes
active through `session/new` or its first `session/load` in the process,
alongside the system prompt, and is kept for the active session; a missing
skills directory means no skills.

Ox ignores unknown top-level frontmatter keys and unknown hook kinds, so skills
can share a directory with other agents. An empty `hooks` map declares no hook;
a `before_stop` definition must contain only a nonblank `command`.

A prompt whose first word is `/<name>` for a catalog skill invokes it. The rest
of the text, trimmed, is its arguments. The prompt run saves a skill invocation
in place of the user message and passes the skill's hook to that run only.
The hook holds its command and directory; its name and arguments come from the
saved skill invocation for the current turn.
Other slash-prefixed text is an ordinary user message. Headless prompts never
invoke skills.

### Session metadata

A session summary holds the session ID, exact workspace path, optional session
title, and creation and activity timestamps. The stored workspace path is the
tool context and the identity used when loading a session. Ox requires an
absolute path and compares it exactly without resolving aliases.

The first saved turn start supplies the session title from its first nonblank
line; a skill invocation contributes `/<name> <arguments>`. Later turns do not
replace it.

### Process state

Process state is either live coordination state or a cache of reconstructible
state:

- operation guards and prompt cancellation coordinate active session operations;
- active sessions hold, for each session created or loaded in the process, the
  ACP selections chosen for a future turn and the system prompt and skill
  catalog captured at activation;
- the OpenRouter client cache holds credentials and reusable HTTP state; and
- the session store holds one SQLite connection.

ACP selections are not a second durable settings store. A prompt run takes a
model, effort, and mode snapshot at its turn boundary. Changes made while it is
running apply to the next turn. The transcript remains authoritative for the
fixed model and last saved effort level and mode.

### System prompt

Ox's built-in prompt is the editable Markdown file
`src/prompts/system_prompt.md`. When a session becomes active through
`session/new`, its first `session/load` in the process, or the headless entry
point, Ox appends the workspace root `AGENTS.md` under a workspace-instructions
heading. A blank or missing `AGENTS.md` adds nothing. The resulting system
prompt is kept in memory for the active session. A prompt for a session that is
not active fails before saving its user message. A repeated load in the same
process keeps both the system prompt and the skill catalog.

Every ordinary model request for an active session sends the same system-role message
before the transcript. This keeps the request prefix stable while the transcript
grows. A repeated load in the same process reuses the assembled prompt. A later
process assembles it again from its built-in prompt and the then-current
`AGENTS.md`. The system prompt is not written to SQLite, replayed, or used for
the session title.

Compaction uses the dedicated `src/prompts/compaction_prompt.md` as its system
prompt, the session model at Low effort, no tools, and a bounded text completion.
The active session's system prompt remains unchanged for ordinary requests.

## Lifecycle boundaries

### Turn boundary

One prompt run owns the state for one turn: its settings snapshot, saved
transcript copy, cancellation signal, active completion stream, any
uncommitted assistant batch, and the invoked skill's hook.

An input that cannot fit even after the largest eligible compaction cut is
rejected before persistence. An accepted user message or skill invocation and
any session-setting changes are committed before the first model request. Model output remains
provisional until the OpenRouter client yields a validated completion. Tools run
only from that completion.

Before each ordinary model request, the prompt run estimates its serialized
size and compacts when it reaches the automatic threshold. A checkpoint is
appended in one store transaction and becomes active only after that commit.
Failed or cancelled summarization leaves the previous model context intact.
An explicit pre-stream input-context overflow may force one compaction and one
retry if the request becomes smaller.

When a completion contains tool calls, the prompt run executes them in order and
builds one assistant batch. The batch is committed before another model request
begins. A successful final response follows the same commit boundary.

When the prompt run was started by a skill invocation whose skill declares a
`before_stop` hook, each committed assistant message with a finished OpenRouter
stop is given to the hook. A `continue` decision saves the hook feedback and
makes another model request in the same prompt run; a `stop` decision saves the
feedback and ends the run. Feedback passes the same input admission as a turn
start. A hook error ends the run with an error and saves nothing for that hook
run. Refusal, token limit, request failure, and cancellation never run a hook.
The hook ends with its prompt run; nothing stays enabled for later turns. The
final answer is the text of the assistant message committed with the finished
stop that ended the run.

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
only saved, displayable transcript content and final tool states. A hook run is
shown as an ACP execute tool call with an Ox-generated ID. It is not a model
tool call and never enters an assistant batch; replay rebuilds it from saved
hook feedback as a completed tool call.

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
model requests, tool work, and hook runs but does not roll back a saved user
message, observed tool effects, or committed transcript entries. Each running
tool or hook owns the cleanup boundary for its resources: its whole process
group is stopped, a hook's after a two-second SIGTERM grace period. Connection shutdown cancels active
prompts and waits for their operation guards to drop.

## Capability and trust boundaries

Text and descriptive resource links are the supported prompt input. Resource
links contribute text and are not fetched. OpenRouter streams, tool calls,
stored transcript entries, and ACP input are all treated as untrusted at their
boundaries.

Read, search, and patch operations are constrained to the session workspace.
`read_file` and `grep` accept a directly named symbolic link to a file only when
its target remains inside the workspace; search traversal does not follow
symbolic links. Patch operations reject a symbolic link as their target. Read
and patch open files through a workspace directory handle after path validation.
Search uses ripgrep to discover candidate paths and checks each candidate
through that handle before returning its name or opening it to search contents.
Read and patch reject a link swapped into a validated path before use; search
drops a candidate that no longer resolves inside the workspace and does not
forward ripgrep's unchecked path diagnostics. Shell starts in the workspace but
may access other paths and the network with Ox's
permissions. In Ask mode, every ACP shell call requires a permission request.
In Auto mode, shell calls run without that request. Headless prompts use and
save Auto mode. The captured mode is the authorization policy for the whole
turn; the ACP connection only transports Ask requests. Tool effects are not
transactional and may remain after failure or cancellation.

`AGENTS.md` is user-controlled workspace input appended to the system prompt. A
file that cannot be read, is not UTF-8, or exceeds 32 KiB fails session
activation instead of being ignored. The same holds for each `SKILL.md`, and a
definition that fails validation, or a skill named `compact`, fails activation
with its path.

A skill's `before_stop` hook is a workspace-defined command run with
`/bin/sh -c` in the skill directory with Ox's permissions. Invoking the skill is
the approval to run its hook in both Ask and Auto mode. The hook receives the
skill, arguments, workspace path, Ox executable path, model, effort level, and
answer as JSON on stdin, and must print one decision object within its time and
output limits.

The example goal skill launches a nested headless Auto-mode agent in the same
workspace. Its prompt asks it not to change files; permissions do not enforce
that restriction.

Credentials come from `OPENROUTER_API_KEY` or the operating-system keyring and
are not part of a session or transcript. The OpenRouter client is loaded lazily
for ACP work and cached. Child shell processes do not inherit
`OPENROUTER_API_KEY`. Hooks inherit it, so a hook can run a nested `ox run`.

## Invariants

The implementation enforces these properties:

1. A session has at most one active prompt, load, or delete operation in the
   process.
2. The saved user message or skill invocation is durable before its turn's first
   model request.
3. A nonempty transcript begins with exactly one model entry, and every model
   request in the session uses that model.
4. Effort and mode entries form one nonduplicating settings block immediately
   before the turn start where they take effect.
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
13. Every model request for an active session sends the same system prompt
    assembled when the session became active before the transcript.
14. Every shell call uses the session mode captured at the prompt's turn
    boundary.
15. A hook runs only on an assistant message committed with a finished
    OpenRouter stop, in the prompt run that invoked its skill, and its feedback
    is saved before the next model request.

## Deliberate constraints

The implemented architecture has one OpenRouter provider, one concrete tool set,
one hook point, one SQLite connection, sequential tool execution,
whole-transcript reads, and process-local operation guards. It has no provider fallback, prompt queue,
durable provisional output, background continuation, cross-process coordination,
or database migration path.

These are current system constraints, not unimplemented abstractions. Changing
one requires revisiting the authority or lifecycle boundary that depends on it.
