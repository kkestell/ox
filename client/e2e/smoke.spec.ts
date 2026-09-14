import { expect, test } from "@playwright/test";
import * as acp from "@agentclientprotocol/sdk";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { Readable, Writable } from "node:stream";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

const clientRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(clientRoot, "..");

test("renders the shell and independently drives Ox through the deterministic provider", async ({ page }) => {
  const fixture = await createFixture();
  const host = startBrowserHost();
  try {
    const url = await host.url;
    await page.goto(url);
    await expect(page.getByRole("heading", { name: "Ox" })).toBeVisible();
    await expect(page.getByText("Browser ACP client")).toBeVisible();
    await expect(page.getByText("Connected")).toBeVisible();

    await driveOx(fixture);
    expect(fixture.requests).toBe(1);
  } finally {
    host.stop();
    await fixture.close();
  }
});

function startBrowserHost(): { stop(): void; url: Promise<string> } {
  const child = spawn("bun", ["run", "./src/host.ts", "--port", "0"], {
    cwd: clientRoot,
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
    stop() {
      child.kill();
    },
    url,
  };
}

type Fixture = {
  close(): Promise<void>;
  credential: string;
  providerURL: string;
  requests: number;
  workspace: string;
};

async function createFixture(): Promise<Fixture> {
  const workspace = await mkdtemp(join(tmpdir(), "ox-client-smoke-"));
  const provider = createServer();
  const fixture: Fixture = {
    async close() {
      provider.close();
      await once(provider, "close");
      await rm(workspace, { force: true, recursive: true });
    },
    credential: join(workspace, "credential"),
    providerURL: "",
    requests: 0,
    workspace,
  };
  provider.on("request", (request, response) => {
    if (request.method !== "POST" || request.url !== "/api/v1/chat/completions") {
      response.writeHead(404).end();
      return;
    }
    fixture.requests += 1;
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
  await new Promise<void>((resolveListen) => provider.listen(0, "127.0.0.1", resolveListen));
  const address = provider.address();
  if (!address || typeof address === "string") {
    throw new Error("provider did not bind a TCP address");
  }
  fixture.providerURL = `http://127.0.0.1:${address.port}`;
  await seedOxFixture(fixture);
  return fixture;
}

async function seedOxFixture(fixture: Fixture): Promise<void> {
  await mkdir(join(fixture.workspace, "cache", "ox"), { recursive: true });
  await mkdir(join(fixture.workspace, "config", "ox"), { recursive: true });
  await writeFile(fixture.credential, "test-key\n", { mode: 0o600 });
  await writeFile(
    join(fixture.workspace, "cache", "ox", "models.json"),
    JSON.stringify({
      data: [
        {
          architecture: { input_modalities: ["text"] },
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
}

async function driveOx(fixture: Fixture): Promise<void> {
  const binary = join(fixture.workspace, "ox");
  await run("go", ["build", "-o", binary, "./cmd/ox"], repositoryRoot);
  const child = spawn(
    binary,
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
      env: {
        ...process.env,
        HOME: fixture.workspace,
        XDG_CACHE_HOME: join(fixture.workspace, "cache"),
        XDG_CONFIG_HOME: join(fixture.workspace, "config"),
        XDG_DATA_HOME: join(fixture.workspace, "data"),
      },
      stdio: ["pipe", "pipe", "pipe"],
    },
  );
  const stream = acp.ndJsonStream(
    Writable.toWeb(child.stdin),
    Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
  );
  try {
    await acp
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
      });
  } finally {
    child.stdin.end();
    await once(child, "exit");
  }
}

async function run(command: string, arguments_: string[], cwd: string): Promise<void> {
  const child = spawn(command, arguments_, { cwd, stdio: "inherit" });
  const [code] = (await once(child, "exit")) as [number | null];
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}`);
  }
}
