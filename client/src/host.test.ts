import { afterEach, describe, expect, test } from "bun:test";

import { startHost, type StartedHost } from "./host.ts";

let host: StartedHost | undefined;

afterEach(async () => {
  await host?.stop();
  host = undefined;
});

describe("browser host", () => {
  test("serves health without a browser connection", async () => {
    host = await startHost();

    const response = await fetch(`${host.url}/health`);

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ status: "ok" });
  });

  test("fails authentication commands when no workspace is configured", async () => {
    host = await startHost();
    const socket = connect(`${host.url.replace(/^http/, "ws")}/socket`, host.url);
    await new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", reject, { once: true });
    });
    const result = new Promise<unknown>((resolve) => {
      socket.addEventListener("message", (event) => {
        const message = JSON.parse(String(event.data));
        if (message.type === "result") {
          resolve(message);
        }
      });
    });

    socket.send(JSON.stringify({ type: "logout", requestId: "request-1" }));

    expect(await result).toEqual({
      type: "result",
      requestId: "request-1",
      ok: false,
      error: "workspace is not configured",
    });
    socket.close();
  });

  test("rejects a cross-origin WebSocket upgrade", async () => {
    host = await startHost();

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

// Bun's WebSocket client accepts request headers, which the browser lib's
// constructor type does not describe.
function connect(url: string, origin: string): WebSocket {
  const client = WebSocket as unknown as new (
    url: string,
    options: { headers: Record<string, string> },
  ) => WebSocket;
  return new client(url, { headers: { Origin: origin } });
}
