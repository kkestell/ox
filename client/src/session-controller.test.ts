import { describe, expect, test } from "bun:test";
import type * as acp from "@agentclientprotocol/sdk";

import { SessionController } from "./session-controller.ts";

describe("session controller", () => {
  test("retains updates routed before a load response", () => {
    const controller = new SessionController("session-1");
    const update: acp.SessionUpdate = {
      content: { text: "replayed", type: "text" },
      sessionUpdate: "agent_message_chunk",
    };

    controller.accept(update);

    expect(controller.updates).toEqual([update]);
  });
});
