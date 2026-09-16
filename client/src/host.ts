import { join } from "node:path";

import {
  type BrowserCommand,
  type BrowserMessage,
  type Conversation,
  initialSnapshot,
  maximumRecentConversations,
  parseBrowserCommand,
  type Snapshot,
} from "./protocol.ts";
import { WorkspaceSupervisor, type WorkspaceState } from "./workspace-supervisor.ts";
import {
  type RegisteredWorkspace,
  WorkspaceRegistry,
  type WorkspaceRegistryState,
} from "./workspace-registry.ts";

export type HostOptions = {
  assetDirectory?: string;
  hostname?: string;
  oxArguments?: string[];
  oxCommand?: string;
  port?: number;
  registryPath?: string;
};

export type StartedHost = {
  stop(): Promise<void>;
  url: string;
};

type SocketData = Record<string, never>;

const mimeTypes = new Map([
  [".css", "text/css; charset=utf-8"],
  [".html", "text/html; charset=utf-8"],
  [".js", "text/javascript; charset=utf-8"],
  [".map", "application/json; charset=utf-8"],
  [".svg", "image/svg+xml"],
]);

export async function startHost(options: HostOptions = {}): Promise<StartedHost> {
  const assetDirectory = options.assetDirectory ?? join(import.meta.dir, "..", "public");
  const hostname = options.hostname ?? "127.0.0.1";
  // Keep the default outside browser-blocked and commonly claimed development
  // ports. Callers can select a different port with --port.
  const port = options.port ?? 41837;
  const sockets = new Set<Bun.ServerWebSocket<SocketData>>();
  const registry = options.registryPath
    ? await WorkspaceRegistry.load(options.registryPath)
    : await WorkspaceRegistry.loadDefault();
  const supervisors = new Map<string, WorkspaceSupervisor>();
  const unsubscribes = new Map<string, () => void>();
  let visibleRegistry = registry.state;
  let workspaceOperation: Promise<void> = Promise.resolve();
  let revision = 0;
  // The snapshot exists before the first supervisor subscribes, because a
  // workspace that starts early can publish while another is still starting.
  let snapshot = snapshotFor(revision, visibleRegistry, supervisors, undefined);
  await Promise.all(visibleRegistry.workspaces.map(startSupervisorQuietly));
  snapshot = snapshotFor(revision, visibleRegistry, supervisors, selectedState());

  function selectedState(): WorkspaceState | undefined {
    const selected = visibleRegistry.selectedId;
    return selected === undefined ? undefined : supervisors.get(selected)?.state;
  }

  // A publish caused by an unselected workspace cannot change the selected
  // detail, so it rebuilds only the catalog. Every host-driven publish passes no
  // ID and rebuilds the whole snapshot, which is what keeps registry changes and
  // redaction correct.
  function publish(workspaceId?: string): void {
    revision += 1;
    snapshot =
      workspaceId !== undefined && workspaceId !== visibleRegistry.selectedId
        ? { ...snapshot, revision, workspaces: browserWorkspaces(visibleRegistry, supervisors) }
        : snapshotFor(revision, visibleRegistry, supervisors, selectedState());
    for (const socket of sockets) {
      send(socket, snapshot);
    }
  }

  // The supervisor is registered and subscribed before its process exists, so
  // the entry reports starting for the whole launch rather than looking
  // unavailable until the process answers.
  async function startSupervisor(workspace: RegisteredWorkspace, diagnostics: string[] = []): Promise<void> {
    const supervisor = new WorkspaceSupervisor({
      arguments: options.oxArguments,
      command: options.oxCommand,
      diagnostics,
      workspace: workspace.root,
    });
    supervisors.set(workspace.id, supervisor);
    unsubscribes.set(workspace.id, supervisor.subscribe(() => publish(workspace.id)));
    publish();
    await supervisor.start();
  }

  // A workspace that cannot launch reports unavailable and refuses commands
  // rather than taking the host or the registration down with it. An explicit
  // restart is a user request, so it reports its failure instead.
  async function startSupervisorQuietly(workspace: RegisteredWorkspace): Promise<void> {
    await startSupervisor(workspace).catch(() => undefined);
  }

  // The supervisor stays in the map until stop resolves, so the entry reports
  // stopped rather than jumping straight to unavailable.
  async function stopSupervisor(id: string): Promise<void> {
    await supervisors.get(id)?.stop();
    unsubscribes.get(id)?.();
    unsubscribes.delete(id);
    supervisors.delete(id);
  }

  function serializeWorkspace<T>(operation: () => Promise<T>): Promise<T> {
    const running = workspaceOperation.then(operation, operation);
    workspaceOperation = running.then(
      () => undefined,
      () => undefined,
    );
    return running;
  }

  function performWorkspace(command: WorkspaceCommand): Promise<void> {
    return serializeWorkspace(async () => {
      switch (command.type) {
        case "register-workspace": {
          const result = await registry.register(command.path);
          // The new entry has to be visible before its supervisor starts
          // publishing, or its early transitions would name nothing.
          visibleRegistry = registry.state;
          await startSupervisorQuietly(result.workspace);
          break;
        }
        case "restart-workspace": {
          const workspace = registry.state.workspaces.find((entry) => entry.id === command.workspaceId);
          if (!workspace) {
            throw new Error("workspace is not registered");
          }
          const diagnostics = supervisors.get(workspace.id)?.state.diagnostics ?? [];
          await stopSupervisor(workspace.id);
          await startSupervisor(workspace, diagnostics);
          break;
        }
        case "select-workspace":
          if (!(await registry.select(command.workspaceId))) {
            return;
          }
          break;
        case "remove-workspace":
          await registry.remove(command.workspaceId);
          await stopSupervisor(command.workspaceId);
          break;
      }
      visibleRegistry = registry.state;
      publish();
    });
  }

  // A host that cannot listen must not leave the Ox processes it started
  // running, so the port failure reaches the caller with nothing spawned.
  let server: ReturnType<typeof Bun.serve<SocketData>>;
  try {
    server = Bun.serve<SocketData>({
      hostname,
      port,
      async fetch(request, server) {
        const url = new URL(request.url);
        if (url.pathname === "/health") {
          return Response.json({ status: "ok" });
        }
        if (url.pathname === "/socket") {
          if (!sameOrigin(request, url)) {
            return new Response("WebSocket origin must match the host", { status: 403 });
          }
          if (server.upgrade(request, { data: {} })) {
            return undefined;
          }
          return new Response("WebSocket upgrade required", { status: 426 });
        }
        if (request.method !== "GET" && request.method !== "HEAD") {
          return new Response("Method not allowed", { status: 405 });
        }
        const pathname = url.pathname === "/" ? "/index.html" : url.pathname;
        if (!pathname.startsWith("/") || pathname.includes("..")) {
          return new Response("Not found", { status: 404 });
        }
        const path = join(assetDirectory, pathname);
        const file = Bun.file(path);
        if (!(await file.exists())) {
          return new Response("Not found", { status: 404 });
        }
        const extension = pathname.slice(pathname.lastIndexOf("."));
        const headers = new Headers({
          "Cache-Control": "no-store",
          "Content-Type": mimeTypes.get(extension) ?? "application/octet-stream",
        });
        return new Response(request.method === "HEAD" ? null : file, { headers });
      },
      websocket: {
        open(socket) {
          sockets.add(socket);
          send(socket, snapshot);
        },
        message(socket, message) {
          if (typeof message !== "string") {
            send(socket, { type: "result", requestId: "unknown", ok: false, error: "invalid browser command" });
            return;
          }
          let value: unknown;
          try {
            value = JSON.parse(message);
          } catch {
            send(socket, { type: "result", requestId: "unknown", ok: false, error: "invalid browser command" });
            return;
          }
          const command = parseBrowserCommand(value);
          if (!command.ok) {
            const requestId = requestID(value);
            send(socket, { type: "result", requestId, ok: false, error: command.error });
            return;
          }
          const browserCommand = command.value;
          if (browserCommand.type === "ping") {
            send(socket, {
              type: "result",
              requestId: browserCommand.requestId,
              ok: true,
              value: { revision: snapshot.revision },
            });
            return;
          }
          void respond(socket, browserCommand.requestId, () => snapshot.revision, () => {
            switch (browserCommand.type) {
              case "register-workspace":
              case "remove-workspace":
              case "restart-workspace":
              case "select-workspace":
                return performWorkspace(browserCommand);
            }
            const supervisor = supervisors.get(browserCommand.workspaceId);
            if (!supervisor) {
              return Promise.reject(new Error("workspace is not active"));
            }
            const operation = perform(supervisor, browserCommand);
            // Opening or creating a conversation switches to its workspace, and
            // only once the supervisor has accepted it, so a refused open leaves
            // the browser on the workspace it was showing.
            if (browserCommand.type === "new-conversation" || browserCommand.type === "open-conversation") {
              const { requestId, workspaceId } = browserCommand;
              return operation.then(() => performWorkspace({ requestId, type: "select-workspace", workspaceId }));
            }
            return operation;
          });
        },
        close(socket) {
          sockets.delete(socket);
        },
      },
    });
  } catch (error) {
    await Promise.all([...supervisors.keys()].map(stopSupervisor));
    throw error;
  }

  return {
    async stop() {
      for (const socket of sockets) {
        socket.close();
      }
      server.stop(true);
      await workspaceOperation;
      await Promise.all([...supervisors.keys()].map(stopSupervisor));
    },
    url: server.url.toString().replace(/\/$/, ""),
  };
}

