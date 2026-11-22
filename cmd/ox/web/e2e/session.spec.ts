// The web client against the real relay and oxd, with the fake harness,
// which echoes each prompt back a word at a time, or first asks to approve a
// tool call for a prompt "approve <tool> <input JSON>", or also runs a
// background task for a prompt "background <milliseconds>".
import { mkdtempSync } from "node:fs";
import { createConnection } from "node:net";
import { basename } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { expect, type Page, test } from "@playwright/test";
import { fakeConfig, haikuConfig, type Services, startServices } from "./services";
import {
  hold,
  menuItem,
  open,
  openMenu,
  prompt,
  send,
  sessionButton,
  sessionRow,
  stop,
  toolResult,
  workspaceRow,
} from "./ui";

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
  await expect(page.locator(".context h1")).not.toHaveText("New session");
  await prompt(page).pressSequentially(" again");
  firstSend.release();
  await expect(page.locator(".reply")).toHaveText("You said: first prompt");
  await expect(page.locator(".prompt")).toHaveText(["first prompt"]);
  await expect(prompt(page)).toHaveValue("first prompt and more again");

  // Text retyped while a send is in flight stays, even when it equals the
  // prompt sent.
  const retyped = await hold(page, "session/send");
  await prompt(page).fill("same text");
  await prompt(page).press("Enter");
  await retyped.arrived;
  await prompt(page).fill("");
  await prompt(page).fill("same text");
  retyped.release();
  await expect(page.locator(".reply").last()).toHaveText("You said: same text");
  await expect(prompt(page)).toHaveValue("same text");

  // So does text retyped while the session is being created.
  await page.getByRole("button", { name: "New session" }).click();
  const draftCreate = await hold(page, "session/create");
  const draftSend = await hold(page, "session/send");
  await prompt(page).fill("draft text");
  await prompt(page).press("Enter");
  await draftCreate.arrived;
  await prompt(page).fill("");
  await prompt(page).fill("draft text");
  draftCreate.release();
  draftSend.release();
  await expect(page.locator(".reply")).toHaveText("You said: draft text");
  await expect(prompt(page)).toHaveValue("draft text");
});

test("a reply renders as Markdown and its prompt stays plain", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  const text = [
    "**bold** `code` <b>raw</b> [site](https://example.com) [bad](javascript:alert(1)) ![pic](https://example.com/p.png) https://example.org",
    "",
    "- one",
    "- two",
    "",
    "1. first",
    "2. second",
  ].join("\n");
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(stop(page)).toHaveCount(0);
  const reply = page.locator(".reply");
  await expect(reply.locator("strong")).toHaveText("bold");
  await expect(reply.locator("code")).toHaveText("code");
  await expect(reply.locator("ul li")).toHaveText(["one", "two"]);
  await expect(reply.locator("ul")).toHaveCSS("list-style-type", "disc");
  await expect(reply.locator("ol li")).toHaveText(["first", "second"]);
  await expect(reply.locator("ol")).toHaveCSS("list-style-type", "decimal");
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

test("a rejected creation keeps the draft and its settings after switching views", async ({ page }) => {
  await open(page, services);
  await newSession(page, "existing conversation");
  await page.getByRole("button", { name: "New session" }).click();
  // Enter in the draft's settings sends nothing.
  const creates: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/rpc") && request.postDataJSON()?.method === "session/create") {
      creates.push(request.url());
    }
  });
  await prompt(page).fill("keep this draft");
  await page.locator("button.picker").click();
  await page.getByRole("slider", { name: "Effort" }).press("Enter");
  await page.keyboard.press("Escape");
  await page.waitForTimeout(500);
  expect(creates).toEqual([]);
  await expect(page.locator(".context h1")).toHaveText("New session");
  await expect(prompt(page)).toHaveValue("keep this draft");
  const mode = page.locator("select[name=permission_mode]");
  await mode.selectOption("plan");
  const create = await hold(page, "session/create");
  await prompt(page).fill("keep this draft");
  await prompt(page).press("Enter");
  await create.arrived;
  await sessionButton(page, "existing conversation").click();
  create.release({ model: "not-offered" });
  const error = page.getByRole("alert").filter({ hasText: "not-offered" });
  await expect(error).toBeVisible();
  await expect(page.locator(".prompt")).toHaveText(["existing conversation"]);
  await error.getByRole("button", { name: "Reopen draft" }).click();
  await expect(page.getByRole("alert")).toContainText("not-offered");
  await expect(prompt(page)).toHaveValue("keep this draft");
  await expect(mode).toHaveValue("plan");
  await send(page).click();
  await expect(page.locator(".prompt")).toHaveText(["keep this draft"]);
  await expect(mode).toHaveValue("plan");
});

