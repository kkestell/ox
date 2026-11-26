# Show each session's directory and worktree to other sessions

## Goal

`list_sessions` gives each session's `workspace`, which for a worktree session
is the main checkout, and a delivered message names its sender's workspace. An
agent cannot tell which session works in which worktree, so it cannot find the
top-level agent of another worktree, as `AGENTS.md` ("Workflow") asks. After the
change, each `list_sessions` entry carries the session's `directory` and, for a
worktree session, its `worktree`, and a delivered message names the directory
its sender works in.

## Related code

- `internal/server/mcp.go:228` — `listSessions` builds each entry on the
  session's goroutine from fields fixed at creation.
- `internal/server/mcp.go:693` — `messageInput` writes the header
  `Message from Ox session <from> ("<label>") in <workspace>:`.
- `internal/server/session.go:31` — `session.directory` and `session.worktree`,
  fixed at creation.
- `internal/server/methods_test.go:1830` — `TestSendMessage` compares a full
  `list_sessions` entry for a plain session.
- `internal/server/worktree_test.go:223` — `TestWorktreeSession` creates a
  worktree session with the fake harness.

## Decisions

- **Field names and shape.** The entry adds `directory` (always) and `worktree`
  (only for a worktree session), the names and presence rule `workspace/list`
  already uses (`docs/protocol.md`, "Sessions"). `workspace` stays: it names the
  workspace a session belongs to, which `directory` does not for a child in
  another folder. A child in its parent's worktree has no `worktree`, as in
  `workspace/list`; its `directory` lies inside the worktree.
- **Message header.** The header names `sender.directory` in place of
  `sender.workspace`. For a session in its workspace's folder the two are the
  same path; for a worktree session or a child elsewhere the directory is where
  it works.
- **Requirements:** **Claude can message other sessions** gains the directory
  and worktree in the listing. **Message delivery** is unchanged. Both values
  are fixed at creation and read on the session's goroutine like the rest of the
  entry, so every listing and every message gives the same paths.
- **Construction:** `directory` and `worktree` are fixed at creation and have
  one source, the `session` fields; the listing and the header read them.

## Check your assumptions

1. **Claude sees each session's folder and worktree when it lists sessions.**
   Kept: the user approved the new text.
   - **Plan:** The list Claude gets of other sessions names, for each one, the
     folder it works in and, for a worktree session, its worktree, and a
     message from another session says which folder its sender works in. The
     requirement **Claude can message other sessions** changes from "with each
     one's label, workspace, settings, and status" to "with each one's label,
     workspace, the folder it works in, its worktree if it has one, settings,
     and status".
   - **Alternative:** Leave the requirement as it is and treat the folder as a
     detail of the listing. Claude would still get it, but a later change could
     drop it without breaking a stated requirement, and agents would go back to
     guessing which session owns a worktree.

## Test plan

- Server, `TestSendMessage` (`internal/server/methods_test.go`): the `listed`
  struct gains `Directory` and `Worktree`, and the expected entry for `b` has
  `Directory: dir` and no `Worktree`. Fails before the change: `directory` is
  missing.
- Server, `TestWorktreeSession` (`internal/server/worktree_test.go`): after the
  worktree session is created, create a plain session in the same workspace
  (`session/create` without `worktree`) and attach to each. The plain
  session's `list_sessions` lists the worktree session with its `directory`
  and `worktree` equal to those `workspace/list` gives. The worktree session's
  `send_message` to the plain one is received with the header ending
  `in <directory>:`, read from the fake harness's `result` reply as
  `TestSendMessage` does. Both fail before the change: the listing lacks the
  fields, and the header names the main checkout's `sub` folder.

## Implementation plan

1. `internal/server/mcp.go`: add `Directory string json:"directory"` and
   `Worktree string json:"worktree,omitempty"` to `listSessions`'s entry after
   `Workspace`, filled from `sess.directory` and `sess.worktree`. In
   `messageInput`, use `sender.directory` in the header.
2. `internal/server/methods_test.go` and `internal/server/worktree_test.go`:
   the tests above.
3. `docs/protocol.md` ("Tools"): the `list_sessions` entry lists `directory`
   and `worktree` after `workspace`, linking "Sessions" for their meaning and
   the rule that a session without a worktree has no `worktree`; the message
   template reads `in <directory>:`.
4. `docs/functional-requirements.md`: the approved text below.

## Requirement changes

- **Changed:** **Claude can message other sessions.**
  - Old: "Claude can list the other sessions that are not archived, with each
    one's label, workspace, settings, and status, and send any of them a
    message. It does so without asking for approval, except in plan mode, where
    each message asks first."
  - New: "Claude can list the other sessions that are not archived, with each
    one's label, workspace, the folder it works in, its worktree if it has one,
    settings, and status, and send any of them a message. It does so without
    asking for approval, except in plan mode, where each message asks first."

## Documentation updates

- `docs/protocol.md`: the `list_sessions` entry fields and the message header.
