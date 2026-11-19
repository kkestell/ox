// Services runs oxd and the ox relay for a test file, on a temporary socket,
// database, and port, built with the race detector by global-setup.ts.
import { type ChildProcess, spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

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
  ],
};

export const fakeConfig = {
  harnesses: { fake },
  defaults: { harness: "fake", model: "echo", effort: "low", permission_mode: "manual" },
};

export const haikuConfig = {
  harnesses: { claude },
  defaults: { harness: "claude", model: "claude-haiku-5-5", effort: "low", permission_mode: "manual" },
};

export type Services = {
  url: string;
  // workspace is an empty directory for the sessions to run in.
  workspace: string;
  restartRelay(): Promise<void>;
  stop(): Promise<void>;
};

export async function startServices(config: object): Promise<Services> {
  const bin = process.env.OX_E2E_BIN;
  if (!bin) throw new Error("global-setup.ts did not build ox");
  const dir = mkdtempSync(join(tmpdir(), "ox-e2e-"));
  const workspace = mkdtempSync(join(tmpdir(), "ox-e2e-workspace-"));
  const socket = join(dir, "oxd.sock");
  writeFileSync(join(dir, "config.json"), JSON.stringify(config));
  const port = await freePort();
  const url = `http://127.0.0.1:${port}`;
  const env = { ...process.env, GORACE: "halt_on_error=1" };

  const daemon = run(join(bin, "oxd"), [
    `-socket=${socket}`,
    `-db=${join(dir, "ox.db")}`,
    `-config=${join(dir, "config.json")}`,
  ]);
  let relay: ChildProcess | undefined;
  const startRelay = async () => {
    relay = run(join(bin, "ox"), [`-addr=127.0.0.1:${port}`, `-socket=${socket}`]);
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
    async restartRelay() {
      if (relay) await stopProcess(relay);
      await startRelay();
    },
    async stop() {
      const exited = daemon.exitCode;
      if (relay) await stopProcess(relay);
      await stopProcess(daemon);
      if (exited !== null) throw new Error(`oxd exited during the tests with status ${exited}`);
      rmSync(dir, { recursive: true, force: true });
      rmSync(workspace, { recursive: true, force: true });
    },
  };
}

function stopProcess(child: ChildProcess) {
  return new Promise<void>((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) return resolve();
    child.once("exit", () => resolve());
    child.kill("SIGTERM");
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
