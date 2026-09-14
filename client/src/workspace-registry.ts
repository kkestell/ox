import { randomUUID } from "node:crypto";
import { mkdir, open, opendir, readFile, realpath, rename, unlink } from "node:fs/promises";
import { basename, dirname, isAbsolute, join } from "node:path";

import { z } from "zod";

const registryVersion = 1;
const maximumWorkspaces = 1_024;

const persistedRegistrySchema = z
  .object({
    version: z.literal(registryVersion),
    selectedId: z.string().uuid().optional(),
    workspaces: z
      .array(
        z
          .object({
            id: z.string().uuid(),
            root: z.string().min(1),
          })
          .strict(),
      )
      .max(maximumWorkspaces),
  })
  .strict();

type PersistedRegistry = z.infer<typeof persistedRegistrySchema>;

export type RegisteredWorkspace = {
  id: string;
  name: string;
  root: string;
};

export type WorkspaceRegistryState = {
  selectedId?: string;
  workspaces: RegisteredWorkspace[];
};

export function defaultWorkspaceRegistryPath(environment: NodeJS.ProcessEnv = process.env): string {
  const xdg = environment.XDG_CONFIG_HOME;
  if (xdg && isAbsolute(xdg)) {
    return join(xdg, "ox", "workspaces.json");
  }
  const home = environment.HOME;
  if (home && isAbsolute(home)) {
    return join(home, ".config", "ox", "workspaces.json");
  }
  throw new Error("workspace registry requires an absolute XDG_CONFIG_HOME or HOME");
}

export class WorkspaceRegistry {
  readonly path: string;

  #operation: Promise<void> = Promise.resolve();
  #state: WorkspaceRegistryState;

  private constructor(path: string, state: WorkspaceRegistryState) {
    this.path = path;
    this.#state = state;
  }

  static async load(path: string): Promise<WorkspaceRegistry> {
    if (!isAbsolute(path)) {
      throw new Error("workspace registry path must be absolute");
    }
    let encoded: string;
    try {
      encoded = await readFile(path, "utf8");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") {
        return new WorkspaceRegistry(path, { workspaces: [] });
      }
      throw new Error("could not read workspace registry");
    }

