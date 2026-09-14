import { describe, expect, test } from "bun:test";

import { SessionController } from "./session-controller.ts";

describe("session controller", () => {
  test("merges content chunks by message identity while preserving content blocks", () => {
    const controller = new SessionController("session-1");

    controller.accept({ content: { text: "hello ", type: "text" }, messageId: "agent-1", sessionUpdate: "agent_message_chunk" });
    controller.accept({ content: { text: "world", type: "text" }, messageId: "agent-1", sessionUpdate: "agent_message_chunk" });
    controller.accept({
      content: { mimeType: "image/png", data: "aGVsbG8=", type: "image", uri: "file:///image.png" },
      messageId: "agent-1",
      sessionUpdate: "agent_message_chunk",
    });
    controller.accept({ content: { text: "reasoning", type: "text" }, messageId: "thought-1", sessionUpdate: "agent_thought_chunk" });
    controller.accept({ content: { name: "notes", type: "resource_link", uri: "file:///notes.txt" }, messageId: "user-1", sessionUpdate: "user_message_chunk" });

    expect(controller.transcript.entries).toEqual([
      {
        content: [
          { text: "hello ", type: "text" },
          { text: "world", type: "text" },
          { data: "aGVsbG8=", mimeType: "image/png", type: "image", uri: "file:///image.png" },
        ],
        id: "message:agent:agent-1",
        kind: "agent",
      },
      { content: [{ text: "reasoning", type: "text" }], id: "message:thought:thought-1", kind: "thought" },
      {
        content: [{ name: "notes", type: "resource_link", uri: "file:///notes.txt" }],
        id: "message:user:user-1",
        kind: "user",
      },
    ]);
  });

  test("merges tool updates and replaces output with its final terminal outcome", () => {
    const controller = new SessionController("session-1");

    controller.accept({
      kind: "execute",
      name: "shell",
      sessionUpdate: "tool_call",
      status: "pending",
      title: "Run tests",
      toolCallId: "tool-1",
    });
    controller.accept({
      content: [{ content: { text: "running", type: "text" }, type: "content" }],
      sessionUpdate: "tool_call_update",
      status: "in_progress",
      toolCallId: "tool-1",
    });
    controller.accept({
      content: [{ content: { text: "tests passed", type: "text" }, type: "content" }],
      sessionUpdate: "tool_call_update",
      status: "completed",
      toolCallId: "tool-1",
    });

    expect(controller.transcript.entries).toEqual([
      {
        content: [{ content: { text: "tests passed", type: "text" }, type: "content" }],
        id: "tool:tool-1",
        kind: "tool",
        locations: [],
        name: "shell",
        status: "completed",
        title: "Run tests",
        toolKind: "execute",
      },
    ]);
  });

  test("replaces plans, usage, and configuration through live updates and load state", () => {
    const controller = new SessionController("session-1");
    controller.replaceConfiguration([
      { category: "mode", currentValue: "code", id: "mode", name: "Mode", options: [], type: "select" },
    ]);
    controller.accept({
      entries: [{ content: "Inspect", priority: "high", status: "in_progress" }],
      sessionUpdate: "plan",
    });
    controller.accept({
      entries: [{ content: "Finish", priority: "medium", status: "completed" }],
      sessionUpdate: "plan",
    });
    controller.accept({ cost: { amount: 0.2, currency: "USD" }, sessionUpdate: "usage_update", size: 100, used: 25 });
    controller.accept({
      configOptions: [{ currentValue: "plan", id: "mode", name: "Mode", options: [], type: "select" }],
      sessionUpdate: "config_option_update",
    });

    expect(controller.transcript).toEqual({
      configuration: [{ currentValue: "plan", id: "mode", name: "Mode", type: "select" }],
      entries: [],
      plan: [{ content: "Finish", priority: "medium", status: "completed" }],
      usage: { cost: { amount: 0.2, currency: "USD" }, size: 100, used: 25 },
    });
  });

  test("keeps unknown extensible values visible without exposing their payload", () => {
    const controller = new SessionController("session-1");

    controller.accept({ content: { type: "diagram", value: "hidden" }, messageId: "agent-1", sessionUpdate: "agent_message_chunk" });
    controller.accept({ sessionUpdate: "future_update", value: "hidden" });

    expect(controller.transcript.entries).toEqual([
      {
        content: [{ label: "Unknown content: diagram", type: "unknown" }],
        id: "message:agent:agent-1",
        kind: "agent",
      },
      { id: "unknown:1", kind: "unknown", label: "Unknown session update: future_update" },
    ]);
  });
});
