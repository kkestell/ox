import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Request, type Route, test } from "@playwright/test";
import { fakeConfig, startServices } from "./services";

import { hold, menuItem, open, openMenu, prompt, send, sessionButton, sessionRow, workspaceRow } from "./ui";

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

    const remove = async (path: string) => menuItem(await openMenu(workspaceRow(page, path)), "Remove workspace");
    const removeDialog = page.getByRole("alertdialog");
    const confirm = removeDialog.getByRole("button", { name: "Remove", exact: true });
    const firstRemove = await hold(page, "workspace/remove");
    await (await remove("/")).click();
    await confirm.click();
    await firstRemove.arrived;
    await removeDialog.getByRole("button", { name: "Cancel" }).click();
    await (await remove(services.workspace)).click();
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
    const workspace = workspaceRow(page, services.workspace);
    const remove = async () => menuItem(await openMenu(workspace), "Remove workspace");
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("hello");
    await send(page).click();
    await expect(page.locator(".reply")).toHaveText("You said: hello");
    await expect(await remove()).toHaveAccessibleName("Remove workspace Delete its sessions first");
    await expect(await remove()).toBeDisabled();
    await page.keyboard.press("Escape");

    // An archived session, hidden or not, still keeps the workspace.
    await menuItem(await openMenu(sessionRow(page, "hello")), "Archive").click();
    await expect(sessionButton(page, "hello")).toHaveCount(0);
    await expect(await remove()).toHaveAccessibleName(
      "Remove workspace Delete its sessions first, including archived ones",
    );
    await expect(await remove()).toBeDisabled();
    await page.keyboard.press("Escape");
    await menuItem(await openMenu(workspace), "Show archived").click();
    await menuItem(await openMenu(sessionRow(page, "hello")), "Delete").click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(sessionButton(page, "hello")).toHaveCount(0);
    await expect(await remove()).toBeEnabled();
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("unsent");
    await (await remove()).click();
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

test("the file tree lists the workspace and keeps each session's expanded folders", async ({ page }) => {
  const services = await startServices(fakeConfig);
  const locked = join(services.workspace, "locked");
  const other = mkdtempSync(join(tmpdir(), "ox-e2e-other-"));
  writeFileSync(join(other, "other.txt"), "");
  try {
    mkdirSync(join(services.workspace, "src"));
    mkdirSync(join(services.workspace, ".git"));
    mkdirSync(join(services.workspace, "empty"));
    mkdirSync(locked, { mode: 0o000 });
    writeFileSync(join(services.workspace, "src", "a.txt"), "");
    writeFileSync(join(services.workspace, ".git", "HEAD"), "");
    writeFileSync(join(services.workspace, ".gitignore"), "");
    const listed: string[] = [];
    page.on("request", (request) => {
      const body = request.url().includes("/rpc") && request.postDataJSON();
      if (body?.method === "workspace/list_folder") listed.push(body.params.path);
    });
    const quiet = async (path?: string) => {
      const before = listed.length;
      await page.waitForTimeout(2500);
      expect(listed.slice(before).filter((p) => path === undefined || p === path)).toEqual([]);
    };

    await open(page, services);
    for (const text of ["first", "second"]) {
      await page.getByRole("button", { name: "New session" }).click();
      await prompt(page).fill(text);
      await send(page).click();
      await expect(page.locator(".reply")).toHaveText(`You said: ${text}`);
    }
    const files = page.getByRole("complementary", { name: "Files" });
    const rows = files.locator(".row");
    const src = files.getByRole("button", { name: "src" });
    await expect(rows).toHaveText(["empty", "locked", "src", ".gitignore"]);
    expect(listed).not.toContain("src");

    await sessionButton(page, "first").click();
    await src.click();
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", ".gitignore"]);
    writeFileSync(join(services.workspace, "src", "b.txt"), "");
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"], { timeout: 3000 });

    await sessionButton(page, "second").click();
    await expect(src).toHaveAttribute("aria-expanded", "false");
    await expect(rows).toHaveText(["empty", "locked", "src", ".gitignore"]);
    await sessionButton(page, "first").click();
    await expect(src).toHaveAttribute("aria-expanded", "true");
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);

    const setVisibility = (state: string) =>
      page.evaluate((state) => {
        Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
        document.dispatchEvent(new Event("visibilitychange"));
      }, state);
    await setVisibility("hidden");
    await page.waitForTimeout(100);
    await quiet();
    const visible = page.waitForRequest(
      (r) => r.url().includes("/rpc") && r.postDataJSON()?.method === "workspace/list_folder",
    );
    await setVisibility("visible");
    await visible;

    // A listing in flight holds back the next one for its folder.
    const isSrc = (request: Request) => {
      const body = request.url().includes("/rpc") && request.postDataJSON();
      return body?.method === "workspace/list_folder" && body.params.path === "src";
    };
    let release!: () => void;
    const released = new Promise<void>((resolve) => {
      release = resolve;
    });
    let taken = false;
    const holdSrc = async (route: Route) => {
      if (taken || !isSrc(route.request())) return route.fallback();
      taken = true;
      await released;
      await route.continue();
    };
    await page.route("**/rpc*", holdSrc);
    await expect.poll(() => taken).toBe(true);
    await quiet("src");
    const answered = page.waitForResponse((r) => isSrc(r.request()));
    release();
    await answered;
    await page.unroute("**/rpc*", holdSrc);

    // A relay failure keeps the last listing.
    let failed = 0;
    const failSrc = (route: Route) => {
      const body = route.request().postDataJSON();
      if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
      failed++;
      return route.fulfill({ status: 502, body: "relay unavailable" });
    };
    await page.route("**/rpc*", failSrc);
    await expect.poll(() => failed).toBeGreaterThanOrEqual(2);
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);
    await page.unroute("**/rpc*", failSrc);

    // A lost connection lists nothing and keeps the tree; listing waits until
    // the page has reloaded.
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toBeVisible();
    await page.waitForTimeout(100);
    await quiet();
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);
    const reload = await hold(page, "workspace/list");
    await page.unroute("**/events");
    await reload.arrived;
    await quiet();
    const resumed = page.waitForRequest(
      (r) => r.url().includes("/rpc") && r.postDataJSON()?.method === "workspace/list_folder",
    );
    reload.release();
    await resumed;

    await files.getByRole("button", { name: "locked" }).click();
    await expect(files.locator(".note")).toContainText("permission denied");
    await files.getByRole("button", { name: "empty" }).click();
    await expect(files.locator(".note").first()).toHaveText("Empty");
    await src.click();
    await expect(src).toHaveAttribute("aria-expanded", "false");
    await expect(files.getByText("a.txt")).toHaveCount(0);

    // Another workspace's draft shows that workspace's tree.
    await page.getByRole("button", { name: "Add workspace" }).click();
    await page.getByPlaceholder("/absolute/path/to/directory").fill(other);
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await page.getByTitle(other).getByRole("button", { name: "New session" }).click();
    await expect(files.locator("p").first()).toHaveText(basename(other));
    await expect(rows).toHaveText(["other.txt"]);
  } finally {
    chmodSync(locked, 0o755);
    rmSync(other, { recursive: true, force: true });
    await services.stop();
  }
});