function snapshotFor(
  revision: number,
  registry: WorkspaceRegistryState,
  supervisors: Map<string, WorkspaceSupervisor>,
  workspace: WorkspaceState | undefined,
): Snapshot {
  return {
    ...initialSnapshot(
      browserWorkspaces(registry, supervisors),
      workspace === undefined ? undefined : browserWorkspace(workspace, registry),
      workspace?.authentication,
      workspace === undefined ? undefined : browserSessions(workspace),
    ),
    revision,
  };
}

function browserWorkspaces(
  registry: WorkspaceRegistryState,
  supervisors: Map<string, WorkspaceSupervisor>,
): Snapshot["workspaces"] {
  return {
    ...(registry.selectedId === undefined ? {} : { selectedId: registry.selectedId }),
    values: registry.workspaces.map(({ id, name }) => {
      const catalog = supervisors.get(id)?.catalog;
      return {
        awaiting: catalog?.awaiting ?? false,
        busy: catalog?.busy ?? false,
        conversations:
          id === registry.selectedId ? (catalog?.conversations ?? []) : recentConversations(catalog?.conversations ?? []),
        id,
        name,
        status: catalog?.status ?? "unavailable",
      };
    }),
  };
}

// Only the workspace the browser is showing pages its whole list. Ox orders a
// listing by recency and the browser re-lists on lifecycle changes, so a
// conversation that started waiting since the last listing can sit below the
// recent window; keeping every conversation the host holds active is what makes
// a waiting conversation always nameable.
function recentConversations(conversations: Conversation[]): Conversation[] {
  const recent = conversations.slice(0, maximumRecentConversations);
  return [...recent, ...conversations.slice(maximumRecentConversations).filter((entry) => entry.status !== "inactive")];
}

