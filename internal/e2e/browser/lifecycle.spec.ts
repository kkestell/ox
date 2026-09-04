import { expect, test, type Page } from "@playwright/test";
import { BrowserHarness } from "./harness";

let harness: BrowserHarness | undefined;

test.beforeEach(async ({ page }) => {
  harness = await BrowserHarness.start({ logLevel: "debug" });
  await harness.open(page);
});

test.afterEach(async ({}, testInfo) => {
  await harness?.stop(testInfo);
  harness = undefined;
});

test("records browser capabilities and omits unsupported terminal authentication", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");

  await page.getByTitle("ACP Traffic Monitor").click();
  await expect(page.getByText("📡 ACP Traffic", { exact: true })).toBeVisible();
  await page.getByPlaceholder("/absolute/path/on/agent").fill(running.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  await running.waitForAuthorizedRequest("/api/v1/models");
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  const initializeRequest = page.locator(".traffic-monitor .entry.out").filter({ hasText: "initialize" });
  await expect(initializeRequest).toHaveCount(1);
  await initializeRequest.locator(".entry-header").click();
  const request = JSON.parse(await initializeRequest.locator("pre").innerText());
  expect(request).toEqual({
    jsonrpc: "2.0",
    id: 0,
    method: "initialize",
    params: {
      protocolVersion: 1,
      clientCapabilities: {
        fs: {
          readTextFile: false,
          writeTextFile: false,
        },
      },
      clientInfo: {
        name: "acp-ui",
        title: "ACP UI",
        version: "0.1.15",
      },
    },
  });

  const initializeResponse = page.locator(".traffic-monitor .entry.in").filter({ hasText: "initialize" });
  await expect(initializeResponse).toHaveCount(1);
  await initializeResponse.locator(".entry-header").click();
  const response = JSON.parse(await initializeResponse.locator("pre").innerText());
  expect(response).toMatchObject({
    jsonrpc: "2.0",
    id: request.id,
    result: {
      protocolVersion: 1,
    },
  });
  expect(response.result.agentCapabilities.promptCapabilities).toEqual({
    image: true,
    audio: true,
    embeddedContext: true,
  });
  expect(response.result.authMethods).toEqual([{
    id: "openrouter",
    name: "OpenRouter API key",
    description: "Use the configured OpenRouter API key.",
  }]);
});

test("renders a complete tool turn live and from session replay", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");
  const prompt = "Read the browser fixture";
  const reasoning = "I should inspect the fixture first.";
  const answer = "Read **browser fixture** successfully.";
  await running.scriptReadTurn(prompt, reasoning, answer);

  await page.getByTitle("ACP Traffic Monitor").click();
  await page.getByPlaceholder("/absolute/path/on/agent").fill(running.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  const firstPID = await running.rememberOxProcess();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();
  await observeToolStatuses(page);

  await page.getByPlaceholder(/Type your message/).fill(prompt);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".message-user .message-content")).toHaveText(prompt);
  await expect(page.getByRole("button", { name: "Show Thinking" })).toBeVisible();
  await page.getByRole("button", { name: "Show Thinking" }).click();
  await expect(page.locator(".thought-content")).toHaveText(reasoning);
  await expect(page.locator(".tool-call-inline.tool-completed .tool-name")).toHaveText("read_file");
  await expect(page.locator(".message-assistant .message-content strong")).toHaveText("browser fixture");
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();
  expect((await observedToolStatuses(page)).slice(-2)).toEqual(["in_progress", "completed"]);

  const liveUpdates = await trafficPayloads(page, "in", "session/update");
  expect(updateStatuses(liveUpdates, "browser-read")).toEqual(["pending", "in_progress", "completed"]);
  expect(liveUpdates).toContainEqual(expect.objectContaining({
    params: {
      sessionId: expect.any(String),
      update: {
        sessionUpdate: "usage_update",
        used: 13,
        size: 128_000,
        cost: { amount: 0.001, currency: "USD" },
      },
    },
  }));

  await page.getByRole("button", { name: "Disconnect" }).click();
  await running.waitForOxExit(firstPID);
  await page.locator(".session-item").filter({ hasText: prompt }).click();
  const replayPID = await running.rememberOxProcess();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await expect(page.locator(".message")).toHaveCount(2);
  await expect(page.locator(".message-user .message-content")).toHaveText(prompt);
  await expect(page.getByRole("button", { name: "Show Thinking" })).toBeVisible();
  await page.getByRole("button", { name: "Show Thinking" }).click();
  await expect(page.locator(".thought-content")).toHaveText(reasoning);
  await expect(page.locator(".tool-call-inline.tool-completed .tool-name")).toHaveText("read_file");
  await expect(page.locator(".message-assistant .message-content strong")).toHaveText("browser fixture");
  const replayUpdates = await trafficPayloads(page, "in", "session/update");
  expect(replayUpdates).toContainEqual(expect.objectContaining({
    params: {
      sessionId: expect.any(String),
      update: {
        sessionUpdate: "user_message_chunk",
        content: expect.objectContaining({ type: "text", text: prompt }),
        messageId: expect.any(String),
      },
    },
  }));

  await page.getByRole("button", { name: "Disconnect" }).click();
  await running.waitForOxExit(replayPID);
});

