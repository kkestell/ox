// The web client against the real relay and oxd, with the fake harness,
// which echoes each prompt back a word at a time, or first asks to approve a
// tool call for a prompt "approve <tool> <input JSON>", or first streams
// <text> as reasoning for a prompt "think <text>", or also runs a background
// task for a prompt "background <milliseconds>".
import { mkdtempSync } from "node:fs";
import { basename } from "node:path";
import type { Locator, Page, Route } from "@playwright/test";
import type { Services } from "./services";
import { fakeConfig, haikuConfig } from "./services";
import {
  addWorkspace,
  allow,
  ask,
  cutReply,
  edges,
  errors,
  expect,
  heading,
  hold,
  menuItem,
  model,
  newSession,
  newSessionButton,
  openKeyboard,
  openMenu,
  pending,
  permissionMode,
  prompt,
  prompts,
  replies,
  rpcCalls,
  send,
  sessionButton,
  sessionId,
  sessionItem,
  sessions,
  setSafeArea,
  stop,
  test,
  toolCall,
  transcript,
  waiting,
  workspaceSection,
} from "./ui";

const main = (page: Page) => page.getByRole("main");
const words = (n: number) => Array.from({ length: n }, (_, i) => `word${i}`).join(" ");

// expectSidesAligned checks that the session view's parts start 1rem from the
// pane's left edge and end 1rem from its right edge. The files toggle's glyph
// sits 1rem from the right edge of its header. On a phone the sessions toggle's
// glyph sits on the left edge, and the title follows it.
async function expectSidesAligned(page: Page, phone: boolean) {
  await newSession(page, "line up");
  const pane = await main(page).boundingBox();
  if (!pane) throw new Error("no main box");
  const left = pane.x + 16;
  const right = pane.x + pane.width - 16;
  const near = (actual: number, expected: number) => expect(Math.abs(actual - expected)).toBeLessThanOrEqual(1);
  const box = async (locator: Locator) => {
    const b = await locator.boundingBox();
    if (!b) throw new Error("no box");
    return b;
  };
  // On a phone the sessions toggle sits left of the title.
  if (!phone) near((await box(heading(page))).x, left);
  // The folder row's content starts after its padding.
  const row = page.locator(".context .path");
  const padding = await row.evaluate((el) => Number.parseFloat(getComputedStyle(el).paddingLeft));
  near((await box(row)).x + padding, left);
  const reply = await box(replies(page).last());
  near(reply.x, left);
  near(reply.x + reply.width, right);
  const form = await box(main(page).locator(".composer .box"));
  near(form.x, left);
  near(form.x + form.width, right);
  near((await box(model(page).locator(".harness-icon"))).x, left);
  // A toggle's glyph is the 1rem square at its center. The files toggle sits in
  // the session's header on a phone and in the file tree's header on a wide
  // screen.
  const glyph = async (button: Locator) => {
    const b = await box(button);
    return b.x + (b.width - 16) / 2;
  };
  const files = page.getByRole("button", { name: "Toggle files" });
  const header = await box(files.locator("xpath=ancestor::header[1]"));
  near((await glyph(files)) + 16, header.x + header.width - 16);
  if (phone) near(await glyph(page.getByRole("button", { name: "Toggle sessions" })), left);
}

test("the header, transcript, composer, and settings share one side inset", async ({ ox: page }) => {
  await expectSidesAligned(page, false);
});

test("edits typed while a new session is created and sent stay in the composer", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const create = await hold(page, "session/create");
  const firstSend = await hold(page, "session/send");
  await prompt(page).fill("first prompt");
  await prompt(page).press("Enter");
  await create.arrived;
  await prompt(page).pressSequentially(" and more");
  create.release();
  await firstSend.arrived;
  await expect(heading(page)).not.toHaveText("New session");
  await prompt(page).pressSequentially(" again");
  firstSend.release();
  await expect(replies(page)).toHaveText(["You said: first prompt"]);
  await expect(prompts(page)).toHaveText(["first prompt"]);
  await expect(prompt(page)).toHaveValue("first prompt and more again");
});

test("text retyped equal to the sent prompt stays after the send", async ({ ox: page }) => {
  await newSession(page, "first prompt");
  const retyped = await hold(page, "session/send");
  await prompt(page).fill("same text");
  await prompt(page).press("Enter");
  await retyped.arrived;
  await prompt(page).fill("");
  await prompt(page).fill("same text");
  retyped.release();
  await expect(replies(page).last()).toHaveText("You said: same text");
  await expect(prompt(page)).toHaveValue("same text");
});

test("text retyped equal to the prompt stays after the creation", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const create = await hold(page, "session/create");
  const firstSend = await hold(page, "session/send");
  await prompt(page).fill("draft text");
  await prompt(page).press("Enter");
  await create.arrived;
  await prompt(page).fill("");
  await prompt(page).fill("draft text");
  create.release();
  firstSend.release();
  await expect(replies(page)).toHaveText(["You said: draft text"]);
  await expect(prompt(page)).toHaveValue("draft text");
});

// reply sends text as a new session's first prompt and returns its reply
// once it has ended.
async function reply(page: Page, text: string) {
  await newSessionButton(page).click();
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(stop(page)).toHaveCount(0);
  await expect(replies(page)).toHaveCount(1);
  return replies(page);
}

test("a reply renders Markdown", async ({ ox: page }) => {
  const markdown = await reply(page, ["**bold** `code`", "", "- one", "- two", "", "1. first", "2. second"].join("\n"));
  await expect(markdown.getByRole("strong")).toHaveText("bold");
  await expect(markdown.getByRole("code")).toHaveText("code");
  const lists = markdown.getByRole("list");
  await expect(lists).toHaveCount(2);
  await expect(lists.first().getByRole("listitem")).toHaveText(["one", "two"]);
  await expect(lists.last().getByRole("listitem")).toHaveText(["first", "second"]);
});

test("a reply shows raw HTML as text, loads no images, and opens safe links in a new tab", async ({ ox: page }) => {
  const markdown = await reply(
    page,
    "<b>raw</b> [site](https://example.com) [bad](javascript:alert(1)) ![pic](https://example.com/p.png) https://example.org",
  );
  await expect(markdown).toContainText("<b>raw</b>");
  await expect(markdown.getByRole("img")).toHaveCount(0);
  await expect(markdown.getByRole("link", { name: "pic", exact: true })).toHaveAttribute(
    "href",
    "https://example.com/p.png",
  );
  const site = markdown.getByRole("link", { name: "site", exact: true });
  await expect(site).toHaveAttribute("href", "https://example.com");
  await expect(site).toHaveAttribute("target", "_blank");
  await expect(site).toHaveAttribute("rel", "noopener noreferrer");
  await expect(markdown).toContainText("[bad](javascript:alert(1))");
  await expect(markdown.getByRole("link", { name: "bad" })).toHaveCount(0);
  await expect(markdown.getByRole("link", { name: "https://example.org" })).toHaveAttribute(
    "href",
    /^https:\/\/example\.org\/?$/,
  );
});

test("a prompt shows as the plain text sent", async ({ ox: page }) => {
  await reply(page, "**bold** `code` <b>raw</b>");
  await expect(prompts(page)).toHaveText(["**bold** `code` <b>raw</b>"]);
});

test("a failed first send is retried in the session with its settings", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await permissionMode(page).selectOption("plan");
  const firstSend = await hold(page, "session/send");
  await prompt(page).fill("retry me");
  await prompt(page).press("Enter");
  await firstSend.arrived;
  firstSend.fail("relay unavailable");
  await expect(main(page).getByRole("alert")).toHaveText("relay unavailable");
  await expect(prompt(page)).toHaveValue("retry me");
  await expect(permissionMode(page)).toHaveValue("plan");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: retry me"]);
  await expect(prompts(page)).toHaveText(["retry me"]);
  await expect(main(page).getByRole("alert")).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("");
});

test("a session created after the user moves on leaves the selection alone", async ({ ox: page }) => {
  await newSession(page, "elsewhere");
  await newSessionButton(page).click();
  const create = await hold(page, "session/create");
  await prompt(page).fill("background start");
  await prompt(page).press("Enter");
  await create.arrived;
  await sessionButton(page, "elsewhere").click();
  create.release();
  await expect(sessionButton(page, "background start")).toBeVisible();
  await expect(prompts(page)).toHaveText(["elsewhere"]);
  await sessionButton(page, "background start").click();
  await expect(replies(page)).toHaveText(["You said: background start"]);
});

test("Enter in the new-session settings sends nothing", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const creates = rpcCalls(page, "session/create");
  await prompt(page).fill("keep this draft");
  await model(page).click();
  await page.getByRole("slider", { name: "Effort" }).press("Enter");
  await page.keyboard.press("Escape");
  await expect(heading(page)).toHaveText("New session");
  await expect(prompt(page)).toHaveValue("keep this draft");
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: keep this draft"]);
  expect(creates).toHaveLength(1);
});

test("a rejected creation keeps the draft and its settings after switching views", async ({ ox: page }) => {
  await newSession(page, "existing conversation");
  await newSessionButton(page).click();
  await permissionMode(page).selectOption("plan");
  const create = await hold(page, "session/create");
  await prompt(page).fill("keep this draft");
  await prompt(page).press("Enter");
  await create.arrived;
  await sessionButton(page, "existing conversation").click();
  create.release({ model: "not-offered" });
  const error = page.getByRole("alert").filter({ hasText: "not-offered" });
  await expect(error).toBeVisible();
  await expect(prompts(page)).toHaveText(["existing conversation"]);
  await error.getByRole("button", { name: "Reopen draft" }).click();
  await expect(main(page).getByRole("alert")).toContainText("not-offered");
  await expect(prompt(page)).toHaveValue("keep this draft");
  await expect(permissionMode(page)).toHaveValue("plan");
  await send(page).click();
  await expect(prompts(page)).toHaveText(["keep this draft"]);
  await expect(permissionMode(page)).toHaveValue("plan");
});

