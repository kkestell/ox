import { expect, test } from "@playwright/test";
import { BrowserHarness } from "./harness";

let harness: BrowserHarness | undefined;

test.afterEach(async ({}, testInfo) => {
  await harness?.stop(testInfo);
  harness = undefined;
});

test("completes a browser turn through real OpenRouter", async ({ page }) => {
  test.setTimeout(120_000);
  const apiKey = process.env.OPENROUTER_API_KEY;
  if (!apiKey) throw new Error("OPENROUTER_API_KEY is required for the live client test");
  harness = await BrowserHarness.start({ liveAPIKey: apiKey });
  await harness.open(page);
  await page.getByPlaceholder("/absolute/path/on/agent").fill(harness.workspace);
  await page.getByRole("button", { name: "New Session" }).click();
  const oxPID = await harness.rememberOxProcess();
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled({ timeout: 60_000 });

  await page.getByPlaceholder(/Type your message/).fill("Reply with exactly: OX LIVE OK");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("OX LIVE OK", { exact: true })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByPlaceholder(/Type your message/)).toBeEnabled();

  await page.getByRole("button", { name: "Disconnect" }).click();
  await harness.waitForOxExit(oxPID);
});
