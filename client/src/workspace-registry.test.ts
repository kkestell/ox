import { afterEach, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, realpath, rm, stat, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { defaultWorkspaceRegistryPath, WorkspaceRegistry } from "./workspace-registry.ts";

const temporaryDirectories: string[] = [];
const firstID = "11111111-1111-4111-8111-111111111111";
const secondID = "22222222-2222-4222-8222-222222222222";

afterEach(async () => {
  await Promise.all(temporaryDirectories.splice(0).map((path) => rm(path, { force: true, recursive: true })));
});

describe("workspace registry", () => {
  test("resolves only absolute XDG or home configuration roots", () => {
    expect(defaultWorkspaceRegistryPath({ HOME: "/home/test", XDG_CONFIG_HOME: "/configuration" })).toBe(
      "/configuration/ox/workspaces.json",
    );
    expect(defaultWorkspaceRegistryPath({ HOME: "/home/test", XDG_CONFIG_HOME: "relative" })).toBe(
      "/home/test/.config/ox/workspaces.json",
    );
    expect(() => defaultWorkspaceRegistryPath({ HOME: "relative" })).toThrow(
      "workspace registry requires an absolute XDG_CONFIG_HOME or HOME",
    );
  });

  test("loads missing state and atomically persists owner-only stable entries", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "workspace");
    const path = join(root, "configuration", "workspaces.json");
    await mkdir(workspace);
    const registry = await WorkspaceRegistry.load(path);

    const registered = await registry.register(workspace);

    expect(registry.state).toEqual({
      selectedId: registered.workspace.id,
      workspaces: [{ id: registered.workspace.id, name: "workspace", root: registered.workspace.root }],
    });
    expect((await stat(path)).mode & 0o777).toBe(0o600);
    expect(JSON.parse(await readFile(path, "utf8"))).toEqual({
      version: 1,
      selectedId: registered.workspace.id,
      workspaces: [{ id: registered.workspace.id, root: registered.workspace.root }],
    });
    expect((await WorkspaceRegistry.load(path)).state).toEqual(registry.state);
  });

  test("rejects malformed, unsupported, and internally inconsistent files", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "workspace");
    const path = join(root, "workspaces.json");
    await mkdir(workspace);
    for (const value of [
      "not json",
      JSON.stringify({ version: 2, workspaces: [] }),
      JSON.stringify({ version: 1, workspaces: [], extra: true }),
      JSON.stringify({ version: 1, selectedId: firstID, workspaces: [] }),
      JSON.stringify({ version: 1, workspaces: [{ id: firstID, root: workspace }] }),
      JSON.stringify({ version: 1, selectedId: secondID, workspaces: [{ id: firstID, root: workspace }] }),
    ]) {
      await writeFile(path, value);
      await expect(WorkspaceRegistry.load(path)).rejects.toThrow("workspace registry");
    }
  });

  test("deduplicates canonical roots including symbolic-link aliases", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "workspace");
    const alias = join(root, "alias");
    await mkdir(workspace);
    await symlink(workspace, alias);
    const registry = await WorkspaceRegistry.load(join(root, "workspaces.json"));
    await registry.register(workspace);

    await expect(registry.register(alias)).rejects.toThrow("workspace is already registered");
    expect(registry.state.workspaces).toHaveLength(1);

    await writeFile(
      join(root, "duplicates.json"),
      JSON.stringify({
        version: 1,
        selectedId: firstID,
        workspaces: [{ id: firstID, root: workspace }, { id: secondID, root: alias }],
      }),
    );
    await expect(WorkspaceRegistry.load(join(root, "duplicates.json"))).rejects.toThrow(
      "workspace registry contains a duplicate root",
    );
  });

  test("serializes add, selection, and removal without touching workspace contents", async () => {
    const root = await temporaryDirectory();
    const first = join(root, "first");
    const second = join(root, "second");
    await mkdir(first);
    await mkdir(second);
    await writeFile(join(second, "keep.txt"), "keep");
    const path = join(root, "workspaces.json");
    const registry = await WorkspaceRegistry.load(path);

    const [addedFirst, addedSecond] = await Promise.all([registry.register(first), registry.register(second)]);
    expect(registry.state.selectedId).toBe(addedFirst.workspace.id);
    expect(addedSecond.selectionChanged).toBe(false);
    expect(await registry.select(addedSecond.workspace.id)).toBe(true);
    expect((await registry.remove(addedSecond.workspace.id)).selectionChanged).toBe(true);
    expect(registry.state.selectedId).toBe(addedFirst.workspace.id);
    expect(await readFile(join(second, "keep.txt"), "utf8")).toBe("keep");

    const reloaded = await WorkspaceRegistry.load(path);
    expect(reloaded.state).toEqual(registry.state);
  });

  test("keeps in-memory state unchanged when persistence fails", async () => {
    const root = await temporaryDirectory();
    const workspace = join(root, "workspace");
    const blockedParent = join(root, "not-a-directory");
    await mkdir(workspace);
    const registry = await WorkspaceRegistry.load(join(blockedParent, "workspaces.json"));
    await writeFile(blockedParent, "blocked");

    await expect(registry.register(workspace)).rejects.toThrow("could not persist workspace registry");
    expect(registry.state).toEqual({ workspaces: [] });
  });

  test("rejects non-directory and unusable persisted roots", async () => {
    const root = await temporaryDirectory();
    const file = join(root, "file");
    await writeFile(file, "not a directory");
    const registry = await WorkspaceRegistry.load(join(root, "workspaces.json"));

    await expect(registry.register("relative")).rejects.toThrow("workspace path must be absolute");
    await expect(registry.register(file)).rejects.toThrow("workspace must be a directory");
    expect(registry.state).toEqual({ workspaces: [] });
  });

  test("keeps an entry whose root disappeared and rejects a relative persisted root", async () => {
    const root = await temporaryDirectory();
    const kept = join(await realpath(root), "kept");
    const gone = join(root, "gone");
    const path = join(root, "workspaces.json");
    await mkdir(kept);
    await writeFile(
      path,
      JSON.stringify({
        version: 1,
        selectedId: firstID,
        workspaces: [
          { id: firstID, root: kept },
          { id: secondID, root: gone },
        ],
      }),
    );

    expect((await WorkspaceRegistry.load(path)).state).toEqual({
      selectedId: firstID,
      workspaces: [
        { id: firstID, name: "kept", root: kept },
        { id: secondID, name: "gone", root: gone },
      ],
    });

    await writeFile(
      path,
      JSON.stringify({ version: 1, selectedId: firstID, workspaces: [{ id: firstID, root: "relative" }] }),
    );
    await expect(WorkspaceRegistry.load(path)).rejects.toThrow(
      "workspace registry has an unsupported or malformed format",
    );
  });
});

async function temporaryDirectory(): Promise<string> {
  const path = await mkdtemp(join(tmpdir(), "ox-workspace-registry-"));
  temporaryDirectories.push(path);
  return path;
}
