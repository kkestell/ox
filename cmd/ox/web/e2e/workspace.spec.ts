import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { Locator, Page, Request, Route } from "@playwright/test";
import { fakeConfig } from "./services";
import {
  addWorkspace,
  closeKeyboard,
  edges,
  expect,
  hold,
  isRequest,
  menuItem,
  model,
  newSession,
  newSessionButton,
  openKeyboard,
  openMenu,
  panKeyboard,
  permissionMode,
  prompt,
  replies,
  rpcCalls,
  send,
  sessionButton,
  sessionItem,
  sessions,
  setSafeArea,
  test,
  workspaceSection,
} from "./ui";

// clocked runs the page under Playwright's clock, paused once the page has
// loaded, so the page's clock ticks only on runFor(1000).
const start = new Date("2026-01-01T00:00:00Z");
const clocked = test.extend({
  page: async ({ page }, use) => {
    await page.clock.install({ time: start });
    await use(page);
  },
  ox: async ({ ox }, use) => {
    await ox.clock.pauseAt(new Date(start.getTime() + 3_600_000));
    await use(ox);
  },
});

const files = (page: Page) => page.getByRole("complementary", { name: "Files" });
// entry is the file tree's button for the file or folder called name.
const entry = (page: Page, name: string) => files(page).getByRole("button", { name, exact: true });
// folder is the file tree's item for the folder called name, holding its listing.
const folder = (page: Page, name: string) =>
  files(page)
    .getByRole("listitem")
    .filter({ has: page.getByRole("button", { name, exact: true }) });
const viewer = (page: Page) => page.getByRole("region", { name: "File" });
const tab = (page: Page) => viewer(page).getByRole("tab");
const pane = (page: Page) => viewer(page).getByRole("tabpanel");

// openFile opens the file at path from the tree, expanding the folders on the
// way.
async function openFile(page: Page, path: string) {
  const names = path.split("/");
  for (const name of names.slice(0, -1)) await entry(page, name).click();
  await entry(page, names.at(-1) ?? path).click();
  await expect(tab(page)).toHaveText(names.at(-1) ?? path);
}

// requests collects each request for method the page posts from now on.
function requests(page: Page, method: string) {
  const posted: Request[] = [];
  page.on("request", (request) => {
    if (isRequest(method)(request)) posted.push(request);
  });
  return posted;
}

// tick advances the page's clock one tick and returns the first request for
// method that follows.
async function tick(page: Page, method: string) {
  const next = page.waitForRequest(isRequest(method));
  await page.clock.runFor(1000);
  return next;
}

// answered runs the page's clock until it has sent a second request for
// method about path. The page repeats a request only once the last one is
// answered, so the first request's answer has then been applied. An answer
// itself arrives over the event stream, not as the request's response.
async function answered(page: Page, method: string, path: string) {
  const posted = requests(page, method);
  await expect
    .poll(async () => {
      await page.clock.runFor(1000);
      return posted.filter((request) => request.postDataJSON().params.path === path).length;
    })
    .toBeGreaterThanOrEqual(2);
}

const setVisibility = (page: Page, state: "hidden" | "visible") =>
  page.evaluate((state) => {
    Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));
  }, state);

const width = async (locator: Locator) => (await locator.boundingBox())?.width ?? 0;

// near polls locator's width until it is within a pixel of expected.
const near = (locator: Locator, expected: number) =>
  expect.poll(async () => Math.abs((await width(locator)) - expected)).toBeLessThanOrEqual(1);

// drag moves the separator named label dx pixels along the x axis with the
// given mouse button.
async function drag(page: Page, label: string, dx: number, button: "left" | "right" = "left") {
  const box = await page.getByRole("separator", { name: label }).boundingBox();
  if (!box) throw new Error(`no ${label} separator`);
  const y = box.y + box.height / 2;
  await page.mouse.move(box.x, y);
  await page.mouse.down({ button });
  await page.mouse.move(box.x + dx, y, { steps: 5 });
  await page.mouse.up({ button });
}

test("a malformed session link still lists workspaces", async ({ page, services }) => {
  await page.goto(`${services.url}/#%`);
  await addWorkspace(page, services.workspace);
  await expect(
    workspaceSection(page, services.workspace).getByRole("heading", { name: basename(services.workspace) }),
  ).toBeVisible();
});

test("a draft holds Send until the session list loads", async ({ page, services }) => {
  const list = await hold(page, "workspace/list");
  await page.goto(services.url);
  await list.arrived;
  await addWorkspace(page, services.workspace);
  await newSessionButton(page).click();
  await prompt(page).fill("draft before catalog");
  await expect(send(page)).toBeDisabled();
  list.release();
  await expect(send(page)).toBeEnabled();
});

