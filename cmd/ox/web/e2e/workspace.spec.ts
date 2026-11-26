import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { Locator, Page, Request, Route } from "@playwright/test";
import { fakeConfig } from "./services";
import {
  addWorkspace,
  allow,
  ask,
  clocked,
  cutReply,
  expect,
  gitRepo,
  heading,
  hold,
  isRequest,
  menuItem,
  model,
  newDraft,
  newSession,
  newSessionButton,
  open,
  openMenu,
  permissionMode,
  prompt,
  replies,
  rpcCalls,
  send,
  sessionButton,
  sessionItem,
  sessions,
  showPage,
  test,
  transcript,
  waiting,
  workspaceSection,
} from "./ui";

const files = (page: Page) => page.getByRole("complementary", { name: "Files" });
const changesList = (page: Page) => page.getByRole("complementary", { name: "Changes" });
// entry is the file tree's button for the file or folder called name.
const entry = (page: Page, name: string) => files(page).getByRole("button", { name, exact: true });
// folder is the file tree's item for the folder called name, holding its listing.
const folder = (page: Page, name: string) =>
  files(page)
    .getByRole("listitem")
    .filter({ has: page.getByRole("button", { name, exact: true }) });
const viewer = (page: Page) => page.getByRole("region", { name: "File" });
// fileTab is the tab of the file called name.
const fileTab = (page: Page, name: string) => page.getByRole("tab", { name, exact: true });
const changesTab = (page: Page) => page.getByRole("tab", { name: /^Changes/ });
const pane = (page: Page) => viewer(page).getByRole("tabpanel");
const wrapButton = (page: Page) => page.getByRole("button", { name: "Toggle word wrap" });
const modes = (page: Page) => page.getByRole("group", { name: "Show as" });
// modeButton is the tab strip's button that shows the file as label.
const modeButton = (page: Page, label: "Source" | "Preview") =>
  modes(page).getByRole("button", { name: label, exact: true });
const togglePanel = (page: Page) => page.getByRole("button", { name: "Toggle panel" });
// panelMode is the panel's button that lists its files or its changes.
const panelMode = (page: Page, label: "Files" | "Changes") => page.getByRole("button", { name: label, exact: true });
const closeButton = (page: Page, name: string) => page.getByRole("button", { name: `Close ${name}` });

// showPanel opens the panel at the right, which the page closes at load, on
// its files or its changes.
async function showPanel(page: Page, mode: "Files" | "Changes") {
  if ((await togglePanel(page).getAttribute("aria-pressed")) === "false") await togglePanel(page).click();
  await panelMode(page, mode).click();
  await expect(panelMode(page, mode)).toHaveAttribute("aria-pressed", "true");
}

// showFiles opens the panel on the file tree.
const showFiles = (page: Page) => showPanel(page, "Files");

// showChanges opens the panel on the changes, and the Changes tab from its
// summary row.
async function showChanges(page: Page) {
  await showPanel(page, "Changes");
  await changesList(page)
    .getByRole("button", { name: /^master |git repository/ })
    .click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
}

// openFile opens the file at path from the tree, expanding the folders on the
// way.
async function openFile(page: Page, path: string) {
  const names = path.split("/");
  for (const name of names.slice(0, -1)) await entry(page, name).click();
  await entry(page, names.at(-1) ?? path).click();
  await expect(fileTab(page, names.at(-1) ?? path)).toHaveAttribute("aria-selected", "true");
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
  await togglePanel(page).click();
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

test("the Changes tab's label counts the lines added and removed", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "committed.txt"), "edited\nmore\n");
  // A file whose diff is over 1 MiB, too large to show, still counts.
  writeFileSync(join(services.workspace, "large.txt"), `${"x".repeat(15)}\n`.repeat(70000));
  await newDraft(page);
  await showChanges(page);
  await expect(changesTab(page)).toHaveText(/^Changes\s*\+70002\s*−1$/);
});

test("the panel's Changes list shows the branch, the totals, and each changed file", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "committed.txt"), "edited\nmore\n");
  await newDraft(page);
  await showPanel(page, "Changes");
  await expect(changesList(page)).toMatchAriaSnapshot(`
    - complementary "Changes":
      - button "master +2 −1"
      - list:
        - listitem:
          - button "committed.txt +2 −1"
        - listitem:
          - button "untracked.txt +0 −0"
  `);
  await expect(changesTab(page)).toHaveCount(0);
});

