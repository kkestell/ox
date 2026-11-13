# Render transcript messages as Markdown

- Task: `2026-11-12-016-transcript-markdown`
- Kind: plan
- Status: complete
- Source HEAD: `605310cc7b3bcc829fa9fe1d578705878ece54db`
- Dependencies: none

## Outcome and boundaries

Render user messages, assistant messages and expanded thinking as Markdown,
both while streaming and after replay. Preserve message ordering, collapsed
thinking, attachments, raw tool data, and conversation following. Use shared
layout rules and existing design tokens; do not redesign the transcript.

## Current system

`Conversation.tsx` renders prompt/text/thought strings in `p.text`, tool input
and output in `pre`, and tool headings through `CodeSpans`. `useConversation`
already combines chunks before rendering. Permission and elicitation panels
live in the composer. The E2E helpers select assistant messages by `p.text`.
The scripted fake agent supports text echo but cannot emit arbitrary multiline
chunks or thoughts. The app currently has no external-link opener.

## Design

Add a shared `Markdown` component using synchronous `react-markdown` with
`remark-gfm`. These libraries provide CommonMark plus tables, task lists,
strikethrough and autolinks without HTML injection. Render the accumulated
string each update; preserve source text in state and storage. Keep the
library's escaped HTML and safe URL transformation defaults. No custom parser,
HTML plugin, incremental parser, syntax highlighter or new message protocol.
Use normal Markdown soft breaks; explicit hard breaks remain available.

The shared wrapper owns block spacing, list indentation, wrapping and local
overflow for code/tables. Reuse existing typography and spacing tokens across
all three message kinds. Replace `.text`'s blanket `pre-wrap`; code retains
literal whitespace. Keep the existing message hooks where useful, and update
E2E selectors to allow a block wrapper instead of a paragraph.

Render HTTP(S) and mailto links as anchors whose clicks call Tauri's official
opener plugin, keeping the transcript in its app window. Register the plugin
and permit only supported URL opening. Unsupported relative/file links retain
their label without navigating the app. Do not broaden image/network CSP;
Markdown image alt text remains available where the app blocks the source.

Leave raw tool output and arguments literal: text blocks carry no Markdown
format marker and may contain shell output or code. Preserve `CodeSpans` for
compact tool and permission titles. Keep diagnostics, status text, filenames,
form labels and composer questions unchanged.

## Check your assumptions

All four entries accepted by the user.

1. **Markdown uses CommonMark plus GitHub extensions.** Add `react-markdown`
   and `remark-gfm`; standard soft line breaks collapse, replacing the current
   preservation of every newline. This gives tables, lists and fenced code;
   a smaller custom renderer or preserving plain-text line behavior is given up.
2. **Tool output stays literal.** Existing `pre` output preserves shell/code
   whitespace and punctuation; its protocol text blocks have no Markdown marker.
   Rendering it all as Markdown could change its meaning. JSON arguments,
   diagnostics, filenames and status text likewise remain literal.
3. **Compact titles and composer requests retain their current rendering.**
   Tool and permission titles already render code spans; form questions and
   labels remain unchanged. Full Markdown there would expand beyond transcript
   bodies and introduce different layout/interaction requirements.
4. **Web links open externally; file navigation and rich embeds are excluded.**
   Add the official Tauri opener dependency for HTTP(S)/mailto links. Relative
   paths and file links do not gain a file viewer; remote image permissions,
   HTML execution, syntax highlighting, math and Mermaid are not added.

## Implementation sequence

1. Add Markdown dependencies, shared component and necessary shared layout CSS;
   integrate prompt/text/thought in `Conversation.tsx`.
2. Add opener dependencies, registration and scoped capability in `crates/ox`;
   wire supported Markdown link activation and report errors with `showError`.
3. Extend the existing test fake agent with bounded JSON-string text/thought
   chunk directives; keep test behavior in that fixture. Add Markdown E2E cases
   and update existing message selectors and assertions.
4. Write the linked work record with changes, verification and limitations.

## Verification

- E2E: user emphasis; assistant headings, lists, code, quotes, GFM table/task
  list, link properties, escaped HTML and rejected unsafe URLs; thought
  formatting; delimiters split across chunks; replay; raw tool punctuation.
- Prove the new rendering regression fails with the renderer integration
  removed, then restore it. Use existing UI/ACP paths, without test-only app APIs.
- Inspect screenshots of all three message kinds, long code/table overflow and
  adjacent tool rows for consistent layout and shared spacing.
- Run once at completion: `make check-ui`, `make check-rust`, `make e2e`,
  `make smoke-claude` (fake-agent edits are under `oxd`). Serialize Cargo builds.
- Library contracts: [react-markdown][markdown], [GFM][gfm], [Tauri opener][opener].

[markdown]: https://github.com/remarkjs/react-markdown
[gfm]: https://github.com/remarkjs/remark-gfm
[opener]: https://v2.tauri.app/plugin/opener/
