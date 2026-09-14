import type * as acp from "@agentclientprotocol/sdk";
import { spawn, type ChildProcessByStdio } from "node:child_process";
import { lstat } from "node:fs/promises";
import { isAbsolute, relative, resolve, sep } from "node:path";
import { randomUUID } from "node:crypto";
import type { Readable } from "node:stream";

/** Matches Ox's maximum collected shell output for delegated terminals. */
export const maximumTerminalOutputBytes = 10 << 20;

type ExitStatus = { exitCode?: number; signal?: string };
type TerminalChild = ChildProcessByStdio<null, Readable, Readable>;

type Terminal = {
  child: TerminalChild;
  exit: Promise<ExitStatus>;
  exitStatus?: ExitStatus;
  output: Buffer;
  outputByteLimit: number;
  sessionID: string;
  truncated: boolean;
};

/**
 * Confined ACP terminal callbacks for one canonical workspace. Terminal IDs
 * exist only in this host process and are always associated with one session.
 */
export class TerminalExecutor {
  #terminals = new Map<string, Terminal>();

  constructor(
    private readonly workspace: string,
    private readonly sessionIsActive: (sessionID: string) => boolean,
  ) {}

  async create(request: acp.CreateTerminalRequest, signal: AbortSignal): Promise<acp.CreateTerminalResponse> {
    this.requireActiveSession(request.sessionId);
    throwIfAborted(signal);
    if (!request.command || request.command.includes("\0") || (request.args ?? []).some((argument) => argument.includes("\0"))) {
      throw new Error("terminal command or argument is invalid");
    }
    const cwd = await this.confinedDirectory(request.cwd ?? this.workspace, signal);
    const outputByteLimit = outputLimit(request.outputByteLimit);
    const environment = environmentFor(request.env);
    const child = spawn(request.command, request.args ?? [], {
      cwd,
      detached: true,
      env: environment,
      stdio: ["ignore", "pipe", "pipe"],
    });
    const terminalID = randomUUID();
    let resolveExit!: (status: ExitStatus) => void;
    const terminal: Terminal = {
      child,
      exit: new Promise<ExitStatus>((resolveExitStatus) => {
        resolveExit = resolveExitStatus;
      }),
      output: Buffer.alloc(0),
      outputByteLimit,
      sessionID: request.sessionId,
      truncated: false,
    };
    child.stdout.on("data", (chunk: Buffer) => this.append(terminal, chunk));
    child.stderr.on("data", (chunk: Buffer) => this.append(terminal, chunk));
    child.once("close", (exitCode, exitSignal) => {
      terminal.exitStatus = {
        ...(exitCode === null ? {} : { exitCode }),
        ...(exitSignal === null ? {} : { signal: exitSignal }),
      };
      resolveExit(terminal.exitStatus);
    });
    await started(child, signal);

    if (signal.aborted) {
      await this.killTerminal(terminal);
      throwIfAborted(signal);
    }
    this.#terminals.set(terminalID, terminal);
    return { terminalId: terminalID };
  }

  output(request: acp.TerminalOutputRequest, signal: AbortSignal): acp.TerminalOutputResponse {
    throwIfAborted(signal);
    const terminal = this.terminal(request.sessionId, request.terminalId);
    return {
      ...(terminal.exitStatus === undefined ? {} : { exitStatus: terminal.exitStatus }),
      output: stringOutput(terminal.output),
      truncated: terminal.truncated,
    };
  }

  async wait(request: acp.WaitForTerminalExitRequest, signal: AbortSignal): Promise<acp.WaitForTerminalExitResponse> {
    const terminal = this.terminal(request.sessionId, request.terminalId);
    const status = await waitFor(terminal, signal);
    return status;
  }

  async kill(request: acp.KillTerminalRequest, signal: AbortSignal): Promise<void> {
    throwIfAborted(signal);
    await this.killTerminal(this.terminal(request.sessionId, request.terminalId));
  }

  async release(request: acp.ReleaseTerminalRequest, signal: AbortSignal): Promise<void> {
    throwIfAborted(signal);
    const terminal = this.terminal(request.sessionId, request.terminalId);
    this.#terminals.delete(request.terminalId);
    await this.killTerminal(terminal);
  }

  async releaseSession(sessionID: string): Promise<void> {
    const releases: Promise<void>[] = [];
    for (const [terminalID, terminal] of this.#terminals) {
      if (terminal.sessionID !== sessionID) continue;
      this.#terminals.delete(terminalID);
      releases.push(this.killTerminal(terminal));
    }
    await Promise.all(releases);
  }

