// The web client against the real relay and oxd, with the fake harness,
// which echoes each prompt back a word at a time, or first asks to approve a
// tool call for a prompt "approve <tool> <input JSON>".
import { expect, type Page, test } from "@playwright/test";
import { fakeConfig, haikuConfig, type Services, startServices } from "./services";
import { hold, open, prompt, send, sessionButton, stop } from "./ui";

let services: Services;
test.beforeAll(async () => {
  services = await startServices(fakeConfig);
});
test.afterAll(async () => {
  await services.stop();
});

// newSession sends text as a new session's first prompt and waits for the
// reply.
async function newSession(page: Page, text: string) {
  await page.getByRole("button", { name: "New session" }).click();
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText(`You said: ${text}`);
  await expect(stop(page)).toHaveCount(0);
}

test("the first send hands its text and later edits to the new session", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  const create = await hold(page, "session/create");
  const firstSend = await hold(page, "session/send");
  await prompt(page).fill("first prompt");
  await prompt(page).press("Enter");
  await create.arrived;
  await prompt(page).pressSequentially(" and more");
  create.release();
  await firstSend.arrived;
  await expect(page.locator(".context")).not.toContainText("new session");
  await prompt(page).pressSequentially(" again");
  firstSend.release();
  await expect(page.locator(".reply")).toHaveText("You said: first prompt");
  await expect(page.locator(".prompt")).toHaveText(["first prompt"]);
  await expect(prompt(page)).toHaveValue("first prompt and more again");
});

test("a reply renders as Markdown and its prompt stays plain", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  const text = [
    "**bold** `code` <b>raw</b> [site](https://example.com) [bad](javascript:alert(1)) ![pic](https://example.com/p.png) https://example.org",
    "",
    "- one",
    "- two",
  ].join("\n");
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(stop(page)).toHaveCount(0);
  const reply = page.locator(".reply");
  await expect(reply.locator("strong")).toHaveText("bold");
  await expect(reply.locator("code")).toHaveText("code");
  await expect(reply.locator("li")).toHaveText(["one", "two"]);
  await expect(reply.locator("b")).toHaveCount(0);
  await expect(reply).toContainText("<b>raw</b>");
  await expect(reply.locator("img")).toHaveCount(0);
  await expect(reply.getByRole("link", { name: "pic", exact: true })).toHaveAttribute(
    "href",
    "https://example.com/p.png",
  );
  const site = reply.getByRole("link", { name: "site", exact: true });
  await expect(site).toHaveAttribute("href", "https://example.com");
  await expect(site).toHaveAttribute("target", "_blank");
  await expect(site).toHaveAttribute("rel", "noopener noreferrer");
  await expect(reply.locator("a[href^='javascript:']")).toHaveCount(0);
  await expect(reply).toContainText("[bad](javascript:alert(1))");
  await expect(reply.getByRole("link", { name: "https://example.org" })).toHaveAttribute(
    "href",
    /^https:\/\/example\.org\/?$/,
  );
  await expect(page.locator(".prompt")).toContainText("**bold**");
  await expect(page.locator(".prompt")).toContainText("<b>raw</b>");
  await expect(page.locator(".prompt strong")).toHaveCount(0);
});

test("a failed first send is retried in the session with its settings", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await page.locator("select[name=permission_mode]").selectOption("plan");
  const firstSend = await hold(page, "session/send");
  await prompt(page).fill("retry me");
  await prompt(page).press("Enter");
  await firstSend.arrived;
  firstSend.fail("relay unavailable");
  await expect(page.getByRole("alert")).toHaveText("relay unavailable");
  await expect(prompt(page)).toHaveValue("retry me");
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("plan");
  await send(page).click();
  await expect(page.locator(".reply")).toHaveText("You said: retry me");
  await expect(page.locator(".prompt")).toHaveText(["retry me"]);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("");
});

test("a session created after the user moves on leaves the selection alone", async ({ page }) => {
  await open(page, services);
  await newSession(page, "elsewhere");
  await page.getByRole("button", { name: "New session" }).click();
  const create = await hold(page, "session/create");
  await prompt(page).fill("background start");
  await prompt(page).press("Enter");
  await create.arrived;
  await sessionButton(page, "elsewhere").click();
  create.release();
  await expect(sessionButton(page, "background start")).toBeVisible();
  await expect(page.locator(".prompt")).toHaveText(["elsewhere"]);
  await sessionButton(page, "background start").click();
  await expect(page.locator(".reply")).toHaveText("You said: background start");
});

