# ACP extensions

`docs/spec.md` owns Ox's product behavior; this page collects the `_meta`
extension keys in one place for client authors.

Ox speaks ACP v1 and is a strict superset of it. Custom methods start with an
underscore, and Ox defines none. Custom data travels only in ACP's reserved
`_meta` object under the `kkestell.ox/` namespace, and Ox never adds a
nonstandard field to the root of an ACP-defined type.

Every key is optional to a reader. Ox ignores `_meta` it does not recognize, and
only the exact values below change behavior, so an extension Ox adds later
cannot break an existing client.

| Key                                | Carried on                | Direction       | Value  |
| ---------------------------------- | ------------------------- | --------------- | ------ |
| `kkestell.ox/toolDisplayName`      | tool call                 | agent to client | string |
| `kkestell.ox/toolDisplayArguments` | tool call                 | agent to client | string |
| `kkestell.ox/outcome`              | agent message chunk       | agent to client | string |
| `kkestell.ox/blockIndex`           | user prompt content block | agent to client | number |
| `kkestell.ox/cacheHitRate`         | usage update              | agent to client | number |
| `kkestell.ox/sessionLocked`        | listed session            | agent to client | `true` |
| `kkestell.ox/messageId`            | prompt content block      | client to agent | string |

## Tool display

`kkestell.ox/toolDisplayName` and `kkestell.ox/toolDisplayArguments` appear in
the `_meta` of a tool call on `session/update`, in the tool call of a
`session/request_permission`, and in the tool calls replayed by `session/load`.
They carry a registered tool's human action and its bounded display subject, so
a client can render the action and, for example, a command line, path, or URL as
separate elements. The tool call's ACP `title` is those two values joined on one
line, which is what a client that reads only the title shows. The display name
is present whenever the metadata is, and the display arguments are omitted when
the call has no display subject. A provider-requested tool with no registered
presentation omits both.

## Turn outcome

`kkestell.ox/outcome` is the outcome of a turn that did not complete normally:
`failed`, `cancelled`, or `interrupted`. It appears in the `_meta` of the
`agent_message_chunk` that reports the outcome, whose text is the recorded
failure message or a fixed message for cancellation and interruption. A
completed or refused turn sends no such chunk. Live turns and `session/load`
replay report the same value.

## Replay block index

`kkestell.ox/blockIndex` is the zero-based position of a prompt's content block
within the original `session/prompt`. It appears in the `_meta` of each content
block of the `user_message_chunk` updates that `session/load` replays, where all
chunks share the prompt's `messageId`. Live prompts do not carry it.

## Cache hit rate

`kkestell.ox/cacheHitRate` is the share of the session's input tokens the
provider served from its prompt cache, as a number from 0 through 1. It appears
in the `_meta` of a `usage_update` on `session/update` and in the usage updates
that `session/load` replays. Unlike the update's `used`, which is the size of
the most recent provider request, the rate is cumulative across every request
the session has made, as its `cost` is. The key is absent until the provider
reports cached-token accounting, which distinguishes a model that reports none
from one whose requests have missed the cache.

## Advisory session lock

`kkestell.ox/sessionLocked` is `true` in the `_meta` of a `session/list` entry
when another runtime holds that session's activation lock. Ox omits the key
instead of sending `false`, including for a session that is active in the
listing runtime. The signal is advisory: ownership can change after listing, and
loading or deleting an active session still fails rather than racing its owner.

## Client-supplied message ID

`kkestell.ox/messageId` is an optional string in the `_meta` of the first
content block of a `session/prompt`. It becomes the durable identifier of that
user message, and replay returns it as the `messageId` of every
`user_message_chunk` for the prompt, so a client can match the message it showed
optimistically against replayed history. Ox rejects a non-string value as an
invalid request and generates an identifier when the key is absent. Sending the
key is optional; Ox's browser client does not send it.

## Ignored metadata

Ox decodes `_meta` on the requests it receives, including `initialize`,
`authenticate`, `session/new`, and MCP server definitions, so conforming clients
can send it. It interprets only the keys above and discards every other value;
none of them reach Ox's own messages, session records, or logs.
