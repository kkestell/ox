import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
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

    expect(supervisor.state).toEqual({
      authentication: { logoutAvailable: false, methods: [], status: "required" },
      diagnostics: [],
      sessions: { values: [] },
      status: "ready",
    });

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
      authentication: { logoutAvailable: false, methods: [], status: "unavailable" },
      diagnostics: ["Ox did not initialize within 2 seconds"],
      sessions: { values: [] },
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

  test("authenticates stored credentials, completes terminal login, and logs out", async () => {
    const workspace = await temporaryWorkspace();
    const supervisor = await WorkspaceSupervisor.start({
      arguments: ["--eval", authenticationProgram(), "primary"],
      command: process.execPath,
      workspace,
    });

    expect(supervisor.state.authentication).toEqual({
      logoutAvailable: true,
      methods: [
        { id: "stored", name: "Stored credential", type: "agent" },
        { id: "terminal", name: "Terminal login", type: "terminal" },
      ],
      status: "required",
    });

    await expect(supervisor.authenticate("stored")).rejects.toThrow("authentication failed");
    expect(supervisor.state.authentication).toMatchObject({ error: "Authentication failed", status: "required" });

    await supervisor.login("terminal", "correct-test-credential");
    expect(supervisor.state.authentication.status).toBe("authenticated");
    expect(await readFile(join(workspace, "login-observation.json"), "utf8")).toBe(
      JSON.stringify({ args: ["primary", "login"], environment: "terminal-auth" }),
    );

    await supervisor.logout();
    expect(supervisor.state.authentication.status).toBe("required");
    await expect(supervisor.authenticate("stored")).rejects.toThrow("authentication failed");

    await supervisor.stop();
  });

  test("does not retain a failed terminal credential", async () => {
    const workspace = await temporaryWorkspace();
    const supervisor = await WorkspaceSupervisor.start({
      arguments: ["--eval", authenticationProgram(), "primary"],
      command: process.execPath,
      workspace,
    });

    await expect(supervisor.login("terminal", "incorrect-test-credential")).rejects.toThrow("OpenRouter login failed");

    expect(supervisor.state.authentication).toMatchObject({ error: "OpenRouter login failed", status: "required" });
    expect(supervisor.state.diagnostics.join("\n")).not.toContain("incorrect-test-credential");
    await supervisor.stop();
  });

  test("treats an unadvertised logout capability as unsupported", async () => {
    const supervisor = await WorkspaceSupervisor.start({
      arguments: ["--eval", authenticationProgram("null"), "primary"],
      command: process.execPath,
      workspace: await temporaryWorkspace(),
    });

    expect(supervisor.state.authentication.logoutAvailable).toBe(false);
    await expect(supervisor.logout()).rejects.toThrow("Ox does not support logout");

    await supervisor.stop();
  });

  test("owns paginated session lifecycle state", async () => {
    const supervisor = await WorkspaceSupervisor.start({
      arguments: ["--eval", lifecycleProgram()],
      command: process.execPath,
      workspace: await temporaryWorkspace(),
    });

    await eventually(() => supervisor.state.sessions.values.length === 1);
    expect(supervisor.state.sessions).toEqual({
      nextCursor: "page-2",
      values: [{ id: "first", status: "inactive", title: "First" }],
    });

    await supervisor.nextSessionPage();
    expect(supervisor.state.sessions.values.map((session) => session.id)).toEqual(["first", "second"]);

    await expect(supervisor.loadSession("missing")).rejects.toThrow("unknown session");
    expect(supervisor.state.sessions.values.map((session) => session.id)).toEqual(["first", "second"]);
    expect(supervisor.state.sessions.selectedID).toBeUndefined();

    await supervisor.loadSession("first");
    expect(supervisor.state.sessions.selectedID).toBe("first");
    expect(supervisor.state.sessions.values.find((session) => session.id === "first")?.status).toBe("active");
    expect(supervisor.sessionUpdates("first")).toEqual([
      { content: { text: "replayed", type: "text" }, sessionUpdate: "agent_message_chunk" },
    ]);

    await supervisor.closeSession("first");
    await supervisor.resumeSession("second");
    expect(supervisor.state.sessions.selectedID).toBe("second");
    expect(supervisor.state.sessions.values.find((session) => session.id === "second")?.status).toBe("active");

    await supervisor.closeSession("second");
    await supervisor.deleteSession("second");
    expect(supervisor.state.sessions.values.map((session) => session.id)).toEqual(["first"]);

    await supervisor.newSession();
    expect(supervisor.state.sessions.selectedID).toBe("created");
    expect(supervisor.state.sessions.values.find((session) => session.id === "created")?.status).toBe("active");
    await supervisor.stop();
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

function authenticationProgram(logout = "{}"): string {
  return `
const fs = require('fs');
const path = require('path');
if (process.argv.includes('login')) {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (chunk) => input += chunk);
  process.stdin.on('end', () => {
    if (input.trim() !== 'correct-test-credential') process.exit(1);
    fs.writeFileSync(path.join(process.cwd(), 'credential-present'), 'yes');
    fs.writeFileSync(path.join(process.cwd(), 'login-observation.json'), JSON.stringify({ args: process.argv.slice(1), environment: process.env.OX_CLIENT_LOGIN_ENV }));
    process.exit(0);
  });
} else {
process.stdin.setEncoding('utf8');
let input = '';
process.stdin.on('data', (chunk) => {
  input += chunk;
  for (;;) {
    const newline = input.indexOf('\\n');
    if (newline === -1) break;
    const request = JSON.parse(input.slice(0, newline));
    input = input.slice(newline + 1);
    if (request.method === 'initialize') {
      process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: {
        protocolVersion: 1,
        agentCapabilities: { auth: { logout: ${logout} } },
        authMethods: [
          { id: 'stored', name: 'Stored credential' },
          { id: 'terminal', type: 'terminal', name: 'Terminal login', args: ['login'], env: { OX_CLIENT_LOGIN_ENV: 'terminal-auth' } }
        ]
      } }) + '\\n');
      continue;
    }
    if (request.method === 'authenticate') {
      if (request.params.methodId === 'stored' && fs.existsSync(path.join(process.cwd(), 'credential-present'))) {
        process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: {} }) + '\\n');
      } else {
        process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, error: { code: -32000, message: 'credential required' } }) + '\\n');
      }
      continue;
    }
    if (request.method === 'logout') {
      fs.rmSync(path.join(process.cwd(), 'credential-present'), { force: true });
      process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: {} }) + '\\n');
    }
  }
});
}`;
}

function lifecycleProgram(): string {
  return `
process.stdin.setEncoding('utf8');
let input = '';
let sessions = [
  { sessionId: 'first', cwd: process.cwd(), title: 'First' },
  { sessionId: 'second', cwd: process.cwd(), title: 'Second' },
];
function response(id, result) {
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, result }) + '\\n');
}
process.stdin.on('data', (chunk) => {
  input += chunk;
  for (;;) {
    const newline = input.indexOf('\\n');
    if (newline === -1) break;
    const request = JSON.parse(input.slice(0, newline));
    input = input.slice(newline + 1);
    if (request.method === 'initialize') {
      response(request.id, { protocolVersion: 1, agentCapabilities: {
        loadSession: true,
        sessionCapabilities: { list: {}, delete: {}, resume: {}, close: {} },
      }, authMethods: [] });
      continue;
    }
    if (request.method === 'session/list') {
      if (request.params.cursor === 'page-2') {
        const firstID = sessions.some((session) => session.sessionId === 'created') ? 'created' : 'first';
        response(request.id, { sessions: sessions.filter((session) => session.sessionId !== firstID) });
      } else {
        const firstID = sessions.some((session) => session.sessionId === 'created') ? 'created' : 'first';
        const first = sessions.find((session) => session.sessionId === firstID);
        response(request.id, { sessions: first ? [first] : [], nextCursor: sessions.some((session) => session.sessionId === 'second') ? 'page-2' : undefined });
      }
      continue;
    }
    if (request.method === 'session/new') {
      sessions.push({ sessionId: 'created', cwd: process.cwd(), title: 'Created' });
      response(request.id, { sessionId: 'created' });
      continue;
    }
    if (request.method === 'session/load') {
      if (!sessions.some((session) => session.sessionId === request.params.sessionId)) {
        process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, error: { code: -32602, message: 'unknown session' } }) + '\\n');
        continue;
      }
      process.stdout.write(JSON.stringify({ jsonrpc: '2.0', method: 'session/update', params: {
        sessionId: request.params.sessionId,
        update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'replayed' } },
      } }) + '\\n');
      response(request.id, {});
      continue;
    }
    if (request.method === 'session/resume' || request.method === 'session/close') {
      response(request.id, {});
      continue;
    }
    if (request.method === 'session/delete') {
      sessions = sessions.filter((session) => session.sessionId !== request.params.sessionId);
      response(request.id, {});
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
