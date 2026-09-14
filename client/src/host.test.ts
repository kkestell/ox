import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { startHost, type StartedHost } from "./host.ts";

let host: StartedHost | undefined;
const temporaryDirectories: string[] = [];

afterEach(async () => {
  await host?.stop();
  host = undefined;
  await Promise.all(temporaryDirectories.splice(0).map((path) => rm(path, { force: true, recursive: true })));
});

describe("browser host", () => {
  test("serves health without a browser connection", async () => {
    host = await startTestHost();

    const response = await fetch(`${host.url}/health`);

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ status: "ok" });
  });

  test("fails authentication commands when no workspace is configured", async () => {
    host = await startTestHost();
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

  test("registers the first workspace without returning its root", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "private-workspace");
    await mkdir(workspace);
    host = await startHost({
      oxCommand: "/definitely/not/ox",
      registryPath: join(root, "configuration", "workspaces.json"),
    });
    const socket = connect(`${host.url.replace(/^http/, "ws")}/socket`, host.url);
    await new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", reject, { once: true });
    });
    const messages: unknown[] = [];
    const completed = new Promise<void>((resolve) => {
      socket.addEventListener("message", (event) => {
        const message = JSON.parse(String(event.data));
        messages.push(message);
        if (message.type === "result" && message.requestId === "request-1") {
          resolve();
        }
      });
    });

    socket.send(JSON.stringify({ type: "register-workspace", requestId: "request-1", path: workspace }));
    await completed;

    expect(JSON.stringify(messages)).not.toContain(workspace);
    const snapshot = messages.findLast((message) => (message as { type?: string }).type === "snapshot") as {
      workspaces: { values: { id: string; name: string }[] };
    };
    expect(snapshot.workspaces.values).toEqual([{ id: expect.any(String), name: "private-workspace" }]);
    socket.close();
  });

  test("rejects a cross-origin WebSocket upgrade", async () => {
    host = await startTestHost();

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

async function startTestHost(): Promise<StartedHost> {
  const root = await temporaryDirectory();
  return startHost({ registryPath: join(root, "workspaces.json") });
}

async function temporaryDirectory(): Promise<string> {
  const path = await mkdtemp(join(tmpdir(), "ox-browser-host-"));
  temporaryDirectories.push(path);
  return path;
}

// Bun's WebSocket client accepts request headers, which the browser lib's
// constructor type does not describe.
function connect(url: string, origin: string): WebSocket {
  const client = WebSocket as unknown as new (
    url: string,
    options: { headers: Record<string, string> },
  ) => WebSocket;
  return new client(url, { headers: { Origin: origin } });
}