    let value: unknown;
    try {
      value = JSON.parse(encoded);
    } catch {
      throw new Error("workspace registry is not valid JSON");
    }
    const parsed = persistedRegistrySchema.safeParse(value);
    if (!parsed.success) {
      throw new Error("workspace registry has an unsupported or malformed format");
    }
    return new WorkspaceRegistry(path, await validatePersistedRegistry(parsed.data));
  }

  static loadDefault(environment: NodeJS.ProcessEnv = process.env): Promise<WorkspaceRegistry> {
    return WorkspaceRegistry.load(defaultWorkspaceRegistryPath(environment));
  }

  get state(): WorkspaceRegistryState {
    return copyState(this.#state);
  }

  get selected(): RegisteredWorkspace | undefined {
    const selected = this.#state.workspaces.find((workspace) => workspace.id === this.#state.selectedId);
    return selected === undefined ? undefined : { ...selected };
  }

  register(root: string): Promise<{ selectionChanged: boolean; workspace: RegisteredWorkspace }> {
    return this.serialize(async () => {
      const canonical = await canonicalWorkspace(root);
      if (this.#state.workspaces.some((workspace) => workspace.root === canonical)) {
        throw new Error("workspace is already registered");
      }
      if (this.#state.workspaces.length >= maximumWorkspaces) {
        throw new Error("workspace registry is full");
      }
      const workspace = { id: randomUUID(), name: basename(canonical) || "Workspace", root: canonical };
      const selectionChanged = this.#state.selectedId === undefined;
      const next: WorkspaceRegistryState = {
        selectedId: this.#state.selectedId ?? workspace.id,
        workspaces: [...this.#state.workspaces, workspace],
      };
      await persist(this.path, next);
      this.#state = next;
      return { selectionChanged, workspace: { ...workspace } };
    });
  }

  select(id: string): Promise<boolean> {
    return this.serialize(async () => {
      if (!this.#state.workspaces.some((workspace) => workspace.id === id)) {
        throw new Error("workspace is not registered");
      }
      if (this.#state.selectedId === id) {
        return false;
      }
      const next = { ...this.#state, selectedId: id };
      await persist(this.path, next);
      this.#state = next;
      return true;
    });
  }

  remove(id: string): Promise<{ selectionChanged: boolean }> {
    return this.serialize(async () => {
      if (!this.#state.workspaces.some((workspace) => workspace.id === id)) {
        throw new Error("workspace is not registered");
      }
      const workspaces = this.#state.workspaces.filter((workspace) => workspace.id !== id);
      const selectionChanged = this.#state.selectedId === id;
      const selectedId = selectionChanged ? workspaces[0]?.id : this.#state.selectedId;
      const next: WorkspaceRegistryState = {
        ...(selectedId === undefined ? {} : { selectedId }),
        workspaces,
      };
      await persist(this.path, next);
      this.#state = next;
      return { selectionChanged };
    });
  }

  private serialize<T>(operation: () => Promise<T>): Promise<T> {
    const running = this.#operation.then(operation, operation);
    this.#operation = running.then(
      () => undefined,
      () => undefined,
    );
    return running;
  }
}

async function validatePersistedRegistry(registry: PersistedRegistry): Promise<WorkspaceRegistryState> {
  if ((registry.workspaces.length === 0) !== (registry.selectedId === undefined)) {
    throw new Error("workspace registry selection is invalid");
  }
  const ids = new Set<string>();
  const roots = new Set<string>();
  const workspaces: RegisteredWorkspace[] = [];
  for (const entry of registry.workspaces) {
    if (ids.has(entry.id)) {
      throw new Error("workspace registry contains a duplicate ID");
    }
    ids.add(entry.id);
    const root = await canonicalWorkspace(entry.root);
    if (roots.has(root)) {
      throw new Error("workspace registry contains a duplicate root");
    }
    roots.add(root);
    workspaces.push({ id: entry.id, name: basename(root) || "Workspace", root });
  }
  if (registry.selectedId !== undefined && !ids.has(registry.selectedId)) {
    throw new Error("workspace registry selection is invalid");
  }
  return {
    ...(registry.selectedId === undefined ? {} : { selectedId: registry.selectedId }),
    workspaces,
  };
}

// Opening the directory, rather than only inspecting its mode, is what proves
// the host can actually list the root it is about to hand to an Ox process.
async function canonicalWorkspace(path: string): Promise<string> {
  if (!isAbsolute(path)) {
    throw new Error("workspace path must be absolute");
  }
  try {
    const root = await realpath(path);
    const directory = await opendir(root);
    try {
      await directory.read();
    } finally {
      await directory.close();
    }
    return root;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOTDIR") {
      throw new Error("workspace must be a directory");
    }
    throw new Error("workspace must be a listable directory");
  }
}

async function persist(path: string, state: WorkspaceRegistryState): Promise<void> {
  const parent = dirname(path);
  const temporary = join(parent, `.${basename(path)}.${process.pid}.${randomUUID()}.tmp`);
  let file: Awaited<ReturnType<typeof open>> | undefined;
  try {
    await mkdir(parent, { mode: 0o700, recursive: true });
    file = await open(temporary, "wx", 0o600);
    await file.writeFile(`${JSON.stringify(persistedState(state), null, 2)}\n`, "utf8");
    await file.sync();
    await file.close();
    file = undefined;
    await rename(temporary, path);
  } catch {
    await file?.close().catch(() => {});
    await unlink(temporary).catch(() => {});
    throw new Error("could not persist workspace registry");
  }
}

function persistedState(state: WorkspaceRegistryState): PersistedRegistry {
  return {
    version: registryVersion,
    ...(state.selectedId === undefined ? {} : { selectedId: state.selectedId }),
    workspaces: state.workspaces.map(({ id, root }) => ({ id, root })),
  };
}

function copyState(state: WorkspaceRegistryState): WorkspaceRegistryState {
  return {
    ...(state.selectedId === undefined ? {} : { selectedId: state.selectedId }),
    workspaces: state.workspaces.map((workspace) => ({ ...workspace })),
  };
}
