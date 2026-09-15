import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { type SessionTranscript } from "../protocol.ts";

import { SessionInformation } from "./session-information.tsx";

test("summarizes context usage behind an icon", () => {
  const transcript: SessionTranscript = {
    configuration: [],
    entries: [],
    plan: [],
    usage: {
      cost: { amount: 0.01, currency: "USD" },
      size: 1_050_000,
      used: 51_510,
    },
  };

  const html = renderToStaticMarkup(<SessionInformation onConfigOption={() => {}} transcript={transcript} />);

  expect(html).toContain('aria-label="Show usage: 51,510 / 1,050,000 tokens, $0.01"');
  expect(html).not.toContain("51,510 / 1,050,000 · $0.01");
});
