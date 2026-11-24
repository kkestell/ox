// The specs' fixtures and page helpers. Each test runs its own oxd and relay
// through the services fixture, with the configuration the config option
// names.
import { basename } from "node:path";
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

// newSession sends text as the first prompt of a new session and waits for
// its reply.
export async function newSession(page: Page, text: string) {
  await newSessionButton(page).click();
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(replies(page).last()).toHaveText(`You said: ${text}`);
  await expect(stop(page)).toHaveCount(0);
}

// ask sends a prompt that has the fake harness ask to approve a call of tool
// with inputJSON, and returns the approval.
export async function ask(page: Page, tool: string, inputJSON: string) {
  await prompt(page).fill(`approve ${tool} ${inputJSON}`);
  await prompt(page).press("Enter");
  const approval = waiting(page).getByRole("article");
  await expect(approval).toHaveCount(1);
  return approval;
}

// allow answers an approval with Allow.
export async function allow(approval: Locator) {
  await approval.getByRole("radio", { name: "Allow" }).check();
  await approval.getByRole("button", { name: "Submit" }).click();
}

// setSafeArea gives the page a phone's safe-area insets, in px.
export async function setSafeArea(page: Page, insets: { top: number; bottom: number }) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setSafeAreaInsetsOverride", { insets });
}

// openKeyboard reports an on-screen keyboard height px tall through the visual
// viewport, with the page panned down by offsetTop and zoomed by scale, as
// Safari does when the keyboard opens. closeKeyboard reports it closed.
export async function openKeyboard(page: Page, keyboard: { height: number; offsetTop: number; scale?: number }) {
  await page.evaluate(({ height, offsetTop, scale = 1 }) => {
    const viewport = window.visualViewport!;
    Object.defineProperty(viewport, "height", { configurable: true, get: () => window.innerHeight - height });
    Object.defineProperty(viewport, "offsetTop", { configurable: true, get: () => offsetTop });
    Object.defineProperty(viewport, "scale", { configurable: true, get: () => scale });
    viewport.dispatchEvent(new Event("resize"));
  }, keyboard);
}

// panKeyboard reports the page panned to offsetTop with the keyboard open,
// through a scroll event alone, as Safari does when focus moves while the
// keyboard is up.
export async function panKeyboard(page: Page, offsetTop: number) {
  await page.evaluate((offsetTop) => {
    const viewport = window.visualViewport!;
    Object.defineProperty(viewport, "offsetTop", { configurable: true, get: () => offsetTop });
    viewport.dispatchEvent(new Event("scroll"));
  }, offsetTop);
}

export async function closeKeyboard(page: Page) {
  await page.evaluate(() => {
    const viewport = window.visualViewport! as unknown as Record<string, unknown>;
    for (const key of ["height", "offsetTop", "scale"]) delete viewport[key];
    window.visualViewport!.dispatchEvent(new Event("resize"));
  });
}

// edges are the top and bottom of an element's box on the page, in whole px.
export async function edges(locator: Locator) {
  const box = await locator.boundingBox();
  if (!box) throw new Error("the element is not visible");
  return { top: Math.round(box.y), bottom: Math.round(box.y + box.height) };
}

// expectWithin waits until locator lies between top and bottom on the page,
// such as the area a phone's keyboard leaves visible.
export async function expectWithin(locator: Locator, area: { top: number; bottom: number }) {
  await expect(async () => {
    const { top, bottom } = await edges(locator);
    expect(top).toBeGreaterThanOrEqual(area.top);
    expect(bottom).toBeLessThanOrEqual(area.bottom);
  }).toPass();
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
