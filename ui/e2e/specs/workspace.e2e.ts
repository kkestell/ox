import { sessionEntry, startSession, workspaceGroup } from "../helpers.ts";

describe("A workspace", () => {
  it("groups its sessions under the name of its folder", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    await expect(group.$("h2")).toHaveText(cwd.split("/").pop()!, { ignoreCase: true });
    await expect(group.$$("li.session")).toBeElementsArrayOfSize(1);
    await expect(group.$(`button.select[title="${cwd}"]`)).toBeDisplayed();
  });

  it("is removed once its sessions are deleted", async () => {
    const cwd = await startSession();
    const group = workspaceGroup(cwd);
    const remove = group.$('button[aria-label="Remove Workspace"]');
    await expect(remove).toBeDisabled();

    await sessionEntry(cwd).$('button[aria-label="Delete Session"]').click();
    await expect(sessionEntry(cwd)).not.toBeExisting();
    await expect(remove).toBeEnabled();
    await remove.click();
    await expect(group).not.toBeExisting();
  });
});
