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

  expect(html).toContain('class="rounded-lg py-1.5"');
  expect(html).not.toContain("bg-muted/35");
});

test("renders ACP tool titles unchanged", () => {
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
        title: "Inspect the responsive layout",
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

  expect(html).toContain("Inspect the responsive layout");
  expect(html).toContain("mcp__unknown__raw_fallback");
  expect(html).not.toContain("Unknown raw fallback");
});
