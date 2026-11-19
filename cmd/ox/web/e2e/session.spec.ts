// The web client against the real relay and oxd, with the fake harness,
// which echoes each prompt back a word at a time.
import { expect, type Page, test } from "@playwright/test";
import { fakeConfig, type Services, startServices } from "./services";
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
  await expect(send(page)).toBeVisible();
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
  await expect(send(page)).toBeVisible();
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
  await expect(send(page)).toBeVisible();
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
  await expect(send(page)).toBeVisible();
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

// The fake harness has no approval tool. Replay the raw event a client receives.
test("Allow sends the exact numbers displayed in the tool input", async ({ page }) => {
  await page.addInitScript(() => {
    const BrowserEventSource = window.EventSource;
    window.EventSource = class extends BrowserEventSource {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        window.addEventListener("approval-fixture", (event) =>
          this.dispatchEvent(new MessageEvent("message", { data: (event as CustomEvent).detail })),
        );
      }
    };
  });
  await open(page, services);
  await newSession(page, "approval fixture");

  const input = '{"ids":[9007199254740993],"amount":1.00000000000000001}';
  const id = new URL(page.url()).hash.slice(1);
  const update = `{"method":"session/update","params":{"session_id":"${id}","type":"approval","request_id":"req","request":{"tool_name":"Example","input":${input}}}}`;
  await page.evaluate((detail) => window.dispatchEvent(new CustomEvent("approval-fixture", { detail })), update);
  const card = page.locator(".approval");
  await card.locator("summary").click();
  await expect(card.locator("pre")).toHaveText('{ "ids": [ 9007199254740993 ], "amount": 1.00000000000000001 }');
  await card.getByLabel("Allow").check();
  const answer = page.waitForRequest((r) => r.postDataJSON()?.method === "session/approve");
  await card.getByRole("button", { name: "Submit" }).click();
  expect((await answer).postData()).toContain(`"updatedInput":${input}`);
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
