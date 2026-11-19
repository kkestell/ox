// Page helpers shared by the specs.
import { expect, type Page, type Route } from "@playwright/test";
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

// hold intercepts the page's next request for method until release passes it
// on, or fail answers it as a relay error.
export async function hold(page: Page, method: string) {
  let release!: () => void;
  let fail!: (message: string) => void;
  const answer = new Promise<(route: Route) => Promise<void>>((resolve) => {
    release = () => resolve((route) => route.continue());
    fail = (message) => resolve((route) => route.fulfill({ status: 502, body: message }));
  });
  let held!: () => void;
  const arrived = new Promise<void>((resolve) => {
    held = resolve;
  });
  let taken = false;
  await page.route("**/rpc*", async (route) => {
    if (taken || route.request().postDataJSON()?.method !== method) return route.fallback();
    taken = true;
    held();
    await (await answer)(route);
  });
  return { arrived, release, fail };
}
