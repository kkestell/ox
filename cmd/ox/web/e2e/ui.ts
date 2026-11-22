// Page helpers shared by the specs.
import { expect, type Locator, type Page, type Route } from "@playwright/test";
import type { Services } from "./services";

// open loads the page and adds the services' workspace once.
export async function open(page: Page, services: Services) {
  await page.goto(services.url);
  if (!(await page.getByTitle(services.workspace).count())) {
    await page.getByRole("button", { name: "Add workspace" }).click();
    await page.getByPlaceholder("/absolute/path/to/directory").fill(services.workspace);
    await page.getByRole("button", { name: "Add", exact: true }).click();
  }
  await expect(page.getByTitle(services.workspace)).toBeVisible();
}

export const prompt = (page: Page) => page.getByRole("textbox", { name: "Prompt" });
export const send = (page: Page) => page.getByRole("button", { name: "Send" });
export const stop = (page: Page) => page.getByRole("button", { name: "Stop" });
export const sessionButton = (page: Page, title: string) => page.locator("nav button.open").filter({ hasText: title });
export const sessionRow = (page: Page, title: string) => page.locator("nav li").filter({ hasText: title });
export const workspaceRow = (page: Page, path: string) => page.getByTitle(path, { exact: true });

// toolResult is the result text of the last call of tool.
export const toolResult = (page: Page, tool: string) =>
  page
    .locator("details.tool")
    .filter({ has: page.locator(".name", { hasText: tool }) })
    .last()
    .locator(".card pre")
    .last();

// openMenu opens a sidebar row's menu with its ⋯ button and returns the menu.
export async function openMenu(row: Locator) {
  await row.getByRole("button", { name: /actions$/ }).click();
  const menu = row.page().getByRole("menu");
  await expect(menu).toBeVisible();
  return menu;
}

// menuItem is a menu's item whose label starts with label; a disabled item's
// name ends with its reason.
export const menuItem = (menu: Locator, label: string) => menu.getByRole("menuitem", { name: new RegExp(`^${label}`) });

// hold intercepts the page's next request for method until release passes it
// on (optionally with changed parameters), or fail answers it as a relay error.
// Only a request whose parameters include every key of params is held.
export async function hold(page: Page, method: string, params?: Record<string, unknown>) {
  let release!: (changes?: Record<string, unknown>) => void;
  let fail!: (message: string) => void;
  const answer = new Promise<(route: Route) => Promise<void>>((resolve) => {
    release = (changes) =>
      resolve((route) => {
        if (!changes) return route.continue();
        const request = route.request().postDataJSON();
        return route.continue({ postData: { ...request, params: { ...request.params, ...changes } } });
      });
    fail = (message) => resolve((route) => route.fulfill({ status: 502, body: message }));
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
  return { arrived, release, fail };
}
