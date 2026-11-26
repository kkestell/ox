import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { Locator, Page, Request, Route } from "@playwright/test";
import { fakeConfig } from "./services";
import {
  addWorkspace,
  clocked,
  expect,
  gitRepo,
  hold,
  isRequest,
  menuItem,
  model,
  newDraft,
  newSession,
  newSessionButton,
  openMenu,
  permissionMode,
  prompt,
  replies,
  rpcCalls,
  send,
  sessionButton,
  sessionId,
  sessionItem,
  sessions,
  showPage,
  test,
  workspaceSection,
} from "./ui";

const files = (page: Page) => page.getByRole("complementary", { name: "Files" });
// entry is the file tree's button for the file or folder called name.
const entry = (page: Page, name: string) => files(page).getByRole("button", { name, exact: true });
// folder is the file tree's item for the folder called name, holding its listing.
const folder = (page: Page, name: string) =>
  files(page)
    .getByRole("listitem")
    .filter({ has: page.getByRole("button", { name, exact: true }) });
const viewer = (page: Page) => page.getByRole("region", { name: "File" });
const tab = (page: Page) => viewer(page).getByRole("tablist", { name: "Open files" }).getByRole("tab");
const pane = (page: Page) => viewer(page).getByRole("tabpanel");
const wrapButton = (page: Page) => viewer(page).getByRole("button", { name: "Toggle word wrap" });
const modes = (page: Page) => viewer(page).getByRole("group", { name: "Show as" });
// modeButton is the file view's button that shows the file as label.
const modeButton = (page: Page, label: "Diff" | "Source" | "Preview") =>
  modes(page).getByRole("button", { name: label, exact: true });
const filesTab = (page: Page) => page.getByRole("tab", { name: "Files", exact: true });
const changesTab = (page: Page) => page.getByRole("tab", { name: "Changes", exact: true });
const toggleWorkbench = (page: Page) => page.getByRole("button", { name: "Toggle workbench" });

// showFiles opens the workbench on the file tree, which the page closes at
// load.
async function showFiles(page: Page) {
  await filesTab(page).click();
  await expect(filesTab(page)).toHaveAttribute("aria-selected", "true");
}

// showChanges opens the workbench on the changes.
async function showChanges(page: Page) {
  await changesTab(page).click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
}

// closeWorkbench closes the workbench from its own header.
async function closeWorkbench(page: Page) {
  await toggleWorkbench(page).click();
  await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
}

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

