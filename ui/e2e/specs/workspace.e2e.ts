import { sessionEntry, startSession, workspaceGroup } from "../helpers.ts";

describe("A workspace", () => {
  it("groups its sessions under the name of its folder", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    await expect(group.$("h2")).toHaveText(cwd.split("/").pop()!, { ignoreCase: true });
    await expect(group.$$("li.session")).toBeElementsArrayOfSize(1);
    await expect(group.$(`button.select[title="${cwd}"]`)).toBeDisplayed();
  });

  it("is removed once its sessions are closed", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    await group.$('button[aria-label="Remove Workspace"]').click();
    await expect($(".session-list .error")).toHaveText("the workspace has sessions");
    await expect(group).toBeDisplayed();

    await sessionEntry(cwd).$('button[aria-label="Close Session"]').click();
    await expect(sessionEntry(cwd)).not.toBeExisting();
    await group.$('button[aria-label="Remove Workspace"]').click();
    await expect(group).not.toBeExisting();
  });
});
