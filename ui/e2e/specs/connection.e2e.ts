import {
  agentText,
  openSession,
  send,
  sessionEntry,
  startSession,
  stopDaemon,
} from "../helpers.ts";

describe("The connection to oxd", () => {
  it("views the session again after oxd restarts", async () => {
    const cwd = await startSession();
    await openSession(cwd);
    await send("say 1 0");
    await expect(agentText()[0]).toHaveText("chunk-1");

    await stopDaemon();
    // Only the restarted oxd lists the session as exited, and the view it
    // starts again on the new connection shows the stored history once.
    await expect(sessionEntry(cwd).$('[aria-label="Exited"]')).toBeDisplayed();
    await expect(agentText()).toBeElementsArrayOfSize(1);
    await expect(agentText()[0]).toHaveText("chunk-1");

    await openSession(cwd);
    await expect(agentText()).toBeElementsArrayOfSize(1);
    await expect(agentText()[0]).toHaveText("chunk-1");
  });
});
