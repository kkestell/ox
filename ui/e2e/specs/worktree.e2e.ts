import {
  compareSession,
  git,
  gitWorkspace,
  newTask,
  openMenu,
  startSession,
  workspaceGroup,
  worktreeEntry,
} from "../helpers.ts";

describe("The New Task menu", () => {
  it("offers only a session in the checkout outside Git", async () => {
    const cwd = await startSession();
    const menu = await openMenu(cwd);
    await expect(menu.$('select[aria-label="Agent"]')).toHaveValue("claude");
    await expect(menu.$$('[role="menuitem"]')).toBeElementsArrayOfSize(1);
    await expect(menu.$('[role="menuitem"]')).toHaveText("Session in Checkout");
    await menu.$("button=Session in Checkout").click();
    await expect(workspaceGroup(cwd).$$("li.session")).toBeElementsArrayOfSize(2);
    await expect(workspaceGroup(cwd).$('button.select[aria-current="true"]')).toHaveAttribute(
      "title",
      cwd,
    );
  });

  it("offers a new task and a session in the checkout in Git", async () => {
    const { cwd } = await gitWorkspace();
    const menu = await openMenu(cwd);
    const items = menu.$$('[role="menuitem"]');
    await expect(items).toBeElementsArrayOfSize(2);
    await expect(items[0]).toHaveText("New Task");
    await expect(items[1]).toHaveText("Session in Checkout");
  });

  it("starts a new task on a new branch at main, compared with main", async () => {
    const { cwd, workspace } = await gitWorkspace();
    const path = await newTask(cwd);
    const branches = (await git(cwd, "branch", "--list", "ox/*")).split("\n").filter(Boolean);
    expect(branches).toHaveLength(1);
    expect((await git(path, "branch", "--show-current")).trim()).toMatch(/^ox\//);
    expect(await git(path, "rev-parse", "HEAD")).toBe(await git(cwd, "rev-parse", "main"));
    await expect(worktreeEntry().$(".branch-status")).not.toBeExisting();
    expect((await compareSession(workspace)).split("\t")[0]).toBe("main");
  });
});
