import { expect, test, type Page } from "@playwright/test";
import * as acp from "@agentclientprotocol/sdk";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { Readable, Writable } from "node:stream";
import { fileURLToPath } from "node:url";

const clientRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(clientRoot, "..");

test("independently drives Ox through the deterministic provider", async () => {
  const fixture = await createFixture();
  try {
    await driveOx(fixture);
    expect(fixture.requests).toBe(1);
  } finally {
    await fixture.close();
  }
});

test("supervises a real Ox process through clean shutdown and unexpected exit", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    await host.stop();
    await fixture.waitForOxExit();

    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    await fixture.stopOx();
    await expect(page.getByText("Unavailable", { exact: true })).toBeVisible();
    await expect(page.getByLabel("Workspace process").getByText("unavailable", { exact: true })).toBeVisible();
    await expect(page.getByRole("list", { name: "Workspace diagnostics" })).toContainText(
      "Ox exited from SIGTERM",
    );

    await page.getByRole("button", { name: "New session" }).click();
    await expect(page.getByRole("region", { name: "Sessions" }).getByRole("alert")).toHaveText(
      "Ox is unavailable",
    );
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("authenticates stored credentials and keeps a browser login secret out of the shell", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    await page.getByRole("button", { name: "OpenRouter API key" }).click();
    await expect(page.getByText("authenticated", { exact: true })).toBeVisible();

    const credential = "browser-login-test-secret";
    await page.getByLabel("OpenRouter login credential").fill(credential);
    await page.getByRole("button", { name: "OpenRouter login" }).click();
    await expect(page.getByRole("alert")).toHaveText("OpenRouter login failed");
    await expect(page.getByLabel("OpenRouter login credential")).toHaveValue("");
    await expect(page.locator("main")).not.toContainText(credential);

    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page.getByRole("alert")).toHaveText("Logout failed");
    await expect(page.getByText("authenticated", { exact: true })).toBeVisible();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("keeps a replayed session coherent across refresh and attached browsers", async ({ browser, page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  let secondContext: Awaited<ReturnType<typeof browser.newContext>> | undefined;
  try {
    const sessionID = await driveOx(fixture);
    host = startBrowserHost(fixture);
    const url = await host.url;
    await assertReady(page, url);

    const sessions = page.getByRole("list", { name: "Sessions" });
    await expect(sessions).toContainText("smoke");
    await page.getByRole("button", { name: "Load" }).click();
    await expect(page.getByText(`Selected session ${sessionID}`)).toBeVisible();
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("smoke");
    await expect(transcript).toContainText("browser smoke");
    await expect(transcript).toContainText("Usage");
    await expect(transcript).toContainText("Configuration");

    await page.reload();
    await expect(page.getByText(`Selected session ${sessionID}`)).toBeVisible();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    secondContext = await browser.newContext();
    const second = await secondContext.newPage();
    await second.goto(url);
    await expect(second.getByText(`Selected session ${sessionID}`)).toBeVisible();
    await second.getByRole("button", { name: "Close" }).click();
    await expect(page.getByText("No session selected")).toBeVisible();
    await expect(sessions.getByText("inactive", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "Delete" }).click();
    await expect(sessions).not.toContainText("smoke");
    await page.getByRole("button", { name: "New session" }).click();
    await expect(page.getByText(/^Selected session /)).toBeVisible();
  } finally {
    await secondContext?.close();
    await host?.stop();
    await fixture.close();
  }
});

test("prompts with controls and every supported browser attachment", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByRole("button", { name: "New session" }).click();
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("code");
    await page.getByLabel("Mode", { exact: true }).selectOption("plan");
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("plan");

    await page.getByLabel("Message").fill("inspect these attachments");
    await page.getByLabel("Attachments", { exact: true }).setInputFiles([
      { name: "picture.png", mimeType: "image/png", buffer: Buffer.from("image") },
      { name: "sound.wav", mimeType: "audio/wav", buffer: Buffer.from("audio") },
      { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("notes") },
    ]);
    await page.getByLabel("Name").fill("Guide");
    await page.getByLabel("URI").fill("https://example.test/guide");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect.poll(() => fixture.requests).toBe(1);
    const prompt = JSON.stringify(fixture.prompts[0]);
    expect(prompt).toContain("image_url");
    expect(prompt).toContain("input_audio");
    expect(prompt).toContain("notes.txt");
    expect(prompt).toContain("https://example.test/guide");
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs Ox file tools through the host's confined filesystem callbacks", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    await writeFile(join(fixture.workspace, "browser-file.txt"), "one\ntwo\n");
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Mode", { exact: true }).selectOption("auto");
    await page.getByLabel("Message").fill("exercise filesystem callbacks");
    await page.getByRole("button", { name: "Send prompt" }).click();

    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("filesystem complete");
    expect(await readFile(join(fixture.workspace, "browser-file.txt"), "utf8")).toBe("edited\n");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs Ox shell tools through the host's ACP terminal callbacks", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Mode", { exact: true }).selectOption("auto");
    await page.getByLabel("Message").fill("exercise terminal callbacks");
    await page.getByRole("button", { name: "Send prompt" }).click();

    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("terminal complete");
    await expect(transcript).toContainText("terminal callback output");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs separate sessions concurrently and cancels the selected live prompt", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Message").fill("hold");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();

    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Message").fill("second session");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect.poll(() => fixture.requests).toBe(2);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    const sessions = page.getByRole("list", { name: "Sessions" }).getByRole("listitem");
    await sessions.filter({ hasText: "hold" }).getByRole("button", { name: "Select" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();
    await page.getByRole("button", { name: "Cancel prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeHidden();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("renders a host-owned form elicitation through refresh and answers it", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Message").fill("ask a question");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Pending interactions" })).toContainText("Choose a color");
    await expect(page.getByRole("option", { name: "Red" })).toHaveText("Red");
    await expect(page.getByRole("combobox", { name: "Answer" })).toHaveValue("");
    await page.reload();
    await expect(page.getByRole("region", { name: "Pending interactions" })).toContainText("Choose a color");
    await page.getByRole("combobox", { name: "Answer" }).selectOption("Red");
    await page.getByRole("button", { name: "Submit answer" }).click();
    await expect(page.getByRole("region", { name: "Pending interactions" })).toBeHidden();
    await expect.poll(() => fixture.requests).toBe(2);
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("renders exact permission options and lets the first browser answer win", async ({ browser, page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  let secondContext: Awaited<ReturnType<typeof browser.newContext>> | undefined;
  try {
    host = startBrowserHost(fixture);
    const url = await host.url;
    await assertReady(page, url);
    await page.getByRole("button", { name: "New session" }).click();
    await page.getByLabel("Message").fill("request permission");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Pending interactions" })).toContainText("pwd");
    secondContext = await browser.newContext();
    const second = await secondContext.newPage();
    await second.goto(url);
    await expect(second.getByRole("button", { name: "Allow once" })).toBeVisible();
    await second.getByRole("button", { name: "Allow once" }).click();
    await expect(page.getByRole("region", { name: "Pending interactions" })).toBeHidden();
    await expect.poll(() => fixture.requests).toBe(2);
  } finally {
    await secondContext?.close();
    await host?.stop();
    await fixture.close();
  }
});

type BrowserHost = {
  stop(): Promise<void>;
  url: Promise<string>;
};

function startBrowserHost(fixture: Fixture): BrowserHost {
  const arguments_ = [
    "run",
    "./src/host.ts",
    "--port",
    "0",
    "--workspace",
    fixture.workspace,
    "--ox",
    fixture.runner,
  ];
  for (const argument of [
    fixture.binary,
    "--credential-file",
    fixture.credential,
    "--model",
    "test/model",
    "--no-keyring",
    "--openrouter-base-url",
    `${fixture.providerURL}/api/v1`,
  ]) {
    arguments_.push("--ox-arg", argument);
  }
  const child = spawn("bun", arguments_, {
    cwd: clientRoot,
    env: fixture.environment,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const url = new Promise<string>((resolveURL, reject) => {
    const timer = setTimeout(() => reject(new Error("browser host did not start")), 10_000);
    let output = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      output += chunk;
      const match = output.match(/Ox browser host listening at (http:\/\/[^\s]+)/);
      if (match?.[1]) {
        clearTimeout(timer);
        resolveURL(match[1]);
      }
    });
    child.once("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`browser host exited before startup: ${code}`));
    });
  });
  return {
    async stop() {
      if (child.exitCode !== null || child.signalCode !== null) {
        return;
      }
      child.kill("SIGTERM");
      await once(child, "exit");
    },
    url,
  };
}

type Fixture = {
  binary: string;
  close(): Promise<void>;
  credential: string;
  environment: NodeJS.ProcessEnv;
  providerURL: string;
  prompts: unknown[];
  requests: number;
  runner: string;
  stopOx(): Promise<void>;
  waitForOxExit(): Promise<void>;
  workspace: string;
};

async function createFixture(): Promise<Fixture> {
  const workspace = await mkdtemp(join(tmpdir(), "ox-client-smoke-"));
  const pidFile = join(workspace, "ox.pid");
  const binary = join(workspace, "ox");
  const credential = join(workspace, "credential");
  const runner = join(workspace, "run-ox");
  const provider = createServer();
  let requests = 0;
  let filesystemStage = 0;
  let terminalStage = 0;
  const prompts: unknown[] = [];
  provider.on("request", (request, response) => {
    if (request.method === "GET" && request.url === "/api/v1/auth/key") {
      if (request.headers.authorization === "Bearer test-key") {
        response.writeHead(200, { "content-type": "application/json" }).end('{"data":{}}');
      } else {
        response.writeHead(401, { "content-type": "application/json" }).end('{"error":{"message":"rejected"}}');
      }
      return;
    }
    if (request.method !== "POST" || request.url !== "/api/v1/chat/completions") {
      response.writeHead(404).end();
      return;
    }
    requests += 1;
    let body = "";
    request.setEncoding("utf8");
    request.on("data", (chunk: string) => {
      body += chunk;
    });
    request.once("end", () => {
      try {
        prompts.push(JSON.parse(body));
      } catch {
        prompts.push(body);
      }
      if (body.includes("hold")) {
        return;
      }
      if (body.includes("exercise filesystem callbacks")) {
        const tool = [
          ["browser-read", "read_file", '{\\"path\\":\\"browser-file.txt\\",\\"offset\\":2,\\"limit\\":1}'],
          ["browser-write", "write_file", '{\\"path\\":\\"browser-file.txt\\",\\"content\\":\\"written\\\\n\\"}'],
          ["browser-edit", "edit_file", '{\\"path\\":\\"browser-file.txt\\",\\"old_string\\":\\"written\\",\\"new_string\\":\\"edited\\"}'],
        ][filesystemStage++];
        if (tool) {
          response.writeHead(200, { "content-type": "text/event-stream" });
          response.end(
            [
              `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"${tool[0]}","type":"function","function":{"name":"${tool[1]}","arguments":"${tool[2]}"}}]},"finish_reason":null}]}`,
              "",
              'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
              "",
              "data: [DONE]",
              "",
            ].join("\n"),
          );
          return;
        }
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"content":"filesystem complete"},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      if (body.includes("exercise terminal callbacks")) {
        if (terminalStage++ === 0) {
          response.writeHead(200, { "content-type": "text/event-stream" });
          response.end(
            [
              'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"terminal-call","type":"function","function":{"name":"shell","arguments":"{\\"command\\":\\"printf terminal callback output\\"}"}}]},"finish_reason":null}]}',
              "",
              'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
              "",
              "data: [DONE]",
              "",
            ].join("\n"),
          );
          return;
        }
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"content":"terminal complete"},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      if (body.includes("request permission") && !body.includes('"outcome":"selected"')) {
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"shell-call","type":"function","function":{"name":"shell","arguments":"{\\"command\\":\\"pwd\\"}"}}]},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      if (body.includes("ask a question") && !body.includes('"outcome":"accepted"')) {
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"question-call","type":"function","function":{"name":"question","arguments":"{\\"question\\":\\"Choose a color\\",\\"options\\":[{\\"label\\":\\"Red\\"},{\\"label\\":\\"Blue\\"}]}"}}]},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(
        [
          'data: {"choices":[{"delta":{"content":"browser smoke"},"finish_reason":null}]}',
          "",
          'data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}',
          "",
          "data: [DONE]",
          "",
        ].join("\n"),
      );
    });
  });
  await new Promise<void>((resolveListen) => provider.listen(0, "127.0.0.1", resolveListen));
  const address = provider.address();
  if (!address || typeof address === "string") {
    throw new Error("provider did not bind a TCP address");
  }
  const providerURL = `http://127.0.0.1:${address.port}`;
  await seedOxFixture({ binary, credential, workspace });
  await writeFile(
    runner,
    `#!/bin/sh\nprintf '%s\\n' "$$" > "$OX_CLIENT_TEST_PID_FILE"\nexec "$@"\n`,
    { mode: 0o700 },
  );
  const environment = {
    ...process.env,
    OX_CLIENT_TEST_PID_FILE: pidFile,
    HOME: workspace,
    XDG_CACHE_HOME: join(workspace, "cache"),
    XDG_CONFIG_HOME: join(workspace, "config"),
    XDG_DATA_HOME: join(workspace, "data"),
  };
  return {
    binary,
    async close() {
      provider.close();
      await once(provider, "close");
      await rm(workspace, { force: true, recursive: true });
    },
    credential,
    environment,
    providerURL,
    get requests() {
      return requests;
    },
    prompts,
    runner,
    async stopOx() {
      process.kill(await oxPID(pidFile), "SIGTERM");
    },
    async waitForOxExit() {
      const pid = await oxPID(pidFile);
      await expect.poll(() => processExists(pid)).toBe(false);
    },
    workspace,
  };
}

async function seedOxFixture(fixture: Pick<Fixture, "binary" | "credential" | "workspace">): Promise<void> {
  await mkdir(join(fixture.workspace, "cache", "ox"), { recursive: true });
  await mkdir(join(fixture.workspace, "config", "ox"), { recursive: true });
  await writeFile(fixture.credential, "test-key\n", { mode: 0o600 });
  await writeFile(
    join(fixture.workspace, "cache", "ox", "models.json"),
    JSON.stringify({
      data: [
        {
          architecture: { input_modalities: ["text", "image", "audio"] },
          context_length: 128000,
          id: "test/model",
          supported_parameters: ["tools", "temperature", "max_tokens"],
        },
      ],
    }),
  );
  await writeFile(
    join(fixture.workspace, "config", "ox", "settings.json"),
    JSON.stringify({ default_model: "test/model", models: { "test/model": {} } }),
  );
  await run("go", ["build", "-o", fixture.binary, "./cmd/ox"], repositoryRoot);
}

async function driveOx(fixture: Fixture): Promise<string> {
  const child = spawn(
    fixture.binary,
    [
      "--credential-file",
      fixture.credential,
      "--model",
      "test/model",
      "--no-keyring",
      "--openrouter-base-url",
      `${fixture.providerURL}/api/v1`,
    ],
    {
      cwd: fixture.workspace,
      env: fixture.environment,
      stdio: ["pipe", "pipe", "pipe"],
    },
  );
  const stream = acp.ndJsonStream(
    Writable.toWeb(child.stdin),
    Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
  );
  try {
    return await acp
      .client({ name: "ox-browser-smoke" })
      .onNotification(acp.methods.client.session.update, () => {})
      .connectWith(stream, async (connection) => {
        await connection.request(acp.methods.agent.initialize, {
          clientCapabilities: {},
          clientInfo: { name: "ox-browser-smoke", version: "0" },
          protocolVersion: acp.PROTOCOL_VERSION,
        });
        const session = await connection.request(acp.methods.agent.session.new, {
          cwd: fixture.workspace,
          mcpServers: [],
        });
        const result = await connection.request(acp.methods.agent.session.prompt, {
          prompt: [{ text: "smoke", type: "text" }],
          sessionId: session.sessionId,
        });
        expect(result.stopReason).toBe("end_turn");
        return session.sessionId;
      });
  } finally {
    child.stdin.end();
    await once(child, "exit");
  }
}

async function assertReady(page: Page, url: string): Promise<void> {
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Ox" })).toBeVisible();
  await expect(page.getByText("Browser ACP client")).toBeVisible();
  await expect(page.getByText("Connected")).toBeVisible();
  await expect(page.getByText("ready", { exact: true })).toBeVisible();
}

async function oxPID(pidFile: string): Promise<number> {
  await expect.poll(async () => (await readFile(pidFile, "utf8")).trim()).not.toBe("");
  return Number.parseInt((await readFile(pidFile, "utf8")).trim(), 10);
}

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return (error as NodeJS.ErrnoException).code !== "ESRCH";
  }
}

async function run(command: string, arguments_: string[], cwd: string): Promise<void> {
  const child = spawn(command, arguments_, { cwd, stdio: "inherit" });
  const [code] = (await once(child, "exit")) as [number | null];
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}`);
  }
}
