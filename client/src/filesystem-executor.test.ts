import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { FilesystemExecutor, maximumFileBytes } from "./filesystem-executor.ts";

const workspaces: string[] = [];

afterEach(async () => {
  await Promise.all(workspaces.splice(0).map((workspace) => rm(workspace, { force: true, recursive: true })));
});

describe("filesystem executor", () => {
  test("reads valid UTF-8 with ACP line paging and replaces files in the active workspace", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new FilesystemExecutor(workspace, (sessionID) => sessionID === "session-1");
    const path = join(workspace, "notes.txt");
    await writeFile(path, "one\ntwo\nthree");

    await expect(executor.read({ path, sessionId: "session-1" }, new AbortController().signal)).resolves.toEqual({
      content: "one\ntwo\nthree",
    });
    await expect(executor.read({ line: 2, limit: 1, path, sessionId: "session-1" }, new AbortController().signal)).resolves.toEqual({
      content: "two\n",
    });

    const nested = join(workspace, "created", "replacement.txt");
    await executor.write({ content: "replacement", path: nested, sessionId: "session-1" }, new AbortController().signal);
    expect(await readFile(nested, "utf8")).toBe("replacement");
    await executor.write({ content: "changed", path, sessionId: "session-1" }, new AbortController().signal);
    expect(await readFile(path, "utf8")).toBe("changed");
  });

  test("rejects callbacks for inactive sessions and paths outside the canonical workspace", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new FilesystemExecutor(workspace, (sessionID) => sessionID === "active");
    const signal = new AbortController().signal;

    await expect(executor.read({ path: join(workspace, "notes.txt"), sessionId: "closed" }, signal)).rejects.toThrow("active session");
    await expect(executor.read({ path: join(workspace, "..", "outside.txt"), sessionId: "active" }, signal)).rejects.toThrow(
      "outside the workspace",
    );
    await expect(executor.read({ path: "notes.txt", sessionId: "active" }, signal)).rejects.toThrow("must be absolute");
    await expect(executor.read({ path: join(workspace, "missing.txt"), sessionId: "active" }, signal)).rejects.toThrow("does not exist");
  });

  test("refuses symbolic links and non-regular filesystem targets", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new FilesystemExecutor(workspace, () => true);
    const outside = await temporaryWorkspace();
    const link = join(workspace, "linked.txt");
    const linkedDirectory = join(workspace, "linked-directory");
    await writeFile(join(outside, "secret.txt"), "secret");
    await symlink(join(outside, "secret.txt"), link);
    await symlink(outside, linkedDirectory);
    await mkdir(join(workspace, "directory"));
    const signal = new AbortController().signal;

    await expect(executor.read({ path: link, sessionId: "session-1" }, signal)).rejects.toThrow("symbolic link");
    await expect(executor.read({ path: join(linkedDirectory, "secret.txt"), sessionId: "session-1" }, signal)).rejects.toThrow(
      "symbolic link",
    );
    await expect(executor.write({ content: "no", path: join(workspace, "directory"), sessionId: "session-1" }, signal)).rejects.toThrow(
      "not a regular file",
    );
  });

  test("enforces Ox's delegated read bounds and valid UTF-8", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new FilesystemExecutor(workspace, () => true);
    const signal = new AbortController().signal;
    const invalid = join(workspace, "invalid.txt");
    const oversized = join(workspace, "oversized.txt");
    await writeFile(invalid, Buffer.from([0xff]));
    await writeFile(oversized, Buffer.alloc(maximumFileBytes + 1));

    await expect(executor.read({ path: invalid, sessionId: "session-1" }, signal)).rejects.toThrow("valid UTF-8");
    await expect(executor.read({ path: oversized, sessionId: "session-1" }, signal)).rejects.toThrow("byte limit");
  });

  test("stops an already-cancelled callback before filesystem access", async () => {
    const workspace = await temporaryWorkspace();
    const executor = new FilesystemExecutor(workspace, () => true);
    const controller = new AbortController();
    controller.abort();

    await expect(
      executor.write({ content: "no", path: join(workspace, "not-written.txt"), sessionId: "session-1" }, controller.signal),
    ).rejects.toThrow();
    await expect(readFile(join(workspace, "not-written.txt"), "utf8")).rejects.toThrow();
  });
});

async function temporaryWorkspace(): Promise<string> {
  const created = await mkdtemp(join(tmpdir(), "ox-filesystem-executor-"));
  const workspace = await realpath(created);
  workspaces.push(workspace);
  return workspace;
}