test("a pending setting is marked pending and holds Send until confirmed", async ({ ox: page }) => {
  await newSession(page, "configure me");
  const sends = rpcCalls(page, "session/send");
  const configure = await hold(page, "session/configure");
  await permissionMode(page).selectOption("plan");
  await configure.arrived;
  await expect(permissionMode(page)).toHaveAttribute("aria-busy", "true");
  await prompt(page).fill("after configuring");
  await expect(send(page)).toBeDisabled();
  await prompt(page).press("Enter");
  configure.release();
  await expect(permissionMode(page)).not.toHaveAttribute("aria-busy");
  await expect(permissionMode(page)).toHaveValue("plan");
  await expect(send(page)).toBeEnabled();
  await expect(prompts(page)).toHaveText(["configure me"]);
  expect(sends).toEqual([]);
});

test.describe("with two models", () => {
  const fake = fakeConfig.harnesses.fake;
  test.use({
    config: {
      ...fakeConfig,
      harnesses: {
        fake: {
          ...fake,
          models: [
            ...fake.models,
            {
              id: "other",
              name: "Other",
              efforts: [
                { id: "medium", name: "Medium" },
                { id: "high", name: "High" },
              ],
            },
          ],
        },
      },
    },
  });

  test("picking a model sends the model alone, and the session ends with the effort oxd chose", async ({
    ox: page,
  }) => {
    await newSession(page, "configure me");
    const configures = rpcCalls(page, "session/configure");
    await model(page).click();
    await page.getByRole("option", { name: "Other" }).click();
    await expect(model(page)).toContainText("Medium");
    await expect(model(page)).not.toHaveAttribute("aria-busy");
    expect(configures).toEqual([{ session_id: sessionId(page), model: "other" }]);
  });

  test("Enter while composing in the model search does not choose the match", async ({ ox: page }) => {
    await newSessionButton(page).click();
    await model(page).click();
    const search = page.getByRole("combobox", { name: "Search models" });
    await search.fill("other");
    await search.dispatchEvent("keydown", { key: "Enter", isComposing: true });
    await page.keyboard.press("Escape");
    await expect(model(page)).toContainText("Echo");
  });
});

test("a failed setting change shows the error and the settings Claude has", async ({ ox: page }) => {
  await newSession(page, "configure me");
  const failed = await hold(page, "session/configure");
  await permissionMode(page).selectOption("plan");
  await failed.arrived;
  await expect(permissionMode(page)).toHaveAttribute("aria-busy", "true");
  failed.fail("configuration refused");
  await expect(main(page).getByRole("alert")).toHaveText("configuration refused");
  await expect(permissionMode(page)).toHaveValue("manual");
  await expect(permissionMode(page)).not.toHaveAttribute("aria-busy");
});

test("another page's change during a pending one shows without being marked pending", async ({ ox: page, context }) => {
  await newSession(page, "configure me");
  const other = await context.newPage();
  await other.goto(page.url());
  const configure = await hold(page, "session/configure");
  await permissionMode(page).selectOption("plan");
  await configure.arrived;
  await expect(permissionMode(page)).toHaveAttribute("aria-busy", "true");
  await expect(model(page)).not.toHaveAttribute("aria-busy");
  await model(other).click();
  await other.getByRole("slider", { name: "Effort" }).press("End");
  await expect(model(page)).toContainText("High");
  await expect(model(page)).not.toHaveAttribute("aria-busy");
  await expect(permissionMode(page)).toHaveValue("plan");
  await expect(permissionMode(page)).toHaveAttribute("aria-busy", "true");
  configure.release();
  await expect(permissionMode(page)).toHaveValue("plan");
  await expect(permissionMode(page)).not.toHaveAttribute("aria-busy");
});

test("a reconnect keeps typed text and sends nothing again", async ({ ox: page, services }) => {
  await newSession(page, "sent once");
  await prompt(page).fill("unsent draft");
  await services.restartRelay();
  await expect(main(page).getByRole("alert")).toHaveCount(0, { timeout: 10_000 });
  await expect(send(page)).toBeEnabled({ timeout: 10_000 });
  await expect(prompt(page)).toHaveValue("unsent draft");
  await expect(prompts(page)).toHaveText(["sent once"]);
  await page.reload();
  await expect(prompts(page)).toHaveText(["sent once"]);
});

// open loads the page with the services' workspace added, for a test that
// prepares the page before it loads.
async function open(page: Page, services: Services) {
  await page.goto(services.url);
  await addWorkspace(page, services.workspace);
}

test("a send cut off after acceptance shows an error without resending", async ({ page, services }) => {
  const cutoff = await cutReply(page, "message_id");
  await open(page, services);
  await newSession(page, "before disconnection");
  const sends = rpcCalls(page, "session/send");
  await cutoff.arm();
  await prompt(page).fill("accepted once");
  await prompt(page).press("Enter");
  await cutoff.cut;
  await services.restartRelay();
  // Once reconnected, the send's error is the one left.
  await expect(send(page)).toBeEnabled();
  await expect(main(page).getByRole("alert")).toHaveText(["Lost the connection to ox. Reconnecting."]);
  await expect(prompts(page)).toHaveText(["before disconnection", "accepted once"]);
  expect(sends.map((params) => params.text)).toEqual(["accepted once"]);
});

test("the page links a manifest that opens Ox standalone with its icon", async ({ page, services }) => {
  await page.goto(services.url);
  const href = await page.locator("link[rel=manifest]").getAttribute("href");
  const manifestResponse = await page.request.get(new URL(href ?? "", services.url).href);
  expect(manifestResponse.ok()).toBe(true);
  const manifest = await manifestResponse.json();
  expect(manifest).toMatchObject({ name: "Ox", display: "standalone", start_url: "/" });
  const icon = await page.request.get(new URL(manifest.icons[0].src, services.url).href);
  expect(icon.status()).toBe(200);
  expect(icon.headers()["content-type"]).toBe("image/png");
});

test("reasoning shows in the transcript, before and after a reload", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await prompt(page).fill("think Count to **three**.");
  await prompt(page).press("Enter");
  await expect(replies(page).last()).toHaveText("You said: think Count to three.");
  const thinking = transcript(page).getByRole("article", { name: "Thinking" });
  await expect(thinking).toHaveText("Count to three.");
  await expect(thinking.locator("strong")).toHaveText("three");

  await page.reload();
  await expect(replies(page).last()).toHaveText("You said: think Count to three.");
  await expect(thinking).toHaveText("Count to three.");
});

test("opening and closing a tool call keeps its summary in place", async ({ ox: page }) => {
  // A prompt and reply that overflow the transcript, so it follows its bottom.
  await newSession(page, Array.from({ length: 12 }, (_, i) => `Sheep${i}.`).join("\n\n"));
  await allow(await ask(page, "Bash", '{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6}'));
  await expect(stop(page)).toHaveCount(0);
  const tool = toolCall(page, "Bash");
  const input = tool.getByText('"f": 6');
  const summary = tool.getByText("Bash", { exact: true });
  await expect(summary).toBeInViewport();
  const top = async () => (await summary.boundingBox())?.y;
  const before = await top();
  // Below the summary, the card would unfold upward and push the summary out of view.
  await summary.click();
  await expect(input).toBeVisible();
  await expect.poll(top).toBe(before);
  await summary.click();
  await expect(input).toBeHidden();
  await expect.poll(top).toBe(before);
});

test("a reconnecting stream shows as a status, not an error", async ({ ox: page, services }) => {
  await newSession(page, "still here");
  const drop = (route: Route) => route.abort();
  await page.route("**/events", drop);
  await services.restartRelay();
  await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.unroute("**/events", drop);
  await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toHaveCount(0, { timeout: 10_000 });
  await expect(send(page)).toBeEnabled();
});

test("a closed stream offers a Reload button that reconnects", async ({ ox: page, services }) => {
  await newSession(page, "reload me");
  const refuse = (route: Route) => route.fulfill({ status: 502 });
  await page.route("**/events", refuse);
  await services.restartRelay();
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("Lost the connection to ox.");
  await expect(alert).not.toContainText("Reconnecting");
  await page.unroute("**/events", refuse);
  await alert.getByRole("button", { name: "Reload" }).click();
  await expect(alert).toHaveCount(0);
  await expect(send(page)).toBeEnabled();
});

test("Shift+Enter starts a new line", async ({ ox: page }) => {
  await newSession(page, "keyboard test");
  await prompt(page).fill("first line");
  await prompt(page).press("Shift+Enter");
  await prompt(page).pressSequentially("second line");
  await expect(prompt(page)).toHaveValue("first line\nsecond line");
  await expect(prompts(page)).toHaveText(["keyboard test"]);
});

test("Enter during input-method composition does not send", async ({ ox: page }) => {
  await newSession(page, "keyboard test");
  const cdp = await page.context().newCDPSession(page);
  await prompt(page).focus();
  await cdp.send("Input.imeSetComposition", { text: "にほん", selectionStart: 3, selectionEnd: 3 });
  await page.keyboard.press("Enter");
  await cdp.send("Input.insertText", { text: "日本" });
  await prompt(page).press("Enter");
  await expect(prompts(page)).toHaveText(["keyboard test", "日本"]);
});

