import {
  agentText,
  choose,
  composer,
  openSession,
  send,
  sendButton,
  sessionEntry,
  startSession,
} from "../helpers.ts";

describe("A session", () => {
  let folder: string;

  beforeEach(async () => {
    folder = await startSession();
    await openSession(folder);
  });

  it("is listed by its folder until the agent names it", async () => {
    await expect(sessionEntry(folder).$(".title")).toHaveText("New session");
    await send("title Release notes");
    await expect(sessionEntry(folder).$(".title")).toHaveText("Release notes");
  });

  it("shows the agent's reply while the turn runs", async () => {
    // The agent says one chunk, then keeps the turn open until it is stopped.
    await composer().setValue("say 1 0");
    await browser.keys(["Shift", "Enter"]);
    await composer().addValue("wait");
    await expect(composer()).toHaveValue("say 1 0\nwait");
    await send();
    await expect(agentText()[0]).toHaveText("chunk-1");
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).not.toBeExisting();
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

  it("asks for permission and reports the answer", async () => {
    await send("permission");
    const permission = $(".conversation .permission");
    await expect(sessionEntry(folder).$('[aria-label="Awaiting permission"]')).toBeDisplayed();
    await permission.$("button=Allow").click();
    await expect(permission.$(".outcome")).toHaveText("Allow");
    await expect(agentText()[0]).toHaveText("allowed");
    await expect(sessionEntry(folder).$('[aria-label="Awaiting permission"]')).not.toBeExisting();
  });

  it("shows a tool call's output", async () => {
    await send("tool listed a.rs");
    const tool = $(".conversation .tool-call");
    await expect(tool.$(".title")).toHaveText("Fake tool");
    await tool.$("summary").click();
    await expect(tool.$("p.text")).toHaveText("listed a.rs");
    await expect(tool.$("img")).toBeDisplayed();
  });

  it("stops a turn the agent started", async () => {
    await send("own-turn");
    await expect(agentText()[0]).toHaveText("own-turn");
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("stops a running turn", async () => {
    await send("wait");
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).toBeDisplayed();
    await $('form.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(folder).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("sets a config option on the agent", async () => {
    const mode = 'form.composer select[aria-label="Mode"]';
    await expect($(mode)).toHaveValue("ask");
    await choose(mode, "code");
    await expect($(mode)).toHaveValue("code");
    await send("mode");
    await expect(agentText()[0]).toHaveText("code");
  });

  it("replays the conversation after the webview reloads", async () => {
    await send("say 2 0");
    await expect(agentText()[0]).toHaveText("chunk-1chunk-2");
    await browser.refresh();
    await expect(sessionEntry(folder).$("button.select")).toHaveAttribute("aria-current", "true");
    await expect($(".conversation .prompt .text")).toHaveText("say 2 0");
    await expect(agentText()[0]).toHaveText("chunk-1chunk-2");
  });

  it("reports that the agent exited and disables the composer", async () => {
    await send("exit 3");
    await expect($(".conversation .agent-exited")).toBeDisplayed();
    await expect(sessionEntry(folder).$('[aria-label="Exited"]')).toBeDisplayed();
    await expect(composer()).toBeDisabled();
  });

  it("is removed from the list when closed", async () => {
    await sessionEntry(folder).$('button[aria-label="Close Session"]').click();
    await expect(sessionEntry(folder)).not.toBeExisting();
  });
});