test("a pending configuration shows as pending and holds Send until it is confirmed", async ({ page, context }) => {
  await open(page, services);
  await newSession(page, "configure me");
  const other = await context.newPage();
  await other.goto(page.url());
  const mode = page.locator("select[name=permission_mode]");
  const configure = await hold(page, "session/configure");
  await mode.selectOption("plan");
  await configure.arrived;
  await expect(mode).toHaveAttribute("aria-busy", "true");
  await expect(page.locator(".select .loader")).toBeVisible();
  const picker = page.locator("button.picker");
  await expect(picker).not.toHaveAttribute("aria-busy");
  await other.locator("button.picker").click();
  await other.getByRole("slider", { name: "Effort" }).press("End");
  await expect(picker).toContainText("High");
  await expect(picker).not.toHaveAttribute("aria-busy");
  await expect(mode).toHaveValue("plan");
  await expect(mode).toHaveAttribute("aria-busy", "true");
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
  await expect(send(page)).toBeEnabled({ timeout: 10_000 });
  await expect(prompt(page)).toHaveValue("unsent draft");
  await expect(page.locator(".prompt")).toHaveText(["sent once"]);
  await page.reload();
  await expect(page.locator(".prompt")).toHaveText(["sent once"]);
});

test("a send cut off after acceptance shows an error without resending", async ({ page }) => {
  await cutReply(page, "message_id");
  await open(page, services);
  await newSession(page, "before disconnection");
  const sends: string[] = [];
  page.on("request", (request) => {
    if (request.method() !== "POST" || !request.url().includes("/rpc")) return;
    const body = request.postDataJSON();
    if (body.method === "session/send") sends.push(body.params.text);
  });
  await armCutReply(page);
  await prompt(page).fill("accepted once");
  await prompt(page).press("Enter");
  await expect(page.locator("body")).toHaveAttribute("data-cut-reply", "held");
  await services.restartRelay();
  await expect(page.locator(".log ~ [role=alert]")).toContainText("Lost the connection");
  await expect(send(page)).toBeEnabled();
  await expect(page.locator(".prompt")).toHaveText(["before disconnection", "accepted once"]);
  expect(sends).toEqual(["accepted once"]);
});

test("Shift+Enter inserts a newline and Enter during composition doesn't send", async ({ page }) => {
  await open(page, services);
  await newSession(page, "keyboard test");
  await prompt(page).fill("first line");
  await prompt(page).press("Shift+Enter");
  await prompt(page).pressSequentially("second line");
  await expect(prompt(page)).toHaveValue("first line\nsecond line");
  // Enter must reach the IME rather than being consumed by the send handler.
  const reachesIME = await prompt(page).evaluate((input) =>
    input.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true, cancelable: true }),
    ),
  );
  expect(reachesIME).toBe(true);
  await expect(page.locator(".prompt")).toHaveText(["keyboard test"]);
  await expect(prompt(page)).toHaveValue("first line\nsecond line");
  await prompt(page).press("Enter");
  await expect(page.locator(".prompt")).toHaveText(["keyboard test", "first line second line"]);
});

test("overflowing output follows only while the reader is at the bottom", async ({ page }) => {
  await open(page, services);
  await newSession(page, Array.from({ length: 90 }, (_, i) => `line${i}`).join("\n"));
  const log = page.locator(".log");
  // Measure viewport positions rather than depending on the scroll origin.
  const distance = () =>
    log.evaluate((el) =>
      Math.round(el.firstElementChild!.getBoundingClientRect().bottom - el.getBoundingClientRect().bottom),
    );
  const firstPromptY = () =>
    log
      .locator(".prompt")
      .first()
      .evaluate((el) => el.getBoundingClientRect().top);
  await expect.poll(firstPromptY).toBeLessThan(-100);
  await expect.poll(distance).toBeLessThanOrEqual(1);

  // Growing and shrinking the composer changes the transcript viewport, but
  // is not a scroll gesture and must not stop it following the bottom.
  const height = () => log.evaluate((el) => el.clientHeight);
  const originalHeight = await height();
  await prompt(page).fill("draft line\n".repeat(8));
  await expect.poll(height).toBeLessThan(originalHeight);
  await expect.poll(distance).toBeLessThanOrEqual(1);
  await prompt(page).clear();
  await expect.poll(height).toBe(originalHeight);
  await expect.poll(distance).toBeLessThanOrEqual(1);

  await prompt(page).fill("follow this output");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText("You said: follow this output");
  await expect(stop(page)).toHaveCount(0);
  await expect.poll(distance).toBeLessThanOrEqual(1);

  await log.hover();
  await page.mouse.wheel(0, -20);
  await expect.poll(distance).toBe(20);
  const scrolledUp = await firstPromptY();
  await prompt(page).fill("do not follow this output");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText("You said: do not follow this output");
  await expect(stop(page)).toHaveCount(0);
  await expect.poll(firstPromptY).toBe(scrolledUp);
  expect(await distance()).toBeGreaterThan(20);

  await log.hover();
  await page.mouse.wheel(0, 500);
  await expect.poll(distance).toBeLessThanOrEqual(1);
  await prompt(page).fill("follow again");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply").last()).toHaveText("You said: follow again");
  await expect(stop(page)).toHaveCount(0);
  await expect.poll(distance).toBeLessThanOrEqual(1);
});

