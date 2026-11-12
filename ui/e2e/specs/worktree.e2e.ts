import { join } from "node:path";
import {
  git,
  gitWorkspace,
  gitWorkspaceWithRemote,
  startSession,
  workspaceGroup,
  worktreeEntry,
} from "../helpers.ts";

/** Opens the New Session menu of the workspace in `cwd`. */
async function openMenu(cwd: string) {
  await workspaceGroup(cwd).$('button[aria-label="New Session"]').click();
  const menu = workspaceGroup(cwd).$('[role="menu"]');
  await expect(menu).toBeDisplayed();
  return menu;
}

/** Opens the New Worktree on Branch dialog of the workspace in `cwd`. */
async function openBranchDialog(cwd: string) {
  const menu = await openMenu(cwd);
  await menu.$("button=New Worktree on Branch…").click();
  const dialog = $("dialog[open]");
  await expect(dialog.$("h2")).toHaveText("New Worktree on Branch");
  return dialog;
}

/** Closes `dialog` with its Cancel button. */
async function cancel(dialog: ReturnType<typeof $>) {
  await dialog.$("button=Cancel").click();
  await expect(dialog).not.toBeExisting();
}

/** The suggestion of the branch `name` in `dialog`. */
function suggestion(dialog: ReturnType<typeof $>, name: string) {
  return dialog.$(`datalist option[value="${name}"]`);
}

/** The checkout of the one worktree session, which its entry shows on hover. */
async function worktreePath() {
  await expect(worktreeEntry()).toBeExisting();
  const cwd = await worktreeEntry().$("button.select").getAttribute("title");
  return cwd!.replace(/\/$/, "");
}

// A test that fails with a dialog open leaves it to the next test.
afterEach(async () => {
  while (await $("dialog[open]").isExisting()) await browser.keys("Escape");
});

describe("The New Session menu", () => {
  it("offers only the existing checkout outside Git", async () => {
    const cwd = await startSession();
    const menu = await openMenu(cwd);
    await expect(menu.$('select[aria-label="Agent"]')).toHaveValue("claude");
    await expect(menu.$$('[role="menuitem"]')).toBeElementsArrayOfSize(1);
    await expect(menu.$('[role="menuitem"]')).toHaveText("Existing Checkout");
  });

  it("offers worktrees in Git", async () => {
    const { cwd } = await gitWorkspace();
    const menu = await openMenu(cwd);
    const items = menu.$$('[role="menuitem"]');
    await expect(items).toBeElementsArrayOfSize(3);
    await expect(items[0]).toHaveText("Existing Checkout");
    await expect(items[1]).toHaveText("New Worktree");
    await expect(items[2]).toHaveText("New Worktree on Branch…");
  });

  it("starts a session in a new worktree on a new branch", async () => {
    const { cwd } = await gitWorkspace();
    const menu = await openMenu(cwd);
    await menu.$("button=New Worktree").click();
    await expect(worktreeEntry()).toBeExisting();
    const branches = (await git(cwd, "branch", "--list", "ox/*")).split("\n").filter(Boolean);
    expect(branches).toHaveLength(1);
  });
});

describe("The New Worktree on Branch dialog", () => {
  it("suggests the branches, and again once the remotes are fetched", async () => {
    const { cwd, pusher } = await gitWorkspaceWithRemote();
    let dialog = await openBranchDialog(cwd);
    await expect(suggestion(dialog, "main")).toBeExisting();
    await expect(suggestion(dialog, "origin/feature")).toBeExisting();
    await cancel(dialog);

    await git(pusher, "push", "origin", "main:late");
    dialog = await openBranchDialog(cwd);
    await expect(suggestion(dialog, "origin/late")).toBeExisting();
    await cancel(dialog);
  });

  it("takes a starting point only for a new branch", async () => {
    const { cwd } = await gitWorkspaceWithRemote();
    const dialog = await openBranchDialog(cwd);
    await expect(suggestion(dialog, "origin/feature")).toBeExisting();
    const start = dialog.$('input[name="start"]');
    await start.setValue("main");
    await dialog.$('input[name="branch"]').setValue("feature");
    await expect(start).toBeDisabled();
    await expect(start).toHaveValue("");
    await dialog.$('input[name="branch"]').setValue("fresh");
    await expect(start).toBeEnabled();
    await cancel(dialog);
  });

  it("starts a session in a worktree on the branch it names", async () => {
    const { cwd } = await gitWorkspaceWithRemote();
    const dialog = await openBranchDialog(cwd);
    await dialog.$('input[name="branch"]').setValue("topic");
    await dialog.$('input[name="start"]').setValue("main");
    await dialog.$("button=Create").click();
    await expect(dialog).not.toBeExisting();
    const path = await worktreePath();
    await expect(worktreeEntry().$("button.select")).toHaveAttribute("aria-current", "true");
    expect((await git(path, "branch", "--show-current")).trim()).toBe("topic");
  });

  it("notes a failed fetch and keeps the branches it has", async () => {
    const { cwd } = await gitWorkspaceWithRemote();
    await git(cwd, "remote", "set-url", "origin", join(cwd, "missing"));
    const dialog = await openBranchDialog(cwd);
    await expect(dialog.$(".fetch-error")).toBeDisplayed();
    await expect(suggestion(dialog, "main")).toBeExisting();
    await expect(suggestion(dialog, "origin/feature")).toBeExisting();
    await cancel(dialog);
  });
});