test("a draft keeps its permission mode through a relay restart", async ({ ox: page, services }) => {
  const creates = rpcCalls(page, "session/create");
  await newSessionButton(page).click();
  await prompt(page).fill("draft through a restart");
  await expect(permissionMode(page)).toHaveValue("manual");
  await permissionMode(page).selectOption("plan");
  const reconnect = await hold(page, "workspace/list");
  await services.restartRelay();
  await reconnect.arrived;
  await expect(send(page)).toBeDisabled();
  await expect(permissionMode(page)).toBeDisabled();
  await expect(model(page)).toBeDisabled();
  await prompt(page).press("Enter");
  reconnect.release();
  await expect(send(page)).toBeEnabled();
  await expect(permissionMode(page)).toHaveValue("plan");
  await expect(prompt(page)).toHaveValue("draft through a restart");
  expect(creates).toEqual([]);
});

test.describe(() => {
  const fake = fakeConfig.harnesses.fake;
  test.use({
    config: {
      ...fakeConfig,
      harnesses: { fake: { ...fake, models: fake.models.map((model) => ({ ...model, efforts: [] })) } },
      defaults: { ...fakeConfig.defaults, effort: "" },
    },
  });

  test("a model without efforts creates a session without effort", async ({ ox: page }) => {
    await newSessionButton(page).click();
    await prompt(page).fill("no effort");
    const create = page.waitForRequest(isRequest("session/create"));
    await send(page).click();
    expect((await create).postDataJSON().params).not.toHaveProperty("effort");
    await expect(replies(page)).toHaveText(["You said: no effort"]);
  });
});

test("a cancelled add cannot settle the next add dialog", async ({ page, services }) => {
  await page.goto(services.url);
  const dialog = page.getByRole("dialog", { name: "Add workspace" });
  const directory = dialog.getByRole("textbox", { name: "Directory" });
  const first = await hold(page, "workspace/create");
  await page.getByRole("button", { name: "Add workspace" }).click();
  await directory.fill("/");
  await dialog.getByRole("button", { name: "Add", exact: true }).click();
  await first.arrived;
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Add workspace" }).click();
  await directory.fill(services.workspace);
  first.release();
  await expect(workspaceSection(page, "/")).toBeVisible();
  await expect(directory).toHaveValue(services.workspace);
  await dialog.getByRole("button", { name: "Add", exact: true }).click();
  await expect(workspaceSection(page, services.workspace)).toBeVisible();
});

test("a cancelled removal cannot settle the next removal dialog", async ({ ox: page, services }) => {
  await addWorkspace(page, "/");
  const dialog = page.getByRole("alertdialog", { name: "Remove workspace?" });
  const remove = async (path: string) =>
    menuItem(await openMenu(workspaceSection(page, path)), "Remove workspace").click();
  const first = await hold(page, "workspace/remove");
  await remove("/");
  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await first.arrived;
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await remove(services.workspace);
  first.release();
  await expect(workspaceSection(page, "/")).toHaveCount(0);
  await expect(dialog).toHaveAccessibleDescription(
    `This removes "${services.workspace}" from Ox. The directory stays on disk.`,
  );
  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(workspaceSection(page, services.workspace)).toHaveCount(0);
});

test("Remove workspace gives its reason while sessions remain, archived ones included", async ({
  ox: page,
  services,
}) => {
  const workspace = workspaceSection(page, services.workspace);
  const removeItem = async () => menuItem(await openMenu(workspace), "Remove workspace");
  await newSession(page, "hello");
  const remove = await removeItem();
  await expect(remove).toBeDisabled();
  await expect(remove).toHaveAccessibleDescription("Delete its sessions first");
  await page.keyboard.press("Escape");

  // An archived session, hidden or shown, still keeps the workspace.
  await menuItem(await openMenu(sessionItem(page, "hello")), "Archive").click();
  await expect(sessionButton(page, "hello")).toHaveCount(0);
  const archived = await removeItem();
  await expect(archived).toBeDisabled();
  await expect(archived).toHaveAccessibleDescription("Delete its sessions first, including archived ones");
  await page.keyboard.press("Escape");
  await menuItem(await openMenu(workspace), "Show archived").click();
  await menuItem(await openMenu(sessionItem(page, "hello")), "Delete").click();
  await page.getByRole("alertdialog", { name: "Delete session?" }).getByRole("button", { name: "Delete" }).click();
  await expect(sessionButton(page, "hello")).toHaveCount(0);
  await expect(await removeItem()).toBeEnabled();
});

test("removing a workspace asks first and shows a failure in the dialog", async ({ ox: page, services }) => {
  const workspace = workspaceSection(page, services.workspace);
  await menuItem(await openMenu(workspace), "Remove workspace").click();
  const dialog = page.getByRole("alertdialog", { name: "Remove workspace?" });
  await expect(dialog).toHaveAccessibleDescription(
    `This removes "${services.workspace}" from Ox. The directory stays on disk.`,
  );
  const removal = await hold(page, "workspace/remove");
  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await removal.arrived;
  removal.fail("relay unavailable");
  await expect(dialog.getByRole("alert")).toHaveText("relay unavailable");
  await expect(workspace).toBeVisible();

  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(workspace).toHaveCount(0);
});