test("a pending configuration shows as pending and holds Send until it is confirmed", async ({ page }) => {
  await open(page, services);
  await newSession(page, "configure me");
  const mode = page.locator("select[name=permission_mode]");
  const configure = await hold(page, "session/configure");
  await mode.selectOption("plan");
  await configure.arrived;
  await expect(mode).toHaveAttribute("aria-busy", "true");
  await expect(page.locator(".select .loader")).toBeVisible();
  await expect(page.locator("button.picker")).not.toHaveAttribute("aria-busy");
  await prompt(page).fill("after configuring");
  await expect(send(page)).toBeDisabled();
  await prompt(page).press("Enter");
  configure.release();
  await expect(mode).toHaveValue("plan");
  await expect(mode).not.toHaveAttribute("aria-busy");
  await expect(page.locator(".select .chevron-down")).toBeVisible();
  await expect(send(page)).toBeEnabled();
  await expect(page.locator(".prompt")).toHaveText(["configure me"]);

  const failed = await hold(page, "session/configure");
  await mode.selectOption("manual");
  await failed.arrived;
  await expect(mode).toHaveAttribute("aria-busy", "true");
  await expect(send(page)).toBeDisabled();
  failed.fail("configuration refused");
  await expect(page.getByRole("alert")).toHaveText("configuration refused");
  await expect(mode).toHaveValue("plan");
  await expect(mode).not.toHaveAttribute("aria-busy");
  await send(page).click();
  await expect(page.locator(".reply").last()).toHaveText("You said: after configuring");
});

test("a reconnect keeps typed text and sends nothing again", async ({ page }) => {
  await open(page, services);
  await newSession(page, "sent once");
  await prompt(page).fill("unsent draft");
  await services.restartRelay();
  await expect(page.getByRole("alert")).toHaveCount(0, { timeout: 10_000 });
  await expect(prompt(page)).toHaveValue("unsent draft");
  await expect(page.locator(".prompt")).toHaveText(["sent once"]);
  await page.reload();
  await expect(page.locator(".prompt")).toHaveText(["sent once"]);
});

test("an oversized prompt fails visibly and stays in the box", async ({ page }) => {
  await open(page, services);
  await newSession(page, "small prompt");
  const large = "x".repeat(1 << 20);
  await prompt(page).fill(large);
  await prompt(page).press("Enter");
  await expect(page.getByRole("alert")).toHaveText("request is larger than 1 MiB");
  await expect(send(page)).toBeEnabled();
  await expect(prompt(page)).toHaveValue(large);
});

test("Stop interrupts a running reply", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await prompt(page).fill(Array.from({ length: 200 }, (_, i) => `word${i}`).join(" "));
  await prompt(page).press("Enter");
  await expect(page.locator(".reply")).toContainText("You said:");
  await stop(page).click();
  await expect(page.locator(".error")).toHaveText("Interrupted");
  await expect(stop(page)).toHaveCount(0);
});

test("Stop appears once the page's send is accepted, and stops it", async ({ page }) => {
  await open(page, services);
  await newSession(page, "earlier prompt");
  const held = await hold(page, "session/send");
  await prompt(page).fill(Array.from({ length: 200 }, (_, i) => `word${i}`).join(" "));
  await prompt(page).press("Enter");
  await held.arrived;
  await expect(send(page)).toBeDisabled();
  await expect(stop(page)).toHaveCount(0);
  held.release();
  await stop(page).click();
  await expect(page.locator(".error")).toHaveText("Interrupted");
  await expect(stop(page)).toHaveCount(0);
});

test("a send held in one session leaves another usable", async ({ page }) => {
  await open(page, services);
  await newSession(page, "first session");
  await newSession(page, "second session");
  await sessionButton(page, "first session").click();
  const held = await hold(page, "session/send");
  await prompt(page).fill("held prompt");
  await prompt(page).press("Enter");
  await held.arrived;
  await sessionButton(page, "second session").click();
  await prompt(page).fill("free prompt");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText("You said: free prompt");
  held.release();
  await sessionButton(page, "first session").click();
  await expect(page.locator(".reply").last()).toHaveText("You said: held prompt");
});

// ask sends a prompt that has the fake harness ask to approve a call of tool
// with inputJSON, and returns the approval's card.
async function ask(page: Page, tool: string, inputJSON: string) {
  await prompt(page).fill(`approve ${tool} ${inputJSON}`);
  await prompt(page).press("Enter");
  const card = page.locator(".approval");
  await expect(card).toHaveCount(1);
  return card;
}

// toolResult is the result text of the last call of tool, which the fake
// harness sets to the answer it received.
const toolResult = (page: Page, tool: string) =>
  page
    .locator("details.tool")
    .filter({ has: page.locator(".name", { hasText: tool }) })
    .last()
    .locator(".card pre")
    .last();

