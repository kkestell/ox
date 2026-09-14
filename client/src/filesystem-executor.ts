import type * as acp from "@agentclientprotocol/sdk";
import { lstat, mkdir, open, writeFile } from "node:fs/promises";
import { isAbsolute, relative, resolve, sep } from "node:path";

/** Matches Ox's whole-file read limit so delegated reads cannot widen it. */
export const maximumFileBytes = 8 << 20;

/**
 * Confined implementation of the paired ACP filesystem callbacks for one
 * canonical workspace. It deliberately has no browser-facing API.
 */
export class FilesystemExecutor {
  constructor(
    private readonly workspace: string,
    private readonly sessionIsActive: (sessionID: string) => boolean,
  ) {}

  async read(request: acp.ReadTextFileRequest, signal: AbortSignal): Promise<acp.ReadTextFileResponse> {
    this.requireActiveSession(request.sessionId);
    throwIfAborted(signal);
    const path = await this.confinedPath(request.path, false, signal);
    const content = await readUTF8(path, signal);
    return { content: page(content, request.line, request.limit) };
  }

  async write(request: acp.WriteTextFileRequest, signal: AbortSignal): Promise<void> {
    this.requireActiveSession(request.sessionId);
    throwIfAborted(signal);
    const path = await this.confinedPath(request.path, true, signal);
    throwIfAborted(signal);
    await writeFile(path, request.content, { encoding: "utf8", signal });
  }

  private requireActiveSession(sessionID: string): void {
    if (!this.sessionIsActive(sessionID)) {
      throw new Error("filesystem callback is not associated with an active session");
    }
  }

  private async confinedPath(requested: string, createParents: boolean, signal: AbortSignal): Promise<string> {
    if (!isAbsolute(requested)) {
      throw new Error("filesystem path must be absolute");
    }
    const path = resolve(requested);
    const fromWorkspace = relative(this.workspace, path);
    if (fromWorkspace === ".." || fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace)) {
      throw new Error("filesystem path is outside the workspace");
    }

    const components = fromWorkspace ? fromWorkspace.split(sep) : [];
    let current = this.workspace;
    for (let index = 0; index < components.length; index += 1) {
      throwIfAborted(signal);
      current = resolve(current, components[index]!);
      let info;
      try {
        info = await lstat(current);
      } catch (error) {
        if (!isMissing(error)) throw error;
        if (!createParents || index === components.length - 1) {
          if (createParents && index === components.length - 1) return current;
          throw new Error("filesystem path does not exist");
        }
        await mkdir(current);
        info = await lstat(current);
      }
      if (info.isSymbolicLink()) {
        throw new Error("filesystem path traverses a symbolic link");
      }
      if (index < components.length - 1 && !info.isDirectory()) {
        throw new Error("filesystem path component is not a directory");
      }
      if (index === components.length - 1 && !info.isFile()) {
        throw new Error("filesystem path is not a regular file");
      }
      if (index === components.length - 1) {
        return current;
      }
    }
    throw new Error("filesystem path is not a regular file");
  }
}

async function readUTF8(path: string, signal: AbortSignal): Promise<string> {
  const handle = await open(path, "r");
  try {
    throwIfAborted(signal);
    const info = await handle.stat();
    if (!info.isFile()) throw new Error("filesystem path is not a regular file");
    if (info.size > maximumFileBytes) throw oversized();

    const buffer = Buffer.allocUnsafe(maximumFileBytes + 1);
    let length = 0;
    while (length < buffer.length) {
      throwIfAborted(signal);
      const { bytesRead } = await handle.read(buffer, length, buffer.length - length, length);
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length > maximumFileBytes) throw oversized();
    try {
      return new TextDecoder("utf-8", { fatal: true }).decode(buffer.subarray(0, length));
    } catch {
      throw new Error("filesystem file is not valid UTF-8");
    }
  } finally {
    await handle.close();
  }
}

function page(content: string, requestedLine: number | null | undefined, requestedLimit: number | null | undefined): string {
  const line = requestedLine ?? 1;
  const limit = requestedLimit ?? Number.MAX_SAFE_INTEGER;
  if (!Number.isSafeInteger(line) || line < 1 || !Number.isSafeInteger(limit) || limit < 1) {
    throw new Error("filesystem read line and limit must be positive integers");
  }
  const lines = content.match(/[^\n]*\n|[^\n]+/gu) ?? [];
  return lines.slice(line - 1, line - 1 + limit).join("");
}

function oversized(): Error {
  return new Error(`filesystem file exceeds the ${maximumFileBytes} byte limit`);
}

function isMissing(error: unknown): boolean {
  return (error as NodeJS.ErrnoException).code === "ENOENT";
}

function throwIfAborted(signal: AbortSignal): void {
  signal.throwIfAborted();
}
