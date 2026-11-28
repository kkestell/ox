import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Locator, Page } from "@playwright/test";
import { dropOn, entry, expect, files, fileTab, groups, newSession, openFile, sessions, showFiles, test } from "./ui";

// Each test builds a layout of tab groups on a wide screen, then walks the
// widths and the panels and drawers each width offers, and checks that every
// tab control and panel toggle shown is on screen and can be clicked or
// tapped.

type Layout = { name: string; build(page: Page): Promise<void> };

const right = { x: 0.9, y: 0.5 };
const bottom = { x: 0.5, y: 0.95 };

async function oneGroup(page: Page) {
  await newSession(page, "first");
  await showFiles(page);
  await openFile(page, "src/a.txt");
  await expect(groups(page)).toHaveCount(1);
}

async function twoGroups(page: Page) {
  await oneGroup(page);
  await dropOn(fileTab(page, "a.txt"), groups(page).first(), right);
  await expect(groups(page)).toHaveCount(2);
}

async function nestedGroups(page: Page) {
  await twoGroups(page);
  await entry(page, "b.txt").click();
  await expect(groups(page).nth(1).getByRole("tab")).toHaveText(["a.txt", "b.txt"]);
  await dropOn(fileTab(page, "b.txt"), groups(page).nth(1), bottom);
  await expect(groups(page)).toHaveCount(3);
}

const layouts: Layout[] = [
  { name: "one tab group", build: oneGroup },
  { name: "two tab groups", build: twoGroups },
  { name: "nested tab groups", build: nestedGroups },
];

// reachable checks that a control is on screen and takes a click or tap.
async function reachable(control: Locator, touch: boolean) {
  if (touch) await control.tap({ trial: true });
  else await control.click({ trial: true });
  await expect(control).toBeInViewport();
}

// press clicks or taps a control.
async function press(control: Locator, touch: boolean) {
  if (touch) await control.tap();
  else await control.click();
}

const toggle = (scope: Page | Locator, name: "sessions" | "panel") =>
  scope.getByRole("button", { name: `Toggle ${name}` }).filter({ visible: true });

// shownOrHidden shows or hides a wide layout's panel with its toggle.
async function shownOrHidden(page: Page, name: "sessions" | "panel", shown: boolean) {
  const button = toggle(page, name);
  if ((await button.getAttribute("aria-pressed")) !== String(shown)) await button.click();
  await expect(toggle(page, name)).toHaveAttribute("aria-pressed", String(shown));
}

// wideControlsReachable checks every tab, its close button, and every panel
// toggle of a wide layout.
async function wideControlsReachable(page: Page) {
  for (const group of await groups(page).all()) {
    for (const tab of await group.getByRole("tab").all()) await reachable(tab, false);
    for (const close of await group.getByRole("button", { name: /^Close / }).all()) await reachable(close, false);
  }
  for (const button of await page
    .getByRole("button", { name: /^Toggle (sessions|panel)$/ })
    .filter({ visible: true })
    .all()) {
    await reachable(button, false);
  }
}

// narrowControlsReachable checks every button in the narrow layout's tab bar,
// every tab in its menu with its close button, and each drawer's toggles.
async function narrowControlsReachable(page: Page, touch: boolean) {
  const bar = page.getByRole("toolbar", { name: "Tabs" });
  for (const button of await bar.getByRole("button").all()) await reachable(button, touch);
  // The tab picker is the bar's one button besides the toggles.
  await press(bar.getByRole("button", { name: /^(?!Toggle )/ }), touch);
  const menu = page.getByRole("menu", { name: "Tabs" });
  await expect(menu).toBeVisible();
  for (const item of await menu.getByRole("menuitemradio").all()) await reachable(item, touch);
  for (const close of await menu.getByRole("button", { name: /^Close / }).all()) await reachable(close, touch);
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();

  for (const [name, drawer] of [
    ["sessions", sessions(page)],
    ["panel", files(page)],
  ] as const) {
    await press(toggle(bar, name), touch);
    await expect(drawer).toBeInViewport({ ratio: 1 });
    for (const button of await drawer
      .getByRole("button", { name: /^Toggle (sessions|panel)$/ })
      .filter({ visible: true })
      .all()) {
      await reachable(button, touch);
    }
    await press(toggle(drawer, name), touch);
    await expect(drawer).toBeHidden();
  }
}

test.beforeEach(({ services }) => {
  mkdirSync(join(services.workspace, "src"));
  writeFileSync(join(services.workspace, "src", "a.txt"), "a\n");
  writeFileSync(join(services.workspace, "src", "b.txt"), "b\n");
});

test.describe("on a desktop", () => {
  test.use({ viewport: { width: 1600, height: 900 } });

  for (const layout of layouts) {
    test(`every control is reachable with ${layout.name}`, async ({ ox: page }) => {
      await layout.build(page);
      for (const width of [1600, 960]) {
        await page.setViewportSize({ width, height: 900 });
        for (const sessionsShown of [true, false]) {
          for (const panelShown of [true, false]) {
            await shownOrHidden(page, "sessions", sessionsShown);
            await shownOrHidden(page, "panel", panelShown);
            await wideControlsReachable(page);
          }
        }
      }
      await page.setViewportSize({ width: 390, height: 844 });
      await narrowControlsReachable(page, false);
    });
  }
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  for (const layout of layouts) {
    test(`every control is reachable with ${layout.name}`, async ({ ox: page }) => {
      await page.setViewportSize({ width: 1600, height: 900 });
      await layout.build(page);
      await page.setViewportSize({ width: 390, height: 844 });
      await narrowControlsReachable(page, true);
    });
  }
});