test("Allow sends the exact numbers displayed in the tool input", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();

  const input = '{"ids":[9007199254740993],"amount":1.00000000000000001}';
  const card = await ask(page, "Example", input);
  await card.locator("summary").click();
  await expect(card.locator("pre")).toHaveText('{ "ids": [ 9007199254740993 ], "amount": 1.00000000000000001 }');
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(card).toHaveCount(0);
  await expect(toolResult(page, "Example")).toHaveText(`{"behavior":"allow","updatedInput":${input}}`);
  await expect(stop(page)).toHaveCount(0);
});

test("a prompt sent while Claude works stays pending until Claude takes it", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await ask(page, "Bash", "{}");
  await expect(stop(page)).toBeVisible();
  await expect(send(page)).toBeVisible();
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(prompt(page)).toHaveValue("");
  await expect(page.locator(".prompt.queued")).toHaveText("steer");
  await expect(send(page)).toBeDisabled();

  await page.reload();
  await expect(page.locator(".prompt.queued")).toHaveText("steer");
  const card = page.locator(".approval");
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  await expect(page.locator(".prompt")).toHaveText(["approve Bash {}", "steer"]);
  await expect(page.locator(".reply").last()).toHaveText("You said: steer");
});

test("Stop drops a pending prompt and returns its text to the composer", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await ask(page, "Bash", "{}");
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(page.locator(".prompt.queued")).toHaveText("steer");
  await expect(prompt(page)).toHaveValue("");
  await prompt(page).fill("more");
  await stop(page).click();
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("steer\nmore");
  await expect(page.locator(".error")).toHaveText("Interrupted");
});

test("a dropped prompt returns only to the page that sent it", async ({ page, context }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await ask(page, "Bash", "{}");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator(".approval")).toHaveCount(1);
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(prompt(page)).toHaveValue("");
  await expect(other.locator(".prompt.queued")).toHaveText("steer");
  await stop(other).click();
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  await expect(other.locator(".prompt.queued")).toHaveCount(0);
  await expect(other.locator(".error")).toHaveText("Interrupted");
  await expect(prompt(page)).toHaveValue("steer");
  await expect(prompt(other)).toHaveValue("");
});

test("a reconnect clears a pending prompt Claude took while the page was away", async ({ page, context }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await ask(page, "Bash", "{}");
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(page.locator(".prompt.queued")).toHaveText("steer");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator(".prompt.queued")).toHaveText("steer");

  // The page stays disconnected while the other page reconnects and lets
  // the harness take the prompt.
  await page.route("**/events", (route) => route.abort());
  const reloaded = other.waitForResponse(
    (r) => r.url().includes("/rpc") && r.request().postDataJSON()?.method === "session/get",
  );
  await services.restartRelay();
  await reloaded;
  const card = other.locator(".approval");
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(other.locator(".reply").last()).toHaveText("You said: steer");
  await expect(page.locator(".prompt.queued")).toHaveText("steer");

  await page.unroute("**/events");
  await expect(page.locator(".reply").last()).toHaveText("You said: steer", { timeout: 15_000 });
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  await expect(page.locator(".prompt")).toHaveText(["approve Bash {}", "steer"]);
  await expect(send(page)).toBeEnabled();
});

test("AskUserQuestion is answered from a form", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();

  const input =
    '{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Cool"}],"multiSelect":false},{"question":"Which sections?","header":"Sections","options":[{"label":"Introduction","description":"a"},{"label":"Conclusion","description":"b"},{"label":"Appendix","description":"c"}],"multiSelect":true}]}';
  let card = await ask(page, "AskUserQuestion", input);
  await expect(card.locator("legend")).toHaveText(["ColorWhich color?", "SectionsWhich sections?"]);
  await expect(card).toContainText("Warm");
  const submit = card.getByRole("button", { name: "Submit" });

  await card.getByLabel("Red").check();
  await expect(submit).toBeDisabled();
  await card.getByLabel("Appendix").check();
  await card.getByLabel("Introduction").check();
  await card.getByLabel("Other answer").last().fill("Glossary");
  await expect(submit).toBeEnabled();

  // Moving between an option and Other leaves one answer to "Which color?".
  const otherBox = card.getByLabel("Other", { exact: true }).first();
  const otherField = card.getByLabel("Other answer").first();
  await otherBox.check();
  await expect(submit).toBeDisabled();
  await card.getByLabel("Red").check();
  await expect(submit).toBeEnabled();
  await otherField.fill("Green");
  await otherField.fill("");
  await expect(submit).toBeDisabled();
  await otherField.fill("Green");
  await card.getByLabel("Red").check();
  await expect(otherBox).not.toBeChecked();
  await expect(otherField).toHaveValue("Green");
  await expect(submit).toBeEnabled();

  await submit.click();
  await expect(card).toHaveCount(0);
  await expect(toolResult(page, "AskUserQuestion")).toHaveText(
    `{"behavior":"allow","updatedInput":${input.slice(0, -1)},"answers":{"Which color?":"Red","Which sections?":"Introduction, Appendix, Glossary"}}}`,
  );
  await expect(stop(page)).toHaveCount(0);

  card = await ask(page, "AskUserQuestion", input);
  await card.getByRole("button", { name: "Decline" }).click();
  await expect(card).toHaveCount(0);
  await expect(toolResult(page, "AskUserQuestion")).toHaveText(
    '{"behavior":"deny","message":"User declined to answer"}',
  );
  await expect(stop(page)).toHaveCount(0);

  card = await ask(page, "AskUserQuestion", '{"questions":"x"}');
  await expect(card.getByLabel("Allow")).toBeVisible();
});

