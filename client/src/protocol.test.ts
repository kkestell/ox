import { describe, expect, test } from "bun:test";

import { initialSnapshot, parseBrowserCommand, snapshotSchema } from "./protocol.ts";

const noPromptCapabilities = { audio: false, embeddedContext: false, image: false };
const workspaceID = "11111111-1111-4111-8111-111111111111";
const secondWorkspaceID = "22222222-2222-4222-8222-222222222222";
const registeredWorkspaces = { selectedId: workspaceID, values: [{ id: workspaceID, name: "ox" }] };

describe("browser protocol", () => {
  test("accepts a correlated ping", () => {
    expect(parseBrowserCommand({ type: "ping", requestId: "request-1" })).toEqual({
      ok: true,
      value: { type: "ping", requestId: "request-1" },
    });
  });

  test("accepts bounded workspace registry commands", () => {
    for (const command of [
      { type: "register-workspace", requestId: "request-1", path: "/srv/workspace" },
      { type: "select-workspace", requestId: "request-2", workspaceId: workspaceID },
      { type: "remove-workspace", requestId: "request-3", workspaceId: workspaceID },
    ]) {
      expect(parseBrowserCommand(command).ok).toBe(true);
    }
  });

  test("accepts write-only authentication commands", () => {
    expect(parseBrowserCommand({ type: "authenticate", requestId: "request-1", methodId: "stored" })).toEqual({
      ok: true,
      value: { type: "authenticate", requestId: "request-1", methodId: "stored" },
    });
    expect(parseBrowserCommand({ type: "login", requestId: "request-2", methodId: "terminal", credential: " key " })).toEqual({
      ok: true,
      value: { type: "login", requestId: "request-2", methodId: "terminal", credential: "key" },
    });
    expect(parseBrowserCommand({ type: "logout", requestId: "request-3" })).toEqual({
      ok: true,
      value: { type: "logout", requestId: "request-3" },
    });
  });

  test("accepts bounded session lifecycle commands", () => {
    for (const command of [
      { type: "new-conversation", requestId: "request-1" },
      { type: "refresh-history", requestId: "request-2" },
      { type: "next-history-page", requestId: "request-3" },
      { type: "open-conversation", requestId: "request-4", sessionId: "session-1" },
      { type: "close-conversation", requestId: "request-5", sessionId: "session-1" },
      { type: "delete-conversation", requestId: "request-6", sessionId: "session-1" },
    ]) {
      expect(parseBrowserCommand(command).ok).toBe(true);
    }
  });

  test("accepts bounded HTTP and stdio MCP activation definitions", () => {
    expect(
      parseBrowserCommand({
        type: "set-mcp-servers",
        requestId: "request-1",
        mcpServers: [
          {
            transport: "http",
            name: "remote",
            url: "https://example.test/mcp",
            headers: [{ name: "Authorization", value: "browser-mcp-secret" }],
          },
          {
            transport: "stdio",
            name: "local",
            command: "/usr/local/bin/mcp",
            args: ["--serve"],
            env: [{ name: "MCP_TOKEN", value: "stdio-mcp-secret" }],
          },
        ],
      }),
    ).toEqual({
      ok: true,
      value: {
        type: "set-mcp-servers",
        requestId: "request-1",
        mcpServers: [
          {
            transport: "http",
            name: "remote",
            url: "https://example.test/mcp",
            headers: [{ name: "Authorization", value: "browser-mcp-secret" }],
          },
          {
            transport: "stdio",
            name: "local",
            command: "/usr/local/bin/mcp",
            args: ["--serve"],
            env: [{ name: "MCP_TOKEN", value: "stdio-mcp-secret" }],
          },
        ],
      },
    });
  });

  test("rejects MCP definitions Ox cannot activate", () => {
    for (const mcpServers of [
      [{ transport: "http", name: "remote", url: "http://example.test/mcp", headers: [] }],
      [{ transport: "http", name: "remote", url: "https://user:secret@example.test/mcp", headers: [] }],
      [{ transport: "http", name: "remote", url: "https://example.test/mcp", headers: [{ name: "Bad Header", value: "value" }] }],
      [{ transport: "http", name: "remote", url: "https://example.test/mcp", headers: [{ name: "Accept", value: "one" }, { name: "accept", value: "two" }] }],
      [{ transport: "stdio", name: "local", command: "mcp", args: [], env: [] }],
      [{ transport: "stdio", name: "local", command: "/bin/mcp", args: [], env: [{ name: "MCP=TOKEN", value: "secret" }] }],
      [{ transport: "stdio", name: "local", command: "/bin/mcp", args: [], env: [{ name: "MCP_TOKEN", value: "one" }, { name: "MCP_TOKEN", value: "two" }] }],
      [
        { transport: "http", name: "same", url: "https://one.example.test/mcp", headers: [] },
        { transport: "stdio", name: "same", command: "/bin/mcp", args: [], env: [] },
      ],
    ]) {
      expect(parseBrowserCommand({ type: "set-mcp-servers", requestId: "request-1", mcpServers }).ok).toBe(false);
    }
  });

  test("accepts prompting, cancellation, and configuration commands", () => {
    for (const command of [
      { type: "cancel-prompt", requestId: "request-2", sessionId: "session-1" },
      { type: "set-config-option", requestId: "request-3", sessionId: "session-1", configId: "mode", value: "plan" },
      {
        type: "prompt",
        requestId: "request-4",
        sessionId: "session-1",
        prompt: [
          { type: "text", text: "hello" },
          { type: "image", data: "aGVsbG8=", mimeType: "image/png", uri: "attachment://image" },
          { type: "audio", data: "aGVsbG8=", mimeType: "audio/wav" },
          { type: "resource_link", name: "guide", uri: "https://example.test/guide" },
          { type: "resource", resource: { text: "notes", mimeType: "text/plain", uri: "attachment://notes" } },
          { type: "resource", resource: { blob: "aGVsbG8=", mimeType: "application/octet-stream", uri: "attachment://blob" } },
        ],
      },
    ]) {
      expect(parseBrowserCommand(command).ok).toBe(true);
    }
  });

  test("accepts correlated interaction answers and keeps their values bounded", () => {
    expect(parseBrowserCommand({ type: "resolve-permission", requestId: "request-1", sessionId: "session-1", interactionId: "interaction-1", optionId: "allow" }).ok).toBe(true);
    expect(parseBrowserCommand({ type: "resolve-elicitation", requestId: "request-2", sessionId: "session-1", interactionId: "interaction-1", action: "accept", content: { answer: "yes", count: 2, enabled: true, tags: ["one"] } }).ok).toBe(true);
    expect(parseBrowserCommand({ type: "resolve-elicitation", requestId: "request-3", sessionId: "session-1", interactionId: "interaction-1", action: "cancel", content: { extra: "must not be needed" } }).ok).toBe(true);
  });

  test.each([
    undefined,
    {},
    { type: "ping" },
    { type: "ping", requestId: "" },
    { type: "ping", requestId: "request-1", extra: true },
    { type: "register-workspace", requestId: "request-1", path: "relative" },
    { type: "register-workspace", requestId: "request-1", path: "/valid", root: "/leaked" },
    { type: "select-workspace", requestId: "request-1", workspaceId: "not-a-uuid" },
    { type: "remove-workspace", requestId: "request-1", workspaceId: workspaceID, root: "/leaked" },
    { type: "authenticate", requestId: "request-1" },
    { type: "login", requestId: "request-1", methodId: "terminal", credential: "   " },
    { type: "logout", requestId: "" },
    { type: "new-conversation" },
    { type: "set-mcp-servers", requestId: "request-1", mcpServers: [{ transport: "http", name: "remote", url: "not a URL", headers: [] }] },
    { type: "set-mcp-servers", requestId: "request-1", mcpServers: [{ transport: "sse", name: "remote", url: "https://example.test/mcp", headers: [] }] },
    { type: "open-conversation", requestId: "request-1", sessionId: "" },
    { type: "prompt", requestId: "request-1", sessionId: "session-1", prompt: [] },
    { type: "prompt", requestId: "request-1", sessionId: "session-1", prompt: [{ type: "resource", resource: { uri: "attachment://missing" } }] },
    { type: "set-config-option", requestId: "request-1", sessionId: "session-1", configId: "mode", value: "" },
    { type: "resolve-permission", requestId: "request-1", sessionId: "session-1", interactionId: "", optionId: "allow" },
    { type: "resolve-elicitation", requestId: "request-1", sessionId: "session-1", interactionId: "interaction-1", action: "accept", content: { bad: { nested: true } } },
  ])("rejects invalid commands: %#j", (command) => {
    expect(parseBrowserCommand(command)).toEqual({
      ok: false,
      error: "invalid browser command",
    });
  });

  test("starts with a complete ready snapshot", () => {
    expect(initialSnapshot(registeredWorkspaces, { diagnostics: [], mcpServerCount: 0, name: "ox", promptCapabilities: noPromptCapabilities, status: "ready" })).toEqual({
      type: "snapshot",
      revision: 0,
      workspaces: registeredWorkspaces,
      workspace: { diagnostics: [], mcpServerCount: 0, name: "ox", promptCapabilities: noPromptCapabilities, status: "ready" },
      authentication: { logoutAvailable: false, methods: [], status: "unavailable" },
      sessions: { values: [] },
    });
  });

  test("accepts a browser-safe active transcript and rejects arbitrary payloads", () => {
    const snapshot = initialSnapshot(registeredWorkspaces, { diagnostics: [], mcpServerCount: 0, name: "ox", promptCapabilities: noPromptCapabilities, status: "ready" });
    snapshot.sessions.active = {
      busy: false,
      id: "session-1",
      interactions: [],
      transcript: {
        configuration: [],
        entries: [{ content: [{ text: "hello", type: "text" }], id: "agent-1", kind: "agent" }],
        plan: [],
      },
    };
    expect(snapshotSchema.safeParse(snapshot).success).toBe(true);

    snapshot.sessions.active = {
      busy: false,
      id: "session-1",
      interactions: [],
      transcript: {
        configuration: [],
        entries: [{ id: "unknown-1", kind: "unknown", label: "future", payload: "must not cross the boundary" }],
        plan: [],
      },
    } as unknown as typeof snapshot.sessions.active;
    expect(snapshotSchema.safeParse(snapshot).success).toBe(false);

    snapshot.sessions.active = {
      busy: false,
      id: "session-1",
      interactions: [],
      transcript: {
        configuration: [],
        entries: [
          {
            content: [{ type: "resource", uri: "file:///missing-content" }],
            id: "agent-1",
            kind: "agent",
          },
        ],
        plan: [],
      },
    } as unknown as typeof snapshot.sessions.active;
    expect(snapshotSchema.safeParse(snapshot).success).toBe(false);
  });

  test("accepts only browser-safe workspace catalogs", () => {
    expect(snapshotSchema.safeParse(initialSnapshot({ values: [] })).success).toBe(true);

    for (const workspaces of [
      { selectedId: workspaceID, values: [] },
      { values: [{ id: workspaceID, name: "ox" }] },
      { selectedId: secondWorkspaceID, values: [{ id: workspaceID, name: "ox" }] },
      { selectedId: workspaceID, values: [{ id: workspaceID, name: "one" }, { id: workspaceID, name: "two" }] },
    ]) {
      expect(snapshotSchema.safeParse(initialSnapshot(workspaces)).success).toBe(false);
    }

    const root = initialSnapshot(registeredWorkspaces, {
      diagnostics: [],
      mcpServerCount: 0,
      name: "ox",
      promptCapabilities: noPromptCapabilities,
      status: "ready",
    });
    root.workspaces.values = [{ id: workspaceID, name: "ox", root: "/srv/ox" } as unknown as { id: string; name: string }];
    expect(snapshotSchema.safeParse(root).success).toBe(false);

    const arbitrary = initialSnapshot(registeredWorkspaces);
    arbitrary.workspaces = { ...arbitrary.workspaces, extra: true } as unknown as typeof arbitrary.workspaces;
    expect(snapshotSchema.safeParse(arbitrary).success).toBe(false);
  });
});
