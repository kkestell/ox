import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { type PendingInteraction } from "../protocol.ts";

import { PendingInteractions } from "./pending-interactions.tsx";

test("stacks every unresolved interaction in its labelled tray", () => {
  const interactions: PendingInteraction[] = [
    {
      id: "permission-1",
      kind: "permission",
      options: [{ id: "allow", kind: "allow_once", name: "Allow once" }],
      tool: { id: "tool-1", name: "shell", title: "Run tests", toolKind: "execute" },
    },
    {
      fields: [{ choices: [{ label: "Red", value: "red" }], label: "Answer", name: "answer", required: true, type: "string" }],
      id: "form-1",
      kind: "form",
      message: "Choose a color",
      title: "Choose a color",
    },
  ];

  const html = renderToStaticMarkup(
    <PendingInteractions interactions={interactions} onElicitation={() => {}} onPermission={() => {}} />,
  );

  expect(html).toContain('aria-label="Pending interactions"');
  expect(html.match(/class="pending-interaction /g)).toHaveLength(2);
  expect(html.indexOf("Permission for Run tests")).toBeLessThan(html.indexOf("Choose a color"));
  expect(html).toContain("Allow once");
  expect(html).toContain("Submit answer");
});