// overflowing starts a session whose first prompt overflows the transcript.
async function overflowing(page: Page) {
  await newSession(page, Array.from({ length: 90 }, (_, i) => `line${i}`).join("\n"));
  await expect(replies(page).last()).toBeInViewport();
  await expect(prompts(page).first()).not.toBeInViewport({ ratio: 1 });
}

// sendAndWait sends text in the current session and waits for its reply.
async function sendAndWait(page: Page, text: string) {
  await prompt(page).fill(text);
  await prompt(page).press("Enter");
  await expect(replies(page).last()).toHaveText(`You said: ${text}`);
  await expect(stop(page)).toHaveCount(0);
}

test("output follows at the bottom", async ({ ox: page }) => {
  await overflowing(page);
  await sendAndWait(page, "follow this output");
  await expect(replies(page).last()).toBeInViewport();
});

test("resizing the composer keeps following", async ({ ox: page }) => {
  await overflowing(page);
  await prompt(page).fill("draft line\n".repeat(8));
  await expect(replies(page).last()).toBeInViewport();
  await prompt(page).clear();
  await expect(replies(page).last()).toBeInViewport();
});

test("output does not follow while scrolled up", async ({ ox: page }) => {
  await overflowing(page);
  await transcript(page).hover();
  await page.mouse.wheel(0, -500);
  await expect(replies(page).last()).not.toBeInViewport();
  await sendAndWait(page, "do not follow this output");
  await expect(replies(page).last()).not.toBeInViewport();
});

test("scrolling back to the bottom resumes following", async ({ ox: page }) => {
  await overflowing(page);
  await transcript(page).hover();
  await page.mouse.wheel(0, -500);
  await expect(replies(page).last()).not.toBeInViewport();
  await page.mouse.wheel(0, 5000);
  await expect(replies(page).last()).toBeInViewport();
  await sendAndWait(page, "follow again");
  await expect(replies(page).last()).toBeInViewport();
});

test("an oversized prompt fails visibly and stays in the box", async ({ ox: page }) => {
  await newSession(page, "small prompt");
  // The limit is UTF-8 bytes of text, not the length of the JSON request.
  const large = `${"é".repeat(1 << 19)}x`;
  await prompt(page).fill(large);
  await prompt(page).press("Enter");
  await expect(main(page).getByRole("alert")).toHaveText("prompt is larger than 1 MiB");
  await expect(send(page)).toBeEnabled();
  await expect(prompt(page)).toHaveValue(large);
});

test("Stop appears once the page's send is accepted, and stops it", async ({ ox: page }) => {
  await newSession(page, "earlier prompt");
  const held = await hold(page, "session/send");
  await prompt(page).fill(words(200));
  await prompt(page).press("Enter");
  await held.arrived;
  await expect(send(page)).toBeDisabled();
  await expect(stop(page)).toHaveCount(0);
  held.release();
  await stop(page).click();
  await expect(errors(page)).toHaveText(["Interrupted"]);
  await expect(stop(page)).toHaveCount(0);
});

test("a session in the background shows its status and stays configurable and archivable", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await prompt(page).fill("background 60000");
  await prompt(page).press("Enter");
  await expect(replies(page)).toHaveText(["You said: background 60000"]);
  await expect(sessionButton(page, "background 60000")).toHaveAccessibleDescription("background");
  await expect(permissionMode(page)).toBeEnabled();
  await expect(stop(page)).toHaveCount(0);
  const archive = menuItem(await openMenu(sessionItem(page, "background 60000")), "Archive");
  await expect(archive).toBeEnabled();
  await archive.click();
  await expect(sessionButton(page, "background 60000")).toHaveCount(0);
});

test("a send held in one session leaves another usable", async ({ ox: page }) => {
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
  await expect(replies(page).last()).toHaveText("You said: free prompt");
  held.release();
  await sessionButton(page, "first session").click();
  await expect(replies(page).last()).toHaveText("You said: held prompt");
});

test("Allow sends the exact numbers displayed in the tool input", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const input = '{"ids":[9007199254740993],"amount":1.00000000000000001}';
  const approval = await ask(page, "Example", input);
  await approval.getByRole("group").click();
  await expect(
    approval.getByText('{ "ids": [ 9007199254740993 ], "amount": 1.00000000000000001 }', { exact: true }),
  ).toBeVisible();
  await expect(toolCall(page, "Example")).toContainText(input);
  await allow(approval);
  await expect(approval).toHaveCount(0);
  await expect(toolCall(page, "Example")).toContainText(`{"behavior":"allow","updatedInput":${input}}`);
  await expect(stop(page)).toHaveCount(0);
});

test("Ox's own tools show readable names and the message they send", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const approval = await ask(page, "mcp__ox__send_message", '{"to":"some-session","text":"hello there"}');
  await expect(approval.getByText("Allow SendMessage?")).toBeVisible();
  await expect(approval.getByText("hello there", { exact: true })).toBeVisible();
  const call = toolCall(page, "SendMessage");
  await expect(call).toContainText("SendMessage");
  await expect(call.locator("summary")).toContainText("hello there");
  await expect(call.locator("summary")).not.toContainText("mcp__ox__");
  await expect(call.locator("summary")).not.toContainText("some-session");
  await allow(approval);
  await expect(approval).toHaveCount(0);
  await expect(stop(page)).toHaveCount(0);

  const empty = await ask(page, "mcp__ox__list_sessions", "{}");
  await expect(empty.getByText("Allow ListSessions?")).toBeVisible();
  await expect(empty.locator("summary")).toHaveText("Input");
  await expect(toolCall(page, "ListSessions").locator("summary")).not.toContainText("{}");
});

test("a prompt sent while Claude works stays pending until Claude takes it", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await ask(page, "Bash", "{}");
  await expect(stop(page)).toBeVisible();
  await expect(send(page)).toBeVisible();
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(prompt(page)).toHaveValue("");
  await expect(pending(page).getByRole("article")).toHaveText(["steer"]);
  await expect(send(page)).toBeDisabled();

  await page.reload();
  await expect(pending(page).getByRole("article")).toHaveText(["steer"]);
  await allow(waiting(page).getByRole("article"));
  await expect(pending(page)).toHaveCount(0);
  await expect(transcript(page).getByRole("article")).toHaveText(["approve Bash {}", "steer", "You said: steer"]);
});

test("Stop drops a pending prompt and returns its text to the composer", async ({ ox: page }) => {
  await newSessionButton(page).click();
  await ask(page, "Bash", "{}");
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(pending(page).getByRole("article")).toHaveText(["steer"]);
  await expect(prompt(page)).toHaveValue("");
  await prompt(page).fill("more");
  await stop(page).click();
  await expect(pending(page)).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("steer\nmore");
  await expect(errors(page)).toHaveText(["Interrupted"]);
});

test("a dropped prompt returns only to the page that sent it", async ({ ox: page, context }) => {
  await newSessionButton(page).click();
  await ask(page, "Bash", "{}");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(waiting(other).getByRole("article")).toHaveCount(1);
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(prompt(page)).toHaveValue("");
  await expect(pending(other).getByRole("article")).toHaveText(["steer"]);
  await stop(other).click();
  await expect(pending(page)).toHaveCount(0);
  await expect(pending(other)).toHaveCount(0);
  await expect(errors(other)).toHaveText(["Interrupted"]);
  await expect(prompt(page)).toHaveValue("steer");
  await expect(prompt(other)).toHaveValue("");
});

// messaging creates sessions "first from A" and "first from B" and leaves B
// waiting on an approval, and returns their IDs.
async function messaging(page: Page) {
  await newSession(page, "first from A");
  const a = sessionId(page);
  await newSession(page, "first from B");
  const b = sessionId(page);
  await ask(page, "Bash", "{}");
  return { a, b };
}

const fromA = (page: Page) => transcript(page).getByRole("article", { name: "Message from first from A" });

test("pending inputs show in order at the transcript's end and hold Send", async ({ ox: page, services }) => {
  const { a, b } = await messaging(page);
  await prompt(page).fill("steer");
  await prompt(page).press("Enter");
  await expect(pending(page).getByRole("article")).toHaveText(["steer"]);
  expect(await services.callTool(a, "send_message", { to: b, text: "hello from A" })).toBe(`Message queued for ${b}.`);
  await services.callTool(a, "send_message", { to: b, text: "another from A" });
  const queued = ["steer", "first from Ahello from A", "first from Aanother from A"];
  await expect(pending(page).getByRole("article", { name: "Prompt" })).toHaveText(["steer"]);
  await expect(pending(page).getByRole("article", { name: "Message from first from A" })).toHaveText(queued.slice(1));
  await expect(pending(page).getByRole("article")).toHaveText(queued);
  await expect(transcript(page).getByRole("article")).toHaveText([
    "first from B",
    "You said: first from B",
    "approve Bash {}",
    ...queued,
  ]);
  await expect(send(page)).toBeDisabled();
});

test("a pending message keeps its sender's label after the sender is deleted", async ({ ox: page, services }) => {
  const { a, b } = await messaging(page);
  await services.callTool(a, "send_message", { to: b, text: "hello from A" });
  await expect(fromA(page)).toHaveText(["first from Ahello from A"]);
  await menuItem(await openMenu(sessionItem(page, "first from A")), "Delete").click();
  await page.getByRole("alertdialog", { name: "Delete session?" }).getByRole("button", { name: "Delete" }).click();
  await expect(sessionButton(page, "first from A")).toHaveCount(0);
  await page.reload();
  await expect(pending(page).getByRole("article", { name: "Message from first from A" })).toHaveText([
    "first from Ahello from A",
  ]);
});

