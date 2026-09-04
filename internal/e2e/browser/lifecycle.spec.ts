import { expect, test } from "@playwright/test";
import { BrowserHarness } from "./harness";

let harness: BrowserHarness | undefined;

test.beforeEach(async ({ page }) => {
  harness = await BrowserHarness.start();
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
  expect(response.result.authMethods).toEqual([{
    id: "openrouter",
    name: "OpenRouter API key",
    description: "Use the configured OpenRouter API key.",
  }]);
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
