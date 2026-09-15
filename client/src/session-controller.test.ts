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
          { text: "hello world", type: "text" },
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

  test("opens a live reasoning stream and closes it at the next transcript item", () => {
    const controller = new SessionController("session-1");

    controller.accept({ content: { text: "thinking", type: "text" }, messageId: "thought-1", sessionUpdate: "agent_thought_chunk" });
    expect(controller.transcript.openReasoningID).toBe("message:thought:thought-1");

    controller.accept({ content: { text: "answer", type: "text" }, messageId: "agent-1", sessionUpdate: "agent_message_chunk" });
    expect(controller.transcript.openReasoningID).toBeUndefined();
  });

  test("keeps replayed reasoning closed", () => {
    const controller = new SessionController("session-1", true);

    controller.accept({ content: { text: "earlier thinking", type: "text" }, messageId: "thought-1", sessionUpdate: "agent_thought_chunk" });

    expect(controller.transcript.openReasoningID).toBeUndefined();

    controller.beginLiveUpdates();
    controller.accept({ content: { text: "new thinking", type: "text" }, messageId: "thought-2", sessionUpdate: "agent_thought_chunk" });
    expect(controller.transcript.openReasoningID).toBe("message:thought:thought-2");
  });

  test("merges tool updates and replaces output with its final terminal outcome", () => {
    const controller = new SessionController("session-1");

    controller.accept({
      kind: "execute",
      name: "shell",
      sessionUpdate: "tool_call",
      status: "pending",
      _meta: {
        "kkestell.ox/toolDisplayArguments": "go test ./...",
        "kkestell.ox/toolDisplayName": "Run",
      },
      title: "Run",
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
        arguments: "go test ./...",
        id: "tool:tool-1",
        kind: "tool",
        locations: [],
        name: "shell",
        status: "completed",
        title: "Run",
        toolKind: "execute",
      },
    ]);
  });

  test("replaces plans, usage, and configuration through live updates and load state", () => {
    const controller = new SessionController("session-1");
    controller.replaceConfiguration([
      { category: "mode", currentValue: "code", id: "mode", name: "Mode", options: [{ name: "Code", value: "code" }], type: "select" },
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
      configOptions: [{ currentValue: "plan", id: "mode", name: "Mode", options: [{ name: "Plan", value: "plan" }], type: "select" }],
      sessionUpdate: "config_option_update",
    });

    expect(controller.transcript).toEqual({
      configuration: [{ currentValue: "plan", id: "mode", name: "Mode", options: [{ name: "Plan", value: "plan" }], type: "select" }],
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

  test("retains pending interactions until one valid browser answer settles them", async () => {
    const controller = new SessionController("session-1");
    const signal = new AbortController();
    let changed = 0;
    const permission = controller.requestPermission(
      {
        options: [
          { kind: "allow_once", name: "Allow once", optionId: "once" },
          { kind: "reject_once", name: "Reject", optionId: "reject" },
        ],
        sessionId: "session-1",
        toolCall: {
          _meta: {
            "kkestell.ox/toolDisplayArguments": "go test ./...",
            "kkestell.ox/toolDisplayName": "Run",
          },
          kind: "execute",
          name: "shell",
          title: "Run",
          toolCallId: "tool-1",
        },
      },
      signal.signal,
      () => changed++,
    );

    expect(controller.interactions).toEqual([
      {
        id: "interaction-1",
        kind: "permission",
        options: [
          { id: "once", kind: "allow_once", name: "Allow once" },
          { id: "reject", kind: "reject_once", name: "Reject" },
        ],
        tool: { arguments: "go test ./...", id: "tool-1", name: "shell", title: "Run", toolKind: "execute" },
      },
    ]);
    expect(() => controller.resolvePermission("interaction-1", "missing")).toThrow("not available");
    controller.resolvePermission("interaction-1", "once");
    await expect(permission).resolves.toEqual({ outcome: { optionId: "once", outcome: "selected" } });
    expect(controller.interactions).toEqual([]);
    expect(() => controller.resolvePermission("interaction-1", "once")).toThrow("no longer pending");
    expect(changed).toBe(2);
  });

  test("projects and validates a form, and cancellation wins no later than the callback abort", async () => {
    const controller = new SessionController("session-1");
    const abort = new AbortController();
    const form = controller.requestElicitation(
      {
        message: "Choose a color",
        mode: "form",
        requestedSchema: {
          properties: {
            answer: { oneOf: [{ const: "red", title: "Red" }, { const: "blue", title: "Blue" }], title: "Color", type: "string" },
          },
          required: ["answer"],
          type: "object",
        },
        sessionId: "session-1",
        toolCallId: "question-1",
      },
      abort.signal,
      () => {},
    );

    expect(controller.interactions[0]).toMatchObject({
      fields: [{ choices: [{ label: "Red", value: "red" }, { label: "Blue", value: "blue" }], label: "Color", name: "answer", required: true, type: "string" }],
      kind: "form",
      message: "Choose a color",
    });
    expect(() => controller.resolveElicitation("interaction-1", "accept", { answer: "green" })).toThrow("invalid");
    abort.abort();
    await expect(form).resolves.toEqual({ action: "cancel" });
    expect(controller.interactions).toEqual([]);
  });

  test("cancels forms that cannot be faithfully projected and rejects invalid calendar dates", async () => {
    const controller = new SessionController("session-1");
    const signal = new AbortController();
    const unsupported = controller.requestElicitation(
      {
        message: "Pick one",
        mode: "form",
        requestedSchema: {
          properties: {
            answer: { default: "green", enum: ["red", "blue"], type: "string" },
          },
          type: "object",
        },
        sessionId: "session-1",
      },
      signal.signal,
      () => {},
    );
    await expect(unsupported).resolves.toEqual({ action: "cancel" });
    expect(controller.interactions).toEqual([]);

    const form = controller.requestElicitation(
      {
        message: "When?",
        mode: "form",
        requestedSchema: {
          properties: { date: { format: "date", type: "string" } },
          required: ["date"],
          type: "object",
        },
        sessionId: "session-1",
      },
      signal.signal,
      () => {},
    );
    expect(() => controller.resolveElicitation("interaction-2", "accept", { date: "2026-02-29" })).toThrow("invalid");
    controller.resolveElicitation("interaction-2", "accept", { date: "2028-02-29" });
    await expect(form).resolves.toEqual({ action: "accept", content: { date: "2028-02-29" } });
  });
});