test("a tool whose process ended with oxd shows stopped on every page", async ({ page, context }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await ask(page, "Bash", '{"command":"sleep 60"}');
  const marker = page.locator("details.tool .marker");
  await expect(marker).toHaveAttribute("aria-label", "running");
  await services.restartDaemon(fakeConfig, "SIGKILL");
  await expect(marker).toHaveAttribute("aria-label", "stopped");
  await prompt(page).fill("after the crash");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText("You said: after the crash");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator("details.tool .marker").first()).toHaveAttribute("aria-label", "stopped");
});

test("a reconnect keeps an expanded tool call open", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  const card = await ask(page, "Bash", '{"command":"ls"}');
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(stop(page)).toHaveCount(0);
  const tool = page.locator("details.tool");
  await tool.locator("summary").click();
  await expect(tool).toHaveAttribute("open");
  // Send waits for the session's reloaded transcript.
  await prompt(page).fill("after the reconnect");
  const snapshot = await hold(page, "session/get");
  await services.restartRelay();
  await snapshot.arrived;
  await expect(send(page)).toBeDisabled();
  snapshot.release();
  await expect(send(page)).toBeEnabled();
  await expect(tool).toHaveAttribute("open");
});

test("a session whose harness is no longer offered shows its settings", async ({ page }) => {
  await open(page, services);
  await newSession(page, "harness retired");
  await services.restartDaemon(haikuConfig);
  try {
    await expect(page.locator(".settings .picker .model")).toHaveText("echo");
    await expect(page.locator(".settings select[name=permission_mode]")).toHaveValue("manual");
  } finally {
    await services.restartDaemon(fakeConfig);
  }
});

test("a lost creation reply is recovered after leaving the draft", async ({ page }) => {
  await page.addInitScript(() => {
    const Original = window.EventSource;
    window.EventSource = class extends Original {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        this.addEventListener("message", (event) => {
          if (document.body.dataset.dropCreate && JSON.parse(event.data).result?.session_id) {
            delete document.body.dataset.dropCreate;
            event.stopImmediatePropagation();
          }
        });
      }
    };
  });
  await open(page, services);
  await newSession(page, "another conversation");
  const before = await page.locator("nav button.open").count();
  await page.getByRole("button", { name: "New session" }).click();
  await page.evaluate(() => {
    document.body.dataset.dropCreate = "true";
  });
  await prompt(page).fill("recover my prompt");
  await prompt(page).press("Enter");
  await expect(page.locator("nav button.open")).toHaveCount(before + 1);
  await prompt(page).fill("recover my edited prompt");
  await sessionButton(page, "another conversation").click();
  await services.restartRelay();
  await page.getByRole("button", { name: "Reopen draft" }).click();
  await expect(prompt(page)).toHaveValue("recover my edited prompt");
  await expect(page.getByRole("status").filter({ hasText: "Session recovered" })).toBeVisible();
  await expect(page.locator(".prompt")).toHaveCount(0);
  await send(page).click();
  await expect(page.locator(".reply")).toHaveText("You said: recover my edited prompt");
  await expect(page.locator("nav button.open")).toHaveCount(before + 1);
});

test("on reconnect each session waits for its own settings", async ({ page, context }) => {
  await open(page, services);
  await newSession(page, "available independently");
  await newSession(page, "refresh my settings");
  const id = new URL(page.url()).hash.slice(1);
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator(".reply")).toHaveText("You said: refresh my settings");

  const snapshot = await hold(page, "session/get", id);
  await services.restartRelay();
  await snapshot.arrived;
  await other.locator("select[name=permission_mode]").selectOption("plan");
  await expect(other.locator("select[name=permission_mode]")).not.toHaveAttribute("aria-busy");
  await prompt(page).fill("after reload");
  await expect(send(page)).toBeDisabled();
  await sessionButton(page, "available independently").click();
  await expect(send(page)).toBeEnabled();
  await sessionButton(page, "refresh my settings").click();
  snapshot.release();
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("plan");
  await expect(send(page)).toBeEnabled();
});