  async stop(): Promise<void> {
    const releases = [...this.#terminals.values()].map((terminal) => this.killTerminal(terminal));
    this.#terminals.clear();
    await Promise.all(releases);
  }

  private append(terminal: Terminal, chunk: Buffer): void {
    const combined = Buffer.concat([terminal.output, chunk]);
    if (combined.length <= terminal.outputByteLimit) {
      terminal.output = combined;
      return;
    }
    terminal.truncated = true;
    terminal.output = trimToUTF8Boundary(combined.subarray(combined.length - terminal.outputByteLimit));
  }

  private terminal(sessionID: string, terminalID: string): Terminal {
    this.requireActiveSession(sessionID);
    const terminal = this.#terminals.get(terminalID);
    if (!terminal || terminal.sessionID !== sessionID) {
      throw new Error("unknown terminal for active session");
    }
    return terminal;
  }

  private requireActiveSession(sessionID: string): void {
    if (!this.sessionIsActive(sessionID)) {
      throw new Error("terminal callback is not associated with an active session");
    }
  }

  private async confinedDirectory(requested: string, signal: AbortSignal): Promise<string> {
    if (!isAbsolute(requested)) throw new Error("terminal working directory must be absolute");
    const path = resolve(requested);
    const fromWorkspace = relative(this.workspace, path);
    if (fromWorkspace === ".." || fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace)) {
      throw new Error("terminal working directory is outside the workspace");
    }
    let current = this.workspace;
    for (const component of fromWorkspace ? fromWorkspace.split(sep) : []) {
      throwIfAborted(signal);
      current = resolve(current, component);
      const info = await lstat(current);
      if (info.isSymbolicLink()) throw new Error("terminal working directory traverses a symbolic link");
      if (!info.isDirectory()) throw new Error("terminal working directory component is not a directory");
    }
    return current;
  }

  private async killTerminal(terminal: Terminal): Promise<void> {
    if (terminal.exitStatus !== undefined) return;
    const pid = terminal.child.pid;
    if (pid === undefined) return;
    try {
      process.kill(-pid, "SIGKILL");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
    }
    await terminal.exit;
  }
}

function environmentFor(variables: acp.EnvVariable[] | null | undefined): NodeJS.ProcessEnv {
  const environment: NodeJS.ProcessEnv = {};
  for (const variable of variables ?? []) {
    if (!variable.name || variable.name.includes("=") || variable.name.includes("\0") || variable.value.includes("\0")) {
      throw new Error("terminal environment contains an invalid variable");
    }
    if (variable.name in environment) throw new Error("terminal environment contains a duplicate variable");
    environment[variable.name] = variable.value;
  }
  return environment;
}

function outputLimit(requested: number | null | undefined): number {
  if (requested === undefined || requested === null) return maximumTerminalOutputBytes;
  if (!Number.isSafeInteger(requested) || requested < 1 || requested > maximumTerminalOutputBytes) {
    throw new Error(`terminal output limit must be a positive integer no greater than ${maximumTerminalOutputBytes}`);
  }
  return requested;
}

function started(child: TerminalChild, signal: AbortSignal): Promise<void> {
  return new Promise((resolveStarted, rejectStarted) => {
    const abort = () => {
      child.removeListener("spawn", spawned);
      child.removeListener("error", failed);
      child.once("error", () => {});
      terminateSpawn(child);
      rejectStarted(signal.reason);
    };
    const failed = (error: Error) => {
      signal.removeEventListener("abort", abort);
      rejectStarted(error);
    };
    const spawned = () => {
      signal.removeEventListener("abort", abort);
      child.removeListener("error", failed);
      resolveStarted();
    };
    child.once("spawn", spawned);
    child.once("error", failed);
    signal.addEventListener("abort", abort, { once: true });
  });
}

async function waitFor(terminal: Terminal, signal: AbortSignal): Promise<ExitStatus> {
  throwIfAborted(signal);
  if (terminal.exitStatus !== undefined) return terminal.exitStatus;
  return await new Promise<ExitStatus>((resolveExit, rejectExit) => {
    const abort = () => {
      void killForAbort().catch(() => {});
      rejectExit(signal.reason);
    };
    const finish = (status: ExitStatus) => {
      signal.removeEventListener("abort", abort);
      resolveExit(status);
    };
    const killForAbort = async () => {
      const pid = terminal.child.pid;
      if (pid === undefined) return;
      try {
        process.kill(-pid, "SIGKILL");
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
      }
    };
    terminal.exit.then(finish);
    signal.addEventListener("abort", abort, { once: true });
  });
}

function trimToUTF8Boundary(value: Buffer): Buffer {
  let start = 0;
  while (start < value.length && (value[start]! & 0xc0) === 0x80) start += 1;
  return value.subarray(start);
}

function stringOutput(value: Buffer): string {
  const trimmed = trimToUTF8Boundary(value);
  const decoder = new TextDecoder("utf-8", { fatal: true });
  for (let end = trimmed.length; end > 0; end -= 1) {
    try {
      return decoder.decode(trimmed.subarray(0, end));
    } catch {
      // A stream can end this chunk in the middle of a character. Retain those
      // bytes for the next chunk without exposing a partial character now.
    }
  }
  return "";
}

function terminateSpawn(child: TerminalChild): void {
  const pid = child.pid;
  try {
    if (pid === undefined) {
      child.kill("SIGKILL");
    } else {
      process.kill(-pid, "SIGKILL");
    }
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
  }
}

function throwIfAborted(signal: AbortSignal): void {
  signal.throwIfAborted();
}
