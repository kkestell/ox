import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, mkdir, readFile, realpath, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { maximumTerminalOutputBytes, TerminalExecutor } from "./terminal-executor.ts";

const workspaces: string[] = [];

afterEach(async () => {
  await Promise.all(workspaces.splice(0).map((workspace) => rm(workspace, { force: true, recursive: true })));
});

describe("terminal executor", () => {
  test("runs the exact program, argv, environment, and confined working directory", async () => {
    const workspace = await temporaryWorkspace();
    const nested = join(workspace, "nested");
    await mkdir(nested);
    const executor = new TerminalExecutor(workspace, (sessionID) => sessionID === "session-1");
    const created = await executor.create(
      {
        args: ["-e", "process.stdout.write(JSON.stringify({argv:process.argv.slice(1),cwd:process.cwd(),value:process.env.ONLY}))", "argument"],
        command: process.execPath,
        cwd: nested,
        env: [{ name: "ONLY", value: "expected" }],
        sessionId: "session-1",
      },
      new AbortController().signal,
    );

    await expect(executor.wait({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).resolves.toEqual({
      exitCode: 0,
    });
    expect(executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).toEqual({
      exitStatus: { exitCode: 0 },
      output: JSON.stringify({ argv: ["argument"], cwd: nested, value: "expected" }),
      truncated: false,
    });
  });

  test("returns bounded UTF-8-safe output and a stable completed exit status", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new TerminalExecutor(workspace, () => true);
    const created = await executor.create(
      {
        args: ["-e", "process.stdout.write('α'.repeat(8))"],
        command: process.execPath,
        outputByteLimit: 7,
        sessionId: "session-1",
      },
      new AbortController().signal,
    );

    await executor.wait({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal);
    const output = executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal);
    expect(output).toEqual({ exitStatus: { exitCode: 0 }, output: "ααα", truncated: true });
    await expect(executor.wait({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).resolves.toEqual({
      exitCode: 0,
    });
  });

  test("does not report completion until inherited output streams have closed", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new TerminalExecutor(workspace, () => true);
    const created = await executor.create(
      {
        args: [
          "-e",
          "const { spawn } = require('node:child_process'); spawn(process.execPath, ['-e', `setTimeout(() => process.stdout.write('late output'), 100)`], { stdio: 'inherit' }); process.exit(0)",
        ],
        command: process.execPath,
        sessionId: "session-1",
      },
      new AbortController().signal,
    );

    await new Promise<void>((resolve) => setTimeout(resolve, 20));
    expect(executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).toEqual({
      output: "",
      truncated: false,
    });
    await expect(executor.wait({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).resolves.toEqual({
      exitCode: 0,
    });
    expect(executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal)).toEqual({
      exitStatus: { exitCode: 0 },
      output: "late output",
      truncated: false,
    });
  });

  test("kills the whole terminal process group when a wait callback is cancelled", async () => {
    const workspace = await temporaryWorkspace();
    const childPID = join(workspace, "child.pid");
    const executor = new TerminalExecutor(workspace, () => true);
    const created = await executor.create(
      {
        args: ["-c", "sleep 30 & child=$!; echo $child > \"$CHILD_PID\"; echo ready; wait"],
        command: "/bin/sh",
        env: [{ name: "CHILD_PID", value: childPID }],
        sessionId: "session-1",
      },
      new AbortController().signal,
    );
    await eventually(() => executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal).output === "ready\n");
    const controller = new AbortController();
    const waiting = executor.wait({ sessionId: "session-1", terminalId: created.terminalId }, controller.signal);
    controller.abort();
    await expect(waiting).rejects.toThrow();
    await eventually(
      () => executor.output({ sessionId: "session-1", terminalId: created.terminalId }, new AbortController().signal).exitStatus?.signal === "SIGKILL",
    );
    const pid = Number((await readFile(childPID, "utf8")).trim());
    await eventually(() => {
      try {
        process.kill(pid, 0);
        return false;
      } catch {
        return true;
      }
    });
  });

  test("rejects untrusted terminal creation and terminal identifiers", async () => {
    const workspace = await temporaryWorkspace();
    const outside = await temporaryWorkspace();
    const linked = join(workspace, "linked");
    await symlink(outside, linked);
    const executor = new TerminalExecutor(workspace, (sessionID) => sessionID === "active");
    const signal = new AbortController().signal;

    await expect(executor.create({ command: "true", cwd: "relative", sessionId: "active" }, signal)).rejects.toThrow("must be absolute");
    await expect(executor.create({ command: "true", cwd: outside, sessionId: "active" }, signal)).rejects.toThrow("outside the workspace");
    await expect(executor.create({ command: "true", cwd: linked, sessionId: "active" }, signal)).rejects.toThrow("symbolic link");
    await expect(executor.create({ command: "true", outputByteLimit: maximumTerminalOutputBytes + 1, sessionId: "active" }, signal)).rejects.toThrow(
      "output limit",
    );
    await expect(
      executor.create(
        { command: "true", env: [{ name: "DUPLICATE", value: "one" }, { name: "DUPLICATE", value: "two" }], sessionId: "active" },
        signal,
      ),
    ).rejects.toThrow("duplicate variable");
    await expect(executor.create({ command: join(workspace, "missing-command"), sessionId: "active" }, signal)).rejects.toThrow();
    await expect(executor.create({ command: "true", sessionId: "closed" }, signal)).rejects.toThrow("active session");
    expect(() => executor.output({ sessionId: "active", terminalId: "unknown" }, signal)).toThrow("unknown terminal");
  });

  test("keeps kill and wait usable after termination but removes all state on release", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new TerminalExecutor(workspace, () => true);
    const created = await executor.create(
      { args: ["-e", "setTimeout(() => {}, 30_000)"], command: process.execPath, sessionId: "session-1" },
      new AbortController().signal,
    );
    const request = { sessionId: "session-1", terminalId: created.terminalId };

    await executor.kill(request, new AbortController().signal);
    await executor.kill(request, new AbortController().signal);
    await expect(executor.wait(request, new AbortController().signal)).resolves.toEqual({ signal: "SIGKILL" });
    await executor.release(request, new AbortController().signal);
    expect(() => executor.output(request, new AbortController().signal)).toThrow("unknown terminal");
  });
});

async function temporaryWorkspace(): Promise<string> {
  const workspace = await realpath(await mkdtemp(join(tmpdir(), "ox-terminal-executor-")));
  workspaces.push(workspace);
  return workspace;
}

async function eventually(predicate: () => boolean): Promise<void> {
  const deadline = Date.now() + 1_000;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error("condition did not become true");
    await new Promise<void>((resolve) => setTimeout(resolve, 10));
  }
}
