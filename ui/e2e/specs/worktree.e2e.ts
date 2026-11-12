import { execFile } from "node:child_process";
import { access, mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";
import {
  commit,
  git,
  gitWorkspace,
  gitWorkspaceWithRemote,
  startSession,
  startWorktreeSession,
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

function exists(path: string) {
  return access(path).then(
    () => true,
    () => false,
  );
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

describe("Deleting a worktree session", () => {
  it("asks before discarding unsaved work", async () => {
    const { workspace } = await gitWorkspace();
    await startWorktreeSession(workspace);
    const path = await worktreePath();
    await writeFile(join(path, "unsaved.txt"), "unsaved\n");
    const remove = worktreeEntry().$('button[aria-label="Delete Session"]');

    await remove.click();
    let dialog = $("dialog[open]");
    await expect(dialog.$("h2")).toHaveText("Discard unsaved work?");
    await expect(dialog.$(".message")).toHaveText(expect.stringContaining(path));
    await dialog.$("button=Cancel").click();
    await expect(dialog).not.toBeExisting();
    await expect(worktreeEntry()).toBeExisting();
    expect(await exists(path)).toBe(true);

    await remove.click();
    dialog = $("dialog[open]");
    await dialog.$("button=Discard and Delete").click();
    await expect(worktreeEntry()).not.toBeExisting();
    expect(await exists(path)).toBe(false);
  });

  it("disables its Delete button while the delete waits for the startup", async () => {
    // The worktree's post-checkout hook holds the session's startup until
    // the test writes to `release`.
    const { cwd, workspace } = await gitWorkspace();
    const hooks = join(cwd, ".git", "hooks");
    const started = join(cwd, ".git", "hook-started");
    const release = join(cwd, ".git", "release");
    await promisify(execFile)("mkfifo", [release]);
    await mkdir(hooks, { recursive: true });
    await writeFile(
      join(hooks, "post-checkout"),
      `#!/bin/sh\ntouch '${started}'\ncat '${release}' > /dev/null\n`,
      { mode: 0o755 },
    );
    await git(cwd, "config", "core.hooksPath", hooks);
    // `oxctl new` answers once the session has started.
    const starting = startWorktreeSession(workspace);
    await browser.waitUntil(() => exists(started));

    const remove = worktreeEntry().$('button[aria-label="Delete Session"]');
    try {
      await remove.click();
      await expect(remove).toBeDisabled();
    } finally {
      await writeFile(release, "");
      await starting;
    }
    await expect(worktreeEntry()).not.toBeExisting();
  });

  it("says which branch it kept", async () => {
    const { cwd, workspace } = await gitWorkspace();
    await startWorktreeSession(workspace);
    const path = await worktreePath();
    await commit(path);
    const branch = (await git(path, "branch", "--show-current")).trim();

    await worktreeEntry().$('button[aria-label="Delete Session"]').click();
    const dialog = $("dialog[open]");
    await expect(dialog.$("h2")).toHaveText("Branch kept");
    await expect(dialog.$(".message")).toHaveText(`Ox kept the branch ${branch}.`);
    await expect(worktreeEntry()).not.toBeExisting();
    await dialog.$("button=OK").click();
    await expect(dialog).not.toBeExisting();
    expect((await git(cwd, "branch", "--list", branch)).trim()).toBe(branch);
  });
});