test("removing a workspace discards its draft", async ({ ox: page, services }) => {
  const workspace = workspaceSection(page, services.workspace);
  await newSessionButton(page).click();
  await prompt(page).fill("unsent");
  await menuItem(await openMenu(workspace), "Remove workspace").click();
  const dialog = page.getByRole("alertdialog", { name: "Remove workspace?" });
  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(workspace).toHaveCount(0);
  await expect(prompt(page)).toHaveCount(0);
});

test("the file tree lists folders first and hides `.git`", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  mkdirSync(join(services.workspace, "lib"));
  mkdirSync(join(services.workspace, ".git"));
  writeFileSync(join(services.workspace, ".git", "HEAD"), "");
  writeFileSync(join(services.workspace, ".gitignore"), "");
  writeFileSync(join(services.workspace, "a.txt"), "");
  writeFileSync(join(services.workspace, "Makefile"), "");
  await newSessionButton(page).click();
  await expect(files(page)).toMatchAriaSnapshot(`
    - list:
      - /children: deep-equal
      - listitem:
        - button "lib"
      - listitem:
        - button "src"
      - listitem:
        - button ".gitignore"
      - listitem:
        - button "a.txt"
      - listitem:
        - button "Makefile"
  `);
  await expect(entry(page, ".git")).toHaveCount(0);
});

test("each session keeps its own expanded folders", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newSession(page, "first");
  await newSession(page, "second");
  await sessionButton(page, "first").click();
  await entry(page, "src").click();
  await expect(files(page)).toMatchAriaSnapshot(`
    - list:
      - /children: deep-equal
      - listitem:
        - button "src" [expanded]
        - list:
          - listitem:
            - button "a.txt"
  `);

  await sessionButton(page, "second").click();
  await expect(entry(page, "src")).toHaveAttribute("aria-expanded", "false");
  await expect(entry(page, "a.txt")).toHaveCount(0);
  await sessionButton(page, "first").click();
  await expect(entry(page, "src")).toHaveAttribute("aria-expanded", "true");
  await expect(entry(page, "a.txt")).toBeVisible();
});

clocked("the tree shows changes on disk", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newSessionButton(page).click();
  await entry(page, "src").click();
  await expect(entry(page, "a.txt")).toBeVisible();
  writeFileSync(join(services.workspace, "src", "b.txt"), "");
  writeFileSync(join(services.workspace, "c.txt"), "");
  await page.clock.runFor(1000);
  await expect(files(page)).toMatchAriaSnapshot(`
    - list:
      - /children: deep-equal
      - listitem:
        - button "src" [expanded]
        - list:
          - listitem:
            - button "a.txt"
          - listitem:
            - button "b.txt"
      - listitem:
        - button "c.txt"
  `);
});

clocked("the tree lists nothing while the page is hidden", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "a.txt"), "");
  await newSessionButton(page).click();
  await expect(entry(page, "a.txt")).toBeVisible();
  await setVisibility(page, "hidden");
  const listed = requests(page, "workspace/list_folder");
  await page.clock.runFor(3000);
  await setVisibility(page, "visible");
  const next = await tick(page, "workspace/list_folder");
  expect(listed[0]).toBe(next);
});

clocked("a relay error shows in place of a folder's listing until the next listing", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newSessionButton(page).click();
  const answers: [(route: Route) => Promise<void>, string | RegExp][] = [
    [(route) => route.fulfill({ status: 502, body: "relay unavailable" }), "relay unavailable"],
    [(route) => route.abort(), "Could not reach ox"],
    [(route) => route.fulfill({ status: 502, body: "" }), /502/],
  ];
  await entry(page, "src").click();
  await expect(entry(page, "a.txt")).toBeVisible();
  for (const [answer, text] of answers) {
    const failing = (route: Route) => {
      const body = route.request().postDataJSON();
      if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
      return answer(route);
    };
    await page.route("**/rpc*", failing);
    await page.clock.runFor(1000);
    await expect(folder(page, "src").getByText(text)).toBeVisible();
    await expect(entry(page, "a.txt")).toHaveCount(0);
    await page.unroute("**/rpc*", failing);
    await page.clock.runFor(1000);
    await expect(entry(page, "a.txt")).toBeVisible();
  }
});

clocked("a relay that lost its oxd connection keeps the last listing", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newSessionButton(page).click();
  await entry(page, "src").click();
  await expect(entry(page, "a.txt")).toBeVisible();
  const listing = (route: Route) => {
    const body = route.request().postDataJSON();
    if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
    return route.fulfill({ status: 503, body: "unknown connection" });
  };
  await page.route("**/rpc*", listing);
  await answered(page, "workspace/list_folder", "src");
  await expect(entry(page, "a.txt")).toBeVisible();
});