test("taken messages keep their labels after a reload", async ({ ox: page, services }) => {
  const { a, b } = await messaging(page);
  await services.callTool(a, "send_message", { to: b, text: "hello from A" });
  await services.callTool(a, "send_message", { to: b, text: "another from A" });
  await expect(pending(page).getByRole("article")).toHaveCount(2);
  await allow(waiting(page).getByRole("article"));
  await expect(pending(page)).toHaveCount(0);
  const taken = ["first from Ahello from A", "first from Aanother from A"];
  await expect(fromA(page)).toHaveText(taken);
  await expect(replies(page).last()).toContainText("another from A");
  await expect(stop(page)).toHaveCount(0);
  await page.reload();
  await expect(fromA(page)).toHaveText(taken);
});

test("Stop drops a pending message without touching the composer", async ({ ox: page, services }) => {
  const { a, b } = await messaging(page);
  await prompt(page).fill("draft");
  await services.callTool(a, "send_message", { to: b, text: "hello from A" });
  await services.callTool(a, "send_message", { to: b, text: "another from A" });
  await expect(pending(page).getByRole("article", { name: "Message from first from A" })).toHaveCount(2);
  await stop(page).click();
  await expect(pending(page)).toHaveCount(0);
  await expect(fromA(page)).toHaveCount(0);
  await expect(prompt(page)).toHaveValue("draft");
  await expect(errors(page)).toHaveText(["Interrupted"]);
});

for (const outcome of ["taken", "dropped"] as const) {
  test(`a reconnect reconciles a prompt ${outcome} while the page was away`, async ({
    ox: page,
    services,
    context,
  }) => {
    await newSessionButton(page).click();
    await ask(page, "Bash", "{}");
    await prompt(page).fill("steer");
    await prompt(page).press("Enter");
    await expect(pending(page).getByRole("article")).toHaveText(["steer"]);
    const other = await context.newPage();
    await other.goto(page.url());
    await expect(pending(other).getByRole("article")).toHaveText(["steer"]);

    await expect(prompt(page)).toHaveValue("");
    await prompt(page).fill("next draft");
    // The page stays disconnected while the other page resolves the prompt.
    await page.route("**/events", (route) => route.abort());
    await services.restartRelay();
    await expect(page.getByRole("status").filter({ hasText: "Reconnecting…" })).toBeVisible();
    // Wait for the other page's new snapshot, not its old approval or the
    // HTTP acknowledgement of a session/get for some other session.
    await other.reload();
    await expect(pending(other).getByRole("article")).toHaveText(["steer"]);
    if (outcome === "taken") {
      await allow(waiting(other).getByRole("article"));
      await expect(replies(other).last()).toHaveText("You said: steer");
    } else {
      await stop(other).click();
      await expect(errors(other)).toHaveText(["Interrupted"]);
      await expect(prompt(other)).toHaveValue("");
    }
    await expect(pending(page).getByRole("article")).toHaveText(["steer"]);

    await page.unroute("**/events");
    await expect(send(page)).toBeEnabled({ timeout: 15_000 });
    const expected = outcome === "taken" ? ["approve Bash {}", "steer"] : ["approve Bash {}"];
    const composer = outcome === "taken" ? "next draft" : "steer\nnext draft";
    await expect(prompts(page)).toHaveText(expected);
    await expect(prompt(page)).toHaveValue(composer);
    // A later reload of the session must not restore the same input again.
    const snapshot = await hold(page, "session/get", { session_id: sessionId(page) });
    await services.restartRelay();
    await snapshot.arrived;
    snapshot.release();
    await expect(send(page)).toBeEnabled();
    await expect(prompt(page)).toHaveValue(composer);
    await expect(prompts(page)).toHaveText(expected);
  });
}

const questions =
  '{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Cool"}],"multiSelect":false},{"question":"Which sections?","header":"Sections","options":[{"label":"Introduction","description":"a"},{"label":"Conclusion","description":"b"},{"label":"Appendix","description":"c"}],"multiSelect":true}]}';

// askQuestions has the fake harness ask the two questions above, and returns
// the form, its two questions, and its buttons.
async function askQuestions(page: Page) {
  await newSessionButton(page).click();
  const form = await ask(page, "AskUserQuestion", questions);
  return {
    form,
    color: form.getByRole("group", { name: /Which color\?/ }),
    sections: form.getByRole("group", { name: /Which sections\?/ }),
    submit: form.getByRole("button", { name: "Submit" }),
    next: form.getByRole("button", { name: "Next" }),
    previous: form.getByRole("button", { name: "Previous" }),
  };
}

test("Next waits for an answer to the shown question", async ({ ox: page }) => {
  const { form, color, sections, submit, next, previous } = await askQuestions(page);
  await expect(form.getByText("1 of 2")).toBeVisible();
  await expect(color).toBeVisible();
  await expect(sections).toBeHidden();
  await expect(previous).toBeDisabled();
  await expect(submit).toHaveCount(0);
  await expect(next).toBeDisabled();
  await color.getByRole("radio", { name: "Red" }).check();
  await next.click();
  await expect(sections).toBeVisible();
  await expect(color).toBeHidden();
  await expect(form.getByText("2 of 2")).toBeVisible();
  await expect(next).toHaveCount(0);
  await expect(submit).toBeDisabled();
  await sections.getByRole("checkbox", { name: "Appendix" }).check();
  await expect(submit).toBeEnabled();
});

test("Previous keeps answers", async ({ ox: page }) => {
  const { color, sections, submit, next, previous } = await askQuestions(page);
  await color.getByRole("textbox", { name: "Other answer" }).fill("Green");
  await next.click();
  await sections.getByRole("checkbox", { name: "Appendix" }).check();
  await previous.click();
  await expect(color.getByRole("radio", { name: "Other", exact: true })).toBeChecked();
  await expect(color.getByRole("textbox", { name: "Other answer" })).toHaveValue("Green");
  await next.click();
  await expect(sections.getByRole("checkbox", { name: "Appendix" })).toBeChecked();
  await expect(submit).toBeEnabled();
});

test("keyboard moves between questions", async ({ ox: page }) => {
  const { form, color, sections, previous } = await askQuestions(page);
  const otherAnswer = color.getByRole("textbox", { name: "Other answer" });
  await otherAnswer.press("Enter");
  await expect(form.getByText("1 of 2")).toBeVisible();
  await expect(sections).toBeHidden();
  await otherAnswer.fill("Green");
  await otherAnswer.press("Enter");
  await expect(sections).toBeVisible();
  const introduction = sections.getByRole("checkbox", { name: "Introduction" });
  await expect(introduction).toBeFocused();
  await introduction.check();
  await previous.click();
  await expect(color.getByRole("radio", { name: "Other", exact: true })).toBeFocused();
});

test("a form keeps its question and answers across a reconnect", async ({ ox: page, services }) => {
  const { form, color, sections, next } = await askQuestions(page);
  await color.getByRole("radio", { name: "Red" }).check();
  await next.click();
  await sections.getByRole("checkbox", { name: "Appendix" }).check();
  await services.restartRelay();
  await expect(main(page).getByRole("alert")).toHaveCount(0, { timeout: 10_000 });
  await expect(send(page)).toBeEnabled({ timeout: 10_000 });
  await expect(form.getByText("2 of 2")).toBeVisible();
  await expect(sections.getByRole("checkbox", { name: "Appendix" })).toBeChecked();
});

test("a single question has no Previous or Next", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const form = await ask(
    page,
    "AskUserQuestion",
    '{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red"},{"label":"Blue"}],"multiSelect":false}]}',
  );
  await expect(form.getByRole("button", { name: "Previous" })).toHaveCount(0);
  await expect(form.getByRole("button", { name: "Next" })).toHaveCount(0);
  await expect(form.getByText("1 of 1")).toHaveCount(0);
  await form.getByRole("radio", { name: "Red" }).check();
  await expect(form.getByRole("button", { name: "Submit" })).toBeEnabled();
});

test("choosing an option or Other leaves one answer to a single-select question", async ({ ox: page }) => {
  const { form, color, sections, submit, next } = await askQuestions(page);
  const other = color.getByRole("radio", { name: "Other", exact: true });
  const otherAnswer = color.getByRole("textbox", { name: "Other answer" });
  const red = color.getByRole("radio", { name: "Red" });
  await red.check();
  await other.check();
  await expect(red).not.toBeChecked();
  await expect(next).toBeDisabled();
  await red.check();
  await expect(next).toBeEnabled();
  await otherAnswer.fill("Green");
  await expect(red).not.toBeChecked();
  await otherAnswer.fill("");
  await expect(next).toBeDisabled();
  await otherAnswer.fill("Green");
  await red.check();
  await expect(other).not.toBeChecked();
  await expect(otherAnswer).toHaveValue("Green");
  await next.click();
  await sections.getByRole("checkbox", { name: "Introduction" }).check();
  await submit.click();
  await expect(form).toHaveCount(0);
  await expect(toolCall(page, "AskUserQuestion")).toContainText('"answers":{"Which color?":"Red",');
});

test("submitted answers reach the tool by question text", async ({ ox: page }) => {
  const { form, color, sections, submit, next } = await askQuestions(page);
  await expect(color).toContainText("Warm");
  await color.getByRole("radio", { name: "Red" }).check();
  await next.click();
  await sections.getByRole("checkbox", { name: "Appendix" }).check();
  await sections.getByRole("checkbox", { name: "Introduction" }).check();
  await sections.getByRole("textbox", { name: "Other answer" }).fill("Glossary");
  await submit.click();
  await expect(form).toHaveCount(0);
  await expect(toolCall(page, "AskUserQuestion")).toContainText(
    `{"behavior":"allow","updatedInput":${questions.slice(0, -1)},"answers":{"Which color?":"Red","Which sections?":"Introduction, Appendix, Glossary"}}}`,
  );
  await expect(stop(page)).toHaveCount(0);
});

