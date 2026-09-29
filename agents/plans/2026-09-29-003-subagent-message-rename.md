# Rename agent message to subagent message

Fix OX-0007 from `agents/issues.csv`.

## Goal

"Agent message" names one thing: a subagent's answer or failure, delivered to
the main agent. ACP's `AgentMessageChunk`, which carries the assistant's own
text, is the only other "agent message" in the code. After the rename, the
types, functions, saved entry kind, error text, and tool call ID prefix for the
first say "subagent message", and the second keeps ACP's name.

## Related code

- `crates/ox-acp/src/sessions.rs` — defines `AgentMessage`,
  `AgentMessageContent`, `TranscriptEntry::AgentMessages`, and
  `append_agent_messages`, and saves the entry under the kind
  `"agent_messages"`.
- `crates/ox-acp/src/acp/convert.rs` — builds the live and replayed tool calls
  for these messages. `agent_message_chunk` beside them is ACP's assistant text.
- `crates/ox-acp/src/acp/prompt.rs`, `crates/ox-acp/src/subagents.rs`,
  `crates/ox-acp/src/openrouter.rs`, `crates/ox-acp/src/compaction.rs`,
  `crates/ox-acp/src/acp.rs` — publish, deliver, and read the messages.
- `agents/glossary.md` — defines the term.

## Decisions

- The saved entry kind becomes `"subagent_messages"`, the error strings say
  "subagent messages", and the tool call ID prefix becomes `subagent-message-`.
  No migration, per Backwards Compatibility in `AGENTS.md`.
- `AgentMessageChunk`, `agent_message_chunk`, the wire string
  `"agent_message_chunk"`, and the fake server's "agent message chunk" comment
  stay. They are ACP's name for the assistant's text. A search-and-replace on
  `AgentMessage` or `agent_message` would break them, so rename by the names
  listed below.
- `AgentMessageContent` becomes `SubagentMessageContent`. `Messages` in
  `WaitReason` and `take_messages` already sit under a subagent and keep their
  names.

## Naming

- **Subagent message** — a subagent's answer or failure, delivered to the main
  agent. It replaces "agent message" in the glossary, code, comments, and error
  text.
- **Assistant text** — the model's own reply text, sent to the ACP client as
  `AgentMessageChunk`. It is not a subagent message.

| Old                                    | New                                 |
| -------------------------------------- | ----------------------------------- |
| `AgentMessage`                         | `SubagentMessage`                   |
| `AgentMessageContent`                  | `SubagentMessageContent`            |
| `TranscriptEntry::AgentMessages`       | `TranscriptEntry::SubagentMessages` |
| `append_agent_messages`                | `append_subagent_messages`          |
| `agent_message_text`                   | `subagent_message_text`             |
| `agent_message_updates`                | `subagent_message_updates`          |
| `deliver_agent_messages`               | `deliver_subagent_messages`         |
| locals and test names `agent_messages` | `subagent_messages`                 |
| entry kind `"agent_messages"`          | `"subagent_messages"`               |
| tool call ID prefix `agent-message-`   | `subagent-message-`                 |

## Test plan

No guarantee is gained or lost. The existing tests follow the renames:

- The saved-entry test in `sessions.rs` uses the new entry kind and error text,
  and its name becomes `subagent_messages_are_saved_only_in_main_transcripts`.
- The `convert.rs` test asserts the `subagent-message-` prefix.
- The tests in `compaction.rs`, `prompt.rs`, and `acp.rs` use the new type and
  variant names.

## Implementation plan

1. In `crates/ox-acp/src/sessions.rs`, rename the types, variant, method, entry
   kind, error strings, and the `TranscriptEntry` doc, and update the test.
2. In `convert.rs`, `prompt.rs`, `subagents.rs`, `openrouter.rs`,
   `compaction.rs`, and `acp.rs`, update every use, including imports, locals,
   and tests. Change the ID prefix in `convert.rs` and its test.
3. In `agents/glossary.md`, rename the entry to **Subagent message** without
   changing its definition.
4. Search `crates/` and `agents/glossary.md` for `AgentMessage`,
   `agent_message`, and `agent-message`. Only `AgentMessageChunk`,
   `agent_message_chunk`, and the fake server's comment may remain. Run the
   repository checks.
5. Mark OX-0007 fixed in `agents/issues.csv` and check its task in
   `agents/todo.md`.

## Documentation updates

- `agents/glossary.md` — rename the **Agent message** entry.