test("a workspace added with a trailing slash is named for its directory", async ({ page, services }) => {
  await page.goto(services.url);
  await addWorkspace(page, `${services.workspace}/`);
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
  await newDraft(page);
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
      titling: { ...fakeConfig.titling, effort: "" },
    },
  });

  test("a model without efforts creates a session without effort", async ({ ox: page }) => {
    await newDraft(page);
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

test("removing a workspace asks first, removes its section before oxd answers, and shows a failure in the session list", async ({
  ox: page,
  services,
}) => {
  const workspace = workspaceSection(page, services.workspace);
  await menuItem(await openMenu(workspace), "Remove workspace").click();
  const dialog = page.getByRole("alertdialog", { name: "Remove workspace?" });
  await expect(dialog).toHaveAccessibleDescription(
    `This removes "${services.workspace}" from Ox. The directory stays on disk.`,
  );
  const removal = await hold(page, "workspace/remove");
  await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  await removal.arrived;
  await expect(dialog).toBeHidden();
  await expect(workspace).toHaveCount(0);
  removal.fail("relay unavailable");
  await expect(sessions(page).getByRole("alert")).toHaveText("relay unavailable");
  await expect(workspace).toBeVisible();
});

test("removing a workspace discards its draft", async ({ ox: page, services }) => {
  const workspace = workspaceSection(page, services.workspace);
  await newDraft(page);
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
  await newDraft(page);
  await showFiles(page);
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

test("a worktree session's file tree holds only committed files", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  await newSessionButton(page).click();
  await showFiles(page);
  await expect(entry(page, "untracked.txt")).toBeVisible();
  await prompt(page).fill("committed only");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: committed only"]);
  await expect(entry(page, "committed.txt")).toBeVisible();
  await expect(entry(page, "untracked.txt")).toHaveCount(0);
});

clocked("each session keeps its own expanded folders", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newSession(page, "first");
  await showFiles(page);
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

  // A tree shown for the first session lists through the second once the
  // second is selected, so deleting the first leaves it listing.
  await closeWorkbench(page);
  await expect(files(page)).toHaveCount(0);
  await showFiles(page);
  await expect(entry(page, "a.txt")).toBeVisible();
  await sessionButton(page, "second").click();
  await expect(entry(page, "src")).toHaveAttribute("aria-expanded", "false");
  await menuItem(await openMenu(sessionItem(page, "first")), "Delete").click();
  await page.getByRole("alertdialog", { name: "Delete session?" }).getByRole("button", { name: "Delete" }).click();
  await expect(sessionButton(page, "first")).toHaveCount(0);
  await answered(page, "workspace/list_folder", "");
  await expect(entry(page, "src")).toBeVisible();
  await expect(files(page).getByText("session not found")).toHaveCount(0);
});

clocked("the tree shows changes on disk", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newDraft(page);
  await showFiles(page);
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
  await newDraft(page);
  await showFiles(page);
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
  await newDraft(page);
  await showFiles(page);
  const answers: [(route: Route) => Promise<void>, string | RegExp][] = [
    [(route) => route.fulfill({ status: 502, body: "relay unavailable" }), "relay unavailable"],
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

for (const [failure, fail] of [
  ["a 503", (route: Route) => route.fulfill({ status: 503, body: "unknown connection" })],
  ["a POST that cannot reach the relay", (route: Route) => route.abort()],
] as const) {
  clocked(`a lost connection keeps the last listing after ${failure}`, async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "");
    await newDraft(page);
    await showFiles(page);
    await entry(page, "src").click();
    await expect(entry(page, "a.txt")).toBeVisible();
    const listing = (route: Route) => {
      const body = route.request().postDataJSON();
      if (body?.method !== "workspace/list_folder" || body.params.path !== "src") return route.fallback();
      return fail(route);
    };
    // The replacement stream stays down, so the status stays up to be read.
    const down = (route: Route) => route.abort();
    await page.route("**/events", down);
    await page.route("**/rpc*", listing);
    await page.clock.runFor(1000);
    const reconnecting = page.getByRole("status").filter({ hasText: "Reconnecting…" });
    await expect(reconnecting).toBeVisible();
    // A page that is still reconnecting offers no Reload button.
    await expect(page.getByRole("button", { name: "Reload" })).toHaveCount(0);
    await expect(entry(page, "a.txt")).toBeVisible();
    await expect(page.getByText("Could not reach ox")).toHaveCount(0);
    await page.unroute("**/rpc*", listing);
    await page.unroute("**/events", down);
    await page.clock.runFor(1000);
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toHaveCount(0);
    await expect(send(page)).toBeEnabled();
  });
}

clocked(
  "a request cut off after its connection was replaced leaves the new connection alone",
  async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "");
    await newDraft(page);
    await showFiles(page);
    await entry(page, "src").click();
    await expect(entry(page, "a.txt")).toBeVisible();
    const stale = await hold(page, "workspace/list_folder", { path: "src" });
    await page.clock.runFor(1000);
    await stale.arrived;
    // The replacement stream stays down until the status has been read.
    const down = (route: Route) => route.abort();
    await page.route("**/events", down);
    await services.restartRelay();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
    await page.unroute("**/events", down);
    await page.clock.runFor(1000);
    await expect(send(page)).toBeEnabled({ timeout: 10_000 });
    const lists = rpcCalls(page, "workspace/list");
    stale.abort();
    await page.clock.runFor(3000);
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toHaveCount(0);
    await expect(send(page)).toBeEnabled();
    expect(lists).toEqual([]);
  },
);

clocked("a page shown while connected keeps a request in flight", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "");
  await newDraft(page);
  await showFiles(page);
  const listing = await hold(page, "workspace/list_folder", { path: "src" });
  await entry(page, "src").click();
  await listing.arrived;
  await showPage(page);
  listing.release();
  await expect(entry(page, "a.txt")).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

