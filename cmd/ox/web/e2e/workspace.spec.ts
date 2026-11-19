import { expect, test } from "@playwright/test";
import { fakeConfig, startServices } from "./services";

import { hold, prompt, send } from "./ui";

test("the workspace sidebar works after opening a malformed session link", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    const list = await hold(page, "workspace/list");
    await page.goto(`${services.url}/#%`);
    await list.arrived;
    await page.getByRole("button", { name: "Add workspace" }).click();
    await page.getByPlaceholder("/absolute/path/to/directory").fill("/");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTitle("/").locator(".name")).toHaveText("/");
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("draft before catalog");
    await expect(send(page)).toBeDisabled();
    list.release();
    await expect(page.locator("select[name=permission_mode]")).toHaveValue("manual");
    const mode = page.locator("select[name=permission_mode]");
    await mode.selectOption("plan");
    const reconnect = await hold(page, "workspace/list");
    await services.restartRelay();
    await reconnect.arrived;
    reconnect.release();
    await expect(send(page)).toBeEnabled();
    await expect(mode).toHaveValue("plan");
    await send(page).click();
    await expect(page.locator(".reply")).toHaveText("You said: draft before catalog");
  } finally {
    await services.stop();
  }
});