test("runs file and shell tools locally through visible permissions", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");
  const prompt = "Run the executor workflow";
  const answer = "Executor workflow complete.";
  await running.scriptExecutorWorkflow(prompt, answer);

  await page.getByTitle("ACP Traffic Monitor").click();
  await page.getByPlaceholder("/absolute/path/on/agent").fill(running.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByPlaceholder(/Type your message/).fill(prompt);
  await page.getByRole("button", { name: "Send" }).click();

  const permission = page.locator(".permission-dialog");
  await expect(permission.getByRole("heading", { name: "Permission Required" })).toBeVisible();
  await expect(permission.locator(".tool-title")).toHaveText("edit_file");
  await permission.getByRole("button", { name: "Allow once" }).click();
  await expect(permission.locator(".tool-title")).toHaveText("shell");
  await permission.getByRole("button", { name: "Allow once" }).click();

  await expect(page.locator(".tool-call-inline.tool-completed .tool-name")).toHaveText([
    "read_file",
    "edit_file",
    "shell",
  ]);
  await expect(page.getByText(answer, { exact: true })).toBeVisible();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();
  expect(await running.readWorkspaceFile("executor-fixture.txt")).toBe("after\n");
  expect(await running.readWorkspaceFile("executor-marker.txt")).toBe("shell-output");

  const initialize = await trafficPayloads(page, "out", "initialize");
  expect(initialize[0].params.clientCapabilities).toEqual({
    fs: { readTextFile: false, writeTextFile: false },
  });
  await expect(page.locator(".traffic-monitor .entry.in").filter({
    hasText: /fs\/(read|write)_text_file|terminal\//,
  })).toHaveCount(0);
  expect(await trafficPayloads(page, "in", "session/request_permission")).toHaveLength(2);
});

test("surfaces protocol errors and confines Ox logs to stderr", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");
  const invalidWorkspace = `${running.workspace}/missing`;
  const parseFailures: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error" && message.text().includes("Failed to parse message:")) {
      parseFailures.push(message.text());
    }
  });

  await page.getByTitle("ACP Traffic Monitor").click();
  await page.getByPlaceholder("/absolute/path/on/agent").fill(invalidWorkspace);
  await page.getByRole("button", { name: "New Session" }).click();

  const usefulError = `cannot access the working directory ${invalidWorkspace}`;
  await expect(page.locator(".error-banner .error-text")).toContainText(usefulError);
  await running.waitForOxLog("ox starting");
  await running.waitForOxLog("client initialized");
  expect(running.errorOutput()).toContain("msg=\"ox starting\"");
  expect(running.errorOutput()).toContain("msg=\"client initialized\"");
  expect(running.standardOutput()).not.toContain("msg=\"ox starting\"");
  expect(running.standardOutput()).not.toContain("msg=\"client initialized\"");
  expect(parseFailures).toEqual([]);
  await expect(page.locator(".traffic-monitor .entry").filter({ hasText: "ox starting" })).toHaveCount(0);

  const newSessionResponses = await trafficPayloads(page, "in", "session/new");
  expect(newSessionResponses).toContainEqual(expect.objectContaining({
    error: expect.objectContaining({ message: expect.stringContaining(usefulError) }),
  }));
});

