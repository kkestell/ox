import * as acp from "@agentclientprotocol/sdk";
import { spawn, type ChildProcess, type ChildProcessWithoutNullStreams } from "node:child_process";
import { basename } from "node:path";
import { Readable, Writable } from "node:stream";

import { maximumSessions } from "./protocol.ts";
import type { MCPServer, PromptCapabilities, PromptContentBlock, SessionTranscript } from "./protocol.ts";
import { FilesystemExecutor } from "./filesystem-executor.ts";
import { SessionController } from "./session-controller.ts";
import { TerminalExecutor } from "./terminal-executor.ts";
import { canonicalWorkspace } from "./workspace-registry.ts";

const maximumDiagnostics = 16;
const maximumDiagnosticLength = 512;
const initializationTimeoutMilliseconds = 2_000;
const sessionLockedMetadataKey = "kkestell.ox/sessionLocked";

export type WorkspaceStatus = "starting" | "ready" | "unavailable" | "stopped";

export type AuthenticationMethod = {
  description?: string;
  id: string;
  name: string;
  type: "agent" | "terminal";
};

export type AuthenticationState = {
  error?: string;
  logoutAvailable: boolean;
  methods: AuthenticationMethod[];
  status: "required" | "working" | "authenticated" | "unavailable";
};

export type WorkspaceState = {
  authentication: AuthenticationState;
  awaiting: boolean;
  busy: boolean;
  diagnostics: string[];
  mcpServerCount: number;
  name: string;
  promptCapabilities: PromptCapabilities;
  sessions: SessionSelection;
  status: WorkspaceStatus;
};

/** What the workspace catalog shows without reading a session transcript. */
export type WorkspaceCatalog = {
  awaiting: boolean;
  busy: boolean;
  conversations: SessionSummary[];
  name: string;
  status: WorkspaceStatus;
};

/** The conversation the workspace is showing; the list itself lives on the catalog. */
export type SessionSelection = {
  active?: { busy: boolean; id: string; interactions: import("./protocol.ts").PendingInteraction[]; transcript: SessionTranscript };
  nextCursor?: string;
  selectedID?: string;
};

type SessionState = SessionSelection & { values: SessionSummary[] };

// Busy and awaiting are derived from the active prompts and the controllers'
// pending interactions rather than stored, and the conversation list the
// catalog publishes is stored here rather than on the published selection.
type SupervisorState = Omit<WorkspaceState, "awaiting" | "busy" | "sessions"> & { sessions: SessionState };

export type SessionSummary = {
  awaiting?: boolean;
  id: string;
  status: "inactive" | "locked" | "loading" | "active";
  title?: string;
  updatedAt?: string;
};

type TerminalAuthenticationMethod = AuthenticationMethod & {
  arguments: string[];
  environment: Record<string, string>;
};

type SessionCapabilities = {
  close: boolean;
  delete: boolean;
  list: boolean;
  load: boolean;
  resume: boolean;
};

const unauthenticated: AuthenticationState = { logoutAvailable: false, methods: [], status: "unavailable" };

export type WorkspaceSupervisorOptions = {
  arguments?: string[];
  command?: string;
  // A restart seeds the diagnostics of the supervisor it replaces, so the
  // reason a workspace stopped survives the recovery that follows it.
  diagnostics?: string[];
  environment?: NodeJS.ProcessEnv;
  workspace: string;
};

export class WorkspaceSupervisor {
  readonly workspace: string;

