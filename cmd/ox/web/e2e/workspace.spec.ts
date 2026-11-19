import { expect, test } from "@playwright/test";
import { fakeConfig, startServices } from "./services";

import { hold, open, prompt, send, sessionButton } from "./ui";

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

test("a workspace is removed once it has no sessions", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    await open(page, services);
    const remove = page.getByRole("button", { name: "Remove workspace" });
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("hello");
    await send(page).click();
    await expect(page.locator(".reply")).toHaveText("You said: hello");
    await expect(remove).toHaveCount(0);

    await sessionButton(page, "hello").hover();
    await page.getByRole("button", { name: "Delete session" }).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(remove).toHaveCount(1);

    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("unsent");
    await remove.click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toContainText(`This removes "${services.workspace}" from Ox. The directory stays on disk.`, {
      useInnerText: true,
    });
    const removal = await hold(page, "workspace/remove");
    await dialog.getByRole("button", { name: "Remove", exact: true }).click();
    await removal.arrived;
    removal.fail("relay unavailable");
    await expect(dialog.getByRole("alert")).toHaveText("relay unavailable");
    await expect(page.getByTitle(services.workspace)).toBeVisible();

    await dialog.getByRole("button", { name: "Remove", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByTitle(services.workspace)).toHaveCount(0);
    await expect(prompt(page)).toHaveCount(0);
  } finally {
    await services.stop();
  }
});
