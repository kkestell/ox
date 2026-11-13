import { join } from "node:path";
import { agentText, composer, openSession, send, startSession } from "../helpers.ts";

const menu = () => $('.command-menu [role="listbox"]');
const firstOption = () => menu().$('[role="option"]');
const options = () => menu().$$('[role="option"]');

/** Moves the editor's caret as a user editing the command token would. */
async function caret(position: number) {
  await browser.execute((offset) => {
    const input = document.querySelector<HTMLTextAreaElement>(".composer textarea")!;
    input.focus();
    input.setSelectionRange(offset, offset);
    document.dispatchEvent(new Event("selectionchange"));
  }, position);
}

describe("Slash command completion", () => {
  beforeEach(async () => {
    await openSession(await startSession());
  });

  it("discovers commands before a prompt, completes with the keyboard, then sends arguments", async () => {
    await composer().setValue("/");
    await expect(options()).toBeElementsArrayOfSize(3);
    await expect(firstOption().$(".menu-item-text > span:first-child")).toHaveText("/echo text");
    await expect(firstOption().$(".menu-item-text > .menu-detail")).toHaveText(
      "Echo the arguments",
    );
    await expect(composer()).toHaveAttribute("aria-expanded", "true");
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "command-menu.png"));
    await browser.keys("ArrowDown");
    await browser.keys("Enter");
    await expect(composer()).toHaveValue("/review ", { trim: false });
    await expect(menu()).not.toBeExisting();
    await expect($(".conversation .prompt")).not.toBeExisting();
    await composer().addValue("src/main.rs");
    await browser.keys("Enter");
    await expect(agentText()[0]).toHaveText("src/main.rs");
    await expect($(".conversation .prompt .text")).toHaveText("/review src/main.rs");
    await $("button.model-picker").click();
    await $("#model-menu").waitForDisplayed();
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "model-menu.png"));
  });

  it("filters names without case sensitivity and accepts pointer and Tab selections", async () => {
    await composer().setValue("/RE");
    await expect(options()).toBeElementsArrayOfSize(1);
    await expect(firstOption().$(".menu-item-text > span:first-child")).toHaveText("/review file");
    await expect(firstOption().$(".menu-item-text > .menu-detail")).toHaveText("Review a file");
    await firstOption().click();
    await expect(composer()).toHaveValue("/review ", { trim: false });
    await expect(composer()).toBeFocused();
    await composer().setValue("/plugin:");
    await expect(options()).toBeElementsArrayOfSize(1);
    await browser.keys("Tab");
    await expect(composer()).toHaveValue("/plugin:check ", { trim: false });
    await expect(composer()).toBeFocused();
  });

  it("preserves arguments when completing a command being edited", async () => {
    await composer().setValue("/re existing arguments");
    await expect(menu()).not.toBeExisting();
    await caret(3);
    await expect(options()).toBeElementsArrayOfSize(1);
    await browser.keys("Enter");
    await expect(composer()).toHaveValue("/review existing arguments");
    await browser.keys("Enter");
    await expect(agentText()[0]).toHaveText("existing arguments");
  });

  it("dismisses suggestions and leaves prose, unmatched commands and multiline input alone", async () => {
    await composer().setValue("/");
    await expect(menu()).toBeDisplayed();
    await browser.keys("Escape");
    await expect(menu()).not.toBeExisting();
    await expect(composer()).toHaveValue("/");
    await composer().setValue("text /re");
    await expect(menu()).not.toBeExisting();
    await composer().setValue("/unknown");
    await expect(menu()).not.toBeExisting();
    await expect(composer()).toHaveValue("/unknown");
    await composer().setValue("/");
    await expect(menu()).toBeDisplayed();
    await browser.keys(["Shift", "Enter"]);
    await expect(composer()).toHaveValue("/\n", { trim: false });
    await expect($(".conversation .prompt")).not.toBeExisting();
  });

  it("replaces and clears catalogs, restores a current snapshot and isolates sessions", async () => {
    await send(
      'commands [{"type":"command","name":"echo","description":"Changed","input":{"type":"_future","hint":"ignored"}}]',
    );
    await composer().setValue("/");
    await expect(options()).toBeElementsArrayOfSize(1);
    await expect(firstOption().$(".menu-item-text > span:first-child")).toHaveText("/echo");
    await expect(firstOption().$(".menu-item-text > .menu-detail")).toHaveText("Changed");
    await browser.refresh();
    await composer().setValue("/");
    await expect(options()).toBeElementsArrayOfSize(1);
    await expect(firstOption().$(".menu-item-text > span:first-child")).toHaveText("/echo");
    await expect(firstOption().$(".menu-item-text > .menu-detail")).toHaveText("Changed");
    await send("commands []");
    await composer().setValue("/");
    await expect(menu()).not.toBeExisting();
    await openSession(await startSession());
    await expect(composer()).toHaveValue("");
    await composer().setValue("/");
    await expect(options()).toBeElementsArrayOfSize(3);
  });
});
