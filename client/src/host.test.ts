import { afterEach, describe, expect, test } from "bun:test";

import { startHost, type StartedHost } from "./host.ts";

let host: StartedHost | undefined;

afterEach(() => {
  host?.stop();
  host = undefined;
});

describe("browser host", () => {
  test("serves health without a browser connection", async () => {
    host = startHost();

    const response = await fetch(`${host.url}/health`);

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ status: "ok" });
  });

  test("rejects a cross-origin WebSocket upgrade", async () => {
    host = startHost();

    const response = await fetch(`${host.url}/socket`, {
      headers: {
        Connection: "Upgrade",
        Origin: "http://untrusted.example",
        Upgrade: "websocket",
      },
    });

    expect(response.status).toBe(403);
  });
});
