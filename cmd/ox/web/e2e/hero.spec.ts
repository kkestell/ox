// Plays docs/screenshots/hero.json through oxd, the relay, and the page, with
// oxd's scene player in place of Claude and Ur, and captures the README's
// screenshots in docs/images. It runs only with OX_SCREENSHOTS=1, as make
// screenshots does.
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { devices, type Page } from "@playwright/test";
import type { Services } from "./services";
import { expect, gitEnv, menuItem, openMenu, sessionButton, test, transcript, waiting, workspaceSection } from "./ui";

type Turn = { end: "idle" | "working" | "waiting"; reply?: string; updates?: { type: string; text?: string }[] };
type Child = {
  name: string;
  title: string;
  harness: string;
  model: string;
  effort: string;
  message: string;
  turns: Turn[];
};
type Session = {
  title: string;
  harness: string;
  model: string;
  effort: string;
  permission_mode: string;
  worktree?: boolean;
  parked?: boolean;
  archived?: boolean;
  prompt: string;
  turns: Turn[];
  children?: Child[];
};
type Scene = {
  workspaces: { repo: string; sessions: Session[] }[];
  desktop: { session: string; file: string };
  phone: { session: string };
};

const root = resolve(import.meta.dirname, "../../../..");
const scenePath = join(root, "docs/screenshots/hero.json");
const scene: Scene = JSON.parse(readFileSync(scenePath, "utf8"));
const config = JSON.parse(readFileSync(join(root, "internal/config/default.json"), "utf8"));

test.skip(!process.env.OX_SCREENSHOTS, "set OX_SCREENSHOTS=1 to capture the README's screenshots");
test.setTimeout(180_000);
test.use({ config, scene: scenePath });

// The session state each turn's end leaves.
const states = { idle: "idle", working: "running", waiting: "requires_action" };

// settle waits until the session has taken n inputs and is in the state turn
// n ends in.
async function settle(services: Services, id: string, label: string, turns: Turn[], n: number) {
  const state = states[turns[n - 1].end];
  await expect
    .poll(
      async () => {
        const session = await services.request<{ state: string; history: { type: string }[] }>("session/get", {
          session_id: id,
        });
        const prompts = session.history.filter((u) => u.type === "prompt").length;
        return prompts === n && session.state === state;
      },
      { message: `${label} has taken ${n} inputs and is ${state}`, timeout: 15_000, intervals: [100] },
    )
    .toBe(true);
}

// lastText is the start of the last text a session's turns show, as the page
// renders it.
function lastText(turns: Turn[]) {
  for (const turn of turns.toReversed()) {
    const text = turn.reply ?? turn.updates?.findLast((u) => u.type === "text_delta")?.text;
    if (text) return text.split("\n")[0].replaceAll("`", "");
  }
  throw new Error("no text to wait for");
}

// ready waits until the page shows the session's last text and has loaded
// its fonts.
async function ready(page: Page, session: Session) {
  await expect(transcript(page)).toContainText(lastText(session.turns));
  if (session.turns.at(-1)?.end === "waiting") await expect(waiting(page).getByRole("article")).toBeVisible();
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
}

async function capture(page: Page, name: string) {
  for (const colorScheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme });
    await page.screenshot({
      path: join(root, "docs/images", `${name}-${colorScheme}.png`),
      animations: "disabled",
      caret: "hide",
    });
  }
}

test("hero", async ({ browser, services }) => {
  const repos = mkdtempSync(join(tmpdir(), "ox-hero-"));
  try {
    const ids = new Map<string, string>();
    const workspaces: (Scene["workspaces"][number] & { path: string; id: string })[] = [];
    for (const workspace of scene.workspaces) {
      const path = join(repos, workspace.repo);
      cpSync(join(root, "docs/screenshots/repos", workspace.repo), path, { recursive: true });
      for (const args of [
        ["init", "-q"],
        ["add", "."],
        ["commit", "-q", "-m", "Initial commit"],
      ]) {
        execFileSync("git", ["-C", path, ...args], { env: gitEnv });
      }
      const { workspace_id } = await services.request<{ workspace_id: string }>("workspace/create", { path });
      workspaces.push({ ...workspace, path, id: workspace_id });
    }

    const play = async (workspaceId: string, session: Session) => {
      const { session_id: id } = await services.request<{ session_id: string }>("session/create", {
        workspace_id: workspaceId,
        harness: session.harness,
        model: session.model,
        effort: session.effort,
        permission_mode: session.permission_mode,
        worktree: session.worktree,
      });
      ids.set(session.title, id);
      await services.request("session/send", { session_id: id, text: session.prompt });
      await settle(services, id, session.title, session.turns, 1);
      let turn = 1;
      for (const child of session.children ?? []) {
        const { name, title, message, harness, model, effort } = child;
        const created = await services.callTool(id, "create_session", { name, title, message, harness, model, effort });
        const childId = JSON.parse(created).session_id;
        ids.set(title, childId);
        await settle(services, childId, title, child.turns, child.turns.length);
        // The child's report is the parent's next input.
        if (child.turns.at(-1)?.end === "idle") await settle(services, id, session.title, session.turns, ++turn);
      }
      if (session.archived) await services.request("session/archive", { session_id: id });
    };
    // The sidebar lists a workspace's newest session first, so each pass
    // creates sessions in reverse, and parked ones come first.
    const pass = async (parked: boolean) => {
      for (const workspace of workspaces) {
        for (const session of workspace.sessions.filter((s) => !!s.parked === parked).toReversed()) {
          await play(workspace.id, session);
        }
      }
    };
    await pass(true);
    if (workspaces.some((w) => w.sessions.some((s) => s.parked))) await services.restartDaemon(config);
    await pass(false);

    const byTitle = (title: string) => {
      for (const workspace of scene.workspaces) {
        const session = workspace.sessions.find((s) => s.title === title);
        if (session) return { session, repo: workspace.repo };
      }
      throw new Error(`no session titled ${title}`);
    };
    const shown = byTitle(scene.desktop.session);

    const desktop = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 });
    const page = await desktop.newPage();
    await page.goto(`${services.url}/#${ids.get(scene.desktop.session)}`);
    await page.getByRole("tab", { name: "Files", exact: true }).click();
    const files = page.getByRole("complementary", { name: "Files" });
    for (const workspace of workspaces.filter((w) => w.sessions.some((s) => s.archived))) {
      await menuItem(await openMenu(workspaceSection(page, workspace.path)), "Show archived").click();
    }
    for (const name of scene.desktop.file.split("/")) {
      await files.getByRole("button", { name, exact: true }).click();
    }
    for (const title of ids.keys()) await expect(sessionButton(page, title)).toBeVisible();
    const file = readFileSync(join(root, "docs/screenshots/repos", shown.repo, scene.desktop.file), "utf8");
    await expect(page.getByRole("region", { name: "File" })).toContainText(file.split("\n")[0]);
    await ready(page, shown.session);
    await capture(page, "hero");
    await desktop.close();

    const { defaultBrowserType: _, ...iPhone } = devices["iPhone 15"];
    const phone = await browser.newContext(iPhone);
    const phonePage = await phone.newPage();
    await phonePage.goto(`${services.url}/#${ids.get(scene.phone.session)}`);
    await ready(phonePage, byTitle(scene.phone.session).session);
    await capture(phonePage, "hero-phone");
    await phone.close();
  } finally {
    rmSync(repos, { recursive: true, force: true });
  }
});