test("Decline denies the question", async ({ ox: page }) => {
  const { form, color, next } = await askQuestions(page);
  await color.getByRole("radio", { name: "Red" }).check();
  await next.click();
  await form.getByRole("button", { name: "Decline" }).click();
  await expect(form).toHaveCount(0);
  await expect(toolCall(page, "AskUserQuestion")).toContainText(
    '{"behavior":"deny","message":"User declined to answer"}',
  );
  await expect(stop(page)).toHaveCount(0);
});

test("a malformed question falls back to Allow and Deny", async ({ ox: page }) => {
  await newSessionButton(page).click();
  const approval = await ask(page, "AskUserQuestion", '{"questions":"x"}');
  await expect(approval.getByRole("radio", { name: "Allow" })).toBeVisible();
  await expect(approval.getByRole("radio", { name: "Deny" })).toBeVisible();
});

test("a tool whose process ended with oxd shows stopped on every page", async ({
  ox: page,
  services,
  config,
  context,
}) => {
  await newSessionButton(page).click();
  await ask(page, "Bash", '{"command":"sleep 60"}');
  const marker = toolCall(page, "Bash").getByRole("img");
  await expect(marker).toHaveAccessibleName("running");
  await services.restartDaemon(config, "SIGKILL");
  await expect(marker).toHaveAccessibleName("stopped");
  await prompt(page).fill("after the crash");
  await prompt(page).press("Enter");
  await expect(replies(page).last()).toHaveText("You said: after the crash");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(transcript(other).getByRole("group", { name: "Bash" }).getByRole("img")).toHaveAccessibleName("stopped");
});

test("a tool its turn ended without a result shows stopped", async ({ ox: page }) => {
  // The page hears no tool_result, as with Ur, which publishes none for an interrupted call.
  await page.addInitScript(() => {
    const Original = window.EventSource;
    window.EventSource = class extends Original {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init);
        this.addEventListener("message", (event) => {
          if (JSON.parse(event.data).params?.type === "tool_result") event.stopImmediatePropagation();
        });
      }
    };
  });
  await page.goto(page.url());
  await newSessionButton(page).click();
  await ask(page, "Bash", '{"command":"sleep 60"}');
  const marker = toolCall(page, "Bash").getByRole("img");
  await expect(marker).toHaveAccessibleName("running");
  await stop(page).click();
  await expect(marker).toHaveAccessibleName("stopped");
});

test("a reconnect keeps an expanded tool call open", async ({ ox: page, services }) => {
  await newSessionButton(page).click();
  await allow(await ask(page, "Bash", '{"command":"ls"}'));
  await expect(stop(page)).toHaveCount(0);
  const tool = toolCall(page, "Bash");
  const input = tool.getByText('{ "command": "ls" }', { exact: true });
  await expect(input).toBeHidden();
  await tool.click();
  await expect(input).toBeVisible();
  // Send waits for the session's reloaded transcript.
  await prompt(page).fill("after the reconnect");
  const snapshot = await hold(page, "session/get");
  await services.restartRelay();
  await snapshot.arrived;
  await expect(send(page)).toBeDisabled();
  snapshot.release();
  await expect(send(page)).toBeEnabled();
  await expect(input).toBeVisible();
});

test("a session whose harness is no longer offered shows its settings", async ({ ox: page, services }) => {
  await newSession(page, "harness retired");
  await services.restartDaemon(haikuConfig);
  await expect(model(page)).toContainText("echo");
  await expect(permissionMode(page)).toHaveValue("manual");
});

test("reselecting a model repairs a retired effort when there is only one choice", async ({ ox: page, services }) => {
  await newSession(page, "retired effort");
  const effort = page.getByRole("slider", { name: "Effort" });
  await model(page).click();
  await effort.press("End");
  await expect(model(page)).toContainText("High");
  await expect(model(page)).not.toHaveAttribute("aria-busy");
  await page.keyboard.press("Escape");
  const fake = fakeConfig.harnesses.fake;
  const echo = fake.models[0];
  await services.restartDaemon({
    ...fakeConfig,
    harnesses: { fake: { ...fake, models: [{ ...echo, efforts: echo.efforts.slice(0, 1) }] } },
  });
  await expect(model(page)).toContainText("high");
  await model(page).click();
  await expect(effort).toBeDisabled();
  await page.getByRole("option", { name: "Echo" }).click();
  await expect(model(page)).toContainText("Low");
  await expect(model(page)).not.toHaveAttribute("aria-busy");
  await page.reload();
  await expect(model(page)).toContainText("Low");
});

test("a lost creation reply is recovered after leaving the draft", async ({ page, services }) => {
  const cutoff = await cutReply(page, "session_id");
  await open(page, services);
  await newSession(page, "another conversation");
  const listed = sessions(page).getByRole("listitem");
  await expect(listed).toHaveCount(1);
  await newSessionButton(page).click();
  await cutoff.arm();
  await prompt(page).fill("recover my prompt");
  await prompt(page).press("Enter");
  await cutoff.cut;
  await expect(listed).toHaveCount(2);
  await prompt(page).fill("recover my edited prompt");
  await sessionButton(page, "another conversation").click();
  await services.restartRelay();
  await page.getByRole("button", { name: "Reopen draft" }).click();
  await expect(prompt(page)).toHaveValue("recover my edited prompt");
  await expect(main(page).getByRole("status")).toContainText("Session recovered");
  await expect(prompts(page)).toHaveCount(0);
  await send(page).click();
  await expect(replies(page)).toHaveText(["You said: recover my edited prompt"]);
  await expect(listed).toHaveCount(2);
});

test("a retried creation recovers the session without sending", async ({ page, services }) => {
  const cutoff = await cutReply(page, "session_id");
  // Hold every snapshot of the new session, so it loads only after the retry.
  let id: string | undefined;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/rpc*", async (route) => {
    const body = route.request().postDataJSON();
    if (body?.method === "session/create") id ??= body.params.session_id;
    if (body?.method === "session/get" && id !== undefined && body.params.session_id === id) await released;
    return route.fallback();
  });
  await open(page, services);
  const sends = rpcCalls(page, "session/send");
  await newSessionButton(page).click();
  await cutoff.arm();
  await prompt(page).fill("retry my prompt");
  await prompt(page).press("Enter");
  await cutoff.cut;
  await services.restartRelay();
  // Once reconnected, the creation's error is the one left.
  await expect(send(page)).toBeEnabled();
  await expect(main(page).getByRole("alert")).toHaveText(["Lost the connection to ox. Reconnecting."]);
  // The retry's reply finds the session already created.
  await send(page).click();
  await cutoff.existing;
  release();
  await expect(main(page).getByRole("status")).toContainText("Session recovered");
  await expect(prompt(page)).toHaveValue("retry my prompt");
  await expect(prompts(page)).toHaveCount(0);
  expect(sends).toEqual([]);
});

test("session list membership and order don't wait for transcripts", async ({ ox: page, services }) => {
  const listed = sessions(page).getByRole("listitem");
  const firstSnapshot = await hold(page, "session/get");
  await newSessionButton(page).click();
  await prompt(page).fill("older session");
  await prompt(page).press("Enter");
  await firstSnapshot.arrived;
  await expect(listed).toHaveText(["older session"]);
  await expect(menuItem(await openMenu(workspaceSection(page, services.workspace)), "Remove workspace")).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(main(page).getByRole("status")).toHaveText("Loading session…");
  const id = sessionId(page);
  await newSession(page, "younger session");
  await expect(listed).toHaveText(["younger session", "older session"]);
  firstSnapshot.release();
  await expect(listed).toHaveText(["younger session", "older session"]);

  const reloadSnapshot = await hold(page, "session/get", { session_id: id });
  await page.reload();
  await reloadSnapshot.arrived;
  await expect(replies(page)).toHaveText(["You said: younger session"]);
  await expect(listed).toHaveText(["younger session", "older session"]);
  reloadSnapshot.release();
  await expect(listed).toHaveText(["younger session", "older session"]);
});

test("a hash change selects the session it names", async ({ ox: page }) => {
  await newSession(page, "older session");
  const url = page.url();
  await newSession(page, "younger session");
  await page.goto(url);
  await expect(sessionButton(page, "older session")).toHaveAttribute("aria-current", "page");
  await expect(replies(page)).toHaveText(["You said: older session"]);
  // The selected session's title is set in medium weight, as shadcn's active item is.
  await expect(sessionButton(page, "older session")).toHaveCSS("font-weight", "500");
  await expect(sessionButton(page, "younger session")).toHaveCSS("font-weight", "400");
});

test("a cancelled deletion's reply cannot close the next confirmation", async ({ ox: page }) => {
  await newSession(page, "old deletion");
  await newSession(page, "new deletion");
  const remove = async (title: string) => menuItem(await openMenu(sessionItem(page, title)), "Delete").click();
  const first = await hold(page, "session/destroy");
  const dialog = page.getByRole("alertdialog", { name: "Delete session?" });
  await remove("old deletion");
  await dialog.getByRole("button", { name: "Delete" }).click();
  await first.arrived;
  await page.keyboard.press("Escape");
  await remove("new deletion");
  first.release();
  await expect(sessionButton(page, "old deletion")).toHaveCount(0);
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAccessibleDescription('This deletes "new deletion" and its history.');
  await dialog.getByRole("button", { name: "Delete" }).click();
  await expect(sessionButton(page, "new deletion")).toHaveCount(0);
});