clocked(
  "a lost connection keeps the tree and lists nothing until the page has reloaded",
  async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    mkdirSync(join(services.workspace, "lib"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "");
    await newSessionButton(page).click();
    await prompt(page).fill("draft");
    await entry(page, "src").click();
    await expect(entry(page, "a.txt")).toBeVisible();
    // A listing in flight when the connection is lost.
    const inFlight = await hold(page, "workspace/list_folder", { path: "src" });
    await page.clock.runFor(1000);
    await inFlight.arrived;
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toBeVisible();
    const listed = requests(page, "workspace/list_folder");

    await entry(page, "lib").click();
    await page.clock.runFor(3000);
    await expect(files(page)).toMatchAriaSnapshot(`
    - list:
      - /children: deep-equal
      - listitem:
        - button "lib" [expanded]
      - listitem:
        - button "src" [expanded]
        - list:
          - listitem:
            - button "a.txt"
  `);
    inFlight.release();

    const reload = await hold(page, "workspace/list");
    await page.unroute("**/events");
    await reload.arrived;
    await page.clock.runFor(3000);
    reload.release();
    await expect(send(page)).toBeEnabled();
    const next = await tick(page, "workspace/list_folder");
    expect(listed[0]).toBe(next);
  },
);

test("an unreadable folder says why, and an empty folder says Empty", async ({ ox: page, services }) => {
  const locked = join(services.workspace, "locked");
  mkdirSync(locked, { mode: 0o000 });
  mkdirSync(join(services.workspace, "empty"));
  try {
    await newSessionButton(page).click();
    await entry(page, "locked").click();
    await expect(folder(page, "locked").getByText("permission denied")).toBeVisible();
    await entry(page, "empty").click();
    await expect(folder(page, "empty").getByText("Empty", { exact: true })).toBeVisible();
  } finally {
    chmodSync(locked, 0o755);
  }
});

test("a workspace header is as tall as a session row, with its buttons centered", async ({ ox: page, services }) => {
  await newSession(page, "first");
  const section = workspaceSection(page, services.workspace);
  const header = await section.locator("header").boundingBox();
  const row = await sessionItem(page, "first").boundingBox();
  if (!header || !row) throw new Error("the workspace's header or session is not shown");
  expect(header.height).toBe(row.height);
  await section.getByRole("heading").hover();
  for (const name of ["Workspace actions", "New session"]) {
    const button = await section.getByRole("button", { name }).boundingBox();
    if (!button) throw new Error(`${name} is not shown`);
    expect(button.y + button.height / 2).toBe(header.y + header.height / 2);
  }
});

test("a long workspace name runs to its header's edge beneath its buttons' fade", async ({ ox: page }) => {
  const long = mkdtempSync(join(tmpdir(), `ox-e2e-${"long".repeat(24)}-`));
  try {
    await addWorkspace(page, long);
    const section = workspaceSection(page, long);
    const name = section.getByRole("heading", { name: basename(long) });
    await page.mouse.move(0, 0);
    const nameBox = await name.boundingBox();
    const sectionBox = await section.boundingBox();
    if (!nameBox || !sectionBox) throw new Error("the long workspace is not shown");
    expect(Math.abs(nameBox.x + nameBox.width - (sectionBox.x + sectionBox.width))).toBeLessThanOrEqual(1);
    await expect(name).toHaveCSS("text-overflow", "clip");
    // Hovered, the ⋯ icon stands on the header's own color over the name
    // beneath it.
    await name.hover();
    const actions = await section.getByRole("button", { name: "Workspace actions" }).boundingBox();
    if (!actions) throw new Error("the long workspace's buttons are not shown");
    const strip = (x: number) =>
      page.screenshot({ clip: { x: Math.ceil(x), y: nameBox.y, width: 3, height: nameBox.height } });
    // The header's padding, left of the name, is empty.
    expect(await strip(actions.x)).toEqual(await strip(nameBox.x - 5));
  } finally {
    rmSync(long, { recursive: true, force: true });
  }
});

test("another workspace's draft shows that workspace's tree", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "mine.txt"), "");
  const other = mkdtempSync(join(tmpdir(), "ox-e2e-other-"));
  try {
    writeFileSync(join(other, "other.txt"), "");
    await workspaceSection(page, services.workspace).getByRole("button", { name: "New session" }).click();
    await expect(files(page).getByRole("heading", { name: basename(services.workspace) })).toBeVisible();
    await expect(entry(page, "mine.txt")).toBeVisible();
    await addWorkspace(page, other);
    await workspaceSection(page, other).getByRole("button", { name: "New session" }).click();
    await expect(files(page).getByRole("heading", { name: basename(other) })).toBeVisible();
    await expect(files(page)).toMatchAriaSnapshot(`
      - list:
        - /children: deep-equal
        - listitem:
          - button "other.txt"
    `);
  } finally {
    rmSync(other, { recursive: true, force: true });
  }
});

