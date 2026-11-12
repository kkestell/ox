import { agentText, openSession, send, slide, startSession } from "../helpers.ts";

const picker = () => $("button.model-picker");
const menu = () => $("#model-menu");
const search = () => menu().$('input[aria-label="Search models"]');
const options = () => menu().$$('[role="option"]');
const sliderSelector = '#model-menu input[type="range"]';
const slider = () => $(sliderSelector);

describe("The model picker", () => {
  beforeEach(async () => {
    await openSession(await startSession());
  });

  it("shows the session's model and effort", async () => {
    await expect(picker()).toHaveText("SmartMedium");
  });

  it("finds a model by its name and chooses it with Enter, staying open", async () => {
    await picker().click();
    await expect(menu()).toBeDisplayed();
    await expect(search()).toBeFocused();
    await expect(options()).toBeElementsArrayOfSize(2);
    await expect(menu().$('[role="option"][aria-selected="true"]')).toHaveText("Smart");

    await search().setValue("fa");
    await expect(options()).toBeElementsArrayOfSize(1);
    await browser.keys("Enter");
    await expect(picker()).toHaveText("FastMedium");
    await expect(menu()).toBeDisplayed();
    await expect(menu().$('[role="option"][aria-selected="true"]')).toHaveText("Fast");
    await send("config model");
    await expect(agentText()[0]).toHaveText("fast");
  });

  it("sets the effort with the slider", async () => {
    await picker().click();
    await expect(slider()).toHaveValue("1");
    await slide(sliderSelector, 2);
    await expect(slider()).toHaveValue("2");
    await expect(picker()).toHaveText("SmartHigh");
    await send("config effort");
    await expect(agentText()[0]).toHaveText("high");
  });
});
