import {
  agentText,
  choose,
  composer,
  deleteSessions,
  openSession,
  send,
  sendButton,
  sessionEntry,
  startSession,
} from "../helpers.ts";

describe("A session", () => {
  let cwd: string;

  beforeEach(async () => {
    cwd = await startSession();
    await openSession(cwd);
  });

  it("is listed as a new session until the agent names it", async () => {
    await expect(sessionEntry(cwd).$(".title")).toHaveText("New session");
    await send("title Release notes");
    await expect(sessionEntry(cwd).$(".title")).toHaveText("Release notes");
  });

  it("shows the agent's reply while the turn runs", async () => {
    // The agent says one chunk, then keeps the turn open until it is stopped.
    await composer().setValue("say 1 0");
    await browser.keys(["Shift", "Enter"]);
    await composer().addValue("wait");
    await expect(composer()).toHaveValue("say 1 0\nwait");
    await send();
    await expect(agentText()[0]).toHaveText("chunk-1");
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).not.toBeExisting();
  });

  it("shows the prompt and the agent's reply", async () => {
    await send("say 3 0");
    await expect($(".conversation .prompt .text")).toHaveText("say 3 0");
    await expect(agentText()).toBeElementsArrayOfSize(1);
    await expect(agentText()[0]).toHaveText("chunk-1chunk-2chunk-3");
    await expect(composer()).toHaveValue("");
  });

  it("sends with Enter", async () => {
    await composer().setValue("say 1 0");
    await expect(sendButton()).toBeEnabled();
    await browser.keys("Enter");
    await expect(agentText()[0]).toHaveText("chunk-1");
  });

  it("asks for permission above the composer until it is answered", async () => {
    await send("permission");
    const permission = $("form.composer .permission");
    await expect(sessionEntry(cwd).$('[aria-label="Awaiting permission"]')).toBeDisplayed();
    await permission.$("button=Allow").click();
    await expect(agentText()[0]).toHaveText("allowed");
    await expect(permission).not.toBeExisting();
    await expect($(".conversation .permission")).not.toBeExisting();
    await expect(sessionEntry(cwd).$('[aria-label="Awaiting permission"]')).not.toBeExisting();
  });

  it("shows a tool call's output", async () => {
    await send("tool listed `a.rs`");
    const tool = $(".conversation .tool-call");
    await expect(tool.$(".title")).toHaveText("Fake tool listed `a.rs`");
    await expect(tool.$(".title code")).toHaveText("listed `a.rs`");
    await expect(tool.$('.status[aria-label="Completed"] svg')).toBeDisplayed();
    await tool.$("summary").click();
    await expect(tool.$("section:first-child pre")).toHaveText('{\n  "text": "listed `a.rs`"\n}');
    await expect(tool.$("section:last-child pre")).toHaveText("listed `a.rs`");
    await expect(tool.$("section:last-child img")).toBeDisplayed();
  });

  it("stops a turn the agent started", async () => {
    await send("own-turn");
    await expect(agentText()[0]).toHaveText("own-turn");
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("stops a running turn", async () => {
    await send("wait");
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("sets a config option on the agent", async () => {
    const mode = 'form.composer select[aria-label="Mode"]';
    await expect($(mode)).toHaveValue("ask");
    await choose(mode, "code");
    await expect($(mode)).toHaveValue("code");
    await send("config mode");
    await expect(agentText()[0]).toHaveText("code");
  });

  it("replays the conversation after the webview reloads", async () => {
    await send("say 2 0");
    await expect(agentText()[0]).toHaveText("chunk-1chunk-2");
    await browser.refresh();
    await expect(sessionEntry(cwd).$("button.select")).toHaveAttribute("aria-current", "true");
    await expect($(".conversation .prompt .text")).toHaveText("say 2 0");
    await expect(agentText()[0]).toHaveText("chunk-1chunk-2");
  });

  it("reports that the agent exited and disables the composer", async () => {
    await send("exit 3");
    await expect($(".conversation .agent-exited")).toBeDisplayed();
    await expect(sessionEntry(cwd).$('[aria-label="Exited"]')).toBeDisplayed();
    await expect(composer()).toBeDisabled();
  });

  it("is removed from the list when deleted", async () => {
    await expect(sessionEntry(cwd)).toBeExisting();
    await deleteSessions(cwd);
    await expect(sessionEntry(cwd)).not.toBeExisting();
  });
});
