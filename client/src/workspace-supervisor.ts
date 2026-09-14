import * as acp from "@agentclientprotocol/sdk";
import { spawn, type ChildProcess, type ChildProcessWithoutNullStreams } from "node:child_process";
import { realpath, stat } from "node:fs/promises";
import { Readable, Writable } from "node:stream";

const maximumDiagnostics = 16;
const maximumDiagnosticLength = 512;
const initializationTimeoutMilliseconds = 2_000;

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
  diagnostics: string[];
  status: WorkspaceStatus;
};

type TerminalAuthenticationMethod = AuthenticationMethod & {
  arguments: string[];
  environment: Record<string, string>;
};

const unauthenticated: AuthenticationState = { logoutAvailable: false, methods: [], status: "unavailable" };

export type WorkspaceSupervisorOptions = {
  arguments?: string[];
  command?: string;
  environment?: NodeJS.ProcessEnv;
  workspace: string;
};

export class WorkspaceSupervisor {
  readonly workspace: string;

  #child?: ChildProcessWithoutNullStreams;
  #connection?: acp.ClientConnection;
  #diagnosticRemainder = "";
  #childTermination?: Promise<void>;
  #authenticationMethods = new Map<string, AuthenticationMethod | TerminalAuthenticationMethod>();
  #authenticationOperation: Promise<void> = Promise.resolve();
  #launchFailed = false;
  #listeners = new Set<(state: WorkspaceState) => void>();
  #state: WorkspaceState = {
    authentication: unauthenticated,
    diagnostics: [],
    status: "starting",
  };
  #stop?: Promise<void>;
  #stopping = false;
  #terminating = false;

  private constructor(
    workspace: string,
    private readonly options: Required<Omit<WorkspaceSupervisorOptions, "workspace">>,
  ) {
    this.workspace = workspace;
  }

  static async start(options: WorkspaceSupervisorOptions): Promise<WorkspaceSupervisor> {
    const workspace = await canonicalWorkspace(options.workspace);
    const supervisor = new WorkspaceSupervisor(workspace, {
      arguments: options.arguments ?? [],
      command: options.command ?? "ox",
      environment: options.environment ?? process.env,
    });
    await supervisor.start();
    return supervisor;
  }

  get state(): WorkspaceState {
    return {
      authentication: {
        ...this.#state.authentication,
        methods: this.#state.authentication.methods.map((method) => ({ ...method })),
      },
      diagnostics: [...this.#state.diagnostics],
      status: this.#state.status,
    };
  }

  subscribe(listener: (state: WorkspaceState) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  async stop(): Promise<void> {
    this.#stop ??= this.stopImpl();
    await this.#stop;
  }

  authenticate(methodID: string): Promise<void> {
    return this.serializeAuthentication(() => this.authenticateImpl(methodID));
  }

  login(methodID: string, credential: string): Promise<void> {
    return this.serializeAuthentication(() => this.loginImpl(methodID, credential));
  }

  logout(): Promise<void> {
    return this.serializeAuthentication(() => this.logoutImpl());
  }

  private async stopImpl(): Promise<void> {
    this.#stopping = true;
    this.#connection?.close();
    await this.terminateChild();
    this.setAuthentication(unauthenticated);
    this.setState("stopped");
  }

  private async start(): Promise<void> {
    const child = spawn(this.options.command, this.options.arguments, {
      cwd: this.workspace,
      env: this.options.environment,
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
        this.setState("unavailable");
      }
      childTerminated();
    });

    const stream = acp.ndJsonStream(
      Writable.toWeb(child.stdin),
      Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
    );
    const connection = acp.client({ name: "ox-browser-client" }).connect(stream);
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
          clientCapabilities: { auth: { terminal: true } },
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
      const termination = this.terminateChild();
      this.#connection?.close();
      void termination;
      return;
    }
    if (initialized && !this.#stopping && this.#state.status !== "unavailable") {
      this.setState("ready");
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
    const child = spawn(this.options.command, [...this.options.arguments, ...method.arguments], {
      cwd: this.workspace,
      env: { ...this.options.environment, ...method.environment },
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
    const state = this.state;
    for (const listener of this.#listeners) {
      listener(state);
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

async function canonicalWorkspace(path: string): Promise<string> {
  const workspace = await realpath(path);
  if (!(await stat(workspace)).isDirectory()) {
    throw new Error("workspace must be a directory");
  }
  return workspace;
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