test.describe("on a wide screen", () => {
  test.use({ viewport: { width: 1600, height: 900 } });

  test.beforeEach(({ services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  });

  test("the toggles show and hide the panels from the header they sit in", async ({ ox: page }) => {
    const main = page.getByRole("main");
    const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
    const toggleFiles = page.getByRole("button", { name: "Toggle files" });

    // With nothing selected, the toggles sit in the main header.
    await expect(main.getByRole("button", { name: "Toggle sessions" })).toHaveAttribute("aria-pressed", "true");
    await expect(main.getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "true");

    // With the file tree shown, they sit in its header.
    await newSession(page, "first");
    await expect(files(page).getByRole("button", { name: "Toggle sessions" })).toHaveAttribute("aria-pressed", "true");
    await expect(files(page).getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "true");
    await expect(main.getByRole("button", { name: /^Toggle/ })).toHaveCount(0);

    // Hiding the tree moves them to the session's header, in the same place
    // on screen.
    const shown = await toggleFiles.boundingBox();
    await toggleFiles.click();
    await expect(files(page)).toHaveCount(0);
    await expect(main.getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "false");
    await expect.poll(() => toggleFiles.boundingBox()).toEqual(shown);
    await toggleFiles.click();
    await expect(files(page).getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "true");

    // With a file open, hiding the tree keeps the file, and the toggles move
    // to the session's header, not the file's.
    await openFile(page, "src/a.txt");
    await toggleFiles.click();
    await expect(files(page)).toHaveCount(0);
    await expect(viewer(page)).toBeVisible();
    await expect(main.getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "false");
    await expect(viewer(page).getByRole("button", { name: /^Toggle/ })).toHaveCount(0);
    await expect(page.getByRole("separator", { name: "Resize files" })).toHaveCount(0);

    await toggleSessions.click();
    await expect(sessions(page)).toBeHidden();
    await expect(toggleSessions).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByRole("separator", { name: "Resize sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(sessions(page)).toBeVisible();
    await toggleFiles.click();
    await expect(files(page)).toBeVisible();
    await expect(files(page).getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "true");
  });

  test("dragging a sash resizes its panel within bounds, and only while the button is down", async ({ ox: page }) => {
    const nav = sessions(page);
    await newSessionButton(page).click();
    const before = await width(nav);
    await drag(page, "Resize sessions", 100, "right");
    await near(nav, before);
    await drag(page, "Resize sessions", 100);
    await near(nav, before + 100);
    // The drag ends with the button: a move over the sash leaves it in place.
    const sash = await page.getByRole("separator", { name: "Resize sessions" }).boundingBox();
    await page.mouse.move((sash?.x ?? 0) + 50, (sash?.y ?? 0) + 100);
    await near(nav, before + 100);
    await drag(page, "Resize sessions", 1000);
    await near(nav, 640);
    await drag(page, "Resize sessions", -1000);
    await near(nav, 160);

    const tree = files(page);
    const treeStart = await width(tree);
    await drag(page, "Resize files", -100);
    await near(tree, treeStart + 100);
  });

  test("the file split leaves each side its minimum and comes back when the file reopens", async ({ ox: page }) => {
    const main = page.getByRole("main");
    await newSessionButton(page).click();
    await openFile(page, "src/a.txt");
    const [mainBefore, viewerBefore] = [await width(main), await width(viewer(page))];
    await drag(page, "Resize file viewer", -100);
    await near(main, mainBefore - 100);
    await near(viewer(page), viewerBefore + 100);
    await drag(page, "Resize file viewer", -1000);
    await near(main, 240);
    await drag(page, "Resize file viewer", 2000);
    await near(viewer(page), 240);
    await drag(page, "Resize file viewer", -200);
    const split = await width(main);
    await viewer(page).getByRole("button", { name: "Close a.txt" }).click();
    await expect(viewer(page)).toHaveCount(0);
    await entry(page, "a.txt").click();
    await expect(viewer(page)).toBeVisible();
    await near(main, split);

    // A window too tight for both minimums splits the center evenly, ignores
    // the split's sash, and keeps the saved split for when it widens. A
    // fractional sessions width leaves the tight center a fractional width.
    await drag(page, "Resize sessions", 0.3);
    await page.setViewportSize({ width: 820, height: 900 });
    const even = await width(viewer(page));
    await near(main, even);
    await drag(page, "Resize file viewer", -35);
    await near(main, even);
    await near(viewer(page), even);
    await drag(page, "Resize file viewer", 35);
    await near(main, even);
    await near(viewer(page), even);
    await page.setViewportSize({ width: 1600, height: 900 });
    await near(main, split);
  });

  test("panel widths are page-wide and each session keeps its own split", async ({ ox: page }) => {
    const main = page.getByRole("main");
    const nav = sessions(page);
    await newSession(page, "first");
    await newSession(page, "second");
    await sessionButton(page, "first").click();
    await openFile(page, "src/a.txt");
    const evenSplit = await width(main);
    await drag(page, "Resize sessions", 100);
    await drag(page, "Resize files", -100);
    await drag(page, "Resize file viewer", -100);
    const [navWidth, treeWidth, firstSplit] = [await width(nav), await width(files(page)), await width(main)];

    await sessionButton(page, "second").click();
    await openFile(page, "src/a.txt");
    await near(nav, navWidth);
    await near(files(page), treeWidth);
    await expect.poll(async () => Math.abs((await width(main)) - firstSplit)).toBeGreaterThan(1);
    expect(firstSplit).not.toBe(evenSplit);
    await sessionButton(page, "first").click();
    await near(main, firstSplit);
    await near(nav, navWidth);
    await near(files(page), treeWidth);

    // Whether a panel shows is page-wide too.
    await page.getByRole("button", { name: "Toggle files" }).click();
    await sessionButton(page, "second").click();
    await expect(files(page)).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Toggle files" })).toHaveAttribute("aria-pressed", "false");
  });

  test("a draft's split carries to the session it creates", async ({ ox: page }) => {
    const main = page.getByRole("main");
    await newSessionButton(page).click();
    await openFile(page, "src/a.txt");
    await drag(page, "Resize file viewer", -100);
    const split = await width(main);
    await prompt(page).fill("third");
    await send(page).click();
    await expect(replies(page)).toHaveText(["You said: third"]);
    await expect(viewer(page)).toBeVisible();
    await near(main, split);
  });

  test("a reload restores the default widths", async ({ ox: page }) => {
    const main = page.getByRole("main");
    const nav = sessions(page);
    await newSession(page, "first");
    await openFile(page, "src/a.txt");
    const first = [await width(nav), await width(files(page)), await width(main), await width(viewer(page))];
    await drag(page, "Resize sessions", 100);
    await drag(page, "Resize files", -100);
    await drag(page, "Resize file viewer", -100);
    await page.reload();
    await expect(replies(page)).toHaveText(["You said: first"]);
    await openFile(page, "src/a.txt");
    await near(nav, first[0]);
    await near(files(page), first[1]);
    await near(main, first[2]);
    await near(viewer(page), first[3]);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test.beforeEach(({ services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  });

  const toggleSessions = (page: Page) => page.getByRole("button", { name: "Toggle sessions" });
  const toggleFiles = (page: Page) => page.getByRole("button", { name: "Toggle files" });
  // tapBeside taps the page just past the drawer's edge on the side given.
  async function tapBeside(page: Page, drawer: Locator, side: "left" | "right") {
    // A trial tap waits for the drawer to finish sliding in.
    await drawer.tap({ trial: true });
    const box = await drawer.boundingBox();
    if (!box) throw new Error("the drawer is not open");
    const x = side === "right" ? box.x + box.width + 10 : box.x - 10;
    await page.touchscreen.tap(x, box.y + box.height / 2);
  }

  test("a lost connection shows above an open file", async ({ ox: page, services }) => {
    await newSession(page, "hello");
    await toggleFiles(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(viewer(page)).toBeVisible();
    await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toBeVisible();
  });

  test("the session list opens at load only when the URL names no session", async ({ ox: page }) => {
    await expect(sessions(page)).toBeVisible();
    await expect(toggleSessions(page)).toHaveAttribute("aria-pressed", "true");
    await newSession(page, "first");
    await page.reload();
    await expect(replies(page)).toHaveText(["You said: first"]);
    await expect(sessions(page)).toBeHidden();
    await expect(toggleSessions(page)).toHaveAttribute("aria-pressed", "false");
  });

  test("the session list closes on picking a session or tapping outside it", async ({ ox: page }) => {
    await newSession(page, "first");
    await expect(sessions(page)).toBeHidden();
    await toggleSessions(page).tap();
    await expect(sessions(page)).toBeVisible();
    await tapBeside(page, sessions(page), "right");
    await expect(sessions(page)).toBeHidden();
    await toggleSessions(page).tap();
    await sessionButton(page, "first").tap();
    await expect(sessions(page)).toBeHidden();
  });

  test("the file tree drawer closes on tapping outside, from its toggle by keyboard, or on opening a file", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await toggleFiles(page).tap();
    await expect(files(page)).toBeVisible();
    await expect(toggleFiles(page)).toHaveAttribute("aria-pressed", "true");
    await expect(files(page).getByRole("button", { name: /^Toggle/ })).toHaveCount(0);
    // The drawer covers its toggle, which closes it from the keyboard.
    await toggleFiles(page).press("Enter");
    await expect(files(page)).toHaveCount(0);
    await toggleFiles(page).tap();
    await tapBeside(page, files(page), "left");
    await expect(files(page)).toHaveCount(0);
    await toggleFiles(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(files(page)).toHaveCount(0);
    await expect(viewer(page)).toBeVisible();
  });

  test("an open file replaces the transcript until its tab closes", async ({ ox: page }) => {
    await newSession(page, "first");
    await toggleFiles(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await expect(page.getByRole("main")).toBeHidden();
    await viewer(page).getByRole("button", { name: "Close a.txt" }).tap();
    await expect(viewer(page)).toHaveCount(0);
    await expect(page.getByRole("main")).toBeVisible();
    await expect(replies(page)).toHaveText(["You said: first"]);
  });

  test("panels cannot be resized on a narrow screen", async ({ ox: page }) => {
    // A hidden separator is found only with includeHidden.
    const separator = (name: string) => page.getByRole("separator", { name, includeHidden: true });
    await expect(separator("Resize sessions")).toHaveCount(1);
    await expect(separator("Resize sessions")).toBeHidden();
    await newSession(page, "first");
    await toggleFiles(page).tap();
    await expect(separator("Resize files")).toHaveCount(1);
    await expect(separator("Resize files")).toBeHidden();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(separator("Resize file viewer")).toHaveCount(1);
    await expect(separator("Resize file viewer")).toBeHidden();
  });

  test("crossing the breakpoint closes the drawer, and a wide drag leaves the drawer's width", async ({ ox: page }) => {
    const nav = sessions(page);
    await newSession(page, "first");
    await toggleSessions(page).tap();
    await expect(nav).toBeVisible();
    const drawer = await width(nav);

    await page.setViewportSize({ width: 1000, height: 844 });
    await expect(nav).toBeVisible();
    await expect(files(page)).toBeVisible();
    const wide = await width(nav);
    await drag(page, "Resize sessions", 100);
    await near(nav, wide + 100);

    await page.setViewportSize({ width: 390, height: 844 });
    await expect(nav).toBeHidden();
    await expect(toggleSessions(page)).toHaveAttribute("aria-pressed", "false");
    await toggleSessions(page).tap();
    await expect(nav).toBeVisible();
    await near(nav, drawer);
  });

  // The keyboard below covers 336 px of the 844 px window, and Safari pans the
  // page 120 px down, leaving 508 px visible from 120 to 628.
  const keyboard = { height: 336, offsetTop: 120 };

  test("the header and composer stay at the visible area's edges as the keyboard opens and closes", async ({
    ox: page,
  }) => {
    await setSafeArea(page, { top: 47, bottom: 34 });
    await newSession(page, "first");
    const header = page.getByRole("main").locator("header");
    const composer = page.getByRole("main").locator(".composer");
    // frame is the header's top and the composer's bottom.
    const frame = async () => ({ top: (await edges(header)).top, bottom: (await edges(composer)).bottom });
    // The composer sits its 16 px margin above the home indicator's 34 px inset.
    await expect.poll(frame).toEqual({ top: 47, bottom: 794 });
    await openKeyboard(page, keyboard);
    // Above the keyboard, the home indicator's inset is gone.
    await expect.poll(frame).toEqual({ top: 167, bottom: 612 });
    // A pan alone moves the layout with the visible area.
    await panKeyboard(page, 60);
    await expect.poll(frame).toEqual({ top: 107, bottom: 552 });
    await closeKeyboard(page);
    await expect.poll(frame).toEqual({ top: 47, bottom: 794 });
    // A pinch zoom leaves the layout where it was.
    await openKeyboard(page, { ...keyboard, scale: 2 });
    await expect.poll(frame).toEqual({ top: 47, bottom: 794 });
  });

  test("a drawer spans the visible area while the keyboard is open", async ({ ox: page }) => {
    await setSafeArea(page, { top: 47, bottom: 34 });
    await newSession(page, "first");
    await openKeyboard(page, keyboard);
    await toggleSessions(page).tap();
    await expect(sessions(page)).toBeVisible();
    await expect.poll(() => edges(sessions(page))).toEqual({ top: 120, bottom: 628 });
  });
});

test("clicking a file opens it under its tab and marks its row, and another file replaces it", async ({
  ox: page,
  services,
}) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "first\n");
  writeFileSync(join(services.workspace, "src", "b.txt"), "second\n");
  await newSessionButton(page).click();
  await entry(page, "src").click();
  const read = await hold(page, "workspace/read_file", { path: "src/a.txt" });
  await entry(page, "a.txt").click();
  await expect(tab(page)).toHaveText("a.txt");
  await expect(tab(page)).toHaveAccessibleDescription("src/a.txt");
  await read.arrived;
  await expect(pane(page)).toHaveText("");
  read.release();
  await expect(pane(page)).toHaveText("first");
  await expect(entry(page, "a.txt")).toHaveAttribute("aria-current", "true");

  await entry(page, "b.txt").click();
  await expect(tab(page)).toHaveText("b.txt");
  await expect(pane(page)).toHaveText("second");
  await expect(entry(page, "b.txt")).toHaveAttribute("aria-current", "true");
  await expect(entry(page, "a.txt")).not.toHaveAttribute("aria-current");
});

clocked(
  "the pane keeps its scroll position across a re-read and a click on the open file",
  async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), `first\n${`${"long line ".repeat(200)}\n`.repeat(400)}`);
    await newSessionButton(page).click();
    await openFile(page, "src/a.txt");
    await expect(pane(page)).toContainText("first");
    await pane(page).hover();
    await page.mouse.wheel(300, 2000);
    await expect(pane(page)).toHaveJSProperty("scrollTop", 2000);
    await expect(pane(page)).toHaveJSProperty("scrollLeft", 300);

    await answered(page, "workspace/read_file", "src/a.txt");
    await expect(pane(page)).toHaveJSProperty("scrollTop", 2000);
    await expect(pane(page)).toHaveJSProperty("scrollLeft", 300);

    await entry(page, "a.txt").click();
    await answered(page, "workspace/read_file", "src/a.txt");
    await expect(pane(page)).toHaveJSProperty("scrollTop", 2000);
    await expect(pane(page)).toHaveJSProperty("scrollLeft", 300);
  },
);

clocked("a re-read of an unchanged file keeps the text selected in the pane", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "main.go"), "package main\n\n// note\nfunc main() {}\n");
  await newSessionButton(page).click();
  await openFile(page, "src/main.go");
  await expect(pane(page)).toContainText("func main() {}");
  await pane(page).selectText();
  // Playwright has no web-first assertion for a selection.
  const selection = () => page.evaluate(() => getSelection()?.toString());
  await expect.poll(selection).toContain("func main() {}");
  const selected = await selection();
  await answered(page, "workspace/read_file", "src/main.go");
  await expect.poll(selection).toBe(selected);
});

