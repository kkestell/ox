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
    await $('.composer button[aria-label="Stop"]').click();
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
    const permission = $(".composer .request-panel");
    await expect(sessionEntry(cwd).$('[aria-label="Awaiting permission"]')).toBeDisplayed();
    await permission.$("button=Allow").click();
    await expect(agentText()[0]).toHaveText("allowed");
    await expect(permission).not.toBeExisting();
    await expect($(".conversation .request-panel")).not.toBeExisting();
    await expect(sessionEntry(cwd).$('[aria-label="Awaiting permission"]')).not.toBeExisting();
  });

  it("asks the agent's questions above the composer", async () => {
    await send("elicit");
    const form = $(".composer .request-panel");
    const awaiting = sessionEntry(cwd).$('[aria-label="Awaiting input"]');
    await expect(form.$(".title")).toHaveText("Order");
    await expect(form.$("legend=Color")).toBeDisplayed();
    await expect(form.$("p=Pick a color")).toBeDisplayed();
    await expect(form.$("label*=Blue").$(".description")).toHaveText("Cool");
    await expect(awaiting).toBeDisplayed();
    // The colour is required, so the form stays up and sends nothing.
    await form.$("button=Submit").click();
    await expect(form).toBeDisplayed();
    await expect(awaiting).toBeDisplayed();
    await form.$("label*=Blue").click();
    await form.$("label*=Nuts").click();
    await form.$('input[aria-label="Note"]').setValue("hi");
    await form.$('input[aria-label="Count"]').setValue("2");
    // The date and time's default shows in local time and goes back as the
    // same instant.
    await expect(form.$('input[aria-label="When"]')).toHaveValue(/^2026-11-1[123]T\d\d:\d\d$/);
    await form.$("button=Submit").click();
    // Had the first Submit gone out, the agent would have answered it alone.
    await expect(agentText()[0]).toHaveText(
      'accepted {"color":"blue","count":2,"extras":["nuts"],"note":"hi","rush":false,' +
        '"when":"2026-11-12T08:30:00.000Z"}',
    );
    await expect(form).not.toBeExisting();
    await expect(awaiting).not.toBeExisting();
  });

  it("checks the agent's constraints before sending the answers", async () => {
    await send("elicit");
    const form = $(".composer .request-panel");
    const invalid = (selector: string) => form.$(`${selector}:invalid`);
    await form.$("label*=Blue").click();
    await form.$('input[aria-label="Weight"]').setValue("0.25");
    await form.$('input[aria-label="Email"]').setValue("nope");
    await form.$('input[aria-label="Code"]').setValue("ab12");
    await form.$("label*=Sprinkles").click();
    await form.$("label*=Nuts").click();
    await form.$("label*=Cherries").click();
    await form.$("label*=ketchup").click();
    await expect(invalid('input[aria-label="Weight"]')).toBeExisting();
    await expect(invalid('input[aria-label="Email"]')).toBeExisting();
    await expect(invalid('input[aria-label="Code"]')).toBeExisting();
    // At most two extras and at least two sauces.
    await expect(invalid('input[name="extras"]')).toBeExisting();
    await expect(invalid('input[name="sauces"]')).toBeExisting();
    await form.$("button=Submit").click();
    await expect(form).toBeDisplayed();

    await form.$('input[aria-label="Weight"]').setValue("1.5");
    await form.$('input[aria-label="Email"]').setValue("a@b.co");
    // A schema pattern matches anywhere in the value.
    await form.$('input[aria-label="Code"]').setValue("x123y");
    await expect(invalid('input[aria-label="Code"]')).not.toBeExisting();
    await form.$("label*=Sprinkles").click();
    await form.$("label*=mustard").click();
    await form.$("label*=large").click();
    await expect(form.$(":invalid")).not.toBeExisting();
    await form.$("button=Submit").click();
    await expect(agentText()[0]).toHaveText(
      'accepted {"code":"x123y","color":"blue","email":"a@b.co","extras":["nuts","cherries"],' +
        '"rush":false,"sauces":["ketchup","mustard"],"size":"large","weight":1.5,' +
        '"when":"2026-11-12T08:30:00.000Z"}',
    );
    await expect(form).not.toBeExisting();
  });

  it("declines the agent's questions", async () => {
    await send("elicit");
    const form = $(".composer .request-panel");
    await form.$("button=Decline").click();
    await expect(agentText()[0]).toHaveText("declined");
    await expect(form).not.toBeExisting();
  });

  it("withdraws the questions when the turn stops", async () => {
    await send("elicit");
    const form = $(".composer .request-panel");
    await expect(form.$(".title")).toHaveText("Order");
    await $('.composer button[aria-label="Stop"]').click();
    await expect(agentText()[0]).toHaveText("withdrawn");
    await expect(form).not.toBeExisting();
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
    await $('.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("stops a running turn", async () => {
    await send("wait");
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).toBeDisplayed();
    await $('.composer button[aria-label="Stop"]').click();
    await expect(sessionEntry(cwd).$('[aria-label="Running"]')).not.toBeExisting();
    await expect(sendButton()).toBeDisplayed();
  });

  it("sets a config option on the agent", async () => {
    const mode = '.composer select[aria-label="Mode"]';
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