test("on reconnect each session waits for its own settings", async ({ ox: page, services, context }) => {
  await newSession(page, "available independently");
  await newSession(page, "refresh my settings");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(replies(other)).toHaveText(["You said: refresh my settings"]);

  const snapshot = await hold(page, "session/get", { session_id: sessionId(page) });
  await services.restartRelay();
  await snapshot.arrived;
  await permissionMode(other).selectOption("plan");
  await expect(permissionMode(other)).not.toHaveAttribute("aria-busy");
  await prompt(other).fill("written while reloading");
  await prompt(other).press("Enter");
  await expect(replies(other).last()).toHaveText("You said: written while reloading");
  await expect(permissionMode(page)).toHaveValue("manual");
  await expect(prompts(page)).toHaveText(["refresh my settings"]);
  await prompt(page).fill("after reload");
  await expect(send(page)).toBeDisabled();
  await sessionButton(page, "available independently").click();
  await expect(send(page)).toBeEnabled();
  await sessionButton(page, "refresh my settings").click();
  snapshot.release();
  await expect(permissionMode(page)).toHaveValue("plan");
  await expect(prompts(page)).toHaveText(["refresh my settings", "written while reloading"]);
  await expect(send(page)).toBeEnabled();
  await prompt(other).fill("written after reloading");
  await prompt(other).press("Enter");
  await expect(prompts(page)).toHaveText(["refresh my settings", "written while reloading", "written after reloading"]);
});

test("a creation failed by an empty relay answer shows its error with Reopen draft", async ({ ox: page }) => {
  await newSession(page, "existing conversation");
  await newSessionButton(page).click();
  const create = await hold(page, "session/create");
  await prompt(page).fill("keep this draft");
  await prompt(page).press("Enter");
  await create.arrived;
  await sessionButton(page, "existing conversation").click();
  create.fail("");
  const error = page.getByRole("alert").filter({ hasText: "502" });
  await expect(error).toBeVisible();
  await error.getByRole("button", { name: "Reopen draft" }).click();
  await expect(prompt(page)).toHaveValue("keep this draft");
});

test("a reconnect repeats a failed request until the page catches up", async ({ ox: page, services, context }) => {
  await newSession(page, "first");
  await newSession(page, "second");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(replies(other)).toHaveText(["You said: second"]);

  const id = sessionId(page);
  const listing = await hold(page, "workspace/list");
  const snapshot = await hold(page, "session/get", { session_id: id });
  await services.restartRelay();
  await listing.arrived;
  listing.fail("listing unavailable");
  const alert = page.getByRole("alert").filter({ hasText: "listing unavailable" });
  await expect(alert).toBeVisible();
  await snapshot.arrived;
  snapshot.fail("snapshot unavailable");
  await expect(page.getByRole("alert").filter({ hasText: "snapshot unavailable" })).toBeVisible();
  await prompt(other).fill("while failing");
  await prompt(other).press("Enter");
  await expect(replies(other).last()).toHaveText("You said: while failing");
  await expect(prompts(page)).toHaveText(["second", "while failing"]);
  await expect(send(page)).toBeEnabled();
  await expect(page.getByRole("alert")).toHaveCount(0);
});

// createChild has session parent create a child titled title, which starts
// with message, and returns the child's ID and directory.
async function createChild(services: Services, parent: string, title: string, args: Record<string, string> = {}) {
  const created = JSON.parse(
    await services.callTool(parent, "create_session", {
      name: title.toLowerCase().replaceAll(" ", "-"),
      title,
      message: "hello",
      ...args,
    }),
  );
  return created as { session_id: string; directory: string };
}

// family creates the session "Parent", its child "Child", and the child's
// child "Grandchild", and waits until all three are idle.
async function family(page: Page, services: Services) {
  await newSession(page, "Parent");
  const child = await createChild(services, sessionId(page), "Child");
  await createChild(services, child.session_id, "Grandchild");
  for (const title of ["Parent", "Child", "Grandchild"]) {
    await expect(sessionButton(page, title)).toHaveAccessibleDescription("idle");
  }
}

// archive archives the session titled title from its menu.
async function archive(page: Page, title: string) {
  await menuItem(await openMenu(sessionItem(page, title)), "Archive").click();
  await expect(sessionButton(page, title)).toHaveCount(0);
}

// showArchived shows the archived sessions of the services' workspace.
async function showArchived(page: Page, services: Services) {
  await menuItem(await openMenu(workspaceSection(page, services.workspace)), "Show archived").click();
}

// remove deletes the session titled title from its menu.
async function remove(page: Page, title: string) {
  await menuItem(await openMenu(sessionItem(page, title)), "Delete").click();
  await page.getByRole("alertdialog", { name: "Delete session?" }).getByRole("button", { name: "Delete" }).click();
  await expect(sessionButton(page, title)).toHaveCount(0);
}

test("a child session shows its title, its directory, and its parent's message, also after a reload", async ({
  ox: page,
  services,
}) => {
  await newSession(page, "child parent");
  const dir = basename(mkdtempSync(`${services.workspace}/child-`));
  const child = await createChild(services, sessionId(page), "Helper child", { message: "hello child", dir });
  const directory = `${services.workspace}/${dir}`;
  expect(child.directory).toBe(directory);
  for (const reload of [false, true]) {
    if (reload) await page.reload();
    await sessionButton(page, "Helper child").click();
    await expect(heading(page)).toHaveText("Helper child");
    expect(sessionId(page)).toBe(child.session_id);
    await expect(main(page).getByText(directory, { exact: true })).toBeVisible();
    await expect(transcript(page).getByRole("article", { name: "Message from child parent" })).toHaveText(
      "child parenthello child",
    );
  }
});

test("children list oldest first beneath their parent, each level one deeper, after newer top-level sessions", async ({
  ox: page,
  services,
}) => {
  await newSession(page, "tree root");
  const root = sessionId(page);
  await newSession(page, "after the root");
  const first = await createChild(services, root, "Tree first");
  await createChild(services, first.session_id, "Tree leaf");
  await createChild(services, root, "Tree second");
  const listed = sessions(page).getByRole("listitem");
  await expect(listed).toHaveText(["after the root", "tree root", "Tree first", "Tree leaf", "Tree second"]);
  await expect(sessions(page).getByRole("listitem", { level: 1 })).toHaveText(["after the root", "tree root"]);
  await expect(sessions(page).getByRole("listitem", { level: 2 })).toHaveText(["Tree first", "Tree second"]);
  await expect(sessions(page).getByRole("listitem", { level: 3 })).toHaveText(["Tree leaf"]);
});

// chevron is the button that hides and shows the sessions beneath the
// session titled title.
const chevron = (page: Page, title: string) =>
  sessionItem(page, title).getByRole("button", { name: "Child sessions", includeHidden: true });

// toggleChildren hovers the row of the session titled title and clicks its
// chevron.
async function toggleChildren(page: Page, title: string) {
  await sessionItem(page, title).hover();
  await chevron(page, title).click();
}

