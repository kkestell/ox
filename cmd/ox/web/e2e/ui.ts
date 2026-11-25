// The specs' fixtures and page helpers. Each test runs its own oxd and relay
// through the services fixture, with the configuration the config option
// names.
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { devNull } from "node:os";
import { basename, join } from "node:path";
import { test as base, expect, type Locator, type Page, type Route } from "@playwright/test";
import { fakeConfig, type Services, startServices } from "./services";

export { expect };

export const test = base.extend<{ config: object; services: Services; ox: Page }>({
  config: [fakeConfig, { option: true }],
  services: async ({ config }, use) => {
    const services = await startServices(config);
    await use(services);
    await services.stop();
  },
  // ox is the page loaded with the services' workspace added.
  ox: async ({ page, services }, use) => {
    await page.goto(services.url);
    await addWorkspace(page, services.workspace);
    await use(page);
  },
});

// clocked runs the page under Playwright's clock, paused once the page has
// loaded, so the page's clock ticks only on runFor(1000).
const start = new Date("2026-01-01T00:00:00Z");
export const clocked = test.extend({
  page: async ({ page }, use) => {
    await page.clock.install({ time: start });
    await use(page);
  },
  ox: async ({ ox }, use) => {
    await ox.clock.pauseAt(new Date(start.getTime() + 3_600_000));
    await use(ox);
  },
});

// addWorkspace adds the directory at path through the Add workspace dialog.
export async function addWorkspace(page: Page, path: string) {
  await page.getByRole("button", { name: "Add workspace" }).click();
  const dialog = page.getByRole("dialog", { name: "Add workspace" });
  await dialog.getByRole("textbox", { name: "Directory" }).fill(path);
  await dialog.getByRole("button", { name: "Add", exact: true }).click();
  await expect(workspaceSection(page, path)).toBeVisible();
}

export const prompt = (page: Page) => page.getByRole("textbox", { name: "Prompt" });
export const send = (page: Page) => page.getByRole("button", { name: "Send", exact: true });
export const stop = (page: Page) => page.getByRole("button", { name: "Stop", exact: true });
export const newSessionButton = (page: Page) => page.getByRole("button", { name: "New session" });
// The model picker is named "Model" followed by what it shows.
export const model = (page: Page) => page.getByRole("main").getByRole("button", { name: /^Model\b/ });
export const permissionMode = (page: Page) => page.getByRole("combobox", { name: "Permission mode" });
export const heading = (page: Page) => page.getByRole("main").getByRole("heading", { level: 1 });

export const transcript = (page: Page) => page.getByRole("log", { name: "Transcript" });
// prompts holds the transcript's prompts, pending ones included.
export const prompts = (page: Page) => transcript(page).getByRole("article", { name: "Prompt" });
export const replies = (page: Page) => transcript(page).getByRole("article", { name: "Reply" });
export const errors = (page: Page) => transcript(page).getByRole("article", { name: "Error" });
export const pending = (page: Page) => transcript(page).getByRole("region", { name: "Pending" });
// toolCall is the last call of tool in the transcript.
export const toolCall = (page: Page, tool: string) =>
  transcript(page).getByRole("group", { name: tool, exact: true }).last();
export const waiting = (page: Page) => page.getByRole("region", { name: "Waiting on you" });

export const sessions = (page: Page) => page.getByRole("navigation", { name: "Sessions" });
// workspaceSection is the session list's section for the workspace at path.
export const workspaceSection = (page: Page, path: string) =>
  sessions(page).getByRole("region", { name: basename(path) || path, exact: true });
export const sessionButton = (page: Page, title: string) =>
  sessions(page).getByRole("button", { name: title, exact: true });
export const sessionItem = (page: Page, title: string) =>
  sessions(page)
    .getByRole("listitem")
    .filter({ has: page.getByRole("button", { name: title, exact: true }) });

// pixel is a 1×1 PNG.
export const pixel = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

export type Picked = { name: string; mimeType: string; buffer: Buffer };

// pickFiles attaches files through the Attach Image button's file picker.
export async function pickFiles(page: Page, files: Picked[]) {
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("main").getByRole("button", { name: "Attach Image" }).click();
  await (await chooser).setFiles(files);
}

// attachPixel attaches pixel as name through the file picker.
export const attachPixel = (page: Page, name = "pixel.png") =>
  pickFiles(page, [{ name, mimeType: "image/png", buffer: pixel }]);

// thumbnail is the composer's thumbnail of the image attached as name.
export const thumbnail = (page: Page, name = "pixel.png") =>
  page.getByRole("main").getByRole("img", { name, exact: true });

// openMenu opens the menu of a session's item or a workspace's section with
// its ⋯ button, the first in either, and returns the menu.
export async function openMenu(scope: Locator) {
  await scope
    .getByRole("button", { name: /^(Session|Workspace) actions$/ })
    .first()
    .click();
  const menu = scope.page().getByRole("menu");
  await expect(menu).toBeVisible();
  return menu;
}

// menuItem is a menu's item named label. A disabled item's reason is its
// description.
export const menuItem = (menu: Locator, label: string) => menu.getByRole("menuitem", { name: label, exact: true });

// location is the new-session draft's choice of where the session runs.
export const location = (page: Page) => page.getByRole("combobox", { name: "Location" });

