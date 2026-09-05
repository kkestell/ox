import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer, type Server, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import { dirname, extname, join, normalize, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import type { Page, TestInfo } from "@playwright/test";

const browserDir = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(browserDir, "../../..");
const clientRoot = join(browserDir, "acp-ui");
const bridgeBin = join(browserDir, "node_modules", ".bin", "stdio-to-ws");
const timeoutMs = 10_000;

type HarnessOptions = {
  contextWindow?: number;
  liveAPIKey?: string;
  logLevel?: string;
};

type QueuedResponse = {
  prompt: string;
  opening: object[];
  held: boolean;
  consumed: boolean;
  response?: ServerResponse;
  completed: boolean;
  started: Deferred<void>;
  cancelled: Deferred<void>;
};

class Deferred<T> {
  readonly promise: Promise<T>;
  resolve!: (value: T | PromiseLike<T>) => void;
  reject!: (reason?: unknown) => void;

  constructor() {
    this.promise = new Promise<T>((resolvePromise, rejectPromise) => {
      this.resolve = resolvePromise;
      this.reject = rejectPromise;
    });
  }
}

export class HeldResponse {
  constructor(private readonly queued: QueuedResponse) {}

  async waitUntilStarted(): Promise<void> {
    await withTimeout(this.queued.started.promise, "provider request did not start");
  }

  async waitUntilCancelled(): Promise<void> {
    await withTimeout(this.queued.cancelled.promise, "provider request was not cancelled");
  }

  finish(...chunks: string[]): void {
    const response = this.queued.response;
    if (!response || this.queued.completed) {
      throw new Error("provider response is not open");
    }
    for (const chunk of chunks) response.write(event(textDelta(chunk)));
    response.write(event(finishDelta()));
    this.queued.completed = true;
    response.end("data: [DONE]\n\n");
  }
}

export class BrowserHarness {
  readonly workspace: string;
  readonly clientURL: string;
  readonly websocketURL: string;

  private readonly scratch: string;
  private readonly provider: Server;
  private readonly staticServer: Server;
  private readonly bridge: ChildProcess;
  private readonly contextWindow: number;
  private readonly responses: QueuedResponse[] = [];
  private readonly providerErrors: string[] = [];
  private readonly authorizedRequests: string[] = [];
  private bridgeStandardOutput = "";
  private bridgeErrorOutput = "";
  private oxPID: number | undefined;

  private constructor(options: {
    scratch: string;
    workspace: string;
    clientURL: string;
    websocketURL: string;
    provider: Server;
    staticServer: Server;
    bridge: ChildProcess;
    contextWindow: number;
  }) {
    this.scratch = options.scratch;
    this.workspace = options.workspace;
    this.clientURL = options.clientURL;
    this.websocketURL = options.websocketURL;
    this.provider = options.provider;
    this.staticServer = options.staticServer;
    this.bridge = options.bridge;
    this.contextWindow = options.contextWindow;
  }

  static async start(options: HarnessOptions = {}): Promise<BrowserHarness> {
    const liveProvider = options.liveAPIKey !== undefined;
    const scratch = await mkdtemp(join(tmpdir(), "ox-client-e2e-"));
    const workspace = join(scratch, "workspace");
    const configHome = join(scratch, "config");
    await mkdir(workspace, { recursive: true });
    const credentialFile = join(scratch, "credential");
    await writeFile(credentialFile, `${options.liveAPIKey ?? "browser-test-key"}\n`, { mode: 0o600 });

    const oxBin = join(scratch, "ox");
    await run("go", ["build", "-o", oxBin, "./cmd/ox"], repositoryRoot);

    let harness: BrowserHarness | undefined;
    const provider = createServer((request, response) => {
      void harness?.serveProvider(request, response).catch((error) => {
        harness?.providerErrors.push(`provider handler failed: ${String(error)}`);
        if (!response.headersSent) response.writeHead(500);
        response.end();
      });
    });
    const providerPort = await listen(provider);
    const staticServer = createServer((request, response) => {
      void serveStatic(request.url ?? "/", response).catch((error) => {
        if (!response.headersSent) response.writeHead(500);
        response.end(String(error));
      });
    });
    const staticPort = await listen(staticServer);
    const bridgePort = await freePort();

    const oxEnvironment: NodeJS.ProcessEnv = {
      ...process.env,
      HOME: scratch,
      XDG_CACHE_HOME: join(scratch, "cache"),
      XDG_CONFIG_HOME: configHome,
      XDG_DATA_HOME: join(scratch, "data"),
    };
    const oxArguments = [
      "--model",
      liveProvider ? "openai/gpt-5.6-luna" : "test/model",
      "--credential-file",
      credentialFile,
      "--no-keyring",
    ];
    if (!liveProvider) oxArguments.push("--openrouter-base-url", `http://127.0.0.1:${providerPort}/api/v1`);
    if (options.logLevel !== undefined) oxArguments.push("--log-level", options.logLevel);
    const oxCommand = [oxBin, ...oxArguments].map((argument) => JSON.stringify(argument)).join(" ");

    const bridge = spawn(
      bridgeBin,
      ["--port", String(bridgePort), oxCommand],
      {
        cwd: workspace,
        env: oxEnvironment,
        stdio: ["ignore", "pipe", "pipe"],
      },
    );

    harness = new BrowserHarness({
      scratch,
      workspace,
      clientURL: `http://127.0.0.1:${staticPort}`,
      websocketURL: `ws://127.0.0.1:${bridgePort}`,
      provider,
      staticServer,
      bridge,
      contextWindow: options.contextWindow ?? 128_000,
    });
    harness.captureBridgeOutput();
    await harness.waitForBridge();
    return harness;
  }

  hold(prompt: string, opening: string): HeldResponse {
    const queued = this.queue(prompt, [textDelta(opening)], true);
    return new HeldResponse(queued);
  }

  async scriptReadTurn(prompt: string, reasoning: string, answer: string): Promise<void> {
    const path = "browser-fixture.md";
    const contents = "Browser fixture contents.";
    await writeFile(join(this.workspace, path), contents);
    this.queue(prompt, [
      reasoningDelta(reasoning),
      toolCallDelta(0, "browser-read", "read_file", JSON.stringify({ path })),
      finishDelta("tool_calls"),
    ]);
    this.queue(contents, [
      textDelta(answer),
      finishDelta(),
      usageDelta(8, 5, 13, 0.001),
    ]);
  }

  scriptCompactionTurns(
    firstPrompt: string,
    oldAnswer: string,
    secondPrompt: string,
    recentAnswer: string,
    thirdPrompt: string,
    summary: string,
    finalAnswer: string,
  ): void {
    this.queue(firstPrompt, [
      textDelta(oldAnswer),
      finishDelta(),
      usageDelta(100, 600, 700, 0.001),
    ]);
    this.queue(secondPrompt, [
      textDelta(recentAnswer),
      finishDelta(),
      usageDelta(800, 5, 805, 0.002),
    ]);
    this.queue(`[assistant] ${oldAnswer}\n\n[user] ${secondPrompt}\n\n`, [
      textDelta(summary),
      finishDelta(),
      usageDelta(650, 20, 670, 0.003),
    ]);
    this.queue(thirdPrompt, [
      textDelta(finalAnswer),
      finishDelta(),
      usageDelta(100, 5, 105, 0.001),
    ]);
  }

  async scriptExecutorWorkflow(prompt: string, answer: string): Promise<void> {
    const path = "executor-fixture.txt";
    const marker = "executor-marker.txt";
    const command = `printf shell-output | tee ${marker}`;
    await writeFile(join(this.workspace, path), "before\n");
    this.queue(prompt, [
      reasoningDelta("I will run the requested workflow."),
      toolCallDelta(0, "browser-executor-read", "read_file", JSON.stringify({ path })),
      finishDelta("tool_calls"),
    ]);
    this.queue("before\n", [
      toolCallDelta(0, "browser-executor-edit", "edit_file", JSON.stringify({
        path,
        old_string: "before",
        new_string: "after",
      })),
      finishDelta("tool_calls"),
    ]);
    this.queue(`Edited ${path} (1 replacement(s))`, [
      toolCallDelta(0, "browser-executor-shell", "shell", JSON.stringify({ command })),
      finishDelta("tool_calls"),
    ]);
    this.queue("exit code: 0\nshell-output", [
      textDelta(answer),
      finishDelta(),
    ]);
  }

  async readWorkspaceFile(path: string): Promise<string> {
    const absolute = resolve(this.workspace, path);
    if (absolute !== this.workspace && !absolute.startsWith(this.workspace + sep)) {
      throw new Error(`workspace fixture path escapes: ${path}`);
    }
    return readFile(absolute, "utf8");
  }

  async open(page: Page): Promise<void> {
    await page.addInitScript((websocketURL) => {
      localStorage.clear();
      localStorage.setItem(
        "acp-ui:agents",
        JSON.stringify({
          agents: { Ox: { transport: "websocket", url: websocketURL } },
        }),
      );
      localStorage.setItem(
        "acp-ui:preferences.json",
        JSON.stringify({ telemetryEnabled: false }),
      );
    }, this.websocketURL);
    await page.goto(this.clientURL);
  }

  async waitForAuthorizedRequest(path: string): Promise<void> {
    await poll(
      () => this.authorizedRequests.includes(path),
      `provider did not receive an authorized ${path} request`,
    );
  }

  async waitForOxLog(fragment: string): Promise<void> {
    await poll(
      () => this.bridgeErrorOutput.includes(fragment),
      `Ox stderr did not contain ${JSON.stringify(fragment)}`,
    );
  }

  standardOutput(): string {
    return this.bridgeStandardOutput;
  }

  errorOutput(): string {
    return this.bridgeErrorOutput;
  }

  async rememberOxProcess(): Promise<number> {
    const pid = await pollValue(async () => {
      const children = await childPIDs(this.bridge.pid);
      return children[0];
    }, "bridge did not start Ox");
    this.oxPID = pid;
    return pid;
  }

  async waitForOxExit(pid = this.oxPID): Promise<void> {
    if (pid === undefined) throw new Error("Ox PID was not recorded");
    await poll(() => !processExists(pid), `Ox process ${pid} did not exit`);
  }

  async stop(testInfo: TestInfo): Promise<void> {
    const problems: string[] = [];
    for (const queued of this.responses) {
      if (!queued.consumed) problems.push(`no provider request for prompt ${JSON.stringify(queued.prompt)}`);
    }
    problems.push(...this.providerErrors);

    if (testInfo.status !== testInfo.expectedStatus || problems.length > 0) {
      await testInfo.attach("bridge-stdout", {
        body: this.bridgeStandardOutput || "(no bridge stdout)",
        contentType: "text/plain",
      });
      await testInfo.attach("bridge-stderr", {
        body: this.bridgeErrorOutput || "(no bridge stderr)",
        contentType: "text/plain",
      });
    }

    const oxPIDs = this.oxPID === undefined ? await childPIDs(this.bridge.pid) : [this.oxPID];
    for (const pid of oxPIDs) terminate(pid);
    await terminateChild(this.bridge);
    await Promise.all([closeServer(this.provider), closeServer(this.staticServer)]);
    await rm(this.scratch, { recursive: true, force: true });

    if (problems.length > 0 && testInfo.status === testInfo.expectedStatus) {
      throw new Error(problems.join("\n"));
    }
  }

  private captureBridgeOutput(): void {
    this.bridge.stdout?.on("data", (chunk: Buffer) => {
      this.bridgeStandardOutput = appendBounded(this.bridgeStandardOutput, chunk);
    });
    this.bridge.stderr?.on("data", (chunk: Buffer) => {
      this.bridgeErrorOutput = appendBounded(this.bridgeErrorOutput, chunk);
    });
  }

  private async waitForBridge(): Promise<void> {
    await poll(async () => {
      if (this.bridge.exitCode !== null) {
        throw new Error(
          `stdio bridge exited early\nstdout:\n${this.bridgeStandardOutput}\nstderr:\n${this.bridgeErrorOutput}`,
        );
      }
      return canConnect(this.websocketURL.replace("ws://", "http://"));
    }, "stdio bridge did not listen");
  }

  private async serveProvider(request: import("node:http").IncomingMessage, response: ServerResponse): Promise<void> {
    const authorization = request.headers.authorization;
    if (authorization !== "Bearer browser-test-key") {
      this.providerErrors.push(`provider Authorization = ${JSON.stringify(authorization)}`);
      response.writeHead(401).end("invalid authorization");
      return;
    }
    this.authorizedRequests.push(request.url ?? "");

    if (request.method === "GET" && request.url === "/api/v1/auth/key") {
      json(response, { data: {} });
      return;
    }
    if (request.method === "GET" && request.url === "/api/v1/models") {
      json(response, {
        data: [{
          id: "test/model",
          name: "Test Model",
          context_length: this.contextWindow,
          supported_parameters: ["tools"],
          architecture: { input_modalities: ["text", "image", "audio"] },
        }],
      });
      return;
    }
    if (request.method !== "POST" || request.url !== "/api/v1/chat/completions") {
      this.providerErrors.push(`unexpected provider request ${request.method} ${request.url}`);
      response.writeHead(404).end("not found");
      return;
    }

    const body = JSON.parse(await readBody(request)) as {
      messages?: Array<{ content?: Array<{ text?: string }> }>;
    };
    const last = body.messages?.at(-1);
    const prompt = last?.content?.map((part) => part.text ?? "").join("") ?? "";
    const queued = this.responses.find((candidate) => !candidate.consumed && candidate.prompt === prompt);
    if (!queued) {
      this.providerErrors.push(`no queued response for prompt ${JSON.stringify(prompt)}`);
      response.writeHead(500).end("no queued response");
      return;
    }

    queued.consumed = true;
    queued.response = response;
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    for (const opening of queued.opening) response.write(event(opening));
    queued.started.resolve();
    if (!queued.held) {
      queued.completed = true;
      response.end("data: [DONE]\n\n");
      return;
    }
    response.on("close", () => {
      if (!queued.completed) queued.cancelled.resolve();
    });
  }

  private queue(prompt: string, opening: object[], held = false): QueuedResponse {
    const queued: QueuedResponse = {
      prompt,
      opening,
      held,
      consumed: false,
      completed: false,
      started: new Deferred<void>(),
      cancelled: new Deferred<void>(),
    };
    this.responses.push(queued);
    return queued;
  }
}

function textDelta(text: string): object {
  return { choices: [{ delta: { content: text } }] };
}

function reasoningDelta(text: string): object {
  return { choices: [{ delta: { reasoning: text } }] };
}

function toolCallDelta(index: number, id: string, name: string, argumentsJSON: string): object {
  return {
    choices: [{
      delta: {
        tool_calls: [{
          index,
          id,
          type: "function",
          function: { name, arguments: argumentsJSON },
        }],
      },
    }],
  };
}

function finishDelta(reason = "stop"): object {
  return { choices: [{ delta: {}, finish_reason: reason }] };
}

function usageDelta(promptTokens: number, completionTokens: number, totalTokens: number, cost: number): object {
  return {
    choices: [],
    usage: {
      prompt_tokens: promptTokens,
      completion_tokens: completionTokens,
      total_tokens: totalTokens,
      cost,
    },
  };
}

function event(value: object): string {
  return `data: ${JSON.stringify(value)}\n\n`;
}

function appendBounded(current: string, chunk: Buffer): string {
  const appended = current + chunk.toString();
  return appended.length <= 64 * 1024 ? appended : appended.slice(-64 * 1024);
}

function json(response: ServerResponse, value: object): void {
  response.writeHead(200, { "Content-Type": "application/json" });
  response.end(JSON.stringify(value));
}

async function readBody(request: import("node:http").IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks).toString();
}

