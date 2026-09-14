import { describe, expect, test } from "bun:test";

import { initialSnapshot, parseBrowserCommand } from "./protocol.ts";

describe("browser protocol", () => {
  test("accepts a correlated ping", () => {
    expect(parseBrowserCommand({ type: "ping", requestId: "request-1" })).toEqual({
      ok: true,
      value: { type: "ping", requestId: "request-1" },
    });
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
      { type: "new-session", requestId: "request-1" },
      { type: "refresh-sessions", requestId: "request-2" },
      { type: "next-session-page", requestId: "request-3" },
      { type: "load-session", requestId: "request-4", sessionId: "session-1" },
      { type: "resume-session", requestId: "request-5", sessionId: "session-1" },
      { type: "close-session", requestId: "request-6", sessionId: "session-1" },
      { type: "delete-session", requestId: "request-7", sessionId: "session-1" },
    ]) {
      expect(parseBrowserCommand(command).ok).toBe(true);
    }
  });

  test.each([
    undefined,
    {},
    { type: "ping" },
    { type: "ping", requestId: "" },
    { type: "ping", requestId: "request-1", extra: true },
    { type: "authenticate", requestId: "request-1" },
    { type: "login", requestId: "request-1", methodId: "terminal", credential: "   " },
    { type: "logout", requestId: "" },
    { type: "new-session" },
    { type: "load-session", requestId: "request-1", sessionId: "" },
  ])("rejects invalid commands: %#j", (command) => {
    expect(parseBrowserCommand(command)).toEqual({
      ok: false,
      error: "invalid browser command",
    });
  });

  test("starts with a complete ready snapshot", () => {
    expect(initialSnapshot({ diagnostics: [], status: "ready" })).toEqual({
      type: "snapshot",
      revision: 0,
      connection: { status: "ready" },
      workspace: { diagnostics: [], status: "ready" },
      authentication: { logoutAvailable: false, methods: [], status: "unavailable" },
      sessions: { values: [] },
    });
  });
});