test("a session's chevron hides and shows every session beneath it", async ({ ox: page, services }) => {
  await family(page, services);
  const listed = sessions(page).getByRole("listitem");
  await expect(chevron(page, "Parent")).toHaveCount(1);
  await expect(chevron(page, "Child")).toHaveCount(1);
  await expect(chevron(page, "Grandchild")).toHaveCount(0);

  await page.mouse.move(0, 0);
  await expect(chevron(page, "Child")).toHaveCSS("opacity", "0");
  await sessionItem(page, "Child").hover();
  await expect(chevron(page, "Child")).toHaveCSS("opacity", "1");
  await sessionButton(page, "Child").click();
  const url = page.url();
  await chevron(page, "Child").click();
  await expect(listed).toHaveText(["Parent", "Child"]);
  await expect(chevron(page, "Child")).toHaveAttribute("aria-expanded", "false");
  await expect(sessionButton(page, "Child")).toHaveAttribute("aria-current", "page");
  expect(page.url()).toBe(url);

  // A click focuses the chevron without a focus ring, so the row's buttons
  // hide when the pointer leaves; keyboard focus shows them.
  await page.mouse.move(0, 0);
  await expect(chevron(page, "Child")).toHaveCSS("opacity", "0");
  await expect(sessionItem(page, "Child").locator(".actions")).toHaveCSS("opacity", "0");
  await page.keyboard.press("Shift+Tab");
  await expect(sessionButton(page, "Child")).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(chevron(page, "Child")).toBeFocused();
  await expect(chevron(page, "Child")).toHaveCSS("opacity", "1");
  await expect(sessionItem(page, "Child").locator(".actions")).toHaveCSS("opacity", "1");
  await page.keyboard.press("Tab");
  await expect(sessionItem(page, "Child").getByRole("button", { name: "Session actions" })).toBeFocused();

  await toggleChildren(page, "Parent");
  await expect(listed).toHaveText(["Parent"]);

  const list = await hold(page, "workspace/list");
  await services.restartRelay();
  await list.arrived;
  list.release();
  // Archive is enabled once the page is connected and has the new list.
  await expect(menuItem(await openMenu(sessionItem(page, "Parent")), "Archive")).toBeEnabled();
  await page.keyboard.press("Escape");
  await expect(listed).toHaveText(["Parent"]);

  await toggleChildren(page, "Parent");
  await expect(listed).toHaveText(["Parent", "Child"]);
  await expect(chevron(page, "Child")).toHaveAttribute("aria-expanded", "false");

  const row = sessionItem(page, "Parent");
  const box = await row.boundingBox();
  if (!box) throw new Error("Parent's row is not shown");
  await row.click({ position: { x: box.width - 40, y: box.height / 2 } });
  await expect(sessionButton(page, "Parent")).toHaveAttribute("aria-current", "page");
  await expect(heading(page)).toHaveText("Parent");

  await page.reload();
  await expect(listed).toHaveText(["Parent", "Child", "Grandchild"]);

  // A title too long to fit runs to its row's edge, clipped without an
  // ellipsis, and keeps its width when the chevron shows over its end. The
  // chevron keeps its size expanded and collapsed.
  const long = `Long parent ${words(40)}`;
  // A child title is at most 48 characters, still too long to fit, and ends
  // without spaces, so its text lies beneath the fade before the ⋯ icon.
  const longChild = `Long child ${"M".repeat(30)}`;
  await newSession(page, long);
  await createChild(services, sessionId(page), longChild);
  await expect(sessionButton(page, longChild)).toBeVisible();
  // Hovered but not selected, the row shows its hover color.
  await sessionButton(page, "Parent").click();
  await page.mouse.move(0, 0);
  const title = sessionButton(page, long);
  const titleBox = await title.boundingBox();
  const longRow = await sessionItem(page, long).boundingBox();
  if (!titleBox || !longRow) throw new Error("the long parent's row is not shown");
  expect(Math.abs(titleBox.x + titleBox.width - (longRow.x + longRow.width))).toBeLessThanOrEqual(1);
  await expect(sessionItem(page, long).getByText(long, { exact: true })).toHaveCSS("text-overflow", "clip");
  // strip is a 3px-wide strip of the row titled name at x, as tall as its
  // title's text.
  const strip = async (name: string, x: number) => {
    const text = await sessionItem(page, name).getByText(name, { exact: true }).boundingBox();
    if (!text) throw new Error(`${name}'s title is not shown`);
    return page.screenshot({ clip: { x: Math.ceil(x), y: text.y, width: 3, height: text.height } });
  };
  // Selected, the long child keeps its color when hovered. Hovered without a
  // chevron, it fades its title just before the ⋯ icon, which stands on the
  // row's own color over the title beneath it.
  await sessionButton(page, longChild).click();
  await sessionButton(page, longChild).blur();
  await page.mouse.move(0, 0);
  const childRow = await sessionItem(page, longChild).boundingBox();
  const childActions = await sessionItem(page, longChild)
    .getByRole("button", { name: "Session actions" })
    .boundingBox();
  if (!childRow || !childActions) throw new Error("the long child's row is not shown");
  const empty = await strip(longChild, childRow.x + 2);
  const unfaded = await strip(longChild, childActions.x - 4);
  await sessionItem(page, longChild).hover();
  expect(await strip(longChild, childRow.x + 2)).toEqual(empty);
  expect(await strip(longChild, childActions.x - 4)).not.toEqual(unfaded);
  expect(await strip(longChild, childActions.x)).toEqual(empty);
  // A hovered row shows a different color from the selected one.
  const swatch = (row: { x: number; y: number; height: number }) =>
    page.screenshot({ clip: { x: row.x + 2, y: Math.round(row.y + row.height / 2), width: 3, height: 3 } });
  await sessionItem(page, long).hover();
  expect(await swatch(longRow)).not.toEqual(await swatch(childRow));
  await expect(chevron(page, long)).toHaveCSS("opacity", "1");
  expect((await title.boundingBox())?.width).toBe(titleBox.width);
  // The chevron stays clear of the ⋯ button, over the title's end, and from
  // just past its icon to the ⋯ button only the row's color shows.
  const actions = await sessionItem(page, long).getByRole("button", { name: "Session actions" }).boundingBox();
  const over = await chevron(page, long).boundingBox();
  if (!actions || !over) throw new Error("the long parent's buttons are not shown");
  expect(over.x + over.width).toBeLessThanOrEqual(actions.x);
  const beside = (x: number) => page.screenshot({ clip: { x, y: over.y, width: 5, height: over.height } });
  expect(await beside(over.x + over.width - 2)).toEqual(await beside(longRow.x + 2));
  // In the dark theme too, the chevron's box beside its icon shows the
  // hovered row's own color.
  await page.emulateMedia({ colorScheme: "dark" });
  const pixel = (x: number) =>
    page.screenshot({ clip: { x, y: Math.round(over.y + over.height / 2), width: 1, height: 1 } });
  expect(await pixel(Math.round(over.x + over.width) - 2)).toEqual(await pixel(Math.round(longRow.x) + 3));
  for (const expanded of ["true", "false"]) {
    await expect(chevron(page, long)).toHaveAttribute("aria-expanded", expanded);
    const box = await chevron(page, long).boundingBox();
    expect([box?.width, box?.height]).toEqual([20, 20]);
    if (expanded === "true") await chevron(page, long).click();
  }
  // An archived parent's chevron is not dimmed with its title, expanded or not.
  await chevron(page, long).click();
  await archive(page, long);
  await showArchived(page, services);
  await sessionItem(page, long).hover();
  await expect(chevron(page, long)).toHaveAttribute("aria-expanded", "true");
  await expect(chevron(page, long)).toHaveCSS("opacity", "1");
});

test("collapsing the open session's family leaves it open, and a new child stays hidden", async ({
  ox: page,
  services,
}) => {
  await family(page, services);
  const parent = sessionId(page);
  const listed = sessions(page).getByRole("listitem");
  await sessionButton(page, "Grandchild").click();
  await toggleChildren(page, "Parent");
  await expect(listed).toHaveText(["Parent"]);
  await expect(heading(page)).toHaveText("Grandchild");
  await expect(sessions(page).locator("[aria-current]")).toHaveCount(0);

  const late = await createChild(services, parent, "Late child");
  await page.goto(`${new URL(page.url()).origin}/#${encodeURIComponent(late.session_id)}`);
  await expect(heading(page)).toHaveText("Late child");
  await expect(listed).toHaveText(["Parent"]);
  await toggleChildren(page, "Parent");
  await expect(listed).toHaveText(["Parent", "Child", "Grandchild", "Late child"]);
});

test.describe("on a touch screen", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test("the header, transcript, composer, and settings share one side inset", async ({ ox: page }) => {
    await expectSidesAligned(page, true);
  });

  test("a parent's chevron shows without hover and keeps the session list open", async ({ ox: page, services }) => {
    await newSession(page, "Parent");
    await createChild(services, sessionId(page), "Child");
    await page.getByRole("button", { name: "Toggle sessions" }).tap();
    await expect(sessionButton(page, "Child")).toBeVisible();
    await expect(chevron(page, "Parent")).toHaveCSS("opacity", "1");
    await chevron(page, "Parent").tap();
    await expect(sessionButton(page, "Child")).toHaveCount(0);
    await expect(sessions(page)).toBeVisible();
  });

  test("a wide table scrolls inside its reply instead of the transcript", async ({ ox: page }) => {
    const paths = [
      "cmd/ox/web/e2e/workspace.spec.ts:352",
      "cmd/ox/web/e2e/session.spec.ts:1220",
      "cmd/ox/web/src/FileView.tsx:14",
    ];
    const markdown = await reply(
      page,
      [
        "Locations",
        "",
        "| Title | Location |",
        "| --- | --- |",
        ...paths.map((path) => `| \`${path}\` | \`${path}\` |`),
      ].join("\n"),
    );
    const table = markdown.getByRole("table");
    await expect(table.getByRole("row")).toHaveCount(paths.length + 1);
    const lastCell = table.getByRole("row").last().getByRole("cell").last();
    await expect(lastCell).toHaveText(paths[paths.length - 1]);
    await expect(table).toBeInViewport({ ratio: 1 });
    expect(await table.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
    await table.evaluate((el) => el.scrollTo({ left: el.scrollWidth }));
    await expect(lastCell).toBeInViewport({ ratio: 1 });
    await expect.poll(() => transcript(page).evaluate((el) => el.scrollWidth - el.clientWidth)).toBe(0);
  });

  test("pending approvals leave the composer above the keyboard", async ({ ox: page }) => {
    await setSafeArea(page, { top: 47, bottom: 34 });
    await newSessionButton(page).click();
    // The keyboard covers 336 px of the 844 px window, and Safari pans the page
    // 120 px down, leaving 508 px visible from 120 to 628.
    await openKeyboard(page, { height: 336, offsetTop: 120 });
    const lines = Object.fromEntries(Array.from({ length: 40 }, (_, i) => [`line${i}`, i]));
    const approval = await ask(page, "Example", JSON.stringify(lines));
    await approval.getByRole("group").click();
    await expect(approval.getByText('"line39": 39')).toBeVisible();
    await expect.poll(async () => (await waiting(page).boundingBox())?.height).toBeLessThanOrEqual(254);
    await expect.poll(async () => (await edges(page.getByRole("main").locator(".composer"))).bottom).toBe(612);
  });
});

test("a session with children cannot be deleted", async ({ ox: page, services }) => {
  await newSession(page, "Parent");
  await createChild(services, sessionId(page), "Child");
  await expect(sessionButton(page, "Child")).toBeVisible();
  const deletion = menuItem(await openMenu(sessionItem(page, "Parent")), "Delete");
  await expect(deletion).toHaveAccessibleDescription("Delete its child sessions first");
  await expect(deletion).toBeDisabled();
});

test("a waiting descendant keeps its family from being archived", async ({ ox: page, services }) => {
  await family(page, services);
  await sessionButton(page, "Grandchild").click();
  await ask(page, "Bash", "{}");
  const blocked = menuItem(await openMenu(sessionItem(page, "Parent")), "Archive");
  await expect(blocked).toHaveAccessibleDescription("Wait until it and its child sessions are idle");
  await expect(blocked).toBeDisabled();
});

