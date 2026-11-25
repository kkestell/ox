// Services runs oxd and the ox relay for a test, on a temporary socket,
// database, and port, built with the race detector by global-setup.ts.
import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createConnection, createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { stopProcess } from "../stack.mjs";

const fake = {
  models: [
    {
      id: "echo",
      name: "Echo",
      efforts: [
        { id: "low", name: "Low" },
        { id: "high", name: "High" },
      ],
    },
  ],
  permission_modes: [
    { id: "manual", name: "Manual" },
    { id: "plan", name: "Plan" },
  ],
};

const claude = {
  models: [{ id: "claude-haiku-5-5", name: "Haiku 5.5", efforts: [{ id: "low", name: "Low" }] }],
  permission_modes: [
    { id: "manual", name: "Manual" },
    { id: "acceptEdits", name: "Accept edits" },
    { id: "plan", name: "Plan" },
  ],
};

export const fakeConfig = {
  harnesses: { fake },
  defaults: { harness: "fake", model: "echo", effort: "low", permission_mode: "manual" },
  titling: { harness: "fake", model: "echo", effort: "low" },
  max_child_depth: 2,
};

export const haikuConfig = {
  harnesses: { claude },
  defaults: { harness: "claude", model: "claude-haiku-5-5", effort: "low", permission_mode: "manual" },
  titling: { harness: "claude", model: "claude-haiku-5-5", effort: "low" },
  max_child_depth: 2,
};

export type Services = {
  url: string;
  // workspace is an empty directory for the sessions to run in.
  workspace: string;
  // worktrees is the directory oxd makes session worktrees in.
  worktrees: string;
  // callTool calls an MCP tool as session from's Claude does, through an MCP
  // connection to oxd's socket attached to it, and returns the tool's text.
  callTool(from: string, name: string, args: Record<string, string>): Promise<string>;
  restartRelay(): Promise<void>;
  // restartDaemon restarts oxd over the same database with config. SIGKILL
  // stops it without its shutdown, as a crash does.
  restartDaemon(config: object, signal?: NodeJS.Signals): Promise<void>;
  stop(): Promise<void>;
};

export async function startServices(config: object): Promise<Services> {
  const bin = process.env.OX_E2E_BIN;
  if (!bin) throw new Error("global-setup.ts did not build ox");
  const dir = mkdtempSync(join(tmpdir(), "ox-e2e-"));
  const workspace = mkdtempSync(join(tmpdir(), "ox-e2e-workspace-"));
  const socket = join(dir, "oxd.sock");
  const database = join(dir, "ox.db");
  const worktrees = join(dir, "worktrees");
  writeFileSync(join(dir, "config.json"), JSON.stringify(config));
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const env = { ...process.env, GORACE: "halt_on_error=1" };

  const startDaemon = () =>
    run(join(bin, "oxd"), [
      `-socket=${socket}`,
      `-db=${database}`,
      `-config=${join(dir, "config.json")}`,
      `-worktrees=${worktrees}`,
    ]);
  let daemon = startDaemon();
  let relay!: ChildProcess;
  const startRelay = async () => {
    relay = run(join(bin, "ox"), [
      `-addr=127.0.0.1:${port}`,
      `-socket=${socket}`,
      `-config=${join(dir, "config.json")}`,
    ]);
    await waitFor(url);
  };
  function run(command: string, args: string[]) {
    const child = spawn(command, args, { env, stdio: ["ignore", "inherit", "inherit"] });
    child.on("exit", (code, signal) => {
      // A race report stops oxd with exit status 66.
      if (code) console.error(`${command} exited with status ${code}`);
      else if (signal && signal !== "SIGTERM") console.error(`${command} exited on ${signal}`);
    });
    return child;
  }
  await startRelay();

  return {
    url,
    workspace,
    worktrees,
    callTool: (from, name, args) => callTool(socket, from, name, args),
    async restartRelay() {
      await stopProcess(relay);
      await startRelay();
    },
    async restartDaemon(config: object, signal: NodeJS.Signals = "SIGTERM") {
      await stopProcess(daemon, signal);
      writeFileSync(join(dir, "config.json"), JSON.stringify(config));
      daemon = startDaemon();
      await waitFor(url);
    },
    async stop() {
      const exited = daemon.exitCode;
      // Both stop even when one exits badly, so a failure leaves neither running.
      const stopped = await Promise.allSettled([stopProcess(relay), stopProcess(daemon)]);
      for (const result of stopped) if (result.status === "rejected") throw result.reason;
      if (exited !== null) throw new Error(`oxd exited during the tests with status ${exited}`);
      rmSync(dir, { recursive: true, force: true });
      rmSync(workspace, { recursive: true, force: true });
    },
  };
}

function callTool(socket: string, from: string, name: string, args: Record<string, string>) {
  return new Promise<string>((resolve, reject) => {
    const connection = createConnection(socket);
    const call = (id: number, method: string, params: object) =>
      connection.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    let buffered = "";
    connection.on("data", (data) => {
      buffered += data;
      for (let i = buffered.indexOf("\n"); i >= 0; i = buffered.indexOf("\n")) {
        const line = JSON.parse(buffered.slice(0, i));
        buffered = buffered.slice(i + 1);
        if (line.id !== 2) continue;
        connection.end();
        if (line.error || line.result.isError) reject(new Error(JSON.stringify(line)));
        else resolve(line.result.content[0].text);
      }
    });
    connection.on("error", reject);
    call(1, "mcp/attach", { session_id: from });
    call(2, "tools/call", { name, arguments: args });
  });
}

function freePort() {
  return new Promise<number>((resolve, reject) => {
    const server = createServer();
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      server.close(() => (typeof address === "object" && address ? resolve(address.port) : reject()));
    });
  });
}

// waitFor resolves once url serves the page and its relay reaches oxd.
async function waitFor(url: string) {
  for (let i = 0; i < 100; i++) {
    try {
      const controller = new AbortController();
      const response = await fetch(`${url}/events`, { signal: controller.signal });
      controller.abort();
      if (response.ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`${url} did not start`);
}
