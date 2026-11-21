import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Locator, type Request, type Route, test } from "@playwright/test";
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

    // A relay error shows in place of the folder's listing until the next
    // listing replaces it.
    const failSrc = (route: Route) => {
      const body = route.request().postDataJSON();
      if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
      return route.fulfill({ status: 502, body: "relay unavailable" });
    };
    await page.route("**/rpc*", failSrc);
    await expect(rows).toHaveText(["empty", "locked", "src", "relay unavailable", ".gitignore"]);
    await page.unroute("**/rpc*", failSrc);
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);

    // A request that cannot reach the relay, or that the relay answers after
    // its oxd connection is gone, keeps the last listing.
    for (const unreachable of [
      (route: Route) => route.abort(),
      (route: Route) => route.fulfill({ status: 503, body: "unknown connection" }),
    ]) {
      let answered = 0;
      const answerSrc = (route: Route) => {
        const body = route.request().postDataJSON();
        if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
        answered++;
        return unreachable(route);
      };
      await page.route("**/rpc*", answerSrc);
      await expect.poll(() => answered).toBeGreaterThanOrEqual(2);
      await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);
      await page.unroute("**/rpc*", answerSrc);
    }

    // A lost connection lists nothing and keeps the tree, including a listing
    // in flight and a folder expanded while disconnected; listing waits until
    // the page has reloaded.
    const lostListing = await hold(page, "workspace/list_folder", { path: "src" });
    await lostListing.arrived;
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toBeVisible();
    const empty = files.getByRole("button", { name: "empty" });
    await empty.click();
    await page.waitForTimeout(100);
    await quiet();
    await expect(rows).toHaveText(["empty", "locked", "src", "a.txt", "b.txt", ".gitignore"]);
    await empty.click();
    lostListing.release();
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