test("a child archived alone hides beneath its parent with its own children", async ({ ox: page, services }) => {
  await family(page, services);
  await archive(page, "Child");
  await expect(sessionButton(page, "Grandchild")).toHaveCount(0);
  await expect(sessionButton(page, "Parent")).toBeVisible();
  await showArchived(page, services);
  await expect(sessionItem(page, "Child")).toHaveAttribute("aria-level", "2");
  await expect(sessionItem(page, "Grandchild")).toHaveAttribute("aria-level", "3");
  await expect(sessionButton(page, "Child")).toHaveAccessibleDescription("archived");
  await expect(sessionButton(page, "Grandchild")).toHaveAccessibleDescription("archived");
  await expect(sessionButton(page, "Parent")).toHaveAccessibleDescription("idle");
});

test("archiving a parent archives the sessions beneath it", async ({ ox: page, services }) => {
  await family(page, services);
  await archive(page, "Parent");
  await expect(sessionButton(page, "Child")).toHaveCount(0);
  await expect(sessionButton(page, "Grandchild")).toHaveCount(0);
  await showArchived(page, services);
  for (const title of ["Parent", "Child", "Grandchild"]) {
    await expect(sessionButton(page, title)).toHaveAccessibleDescription("archived");
  }
});

test("a child cannot be unarchived while its parent is archived", async ({ ox: page, services }) => {
  await family(page, services);
  await archive(page, "Parent");
  await showArchived(page, services);
  const unarchive = menuItem(await openMenu(sessionItem(page, "Child")), "Unarchive");
  await expect(unarchive).toHaveAccessibleDescription("Unarchive its parent first");
  await expect(unarchive).toBeDisabled();
  await page.keyboard.press("Escape");
  await sessionButton(page, "Child").click();
  await expect(main(page).getByText("This session is archived with its parent.")).toBeVisible();
  await expect(main(page).getByRole("button", { name: "Unarchive" })).toBeDisabled();
});

test("archived sessions show with an archived marker when shown", async ({ ox: page, services }) => {
  await newSession(page, "archive me");
  await archive(page, "archive me");
  await showArchived(page, services);
  await expect(sessionItem(page, "archive me").getByRole("img", { name: "archived" })).toBeVisible();
  await expect(sessionButton(page, "archive me")).toHaveAccessibleDescription("archived");
});

test("deleting a family from the leaves up enables each parent's Delete", async ({ ox: page, services }) => {
  await family(page, services);
  for (const title of ["Parent", "Child"]) {
    await expect(menuItem(await openMenu(sessionItem(page, title)), "Delete")).toBeDisabled();
    await page.keyboard.press("Escape");
  }
  await remove(page, "Grandchild");
  await remove(page, "Child");
  await expect(menuItem(await openMenu(sessionItem(page, "Parent")), "Delete")).toBeEnabled();
});

test("Archive waits until the session is idle", async ({ ox: page }) => {
  await newSession(page, "archive me");
  const approval = await ask(page, "Bash", '{"command":"ls"}');
  const busy = menuItem(await openMenu(sessionItem(page, "archive me")), "Archive");
  await expect(busy).toHaveAccessibleDescription("Wait until it is idle");
  await expect(busy).toBeDisabled();
  await page.keyboard.press("Escape");
  await allow(approval);
  await expect(stop(page)).toHaveCount(0);
  await expect(menuItem(await openMenu(sessionItem(page, "archive me")), "Archive")).toBeEnabled();
});

test("Archive and Unarchive are unavailable while not connected", async ({ ox: page, services }) => {
  await newSession(page, "archive me");
  const offline = await hold(page, "workspace/list");
  await services.restartRelay();
  await offline.arrived;
  const archiveItem = menuItem(await openMenu(sessionItem(page, "archive me")), "Archive");
  await expect(archiveItem).toHaveAccessibleDescription("Not connected");
  await expect(archiveItem).toBeDisabled();
  await page.keyboard.press("Escape");
  offline.release();
  await archive(page, "archive me");
  await showArchived(page, services);

  const reconnect = await hold(page, "workspace/list");
  await services.restartRelay();
  await reconnect.arrived;
  const unarchiveItem = menuItem(await openMenu(sessionItem(page, "archive me")), "Unarchive");
  await expect(unarchiveItem).toHaveAccessibleDescription("Not connected");
  await expect(unarchiveItem).toBeDisabled();
  await page.keyboard.press("Escape");
  const unarchive = main(page).getByRole("button", { name: "Unarchive" });
  await expect(unarchive).toBeDisabled();
  reconnect.release();
  await expect(unarchive).toBeEnabled();
});

test("a failed archive shows its error in the session list", async ({ ox: page }) => {
  await newSession(page, "archive me");
  const failed = await hold(page, "session/archive");
  await menuItem(await openMenu(sessionItem(page, "archive me")), "Archive").click();
  await failed.arrived;
  failed.fail("relay unavailable");
  await expect(sessions(page).getByRole("alert")).toHaveText("relay unavailable");
  await expect(sessionButton(page, "archive me")).toBeVisible();
  await archive(page, "archive me");
  await expect(sessions(page).getByRole("alert")).toHaveCount(0);
});

test("an archived session shows its transcript and Unarchive in place of the composer", async ({ ox: page }) => {
  await newSession(page, "archive me");
  await archive(page, "archive me");
  await expect(main(page).getByText("This session is archived.")).toBeVisible();
  await expect(main(page).getByRole("button", { name: "Unarchive" })).toBeEnabled();
  await expect(prompt(page)).toHaveCount(0);
  await expect(permissionMode(page)).toHaveCount(0);
  await expect(replies(page)).toHaveText(["You said: archive me"]);
});

test("an archived session is archived on every page", async ({ ox: page, context }) => {
  await newSession(page, "archive me");
  const other = await context.newPage();
  await other.goto(page.url());
  await expect(prompt(other)).toBeVisible();
  await archive(page, "archive me");
  await expect(main(other).getByText("This session is archived.")).toBeVisible();
  await expect(prompt(other)).toHaveCount(0);
});

test("showing archived sessions lasts through hiding the session list", async ({ ox: page, services }) => {
  await newSession(page, "archive me");
  await archive(page, "archive me");
  await showArchived(page, services);
  const archived = sessionButton(page, "archive me");
  await expect(archived).toBeVisible();
  const toggle = page.getByRole("button", { name: "Toggle sessions" });
  await toggle.click();
  await expect(archived).toBeHidden();
  await toggle.click();
  await expect(archived).toBeVisible();
  await expect(archived).toHaveAccessibleDescription("archived");
});

test("opening a menu while another is open shows the new one", async ({ ox: page, services }) => {
  await newSession(page, "archive me");
  await archive(page, "archive me");
  await showArchived(page, services);
  await openMenu(sessionItem(page, "archive me"));
  const menu = await openMenu(workspaceSection(page, services.workspace));
  await expect(menu.getByRole("menuitem")).toHaveText(["Hide archived", /^Remove workspace/]);
  // Choosing from it shows it stayed open past the first menu's close.
  await menuItem(menu, "Hide archived").click();
  await expect(sessionButton(page, "archive me")).toHaveCount(0);
});

test("a menu opened near the bottom edge stays on screen as it grows", async ({ ox: page, services }) => {
  await newSession(page, "menu row");
  const item = sessionItem(page, "menu row");
  const viewport = page.viewportSize();
  const box = await item.boundingBox();
  if (!viewport || !box) throw new Error("no viewport or row box");
  await page.setViewportSize({ width: viewport.width, height: Math.ceil(box.y + box.height) + 4 });
  await item.click({ button: "right", position: { x: 10, y: box.height - 2 } });
  const menu = page.getByRole("menu");
  await expect(menu).toBeInViewport({ ratio: 1 });
  // A reason that appears while the menu is open keeps it on screen.
  const lost = await hold(page, "workspace/list");
  await services.restartRelay();
  await lost.arrived;
  await expect(menuItem(menu, "Archive")).toHaveAccessibleDescription("Not connected");
  await expect(menu).toBeInViewport({ ratio: 1 });
});

test("unarchiving returns the unsent text", async ({ ox: page }) => {
  await newSession(page, "archive me");
  await prompt(page).fill("kept while archived");
  await archive(page, "archive me");
  await expect(prompt(page)).toHaveCount(0);
  await main(page).getByRole("button", { name: "Unarchive" }).click();
  await expect(prompt(page)).toHaveValue("kept while archived");
  await expect(sessionButton(page, "archive me")).not.toHaveAccessibleDescription("archived");
});

test("an archive missed while disconnected shows after reconnecting", async ({ ox: page, services, context }) => {
  await newSession(page, "archive me");
  await page.route("**/events", (route) => route.abort());
  await services.restartRelay();
  const archiver = await context.newPage();
  await archiver.goto(page.url());
  await archive(archiver, "archive me");
  await expect(main(archiver).getByText("This session is archived.")).toBeVisible();
  await archiver.close();
  await expect(prompt(page)).toBeVisible();
  await page.unroute("**/events");
  await expect(main(page).getByText("This session is archived.")).toBeVisible();
});

test("a reload hides archived sessions, and an archived session's URL opens it", async ({ ox: page, services }) => {
  await newSession(page, "archive me");
  await archive(page, "archive me");
  await showArchived(page, services);
  await expect(sessionButton(page, "archive me")).toBeVisible();
  await page.reload();
  await expect(main(page).getByText("This session is archived.")).toBeVisible();
  await expect(heading(page)).toHaveText("archive me");
  await expect(sessionButton(page, "archive me")).toHaveCount(0);
});