function browserWorkspace(
  workspace: WorkspaceState,
  registry: WorkspaceRegistryState,
): NonNullable<Snapshot["workspace"]> {
  const roots = registry.workspaces.map((entry) => entry.root).sort((left, right) => right.length - left.length);
  return {
    diagnostics: workspace.diagnostics.map((diagnostic) => redactWorkspaceRoots(diagnostic, roots)),
    mcpServerCount: workspace.mcpServerCount,
    name: workspace.name,
    promptCapabilities: workspace.promptCapabilities,
    status: workspace.status,
  };
}

function redactWorkspaceRoots(diagnostic: string, roots: string[]): string {
  return roots.reduce((redacted, root) => redacted.split(root).join("[workspace]"), diagnostic);
}

function browserSessions(workspace: WorkspaceState): Snapshot["sessions"] {
  return {
    ...(workspace.sessions.active === undefined
      ? {}
      : {
          active: {
            busy: workspace.sessions.active.busy,
            id: workspace.sessions.active.id,
            interactions: workspace.sessions.active.interactions,
            transcript: workspace.sessions.active.transcript,
          },
        }),
    ...(workspace.sessions.nextCursor === undefined ? {} : { nextCursor: workspace.sessions.nextCursor }),
    ...(workspace.sessions.selectedID === undefined ? {} : { selectedId: workspace.sessions.selectedID }),
  };
}