async function serveStatic(url: string, response: ServerResponse): Promise<void> {
  const pathname = decodeURIComponent(new URL(url, "http://localhost").pathname);
  const relative = pathname === "/" ? "index.html" : normalize(pathname).replace(/^[/\\]+/, "");
  const path = resolve(clientRoot, relative);
  if (path !== clientRoot && !path.startsWith(clientRoot + sep)) {
    response.writeHead(403).end("forbidden");
    return;
  }
  try {
    const body = await readFile(path);
    const contentTypes: Record<string, string> = {
      ".css": "text/css",
      ".html": "text/html",
      ".js": "text/javascript",
      ".svg": "image/svg+xml",
    };
    response.writeHead(200, { "Content-Type": contentTypes[extname(path)] ?? "application/octet-stream" });
    response.end(body);
  } catch {
    response.writeHead(404).end("not found");
  }
}

async function listen(server: Server): Promise<number> {
  await new Promise<void>((resolvePromise, rejectPromise) => {
    server.once("error", rejectPromise);
    server.listen(0, "127.0.0.1", () => resolvePromise());
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("server has no TCP address");
  return address.port;
}

async function freePort(): Promise<number> {
  const server = createServer();
  const port = await listen(server);
  await closeServer(server);
  return port;
}

async function closeServer(server: Server): Promise<void> {
  await new Promise<void>((resolvePromise, rejectPromise) => {
    server.close((error) => error ? rejectPromise(error) : resolvePromise());
    server.closeAllConnections();
  });
}

async function canConnect(url: string): Promise<boolean> {
  try {
    const response = await fetch(url);
    await response.body?.cancel();
    return true;
  } catch {
    return false;
  }
}

async function childPIDs(parent: number | undefined): Promise<number[]> {
  if (parent === undefined) return [];
  try {
    const output = await exec("pgrep", ["-P", String(parent)]);
    return output.trim().split(/\s+/).filter(Boolean).map(Number);
  } catch {
    return [];
  }
}

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function terminate(pid: number): void {
  if (!processExists(pid)) return;
  try {
    process.kill(pid, "SIGTERM");
  } catch {
    // The process exited between the existence check and signal.
  }
}

async function terminateChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null) return;
  child.kill("SIGTERM");
  try {
    await withTimeout(new Promise<void>((resolvePromise) => child.once("exit", () => resolvePromise())), "process did not exit", 2_000);
  } catch {
    child.kill("SIGKILL");
  }
}

async function run(command: string, args: string[], cwd: string): Promise<void> {
  try {
    await exec(command, args, cwd);
  } catch (error) {
    throw new Error(`${command} ${args.join(" ")} failed: ${String(error)}`);
  }
}

async function exec(command: string, args: string[], cwd?: string): Promise<string> {
  return new Promise<string>((resolvePromise, rejectPromise) => {
    execFile(command, args, { cwd }, (error, stdout, stderr) => {
      if (error) {
        rejectPromise(new Error(`${error.message}\n${stderr}`));
        return;
      }
      resolvePromise(stdout);
    });
  });
}

async function poll(check: () => boolean | Promise<boolean>, message: string): Promise<void> {
  await pollValue(async () => await check() ? true : undefined, message);
}

async function pollValue<T>(check: () => T | undefined | Promise<T | undefined>, message: string): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = await check();
    if (value !== undefined) return value;
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 50));
  }
  throw new Error(message);
}

async function withTimeout<T>(promise: Promise<T>, message: string, duration = timeoutMs): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, rejectPromise) => {
        timer = setTimeout(() => rejectPromise(new Error(message)), duration);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}
