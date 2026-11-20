import { expect, test } from "@playwright/test";
import { fakeConfig, startServices } from "./services";

import { hold, open, prompt, send, sessionButton } from "./ui";

test("the workspace sidebar works after opening a malformed session link", async ({ page }) => {
  const fake = fakeConfig.harnesses.fake;
  const services = await startServices({
    harnesses: { fake: { ...fake, models: fake.models.map((model) => ({ ...model, efforts: [] })) } },
    defaults: { ...fakeConfig.defaults, effort: "" },
  });
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
    const create = page.waitForRequest(
      (r) => r.url().includes("/rpc") && r.postDataJSON()?.method === "session/create",
    );
    await send(page).click();
    expect((await create).postDataJSON().params).not.toHaveProperty("effort");
    await expect(page.locator(".reply")).toHaveText("You said: draft before catalog");
  } finally {
    await services.stop();
  }
});

test("a cancelled add or remove cannot settle the next dialog", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    await page.goto(services.url);
    const add = page.getByRole("button", { name: "Add workspace" });
    const addDialog = page.getByRole("dialog");
    const path = addDialog.getByPlaceholder("/absolute/path/to/directory");
    const submit = addDialog.getByRole("button", { name: "Add", exact: true });
    const firstAdd = await hold(page, "workspace/create");
    await add.click();
    await path.fill("/");
    await submit.click();
    await firstAdd.arrived;
    await addDialog.getByRole("button", { name: "Cancel" }).click();
    await add.click();
    await path.fill(services.workspace);
    firstAdd.release();
    await expect(page.getByTitle("/", { exact: true })).toBeVisible();
    await expect(path).toHaveValue(services.workspace);
    await submit.click();
    await expect(page.getByTitle(services.workspace)).toBeVisible();

    const remove = (path: string) =>
      page.getByTitle(path, { exact: true }).getByRole("button", { name: "Remove workspace" });
    const removeDialog = page.getByRole("alertdialog");
    const confirm = removeDialog.getByRole("button", { name: "Remove", exact: true });
    const firstRemove = await hold(page, "workspace/remove");
    await remove("/").click();
    await confirm.click();
    await firstRemove.arrived;
    await removeDialog.getByRole("button", { name: "Cancel" }).click();
    await remove(services.workspace).click();
    firstRemove.release();
    await expect(page.getByTitle("/", { exact: true })).toHaveCount(0);
    await expect(removeDialog).toContainText(services.workspace);
    await confirm.click();
    await expect(page.getByTitle(services.workspace)).toHaveCount(0);
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