test("the file viewer shows each session's open file", async ({ page }) => {
  const services = await startServices(fakeConfig);
  const a = join(services.workspace, "src", "a.txt");
  try {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(a, `first\n${`${"long line ".repeat(200)}\n`.repeat(400)}`);
    writeFileSync(join(services.workspace, "bin.dat"), "a\0b");
    const main = join(services.workspace, "src", "main.go");
    const template = `const s = \`a \${b}\`;\r\nlet c = 1;\rlet d = 2;\n`;
    writeFileSync(join(services.workspace, "src", "template.ts"), template);
    writeFileSync(main, "package main\n\n// note\nfunc main() {}\n");
    writeFileSync(join(services.workspace, "src", "main.txt"), "package main\n\n// note\nfunc main() {}\n");
    await open(page, services);
    for (const text of ["first", "second"]) {
      await page.getByRole("button", { name: "New session" }).click();
      await prompt(page).fill(text);
      await send(page).click();
      await expect(page.locator(".reply")).toHaveText(`You said: ${text}`);
    }
    const files = page.getByRole("complementary", { name: "Files" });
    const src = files.getByRole("button", { name: "src" });
    const aRow = files.getByRole("button", { name: "a.txt", exact: true });
    const binRow = files.getByRole("button", { name: "bin.dat", exact: true });
    const viewer = page.getByRole("region", { name: "File" });
    const tab = viewer.getByRole("tab");
    const panel = viewer.getByRole("tabpanel");

    await sessionButton(page, "first").click();
    await src.click();
    await aRow.click();
    await expect(tab).toHaveText("a.txt");
    await expect(tab).toHaveAttribute("title", "src/a.txt");
    await expect(panel).toContainText("first");
    await expect(aRow).toHaveAttribute("aria-current", "true");
    const height = async (locator: Locator) => (await locator.boundingBox())?.height;
    expect(await height(viewer.locator("header"))).toBe(await height(page.locator("header.context h1")));
    const width = async (locator: Locator) => (await locator.boundingBox())?.width ?? 0;
    expect(Math.abs((await width(page.locator("main"))) - (await width(viewer)))).toBeLessThanOrEqual(1);
    expect(await panel.evaluate((pre) => pre.scrollWidth > pre.clientWidth)).toBe(true);

    // Reading an unchanged file keeps the pane's scroll position.
    const scrolled = await panel.evaluate((pre) => {
      pre.scrollTop = 2000;
      pre.scrollLeft = 300;
      return [pre.scrollTop, pre.scrollLeft];
    });
    expect(scrolled).toEqual([2000, 300]);
    await page.waitForTimeout(2500);
    expect(await panel.evaluate((pre) => [pre.scrollTop, pre.scrollLeft])).toEqual(scrolled);

    writeFileSync(a, "changed\n");
    await expect(panel).toHaveText("changed\n", { timeout: 3000 });

    // Clicking the open file leaves its view alone.
    await panel.evaluate((pre) => {
      pre.dataset.marked = "true";
    });
    await aRow.click();
    await page.waitForTimeout(1500);
    await expect(viewer.locator("pre[data-marked]")).toHaveCount(1);

    rmSync(a);
    await expect(panel).toContainText("no such file or directory", { timeout: 3000 });
    await expect(tab).toHaveText("a.txt");
    writeFileSync(a, "back\n");
    await expect(panel).toHaveText("back\n", { timeout: 3000 });

    // A file of a language Ox does not register shows as plain text; a Go file
    // is colored for the page's scheme, and its pane is not re-rendered while
    // the file is unchanged.
    await expect(panel.locator("span")).toHaveCount(0);
    await files.getByRole("button", { name: "main.txt", exact: true }).click();
    await expect(panel).toContainText("package main");
    await expect(panel.locator("span")).toHaveCount(0);
    const firstRead = await hold(page, "workspace/read_file", { path: "src/main.go" });
    await files.getByRole("button", { name: "main.go", exact: true }).click();
    await expect(tab).toHaveText("main.go");
    await firstRead.arrived;
    await expect(panel).toHaveText("");
    firstRead.release();
    const keyword = panel.locator(".hljs-keyword").first();
    const comment = panel.locator(".hljs-comment");
    await expect(keyword).toHaveText("package");
    await expect(comment).toHaveText("// note");
    await expect(keyword).toHaveCSS("color", "rgb(215, 58, 73)");
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(keyword).toHaveCSS("color", "rgb(255, 123, 114)");
    await page.emulateMedia({ colorScheme: "light" });
    await comment.evaluate((span) => {
      span.dataset.marked = "true";
    });
    await page.waitForTimeout(1500);
    await expect(panel.locator("span[data-marked]")).toHaveCount(1);
    writeFileSync(main, "// changed\n");
    await expect(comment).toHaveText("// changed", { timeout: 3000 });

    // The pane keeps the file's carriage returns, and a substitution inside a
    // string keeps the pane's foreground.
    await files.getByRole("button", { name: "template.ts", exact: true }).click();
    await expect(tab).toHaveText("template.ts");
    await expect(panel.locator(".hljs-subst")).toHaveCount(1);
    expect(await panel.evaluate((pre) => pre.textContent)).toBe(template);
    const color = (element: Element) => getComputedStyle(element).color;
    expect(await panel.locator(".hljs-subst").evaluate(color)).toBe(await panel.evaluate(color));

    // A relay error on a read shows in the pane until the next read replaces it.
    const failedRead = await hold(page, "workspace/read_file", { path: "bin.dat" });
    await binRow.click();
    await expect(tab).toHaveText("bin.dat");
    await failedRead.arrived;
    failedRead.fail("relay unavailable");
    await expect(panel).toHaveText("relay unavailable");
    await expect(panel).toHaveText("file is binary");
    await expect(aRow).not.toHaveAttribute("aria-current");

    await sessionButton(page, "second").click();
    await expect(viewer).toHaveCount(0);
    await sessionButton(page, "first").click();
    await expect(tab).toHaveText("bin.dat");

    await viewer.getByRole("button", { name: "Close bin.dat" }).click();
    await expect(viewer).toHaveCount(0);
    await expect(binRow).not.toHaveAttribute("aria-current");

    // A draft keeps its open file, and the session it creates inherits it.
    await page.getByRole("button", { name: "New session" }).click();
    await src.click();
    await aRow.click();
    await expect(tab).toHaveText("a.txt");
    await sessionButton(page, "second").click();
    await expect(viewer).toHaveCount(0);
    await page.getByRole("button", { name: "New session" }).click();
    await expect(tab).toHaveText("a.txt");
    await prompt(page).fill("third");
    await send(page).click();
    await expect(page.locator(".reply")).toHaveText("You said: third");
    await expect(tab).toHaveText("a.txt");
    await expect(panel).toHaveText("back\n");

    await page.reload();
    await expect(page.locator(".reply")).toHaveText("You said: third");
    await expect(files.getByRole("button", { name: "src" })).toBeVisible();
    await expect(viewer).toHaveCount(0);
  } finally {
    await services.stop();
  }
});