clocked("the pane shows changes on disk and why a deleted file cannot be read", async ({ ox: page, services }) => {
  const a = join(services.workspace, "src", "a.txt");
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(a, "first\n");
  await newSessionButton(page).click();
  await openFile(page, "src/a.txt");
  await expect(pane(page)).toHaveText("first");
  writeFileSync(a, "changed\n");
  await page.clock.runFor(1000);
  await expect(pane(page)).toHaveText("changed");
  rmSync(a);
  await page.clock.runFor(1000);
  await expect(pane(page)).toContainText("no such file or directory");
  await expect(tab(page)).toHaveText("a.txt");
  writeFileSync(a, "back\n");
  await page.clock.runFor(1000);
  await expect(pane(page)).toHaveText("back");
});

test("the pane keeps a file's carriage returns", async ({ ox: page, services }) => {
  const template = "let a = 1;\r\nlet b = 2;\rlet c = 3;\n\tindented\n";
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "template.ts"), template);
  await newSessionButton(page).click();
  await openFile(page, "src/template.ts");
  await expect(tab(page)).toHaveText("template.ts");
  await expect(pane(page)).toHaveJSProperty("textContent", template);
});

clocked(
  "a read error shows in the pane until the next read, and a binary file says so",
  async ({ ox: page, services }) => {
    writeFileSync(join(services.workspace, "bin.dat"), "a\0b");
    await newSessionButton(page).click();
    const read = await hold(page, "workspace/read_file", { path: "bin.dat" });
    await entry(page, "bin.dat").click();
    await expect(tab(page)).toHaveText("bin.dat");
    await read.arrived;
    read.fail("relay unavailable");
    await expect(pane(page)).toHaveText("relay unavailable");
    await page.clock.runFor(1000);
    await expect(pane(page)).toHaveText("file is binary");
  },
);

