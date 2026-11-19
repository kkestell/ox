// Approvals against real Claude with Haiku. They run only with OX_LIVE=1, as
// make live does.
import { existsSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { haikuConfig, type Services, startServices } from "./services";
import { open, prompt, send } from "./ui";

test.skip(!process.env.OX_LIVE, "set OX_LIVE=1 to run against real Claude with Haiku");
test.setTimeout(180_000);

let services: Services;
test.beforeAll(async () => {
  services = await startServices(haikuConfig);
});
test.afterAll(async () => {
  await services.stop();
});

test("an approval is answered from its card", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("manual");
  const approval = page.locator(".approval");

  await prompt(page).fill("Run the Bash command `touch allowed.txt`, then reply with just the word done.");
  await prompt(page).press("Enter");
  await expect(approval).toContainText("touch allowed.txt", { timeout: 120_000 });
  await approval.getByLabel("Allow").check();
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(send(page)).toBeVisible({ timeout: 120_000 });
  expect(existsSync(join(services.workspace, "allowed.txt"))).toBe(true);

  await prompt(page).fill(
    "Run the Bash command `touch denied.txt`. If it is denied, reply with just the word stopped.",
  );
  await prompt(page).press("Enter");
  await expect(approval).toContainText("touch denied.txt", { timeout: 120_000 });
  await approval.getByLabel("Other response").fill("Do not create files.");
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(send(page)).toBeVisible({ timeout: 120_000 });
  expect(existsSync(join(services.workspace, "denied.txt"))).toBe(false);
});

test("an ExitPlanMode plan renders as Markdown", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await page.locator("select[name=permission_mode]").selectOption("plan");
  const approval = page.locator(".approval");

  await prompt(page).fill(
    "Load and call the ExitPlanMode tool now with this plan, as a Markdown list of exactly two items: first step, second step.",
  );
  await prompt(page).press("Enter");
  await expect(approval.locator(".plan li").nth(1)).toBeVisible({ timeout: 120_000 });
  await expect(approval.locator("summary .subject")).toHaveText("Input");
  await approval.getByLabel("Deny").check();
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(send(page)).toBeVisible({ timeout: 120_000 });

  const tool = page.locator("details.tool").filter({ has: page.locator(".name", { hasText: "ExitPlanMode" }) });
  await tool.locator("summary").click();
  await expect(tool.locator(".plan li").first()).toBeVisible();
});

test("an AskUserQuestion is answered from its form", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("manual");
  const approval = page.locator(".approval");

  await prompt(page).fill(
    'Load and call the AskUserQuestion tool now with one single-select question, "Which color?", with the options Red and Blue. Then reply with just the chosen color.',
  );
  await prompt(page).press("Enter");
  await expect(approval.locator("fieldset")).toBeVisible({ timeout: 120_000 });
  await approval.getByLabel("Red").check();
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(send(page)).toBeVisible({ timeout: 120_000 });

  const tool = page.locator("details.tool").filter({ has: page.locator(".name", { hasText: "AskUserQuestion" }) });
  await tool.locator("summary").click();
  await expect(tool).toContainText('="Red"');
});