test("an oversized prompt fails visibly and stays in the box", async ({ page }) => {
  await open(page, services);
  await newSession(page, "small prompt");
  // The limit is UTF-8 bytes of text, not the length of the JSON request.
  const large = `${"é".repeat(1 << 19)}x`;
  await prompt(page).fill(large);
  await prompt(page).press("Enter");
  await expect(page.getByRole("alert")).toHaveText("prompt is larger than 1 MiB");
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

test("a session in the background shows its status and stays configurable and archivable", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();
  await prompt(page).fill("background 2000");
  await prompt(page).press("Enter");
  await expect(page.locator(".reply")).toHaveText("You said: background 2000");
  const status = sessionRow(page, "background 2000").locator(".status");
  await expect(status).toHaveAttribute("aria-label", "background");
  await expect(page.locator("select[name=permission_mode]")).toBeEnabled();
  await expect(stop(page)).toHaveCount(0);
  await expect(menuItem(await openMenu(sessionRow(page, "background 2000")), "Archive")).toBeEnabled();
  await page.keyboard.press("Escape");
  // Each check above passed while the task still ran.
  expect(await status.getAttribute("aria-label")).toBe("background");
  await expect(status).toHaveAttribute("aria-label", "idle");
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

test("Allow sends the exact numbers displayed in the tool input", async ({ page }) => {
  await open(page, services);
  await page.getByRole("button", { name: "New session" }).click();

  const input = '{"ids":[9007199254740993],"amount":1.00000000000000001}';
  const card = await ask(page, "Example", input);
  await card.locator("summary").click();
  await expect(card.locator("pre")).toHaveText('{ "ids": [ 9007199254740993 ], "amount": 1.00000000000000001 }');
  const exampleTool = page.locator("details.tool").filter({ has: page.locator(".name", { hasText: "Example" }) });
  await expect(exampleTool.locator(".detail")).toHaveText(input);
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

// callTool calls an MCP tool as session from's Claude does, through an MCP
// connection attached to it, and returns the tool's text.
function callTool(from: string, name: string, args: Record<string, string>) {
  return new Promise<string>((resolve, reject) => {
    const socket = createConnection(services.socket);
    const call = (id: number, method: string, params: object) =>
      socket.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    let buffered = "";
    socket.on("data", (data) => {
      buffered += data;
      for (let i = buffered.indexOf("\n"); i >= 0; i = buffered.indexOf("\n")) {
        const line = JSON.parse(buffered.slice(0, i));
        buffered = buffered.slice(i + 1);
        if (line.id !== 2) continue;
        socket.end();
        if (line.error || line.result.isError) reject(new Error(JSON.stringify(line)));
        else resolve(line.result.content[0].text);
      }
    });
    socket.on("error", reject);
    call(1, "mcp/attach", { session_id: from });
    call(2, "tools/call", { name, arguments: args });
  });
}

// sessionId is the ID of the session the page shows.
const sessionId = (page: Page) => decodeURIComponent(new URL(page.url()).hash.slice(1));

// messaging creates sessions "first from A" and "first from B" and leaves B
// waiting on an approval, and returns their IDs.
async function messaging(page: Page) {
  await open(page, services);
  await newSession(page, "first from A");
  const a = sessionId(page);
  await newSession(page, "first from B");
  const b = sessionId(page);
  await ask(page, "Bash", "{}");
  return { a, b };
}

test("all waiting inputs show in order with their send-time labels", async ({ page }) => {
  const { a, b } = await messaging(page);
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  const queued = page.locator(".prompt.queued");
  await expect(queued).toHaveText(["steer"]);
  expect(await callTool(a, "send_message", { to: b, text: "hello from A" })).toBe(`Message queued for ${b}.`);
  await callTool(a, "send_message", { to: b, text: "another from A" });
  const checkPending = async () => {
    const expected = ["steer", "first from Ahello from A", "first from Aanother from A"];
    await expect(queued).toHaveText(expected);
    await expect(page.locator(".transcript > :nth-last-child(-n+3)")).toHaveText(expected);
    await expect(send(page)).toBeDisabled();
  };
  await checkPending();

  // Even deleting the sender doesn't change the label recorded on its messages.
  const sender = sessionRow(page, "first from A");
  await menuItem(await openMenu(sender), "Delete").click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(sender).toHaveCount(0);
  await page.reload();
  await checkPending();
  const card = page.locator(".approval");
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  const message = page.locator(".prompt.message");
  await expect(message.locator(".from")).toHaveText(["first from A", "first from A"]);
  await expect(message).toHaveText(["first from Ahello from A", "first from Aanother from A"]);
  await expect(page.locator(".reply").last()).toContainText("another from A");
  await expect(stop(page)).toHaveCount(0);

  await page.reload();
  await expect(message.locator(".from")).toHaveText(["first from A", "first from A"]);
  await expect(message).toHaveText(["first from Ahello from A", "first from Aanother from A"]);
  await expect(sessionButton(page, "first from B")).toHaveCount(1);
});

test("a recorded message without a saved label still shows its sender", async ({ page }) => {
  await open(page, services);
  await newSession(page, "legacy sender");
  const sender = sessionId(page);
  await newSession(page, "legacy recipient");
  const recipient = sessionId(page);
  await callTool(sender, "send_message", { to: recipient, text: "older message" });
  await expect(page.locator(".reply").last()).toContainText("older message");
  await expect(stop(page)).toHaveCount(0);

  const database = new DatabaseSync(services.database);
  try {
    const changed = database
      .prepare(
        "UPDATE events SET data = json_remove(data, '$.from_label') WHERE session_id = ? AND data ->> 'from' = ?",
      )
      .run(recipient, sender);
    expect(changed.changes).toBe(1);
  } finally {
    database.close();
  }

  const from = page.locator(".prompt.message .from");
  await page.reload();
  await expect(from).toHaveText("legacy sender");

  await menuItem(await openMenu(sessionRow(page, "legacy sender")), "Delete").click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(from).toHaveText(sender.slice(0, 8));
});

test("Stop drops a pending message without touching the composer", async ({ page }) => {
  const { a, b } = await messaging(page);
  await prompt(page).fill("draft");
  await callTool(a, "send_message", { to: b, text: "hello from A" });
  await callTool(a, "send_message", { to: b, text: "another from A" });
  await expect(page.locator(".prompt.queued.message")).toHaveCount(2);
  await stop(page).click();
  await expect(page.locator(".prompt.queued")).toHaveCount(0);
  await expect(page.locator(".prompt.message")).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("draft");
  await expect(page.locator(".error")).toHaveText("Interrupted");
});

for (const outcome of ["taken", "dropped"] as const) {
  test(`a reconnect reconciles a prompt ${outcome} while the page was away`, async ({ page, context }) => {
    await open(page, services);
    await page.getByRole("button", { name: "New session" }).click();
    await ask(page, "Bash", "{}");
    await prompt(page).fill("steer");
    await prompt(page).press("Enter");
    await expect(page.locator(".prompt.queued")).toHaveText("steer");
    const other = await context.newPage();
    await other.goto(page.url());
    await expect(other.locator(".prompt.queued")).toHaveText("steer");

    await expect(prompt(page)).toHaveValue("");
    await prompt(page).fill("next draft");
    // The page stays disconnected while the other page resolves the prompt.
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("alert").filter({ hasText: "Lost the connection" })).toBeVisible();
    // Wait for the other page's new snapshot, not its old approval card or
    // the HTTP acknowledgement of a session/get for some other session.
    await other.reload();
    await expect(other.locator(".prompt.queued")).toHaveText("steer");
    if (outcome === "taken") {
      const card = other.locator(".approval");
      await card.getByLabel("Allow").check();
      await card.getByRole("button", { name: "Submit" }).click();
      await expect(other.locator(".reply").last()).toHaveText("You said: steer");
    } else {
      await stop(other).click();
      await expect(other.locator(".error")).toHaveText("Interrupted");
      await expect(prompt(other)).toHaveValue("");
    }
    await expect(page.locator(".prompt.queued")).toHaveText("steer");

    await page.unroute("**/events");
    await expect(send(page)).toBeEnabled({ timeout: 15_000 });
    const expected = outcome === "taken" ? ["approve Bash {}", "steer"] : ["approve Bash {}"];
    const composer = outcome === "taken" ? "next draft" : "steer\nnext draft";
    await expect(page.locator(".prompt")).toHaveText(expected);
    await expect(prompt(page)).toHaveValue(composer);
    // A later reload of the session must not restore the same input again.
    const snapshot = await hold(page, "session/get", { session_id: sessionId(page) });
    await services.restartRelay();
    await snapshot.arrived;
    snapshot.release();
    await expect(send(page)).toBeEnabled();
    await expect(prompt(page)).toHaveValue(composer);
    await expect(page.locator(".prompt")).toHaveText(expected);
  });
}

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
  await expect(
    page
      .locator("details.tool")
      .filter({ has: page.locator(".name", { hasText: "AskUserQuestion" }) })
      .last()
      .locator(".detail"),
  ).toHaveText("Which color?");
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

test("reselecting a model repairs a retired effort when there is only one choice", async ({ page }) => {
  await open(page, services);
  await newSession(page, "retired effort");
  const picker = page.locator(".settings .picker");
  const effort = page.getByRole("slider", { name: "Effort" });
  await picker.click();
  await effort.press("End");
  await expect(picker).toContainText("High");
  await expect(picker).not.toHaveAttribute("aria-busy");
  await page.keyboard.press("Escape");
  const model = fakeConfig.harnesses.fake.models[0];
  const config = {
    ...fakeConfig,
    harnesses: { fake: { ...fakeConfig.harnesses.fake, models: [{ ...model, efforts: model.efforts.slice(0, 1) }] } },
  };
  await services.restartDaemon(config);
  try {
    await expect(picker).toContainText("high");
    await picker.click();
    await expect(effort).toBeDisabled();
    await page.getByRole("option", { name: "Echo" }).click();
    await expect(picker).toContainText("Low");
    await expect(picker).not.toHaveAttribute("aria-busy");
    await page.reload();
    await expect(picker).toContainText("Low");
  } finally {
    await services.restartDaemon(fakeConfig);
  }
});

// Withhold an accepted reply at the SSE boundary, leaving the call pending.
async function cutReply(page: Page, field: "session_id" | "message_id") {
  await page.addInitScript((field) => {
    const Original = window.EventSource;
    window.EventSource = class extends Original {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        this.addEventListener("message", (event) => {
          if (document.body.dataset.cutReply === "armed" && JSON.parse(event.data).result?.[field]) {
            document.body.dataset.cutReply = "held";
            event.stopImmediatePropagation();
          }
        });
      }
    };
  }, field);
}

const armCutReply = (page: Page) =>
  page.evaluate(() => {
    document.body.dataset.cutReply = "armed";
  });

test("a lost creation reply is recovered after leaving the draft", async ({ page }) => {
  await cutReply(page, "session_id");
  await open(page, services);
  await newSession(page, "another conversation");
  const before = await page.locator("nav button.open").count();
  await page.getByRole("button", { name: "New session" }).click();
  await armCutReply(page);
  await prompt(page).fill("recover my prompt");
  await prompt(page).press("Enter");
  await expect(page.locator("body")).toHaveAttribute("data-cut-reply", "held");
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

test("session list membership and ordering don't wait for transcripts", async ({ page }) => {
  const isolated = await startServices(fakeConfig);
  try {
    await open(page, isolated);
    const firstSnapshot = await hold(page, "session/get");
    await page.getByRole("button", { name: "New session" }).click();
    await prompt(page).fill("older session");
    await prompt(page).press("Enter");
    await firstSnapshot.arrived;
    await expect(page.locator("nav li .title")).toHaveCount(1);
    await expect(menuItem(await openMenu(workspaceRow(page, isolated.workspace)), "Remove workspace")).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("status")).toHaveText("Loading session…");
    const id = sessionId(page);
    await newSession(page, "younger session");
    await expect(page.locator("nav li .title")).toHaveText(["older session", "younger session"]);
    firstSnapshot.release();
    await expect(page.locator("nav li .title")).toHaveText(["older session", "younger session"]);

    const reloadSnapshot = await hold(page, "session/get", { session_id: id });
    await page.reload();
    await reloadSnapshot.arrived;
    await expect(page.locator(".reply")).toHaveText("You said: younger session");
    await expect(page.locator("nav li .title")).toHaveText(["older session", "younger session"]);
    reloadSnapshot.release();
    await expect(page.locator("nav li .title")).toHaveText(["older session", "younger session"]);

    // A hash change in the document selects the session it names.
    await page.evaluate((hash) => {
      location.hash = hash;
    }, encodeURIComponent(id));
    await expect(sessionButton(page, "older session")).toHaveAttribute("aria-current", "page");
    await expect(page.locator(".reply")).toHaveText("You said: older session");
  } finally {
    await isolated.stop();
  }
});

test("a cancelled deletion's reply cannot close the next confirmation", async ({ page }) => {
  await open(page, services);
  await newSession(page, "old deletion");
  await newSession(page, "new deletion");
  const remove = async (label: string) => menuItem(await openMenu(sessionRow(page, label)), "Delete");
  const first = await hold(page, "session/destroy");
  const dialog = page.getByRole("alertdialog");
  await (await remove("old deletion")).click();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await first.arrived;
  await page.keyboard.press("Escape");
  await (await remove("new deletion")).click();
  first.release();
  await expect(sessionButton(page, "old deletion")).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("new deletion");
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(sessionButton(page, "new deletion")).toHaveCount(0);
});

test("on reconnect each session waits for its own settings", async ({ page, context }) => {
  await open(page, services);
  await newSession(page, "available independently");
  await newSession(page, "refresh my settings");
  const id = new URL(page.url()).hash.slice(1);
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator(".reply")).toHaveText("You said: refresh my settings");

  const snapshot = await hold(page, "session/get", { session_id: id });
  await services.restartRelay();
  await snapshot.arrived;
  await other.locator("select[name=permission_mode]").selectOption("plan");
  await expect(other.locator("select[name=permission_mode]")).not.toHaveAttribute("aria-busy");
  await prompt(other).fill("written while reloading");
  await prompt(other).press("Enter");
  await expect(other.locator(".reply").last()).toHaveText("You said: written while reloading");
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("manual");
  await expect(page.locator(".prompt")).toHaveText(["refresh my settings"]);
  await prompt(page).fill("after reload");
  await expect(send(page)).toBeDisabled();
  await sessionButton(page, "available independently").click();
  await expect(send(page)).toBeEnabled();
  await sessionButton(page, "refresh my settings").click();
  snapshot.release();
  await expect(page.locator("select[name=permission_mode]")).toHaveValue("plan");
  await expect(page.locator(".prompt")).toHaveText(["refresh my settings", "written while reloading"]);
  await expect(send(page)).toBeEnabled();
  await prompt(other).fill("written after reloading");
  await prompt(other).press("Enter");
  await expect(page.locator(".prompt")).toHaveText([
    "refresh my settings",
    "written while reloading",
    "written after reloading",
  ]);
});

test("a child session shows beneath its parent with its title", async ({ page }) => {
  await open(page, services);
  await newSession(page, "child parent");
  const parent = sessionId(page);
  // A later top-level session lists after the parent's children.
  await newSession(page, "after the parent");
  const name = basename(mkdtempSync(`${services.workspace}/child-`));
  const created = JSON.parse(
    await callTool(parent, "create_session", {
      name: "helper",
      title: "Helper child",
      message: "hello child",
      dir: name,
    }),
  );
  const directory = `${services.workspace}/${name}`;
  expect(created.directory).toBe(directory);

  const parentRow = page.locator("nav li").filter({ hasText: "child parent" });
  const childRow = page.locator("nav li:has-text('child parent') + li");
  const check = async () => {
    await expect(childRow).toHaveClass(/child/);
    await expect(childRow.locator(".title")).toHaveText("Helper child");
    await expect(sessionButton(page, "Helper child")).toHaveCount(1);
    const parentDelete = menuItem(await openMenu(parentRow), "Delete");
    await expect(parentDelete).toHaveAccessibleName("Delete Delete its child sessions first");
    await expect(parentDelete).toBeDisabled();
    await page.keyboard.press("Escape");
    await childRow.locator("button.open").click();
    await expect(page.locator(".context h1")).toHaveText("Helper child");
    expect(sessionId(page)).toBe(created.session_id);
    await expect(page.locator(".context .path")).toHaveText(directory);
    await expect(page.locator(".prompt.message .from")).toHaveText("child parent");
    await expect(page.locator(".prompt.message")).toHaveText("child parenthello child");
  };
  await check();
  await page.reload();
  await check();

  // The child's own child follows it, indented one level further, and keeps
  // the family from being archived while it waits on an approval.
  await callTool(created.session_id, "create_session", {
    name: "leaf",
    title: "Leaf grandchild",
    message: "hello grandchild",
  });
  const grandchildRow = page.locator("nav li:has-text('child parent') + li + li");
  await expect(grandchildRow.locator(".title")).toHaveText("Leaf grandchild");
  const childTitle = await childRow.locator(".title").boundingBox();
  const grandchildTitle = await grandchildRow.locator(".title").boundingBox();
  expect(grandchildTitle?.x).toBeGreaterThan(childTitle?.x ?? Infinity);
  await grandchildRow.locator("button.open").click();
  await expect(page.locator(".context h1")).toHaveText("Leaf grandchild");
  await expect(stop(page)).toHaveCount(0);
  await prompt(page).fill("approve Bash {}");
  await prompt(page).press("Enter");
  const card = page.locator(".approval");
  await expect(card).toBeVisible();
  const blocked = menuItem(await openMenu(parentRow), "Archive");
  await expect(blocked).toHaveAccessibleName("Archive Wait until it and its child sessions are idle");
  await expect(blocked).toBeDisabled();
  await page.keyboard.press("Escape");
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(stop(page)).toHaveCount(0);
  await childRow.locator("button.open").click();

  // A child archived without its parent hides beneath it, with its own child.
  const archiveChild = menuItem(await openMenu(childRow), "Archive");
  await expect(archiveChild).toBeEnabled();
  await archiveChild.click();
  await expect(sessionButton(page, "Helper child")).toHaveCount(0);
  await expect(sessionButton(page, "Leaf grandchild")).toHaveCount(0);
  await expect(sessionButton(page, "child parent")).toHaveCount(1);
  await expect(page.locator(".composer.archived p")).toHaveText("This session is archived.");

  // Archiving the parent archives the sessions beneath it, and every row hides
  // until shown.
  const archive = menuItem(await openMenu(parentRow), "Archive");
  await expect(archive).toBeEnabled();
  await archive.click();
  await expect(sessionButton(page, "child parent")).toHaveCount(0);
  await expect(sessionButton(page, "Helper child")).toHaveCount(0);
  await expect(sessionButton(page, "Leaf grandchild")).toHaveCount(0);
  await expect(page.locator(".composer.archived p")).toHaveText("This session is archived with its parent.");
  await expect(page.getByRole("button", { name: "Unarchive", exact: true })).toBeDisabled();
  const workspace = workspaceRow(page, services.workspace);
  await menuItem(await openMenu(workspace), "Show archived").click();
  await expect(parentRow).toHaveClass(/archived/);
  await expect(childRow).toHaveClass(/archived/);
  await expect(grandchildRow).toHaveClass(/archived/);
  await expect(childRow.getByRole("img", { name: "archived" })).toHaveCount(1);
  const childMenu = await openMenu(childRow);
  await expect(menuItem(childMenu, "Unarchive")).toHaveAccessibleName("Unarchive Unarchive its parent first");
  await expect(menuItem(childMenu, "Unarchive")).toBeDisabled();
  await expect(menuItem(childMenu, "Delete")).toHaveAccessibleName("Delete Delete its child sessions first");
  await page.keyboard.press("Escape");
  await menuItem(await openMenu(grandchildRow), "Delete").click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(sessionButton(page, "Leaf grandchild")).toHaveCount(0);
  await menuItem(await openMenu(childRow), "Delete").click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(sessionButton(page, "Helper child")).toHaveCount(0);
  await expect(menuItem(await openMenu(parentRow), "Delete")).toBeEnabled();
  await menuItem(page.getByRole("menu"), "Unarchive").click();
  await expect(parentRow).not.toHaveClass(/archived/);
  await menuItem(await openMenu(workspace), "Hide archived").click();
});

test("a child's guide starts at its parent's dot or where its previous sibling branches", async ({ page }) => {
  await open(page, services);
  await newSession(page, "tree root");
  const root = sessionId(page);
  const first = JSON.parse(
    await callTool(root, "create_session", { name: "first", title: "Tree first", message: "hi" }),
  );
  await callTool(first.session_id, "create_session", { name: "leaf", title: "Tree leaf", message: "hi" });
  await callTool(root, "create_session", { name: "second", title: "Tree second", message: "hi" });
  await expect(page.locator("nav li:has-text('tree root') ~ li .title").first()).toHaveText("Tree first");
  await expect(page.locator("nav li:has-text('Tree first') + li .title")).toHaveText("Tree leaf");
  await expect(page.locator("nav li:has-text('Tree leaf') + li .title")).toHaveText("Tree second");

  const dot = (title: string) =>
    sessionRow(page, title)
      .locator(".status")
      .evaluate((status) => {
        const box = status.getBoundingClientRect();
        return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
      });
  const middle = (title: string) =>
    sessionRow(page, title).evaluate((row) => {
      const box = row.getBoundingClientRect();
      return box.top + box.height / 2;
    });
  const guide = (title: string) =>
    sessionRow(page, title).evaluate((row) => {
      const box = row.getBoundingClientRect();
      const style = getComputedStyle(row, "::before");
      return {
        x: box.left + Number.parseFloat(style.left) + 0.5,
        top: box.top + Number.parseFloat(style.top),
        radius: style.borderBottomLeftRadius,
      };
    });
  const rootDot = await dot("tree root");
  const firstDot = await dot("Tree first");
  const firstGuide = await guide("Tree first");
  expect(firstGuide.x).toBeCloseTo(rootDot.x, 0);
  expect(firstGuide.top).toBeCloseTo(rootDot.y + 4, 0);
  expect(firstGuide.radius).toBe("0px");
  const leafGuide = await guide("Tree leaf");
  expect(leafGuide.x).toBeCloseTo(firstDot.x, 0);
  expect(leafGuide.top).toBeCloseTo(firstDot.y + 4, 0);
  expect(leafGuide.radius).toBe("6px");
  const secondGuide = await guide("Tree second");
  expect(secondGuide.x).toBeCloseTo(rootDot.x, 0);
  expect(secondGuide.top).toBeCloseTo(await middle("Tree first"), 0);
  expect(secondGuide.radius).toBe("6px");
});

test("an archived session is read-only and hidden until shown", async ({ page, context }) => {
  await open(page, services);
  await newSession(page, "archive me");
  const row = sessionRow(page, "archive me");
  const workspace = workspaceRow(page, services.workspace);
  await prompt(page).fill('approve Bash {"command":"ls"}');
  await prompt(page).press("Enter");
  const card = page.locator(".approval");
  await expect(card).toBeVisible();
  const busy = menuItem(await openMenu(row), "Archive");
  await expect(busy).toHaveAccessibleName("Archive Wait until it is idle");
  await expect(busy).toBeDisabled();
  await page.keyboard.press("Escape");
  await card.getByLabel("Allow").check();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(stop(page)).toHaveCount(0);
  const offline = await hold(page, "workspace/list");
  await services.restartRelay();
  await offline.arrived;
  await expect(menuItem(await openMenu(row), "Archive")).toHaveAccessibleName("Archive Not connected");
  await page.keyboard.press("Escape");
  offline.release();

  await prompt(page).fill("kept while archived");
  const archive = menuItem(await openMenu(row), "Archive");
  await expect(archive).toBeEnabled();
  const failed = await hold(page, "session/archive");
  await archive.click();
  await failed.arrived;
  failed.fail("relay unavailable");
  await expect(page.locator("nav [role=alert]")).toHaveText("relay unavailable");
  await menuItem(await openMenu(row), "Archive").click();
  await expect(row).toHaveCount(0);
  await expect(page.locator("nav [role=alert]")).toHaveCount(0);
  const archivedLine = page.locator(".composer.archived p");
  await expect(archivedLine).toHaveText("This session is archived.");
  await expect(prompt(page)).toHaveCount(0);
  await expect(page.locator("select[name=permission_mode]")).toHaveCount(0);
  await expect(page.locator(".reply").last()).toHaveText('You said: approve Bash {"command":"ls"}');

  const other = await context.newPage();
  await other.goto(page.url());
  await expect(other.locator(".composer.archived p")).toHaveText("This session is archived.");
  await expect(prompt(other)).toHaveCount(0);
  await other.close();

  await menuItem(await openMenu(workspace), "Show archived").click();
  await expect(row).toHaveClass(/archived/);
  // Hiding the session list keeps its choice to show archived sessions.
  await page.getByRole("button", { name: "Toggle sessions" }).click();
  await expect(row).toBeHidden();
  await page.getByRole("button", { name: "Toggle sessions" }).click();
  await expect(row).toHaveClass(/archived/);
  await expect(row.getByRole("img", { name: "archived" })).toHaveCount(1);
  const unarchive = page.getByRole("button", { name: "Unarchive", exact: true });
  const reconnect = await hold(page, "workspace/list");
  await services.restartRelay();
  await reconnect.arrived;
  await expect(menuItem(await openMenu(row), "Unarchive")).toHaveAccessibleName("Unarchive Not connected");
  await page.keyboard.press("Escape");
  await expect(unarchive).toBeDisabled();
  reconnect.release();
  await expect(unarchive).toBeEnabled();
  // Opening another menu while one is open shows the new one.
  await openMenu(row);
  await expect((await openMenu(workspace)).getByRole("menuitem")).toHaveText(["Hide archived", /^Remove workspace/]);
  // The first menu reports its close after the second opens.
  await page.waitForTimeout(100);
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  // A menu opened near the window's bottom edge moves up to end at it.
  const viewport = page.viewportSize();
  const rowBox = await row.boundingBox();
  if (!viewport || !rowBox) throw new Error("no viewport or row box");
  const height = Math.ceil(rowBox.y + rowBox.height) + 4;
  await page.setViewportSize({ width: viewport.width, height });
  await row.click({ button: "right", position: { x: 10, y: rowBox.height - 2 } });
  const menu = page.getByRole("menu");
  await expect(menu.getByRole("menuitem")).toHaveText(["Unarchive", "Delete"]);
  const menuBox = await menu.boundingBox();
  if (!menuBox) throw new Error("no menu box");
  expect(menuBox.y + menuBox.height).toBeCloseTo(height, 0);
  // A reason that appears while the menu is open keeps it on screen.
  const lost = await hold(page, "workspace/list");
  await services.restartRelay();
  await lost.arrived;
  await expect(menuItem(menu, "Unarchive")).toHaveAccessibleName("Unarchive Not connected");
  await expect
    .poll(async () => {
      const box = await menu.boundingBox();
      return box && Math.abs(box.y + box.height - height);
    })
    .toBeLessThanOrEqual(1);
  await page.keyboard.press("Escape");
  lost.release();
  await page.setViewportSize(viewport);
  await menuItem(await openMenu(workspace), "Hide archived").click();
  await expect(row).toHaveCount(0);

  await unarchive.click();
  await expect(prompt(page)).toHaveValue("kept while archived");
  await expect(row).not.toHaveClass(/archived/);

  // An archive this page missed while disconnected shows once it reconnects.
  await page.route("**/events", (route) => route.abort());
  await services.restartRelay();
  const archiver = await context.newPage();
  await archiver.goto(page.url());
  await menuItem(await openMenu(sessionRow(archiver, "archive me")), "Archive").click();
  await expect(archiver.locator(".composer.archived p")).toBeVisible();
  await archiver.close();
  await page.unroute("**/events");
  await expect(archivedLine).toBeVisible();
  await menuItem(await openMenu(workspace), "Show archived").click();
  await expect(row).toHaveCount(1);
  await page.reload();
  await expect(archivedLine).toBeVisible();
  await expect(row).toHaveCount(0);
});

test("workspace headers keep their height when archived sessions overflow the sidebar", async ({ page }) => {
  await open(page, services);
  await newSession(page, "overflow archived");
  const row = sessionRow(page, "overflow archived");
  await menuItem(await openMenu(row), "Archive").click();
  await expect(row).toHaveCount(0);
  await menuItem(await openMenu(workspaceRow(page, services.workspace)), "Show archived").click();
  await expect(row).toHaveClass(/archived/);
  await page.setViewportSize({ width: 1280, height: 100 });
  const sidebar = page.locator("nav");
  expect(await sidebar.evaluate((nav) => nav.scrollHeight > nav.clientHeight)).toBe(true);
  for (const header of await page.locator("nav > p").all()) {
    expect((await header.boundingBox())?.height).toBe(28);
  }
});