test("uses the configured credential, streams a prompt, and shuts down", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");
  const held = running.hold("stream this", "First half");

  await page.getByPlaceholder("/absolute/path/on/agent").fill(running.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  const oxPID = await running.rememberOxProcess();
  await running.waitForAuthorizedRequest("/api/v1/models");
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByPlaceholder(/Type your message/).fill("stream this");
  await page.getByRole("button", { name: "Send" }).click();
  await held.waitUntilStarted();
  await expect(page.getByText("First half", { exact: true })).toBeVisible();
  await expect(page.getByPlaceholder(/Type your message/)).toBeDisabled();

  held.finish(" and second half");
  await expect(page.getByText("First half and second half", { exact: true })).toBeVisible();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByRole("button", { name: "Disconnect" }).click();
  await expect(page.getByRole("button", { name: "New Session" })).toBeVisible();
  await running.waitForOxExit(oxPID);
});

test("cancels a streaming prompt and shuts down", async ({ page }) => {
  const running = harness;
  if (!running) throw new Error("browser harness did not start");
  const held = running.hold("cancel this", "Partial answer");

  await page.getByPlaceholder("/absolute/path/on/agent").fill(running.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  const oxPID = await running.rememberOxProcess();
  await running.waitForAuthorizedRequest("/api/v1/models");
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByPlaceholder(/Type your message/).fill("cancel this");
  await page.getByRole("button", { name: "Send" }).click();
  await held.waitUntilStarted();
  await expect(page.getByText("Partial answer", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();
  await held.waitUntilCancelled();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByRole("button", { name: "Disconnect" }).click();
  await running.waitForOxExit(oxPID);
});

async function trafficPayloads(page: Page, direction: "in" | "out", method: string): Promise<any[]> {
  const entries = page.locator(`.traffic-monitor .entry.${direction}`).filter({ hasText: method });
  await expect(entries.first()).toBeVisible();
  const payloads: any[] = [];
  for (let index = 0; index < await entries.count(); index++) {
    const entry = entries.nth(index);
    const payload = entry.locator("pre");
    if (await payload.count() === 0) {
      await entry.locator(".entry-header").click();
    }
    payloads.push(JSON.parse(await entry.locator("pre").innerText()));
  }
  return payloads;
}

function updateStatuses(messages: any[], toolCallID: string): string[] {
  return messages
    .map((message) => message.params?.update)
    .filter((update) => update?.toolCallId === toolCallID && update.status !== undefined)
    .map((update) => update.status);
}

async function observeToolStatuses(page: Page): Promise<void> {
  await page.evaluate(() => {
    const statuses: string[] = [];
    const record = (className: string | null) => {
      const status = className?.match(/(?:^|\s)tool-(pending|in_progress|completed|failed)(?:\s|$)/)?.[1];
      if (status && !statuses.includes(status)) statuses.push(status);
    };
    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        if (mutation.type === "attributes") {
          record(mutation.oldValue);
          record((mutation.target as Element).getAttribute("class"));
        }
        for (const node of mutation.addedNodes) {
          if (!(node instanceof Element)) continue;
          record(node.getAttribute("class"));
          for (const tool of node.querySelectorAll(".tool-call-inline")) {
            record(tool.getAttribute("class"));
          }
        }
      }
    });
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeOldValue: true,
      attributeFilter: ["class"],
    });
    (window as unknown as { __oxToolStatuses: string[] }).__oxToolStatuses = statuses;
  });
}

async function observedToolStatuses(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    (window as unknown as { __oxToolStatuses: string[] }).__oxToolStatuses,
  );
}
