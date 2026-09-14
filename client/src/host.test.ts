import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { startHost, type StartedHost } from "./host.ts";
import type { Snapshot } from "./protocol.ts";

let host: StartedHost | undefined;
const temporaryDirectories: string[] = [];

afterEach(async () => {
  await host?.stop();
  host = undefined;
  await Promise.all(temporaryDirectories.splice(0).map((path) => rm(path, { force: true, recursive: true })));
});

const unregisteredWorkspaceID = "11111111-1111-4111-8111-111111111111";

const httpMCPServer = {
  transport: "http",
  name: "remote",
  url: "https://example.test/mcp",
  headers: [],
};

describe("browser host", () => {
  test("serves health without a browser connection", async () => {
    host = await startTestHost();

    const response = await fetch(`${host.url}/health`);

    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ status: "ok" });
  });

  test("refuses a command that names a workspace with no supervisor", async () => {
    host = await startTestHost();
    const client = await connect(host);

    expect(await client.request({ type: "logout", workspaceId: unregisteredWorkspaceID })).toEqual({
      ok: false,
      error: "workspace is not active",
    });
    client.close();
  });

  test("registers the first workspace without returning its root", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "private-workspace");
    await mkdir(workspace);
    host = await startHost({
      oxCommand: "/definitely/not/ox",
      registryPath: join(root, "configuration", "workspaces.json"),
    });
    const client = await connect(host);

    expect(await client.request({ type: "register-workspace", path: workspace })).toEqual({ ok: true });

    expect(client.received()).not.toContain(workspace);
    expect(client.snapshot().workspaces.values).toEqual([
      { busy: false, id: expect.any(String), name: "private-workspace", status: "unavailable" },
    ]);
    client.close();
  });

  test("supervises every registered workspace and routes a command to the one it names", async () => {
    const root = await temporaryDirectory();
    await mkdir(join(root, "one"));
    await mkdir(join(root, "two"));
    host = await startHost({ oxCommand: "/definitely/not/ox", registryPath: join(root, "workspaces.json") });
    const client = await connect(host);
    await client.request({ type: "register-workspace", path: join(root, "one") });
    await client.request({ type: "register-workspace", path: join(root, "two") });
    const [one, two] = client.snapshot().workspaces.values;
    if (!one || !two) {
      throw new Error("both workspaces should be registered");
    }

    expect(
      await client.request({ type: "set-mcp-servers", workspaceId: two.id, mcpServers: [httpMCPServer] }),
    ).toEqual({ ok: true });

    await client.request({ type: "select-workspace", workspaceId: two.id });
    expect(client.snapshot().workspace?.mcpServerCount).toBe(1);
    await client.request({ type: "select-workspace", workspaceId: one.id });
    expect(client.snapshot().workspace?.mcpServerCount).toBe(0);
    client.close();
  });

  test("keeps a removed workspace's failure out of the remaining workspace", async () => {
    const root = await temporaryDirectory();
    await mkdir(join(root, "kept"));
    await mkdir(join(root, "removed"));
    host = await startHost({ oxCommand: "/definitely/not/ox", registryPath: join(root, "workspaces.json") });
    const client = await connect(host);
    await client.request({ type: "register-workspace", path: join(root, "kept") });
    await client.request({ type: "register-workspace", path: join(root, "removed") });
    const [kept, removed] = client.snapshot().workspaces.values;
    if (!kept || !removed) {
      throw new Error("both workspaces should be registered");
    }

    expect(await client.request({ type: "remove-workspace", workspaceId: removed.id })).toEqual({ ok: true });

    expect(await client.request({ type: "set-mcp-servers", workspaceId: removed.id, mcpServers: [] })).toEqual({
      ok: false,
      error: "workspace is not active",
    });
    expect(await client.request({ type: "set-mcp-servers", workspaceId: kept.id, mcpServers: [] })).toEqual({
      ok: true,
    });
    expect(client.snapshot().workspaces.values.map((workspace) => workspace.id)).toEqual([kept.id]);
    client.close();
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

type Client = {
  close(): void;
  received(): string;
  request(command: Record<string, unknown>): Promise<{ ok: boolean; error?: string }>;
  snapshot(): Snapshot;
};

async function connect(started: StartedHost): Promise<Client> {
  const socket = open(`${started.url.replace(/^http/, "ws")}/socket`, started.url);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  const results = new Map<string, (result: { ok: boolean; error?: string }) => void>();
  const received: string[] = [];
  let snapshot: Snapshot | undefined;
  let requests = 0;
  socket.addEventListener("message", (event) => {
    received.push(String(event.data));
    const message = JSON.parse(String(event.data));
    if (message.type === "snapshot") {
      snapshot = message;
      return;
    }
    results.get(message.requestId)?.({ ok: message.ok, ...(message.ok ? {} : { error: message.error }) });
    results.delete(message.requestId);
  });
  return {
    close: () => socket.close(),
    received: () => received.join("\n"),
    request(command) {
      const requestId = `request-${(requests += 1)}`;
      const result = new Promise<{ ok: boolean; error?: string }>((resolve) => results.set(requestId, resolve));
      socket.send(JSON.stringify({ ...command, requestId }));
      return result;
    },
    snapshot() {
      if (!snapshot) {
        throw new Error("the host did not send a snapshot");
      }
      return snapshot;
    },
  };
}

// Bun's WebSocket client accepts request headers, which the browser lib's
// constructor type does not describe.
function open(url: string, origin: string): WebSocket {
  const client = WebSocket as unknown as new (
    url: string,
    options: { headers: Record<string, string> },
  ) => WebSocket;
  return new client(url, { headers: { Origin: origin } });
}