  #child?: ChildProcessWithoutNullStreams;
  #connection?: acp.ClientConnection;
  #controllers = new Map<string, SessionController>();
  #filesystem: FilesystemExecutor;
  #terminal: TerminalExecutor;
  #activePrompts = new Map<string, Promise<void>>();
  #diagnosticRemainder = "";
  #childTermination?: Promise<void>;
  #authenticationMethods = new Map<string, AuthenticationMethod | TerminalAuthenticationMethod>();
  #authenticationOperation: Promise<void> = Promise.resolve();
  #launchFailed = false;
  #listeners = new Set<() => void>();
  #mcpServers: acp.McpServer[] = [];
  #sessionOperation: Promise<void> = Promise.resolve();
  #sessionCapabilities: SessionCapabilities = { close: false, delete: false, list: false, load: false, resume: false };
  #state: SupervisorState = {
    authentication: unauthenticated,
    diagnostics: [],
    mcpServerCount: 0,
    name: "Workspace",
    promptCapabilities: { audio: false, embeddedContext: false, image: false },
    sessions: { values: [] },
    status: "starting",
  };
  #stop?: Promise<void>;
  #stopping = false;
  #terminating = false;

  readonly #options: Required<Omit<WorkspaceSupervisorOptions, "diagnostics" | "workspace">>;

  // Construction is synchronous so the host can register and subscribe a
  // supervisor before its process exists, which is what makes the starting and
  // stopped statuses observable.
  constructor(options: WorkspaceSupervisorOptions) {
    const workspace = options.workspace;
    this.workspace = workspace;
    this.#options = {
      arguments: options.arguments ?? [],
      command: options.command ?? "ox",
      environment: options.environment ?? process.env,
    };
    this.#state = {
      ...this.#state,
      diagnostics: (options.diagnostics ?? []).slice(-maximumDiagnostics),
      name: basename(workspace) || "Workspace",
    };
    this.#filesystem = new FilesystemExecutor(workspace, (sessionID) => this.#controllers.has(sessionID));
    this.#terminal = new TerminalExecutor(workspace, (sessionID) => this.#controllers.has(sessionID));
  }

  get state(): WorkspaceState {
    const active = this.activeSession();
    return {
      authentication: {
        ...this.#state.authentication,
        methods: this.#state.authentication.methods.map((method) => ({ ...method })),
      },
      awaiting: [...this.#controllers.values()].some((controller) => controller.awaiting),
      busy: this.#activePrompts.size > 0,
      diagnostics: [...this.#state.diagnostics],
      mcpServerCount: this.#state.mcpServerCount,
      name: this.#state.name,
      promptCapabilities: { ...this.#state.promptCapabilities },
      sessions: {
        ...(active === undefined ? {} : { active }),
        ...(this.#state.sessions.nextCursor === undefined ? {} : { nextCursor: this.#state.sessions.nextCursor }),
        ...(this.#state.sessions.selectedID === undefined ? {} : { selectedID: this.#state.sessions.selectedID }),
      },
      status: this.#state.status,
    };
  }

  get catalog(): WorkspaceCatalog {
    return {
      awaiting: [...this.#controllers.values()].some((controller) => controller.awaiting),
      busy: this.#activePrompts.size > 0,
      conversations: this.#state.sessions.values.map((session) => ({
        ...session,
        ...(this.#controllers.get(session.id)?.awaiting ? { awaiting: true } : {}),
      })),
      name: this.#state.name,
      status: this.#state.status,
    };
  }

  subscribe(listener: () => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  async stop(): Promise<void> {
    this.#stop ??= this.stopImpl();
    await this.#stop;
  }

  authenticate(methodID: string): Promise<void> {
    return this.serializeAuthentication(async () => {
      await this.authenticateImpl(methodID);
      await this.serializeSessions(() => this.ensureDefaultConversation());
    });
  }

  login(methodID: string, credential: string): Promise<void> {
    return this.serializeAuthentication(async () => {
      await this.loginImpl(methodID, credential);
      await this.serializeSessions(() => this.ensureDefaultConversation());
    });
  }

  logout(): Promise<void> {
    return this.serializeAuthentication(() => this.logoutImpl());
  }

  /** Browser-safe state Ox has routed to an active session. */
  sessionTranscript(sessionID: string): SessionTranscript | undefined {
    return this.#controllers.get(sessionID)?.transcript;
  }

  newConversation(): Promise<void> {
    return this.serializeSessions(() => this.newSessionImpl());
  }

  newSession(mcpServers: MCPServer[]): Promise<void> {
    return this.serializeSessions(() => this.newSessionImpl(mcpServers));
  }

  refreshSessions(): Promise<void> {
    return this.serializeSessions(() => this.refreshSessionsImpl());
  }

  nextSessionPage(): Promise<void> {
    return this.serializeSessions(() => this.nextSessionPageImpl());
  }

  openConversation(sessionID: string): Promise<void> {
    return this.serializeSessions(async () => {
      if (this.#controllers.has(sessionID)) {
        this.selectSession(sessionID);
        return;
      }
      await this.activateSession(sessionID, "load");
    });
  }

  loadSession(sessionID: string, mcpServers: MCPServer[]): Promise<void> {
    return this.serializeSessions(() => this.activateSession(sessionID, "load", mcpServers));
  }

  /** Protocol-only coverage; browser navigation always opens with replay. */
  resumeSession(sessionID: string, mcpServers: MCPServer[]): Promise<void> {
    return this.serializeSessions(() => this.activateSession(sessionID, "resume", mcpServers));
  }

  closeSession(sessionID: string): Promise<void> {
    return this.serializeSessions(() => this.closeSessionImpl(sessionID));
  }

  deleteConversation(sessionID: string): Promise<void> {
    return this.serializeSessions(() => this.deleteConversationImpl(sessionID));
  }

  deleteSession(sessionID: string): Promise<void> {
    return this.serializeSessions(() => this.deleteSessionImpl(sessionID));
  }

  setMCPServers(mcpServers: MCPServer[]): Promise<void> {
    return this.serializeSessions(async () => {
      this.#mcpServers = mcpServersForACP(mcpServers);
      this.#state = { ...this.#state, mcpServerCount: this.#mcpServers.length };
      this.publish();
    });
  }

  selectSession(sessionID: string): void {
    if (!this.#controllers.has(sessionID)) {
      throw new Error("session is not active");
    }
    this.setSessions({ ...this.#state.sessions, selectedID: sessionID });
  }

  // Turn ownership is session-local: the in-flight request is the session's
  // turn, so a second prompt is refused here while other sessions on the same
  // connection keep prompting.
  prompt(sessionID: string, prompt: PromptContentBlock[]): Promise<void> {
    const controller = this.#controllers.get(sessionID);
    if (!controller) {
      return Promise.reject(new Error("session is not active"));
    }
    if (this.#activePrompts.has(sessionID)) {
      return Promise.reject(new Error("session already has an active prompt"));
    }
    if (!prompt.every((block) => this.supportsPromptBlock(block))) {
      return Promise.reject(new Error("Ox does not support one or more prompt blocks"));
    }
    controller.appendPrompt(prompt);
    const running = this.readyConnection().agent
      .request(acp.methods.agent.session.prompt, { prompt, sessionId: sessionID })
      .then(() => undefined);
    this.#activePrompts.set(sessionID, running);
    this.publish();
    void running.then(
      () => this.finishPrompt(sessionID, running),
      () => this.finishPrompt(sessionID, running),
    );
    return running;
  }

  async cancelPrompt(sessionID: string): Promise<void> {
    if (!this.#activePrompts.has(sessionID)) {
      throw new Error("session has no active prompt");
    }
    this.#controllers.get(sessionID)?.cancelInteractions();
    await this.readyConnection().agent.notify(acp.methods.agent.session.cancel, { sessionId: sessionID });
  }

  resolvePermission(sessionID: string, interactionID: string, optionID: string): void {
    const controller = this.#controllers.get(sessionID);
    if (!controller) throw new Error("session is not active");
    controller.resolvePermission(interactionID, optionID);
    this.publish();
  }

  resolveElicitation(sessionID: string, interactionID: string, action: "accept" | "decline" | "cancel", content?: Record<string, import("./protocol.ts").FormValue>): void {
    const controller = this.#controllers.get(sessionID);
    if (!controller) throw new Error("session is not active");
    controller.resolveElicitation(interactionID, action, content);
    this.publish();
  }

  async setConfigOption(sessionID: string, configID: string, value: string): Promise<void> {
    const configuration = this.#controllers.get(sessionID)?.transcript.configuration ?? [];
    const option = configuration.find((candidate) => candidate.id === configID);
    if (!option) {
      throw new Error("unknown session configuration option");
    }
    if (!option.options.some((candidate) => candidate.value === value)) {
      throw new Error("unsupported session configuration value");
    }
    const response = await this.readyConnection().agent.request(acp.methods.agent.session.setConfigOption, {
      configId: configID,
      sessionId: sessionID,
      value,
    });
    this.#controllers.get(sessionID)?.replaceConfiguration(response.configOptions);
    this.publish();
  }

  private async stopImpl(): Promise<void> {
    this.#stopping = true;
    this.#connection?.close();
    this.#activePrompts.clear();
    await this.#terminal.stop();
    await this.terminateChild();
    this.clearActiveSessions();
    this.setAuthentication(unauthenticated);
    this.setState("stopped");
  }

  async start(): Promise<void> {
    // A spawn into a missing cwd reports ENOENT against the executable path,
    // which would blame Ox for a directory the user deleted.
    try {
      await canonicalWorkspace(this.workspace);
    } catch (error) {
      this.unavailable(message(error));
      return;
    }
    const child = spawn(this.#options.command, this.#options.arguments, {
      cwd: this.workspace,
      env: this.#options.environment,
      stdio: ["pipe", "pipe", "pipe"],
    });
    this.#child = child;
    let childTerminated!: () => void;
    const termination = new Promise<void>((resolve) => {
      childTerminated = resolve;
    });
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk: string) => this.recordStderr(chunk));
    child.once("error", (error) => {
      this.#launchFailed = true;
      this.unavailable(`Could not start Ox: ${message(error)}`);
      childTerminated();
    });
    child.once("exit", (code, signal) => {
      this.recordDiagnostic(this.#diagnosticRemainder);
      this.#diagnosticRemainder = "";
      if (!this.#stopping && !this.#terminating) {
        this.recordDiagnostic(signal ? `Ox exited from ${signal}` : `Ox exited with code ${code ?? "unknown"}`);
        this.setAuthentication(unauthenticated);
        this.clearActiveSessions();
        this.setState("unavailable");
      }
      childTerminated();
    });

    const stream = acp.ndJsonStream(
      Writable.toWeb(child.stdin),
      Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
    );
    const connection = acp
      .client({ name: "ox-browser-client" })
      .onRequest(acp.methods.client.session.requestPermission, ({ params, signal }) => this.requestPermission(params, signal))
      .onRequest(acp.methods.client.elicitation.create, ({ params, signal }) => this.requestElicitation(params, signal))
      .onRequest(acp.methods.client.fs.readTextFile, ({ params, signal }) => this.#filesystem.read(params, signal))
      .onRequest(acp.methods.client.fs.writeTextFile, ({ params, signal }) => this.#filesystem.write(params, signal))
      .onRequest(acp.methods.client.terminal.create, ({ params, signal }) => this.#terminal.create(params, signal))
      .onRequest(acp.methods.client.terminal.output, ({ params, signal }) => this.#terminal.output(params, signal))
      .onRequest(acp.methods.client.terminal.waitForExit, ({ params, signal }) => this.#terminal.wait(params, signal))
      .onRequest(acp.methods.client.terminal.kill, ({ params, signal }) => this.#terminal.kill(params, signal))
      .onRequest(acp.methods.client.terminal.release, ({ params, signal }) => this.#terminal.release(params, signal))
      .onNotification(acp.methods.client.session.update, ({ params }) => this.routeSessionUpdate(params))
      .connect(stream);
    this.#connection = connection;
    void connection.closed.then(() => {
      if (!this.#stopping && this.#state.status !== "unavailable") {
        this.unavailable("Ox ACP connection closed");
      }
    });
    let timer!: ReturnType<typeof setTimeout>;
    const initializationTimeout = new Promise<"timed out">((resolve) => {
      timer = setTimeout(() => resolve("timed out"), initializationTimeoutMilliseconds);
    });
    const initialized = await Promise.race([
      connection.agent
        .request(acp.methods.agent.initialize, {
          clientCapabilities: {
            auth: { terminal: true },
            elicitation: { form: {} },
            fs: { readTextFile: true, writeTextFile: true },
            terminal: true,
          },
          clientInfo: { name: "ox-browser-client", version: "0" },
          protocolVersion: acp.PROTOCOL_VERSION,
        })
        .then(
          (response) => {
            this.recordAuthentication(response);
            return true;
          },
          (error) => {
            if (!this.#stopping && !this.#terminating) {
              this.unavailable(`Could not initialize Ox: ${message(error)}`);
            }
            return false;
          },
      ),
      termination.then(() => false),
      initializationTimeout,
    ]);
    clearTimeout(timer);
    if (initialized === "timed out" && !this.#stopping) {
      this.unavailable("Ox did not initialize within 2 seconds");
      this.#connection?.close();
      // Every caller of stop awaits the same memoized termination.
      void this.terminateChild();
      return;
    }
    if (initialized && !this.#stopping && this.#state.status !== "unavailable") {
      this.setState("ready");
      await this.authenticateStoredCredential();
      if (this.#state.authentication.status === "authenticated") {
        await this.serializeSessions(() => this.ensureDefaultConversation()).catch((error) => {
          this.recordDiagnostic(`Could not open the default conversation: ${message(error)}`);
        });
      } else if (this.#sessionCapabilities.list) {
        void this.refreshSessions().catch((error) => this.recordDiagnostic(`Could not list sessions: ${message(error)}`));
      }
    }
  }

  private recordStderr(chunk: string): void {
    const lines = (this.#diagnosticRemainder + chunk).split(/\r?\n/);
    this.#diagnosticRemainder = lines.pop() ?? "";
    for (const line of lines) {
      this.recordDiagnostic(line);
    }
  }

  private recordDiagnostic(value: string): void {
    const diagnostic = redact(value.trim());
    if (!diagnostic) {
      return;
    }
    const diagnostics = [...this.#state.diagnostics, diagnostic.slice(-maximumDiagnosticLength)];
    this.#state = { ...this.#state, diagnostics: diagnostics.slice(-maximumDiagnostics) };
    this.publish();
  }

  private unavailable(diagnostic: string): void {
    this.recordDiagnostic(diagnostic);
    this.setAuthentication(unauthenticated);
    this.clearActiveSessions();
    this.setState("unavailable");
  }

  private setState(status: WorkspaceStatus): void {
    if (this.#state.status === status) {
      return;
    }
    this.#state = { ...this.#state, status };
    this.publish();
  }

  private recordAuthentication(response: acp.InitializeResponse): void {
    const capabilities = response.agentCapabilities;
    const promptCapabilities = capabilities?.promptCapabilities;
    this.#state = {
      ...this.#state,
      promptCapabilities: {
        audio: promptCapabilities?.audio === true,
        embeddedContext: promptCapabilities?.embeddedContext === true,
        image: promptCapabilities?.image === true,
      },
    };
    const sessionCapabilities = capabilities?.sessionCapabilities;
    this.#sessionCapabilities = {
      close: sessionCapabilities?.close != null,
      delete: sessionCapabilities?.delete != null,
      list: sessionCapabilities?.list != null,
      load: capabilities?.loadSession === true,
      resume: sessionCapabilities?.resume != null,
    };
    this.#authenticationMethods.clear();
    for (const method of response.authMethods ?? []) {
      if (!method || typeof method.id !== "string" || !method.id || typeof method.name !== "string" || !method.name) {
        continue;
      }
      if ("type" in method && method.type === "terminal") {
        const terminal: TerminalAuthenticationMethod = {
          arguments: method.args ?? [],
          description: method.description || undefined,
          environment: method.env ?? {},
          id: method.id,
          name: method.name,
          type: "terminal",
        };
        this.#authenticationMethods.set(terminal.id, terminal);
        continue;
      }
      if (!("type" in method)) {
        const agent: AuthenticationMethod = {
          description: method.description || undefined,
          id: method.id,
          name: method.name,
          type: "agent",
        };
        this.#authenticationMethods.set(agent.id, agent);
      }
    }
    this.setAuthentication({
      logoutAvailable: response.agentCapabilities?.auth?.logout != null,
      methods: [...this.#authenticationMethods.values()].map(({ id, name, type, description }) => ({
        ...(description === undefined ? {} : { description }),
        id,
        name,
        type,
      })),
      status: "required",
    });
  }

  private serializeAuthentication(operation: () => Promise<void>): Promise<void> {
    const next = this.#authenticationOperation.then(() => operation());
    this.#authenticationOperation = next.catch(() => {});
    return next;
  }

  private serializeSessions(operation: () => Promise<void>): Promise<void> {
    const next = this.#sessionOperation.then(() => operation());
    this.#sessionOperation = next.catch(() => {});
    return next;
  }

  private async newSessionImpl(mcpServers?: MCPServer[]): Promise<void> {
    const response = await this.readyConnection().agent.request(acp.methods.agent.session.new, {
      cwd: this.workspace,
      mcpServers: mcpServers === undefined ? this.#mcpServers : mcpServersForACP(mcpServers),
    });
    const controller = new SessionController(response.sessionId);
    controller.replaceConfiguration(response.configOptions);
    this.#controllers.set(response.sessionId, controller);
    this.setSessions({ ...this.withSessionStatus(response.sessionId, "active"), selectedID: response.sessionId });
    if (this.#sessionCapabilities.list) {
      await this.refreshSessionsImpl();
    }
  }

  private async ensureDefaultConversation(): Promise<void> {
    if (this.#controllers.size > 0 || this.#state.authentication.status !== "authenticated") {
      return;
    }
    if (this.#sessionCapabilities.list) {
      await this.refreshSessionsImpl();
      const newest = this.#state.sessions.values.find((session) => session.status !== "locked");
      if (newest && this.#sessionCapabilities.load) {
        await this.activateSession(newest.id, "load");
        return;
      }
    }
    await this.newSessionImpl();
  }

  private async refreshSessionsImpl(): Promise<void> {
    this.requireSessionCapability("list");
    const response = await this.readyConnection().agent.request(acp.methods.agent.session.list, { cwd: this.workspace });
    this.setSessions({
      nextCursor: response.nextCursor ?? undefined,
      selectedID: this.#state.sessions.selectedID,
      values: this.mergeActiveSessions(this.summaries(response.sessions)),
    });
  }

  private async nextSessionPageImpl(): Promise<void> {
    this.requireSessionCapability("list");
    const cursor = this.#state.sessions.nextCursor;
    if (!cursor) {
      throw new Error("there are no more sessions");
    }
    const response = await this.readyConnection().agent.request(acp.methods.agent.session.list, {
      cursor,
      cwd: this.workspace,
    });
    const seen = new Set(this.#state.sessions.values.map((session) => session.id));
    const added = this.summaries(response.sessions).filter((session) => !seen.has(session.id));
    this.setSessions({
      nextCursor: response.nextCursor ?? undefined,
      selectedID: this.#state.sessions.selectedID,
      values: [...this.#state.sessions.values, ...added],
    });
  }

  private async activateSession(sessionID: string, operation: "load" | "resume", mcpServers?: MCPServer[]): Promise<void> {
    this.requireSessionCapability(operation);
    if (this.#controllers.has(sessionID)) {
      throw new Error("session is already active");
    }
    const connection = this.readyConnection();
    const knownSession = this.#state.sessions.values.some((session) => session.id === sessionID);
    const previousSessions = this.#state.sessions;
    // Install this route before session/load so replay notifications cannot win
    // the race with its successful response.
    this.#controllers.set(sessionID, new SessionController(sessionID));
    this.setSessions(this.withSessionStatus(sessionID, "loading"));
    try {
      const request = {
        cwd: this.workspace,
        mcpServers: mcpServers === undefined ? this.#mcpServers : mcpServersForACP(mcpServers),
        sessionId: sessionID,
      };
      if (operation === "load") {
        const response = (await connection.agent.request(acp.methods.agent.session.load, request)) as acp.LoadSessionResponse;
        this.#controllers.get(sessionID)?.replaceConfiguration(response.configOptions);
      } else {
        const response = (await connection.agent.request(acp.methods.agent.session.resume, request)) as acp.ResumeSessionResponse;
        this.#controllers.get(sessionID)?.replaceConfiguration(response.configOptions);
      }
    } catch (error) {
      this.#controllers.delete(sessionID);
      this.setSessions(knownSession ? previousSessions : this.removeSession(sessionID));
      throw error;
    }
    this.setSessions({ ...this.withSessionStatus(sessionID, "active"), selectedID: sessionID });
    if (this.#sessionCapabilities.list) {
      await this.refreshSessionsImpl();
    }
  }

  private async closeSessionImpl(sessionID: string): Promise<void> {
    this.requireSessionCapability("close");
    if (!this.#controllers.has(sessionID)) {
      throw new Error("session is not active");
    }
    this.#controllers.get(sessionID)?.cancelInteractions();
    await this.#terminal.releaseSession(sessionID);
    await this.readyConnection().agent.request(acp.methods.agent.session.close, { sessionId: sessionID });
    this.#controllers.delete(sessionID);
    this.#activePrompts.delete(sessionID);
    this.setSessions(this.withoutSession(sessionID));
    if (this.#sessionCapabilities.list) {
      await this.refreshSessionsImpl();
    }
  }

  private async deleteSessionImpl(sessionID: string): Promise<void> {
    this.requireSessionCapability("delete");
    if (this.#controllers.has(sessionID)) {
      throw new Error("cannot delete an active session");
    }
    await this.readyConnection().agent.request(acp.methods.agent.session.delete, { sessionId: sessionID });
    this.setSessions(this.removeSession(sessionID));
    if (this.#sessionCapabilities.list) {
      await this.refreshSessionsImpl();
    }
  }

  private async deleteConversationImpl(sessionID: string): Promise<void> {
    if (this.#controllers.has(sessionID)) {
      await this.closeSessionImpl(sessionID);
    }
    await this.deleteSessionImpl(sessionID);
  }

  private supportsPromptBlock(block: PromptContentBlock): boolean {
    switch (block.type) {
      case "text":
      case "resource_link":
        return true;
      case "image":
        return this.#state.promptCapabilities.image;
      case "audio":
        return this.#state.promptCapabilities.audio;
      case "resource":
        return this.#state.promptCapabilities.embeddedContext;
    }
  }

  private finishPrompt(sessionID: string, running: Promise<void>): void {
    if (this.#activePrompts.get(sessionID) !== running) return;
    this.#activePrompts.delete(sessionID);
    this.publish();
  }

  private summaries(sessions: acp.SessionInfo[]): SessionSummary[] {
    return sessions.map((session) => ({
      id: session.sessionId,
      status: this.#controllers.has(session.sessionId) ? "active" : session._meta?.[sessionLockedMetadataKey] === true ? "locked" : "inactive",
      ...(session.title ? { title: session.title } : {}),
      ...(session.updatedAt ? { updatedAt: session.updatedAt } : {}),
    }));
  }

  private routeSessionUpdate(notification: acp.SessionNotification): void {
    const controller = this.#controllers.get(notification.sessionId);
    if (!controller) return;
    controller.accept(notification.update);
    this.publish();
  }

  private requestPermission(request: acp.RequestPermissionRequest, signal: AbortSignal): Promise<acp.RequestPermissionResponse> {
    const controller = this.#controllers.get(request.sessionId);
    return controller ? controller.requestPermission(request, signal, () => this.publish()) : Promise.resolve({ outcome: { outcome: "cancelled" } });
  }

  private requestElicitation(request: acp.CreateElicitationRequest, signal: AbortSignal): Promise<acp.CreateElicitationResponse> {
    if (!("sessionId" in request) || typeof request.sessionId !== "string") return Promise.resolve({ action: "cancel" });
    const controller = this.#controllers.get(request.sessionId);
    return controller ? controller.requestElicitation(request, signal, () => this.publish()) : Promise.resolve({ action: "cancel" });
  }

  private activeSession(): SessionSelection["active"] {
    const id = this.#state.sessions.selectedID;
    if (id === undefined) return undefined;
    const controller = this.#controllers.get(id);
    if (controller === undefined) return undefined;
    return { busy: this.#activePrompts.has(id), id, interactions: controller.interactions, transcript: controller.transcript };
  }

  private mergeActiveSessions(values: SessionSummary[]): SessionSummary[] {
    const seen = new Set(values.map((session) => session.id));
    for (const session of this.#state.sessions.values) {
      if (this.#controllers.has(session.id) && !seen.has(session.id)) {
        values.push({ ...session, status: "active" });
      }
    }
    return values;
  }

  private clearActiveSessions(): void {
    if (this.#controllers.size === 0 && this.#state.sessions.selectedID === undefined) {
      return;
    }
    for (const controller of this.#controllers.values()) controller.cancelInteractions();
    void this.#terminal.stop().catch((error) => this.recordDiagnostic(`Could not stop delegated terminals: ${message(error)}`));
    this.#controllers.clear();
    this.#activePrompts.clear();
    this.setSessions({
      ...this.#state.sessions,
      selectedID: undefined,
      values: this.#state.sessions.values.map((session) => ({ ...session, status: "inactive" })),
    });
  }

  private requireSessionCapability(capability: keyof SessionCapabilities): void {
    if (!this.#sessionCapabilities[capability]) {
      throw new Error(`Ox does not support session ${capability}`);
    }
  }

  private withSessionStatus(sessionID: string, status: SessionSummary["status"]): SessionState {
    let found = false;
    const values = this.#state.sessions.values.map((session) => {
      if (session.id !== sessionID) {
        return session;
      }
      found = true;
      return { ...session, status };
    });
    if (!found) {
      values.push({ id: sessionID, status });
    }
    return { ...this.#state.sessions, values };
  }

  private withoutSession(sessionID: string): SessionState {
    return {
      ...this.#state.sessions,
      ...(this.#state.sessions.selectedID === sessionID ? { selectedID: undefined } : {}),
      values: this.#state.sessions.values.map((session) =>
        session.id === sessionID ? { ...session, status: "inactive" } : session,
      ),
    };
  }

  private removeSession(sessionID: string): SessionState {
    return {
      ...this.#state.sessions,
      ...(this.#state.sessions.selectedID === sessionID ? { selectedID: undefined } : {}),
      values: this.#state.sessions.values.filter((session) => session.id !== sessionID),
    };
  }

  private setSessions(sessions: SessionState): void {
    // A snapshot carries at most the catalog bound, so the catalog holds no more
    // than that and stops offering further pages once it is full.
    const values = sessions.values.slice(0, maximumSessions);
    const nextCursor = values.length < maximumSessions ? sessions.nextCursor : undefined;
    this.#state = {
      ...this.#state,
      sessions: {
        ...(nextCursor === undefined ? {} : { nextCursor }),
        ...(sessions.selectedID === undefined ? {} : { selectedID: sessions.selectedID }),
        values,
      },
    };
    this.publish();
  }

  private async authenticateImpl(methodID: string): Promise<void> {
    const method = this.#authenticationMethods.get(methodID);
    if (!method || method.type !== "agent") {
      throw new Error("unsupported stored-credential authentication method");
    }
    const connection = this.readyConnection();
    this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "working" });
    try {
      await connection.agent.request(acp.methods.agent.authenticate, { methodId: method.id });
      this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "authenticated" });
    } catch {
      this.setAuthentication({ ...this.#state.authentication, error: "Authentication failed", status: "required" });
      throw new Error("authentication failed");
    }
  }

  private async authenticateStoredCredential(): Promise<void> {
    const method = [...this.#authenticationMethods.values()].find((candidate) => candidate.type === "agent");
    if (!method) return;
    try {
      await this.authenticateImpl(method.id);
    } catch {
      // A missing or expired keychain credential is ordinary startup state. The
      // browser should offer manual authentication without surfacing a failed
      // automatic attempt as an error.
      this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "required" });
    }
  }

  private async loginImpl(methodID: string, credential: string): Promise<void> {
    const method = this.#authenticationMethods.get(methodID);
    if (!isTerminalAuthenticationMethod(method)) {
      throw new Error("unsupported terminal authentication method");
    }
    const storedMethod = [...this.#authenticationMethods.values()].find((candidate) => candidate.type === "agent");
    if (!storedMethod) {
      throw new Error("Ox did not advertise stored-credential authentication");
    }
    this.readyConnection();
    const previousStatus = this.#state.authentication.status;
    this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "working" });
    try {
      await this.runLogin(method, credential);
    } catch {
      this.setAuthentication({
        ...this.#state.authentication,
        error: "OpenRouter login failed",
        status: previousStatus === "authenticated" ? "authenticated" : "required",
      });
      throw new Error("OpenRouter login failed");
    }
    await this.authenticateImpl(storedMethod.id);
  }

  private async logoutImpl(): Promise<void> {
    if (!this.#state.authentication.logoutAvailable) {
      throw new Error("Ox does not support logout");
    }
    const connection = this.readyConnection();
    const previousStatus = this.#state.authentication.status;
    this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "working" });
    try {
      await connection.agent.request(acp.methods.agent.logout, {});
      this.setAuthentication({ ...this.#state.authentication, error: undefined, status: "required" });
    } catch {
      const status = previousStatus === "authenticated" ? "authenticated" : "required";
      this.setAuthentication({ ...this.#state.authentication, error: "Logout failed", status });
      throw new Error("logout failed");
    }
  }

  private readyConnection(): acp.ClientConnection {
    if (this.#state.status !== "ready" || !this.#connection) {
      throw new Error("Ox is unavailable");
    }
    return this.#connection;
  }

  private async runLogin(method: TerminalAuthenticationMethod, credential: string): Promise<void> {
    const child = spawn(this.#options.command, [...this.#options.arguments, ...method.arguments], {
      cwd: this.workspace,
      env: { ...this.#options.environment, ...method.environment },
      stdio: ["pipe", "ignore", "ignore"],
    });
    try {
      // A login that fails before reading the credential closes the pipe, and an
      // unhandled stdin error would take down the host. The exit code decides.
      child.stdin.on("error", () => {});
      child.stdin.end(`${credential}\n`);
      const code = await exitCode(child);
      if (code !== 0) {
        throw new Error("login process exited unsuccessfully");
      }
    } finally {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill("SIGTERM");
      }
    }
  }

  private setAuthentication(authentication: AuthenticationState): void {
    this.#state = { ...this.#state, authentication };
    this.publish();
  }

  private publish(): void {
    for (const listener of this.#listeners) {
      listener();
    }
  }

  private terminateChild(): Promise<void> {
    this.#terminating = true;
    this.#childTermination ??= this.terminateChildImpl();
    return this.#childTermination;
  }

  private async terminateChildImpl(): Promise<void> {
    const child = this.#child;
    if (!child || this.#launchFailed || child.exitCode !== null || child.signalCode !== null) {
      return;
    }
    child.kill("SIGTERM");
    if (!(await exitsWithin(child, 2_000))) {
      child.kill("SIGKILL");
      await exitsWithin(child, 2_000);
    }
  }
}

function exitCode(child: ChildProcess): Promise<number | null> {
  if (child.exitCode !== null) {
    return Promise.resolve(child.exitCode);
  }
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code) => resolve(code));
  });
}

function isTerminalAuthenticationMethod(
  method: AuthenticationMethod | TerminalAuthenticationMethod | undefined,
): method is TerminalAuthenticationMethod {
  return method?.type === "terminal";
}

function mcpServersForACP(servers: MCPServer[]): acp.McpServer[] {
  return servers.map((server) =>
    server.transport === "http"
      ? {
          type: "http",
          name: server.name,
          url: server.url,
          headers: server.headers.map((header) => ({ ...header })),
        }
      : {
          name: server.name,
          command: server.command,
          args: [...server.args],
          env: server.env.map((variable) => ({ ...variable })),
        },
  );
}

function exitsWithin(child: ChildProcessWithoutNullStreams, milliseconds: number): Promise<boolean> {
  if (child.exitCode !== null || child.signalCode !== null) {
    return Promise.resolve(true);
  }
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), milliseconds);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve(true);
    });
  });
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : "unknown error";
}

function redact(value: string): string {
  return value
    .replace(
      /((?:["']?authorization["']?)\s*:\s*bearer\s+)(?:"[^"]*"|'[^']*'|\S+)/gi,
      "$1[redacted]",
    )
    .replace(
      /((?:["']?(?:api[_-]?key|token|secret|password)["']?)\s*(?:=|:)\s*(?:bearer\s+)?)(?:"[^"]*"|'[^']*'|[^\s,}\]]+)/gi,
      "$1[redacted]",
    );
}
