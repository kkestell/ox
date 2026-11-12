import { deleteSessions, removeWorkspace, startSession, workspaceGroup } from "../helpers.ts";

describe("A workspace", () => {
  it("groups its sessions under the name of its folder", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    await expect(group.$("h2")).toHaveText(cwd.split("/").pop()!, { ignoreCase: true });
    await expect(group.$$("li.session")).toBeElementsArrayOfSize(1);
    await expect(group.$(`button.select[title="${cwd}"]`)).toBeDisplayed();
  });

  it("disappears when removed", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    await expect(group).toBeExisting();

    await deleteSessions(cwd);
    await expect(group.$$("li.session")).toBeElementsArrayOfSize(0);
    await removeWorkspace(cwd);
    await expect(group).not.toBeExisting();
  });
});