async function respond(
  socket: Bun.ServerWebSocket<SocketData>,
  requestId: string,
  revision: () => number,
  operation: () => Promise<void>,
): Promise<void> {
  try {
    await operation();
    send(socket, { type: "result", requestId, ok: true, value: { revision: revision() } });
  } catch (error) {
    send(socket, {
      type: "result",
      requestId,
      ok: false,
      error: (error instanceof Error ? error.message : "") || "request failed",
    });
  }
}

function perform(
  supervisor: WorkspaceSupervisor,
  command: Exclude<BrowserCommand, { type: "ping" } | WorkspaceCommand>,
): Promise<void> {
  switch (command.type) {
    case "authenticate":
      return supervisor.authenticate(command.methodId);
    case "login":
      return supervisor.login(command.methodId, command.credential);
    case "logout":
      return supervisor.logout();
    case "new-conversation":
      return supervisor.newConversation();
    case "refresh-history":
      return supervisor.refreshSessions();
    case "next-history-page":
      return supervisor.nextSessionPage();
    case "open-conversation":
      return supervisor.openConversation(command.sessionId);
    case "close-conversation":
      return supervisor.closeSession(command.sessionId);
    case "delete-conversation":
      return supervisor.deleteConversation(command.sessionId);
    case "set-mcp-servers":
      return supervisor.setMCPServers(command.mcpServers);
    case "prompt":
      return supervisor.prompt(command.sessionId, command.prompt);
    case "cancel-prompt":
      return supervisor.cancelPrompt(command.sessionId);
    case "set-config-option":
      return supervisor.setConfigOption(command.sessionId, command.configId, command.value);
    case "resolve-permission":
      supervisor.resolvePermission(command.sessionId, command.interactionId, command.optionId);
      return Promise.resolve();
    case "resolve-elicitation":
      supervisor.resolveElicitation(command.sessionId, command.interactionId, command.action, command.content);
      return Promise.resolve();
  }
}

type WorkspaceCommand = Extract<
  BrowserCommand,
  { type: "register-workspace" | "remove-workspace" | "restart-workspace" | "select-workspace" }
>;

function requestID(value: unknown): string {
  if (
    typeof value === "object" &&
    value !== null &&
    "requestId" in value &&
    typeof value.requestId === "string" &&
    value.requestId.length > 0 &&
    value.requestId.length <= 128
  ) {
    return value.requestId;
  }
  return "unknown";
}

function sameOrigin(request: Request, url: URL): boolean {
  const origin = request.headers.get("origin");
  const expected = new URL(url);
  expected.protocol = expected.protocol === "https:" || expected.protocol === "wss:" ? "https:" : "http:";
  return origin === expected.origin;
}

function send(socket: Bun.ServerWebSocket<SocketData>, message: BrowserMessage): void {
  socket.send(JSON.stringify(message));
}

if (import.meta.main) {
  main().catch((error: unknown) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exit(1);
  });
}

function argument(name: string): string | undefined {
  const index = process.argv.indexOf(name);
  return index === -1 ? undefined : process.argv[index + 1];
}

function argumentsFor(name: string): string[] {
  return process.argv.flatMap((argument, index) => (argument === name ? [process.argv[index + 1] ?? ""] : []));
}

async function main(): Promise<void> {
  const port = argument("--port");
  const host = await startHost({
    hostname: argument("--host"),
    oxArguments: argumentsFor("--ox-arg"),
    oxCommand: argument("--ox"),
    port: port === undefined ? undefined : Number.parseInt(port, 10),
  });
  process.stdout.write(`Ox browser host listening at ${host.url}\n`);
  const stop = () => {
    void host.stop().finally(() => process.exit());
  };
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
}
