// Approvals against real Claude with Haiku. They run only with OX_LIVE=1, as
// make live does.
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { haikuConfig } from "./services";
import { allow, expect, newSessionButton, permissionMode, prompt, stop, test, toolCall, waiting } from "./ui";

test.skip(!process.env.OX_LIVE, "set OX_LIVE=1 to run against real Claude with Haiku");
test.setTimeout(180_000);
test.use({ config: haikuConfig });

// Claude writes plan files under the test's workspace, which the services
// fixture removes, rather than in the user's ~/.claude/plans.
test.beforeEach(({ services }) => {
  mkdirSync(join(services.workspace, ".claude"));
  writeFileSync(join(services.workspace, ".claude", "settings.json"), '{"plansDirectory": ".claude/plans"}');
});

test("Allow runs the tool", async ({ ox: page, services }) => {
  await newSessionButton(page).click();
  await expect(permissionMode(page)).toHaveValue("manual");
  const approval = waiting(page).getByRole("article", { name: "Allow Bash?" });
  await prompt(page).fill("Run the Bash command `touch allowed.txt`, then reply with just the word done.");
  await prompt(page).press("Enter");
  await expect(approval).toContainText("touch allowed.txt", { timeout: 120_000 });
  await allow(approval);
  await expect(approval).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0, { timeout: 120_000 });
  expect(existsSync(join(services.workspace, "allowed.txt"))).toBe(true);
});

test("a typed reply denies with that message", async ({ ox: page, services }) => {
  await newSessionButton(page).click();
  await expect(permissionMode(page)).toHaveValue("manual");
  const approval = waiting(page).getByRole("article", { name: "Allow Bash?" });
  await prompt(page).fill(
    "Run the Bash command `touch denied.txt`. If it is denied, reply with just the word stopped.",
  );
  await prompt(page).press("Enter");
  await expect(approval).toContainText("touch denied.txt", { timeout: 120_000 });
  await approval.getByRole("textbox", { name: "Other response" }).fill("Do not create files.");
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0, { timeout: 120_000 });
  await expect(toolCall(page, "Bash")).toContainText("Do not create files.");
  expect(existsSync(join(services.workspace, "denied.txt"))).toBe(false);
});

test("an ExitPlanMode plan renders as Markdown", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await permissionMode(page).selectOption("plan");
  const approval = waiting(page).getByRole("article", { name: "Allow ExitPlanMode?" });
  await prompt(page).fill(
    "Load and call the ExitPlanMode tool now with this plan, as a Markdown list of exactly two items: first step, second step.",
  );
  await prompt(page).press("Enter");
  await expect(approval.getByRole("listitem")).toHaveCount(2, { timeout: 120_000 });
  await expect(approval.getByText("Input", { exact: true })).toBeVisible();
  await approval.getByRole("radio", { name: "Deny" }).check();
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0, { timeout: 120_000 });

  const tool = toolCall(page, "ExitPlanMode");
  await tool.getByText("ExitPlanMode", { exact: true }).click();
  await expect(tool.getByRole("listitem").first()).toBeVisible();
});

test("an AskUserQuestion is answered from its form", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await expect(permissionMode(page)).toHaveValue("manual");
  const approval = waiting(page).getByRole("article", { name: "Claude asks" });
  await prompt(page).fill(
    'Load and call the AskUserQuestion tool now with one single-select question, "Which color?", with the options Red and Blue. Then reply with just the chosen color.',
  );
  await prompt(page).press("Enter");
  await expect(approval.getByRole("group", { name: /Which color\?/ })).toBeVisible({ timeout: 120_000 });
  await approval.getByRole("radio", { name: "Red" }).check();
  await approval.getByRole("button", { name: "Submit" }).click();
  await expect(approval).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0, { timeout: 120_000 });
  await expect(toolCall(page, "AskUserQuestion")).toContainText('="Red"');
});
