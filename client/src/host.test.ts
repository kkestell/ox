import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
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
const keptWorkspaceID = "22222222-2222-4222-8222-222222222222";
const goneWorkspaceID = "33333333-3333-4333-8333-333333333333";

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
      { awaiting: false, busy: false, id: expect.any(String), name: "private-workspace", status: "unavailable" },
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

  test("restarts a workspace, keeping why its last process stopped", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "restarted");
    await mkdir(workspace);
    host = await startHost({ oxCommand: "/definitely/not/ox", registryPath: join(root, "workspaces.json") });
    const client = await connect(host);
    await client.request({ type: "register-workspace", path: workspace });
    const [entry] = client.snapshot().workspaces.values;
    if (!entry) {
      throw new Error("the workspace should be registered");
    }
    const before = client.snapshot().workspace?.diagnostics ?? [];
    expect(before.length).toBeGreaterThan(0);

    expect(await client.request({ type: "restart-workspace", workspaceId: entry.id })).toEqual({ ok: true });

    const after = client.snapshot().workspace?.diagnostics ?? [];
    expect(after.slice(0, before.length)).toEqual(before);
    expect(after.length).toBeGreaterThan(before.length);
    expect(client.snapshot().workspaces.values).toEqual([
      { awaiting: false, busy: false, id: entry.id, name: "restarted", status: "unavailable" },
    ]);
    client.close();
  });

  test("serves a registry whose root disappeared instead of refusing to start", async () => {
    const root = await realpath(await temporaryDirectory());
    const kept = join(root, "kept");
    await mkdir(kept);
    const registryPath = join(root, "workspaces.json");
    await writeFile(
      registryPath,
      JSON.stringify({
        version: 1,
        selectedId: goneWorkspaceID,
        workspaces: [
          { id: keptWorkspaceID, root: kept },
          { id: goneWorkspaceID, root: join(root, "gone") },
        ],
      }),
    );

    host = await startHost({ oxCommand: "/definitely/not/ox", registryPath });
    const client = await connect(host);
    await client.request({ type: "ping" });

    expect(client.snapshot().workspaces.values).toEqual([
      { awaiting: false, busy: false, id: keptWorkspaceID, name: "kept", status: "unavailable" },
      { awaiting: false, busy: false, id: goneWorkspaceID, name: "gone", status: "unavailable" },
    ]);
    expect(client.snapshot().workspace?.diagnostics).toEqual(["workspace must be a listable directory"]);
    expect(await client.request({ type: "set-mcp-servers", workspaceId: keptWorkspaceID, mcpServers: [] })).toEqual({
      ok: true,
    });
    client.close();
  });

  test("reports a restarting workspace as stopped and then starting", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "restarted");
    await mkdir(workspace);
    host = await startHost({ oxCommand: "/definitely/not/ox", registryPath: join(root, "workspaces.json") });
    const client = await connect(host);
    await client.request({ type: "register-workspace", path: workspace });
    const [entry] = client.snapshot().workspaces.values;
    if (!entry) {
      throw new Error("the workspace should be registered");
    }

    expect(await client.request({ type: "restart-workspace", workspaceId: entry.id })).toEqual({ ok: true });

    expect(client.statuses(entry.id)).toEqual(["starting", "unavailable", "stopped", "starting", "unavailable"]);
    client.close();
  });

  test("keeps the selected workspace's detail across another workspace's publish", async () => {
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
    await client.request({ type: "set-mcp-servers", workspaceId: one.id, mcpServers: [httpMCPServer] });
    const before = client.snapshot();

    await client.request({ type: "set-mcp-servers", workspaceId: two.id, mcpServers: [httpMCPServer] });

    const after = client.snapshot();
    expect(after.revision).toBeGreaterThan(before.revision);
    expect(after.workspaces.selectedId).toBe(one.id);
    expect(after.workspace).toEqual(before.workspace);
    expect(after.workspace?.mcpServerCount).toBe(1);
    client.close();
  });

  test("refuses to restart a workspace that is not registered", async () => {
    host = await startTestHost();
    const client = await connect(host);

    expect(await client.request({ type: "restart-workspace", workspaceId: unregisteredWorkspaceID })).toEqual({
      ok: false,
      error: "workspace is not registered",
    });
    client.close();
  });

  test("leaves the other workspace routing while one restarts", async () => {
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

    const restarted = client.request({ type: "restart-workspace", workspaceId: one.id });

    expect(await client.request({ type: "set-mcp-servers", workspaceId: two.id, mcpServers: [] })).toEqual({ ok: true });
    expect(await restarted).toEqual({ ok: true });
    expect(await client.request({ type: "set-mcp-servers", workspaceId: one.id, mcpServers: [] })).toEqual({ ok: true });
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
  statuses(workspaceId: string): string[];
};

async function connect(started: StartedHost): Promise<Client> {
  const socket = open(`${started.url.replace(/^http/, "ws")}/socket`, started.url);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  const results = new Map<string, (result: { ok: boolean; error?: string }) => void>();
  const received: string[] = [];
  // Statuses only exist between two snapshots, so a test that asserts a
  // transition has to keep every snapshot's catalog rather than the latest one.
  const snapshots: Snapshot[] = [];
  let snapshot: Snapshot | undefined;
  let requests = 0;
  socket.addEventListener("message", (event) => {
    received.push(String(event.data));
    const message = JSON.parse(String(event.data));
    if (message.type === "snapshot") {
      snapshot = message;
      snapshots.push(message);
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
    statuses(workspaceId) {
      return snapshots.flatMap((value, index) => {
        const status = value.workspaces.values.find((entry) => entry.id === workspaceId)?.status;
        const previous = snapshots[index - 1]?.workspaces.values.find((entry) => entry.id === workspaceId)?.status;
        return status !== undefined && status !== previous ? [status] : [];
      });
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