clocked(
  "a lost connection keeps the tree and lists nothing until the page has reloaded",
  async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    mkdirSync(join(services.workspace, "lib"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "");
    await newDraft(page);
    await showFiles(page);
    await prompt(page).fill("draft");
    await entry(page, "src").click();
    await expect(entry(page, "a.txt")).toBeVisible();
    // A listing in flight when the connection is lost.
    const inFlight = await hold(page, "workspace/list_folder", { path: "src" });
    await page.clock.runFor(1000);
    await inFlight.arrived;
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
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
    // The last attempt may still be failing, so fire the reconnect timer until one gets through.
    let reloading = false;
    void reload.arrived.then(() => {
      reloading = true;
    });
    await expect
      .poll(async () => {
        await page.clock.runFor(1000);
        return reloading;
      })
      .toBe(true);
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
    await newDraft(page);
    await showFiles(page);
    await entry(page, "locked").click();
    await expect(folder(page, "locked").getByText("permission denied")).toBeVisible();
    await entry(page, "empty").click();
    await expect(folder(page, "empty").getByText("Empty", { exact: true })).toBeVisible();
  } finally {
    chmodSync(locked, 0o755);
  }
});

test("another workspace's draft shows that workspace's tree", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "mine.txt"), "");
  const other = mkdtempSync(join(tmpdir(), "ox-e2e-other-"));
  try {
    writeFileSync(join(other, "other.txt"), "");
    await workspaceSection(page, services.workspace).getByRole("button", { name: "New session" }).click();
    await showFiles(page);
    await expect(entry(page, "mine.txt")).toBeVisible();
    await addWorkspace(page, other);
    await workspaceSection(page, other).getByRole("button", { name: "New session" }).click();
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

const changes = (page: Page) => page.getByRole("region", { name: "Changes" });
const change = (page: Page, path: string) => changes(page).getByRole("article", { name: path });

// showEdit makes the workspace a git repository with an edit to
// committed.txt, and shows its draft's changes.
async function showEdit(page: Page, workspace: string) {
  gitRepo(workspace);
  writeFileSync(join(workspace, "committed.txt"), "edited\nmore\n");
  await newDraft(page);
  await showChanges(page);
}

test("the Changes tab lists each changed file with its counts and diff", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await expect(changes(page)).toMatchAriaSnapshot(`
    - region "Changes":
      - button "Toggle workbench"
      - text: master +2 −1
      - article "committed.txt":
        - text: committed.txt +2 −1
        - button "Open committed.txt"
        - button "Lines 1–2 +2 −1" [expanded]
        - code:
          - deletion: 1 committed
          - insertion: 1 edited
          - insertion: 2 more
      - article "untracked.txt":
        - text: untracked.txt +0 −0
        - button "Open untracked.txt"
  `);
});

test("the Changes tab shows the changes in place of the file tree", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await expect(changes(page)).toBeVisible();
  await expect(files(page)).toHaveCount(0);
});

test("the file tree's header shows the branch and the lines added and removed", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "committed.txt"), "edited\nmore\n");
  await newDraft(page);
  await showFiles(page);
  await expect(files(page)).toMatchAriaSnapshot(`
    - complementary "Files":
      - text: master +2 −1
  `);
});

test("the file tree's header says why outside a Git repository", async ({ ox: page }) => {
  await newDraft(page);
  await showFiles(page);
  await expect(files(page)).toContainText("not a git repository");
});

test("a section's header collapses and expands its lines", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  const section = change(page, "committed.txt").getByRole("button", { name: /^Lines 1–2/ });
  const insertions = change(page, "committed.txt").getByRole("insertion");
  await expect(section).toHaveAttribute("aria-expanded", "true");
  await expect(insertions).toHaveCount(2);
  await section.click();
  await expect(section).toHaveAttribute("aria-expanded", "false");
  await expect(insertions).toHaveCount(0);
  await section.click();
  await expect(section).toHaveAttribute("aria-expanded", "true");
  await expect(insertions).toHaveCount(2);
});

test("a changed file's Open button opens it under the Files tab", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await change(page, "committed.txt").getByRole("button", { name: "Open committed.txt" }).click();
  await expect(filesTab(page)).toHaveAttribute("aria-selected", "true");
  await expect(tab(page)).toHaveText("committed.txt");
  await expect(modeButton(page, "Diff")).toHaveAttribute("aria-pressed", "true");
});

test("the Changes tab holds through switching sessions and resets on reload", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await newSession(page, "first");
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await expect(change(page, "committed.txt")).toBeVisible();
  await newDraft(page);
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await sessionButton(page, "first").click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await page.reload();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
  await expect(changes(page)).toHaveCount(0);
});

test("a directory outside a Git repository says why in the Changes tab", async ({ ox: page }) => {
  await newDraft(page);
  await showChanges(page);
  await expect(changes(page).getByText("not a git repository")).toHaveCount(1);
});