test("the Changes list says why outside a Git repository", async ({ ox: page }) => {
  await newDraft(page);
  await showPanel(page, "Changes");
  await expect(changesList(page)).toContainText("not a git repository");
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

test("a changed file's Open button opens its file tab beside the Changes tab", async ({ ox: page, services }) => {
  await showEdit(page, services.workspace);
  await change(page, "committed.txt").getByRole("button", { name: "Open committed.txt" }).click();
  await expect(fileTab(page, "committed.txt")).toHaveAttribute("aria-selected", "true");
  await expect(pane(page)).toContainText("edited");
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
});

test("a file in the Changes list opens the Changes tab at its card", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  for (let i = 0; i < 20; i++) {
    writeFileSync(join(services.workspace, `file-${String(i).padStart(2, "0")}.txt`), "line\n".repeat(30));
  }
  await newDraft(page);
  await showPanel(page, "Changes");
  await changesList(page).getByRole("button", { name: "file-19.txt" }).click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await expect(change(page, "file-19.txt")).toBeInViewport();
  await expect(change(page, "file-00.txt")).not.toBeInViewport();
  // The session keeps the Changes tab at its card through switching sessions.
  await fileTab(page, "New session").click();
  await prompt(page).fill("first");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await changesTab(page).click();
  await newSession(page, "second");
  await sessionButton(page, "first").click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await expect(change(page, "file-19.txt")).toBeInViewport();
});

clocked("a revealed file that leaves and returns keeps the Changes tab's scroll", async ({ ox: page, services }) => {
  gitRepo(services.workspace);
  for (let i = 0; i < 20; i++) {
    writeFileSync(join(services.workspace, `file-${String(i).padStart(2, "0")}.txt`), "line\n".repeat(30));
  }
  await newDraft(page);
  await showPanel(page, "Changes");
  await changesList(page).getByRole("button", { name: "file-19.txt" }).click();
  await expect(change(page, "file-19.txt")).toBeInViewport();
  await change(page, "file-00.txt").scrollIntoViewIfNeeded();
  await expect(change(page, "file-00.txt")).toBeInViewport();
  const revealed = join(services.workspace, "file-19.txt");
  rmSync(revealed);
  await page.clock.runFor(1000);
  await expect(change(page, "file-19.txt")).toHaveCount(0);
  writeFileSync(revealed, "line\n".repeat(30));
  await page.clock.runFor(1000);
  await expect(change(page, "file-19.txt")).toHaveCount(1);
  await expect(change(page, "file-00.txt")).toBeInViewport();
});

test("a draft's Changes tab carries to its session, which keeps it through switching sessions and loses it on reload", async ({
  ox: page,
  services,
}) => {
  await showEdit(page, services.workspace);
  await fileTab(page, "New session").click();
  await prompt(page).fill("first");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: first"]);
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "false");
  await changesTab(page).click();
  await expect(change(page, "committed.txt")).toBeVisible();
  await newSession(page, "second");
  await expect(changesTab(page)).toHaveCount(0);
  await sessionButton(page, "first").click();
  await expect(changesTab(page)).toHaveAttribute("aria-selected", "true");
  await expect(change(page, "committed.txt")).toBeVisible();
  await page.reload();
  await expect(sessionButton(page, "first")).toHaveAttribute("aria-current", "page");
  await expect(changesTab(page)).toHaveCount(0);
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

test.describe("chat status behind a file tab", () => {
  test.beforeEach(({ services }) => {
    writeFileSync(join(services.workspace, "a.txt"), "a\n");
  });

  const chatAlert = (page: Page) => page.getByRole("main").getByRole("alert");
  // showFile opens a.txt, which hides the chat.
  async function showFile(page: Page) {
    await showFiles(page);
    await openFile(page, "a.txt");
  }

  test("a creation refused while a file tab shows marks the chat tab", async ({ ox: page }) => {
    await newDraft(page);
    const create = await hold(page, "session/create");
    await prompt(page).fill("keep this draft");
    await prompt(page).press("Enter");
    await create.arrived;
    await showFile(page);
    create.release({ model: "not-offered" });
    await expect(heading(page).getByRole("img", { name: /not-offered/ })).toBeVisible();
    await heading(page).click();
    await expect(chatAlert(page)).toContainText("not-offered");
    await expect(prompt(page)).toHaveValue("keep this draft");
  });

  test("Reopen draft shows the draft's chat", async ({ ox: page }) => {
    await newSession(page, "existing conversation");
    await newDraft(page);
    const create = await hold(page, "session/create");
    await prompt(page).fill("keep this draft");
    await prompt(page).press("Enter");
    await create.arrived;
    await showFile(page);
    await sessionButton(page, "existing conversation").click();
    create.release({ model: "not-offered" });
    await page.getByRole("alert").getByRole("button", { name: "Reopen draft" }).click();
    await expect(heading(page)).toHaveAttribute("aria-selected", "true");
    await expect(prompt(page)).toHaveValue("keep this draft");
  });

  test("a refused setting change while a file tab shows marks the chat tab", async ({ ox: page }) => {
    await newSession(page, "configure me");
    const failed = await hold(page, "session/configure");
    await permissionMode(page).selectOption("plan");
    await failed.arrived;
    await showFile(page);
    failed.fail("configuration refused");
    await expect(heading(page).getByRole("img", { name: "configuration refused" })).toBeVisible();
    await heading(page).click();
    await expect(chatAlert(page)).toHaveText("configuration refused");
  });

  test("a refused approval answer while a file tab shows marks the chat tab", async ({ ox: page }) => {
    await newSession(page, "earlier prompt");
    const approval = await ask(page, "Bash", "{}");
    const answer = await hold(page, "session/approve");
    await allow(approval);
    await answer.arrived;
    await showFile(page);
    answer.fail("relay unavailable");
    await expect(heading(page).getByRole("img", { name: "relay unavailable" })).toBeVisible();
    await heading(page).click();
    await expect(chatAlert(page)).toHaveText("relay unavailable");
    await expect(waiting(page).getByRole("article")).toHaveCount(1);
  });

  test("an approval that arrives while a file tab shows marks the chat tab", async ({ ox: page }) => {
    await newSession(page, "earlier prompt");
    const sent = await hold(page, "session/send");
    await prompt(page).fill("approve Bash {}");
    await prompt(page).press("Enter");
    await sent.arrived;
    await showFile(page);
    sent.release();
    await expect(heading(page).getByRole("img", { name: "Waiting on you" })).toBeVisible();
    await heading(page).click();
    await expect(waiting(page).getByRole("article")).toHaveCount(1);
  });

  test("a recovered creation behind a file tab marks the chat tab with its notice", async ({ page, services }) => {
    const cutoff = await cutReply(page, "session_id");
    await open(page, services);
    await newDraft(page);
    await cutoff.arm();
    await prompt(page).fill("recover my prompt");
    await prompt(page).press("Enter");
    await cutoff.cut;
    await expect(prompt(page)).toHaveValue("");
    await showFile(page);
    const sends = rpcCalls(page, "session/send");
    await services.restartRelay();
    await expect(heading(page).getByRole("img")).toBeVisible();
    await heading(page).click();
    await expect(page.getByRole("main").getByRole("status")).toContainText("Session recovered");
    await expect(prompt(page)).toHaveValue("recover my prompt");
    expect(sends).toHaveLength(0);
  });
});

test.describe("on a wide screen", () => {
  test.use({ viewport: { width: 1600, height: 900 } });

  test.beforeEach(({ services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
    writeFileSync(join(services.workspace, "src", "b.txt"), "b\n");
  });

  const groups = (page: Page) => page.getByRole("region", { name: "Tab group" });
  // dropOn drops the tab or file row from onto the group at the fraction of its box.
  async function dropOn(source: Locator, group: Locator, at: { x: number; y: number }) {
    const box = await group.boundingBox();
    if (!box) throw new Error("no group");
    await source.dragTo(group, { targetPosition: { x: box.width * at.x, y: box.height * at.y } });
  }
  const right = { x: 0.9, y: 0.5 };
  const middle = { x: 0.5, y: 0.5 };

  test("a chat shown beside a file has no mark", async ({ ox: page }) => {
    await newSession(page, "configure me");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    const failed = await hold(page, "session/configure");
    await permissionMode(page).selectOption("plan");
    await failed.arrived;
    failed.fail("configuration refused");
    await expect(page.getByRole("main").getByRole("alert")).toHaveText("configuration refused");
    await expect(heading(page).getByRole("img")).toHaveCount(0);
  });

  test("the sessions toggle sits in the session list's header while it shows, and in the first tab strip while it is hidden", async ({
    ox: page,
  }) => {
    const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
    await newSession(page, "first");
    await expect(sessions(page).getByRole("button", { name: "Toggle sessions" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(groups(page).getByRole("button", { name: "Toggle sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(sessions(page)).toBeHidden();
    await expect(groups(page).first().getByRole("button", { name: "Toggle sessions" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await expect(page.getByRole("separator", { name: "Resize sessions" })).toHaveCount(0);
    await toggleSessions.click();
    await expect(sessions(page)).toBeVisible();
  });

  test("the panel toggle sits in the panel's header while it shows, and in the last tab strip while it is hidden", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await expect(files(page)).toHaveCount(0);
    await expect(groups(page).first().getByRole("button", { name: "Toggle panel" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await togglePanel(page).click();
    await expect(files(page).getByRole("button", { name: "Toggle panel" })).toHaveAttribute("aria-pressed", "true");
    await expect(groups(page).getByRole("button", { name: "Toggle panel" })).toHaveCount(0);
    await togglePanel(page).click();
    await expect(files(page)).toHaveCount(0);
  });

  test("each panel toggle stays in place as it moves between its panel's header and a tab strip", async ({
    ox: page,
  }) => {
    const toggleSessions = page.getByRole("button", { name: "Toggle sessions" });
    const at = async (button: Locator) => (await button.boundingBox())?.x;
    await newSession(page, "first");
    const sessionsShown = await at(toggleSessions);
    await toggleSessions.click();
    await expect(groups(page).getByRole("button", { name: "Toggle sessions" })).toBeVisible();
    expect(await at(toggleSessions)).toBe(sessionsShown);

    const panelHidden = await at(togglePanel(page));
    await togglePanel(page).click();
    await expect(files(page).getByRole("button", { name: "Toggle panel" })).toBeVisible();
    expect(await at(togglePanel(page))).toBe(panelHidden);
  });

  test("a long file name shortens its tab, which ends before the next tab starts", async ({ ox: page, services }) => {
    const long = `${"a-long-file-name-".repeat(4)}first.txt`;
    writeFileSync(join(services.workspace, "src", long), "first\n");
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, `src/${long}`);
    await entry(page, "b.txt").click();
    await expect(fileTab(page, "b.txt")).toHaveAttribute("aria-selected", "true");
    const close = await closeButton(page, long).boundingBox();
    const next = await fileTab(page, "b.txt").boundingBox();
    expect((close?.x ?? 0) + (close?.width ?? 0)).toBeLessThanOrEqual(next?.x ?? 0);
  });

  test("the panel lists the files or the changes, and the choice holds for every session", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await expect(files(page)).toBeVisible();
    await expect(panelMode(page, "Changes")).toHaveAttribute("aria-pressed", "false");
    await panelMode(page, "Changes").click();
    await expect(changesList(page)).toBeVisible();
    await expect(files(page)).toHaveCount(0);
    await newSession(page, "second");
    await expect(changesList(page)).toBeVisible();
    await expect(panelMode(page, "Changes")).toHaveAttribute("aria-pressed", "true");
  });

  test("a file opens as a tab in the focused group, opening it again shows its tab, and closing it removes the tab", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await showFiles(page);
    await expect(viewer(page)).toHaveCount(0);
    await openFile(page, "src/a.txt");
    await expect(viewer(page)).toBeVisible();
    await expect(pane(page)).toHaveText("a");
    await expect(closeButton(page, "first")).toHaveCount(0);
    await fileTab(page, "first").click();
    await expect(viewer(page)).toHaveCount(0);
    await expect(replies(page)).toHaveText(["You said: first"]);
    await entry(page, "a.txt").click();
    await expect(viewer(page)).toBeVisible();
    await expect(fileTab(page, "a.txt")).toHaveCount(1);
    await closeButton(page, "a.txt").click();
    await expect(fileTab(page, "a.txt")).toHaveCount(0);
    await expect(viewer(page)).toHaveCount(0);
    await expect(replies(page)).toHaveText(["You said: first"]);
  });

  test("the chat tab's tooltip is the session's directory, and a draft's chat tab says New session", async ({
    ox: page,
    services,
  }) => {
    await newDraft(page);
    await expect(fileTab(page, "New session")).toBeVisible();
    await expect(fileTab(page, "New session")).toHaveAccessibleDescription(services.workspace);
    await prompt(page).fill("first");
    await send(page).click();
    await expect(fileTab(page, "first")).toHaveAccessibleDescription(services.workspace);
  });

  test("dragging a file's tab to the right edge of its group splits the group", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await expect(groups(page)).toHaveCount(1);
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await expect(groups(page).first().getByRole("tab")).toHaveText(["first"]);
    await expect(groups(page).nth(1).getByRole("tab")).toHaveText(["a.txt"]);
    await expect(groups(page).nth(1).getByRole("tabpanel")).toHaveText("a");
  });

  test("the transcript keeps its scroll position when its group splits and when the split closes", async ({
    ox: page,
  }) => {
    await newSession(page, Array.from({ length: 90 }, (_, i) => `line${i}`).join("\n"));
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await page.getByRole("tab").first().click();
    await transcript(page).hover();
    await page.mouse.wheel(0, -5000);
    await expect(replies(page).last()).not.toBeInViewport();
    const offset = await transcript(page).evaluate((log) => log.scrollTop);
    expect(offset).toBeLessThan(-100);
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await page.getByRole("tab").first().click();
    await expect(transcript(page)).toHaveJSProperty("scrollTop", offset);
    await closeButton(page, "a.txt").click();
    await expect(groups(page)).toHaveCount(1);
    await expect(transcript(page)).toHaveJSProperty("scrollTop", offset);
  });

  test("dragging a tab to the middle of another group moves it there, and the emptied group disappears", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), middle);
    await expect(groups(page)).toHaveCount(1);
    await expect(groups(page).first().getByRole("tab")).toHaveText(["first", "a.txt"]);
    await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "true");
  });

  test("a group's only tab dropped on its own edge stays", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await dropOn(fileTab(page, "a.txt"), groups(page).nth(1), { x: 0.95, y: 0.5 });
    await expect(groups(page)).toHaveCount(2);
    await expect(groups(page).nth(1).getByRole("tab")).toHaveText(["a.txt"]);
  });

  test("dragging a file from the tree onto an edge opens it in a new group", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await entry(page, "src").click();
    await dropOn(entry(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await expect(groups(page).nth(1).getByRole("tab")).toHaveText(["a.txt"]);
    await dropOn(entry(page, "b.txt"), groups(page).first(), { x: 0.5, y: 0.95 });
    await expect(groups(page)).toHaveCount(3);
    await expect(groups(page).nth(1).getByRole("tab")).toHaveText(["b.txt"]);
  });

  test("the toggles move to the outer strips when groups split", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await sessions(page).getByRole("button", { name: "Toggle sessions" }).click();
    await expect(groups(page).first().getByRole("button", { name: "Toggle sessions" })).toBeVisible();
    await expect(groups(page).nth(1).getByRole("button", { name: "Toggle sessions" })).toHaveCount(0);
    await togglePanel(page).click();
    await expect(groups(page).nth(1).getByRole("button", { name: "Toggle panel" })).toBeVisible();
    await expect(groups(page).first().getByRole("button", { name: "Toggle panel" })).toHaveCount(0);
  });

  test("dragging a sash between groups resizes them within bounds", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    const [before, after] = [await width(groups(page).first()), await width(groups(page).nth(1))];
    await drag(page, "Resize groups", 100);
    await near(groups(page).first(), before + 100);
    await near(groups(page).nth(1), after - 100);
    await drag(page, "Resize groups", -2000);
    await near(groups(page).first(), 240);
    await drag(page, "Resize groups", 4000);
    await near(groups(page).nth(1), 240);
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
    await drag(page, "Resize panel", -100);
    await near(tree, treeStart + 100);

    // At a narrow window both side panels together leave the transcript its
    // minimum.
    await page.setViewportSize({ width: 960, height: 900 });
    await drag(page, "Resize sessions", 1000);
    await drag(page, "Resize panel", -1000);
    expect(await width(page.getByRole("main"))).toBeGreaterThanOrEqual(240);
  });

  test("at a 960 px window the session list's sash leaves the tab area 240 px beside the panel", async ({
    ox: page,
  }) => {
    await page.setViewportSize({ width: 960, height: 900 });
    await newDraft(page);
    await showFiles(page);
    await drag(page, "Resize sessions", 2000);
    await near(page.getByRole("main"), 240);
    await expect(files(page)).toBeInViewport({ ratio: 1 });
  });

  test("panel widths are page-wide, and whether the panel shows is too", async ({ ox: page }) => {
    const nav = sessions(page);
    await newSession(page, "first");
    await showFiles(page);
    await newSession(page, "second");
    await sessionButton(page, "first").click();
    await drag(page, "Resize sessions", 100);
    await drag(page, "Resize panel", -100);
    const [navWidth, treeWidth] = [await width(nav), await width(files(page))];

    await sessionButton(page, "second").click();
    await near(nav, navWidth);
    await near(files(page), treeWidth);
    await sessionButton(page, "first").click();
    await near(nav, navWidth);
    await near(files(page), treeWidth);

    await togglePanel(page).click();
    await sessionButton(page, "second").click();
    await expect(files(page)).toHaveCount(0);
  });

  test("each session and draft keeps its tabs and groups, and a draft's carry to its session", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFiles(page);
    await newSession(page, "second");
    await sessionButton(page, "first").click();
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await sessionButton(page, "second").click();
    await expect(groups(page)).toHaveCount(1);
    await expect(viewer(page)).toHaveCount(0);
    await openFile(page, "src/b.txt");
    await sessionButton(page, "first").click();
    await expect(groups(page)).toHaveCount(2);
    await expect(fileTab(page, "a.txt")).toBeVisible();
    await expect(fileTab(page, "b.txt")).toHaveCount(0);
    await sessionButton(page, "second").click();
    await expect(groups(page)).toHaveCount(1);
    await expect(fileTab(page, "b.txt")).toBeVisible();

    await newDraft(page);
    await expect(fileTab(page, "b.txt")).toHaveCount(0);
    await openFile(page, "src/a.txt");
    await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
    await expect(groups(page)).toHaveCount(2);
    await sessionButton(page, "second").click();
    await newDraft(page);
    await expect(groups(page)).toHaveCount(2);
    await fileTab(page, "New session").click();
    await prompt(page).fill("third");
    await send(page).click();
    await expect(replies(page)).toHaveText(["You said: third"]);
    await expect(sessionButton(page, "third")).toHaveAttribute("aria-current", "page");
    await expect(groups(page)).toHaveCount(2);
    await expect(fileTab(page, "a.txt")).toBeVisible();
    await expect(pane(page)).toHaveText("a");
  });

  test("a reload closes the panel and the tabs, and restores the default widths", async ({ ox: page }) => {
    const nav = sessions(page);
    await newSession(page, "first");
    await showFiles(page);
    await openFile(page, "src/a.txt");
    const first = [await width(nav), await width(files(page))];
    await drag(page, "Resize sessions", 100);
    await drag(page, "Resize panel", -100);
    await page.reload();
    await expect(replies(page)).toHaveText(["You said: first"]);
    await expect(files(page)).toHaveCount(0);
    await expect(fileTab(page, "a.txt")).toHaveCount(0);
    await showFiles(page);
    await near(nav, first[0]);
    await near(files(page), first[1]);
  });
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test.beforeEach(({ services }) => {
    mkdirSync(join(services.workspace, "src"));
    writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  });

  const toggleSessions = (page: Page) => page.getByRole("button", { name: "Toggle sessions" });
  const bar = (page: Page) => page.getByRole("toolbar", { name: "Tabs" });
  const picker = (page: Page, name: string) => bar(page).getByRole("button", { name, exact: true });
  const tabMenu = (page: Page) => page.getByRole("menu", { name: "Tabs" });
  // tapBeside taps the page just past the drawer's edge on the side given.
  async function tapBeside(page: Page, drawer: Locator, side: "left" | "right") {
    // A trial tap waits for the drawer to finish sliding in.
    await drawer.tap({ trial: true });
    const box = await drawer.boundingBox();
    if (!box) throw new Error("the drawer is not open");
    const x = side === "right" ? box.x + box.width + 10 : box.x - 10;
    await page.touchscreen.tap(x, box.y + box.height / 2);
  }
  // showFile opens src/a.txt from the panel's drawer, which closes.
  async function showFile(page: Page) {
    await bar(page).getByRole("button", { name: "Toggle panel" }).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(viewer(page)).toBeVisible();
  }

  test("an approval behind a file marks the tab picker", async ({ ox: page }) => {
    await newSession(page, "earlier prompt");
    const sent = await hold(page, "session/send");
    await prompt(page).fill("approve Bash {}");
    await send(page).tap();
    await sent.arrived;
    await showFile(page);
    sent.release();
    await expect(bar(page).getByRole("img", { name: "Waiting on you" })).toBeVisible();
    await bar(page)
      .getByRole("button", { name: /^a\.txt/ })
      .tap();
    await expect(tabMenu(page).getByRole("img", { name: "Waiting on you" })).toBeVisible();
  });

  test("a lost connection shows above an open file", async ({ ox: page, services }) => {
    await newSession(page, "hello");
    await showFile(page);
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(viewer(page)).toBeVisible();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
  });

  test("the session list opens at load only when the URL names no session", async ({ ox: page }) => {
    await expect(sessions(page)).toBeVisible();
    await expect(toggleSessions(page).last()).toHaveAttribute("aria-pressed", "true");
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

  test("the panel drawer closes on tapping outside, from its toggle by keyboard, or on opening a file", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await bar(page).getByRole("button", { name: "Toggle panel" }).tap();
    await expect(files(page)).toBeVisible();
    await tapBeside(page, files(page), "left");
    await expect(files(page)).toHaveCount(0);
    await bar(page).getByRole("button", { name: "Toggle panel" }).tap();
    await expect(files(page)).toBeVisible();
    await files(page).getByRole("button", { name: "Toggle panel" }).press("Enter");
    await expect(files(page)).toHaveCount(0);
    await bar(page).getByRole("button", { name: "Toggle panel" }).tap();
    await entry(page, "src").tap();
    await entry(page, "a.txt").tap();
    await expect(files(page)).toHaveCount(0);
    await expect(viewer(page)).toBeVisible();
    await expect(page.getByRole("main")).toBeHidden();
  });

  test("the picker shows the active tab, its menu lists every tab, and choosing one shows it", async ({ ox: page }) => {
    await newSession(page, "first");
    await expect(picker(page, "first")).toBeVisible();
    await showFile(page);
    await expect(picker(page, "a.txt")).toBeVisible();
    await expect(page.getByRole("tab")).toHaveCount(0);
    await bar(page)
      .getByRole("button", { name: /^a\.txt/ })
      .tap();
    await expect(tabMenu(page)).toMatchAriaSnapshot(`
      - menu "Tabs":
        - menuitemradio "first"
        - menuitemradio "a.txt" [checked]
    `);
    await tabMenu(page).getByRole("menuitemradio", { name: "first" }).tap();
    await expect(tabMenu(page)).toBeHidden();
    await expect(page.getByRole("main")).toBeVisible();
    await expect(viewer(page)).toHaveCount(0);
    await expect(replies(page)).toHaveText(["You said: first"]);
    await expect(picker(page, "first")).toBeVisible();
  });

  test("the menu's close button closes a tab, and the chat tab has none", async ({ ox: page }) => {
    await newSession(page, "first");
    await showFile(page);
    await bar(page)
      .getByRole("button", { name: /^a\.txt/ })
      .tap();
    await expect(closeButton(page, "first")).toHaveCount(0);
    await closeButton(page, "a.txt").tap();
    await expect(viewer(page)).toHaveCount(0);
    await expect(picker(page, "first")).toBeVisible();
    await expect(page.getByRole("main")).toBeVisible();
  });

  test("tabs cannot be dragged and panels cannot be resized", async ({ ox: page }) => {
    // A hidden separator is found only with includeHidden.
    const separator = (name: string) => page.getByRole("separator", { name, includeHidden: true });
    await expect(separator("Resize sessions")).toHaveCount(1);
    await expect(separator("Resize sessions")).toBeHidden();
    await newSession(page, "first");
    await bar(page).getByRole("button", { name: "Toggle panel" }).tap();
    await expect(files(page)).toBeVisible();
    await expect(separator("Resize panel")).toHaveCount(0);
    await entry(page, "src").tap();
    await expect(entry(page, "a.txt")).not.toHaveAttribute("draggable", "true");
    await expect(separator("Resize groups")).toHaveCount(0);
  });

  test("crossing the breakpoint keeps the tabs, closes the drawer, and leaves the wide panel as it was", async ({
    ox: page,
  }) => {
    await newSession(page, "first");
    await showFile(page);
    await page.setViewportSize({ width: 1600, height: 844 });
    await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "true");
    await expect(files(page)).toHaveCount(0);
    await togglePanel(page).click();
    await expect(files(page)).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(files(page)).toHaveCount(0);
    await expect(picker(page, "a.txt")).toBeVisible();
    await expect(viewer(page)).toBeVisible();
    await page.setViewportSize({ width: 1600, height: 844 });
    await expect(files(page)).toBeVisible();
    await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "true");
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

test("clicking a file opens its tab and marks its row, and another file opens beside it", async ({
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
  await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "true");
  await expect(fileTab(page, "a.txt")).toHaveAccessibleDescription("src/a.txt");
  await read.arrived;
  await expect(pane(page)).toHaveText("");
  read.release();
  await expect(pane(page)).toHaveText("first");
  await expect(entry(page, "a.txt")).toHaveAttribute("aria-current", "true");
  await expect(wrapButton(page)).toBeVisible();
  await expect(modes(page)).toHaveCount(0);

  await entry(page, "b.txt").click();
  await expect(fileTab(page, "b.txt")).toHaveAttribute("aria-selected", "true");
  await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "false");
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

    await newSessionButton(page).click();
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
  await expect(fileTab(page, "a.txt")).toHaveAttribute("aria-selected", "true");
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
  await expect(fileTab(page, "template.ts")).toHaveAttribute("aria-selected", "true");
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
  await expect(fileTab(page, "a.md")).toHaveAttribute("aria-selected", "true");
  await expect(modeButton(page, "Source")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toHaveText("# A");

  await entry(page, "a.md").click();
  await expect(modeButton(page, "Source")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page)).toHaveText("# A");

  await openFile(page, "b.md");
  await expect(modeButton(page, "Preview")).toHaveAttribute("aria-pressed", "true");
  await expect(pane(page).getByRole("heading", { name: "B" })).toBeVisible();
});

test("a changed Markdown file offers Source and Preview and opens rendered", async ({ ox: page, services }) => {
  writeFileSync(join(services.workspace, "notes.md"), "# Title\n");
  gitRepo(services.workspace);
  writeFileSync(join(services.workspace, "notes.md"), "# Changed\n");
  await newDraft(page);
  await showFiles(page);
  await openFile(page, "notes.md");
  await expect(modes(page)).toMatchAriaSnapshot(`
    - group "Show as":
      - button "Source"
      - button "Preview" [pressed]
  `);
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
    await expect(fileTab(page, "bin.dat")).toHaveAttribute("aria-selected", "true");
    await read.arrived;
    read.fail("relay unavailable");
    await expect(pane(page)).toHaveText("relay unavailable");
    await page.clock.runFor(1000);
    await expect(pane(page)).toHaveText("file is binary");
  },
);

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
  await expect(fileTab(page, "a.txt")).toHaveCount(0);
  await expect(viewer(page)).toHaveCount(0);
});
