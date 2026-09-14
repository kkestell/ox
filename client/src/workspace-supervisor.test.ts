import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { WorkspaceSupervisor } from "./workspace-supervisor.ts";

const workspaces: string[] = [];

afterEach(async () => {
  await Promise.all(workspaces.splice(0).map((workspace) => rm(workspace, { force: true, recursive: true })));
});

describe("workspace supervisor", () => {
  test("initializes Ox and shuts it down cleanly", async () => {
    const supervisor = await start("agent");

    expect(supervisor.state).toEqual({ diagnostics: [], status: "ready" });

    await supervisor.stop();

    expect(supervisor.state.status).toBe("stopped");
  });

  test("retains a bounded redacted stderr tail", async () => {
    const supervisor = await start("diagnostics");

    expect(supervisor.state.diagnostics).toHaveLength(16);
    expect(supervisor.state.diagnostics).toContain("line 19");
    expect(supervisor.state.diagnostics.at(-1)).toBe("token=[redacted]");
    expect(supervisor.state.diagnostics.join("\n")).not.toContain("bearer-secret");
    expect(supervisor.state.diagnostics.join("\n")).not.toContain("password-secret");
    expect(supervisor.state.diagnostics.join("\n")).not.toContain("token-secret");

    await supervisor.stop();
  });

  test("reports an unexpected Ox exit", async () => {
    const supervisor = await start("exit");

    await eventually(() => supervisor.state.diagnostics.includes("Ox exited with code 7"));

    expect(supervisor.state.diagnostics).toContain("Ox exited with code 7");
    await supervisor.stop();
  });

  test("reports a launch failure without exposing a ready connection", async () => {
    const started = Date.now();
    const supervisor = await WorkspaceSupervisor.start({
      command: "/definitely/not/ox",
      workspace: await temporaryWorkspace(),
    });

    await eventually(() => supervisor.state.diagnostics.some((diagnostic) => diagnostic.startsWith("Could not start Ox")));

    expect(supervisor.state.status).toBe("unavailable");
    expect(supervisor.state.diagnostics.join("\n")).toContain("Could not start Ox");

    await supervisor.stop();
    expect(Date.now() - started).toBeLessThan(1_000);
  });

  test("reports an Ox process that does not initialize", async () => {
    const supervisor = await start("silent");

    expect(supervisor.state).toEqual({
      diagnostics: ["Ox did not initialize within 2 seconds"],
      status: "unavailable",
    });

    await supervisor.stop();
  });

  test("rejects a workspace that is not a directory", async () => {
    const workspace = await temporaryWorkspace();
    const file = join(workspace, "not-a-directory");
    await writeFile(file, "nope");

    await expect(WorkspaceSupervisor.start({ command: "ox", workspace: file })).rejects.toThrow(
      "workspace must be a directory",
    );
  });
});

async function start(mode: "agent" | "diagnostics" | "exit" | "silent"): Promise<WorkspaceSupervisor> {
  return WorkspaceSupervisor.start({
    arguments: ["--eval", program(mode)],
    command: process.execPath,
    workspace: await temporaryWorkspace(),
  });
}

function program(mode: "agent" | "diagnostics" | "exit" | "silent"): string {
  const setup =
    mode === "diagnostics"
      ? "for (let index = 0; index < 20; index += 1) console.error(`line ${index}`); console.error('Authorization: Bearer bearer-secret'); console.error('{\\\"password\\\":\\\"password-secret\\\"}'); console.error('token=token-secret');"
      : "";
  const exit = mode === "exit" ? "setTimeout(() => process.exit(7), 20);" : "";
  if (mode === "silent") {
    return "process.stdin.resume();";
  }
  return `${setup}
process.stdin.setEncoding('utf8');
let input = '';
process.stdin.on('data', (chunk) => {
  input += chunk;
  for (;;) {
    const newline = input.indexOf('\\n');
    if (newline === -1) break;
    const line = input.slice(0, newline);
    input = input.slice(newline + 1);
    const request = JSON.parse(line);
    if (request.method === 'initialize') {
      process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: { protocolVersion: 1, agentCapabilities: {}, authMethods: [] } }) + '\\n');
      ${exit}
    }
  }
});`;
}

async function temporaryWorkspace(): Promise<string> {
  const workspace = await mkdtemp(join(tmpdir(), "ox-workspace-supervisor-"));
  workspaces.push(workspace);
  return workspace;
}

async function eventually(condition: () => boolean): Promise<void> {
  const deadline = Date.now() + 1_000;
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error("condition was not met");
    }
    await Bun.sleep(10);
  }
}