clocked("the Changes tab shows a new edit on the next tick", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await expect(change(page, "committed.txt")).toBeVisible();
  writeFileSync(join(services.workspace, "later.txt"), "later\n");
  await page.clock.runFor(1000);
  await expect(change(page, "later.txt")).toBeVisible();
});

test.describe("on a wide screen", () => {
  test.use({ viewport: { width: 1600, height: 900 } });

  test.beforeEach(({ services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  });

  test("the sessions toggle sits in the session list's header while it shows, and in the session's header while it is hidden", async ({
    ox: page,
  }) => {
    const main = page.getByRole("main");
    const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
    await newSession(page, "first");
    await expect(sessions(page).getByRole("button", { name: "Toggle sessions" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(main.getByRole("button", { name: "Toggle sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(sessions(page)).toBeHidden();
    await expect(main.getByRole("button", { name: "Toggle sessions" })).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByRole("separator", { name: "Resize sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(sessions(page)).toBeVisible();
  });

  test("a tab opens the workbench in its mode, and the workbench's toggle closes it with neither tab selected", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
    await showFiles(page);
    await expect(files(page)).toBeVisible();
    await closeWorkbench(page);
    await expect(files(page)).toHaveCount(0);
    await showChanges(page);
    await expect(changes(page)).toBeVisible();
    await closeWorkbench(page);
    await expect(changes(page)).toHaveCount(0);
  });

  test("Files shows the tree alone; a file opens between the transcript and the tree, and closing its tab leaves the tree", async ({
    ox: page,
  }) => {
    const resizeViewer = page.getByRole("separator", { name: "Resize file viewer" });
    await newSession(page, "first");
    await showFiles(page);
    await expect(files(page)).toBeVisible();
    await expect(viewer(page)).toHaveCount(0);
    await expect(resizeViewer).toHaveCount(0);
    await openFile(page, "src/a.txt");
    await expect(viewer(page)).toBeVisible();
    await expect(resizeViewer).toHaveCount(1);
    await expect(files(page)).toBeVisible();
    await viewer(page).getByRole("button", { name: "Close a.txt" }).click();
    await expect(viewer(page)).toHaveCount(0);
    await expect(resizeViewer).toHaveCount(0);
    await expect(files(page)).toBeVisible();
  });

  test("with nothing selected, the header offers no Files or Changes tabs", async ({ ox: page }) => {
    await expect(filesTab(page)).toHaveCount(0);
    await expect(changesTab(page)).toHaveCount(0);
    await newDraft(page);
    await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
  });

  test("dragging a sash resizes its panel within bounds, and only while the button is down", async ({ ox: page }) => {
    const nav = sessions(page);
    await newDraft(page);
    await showFiles(page);
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

    // At a narrow window both side panels together leave the transcript its
    // minimum.
    await page.setViewportSize({ width: 960, height: 900 });
    await drag(page, "Resize sessions", 1000);
    await drag(page, "Resize files", -1000);
    expect(await width(page.getByRole("main"))).toBeGreaterThanOrEqual(240);
  });

  test("the file split leaves each side its minimum and comes back when the file reopens", async ({ ox: page }) => {
    const main = page.getByRole("main");
    await newDraft(page);
    await showFiles(page);
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

  test("at a 960 px window the transcript and the file split the width beside the tree evenly", async ({
    ox: page,
  }) => {
    await page.setViewportSize({ width: 960, height: 900 });
    await newDraft(page);
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await near(page.getByRole("main"), await width(viewer(page)));
    await expect(files(page)).toBeInViewport({ ratio: 1 });
  });

  test("at a 960 px window the session list's sash leaves the transcript 240 px beside the tree", async ({
    ox: page,
  }) => {
    await page.setViewportSize({ width: 960, height: 900 });
    await newDraft(page);
    await showFiles(page);
    await drag(page, "Resize sessions", 2000);
    await near(page.getByRole("main"), 240);
    await expect(files(page)).toBeInViewport({ ratio: 1 });
  });

  test("panel widths are page-wide and each session keeps its own split", async ({ ox: page }) => {
    const main = page.getByRole("main");
    const nav = sessions(page);
    await newSession(page, "first");
    await showFiles(page);
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

    // Whether the workbench shows is page-wide too.
    await closeWorkbench(page);
    await sessionButton(page, "second").click();
    await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(files(page)).toHaveCount(0);
  });

  test("a draft's split carries to the session it creates", async ({ ox: page }) => {
    const main = page.getByRole("main");
    await newDraft(page);
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await drag(page, "Resize file viewer", -100);
    const split = await width(main);
    await prompt(page).fill("third");
    await send(page).click();
    await expect(replies(page)).toHaveText(["You said: third"]);
    await expect(viewer(page)).toBeVisible();
    await near(main, split);
  });

  test("a reload closes the workbench and restores the default widths", async ({ ox: page }) => {
    const main = page.getByRole("main");
    const nav = sessions(page);
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    const first = [await width(nav), await width(files(page)), await width(main), await width(viewer(page))];
    await drag(page, "Resize sessions", 100);
    await drag(page, "Resize files", -100);
    await drag(page, "Resize file viewer", -100);
    await page.reload();
    await expect(replies(page)).toHaveText(["You said: first"]);
    await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(files(page)).toHaveCount(0);
    await showFiles(page);
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
  const toggleTree = (page: Page) => page.getByRole("button", { name: "Toggle file tree" });
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
    await filesTab(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(viewer(page)).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
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
    await filesTab(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await expect(files(page)).toHaveCount(0);
    await toggleTree(page).tap();
    await expect(files(page)).toBeVisible();
    await expect(toggleTree(page)).toHaveAttribute("aria-pressed", "true");
    await tapBeside(page, files(page), "left");
    await expect(files(page)).toHaveCount(0);
    await toggleTree(page).tap();
    await expect(files(page)).toBeVisible();
    // The drawer covers its toggle, which closes it from the keyboard.
    await toggleTree(page).press("Enter");
    await expect(files(page)).toHaveCount(0);
    await toggleTree(page).tap();
    await entry(page, "a.txt").tap();
    await expect(files(page)).toHaveCount(0);
    await expect(viewer(page)).toBeVisible();
  });

  test("the Files tab replaces the transcript with the tree, and the workbench's toggle brings it back", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await filesTab(page).tap();
    await expect(files(page)).toBeVisible();
    await expect(page.getByRole("main")).toBeHidden();
    await toggleWorkbench(page).tap();
    await expect(files(page)).toHaveCount(0);
    await expect(page.getByRole("main")).toBeVisible();
    await expect(replies(page)).toHaveText(["You said: first"]);
  });

  test("closing the file's tab leaves the tree filling the workbench", async ({ ox: page }) => {
    await newSession(page, "first");
    await filesTab(page).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await viewer(page).getByRole("button", { name: "Close a.txt" }).tap();
    await expect(viewer(page)).toHaveCount(0);
    await expect(files(page)).toBeVisible();
    await expect(page.getByRole("main")).toBeHidden();
  });

  test("a changed file's Open button shows the file in place of the changes", async ({ ox: page, services }) => {
    gitRepo(services.workspace);
    writeFileSync(join(services.workspace, "committed.txt"), "edited\n");
    await newSession(page, "first");
    await changesTab(page).tap();
    await change(page, "committed.txt").getByRole("button", { name: "Open committed.txt" }).tap();
    await expect(tab(page)).toHaveText("committed.txt");
    await expect(changes(page)).toHaveCount(0);
    await expect(page.getByRole("main")).toBeHidden();
  });

  test("panels cannot be resized on a narrow screen", async ({ ox: page }) => {
    // A hidden separator is found only with includeHidden.
    const separator = (name: string) => page.getByRole("separator", { name, includeHidden: true });
    await expect(separator("Resize sessions")).toHaveCount(1);
    await expect(separator("Resize sessions")).toBeHidden();
    await newSession(page, "first");
    await filesTab(page).tap();
    await expect(files(page)).toBeVisible();
    await expect(separator("Resize files")).toHaveCount(0);
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
    await expect(separator("Resize file viewer")).toHaveCount(0);
    await expect(separator("Resize files")).toHaveCount(0);
  });

  test("crossing the breakpoint closes the phone's workbench and leaves the wide one as it was", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await page.setViewportSize({ width: 1600, height: 844 });
    await changesTab(page).tap();
    await expect(changes(page)).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole("main")).toBeVisible();
    await expect(changes(page)).toHaveCount(0);
    await filesTab(page).tap();
    await expect(files(page)).toBeVisible();
    await page.setViewportSize({ width: 1600, height: 844 });
    await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
    await expect(changes(page)).toBeVisible();
    // The wide screen's Changes tab clears only once the page is narrow, so
    // the checks after it see the narrow layout.
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(filesTab(page)).toHaveAttribute("aria-selected", "false");
    await expect(files(page)).toHaveCount(0);
    await expect(page.getByRole("main")).toBeVisible();
  });

  test("crossing the breakpoint closes the drawer, and a wide drag leaves the drawer's width", async ({ ox: page }) => {
    const nav = sessions(page);
    await newSession(page, "first");
    await toggleSessions(page).tap();
    await expect(nav).toBeVisible();
    const drawer = await width(nav);

    await page.setViewportSize({ width: 1000, height: 844 });
    await expect(nav).toBeVisible();
    await expect(files(page)).toHaveCount(0);
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
});

test("clicking a file opens it under its tab and marks its row, and another file replaces it", async ({
  ox: page,
  services,
}) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "first\n");
  writeFileSync(join(services.workspace, "src", "b.txt"), "second\n");
  await newDraft(page);
  await showFiles(page);
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
  await expect(wrapButton(page)).toBeVisible();
  await expect(modes(page)).toHaveCount(0);

  await entry(page, "b.txt").click();
  await expect(tab(page)).toHaveText("b.txt");
  await expect(pane(page)).toHaveText("second");
  await expect(entry(page, "b.txt")).toHaveAttribute("aria-current", "true");
  await expect(entry(page, "a.txt")).not.toHaveAttribute("aria-current");
});

clocked(
  "the pane keeps its scroll position across a re-read, a click on the open file, and a click on the selected draft",
  async ({ ox: page, services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), `first\n${`${"long line ".repeat(200)}\n`.repeat(400)}`);
    await newDraft(page);
    await showFiles(page);
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

    await newDraft(page);
    await answered(page, "workspace/read_file", "src/a.txt");
    await expect(pane(page)).toHaveJSProperty("scrollTop", 2000);
    await expect(pane(page)).toHaveJSProperty("scrollLeft", 300);
  },
);

clocked("a re-read of an unchanged file keeps the text selected in the pane", async ({ ox: page, services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "main.go"), "package main\n\n// note\nfunc main() {}\n");
  await newDraft(page);
  await showFiles(page);
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
  await newDraft(page);
  await showFiles(page);
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
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "src/template.ts");
  await expect(tab(page)).toHaveText("template.ts");
  await expect(pane(page)).toHaveJSProperty("textContent", template);
});

test("the wrap button wraps long lines to the pane", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "a.txt"), `${"long line ".repeat(200)}\n`);
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "a.txt");
  await expect(pane(page)).toContainText("long line");
  await expect(wrapButton(page)).toHaveAttribute("aria-pressed", "false");
  await pane(page).hover();
  await page.mouse.wheel(300, 0);
  await expect(pane(page)).toHaveJSProperty("scrollLeft", 300);

  await wrapButton(page).click();
  await expect(wrapButton(page)).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toHaveJSProperty("scrollLeft", 0);
  await pane(page).hover();
  await page.mouse.wheel(300, 0);
  await expect(pane(page)).toHaveJSProperty("scrollLeft", 0);
});

test("the wrap choice holds for the next file and resets on reload", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "a.txt"), "a\n");
  writeFileSync(join(services.workspace, "b.txt"), "b\n");
  await newSession(page, "first");
  await showFiles(page);
  await openFile(page, "a.txt");
  await wrapButton(page).click();
  await expect(wrapButton(page)).toHaveAttribute("aria-pressed", "true");
  await openFile(page, "b.txt");
  await expect(pane(page)).toHaveText("b");
  await expect(wrapButton(page)).toHaveAttribute("aria-pressed", "true");

  await page.reload();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await showFiles(page);
  await openFile(page, "a.txt");
  await expect(wrapButton(page)).toHaveAttribute("aria-pressed", "false");
});

