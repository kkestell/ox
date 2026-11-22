import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Locator, type Request, type Route, test } from "@playwright/test";
import { fakeConfig, startServices } from "./services";

import { hold, menuItem, open, openMenu, prompt, send, sessionButton, sessionRow, workspaceRow } from "./ui";

test("a malformed session link still lists workspaces", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    await page.goto(`${services.url}/#%`);
    await page.getByRole("button", { name: "Add workspace" }).click();
    await page.getByPlaceholder("/absolute/path/to/directory").fill("/");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTitle("/").locator(".name")).toHaveText("/");
  } finally {
    await services.stop();
  }
});

test("a draft holds Send until the session list loads", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    const list = await hold(page, "workspace/list");
    await page.goto(services.url);
    await list.arrived;
    await page.getByRole("button", { name: "Add workspace" }).click();
    await page.getByPlaceholder("/absolute/path/to/directory").fill(services.workspace);
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTitle(services.workspace)).toBeVisible();
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("draft before catalog");
    await expect(send(page)).toBeDisabled();
    list.release();
    await expect(send(page)).toBeEnabled();
  } finally {
    await services.stop();
  }
});

test("a draft keeps its permission mode through a relay restart", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    let creates = 0;
    page.on("request", (request) => {
      if (request.url().includes("/rpc") && request.postDataJSON()?.method === "session/create") creates++;
    });
    await open(page, services);
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("draft through a restart");
    const mode = page.locator("select[name=permission_mode]");
    await expect(mode).toHaveValue("manual");
    await mode.selectOption("plan");
    const reconnect = await hold(page, "workspace/list");
    await services.restartRelay();
    await reconnect.arrived;
    await expect(send(page)).toBeDisabled();
    await expect(mode).toBeDisabled();
    await expect(page.locator("button.picker")).toBeDisabled();
    await prompt(page).press("Enter");
    reconnect.release();
    await expect(send(page)).toBeEnabled();
    await expect(mode).toHaveValue("plan");
    await expect(prompt(page)).toHaveValue("draft through a restart");
    expect(creates).toBe(0);
  } finally {
    await services.stop();
  }
});