test("each session and draft keeps its open file, and a draft's carries to its session", async ({
  ox: page,
  services,
}) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  writeFileSync(join(services.workspace, "src", "b.txt"), "b\n");
  await newSession(page, "first");
  await newSession(page, "second");
  await sessionButton(page, "first").click();
  await openFile(page, "src/a.txt");
  await sessionButton(page, "second").click();
  await expect(viewer(page)).toHaveCount(0);
  await openFile(page, "src/b.txt");
  await sessionButton(page, "first").click();
  await expect(tab(page)).toHaveText("a.txt");
  await sessionButton(page, "second").click();
  await expect(tab(page)).toHaveText("b.txt");

  await newSessionButton(page).click();
  await expect(viewer(page)).toHaveCount(0);
  await openFile(page, "src/a.txt");
  await sessionButton(page, "second").click();
  await expect(tab(page)).toHaveText("b.txt");
  await newSessionButton(page).click();
  await expect(tab(page)).toHaveText("a.txt");
  await prompt(page).fill("third");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: third"]);
  await expect(sessionButton(page, "third")).toHaveAttribute("aria-current", "page");
  await expect(tab(page)).toHaveText("a.txt");
  await expect(pane(page)).toHaveText("a");
});

test("a reload closes the open file", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  await newSession(page, "first");
  await openFile(page, "src/a.txt");
  await page.reload();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await expect(entry(page, "src")).toBeVisible();
  await expect(viewer(page)).toHaveCount(0);
});