test("an unchanged Markdown file opens in Preview, and Source shows its text", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "notes.md"), "# Title\n\ntext\n");
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "notes.md");
  await expect(pane(page).getByRole("heading", { name: "Title" })).toBeVisible();
  await expect(modeButton(page, "Preview")).toHaveAttribute("aria-pressed", "true");
  await expect(modeButton(page, "Diff")).toHaveCount(0);
  await expect(wrapButton(page)).toHaveCount(0);

  await modeButton(page, "Source").click();
  await expect(modeButton(page, "Source")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toContainText("# Title");
  await expect(pane(page).getByRole("heading")).toHaveCount(0);
  await expect(wrapButton(page)).toBeVisible();
});

test("a Markdown file's source view holds through switching sessions, and the next Markdown file opens as a preview", async ({
  ox: page,
  services,
}) => {
  writeFileSync(join(services.workspace, "a.md"), "# A\n");
  writeFileSync(join(services.workspace, "b.md"), "# B\n");
  await newSession(page, "first");
  await showFiles(page);
  await newSession(page, "second");
  await sessionButton(page, "first").click();
  await openFile(page, "a.md");
  await modeButton(page, "Source").click();
  await expect(pane(page)).toHaveText("# A");
  await sessionButton(page, "second").click();
  await expect(viewer(page)).toHaveCount(0);
  await sessionButton(page, "first").click();
  await expect(tab(page)).toHaveText("a.md");
  await expect(modeButton(page, "Source")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toHaveText("# A");

  await entry(page, "a.md").click();
  await expect(modeButton(page, "Source")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toHaveText("# A");

  await openFile(page, "b.md");
  await expect(modeButton(page, "Preview")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page).getByRole("heading", { name: "B" })).toBeVisible();
});

test("a changed file opens in Diff, showing the whole file with its removed and added lines in place", async ({
  ox: page,
  services,
}) => {
  const path = join(services.workspace, "lines.txt");
  const lines = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`);
  writeFileSync(path, `${lines.join("\n")}\n`);
  gitRepo(services.workspace);
  lines[9] = "changed 10";
  writeFileSync(path, `${lines.join("\n")}\n`);
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "lines.txt");
  await expect(modeButton(page, "Diff")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toMatchAriaSnapshot(`
    - code:
      - text: 1 line 1 2 line 2 3 line 3 4 line 4 5 line 5 6 line 6 7 line 7 8 line 8 9 line 9
      - deletion: 10 line 10
      - insertion: 10 changed 10
      - text: 11 line 11 12 line 12
  `);
});

test("a changed Markdown file offers Diff, Source, and Preview, and opens in Diff", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "notes.md"), "# Title\n");
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "notes.md"), "# Changed\n");
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "notes.md");
  await expect(modes(page)).toMatchAriaSnapshot(`
    - group "Show as":
      - button "Diff" [pressed]
      - button "Source"
      - button "Preview"
  `);
});

test("a changed file waits for its changes to be listed before choosing Diff", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "committed.txt"), "edited\n");
  await newSession(page, "first");
  const first = sessionId(page);
  await showFiles(page);
  await openFile(page, "committed.txt");
  await expect(modeButton(page, "Diff")).toHaveAttribute("aria-pressed", "true");
  await newDraft(page);
  await expect(viewer(page)).toHaveCount(0);
  const listing = await hold(page, "workspace/list_changes", { session_id: first });
  await sessionButton(page, "first").click();
  await listing.arrived;
  await expect(tab(page)).toHaveText("committed.txt");
  await expect(pane(page)).toHaveCount(0);
  await expect(modes(page)).toHaveCount(0);
  await expect(viewer(page).getByRole("deletion")).toHaveCount(0);
  listing.release();
  await expect(modeButton(page, "Diff")).toHaveAttribute("aria-pressed", "true");
});

clocked("the preview shows changes on disk", async ({ ox: page, services }) => {
  const notes = join(services.workspace, "notes.md");
  writeFileSync(notes, "# One\n");
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "notes.md");
  await expect(pane(page).getByRole("heading")).toHaveText("One");
  writeFileSync(notes, "# Two\n");
  await page.clock.runFor(1000);
  await expect(pane(page).getByRole("heading")).toHaveText("Two");
});

clocked(
  "a read error shows in the pane until the next read, and a binary file says so",
  async ({ ox: page, services }) => {
    writeFileSync(join(services.workspace, "bin.dat"), "a\0b");
    await newDraft(page);
    await showFiles(page);
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
  await showFiles(page);
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

  await newDraft(page);
  await expect(viewer(page)).toHaveCount(0);
  await openFile(page, "src/a.txt");
  await sessionButton(page, "second").click();
  await expect(tab(page)).toHaveText("b.txt");
  await newDraft(page);
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
  await showFiles(page);
  await openFile(page, "src/a.txt");
  await page.reload();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await showFiles(page);
  await expect(entry(page, "src")).toBeVisible();
  await expect(viewer(page)).toHaveCount(0);
});
