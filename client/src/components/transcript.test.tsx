import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { type SessionTranscript } from "../protocol.ts";

import { Transcript } from "./transcript.tsx";

test("wraps unbroken content in every visible transcript variant", () => {
  const unbroken = "unbroken".repeat(40);
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [
      {
        content: [
          {
            description: unbroken,
            name: unbroken,
            type: "resource_link",
            uri: "https://example.test/resource",
          },
          { label: unbroken, type: "unknown" },
        ],
        id: "agent-1",
        kind: "agent",
      },
    ],
    plan: [{ content: unbroken, priority: "medium", status: "pending" }],
  };

  const html = renderToStaticMarkup(<Transcript transcript={transcript} />);

  expect(html).toContain(`<p class="break-words [overflow-wrap:anywhere]"><a`);
  expect(html).toContain(`<p class="break-words [overflow-wrap:anywhere]">${unbroken}</p>`);
  expect(html).toContain(`<span class="min-w-0 break-words [overflow-wrap:anywhere] text-foreground">${unbroken}</span>`);
});

test("renders reasoning without a background surface", () => {
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [
      {
        content: [{ text: "reasoning", type: "text" }],
        id: "thought-1",
        kind: "thought",
      },
    ],
    plan: [],
  };

  const html = renderToStaticMarkup(<Transcript transcript={transcript} />);

  expect(html).toContain('class="rounded-lg"');
  expect(html).not.toContain("bg-muted/35");
});

test("opens only the live reasoning stream", () => {
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [
      {
        content: [{ text: "reasoning", type: "text" }],
        id: "thought-1",
        kind: "thought",
      },
    ],
    openReasoningID: "thought-1",
    plan: [],
  };

  const html = renderToStaticMarkup(<Transcript transcript={transcript} />);

  expect(html).toContain('aria-label="Reasoning content"');
  expect(html).toContain("reasoning");
});

test("renders structured ACP tool presentation with a code-styled argument", () => {
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [
      {
        content: [],
        id: "tool-1",
        kind: "tool",
        locations: [],
        name: "mcp__browser__inspect_responsive_layout",
        status: "pending",
        arguments: "client/src/components/transcript.tsx",
        title: "Inspect",
        toolKind: "other",
      },
      {
        content: [],
        id: "tool-2",
        kind: "tool",
        locations: [],
        name: "unknown",
        status: "pending",
        title: "mcp__unknown__raw_fallback",
      },
    ],
    plan: [],
  };

  const html = renderToStaticMarkup(<Transcript transcript={transcript} />);

  expect(html).toContain("Inspect</span><code");
  expect(html).toContain("client/src/components/transcript.tsx</code>");
  expect(html).toContain("mcp__unknown__raw_fallback");
  expect(html).not.toContain("Unknown raw fallback");
  expect(html).not.toContain("Tool name");
  expect(html).not.toContain("Tool kind");
});

test("renders message and reasoning text as Markdown", () => {
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [
      { content: [{ text: "## Agent", type: "text" }], id: "agent-1", kind: "agent" },
      { content: [{ text: "## User", type: "text" }], id: "user-1", kind: "user" },
      { content: [{ text: "## Reasoning", type: "text" }], id: "thought-1", kind: "thought" },
    ],
    openReasoningID: "thought-1",
    plan: [],
  };

  const html = renderToStaticMarkup(<Transcript transcript={transcript} />);

  expect(html).toContain("<h2>Agent</h2>");
  expect(html).toContain("<h2>User</h2>");
  expect(html).toContain("<h2>Reasoning</h2>");
});
