import * as acp from "@agentclientprotocol/sdk";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { realpath, stat } from "node:fs/promises";
import { Readable, Writable } from "node:stream";

const maximumDiagnostics = 16;
const maximumDiagnosticLength = 512;
const initializationTimeoutMilliseconds = 2_000;

export type WorkspaceStatus = "starting" | "ready" | "unavailable" | "stopped";

export type WorkspaceState = {
  diagnostics: string[];
  status: WorkspaceStatus;
};

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
  #launchFailed = false;
  #listeners = new Set<(state: WorkspaceState) => void>();
  #state: WorkspaceState = { diagnostics: [], status: "starting" };
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
    return { diagnostics: [...this.#state.diagnostics], status: this.#state.status };
  }

  subscribe(listener: (state: WorkspaceState) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  async stop(): Promise<void> {
    this.#stop ??= this.stopImpl();
    await this.#stop;
  }

  private async stopImpl(): Promise<void> {
    this.#stopping = true;
    this.#connection?.close();
    await this.terminateChild();
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
          clientCapabilities: {},
          clientInfo: { name: "ox-browser-client", version: "0" },
          protocolVersion: acp.PROTOCOL_VERSION,
        })
        .then(
          () => true,
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
    this.setState("unavailable");
  }

  private setState(status: WorkspaceStatus): void {
    if (this.#state.status === status) {
      return;
    }
    this.#state = { ...this.#state, status };
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
