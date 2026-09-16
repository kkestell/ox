# Cacheable todo context

## Goal

A todo write must not invalidate the provider's prompt cache. The serialized
todo entries currently sit between the system prompt and all history, so every
todo replacement changes the cached prefix of the whole conversation. Move the
todo context to the end of the request, after history, and keep compaction from
mistaking it for the newest message group.

## Related code

- `internal/agent/loop.go` — `modelRequest` assembles the provider request and
  returns the offset that converts request indices into history indices;
  `todoContextMessage` builds the serialized todo message.
- `internal/agent/compact.go` — `planRequestAdmission`, `planCompaction`, and
  `validateCompactionPlan` walk backwards from the end of the request to find
  the newest complete message group to retain.
- `internal/agent/state.go` — `validateCompactionHistory` and `spliceCompacted`
  apply a recorded compaction to history, which carries no system message.

## Decisions

The todo message stays a system message. History holds only user, assistant, and
tool messages, so a trailing system message identifies request context appended
after the conversation without threading a count through every compaction
function.

With the todo message gone from the prefix, history always begins at the message
after the system prompt, so the compaction record's history offset is a
constant.

## Test plan

- `planCompaction` retains the newest history group rather than the trailing
  todo message, and `compactRequest` keeps that message last.
- A compaction plan whose tail starts at the todo message is rejected.
- Through the shipped binary, a continuation request after a todo write ends
  with the todo context message.

## Implementation plan

- Append the todo context message after history in `modelRequest` and replace
  its history offset return with a constant in `compact.go`.
- Add a helper naming where compactable history ends, and use it in
  `planRequestAdmission`, `planCompaction`, and `validateCompactionPlan`.
- Update the compaction and end-to-end todo tests.
