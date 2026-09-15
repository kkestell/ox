# Markdown transcript rendering

## Goal

Render model-authored text in the browser transcript as safe GitHub-flavored
Markdown while keeping user input, reasoning, and technical tool output literal.
The rendered surface must not execute agent-provided HTML or browser URL
schemes, load remote Markdown images, or let wide content expand the transcript
column.

## Related code

- `client/src/components/transcript.tsx` — Owns the present literal rendering of
  transcript content and will select Markdown only for agent text blocks.
- `client/src/components/transcript.test.tsx` — Existing server-rendered
  transcript component coverage.
- `client/package.json` and `client/bun.lock` — Client dependencies and locked
  package graph.
- `references/repos/personal/alpha/extension/webview/Markdown.tsx` — Established
  safe GFM renderer pattern, including single-newline, image, link, and table
  handling.

## Decisions

- Add a small dedicated Markdown component backed by `react-markdown`,
  `remark-gfm`, and `remark-breaks`; do not parse Markdown in the transcript
  reducer or browser protocol.
- Apply it only to `agent` text content. User prompts, reasoning, tool output,
  resource content, and unsupported values retain their current literal or
  native representations.
- Keep untrusted output inert: do not enable raw HTML; limit rendered links to
  the existing HTTP(S) policy; replace Markdown images with their alt text (or
  source URL when unlabelled); and make wide tables and code blocks scroll or
  wrap inside the message rather than widening the transcript.

## Test plan

- Server-render the Markdown component to prove GFM headings, lists, emphasis,
  strikethrough, tables, fenced code, and single-newline line breaks render as
  intended, including an incomplete streamed code fence.
- Prove raw HTML remains text, disallowed link schemes are inert, Markdown
  images do not produce image elements, and wide table/code containers retain
  their bounded presentation classes.
- Extend the transcript component test with agent Markdown and literal user,
  reasoning, and tool text to verify the rendering boundary.

## Implementation plan

- Add the Markdown rendering dependencies to the client manifest and regenerate
  Bun's lockfile.
- Create a browser-only Markdown component with the GFM and break plugins plus
  constrained element renderers for links, images, tables, and code-oriented
  layout.
- Route agent text blocks in `Transcript` through that component; retain the
  existing `Content` path for every other transcript kind and non-text content.
- Add focused Markdown and transcript rendering tests.
- Mark the completed markdown transcript task in `eng/todo.md` after the
  implementation and its focused checks pass.

## Documentation updates

- Update `eng/todo.md` when the task is complete.