// gitRepo makes dir a git repository whose one commit holds committed.txt,
// beside the untracked file untracked.txt, with git kept from the user's own
// configuration.
export function gitRepo(dir: string) {
  const env = {
    ...process.env,
    GIT_CONFIG_GLOBAL: devNull,
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_AUTHOR_NAME: "Ox",
    GIT_AUTHOR_EMAIL: "ox@example.com",
    GIT_COMMITTER_NAME: "Ox",
    GIT_COMMITTER_EMAIL: "ox@example.com",
  };
  writeFileSync(join(dir, "committed.txt"), "committed\n");
  for (const args of [
    ["init", "-q"],
    ["add", "."],
    ["commit", "-q", "-m", "init"],
  ]) {
    execFileSync("git", ["-C", dir, ...args], { env });
  }
  writeFileSync(join(dir, "untracked.txt"), "");
}

// newSession sends text as the first prompt of a new session and waits for
// its reply.
export async function newSession(page: Page, text: string) {
  await newSessionButton(page).click();
  await prompt(page).fill(text);
  await send(page).click();
  await expect(replies(page).last()).toHaveText(`You said: ${text}`);
  await expect(stop(page)).toHaveCount(0);
}

// ask sends a prompt that has the fake harness ask to approve a call of tool
// with inputJSON, and returns the approval.
export async function ask(page: Page, tool: string, inputJSON: string) {
  await prompt(page).fill(`approve ${tool} ${inputJSON}`);
  await send(page).click();
  const approval = waiting(page).getByRole("article");
  await expect(approval).toHaveCount(1);
  return approval;
}

// allow answers an approval with Allow.
export async function allow(approval: Locator) {
  await approval.getByRole("radio", { name: "Allow" }).check();
  await approval.getByRole("button", { name: "Submit" }).click();
}

// showPage fires the event a browser sends when the page is shown again.
export const showPage = (page: Page) => page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));

// setSafeArea gives the page a phone's safe-area insets, in px.
export async function setSafeArea(page: Page, insets: { top: number; bottom: number }) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setSafeAreaInsetsOverride", { insets });
}

// sessionId is the ID of the session the page shows.
export const sessionId = (page: Page) => decodeURIComponent(new URL(page.url()).hash.slice(1));

// rpcCalls collects the params of each request for method the page posts
// from now on.
export function rpcCalls(page: Page, method: string) {
  const calls: Record<string, unknown>[] = [];
  page.on("request", (request) => {
    if (request.method() !== "POST" || !request.url().includes("/rpc")) return;
    const body = request.postDataJSON();
    if (body?.method === method) calls.push(body.params);
  });
  return calls;
}

// isRequest tells whether a request posts method.
export const isRequest = (method: string) => (request: { url(): string; postDataJSON(): { method?: string } }) =>
  request.url().includes("/rpc") && request.postDataJSON()?.method === method;

// hold intercepts the page's next request for method until release passes it
// on (optionally with changed parameters), fail answers it as a relay error, or
// abort cuts it off as a network error. It holds only a request whose
// parameters have each of params's keys with an equal value.
export async function hold(page: Page, method: string, params?: Record<string, unknown>) {
  let release!: (changes?: Record<string, unknown>) => void;
  let fail!: (message: string) => void;
  let abort!: () => void;
  const answer = new Promise<(route: Route) => Promise<void>>((resolve) => {
    release = (changes) =>
      resolve((route) => {
        if (!changes) return route.continue();
        const request = route.request().postDataJSON();
        return route.continue({ postData: { ...request, params: { ...request.params, ...changes } } });
      });
    fail = (message) => resolve((route) => route.fulfill({ status: 502, body: message }));
    abort = () => resolve((route) => route.abort());
  });
  let held!: () => void;
  const arrived = new Promise<void>((resolve) => {
    held = resolve;
  });
  let taken = false;
  await page.route("**/rpc*", async (route) => {
    const request = route.request().postDataJSON();
    const matches = Object.entries(params ?? {}).every(([key, value]) => request?.params?.[key] === value);
    if (taken || request?.method !== method || !matches) return route.fallback();
    taken = true;
    held();
    await (await answer)(route);
  });
  return { arrived, release, fail, abort };
}

declare global {
  interface Window {
    cutNextReply(): void;
    replyWithheld(): void;
    replyExisting(): void;
  }
}

// cutReply withholds, once armed, the next accepted reply whose result has
// field, at the page's EventSource, leaving its call pending. cut resolves
// once a reply is withheld, and existing once a reply reports an existing
// session, withheld or not. Call it before the page loads.
export async function cutReply(page: Page, field: "session_id" | "message_id") {
  let withheld!: () => void;
  const cut = new Promise<void>((resolve) => {
    withheld = resolve;
  });
  let foundExisting!: () => void;
  const existing = new Promise<void>((resolve) => {
    foundExisting = resolve;
  });
  await page.exposeBinding("replyWithheld", () => withheld());
  await page.exposeBinding("replyExisting", () => foundExisting());
  await page.addInitScript((field) => {
    let armed = false;
    window.cutNextReply = () => {
      armed = true;
    };
    const Original = window.EventSource;
    window.EventSource = class extends Original {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        this.addEventListener("message", (event) => {
          const result = JSON.parse(event.data).result;
          if (result === undefined) return;
          if (result?.existing) void window.replyExisting();
          if (armed && result?.[field]) {
            armed = false;
            event.stopImmediatePropagation();
            void window.replyWithheld();
          }
        });
      }
    };
  }, field);
  return {
    arm: () => page.evaluate(() => window.cutNextReply()),
    cut,
    existing,
  };
}