test("a model without efforts creates a session without effort", async ({ page }) => {
  const fake = fakeConfig.harnesses.fake;
  const services = await startServices({
    ...fakeConfig,
    harnesses: { fake: { ...fake, models: fake.models.map((model) => ({ ...model, efforts: [] })) } },
    defaults: { ...fakeConfig.defaults, effort: "" },
  });
  try {
    await open(page, services);
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("no effort");
    const create = page.waitForRequest(
      (r) => r.url().includes("/rpc") && r.postDataJSON()?.method === "session/create",
    );
    await send(page).click();
    expect((await create).postDataJSON().params).not.toHaveProperty("effort");
    await expect(page.locator(".reply")).toHaveText("You said: no effort");
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

test("panel toggles and drags change the layout, which lasts until reload", async ({ page }) => {
  const services = await startServices(fakeConfig);
  try {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
    await page.setViewportSize({ width: 1600, height: 900 });
    await open(page, services);
    const nav = page.locator("nav");
    const main = page.locator("main");
    const files = page.getByRole("complementary", { name: "Files" });
    const viewer = page.getByRole("region", { name: "File" });
    const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
    const toggleFiles = page.getByRole("button", { name: "Toggle files" });
    const width = async (locator: Locator) => (await locator.boundingBox())?.width ?? 0;
    const color = (locator: Locator) => locator.evaluate((element) => getComputedStyle(element).color);
    const addColor = await color(page.getByRole("button", { name: "Add workspace" }));
    const openFile = async () => {
      await files.getByRole("button", { name: "src" }).click();
      await files.getByRole("button", { name: "a.txt", exact: true }).click();
      await expect(viewer).toBeVisible();
    };
    // alone checks that the transcript fills the width between the panels.
    const alone = async () =>
      expect(Math.abs((await width(main)) - (1600 - (await width(nav)) - (await width(files))))).toBeLessThanOrEqual(1);
    const even = async () => expect(Math.abs((await width(main)) - (await width(viewer)))).toBeLessThanOrEqual(1);
    const cursor = () => page.evaluate(() => getComputedStyle(document.body).cursor);
    // drag moves a sash dx pixels along the x axis with the given button.
    const drag = async (label: string, dx: number, button: "left" | "right" = "left") => {
      const box = await page.getByRole("separator", { name: label }).boundingBox();
      if (!box) throw new Error(`no ${label} sash`);
      const y = box.y + box.height / 2;
      await page.mouse.move(box.x, y);
      await page.mouse.down({ button });
      await page.mouse.move(box.x + dx, y, { steps: 5 });
      if (button === "left") expect(await cursor()).toBe("col-resize");
      await page.mouse.up({ button });
    };

    // With nothing selected, the toggles sit in the main header.
    await expect(main.locator("header.context").getByRole("button", { name: "Toggle sessions" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(main.locator("header.context").getByRole("button", { name: "Toggle files" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    for (const text of ["first", "second"]) {
      await page.getByRole("button", { name: "New session" }).click();
      await prompt(page).fill(text);
      await send(page).click();
      await expect(page.locator(".reply")).toHaveText(`You said: ${text}`);
    }
    await sessionButton(page, "first").click();
    await alone();
    await expect(files.getByRole("button", { name: "Toggle sessions" })).toHaveAttribute("aria-pressed", "true");
    await expect(files.getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "true");

    // The toggles keep their place on screen as they move between headers.
    const shownBox = await toggleFiles.boundingBox();
    await toggleFiles.click();
    await expect(files).toHaveCount(0);
    expect(await toggleFiles.boundingBox()).toEqual(shownBox);
    await toggleFiles.click();
    await expect(files).toBeVisible();

    // Hiding the file tree moves the toggles to the session's header and keeps
    // the open file.
    await openFile();
    await toggleFiles.click();
    await expect(files).toHaveCount(0);
    await expect(page.locator("header.context").getByRole("button", { name: "Toggle files" })).toHaveCount(1);
    await expect(viewer.getByRole("button", { name: "Toggle files" })).toHaveCount(0);
    await expect(toggleFiles).toHaveAttribute("aria-pressed", "false");
    expect(await color(toggleFiles)).not.toBe(addColor);
    await expect(viewer).toBeVisible();
    await expect(page.getByRole("separator", { name: "Resize files" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(nav).toBeHidden();
    await expect(toggleSessions).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByRole("separator", { name: "Resize sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await toggleFiles.click();
    await expect(nav).toBeVisible();
    await expect(files).toBeVisible();

    // Drags resize the panels within their bounds, and the split.
    expect(await width(nav)).toBe(288);
    await drag("Resize sessions", 100, "right");
    expect(await width(nav)).toBe(288);
    await drag("Resize sessions", 100);
    expect(await width(nav)).toBe(388);
    expect(await cursor()).not.toBe("col-resize");
    // The drag ends with the button: a move over the sash leaves it in place.
    const sash = await page.getByRole("separator", { name: "Resize sessions" }).boundingBox();
    await page.mouse.move((sash?.x ?? 0) + 1, (sash?.y ?? 0) + 100);
    expect(await width(nav)).toBe(388);
    await drag("Resize sessions", 1000);
    expect(await width(nav)).toBe(640);
    await drag("Resize sessions", -252);
    expect(await width(nav)).toBe(388);
    await drag("Resize files", -100);
    expect(await width(files)).toBe(388);
    const [mainBefore, viewerBefore] = [await width(main), await width(viewer)];
    await drag("Resize file viewer", -100);
    expect(Math.abs((await width(main)) - (mainBefore - 100))).toBeLessThanOrEqual(1);
    expect(Math.abs((await width(viewer)) - (viewerBefore + 100))).toBeLessThanOrEqual(1);
    const firstSplit = await width(main);
    // The split leaves each side at least 240 px.
    await drag("Resize file viewer", -1000);
    expect(Math.abs((await width(main)) - 240)).toBeLessThanOrEqual(1);
    await drag("Resize file viewer", firstSplit - 240);
    expect(Math.abs((await width(main)) - firstSplit)).toBeLessThanOrEqual(1);
    // Closing the file leaves the transcript alone at full width; reopening it
    // restores the split.
    await viewer.getByRole("button", { name: "Close a.txt" }).click();
    await alone();
    await files.getByRole("button", { name: "a.txt", exact: true }).click();
    expect(Math.abs((await width(main)) - firstSplit)).toBeLessThanOrEqual(1);

    // Panels are page-wide; each session keeps its own split.
    await sessionButton(page, "second").click();
    await openFile();
    await even();
    expect(await width(nav)).toBe(388);
    expect(await width(files)).toBe(388);
    await sessionButton(page, "first").click();
    const firstAgain = await width(main);
    expect(Math.abs(firstAgain - firstSplit)).toBeLessThanOrEqual(1);
    expect(await width(nav)).toBe(388);
    expect(await width(files)).toBe(388);
    await toggleFiles.click();
    await sessionButton(page, "second").click();
    await expect(files).toHaveCount(0);
    await expect(toggleFiles).toHaveAttribute("aria-pressed", "false");
    await toggleFiles.click();

    // A draft's split carries to the session it creates.
    await page.getByRole("button", { name: "New session" }).click();
    await openFile();
    await even();
    await drag("Resize file viewer", -100);
    const draftSplit = await width(main);
    await prompt(page).fill("third");
    await send(page).click();
    await expect(page.locator(".reply")).toHaveText("You said: third");
    await expect(viewer).toBeVisible();
    expect(await width(main)).toBe(draftSplit);

    await page.reload();
    await expect(page.locator(".reply")).toHaveText("You said: third");
    expect(await width(nav)).toBe(288);
    expect(await width(files)).toBe(288);
    await openFile();
    await even();
  } finally {
    await services.stop();
  }
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test("the page shows one pane at a time", async ({ page }) => {
    const services = await startServices(fakeConfig);
    try {
      mkdirSync(join(services.workspace, "src"));
      writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
      const nav = page.locator("nav");
      const main = page.locator("main");
      const scrim = page.locator(".scrim");
      const files = page.getByRole("complementary", { name: "Files" });
      const viewer = page.getByRole("region", { name: "File" });
      const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
      const toggleFiles = page.getByRole("button", { name: "Toggle files" });
      const box = async (locator: Locator) => {
        const found = await locator.boundingBox();
        if (!found) throw new Error(`${locator} has no box`);
        return found;
      };
      const style = (locator: Locator, property: string) =>
        locator.evaluate((element, property) => getComputedStyle(element).getPropertyValue(property), property);
      // A sash is zero pixels wide, so it shows by its display. Role queries
      // skip an element with no display, so this one finds it by its label.
      const sashDisplay = (label: string) => style(page.locator(`.sash[aria-label="${label}"]`), "display");
      // slides tells whether a drawer is moving across the screen.
      const slides = (locator: Locator) =>
        locator.evaluate((element) =>
          element.getAnimations().some((a) => a instanceof CSSTransition && a.transitionProperty === "translate"),
        );
      expect(await page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true);

      // The bare URL opens with the session list over the empty transcript.
      await page.goto(services.url);
      await expect(nav).toBeVisible();
      await expect(scrim).toBeVisible();
      expect((await box(main)).width).toBe(390);
      expect((await box(toggleFiles)).x).toBeGreaterThan(300);
      await open(page, services);

      // Touch shows the row buttons and keeps text fields at 16px.
      const newSession = page.getByRole("button", { name: "New session" });
      expect(await style(newSession, "opacity")).toBe("1");
      await newSession.tap();
      await expect(nav).toBeHidden();
      await expect(scrim).toHaveCount(0);
      expect(await style(prompt(page), "font-size")).toBe("16px");
      expect((await box(page.locator(".composer .picker"))).height).toBe(32);
      await prompt(page).fill("first");
      await send(page).tap();
      await expect(page.locator(".reply")).toHaveText("You said: first");

      // The toggles sit at either end of the session's header, and a long
      // directory keeps to one line.
      const title = await box(main.locator("h1"));
      expect((await box(toggleSessions)).x).toBeLessThan(title.x);
      expect((await box(toggleFiles)).x).toBeGreaterThanOrEqual(title.x + title.width);
      const path = main.locator(".path");
      expect(await path.locator(".directory").evaluate((span) => span.scrollWidth > span.clientWidth)).toBe(true);
      expect((await box(path.locator(".directory"))).height).toBeLessThanOrEqual((await box(path)).height);

      // Safe-area insets keep the page clear of the notch, the rounded
      // corners, and Safari's toolbar, in the transcript, the drawers, and
      // the file viewer.
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("Emulation.setSafeAreaInsetsOverride", { insets: { top: 47, bottom: 34, left: 12, right: 12 } });
      expect((await box(main.locator("h1"))).y).toBeGreaterThanOrEqual(47);
      const composer = await box(page.locator(".composer"));
      expect(composer.x).toBe(12 + 8);
      expect(await style(page.locator(".transcript"), "padding-left")).toBe("29px");
      expect(composer.y + composer.height).toBeLessThanOrEqual(844 - 34);

      // The file tree is a drawer that a tap outside or opening a file closes,
      // and an open file replaces the transcript until its tab closes.
      await toggleFiles.tap();
      expect(await slides(files)).toBe(true);
      await expect(files).toBeVisible();
      await expect(files.getByRole("button", { name: /^Toggle/ })).toHaveCount(0);
      await expect(toggleFiles).toHaveAttribute("aria-pressed", "true");
      expect((await box(main)).width).toBe(390 - 24);
      // The drawer covers its toggle, which closes it from the keyboard.
      await toggleFiles.press("Enter");
      await expect(files).toHaveCount(0);
      await toggleFiles.tap();
      await scrim.tap({ position: { x: 20, y: 400 } });
      await expect(files).toHaveCount(0);
      await toggleFiles.tap();
      expect((await box(files.locator("header"))).y).toBeGreaterThanOrEqual(47);
      expect(await style(files.locator(".tree"), "padding-bottom")).toBe("42px");
      expect(await style(files, "padding-right")).toBe("12px");
      expect((await box(files.getByRole("button", { name: "src" }))).height).toBe(40);
      await files.getByRole("button", { name: "src" }).tap();
      await files.getByRole("button", { name: "a.txt", exact: true }).tap();
      await expect(files).toHaveCount(0);
      await expect(viewer).toBeVisible();
      expect((await box(viewer)).width).toBe(390 - 24);
      await expect(main).toBeHidden();
      expect(await sashDisplay("Resize file viewer")).toBe("none");
      expect(await style(viewer.getByRole("tabpanel"), "padding-bottom")).toBe("42px");
      await viewer.getByRole("button", { name: "Close a.txt" }).tap();
      await expect(main).toBeVisible();

      // The session list is a drawer that picking a session closes, and a
      // session's URL opens with it closed.
      await toggleSessions.tap();
      expect(await slides(nav)).toBe(true);
      await expect(nav).toBeVisible();
      await expect(toggleSessions).toHaveAttribute("aria-pressed", "true");
      expect((await box(sessionRow(page, "first"))).height).toBe(40);
      expect(await sashDisplay("Resize sessions")).toBe("none");
      expect((await box(page.getByRole("button", { name: "Add workspace" }))).y).toBeGreaterThanOrEqual(47);
      expect(await style(nav, "padding-bottom")).toBe("42px");
      expect(await style(nav, "padding-left")).toBe("20px");
      await cdp.send("Emulation.setSafeAreaInsetsOverride", { insets: {} });
      await sessionButton(page, "first").tap();
      expect(await slides(nav)).toBe(true);
      await expect(nav).toBeHidden();
      await page.reload();
      await expect(page.locator(".reply")).toHaveText("You said: first");
      await expect(nav).toBeHidden();

      // A wide window shows the panels side by side; returning to narrow
      // closes the drawer that was open.
      await page.setViewportSize({ width: 1000, height: 844 });
      await expect(nav).toBeVisible();
      await expect(files).toBeVisible();
      expect((await box(main)).width).toBe(1000 - (await box(nav)).width - (await box(files)).width);
      expect(await sashDisplay("Resize sessions")).not.toBe("none");
      await expect(scrim).toHaveCount(0);
      // A width dragged wide leaves the drawer at its own width.
      const sash = await box(page.locator('.sash[aria-label="Resize sessions"]'));
      await page.mouse.move(sash.x, sash.y + 100);
      await page.mouse.down();
      await page.mouse.move(sash.x + 100, sash.y + 100, { steps: 5 });
      await page.mouse.up();
      expect((await box(nav)).width).toBe(388);
      await page.setViewportSize({ width: 390, height: 844 });
      await expect(nav).toBeHidden();
      await toggleSessions.tap();
      await expect(nav).toBeVisible();
      expect((await box(nav)).width).toBe(288);
      await page.setViewportSize({ width: 1000, height: 844 });
      await expect(files).toBeVisible();
      expect(await sashDisplay("Resize sessions")).not.toBe("none");
      await page.setViewportSize({ width: 390, height: 844 });
      await expect(nav).toBeHidden();
      await expect(scrim).toHaveCount(0);
    } finally {
      await services.stop();
    }
  });
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
    writeFileSync(join(services.workspace, "src", "view.tsx"), "const v = <div />;\n");
    writeFileSync(join(services.workspace, "src", "notes.md"), "*a*\n");
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
    expect(await height(viewer.locator("header"))).toBe(await height(page.locator("header.context .title")));
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
    const keyword = panel.locator("span").first();
    const comment = panel.locator("span", { hasText: "//" });
    await expect(keyword).toHaveText("package");
    await expect(comment).toHaveText("// note");
    await expect(keyword).toHaveCSS("color", "rgb(207, 34, 46)");
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

    // The pane keeps the file's carriage returns.
    await files.getByRole("button", { name: "template.ts", exact: true }).click();
    await expect(tab).toHaveText("template.ts");
    await expect(panel.locator("span").first()).toBeVisible();
    expect(await panel.evaluate((pre) => pre.textContent)).toBe(template);

    // A TSX element's tag name is colored as a tag, and Markdown emphasis is
    // italic.
    await files.getByRole("button", { name: "view.tsx", exact: true }).click();
    await expect(tab).toHaveText("view.tsx");
    await expect(panel.locator("span", { hasText: /^div$/ })).toHaveCSS("color", "rgb(17, 99, 41)");
    await files.getByRole("button", { name: "notes.md", exact: true }).click();
    await expect(tab).toHaveText("notes.md");
    await expect(panel.locator("span", { hasText: "*a*" })).toHaveCSS("font-style", "italic");

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
