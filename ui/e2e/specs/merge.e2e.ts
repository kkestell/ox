import { access } from "node:fs/promises";
import { join } from "node:path";
import {
  commit,
  compareSession,
  git,
  gitWorkspace,
  newTask,
  openMenu,
  sessionEntry,
  startWorktreeSession,
  worktreeEntry,
} from "../helpers.ts";

/** The Merge button in the content header. */
function mergeButton() {
  return $(".content-header").$("button.text-button");
}

/** The git status icon of the one worktree session's row. */
function branchStatus() {
  return worktreeEntry().$(".branch-status");
}

/** Clicks Merge into main and waits for the dialog that follows the move. */
async function merge() {
  await expect(mergeButton()).toHaveText("Merge into main");
  await expect(mergeButton()).toBeEnabled();
  await mergeButton().click();
  const dialog = $("dialog[open]");
  await expect(dialog.$("h2")).toHaveText("Merged into main");
  return dialog;
}

// A test that fails with a dialog open leaves it to the next test.
afterEach(async () => {
  while (await $("dialog[open]").isExisting()) await browser.keys("Escape");
});

describe("Local merge", () => {
  it("can't merge a task with no commits", async () => {
    const { cwd } = await gitWorkspace();
    await newTask(cwd);
    await expect(mergeButton()).toHaveText("Merge into main");
    await expect(mergeButton()).toBeDisabled();
    await expect(mergeButton()).toHaveAttribute("title", "No commits");
    await expect(branchStatus()).not.toBeExisting();
  });

  it("merges a task one commit ahead into main and deletes it", async () => {
    const { cwd, workspace } = await gitWorkspace();
    const path = await newTask(cwd);
    const file = await commit(path);
    await compareSession(workspace);
    await expect(branchStatus()).toHaveAttribute("title", "1 commit ahead of main");
    await expect(branchStatus()).not.toHaveElementClass("attention");
    const branch = (await git(path, "branch", "--show-current")).trim();
    const tip = await git(path, "rev-parse", "HEAD");

    const dialog = await merge();
    expect(await git(cwd, "rev-parse", "main")).toBe(tip);
    await access(join(cwd, file));
    await expect(dialog.$("code")).toHaveText(branch);
    await dialog.$("button=Delete Session").click();
    await expect(dialog).not.toBeExisting();
    await expect(worktreeEntry()).not.toBeExisting();
    expect(await git(cwd, "branch", "--list", branch)).toBe("");
  });

  it("keeps a merged task, which then has no commits", async () => {
    const { cwd, workspace } = await gitWorkspace();
    const path = await newTask(cwd);
    await commit(path);
    await compareSession(workspace);

    const dialog = await merge();
    await dialog.$("button=Keep Session").click();
    await expect(dialog).not.toBeExisting();
    await expect(worktreeEntry()).toBeExisting();
    await expect(mergeButton()).toHaveAttribute("title", "No commits");
    await expect(mergeButton()).toBeDisabled();
  });

  it("asks for a squash when the task has two commits", async () => {
    const { cwd, workspace } = await gitWorkspace();
    const path = await newTask(cwd);
    await commit(path);
    await commit(path);
    await compareSession(workspace);
    await expect(branchStatus()).toHaveAttribute("title", "2 commits ahead of main");
    await expect(branchStatus()).toHaveElementClass("attention");
    await expect(mergeButton()).toHaveAttribute("title", "2 commits: ask the agent to squash");
    await expect(mergeButton()).toBeDisabled();
  });

  it("asks for a rebase when main has moved on", async () => {
    const { cwd, workspace } = await gitWorkspace();
    const path = await newTask(cwd);
    await commit(path);
    await commit(cwd);
    await compareSession(workspace);
    await expect(branchStatus()).toHaveAttribute("title", "1 commit ahead, 1 behind main");
    await expect(branchStatus()).toHaveElementClass("attention");
    await expect(mergeButton()).toHaveAttribute("title", "Behind main: ask the agent to rebase");
    await expect(mergeButton()).toBeDisabled();
  });

  it("leaves out sessions with no comparison ref", async () => {
    const { cwd, workspace } = await gitWorkspace();
    await (await openMenu(cwd)).$("button=Session in Checkout").click();
    const checkout = sessionEntry(cwd).$("button.select");
    await expect(checkout).toHaveAttribute("aria-current", "true");
    await expect(mergeButton()).not.toBeExisting();
    await expect(sessionEntry(cwd).$(".branch-status")).not.toBeExisting();

    await startWorktreeSession(workspace);
    const worktree = worktreeEntry().$("button.select");
    await worktree.click();
    await expect(worktree).toHaveAttribute("aria-current", "true");
    await expect(mergeButton()).not.toBeExisting();
    await expect(branchStatus()).not.toBeExisting();
  });
});
