import { expect, test, type Locator, type Page } from "@playwright/test";
import * as acp from "@agentclientprotocol/sdk";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createServer, type ServerResponse } from "node:http";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { Readable, Writable } from "node:stream";
import { fileURLToPath } from "node:url";

import { WorkspaceRegistry } from "../src/workspace-registry.ts";

const clientRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(clientRoot, "..");
const responsiveToolName = "inspect_responsive_layout_and_report_overflow";
const responsiveProviderToolName = `mcp__browser__${responsiveToolName}`;

test("independently drives Ox through the deterministic provider", async () => {
  const fixture = await createFixture();
  try {
    await driveOx(fixture);
    expect(fixture.requests).toBe(1);
  } finally {
    await fixture.close();
  }
});

test("disables conversations owned by another Ox runtime", async ({ page }) => {
  const fixture = await createFixture();
  let locked: Awaited<ReturnType<typeof holdOxSession>> | undefined;
  let host: BrowserHost | undefined;
  try {
    await driveOx(fixture);
    locked = await holdOxSession(fixture, "locked elsewhere");
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    const conversations = conversationList(page, basename(fixture.workspace));
    const lockedConversation = conversations.getByRole("listitem").filter({ hasText: "locked elsewhere" });
    await expect(lockedConversation.locator('[aria-label="Open in another client"]')).toBeVisible();
    await expect(lockedConversation).not.toContainText("Open in another client");
    await expect(lockedConversation.getByRole("link")).toHaveCount(0);
    await expect(conversations.getByRole("link", { name: /smoke/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("smoke");
  } finally {
    await host?.stop();
    await locked?.close();
    await fixture.close();
  }
});

test("registers, selects, persists, and removes server-local workspaces", async ({ page }) => {
  const fixture = await createFixture({ registerWorkspace: false });
  const second = await mkdtemp(join(tmpdir(), "ox-client-second-workspace-"));
  let host: BrowserHost | undefined;
  try {
    await writeFile(join(second, "keep.txt"), "keep");
    host = startBrowserHost(fixture);
    const url = await host.url;
    await page.goto(url);
    await expect(page.getByText("No workspace selected")).toBeVisible();
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toHaveCount(0);

    await registerWorkspace(page, "/definitely/not/an/ox-workspace", "workspace must be a listable directory");
    await registerWorkspace(page, fixture.workspace);
    await expect(page.getByRole("navigation", { name: "Workspaces and conversations" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();

    const actions = workspaceActions(page, basename(fixture.workspace));
    await actions.click();
    await expect(page.getByRole("menuitem", { name: `Remove ${basename(fixture.workspace)}` })).toBeVisible();
    await page.keyboard.press("Escape");

    await registerWorkspace(page, second);
    await expect(page.getByRole("list", { name: "Workspaces" }).getByRole("heading", { level: 2 })).toHaveCount(2);
    await showWorkspace(page, basename(second));

    await page.reload();
    await assertShowing(page, basename(second));
    const snapshot = await page.evaluate(
      () =>
        new Promise<unknown>((resolveSnapshot) => {
          const socket = new WebSocket(new URL("/socket", window.location.href));
          socket.addEventListener("message", (event) => {
            socket.close();
            resolveSnapshot(JSON.parse(String(event.data)));
          }, { once: true });
        }),
    );
    expect(JSON.stringify(snapshot)).not.toContain(fixture.workspace);
    expect(JSON.stringify(snapshot)).not.toContain(second);
    await expect(page.locator("main")).not.toContainText(fixture.workspace);
    await expect(page.locator("main")).not.toContainText(second);

    await removeWorkspace(page, basename(second));
    await assertShowing(page, basename(fixture.workspace));
    expect(await readFile(join(second, "keep.txt"), "utf8")).toBe("keep");
  } finally {
    await host?.stop();
    await fixture.close();
    await rm(second, { force: true, recursive: true });
  }
});

test("uses the navigation drawer at phone width", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    await page.setViewportSize({ height: 844, width: 390 });
    host = startBrowserHost(fixture);
    await page.goto(await host.url);
    const navigation = page.getByRole("navigation", { name: "Workspaces and conversations" });
    await expect(navigation).toBeHidden();

    await page.getByRole("button", { name: "Open navigation" }).click();
    await expect(navigation).toBeVisible();
    await expect(newConversation(page)).toBeVisible();
    await expect(page.getByRole("region", { name: "Conversation" })).toHaveCount(0);
    await page.getByRole("button", { name: "Close navigation" }).focus();
    await page.keyboard.press("Tab");
    expect(await page.evaluate(() => document.activeElement?.closest("main") === null)).toBe(true);

    await conversationList(page).getByRole("link").first().click();
    await expect(navigation).toBeHidden();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();

    await page.getByRole("button", { name: "Open navigation" }).click();
    await expect(navigation).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(navigation).toBeHidden();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("supervises a real Ox process through clean shutdown and unexpected exit", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    const url = await host.url;
    await assertReady(page, url);
    await openWorkspaceSettings(page);
    const support = page.getByRole("region", { name: "Support details" });

    await host.stop();
    host = undefined;
    await fixture.waitForOxExit();
    await expect(support).toContainText("Disconnected");

    host = startBrowserHost(fixture, Number.parseInt(new URL(url).port, 10));
    expect(await host.url).toBe(url);
    await expect(support).toContainText("Connected");
    await page.setViewportSize({ height: 844, width: 390 });
    await page.getByRole("button", { name: "Open navigation" }).click();
    await startNewConversation(page);
    const transcript = page.getByRole("region", { name: "Transcript" });
    await page.getByLabel("Message", { exact: true }).fill("after host reconnect");
    await sendPrompt(page);
    await expect(transcript).toContainText("after host reconnect");
    await expect(transcript).toContainText("browser smoke");

    await fixture.stopOx();
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toBeVisible();
    await page.getByRole("button", { name: "Open navigation" }).click();
    await openWorkspaceSettings(page);
    await expect(page.getByRole("list", { name: "Ox processes" })).toContainText(
      `${basename(fixture.workspace)} — unavailable, idle`,
    );
    await expect(page.getByRole("list", { name: "Workspace diagnostics" })).toContainText(
      "Ox exited from SIGTERM",
    );
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("automatically uses stored credentials and keeps a browser login secret out of the shell", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await expect(page.getByText("Ox is not connected.")).toHaveCount(0);
    await openWorkspaceSettings(page);
    await expect(page.getByRole("button", { name: "Use configured credential" })).toHaveCount(0);

    const credential = "browser-login-test-secret";
    await page.getByRole("button", { name: "Change credential" }).click();
    await page.getByLabel("OpenRouter API key").fill(credential);
    await page.getByRole("button", { name: "OpenRouter login" }).click();
    await expect(page.getByRole("alert")).toHaveText("OpenRouter login failed");
    await page.getByRole("button", { name: "Change credential" }).click();
    await expect(page.getByLabel("OpenRouter API key")).toHaveValue("");
    await expect(page.locator("main")).not.toContainText(credential);

    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page.getByRole("alert")).toHaveText("Logout failed");
    await conversationList(page).getByRole("link").first().click();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("asks for a credential in workspace settings when the stored one cannot authenticate", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    fixture.rejectCredential(true);
    host = startBrowserHost(fixture);
    await page.goto(await host.url);
    await expect(page.getByRole("heading", { name: "Connect Ox" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Conversation" })).toHaveCount(0);

    await openWorkspaceSettings(page);
    await expect(page.getByRole("region", { name: "Authentication" })).toContainText("Not connected");
    await page.getByRole("button", { name: "Back to workspace" }).click();
    await expect(page.getByRole("heading", { name: "Connect Ox" })).toBeVisible();
    await openWorkspaceSettings(page);

    fixture.rejectCredential(false);
    await page.getByRole("button", { name: "Use configured credential" }).click();
    await expect(page.getByRole("button", { name: "Use configured credential" })).toHaveCount(0);
    await conversationList(page).getByRole("link").first().click();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("keeps a replayed session coherent across refresh and attached browsers", async ({ browser, page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  let secondContext: Awaited<ReturnType<typeof browser.newContext>> | undefined;
  try {
    await driveOx(fixture);
    host = startBrowserHost(fixture);
    const url = await host.url;
    await assertReady(page, url);

    const sessions = conversationList(page);
    await expect(sessions).toContainText("smoke");
    await expect(page.getByRole("region", { name: "Conversation" }).getByRole("heading", { name: "smoke" })).toBeVisible();
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("smoke");
    await expect(transcript).toContainText("browser smoke");
    await expect(page.getByRole("heading", { name: "Transcript" })).toHaveCount(0);
    const promptRegion = page.getByRole("region", { name: "Prompt" });
    const usageButton = promptRegion.getByRole("button", { name: "Show usage: 1 / 128,000 tokens, $0.01" });
    await usageButton.click();
    const usagePopover = page.locator('[data-slot="popover-content"]');
    await expect(usagePopover).toContainText("1 / 128,000 tokens");
    await expect(usagePopover).toContainText("$0.01");
    await page.keyboard.press("Escape");
    await expect(transcript.getByRole("button", { name: "Cost" })).toHaveCount(0);
    await expect(page.getByLabel("Mode", { exact: true })).toBeVisible();
    await expect(page.getByText("Host revision", { exact: true })).toBeHidden();
    await expect(page.getByText("Session ID", { exact: true })).toBeHidden();
    await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);

    await page.reload();
    await expect(page.getByRole("region", { name: "Conversation" }).getByRole("heading", { name: "smoke" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    await page.getByLabel("Message", { exact: true }).fill("continue cumulative cost");
    await sendPrompt(page);
    const cumulativeUsageButton = promptRegion.getByRole("button", {
      name: "Show usage: 1 / 128,000 tokens, $0.02",
    });
    await cumulativeUsageButton.click();
    await expect(usagePopover).toContainText("$0.02");
    await page.keyboard.press("Escape");

    secondContext = await browser.newContext();
    const second = await secondContext.newPage();
    await assertReady(second, url);
    await second.getByRole("button", { name: "Conversation actions" }).click();
    await second.getByRole("menuitem", { name: "Close conversation" }).click();
    await expect(page.getByText("Ready when you are")).toBeVisible();

    await sessions.getByRole("link", { name: "smoke" }).click();
    await page.getByRole("button", { name: "Conversation actions" }).click();
    await page.getByRole("menuitem", { name: "Delete conversation" }).click();
    await expect(sessions).not.toContainText("smoke");
    await newConversation(page).click();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
  } finally {
    await secondContext?.close();
    await host?.stop();
    await fixture.close();
  }
});

test("prompts with controls and every supported browser attachment", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await expect(page.getByLabel("Mode", { exact: true })).toHaveText("Code");
    await chooseMode(page, "Plan");

    const composer = page.getByLabel("Message");
    await expect(page.getByRole("heading", { name: "Prompt" })).toHaveCount(0);
    await composer.fill("first line");
    await composer.press("Shift+Enter");
    await composer.pressSequentially("second line");
    await expect(composer).toHaveValue("first line\nsecond line");
    expect(fixture.requests).toBe(0);
    await composer.fill("inspect these attachments");
    await expect(page.getByRole("button", { name: "Send prompt" })).toBeVisible();
    await expect(page.getByRole("group", { name: "Resource link" })).toHaveCount(0);
    const addAttachment = page.getByLabel("Add attachment");
    await addAttachment.setInputFiles([
      { name: "picture.png", mimeType: "image/png", buffer: Buffer.from("image") },
      { name: "sound.wav", mimeType: "audio/wav", buffer: Buffer.from("audio") },
      { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("notes") },
    ]);
    const attachments = page.getByRole("list", { name: "Attachments" });
    await expect(attachments.getByRole("listitem")).toHaveText(["picture.png", "sound.wav", "notes.txt"]);
    await addAttachment.setInputFiles({ name: "spare.txt", mimeType: "text/plain", buffer: Buffer.from("spare") });
    await expect(attachments.getByRole("listitem")).toHaveCount(4);
    const removeSpare = page.getByRole("button", { name: "Remove spare.txt" });
    await removeSpare.click();
    await expect(attachments.getByRole("listitem")).toHaveText(["picture.png", "sound.wav", "notes.txt"]);
    // Removing a file leaves the picker usable for the same file again.
    await addAttachment.setInputFiles({ name: "spare.txt", mimeType: "text/plain", buffer: Buffer.from("spare") });
    await expect(attachments.getByRole("listitem")).toHaveCount(4);
    await removeSpare.click();
    await expect(attachments.getByRole("listitem")).toHaveText(["picture.png", "sound.wav", "notes.txt"]);
    await sendPrompt(page);
    await expect.poll(() => fixture.requests).toBe(1);
    const prompt = JSON.stringify(fixture.prompts[0]);
    expect(prompt).toContain("image_url");
    expect(prompt).toContain("input_audio");
    expect(prompt).toContain("notes.txt");
    expect(prompt).not.toContain("spare.txt");
    expect(prompt).not.toContain("example.test/guide");
    await expect(attachments).toHaveCount(0);
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("browser smoke");
    const sendButton = page.getByRole("button", { name: "Send prompt" });
    const usageButton = page.getByRole("button", { name: /^Show usage: [\d,]+ \/ [\d,]+ tokens, \$0\.01$/ });
    await expect(sendButton).toHaveText("");
    await usageButton.click();
    const usagePopover = page.locator('[data-slot="popover-content"]');
    await expect(usagePopover).toContainText(/^[\s\S]*[\d,]+ \/ [\d,]+ tokens[\s\S]*\$0\.01[\s\S]*$/);
    await page.keyboard.press("Escape");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs Ox file tools through the host's confined filesystem callbacks", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    await writeFile(join(fixture.workspace, "browser-file.txt"), "one\ntwo\n");
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await chooseMode(page, "Auto");
    await page.getByLabel("Message").fill("exercise filesystem callbacks");
    await sendPrompt(page);

    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("filesystem complete");
    expect(await readFile(join(fixture.workspace, "browser-file.txt"), "utf8")).toBe("edited\n");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs Ox shell tools through the host's ACP terminal callbacks", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await chooseMode(page, "Auto");
    await page.getByLabel("Message").fill("exercise terminal callbacks");
    await sendPrompt(page);

    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("terminal complete");
    await transcript.getByRole("button", { name: /printf terminal callback output/ }).click();
    await expect(transcript).toContainText("exit code: 0");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("runs separate sessions concurrently and cancels the selected live prompt", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    const message = page.getByLabel("Message", { exact: true });
    await message.fill("hold");
    await sendPrompt(page);
    await expect(message).toBeDisabled();
    await expect(page.getByRole("button", { name: "Stop response" })).toBeVisible();

    await newConversation(page).click();
    await message.fill("second session");
    await sendPrompt(page);
    await expect.poll(() => fixture.requests).toBe(2);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    const sessions = conversationList(page).getByRole("listitem");
    await expect(sessions.filter({ hasText: "hold" }).locator('[aria-label="Working"]')).toBeVisible();
    await sessions.filter({ hasText: "hold" }).getByRole("link").click();
    await expect(message).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(message).toBeEnabled();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("keeps a live prompt running across a browser reload", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    const message = page.getByLabel("Message", { exact: true });
    await message.fill("hold across reload");
    await sendPrompt(page);
    await expect(message).toBeDisabled();

    await page.reload();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
    await expect(message).toBeDisabled();

    fixture.releaseHeldPrompt();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("held prompt complete");
    await expect(message).toBeEnabled();
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("follows live transcript updates until manual scrollback asks to return", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    const scrollport = page.getByTestId("transcript-scrollport");
    await page.getByLabel("Message", { exact: true }).fill("create tall transcript");
    await sendPrompt(page);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("activity 599");
    const heldPrompt = "hold";

    await page.getByLabel("Message", { exact: true }).fill(heldPrompt);
    await sendPrompt(page);
    await expect.poll(() => fixture.requests).toBe(2);
    await expect.poll(() => bottomDistance(scrollport)).toBeLessThanOrEqual(1);
    fixture.releaseHeldPrompt();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("held prompt complete");
    await expect.poll(() => bottomDistance(scrollport)).toBeLessThanOrEqual(1);

    await page.getByLabel("Message", { exact: true }).fill(`${heldPrompt} again`);
    await sendPrompt(page);
    await expect.poll(() => fixture.requests).toBe(3);
    await expect.poll(() => bottomDistance(scrollport)).toBeLessThanOrEqual(1);
    await scrollport.evaluate((element) => {
      element.scrollTop = 0;
      element.dispatchEvent(new Event("scroll"));
    });
    await expect.poll(() => bottomDistance(scrollport)).toBeGreaterThan(40);
    const jump = page.getByRole("button", { name: "Jump to latest" });
    await expect(jump).toBeVisible();
    fixture.releaseHeldPrompt();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("held prompt complete");
    await expect.poll(() => scrollport.evaluate((element) => element.scrollTop)).toBe(0);
    await jump.click();
    await expect.poll(() => bottomDistance(scrollport)).toBeLessThanOrEqual(1);
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("opens reasoning at the latest detail", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByLabel("Message", { exact: true }).fill("create long reasoning");
    await sendPrompt(page);

    const transcript = page.getByRole("region", { name: "Transcript" });
    const reasoning = transcript.getByLabel("Reasoning content");
    await expect(reasoning).toContainText("reasoning 599");
    await expect(reasoning).toHaveClass(/h-\[7\.5rem\]/);
    await expect.poll(() => bottomDistance(reasoning)).toBeLessThanOrEqual(1);
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("closes reasoning when the assistant response follows it", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByLabel("Message", { exact: true }).fill("create reasoning then response");
    await sendPrompt(page);

    const transcript = page.getByRole("region", { name: "Transcript" });
    const reasoning = transcript.getByRole("button", { name: "Reasoning" });
    await expect(reasoning).toHaveAttribute("data-state", "closed");
    await reasoning.click();
    await expect(transcript.getByLabel("Reasoning content")).toContainText("intermediate reasoning");
    await expect(transcript).toContainText("reasoning response complete");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("contains long transcript content across phone and narrow desktop layouts", async ({ page }) => {
  const fixture = await createFixture({
    workspaceName: `workspace-${"unbroken".repeat(16)}`,
  });
  const mcp = await createMCPFixture(fixture.workspace);
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await openWorkspaceSettings(page);
    await addHTTPMCPServer(page, "browser", mcp.httpURL, mcp.httpSecret);
    await saveMCPServers(page);
    await startNewConversation(page);
    const prompt = `responsive stress ${"conversation".repeat(20)}`;
    await page.getByLabel("Message", { exact: true }).fill(prompt);
    await sendPrompt(page);
    await expect(page.getByRole("article", { name: "Permission for Inspect responsive layout" })).toBeVisible();
    await page.getByRole("button", { name: "Allow once" }).click();

    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("responsive complete");
    const activity = transcript.getByRole("button", { name: /Inspect responsive layout/i });
    await expect(activity).toBeVisible();
    await activity.click();
    await expect(transcript.getByText("HTTP MCP fixture result")).toBeVisible();

    for (const viewport of [{ height: 844, width: 390 }, { height: 700, width: 800 }]) {
      await page.setViewportSize(viewport);
      await expectNoHorizontalOverflow(page.locator("main"));
      await expectNoHorizontalOverflow(page.getByTestId("transcript-scrollport"));
    }

    await page.setViewportSize({ height: 720, width: 1280 });
    await openWorkspaceSettings(page);
    await expect(page.getByRole("heading", { name: "Workspace settings" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Back to conversation" })).toBeVisible();
    await expect(page.getByRole("list", { name: "Workspace diagnostics" })).toContainText("prefix_fingerprint=");
    await page.setViewportSize({ height: 844, width: 390 });
    await expectNoHorizontalOverflow(page.locator("main"));
  } finally {
    await host?.stop();
    await mcp.close();
    await fixture.close();
  }
});

test("runs concurrent turns and isolates failure across two workspaces", async ({ page }) => {
  const fixture = await createFixture({ registerSecondWorkspace: true });
  const first = basename(fixture.workspace);
  const second = basename(fixture.workspaceTwo);
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByLabel("Message").fill("hold in the first workspace");
    await sendPrompt(page);
    await expect(page.getByLabel("Message", { exact: true })).toBeDisabled();

    await showWorkspace(page, second);
    await page.getByLabel("Message").fill("second workspace turn");
    await sendPrompt(page);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    // Following another workspace's settings link selects that workspace.
    await openWorkspaceSettings(page, first);
    await expect(page.getByRole("region", { name: "Workspace settings" }).getByRole("heading", { exact: true, level: 2, name: first })).toBeVisible();
    const processes = page.getByRole("list", { name: "Ox processes" });
    await expect(processes).toContainText(`${first} — ready, working`);
    await expect(processes).toContainText(`${second} — ready, idle`);

    fixture.releaseHeldPrompt();
    await showWorkspace(page, first);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("held prompt complete");

    await fixture.stopOx(fixture.workspaceTwo);
    await openWorkspaceSettings(page, first);
    await expect(processes).toContainText(`${second} — unavailable, idle`);
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toHaveCount(0);
    await newConversation(page, first).click();
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).not.toContainText("held prompt complete");
    await page.getByLabel("Message", { exact: true }).fill("the first workspace still works");
    await sendPrompt(page);
    await expect(transcript).toContainText("browser smoke");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("restarts a workspace whose Ox stopped and reopens its durable history", async ({ page }) => {
  const fixture = await createFixture({ registerSecondWorkspace: true });
  const first = basename(fixture.workspace);
  const second = basename(fixture.workspaceTwo);
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await showWorkspace(page, second);
    await page.getByLabel("Message", { exact: true }).fill("remember this turn");
    await sendPrompt(page);
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("browser smoke");

    await fixture.stopOx(fixture.workspaceTwo);
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toBeVisible();
    // A workspace with no process offers its restart instead of conversations
    // it can no longer open.
    await expect(conversationList(page, second)).toHaveCount(0);

    await page.getByRole("button", { exact: true, name: `Restart ${second}` }).click();

    // The restart also clears the turn that the exit interrupted.
    await expect(page.getByRole("alert")).toHaveCount(0);
    await expect(transcript).toContainText("remember this turn");
    await openWorkspaceSettings(page, second);
    const processes = page.getByRole("list", { name: "Ox processes" });
    await expect(processes).toContainText(`${second} — ready, idle`);
    await expect(processes).toContainText(`${first} — ready, idle`);
    await expect(page.getByRole("list", { name: "Workspace diagnostics" })).toContainText("Ox exited from SIGTERM");
    await showWorkspace(page, second);
    await page.getByLabel("Message", { exact: true }).fill("after the restart");
    await sendPrompt(page);
    await expect(transcript).toContainText("browser smoke");
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("routes a pending permission to the workspace the browser is not showing", async ({ page }) => {
  const fixture = await createFixture({ registerSecondWorkspace: true });
  const first = basename(fixture.workspace);
  const second = basename(fixture.workspaceTwo);
  let host: BrowserHost | undefined;
  try {
    await page.setViewportSize({ height: 360, width: 1280 });
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await showWorkspace(page, second);
    await page.getByLabel("Message", { exact: true }).fill("request permission");
    await sendPrompt(page);
    const permission = page.getByLabel("Pending interactions").getByRole("article", { name: /Permission for/ });
    await expect(permission).toContainText("Run pwd");
    await expect(permission).not.toContainText("Tool:");
    await expect(permission).not.toContainText("Kind:");
    const scrollport = page.getByTestId("transcript-scrollport");
    await expect.poll(() => bottomDistance(scrollport)).toBeLessThanOrEqual(1);
    await expect(page.getByRole("button", { name: "Jump to latest" })).toHaveCount(0);
    await expect(conversationList(page, second).locator('[aria-label="Waiting for you"]')).toBeVisible();

    await showWorkspace(page, first);
    await expect(permission).toBeHidden();

    await conversationList(page, second)
      .getByRole("listitem")
      .filter({ has: page.locator('[aria-label="Waiting for you"]') })
      .getByRole("link")
      .click();

    await expect(permission).toContainText("Run pwd");
    await page.getByRole("button", { name: "Allow once" }).click();
    await expect(permission).toBeHidden();
    await expect.poll(() => fixture.requests).toBe(2);
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("renders a host-owned form elicitation through refresh and answers it", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await page.getByLabel("Message").fill("ask a question");
    await sendPrompt(page);
    const interactions = page.getByLabel("Pending interactions");
    await expect(interactions.getByRole("article", { name: "Choose a color", exact: true })).toContainText("Choose a color");
    await expect(page.getByRole("combobox", { name: "Answer" })).toHaveText("Select an option");
    await page.reload();
    await expect(interactions.getByRole("article", { name: "Choose a color", exact: true })).toContainText("Choose a color");
    await page.getByRole("combobox", { name: "Answer" }).click();
    await page.getByRole("option", { name: "Red", exact: true }).click();
    await page.getByRole("button", { name: "Submit answer" }).click();
    await expect(interactions.getByRole("article", { name: "Choose a color", exact: true })).toBeHidden();
    await expect.poll(() => fixture.requests).toBe(2);
  } finally {
    await host?.stop();
    await fixture.close();
  }
});

test("renders exact permission options and lets the first browser answer win", async ({ browser, page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  let secondContext: Awaited<ReturnType<typeof browser.newContext>> | undefined;
  try {
    host = startBrowserHost(fixture);
    const url = await host.url;
    await assertReady(page, url);
    await page.getByLabel("Message").fill("request permission");
    await sendPrompt(page);
    const permission = page.getByLabel("Pending interactions").getByRole("article", { name: /Permission for/ });
    await expect(permission).toContainText("pwd");
    secondContext = await browser.newContext();
    const second = await secondContext.newPage();
    await second.goto(url);
    await expect(second.getByRole("button", { name: "Allow once" })).toBeVisible();
    await second.getByRole("button", { name: "Allow once" }).click();
    await expect(permission).toBeHidden();
    await expect.poll(() => fixture.requests).toBe(2);
  } finally {
    await secondContext?.close();
    await host?.stop();
    await fixture.close();
  }
});

test("activates HTTP and stdio MCP servers without retaining secrets", async ({ page }) => {
  const fixture = await createFixture();
  const mcp = await createMCPFixture(fixture.workspace);
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await openWorkspaceSettings(page);

    await addHTTPMCPServer(page, "http", mcp.httpURL, mcp.httpSecret);
    await saveMCPServers(page);
    await startNewConversation(page);
    await expect(page.locator("main")).not.toContainText(mcp.httpSecret);
    await chooseMode(page, "Auto");
    await page.getByLabel("Message").fill("use HTTP MCP tool");
    await sendPrompt(page);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP complete");
    await page.getByRole("region", { name: "Transcript" }).getByRole("button", { name: /lookup/i }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP fixture result");
    expect(mcp.httpAuthorizations).toContain(mcp.httpSecret);

    await page.getByRole("textbox", { name: "Message" }).fill("use HTTP MCP failure");
    await sendPrompt(page);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP failure complete");
    await page.getByRole("region", { name: "Transcript" }).getByRole("button", { name: /fail/i }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP fixture failure");
    await page.getByRole("button", { name: "Conversation actions" }).click();
    await page.getByRole("menuitem", { name: "Close conversation" }).click();
    await expect(page.getByText("Ready when you are")).toBeVisible();

    await openWorkspaceSettings(page);
    await addStdioMCPServer(page, "stdio", mcp.stdioCommand, mcp.stdioArgument, mcp.stdioEnvironmentName, mcp.stdioSecret);
    await saveMCPServers(page);

    // An incomplete draft survives leaving settings and cannot block opening a
    // conversation.
    await page.getByRole("button", { name: "Add HTTP MCP server" }).click();
    await conversationList(page).getByRole("link").first().click();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
    await openWorkspaceSettings(page);
    await page.getByRole("group", { name: "HTTP MCP server" }).getByRole("button", { name: "Remove server" }).click();
    await conversationList(page).getByRole("link").first().click();
    await page.getByRole("textbox", { name: "Message" }).fill("use stdio MCP tool");
    await sendPrompt(page);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("stdio MCP complete");
    await page.getByRole("region", { name: "Transcript" }).getByRole("button", { name: /lookup/i }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("stdio MCP fixture result");
    await expect(page.locator("main")).not.toContainText(mcp.stdioSecret);
    await page.getByRole("button", { name: "Conversation actions" }).click();
    await page.getByRole("menuitem", { name: "Close conversation" }).click();

    await openWorkspaceSettings(page);
    await addHTTPMCPServer(page, "broken", `${mcp.httpURL}/unavailable`, "failed-activation-secret");
    await saveMCPServers(page);
    await newConversation(page).click();
    await expect(page.getByRole("alert")).toContainText("activate MCP servers");
    await expect(page.locator("main")).not.toContainText("failed-activation-secret");

    await openWorkspaceSettings(page);
    await addHTTPMCPServer(page, "reactivated", mcp.httpURL, mcp.httpSecret);
    await saveMCPServers(page);
    await startNewConversation(page);
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
    const providerRequests = JSON.stringify(fixture.prompts);
    expect(providerRequests).toContain("mcp__http__lookup");
    expect(providerRequests).toContain("mcp__stdio__lookup");
    expect(providerRequests).not.toContain(mcp.httpSecret);
    expect(providerRequests).not.toContain(mcp.stdioSecret);
  } finally {
    await host?.stop();
    await mcp.close();
    await fixture.close();
  }
});

type BrowserHost = {
  stop(): Promise<void>;
  url: Promise<string>;
};

function startBrowserHost(fixture: Fixture, port = 0): BrowserHost {
  const arguments_ = [
    "run",
    "./src/host.ts",
    "--port",
    String(port),
    "--ox",
    fixture.runner,
  ];
  for (const argument of [
    fixture.binary,
    "--credential-file",
    fixture.credential,
    "--model",
    "test/model",
    "--no-keyring",
    "--openrouter-base-url",
    `${fixture.providerURL}/api/v1`,
  ]) {
    arguments_.push("--ox-arg", argument);
  }
  const child = spawn("bun", arguments_, {
    cwd: clientRoot,
    env: fixture.environment,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const url = new Promise<string>((resolveURL, reject) => {
    const timer = setTimeout(() => reject(new Error("browser host did not start")), 10_000);
    let output = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      output += chunk;
      const match = output.match(/Ox browser host listening at (http:\/\/[^\s]+)/);
      if (match?.[1]) {
        clearTimeout(timer);
        resolveURL(match[1]);
      }
    });
    child.once("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`browser host exited before startup: ${code}`));
    });
  });
  return {
    async stop() {
      if (child.exitCode !== null || child.signalCode !== null) {
        return;
      }
      child.kill("SIGTERM");
      await once(child, "exit");
    },
    url,
  };
}

type Fixture = {
  binary: string;
  close(): Promise<void>;
  credential: string;
  environment: NodeJS.ProcessEnv;
  providerURL: string;
  prompts: unknown[];
  rejectCredential(rejected: boolean): void;
  releaseHeldPrompt(): void;
  requests: number;
  runner: string;
  stopOx(workspace?: string): Promise<void>;
  waitForOxExit(workspace?: string): Promise<void>;
  workspace: string;
  workspaceTwo: string;
};

type MCPFixture = {
  close(): Promise<void>;
  httpAuthorizations: string[];
  httpSecret: string;
  httpURL: string;
  stdioArgument: string;
  stdioCommand: string;
  stdioEnvironmentName: string;
  stdioSecret: string;
};

async function createFixture(
  options: {
    registerSecondWorkspace?: boolean;
    registerWorkspace?: boolean;
    workspaceName?: string;
  } = {},
): Promise<Fixture> {
  const root = await mkdtemp(join(tmpdir(), "ox-client-smoke-"));
  const workspace = join(root, options.workspaceName ?? "workspace");
  const workspaceTwo = join(root, "workspace-two");
  await mkdir(workspace);
  await mkdir(workspaceTwo);
  const pidDirectory = join(root, "pids");
  await mkdir(pidDirectory);
  const binary = join(root, "ox");
  const credential = join(root, "credential");
  const runner = join(root, "run-ox");
  const provider = createServer();
  let requests = 0;
  let credentialRejected = false;
  let filesystemStage = 0;
  const heldPrompts: ServerResponse[] = [];
  let terminalStage = 0;
  const prompts: unknown[] = [];
  provider.on("request", (request, response) => {
    if (request.method === "GET" && request.url === "/api/v1/auth/key") {
      if (!credentialRejected && request.headers.authorization === "Bearer test-key") {
        response.writeHead(200, { "content-type": "application/json" }).end('{"data":{}}');
      } else {
        response.writeHead(401, { "content-type": "application/json" }).end('{"error":{"message":"rejected"}}');
      }
      return;
    }
    if (request.method !== "POST" || request.url !== "/api/v1/chat/completions") {
      response.writeHead(404).end();
      return;
    }
    requests += 1;
    let body = "";
    request.setEncoding("utf8");
    request.on("data", (chunk: string) => {
      body += chunk;
    });
    request.once("end", () => {
      try {
        prompts.push(JSON.parse(body));
      } catch {
        prompts.push(body);
      }
      if (body.lastIndexOf("create tall transcript") > body.lastIndexOf("hold")) {
        streamText(response, Array.from({ length: 600 }, (_, index) => `activity ${index}`).join(" "));
        return;
      }
      if (body.lastIndexOf("create reasoning then response") > body.lastIndexOf("hold")) {
        streamReasoning(response, "intermediate reasoning", "reasoning response complete");
        return;
      }
      if (body.lastIndexOf("create long reasoning") > body.lastIndexOf("hold")) {
        streamReasoning(response, Array.from({ length: 600 }, (_, index) => `reasoning ${index}`).join(" "));
        return;
      }
      if (body.includes("responsive stress")) {
        if (!hasToolResultAfterPrompt(body, "responsive stress")) {
          streamToolCall(
            response,
            "responsive-tool",
            responsiveProviderToolName,
          );
          return;
        }
        streamText(response, `responsive complete ${"unbrokenoutput".repeat(80)}`);
        return;
      }
      if (body.includes("hold")) {
        heldPrompts.push(response);
        return;
      }
      if (body.includes("exercise filesystem callbacks")) {
        const tool = [
          ["browser-read", "read_file", '{\\"path\\":\\"browser-file.txt\\",\\"offset\\":2,\\"limit\\":1}'],
          ["browser-write", "write_file", '{\\"path\\":\\"browser-file.txt\\",\\"content\\":\\"written\\\\n\\"}'],
          ["browser-edit", "edit_file", '{\\"path\\":\\"browser-file.txt\\",\\"old_string\\":\\"written\\",\\"new_string\\":\\"edited\\"}'],
        ][filesystemStage++];
        if (tool) {
          response.writeHead(200, { "content-type": "text/event-stream" });
          response.end(
            [
              `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"${tool[0]}","type":"function","function":{"name":"${tool[1]}","arguments":"${tool[2]}"}}]},"finish_reason":null}]}`,
              "",
              'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
              "",
              "data: [DONE]",
              "",
            ].join("\n"),
          );
          return;
        }
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"content":"filesystem complete"},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      if (body.includes("exercise terminal callbacks")) {
        if (terminalStage++ === 0) {
          response.writeHead(200, { "content-type": "text/event-stream" });
          response.end(
            [
              'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"terminal-call","type":"function","function":{"name":"shell","arguments":"{\\"command\\":\\"printf terminal callback output\\"}"}}]},"finish_reason":null}]}',
              "",
              'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
              "",
              "data: [DONE]",
              "",
            ].join("\n"),
          );
          return;
        }
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"content":"terminal complete"},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      const mcpPrompt = latestMCPPrompt(body);
      if (mcpPrompt === "use HTTP MCP failure") {
        if (!hasToolResultAfterPrompt(body, "use HTTP MCP failure")) {
          streamToolCall(response, "http-mcp-failure", "mcp__http__fail");
          return;
        }
        streamText(response, "HTTP MCP failure complete");
        return;
      }
      if (mcpPrompt === "use HTTP MCP tool") {
        if (!hasToolResultAfterPrompt(body, "use HTTP MCP tool")) {
          streamToolCall(response, "http-mcp-call", "mcp__http__lookup");
          return;
        }
        streamText(response, "HTTP MCP complete");
        return;
      }
      if (mcpPrompt === "use stdio MCP tool") {
        if (!hasToolResultAfterPrompt(body, "use stdio MCP tool")) {
          streamToolCall(response, "stdio-mcp-call", "mcp__stdio__lookup");
          return;
        }
        streamText(response, "stdio MCP complete");
        return;
      }
      if (body.includes("request permission") && !body.includes('"outcome":"selected"')) {
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"shell-call","type":"function","function":{"name":"shell","arguments":"{\\"command\\":\\"pwd\\"}"}}]},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      if (body.includes("ask a question") && !body.includes('"outcome":"accepted"')) {
        response.writeHead(200, { "content-type": "text/event-stream" });
        response.end(
          [
            'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"question-call","type":"function","function":{"name":"question","arguments":"{\\"question\\":\\"Choose a color\\",\\"options\\":[{\\"label\\":\\"Red\\"},{\\"label\\":\\"Blue\\"}]}"}}]},"finish_reason":null}]}',
            "",
            'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
            "",
            "data: [DONE]",
            "",
          ].join("\n"),
        );
        return;
      }
      response.writeHead(200, { "content-type": "text/event-stream" });
      response.end(
        [
          'data: {"choices":[{"delta":{"content":"browser smoke"},"finish_reason":null}]}',
          "",
          'data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"cost":0.01}}',
          "",
          "data: [DONE]",
          "",
        ].join("\n"),
      );
    });
  });
  await new Promise<void>((resolveListen) => provider.listen(0, "127.0.0.1", resolveListen));
  const address = provider.address();
  if (!address || typeof address === "string") {
    throw new Error("provider did not bind a TCP address");
  }
  const providerURL = `http://127.0.0.1:${address.port}`;
  await seedOxFixture({ binary, credential }, root);
  // One Ox process runs per workspace, so the runner keys its pid file by the
  // working directory it was launched in.
  await writeFile(
    runner,
    `#!/bin/sh\nprintf '%s\\n' "$$" > "$OX_CLIENT_TEST_PID_DIR/$(basename "$(pwd)").pid"\nexec "$@"\n`,
    { mode: 0o700 },
  );
  const environment = {
    ...process.env,
    OX_CLIENT_TEST_PID_DIR: pidDirectory,
    HOME: root,
    XDG_CACHE_HOME: join(root, "cache"),
    XDG_CONFIG_HOME: join(root, "config"),
    XDG_DATA_HOME: join(root, "data"),
  };
  if (options.registerWorkspace !== false) {
    const registry = await WorkspaceRegistry.load(join(root, "config", "ox", "workspaces.json"));
    await registry.register(workspace);
    if (options.registerSecondWorkspace) {
      await registry.register(workspaceTwo);
    }
  }
  return {
    binary,
    async close() {
      provider.close();
      await once(provider, "close");
      await rm(root, { force: true, recursive: true });
    },
    credential,
    environment,
    providerURL,
    get requests() {
      return requests;
    },
    prompts,
    rejectCredential(rejected: boolean) {
      credentialRejected = rejected;
    },
    releaseHeldPrompt() {
      const response = heldPrompts.shift();
      if (!response) {
        throw new Error("no held prompt is waiting for a provider response");
      }
      streamText(response, "held prompt complete");
    },
    runner,
    async stopOx(target = workspace) {
      process.kill(await oxPID(join(pidDirectory, `${basename(target)}.pid`)), "SIGTERM");
    },
    async waitForOxExit(target = workspace) {
      const pid = await oxPID(join(pidDirectory, `${basename(target)}.pid`));
      await expect.poll(() => processExists(pid)).toBe(false);
    },
    workspace,
    workspaceTwo,
  };
}

function hasToolResultAfterPrompt(body: string, prompt: string): boolean {
  return body.lastIndexOf('"role":"tool"') > body.lastIndexOf(prompt);
}

function latestMCPPrompt(body: string): string | undefined {
  return ["use HTTP MCP failure", "use HTTP MCP tool", "use stdio MCP tool"].reduce<string | undefined>(
    (latest, prompt) => {
      const index = body.lastIndexOf(prompt);
      return index === -1 || (latest !== undefined && index <= body.lastIndexOf(latest)) ? latest : prompt;
    },
    undefined,
  );
}

function streamToolCall(response: import("node:http").ServerResponse, id: string, name: string): void {
  response.writeHead(200, { "content-type": "text/event-stream" });
  response.end(
    [
      `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"${id}","type":"function","function":{"name":"${name}","arguments":"{}"}}]},"finish_reason":null}]}`,
      "",
      'data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n"),
  );
}

function streamText(response: import("node:http").ServerResponse, text: string): void {
  response.writeHead(200, { "content-type": "text/event-stream" });
  response.end(
    [
      `data: {"choices":[{"delta":{"content":"${text}"},"finish_reason":null}]}`,
      "",
      'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n"),
  );
}

function streamReasoning(response: import("node:http").ServerResponse, reasoning: string, content?: string): void {
  response.writeHead(200, { "content-type": "text/event-stream" });
  response.end(
    [
      `data: {"choices":[{"delta":{"reasoning":"${reasoning}"},"finish_reason":null}]}`,
      "",
      ...(content === undefined ? [] : [`data: {"choices":[{"delta":{"content":"${content}"},"finish_reason":null}]}`, ""]),
      'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n"),
  );
}

async function createMCPFixture(workspace: string): Promise<MCPFixture> {
  const httpSecret = "browser-http-mcp-secret";
  const stdioSecret = "browser-stdio-mcp-secret";
  const stdioEnvironmentName = "OX_BROWSER_MCP_SECRET";
  const stdioArgument = join(workspace, "stdio-mcp.js");
  const httpAuthorizations: string[] = [];
  const server = createServer(async (request, response) => {
    if (request.url === "/unavailable") {
      response.writeHead(503).end("unavailable");
      return;
    }
    if (request.method !== "POST" || request.url !== "/mcp") {
      response.writeHead(404).end();
      return;
    }
    httpAuthorizations.push(request.headers.authorization ?? "");
    let body = "";
    request.setEncoding("utf8");
    for await (const chunk of request) {
      body += chunk;
    }
    const message = JSON.parse(body) as { id?: string | number; method?: string; params?: { name?: string } };
    if (message.id === undefined) {
      response.writeHead(202).end();
      return;
    }
    const result = mcpResult(message.method, message.params?.name);
    response.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify({ jsonrpc: "2.0", id: message.id, result }));
  });
  await new Promise<void>((resolveListen) => server.listen(0, "127.0.0.1", resolveListen));
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("MCP fixture did not bind a TCP address");
  }
  await writeFile(stdioArgument, stdioMCPProgram(stdioEnvironmentName, stdioSecret), { mode: 0o600 });
  return {
    async close() {
      server.close();
      await once(server, "close");
    },
    httpAuthorizations,
    httpSecret,
    httpURL: `http://127.0.0.1:${address.port}/mcp`,
    stdioArgument,
    stdioCommand: process.execPath,
    stdioEnvironmentName,
    stdioSecret,
  };
}

function mcpResult(method: string | undefined, toolName: string | undefined): unknown {
  switch (method) {
    case "initialize":
      return {
        capabilities: { tools: {} },
        protocolVersion: "2026-07-28",
        serverInfo: { name: "browser-fixture", version: "1" },
      };
    case "tools/list":
      return {
        tools: [
          { description: "returns a fixture value", inputSchema: { type: "object" }, name: "lookup" },
          { description: "reports a fixture failure", inputSchema: { type: "object" }, name: "fail" },
          { description: "checks responsive layout", inputSchema: { type: "object" }, name: responsiveToolName, title: "Inspect responsive layout" },
        ],
      };
    case "tools/call":
      return toolName === "fail"
        ? { content: [{ text: "HTTP MCP fixture failure", type: "text" }], isError: true }
        : { content: [{ text: "HTTP MCP fixture result", type: "text" }] };
    default:
      return {};
  }
}

function stdioMCPProgram(environmentName: string, secret: string): string {
  return `
let buffer = "";
function result(method) {
  if (method === "initialize") return { capabilities: { tools: {} }, protocolVersion: "2026-07-28", serverInfo: { name: "stdio-browser-fixture", version: "1" } };
  if (method === "tools/list") return { tools: [{ description: "returns a fixture value", inputSchema: { type: "object" }, name: "lookup" }] };
  if (method === "tools/call") return { content: [{ text: process.env[${JSON.stringify(environmentName)}] === ${JSON.stringify(secret)} ? "stdio MCP fixture result" : "missing stdio environment", type: "text" }] };
  return {};
}
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  for (;;) {
    const newline = buffer.indexOf("\\n");
    if (newline === -1) return;
    const line = buffer.slice(0, newline);
    buffer = buffer.slice(newline + 1);
    if (!line) continue;
    const request = JSON.parse(line);
    if (request.id === undefined) continue;
    process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: request.id, result: result(request.method) }) + "\\n");
  }
});
`;
}

async function addHTTPMCPServer(page: Page, name: string, url: string, secret: string): Promise<void> {
  await page.getByRole("button", { name: "Add HTTP MCP server" }).click();
  const server = page.getByRole("group", { name: "HTTP MCP server" });
  await server.getByLabel("Name").fill(name);
  await server.getByLabel("URL").fill(url);
  await server.getByRole("button", { name: "Add header" }).click();
  await server.getByLabel("Header name").fill("Authorization");
  await server.getByLabel("Header value").fill(secret);
}

async function addStdioMCPServer(
  page: Page,
  name: string,
  command: string,
  argument: string,
  environmentName: string,
  secret: string,
): Promise<void> {
  await page.getByRole("button", { name: "Add stdio MCP server" }).click();
  const server = page.getByRole("group", { name: "Stdio MCP server" });
  await server.getByLabel("Name").fill(name);
  await server.getByLabel("Command").fill(command);
  await server.getByRole("button", { name: "Add argument" }).click();
  await server.getByRole("textbox", { name: "Argument" }).fill(argument);
  await server.getByRole("button", { name: "Add variable" }).click();
  await server.getByLabel("Variable name").fill(environmentName);
  await server.getByLabel("Variable value").fill(secret);
}

async function seedOxFixture(fixture: Pick<Fixture, "binary" | "credential">, root: string): Promise<void> {
  await mkdir(join(root, "cache", "ox"), { recursive: true });
  await mkdir(join(root, "config", "ox"), { recursive: true });
  await writeFile(fixture.credential, "test-key\n", { mode: 0o600 });
  await writeFile(
    join(root, "cache", "ox", "models.json"),
    JSON.stringify({
      data: [
        {
          architecture: { input_modalities: ["text", "image", "audio"] },
          context_length: 128000,
          id: "test/model",
          reasoning: { supported_efforts: ["low", "high"] },
          supported_parameters: ["tools", "temperature", "max_tokens"],
        },
      ],
    }),
  );
  await writeFile(
    join(root, "config", "ox", "settings.json"),
    JSON.stringify({ default_model: "test/model", models: { "test/model": {} } }),
  );
  await run("go", ["build", "-o", fixture.binary, "./cmd/ox"], repositoryRoot);
}

async function driveOx(fixture: Fixture): Promise<string> {
  const child = spawn(
    fixture.binary,
    [
      "--credential-file",
      fixture.credential,
      "--model",
      "test/model",
      "--no-keyring",
      "--openrouter-base-url",
      `${fixture.providerURL}/api/v1`,
    ],
    {
      cwd: fixture.workspace,
      env: fixture.environment,
      stdio: ["pipe", "pipe", "pipe"],
    },
  );
  const stream = acp.ndJsonStream(
    Writable.toWeb(child.stdin),
    Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
  );
  try {
    return await acp
      .client({ name: "ox-browser-smoke" })
      .onNotification(acp.methods.client.session.update, () => {})
      .connectWith(stream, async (connection) => {
        await connection.request(acp.methods.agent.initialize, {
          clientCapabilities: {},
          clientInfo: { name: "ox-browser-smoke", version: "0" },
          protocolVersion: acp.PROTOCOL_VERSION,
        });
        const session = await connection.request(acp.methods.agent.session.new, {
          cwd: fixture.workspace,
          mcpServers: [],
        });
        const result = await connection.request(acp.methods.agent.session.prompt, {
          prompt: [{ text: "smoke", type: "text" }],
          sessionId: session.sessionId,
        });
        expect(result.stopReason).toBe("end_turn");
        return session.sessionId;
      });
  } finally {
    child.stdin.end();
    await once(child, "exit");
  }
}

async function holdOxSession(fixture: Fixture, prompt: string): Promise<{ close(): Promise<void> }> {
  const child = spawn(
    fixture.binary,
    [
      "--credential-file",
      fixture.credential,
      "--model",
      "test/model",
      "--no-keyring",
      "--openrouter-base-url",
      `${fixture.providerURL}/api/v1`,
    ],
    {
      cwd: fixture.workspace,
      env: fixture.environment,
      stdio: ["pipe", "pipe", "pipe"],
    },
  );
  const stream = acp.ndJsonStream(
    Writable.toWeb(child.stdin),
    Readable.toWeb(child.stdout) as unknown as ReadableStream<Uint8Array>,
  );
  const connection = acp
    .client({ name: "ox-browser-lock-holder" })
    .onNotification(acp.methods.client.session.update, () => {})
    .connect(stream);
  try {
    await connection.agent.request(acp.methods.agent.initialize, {
      clientCapabilities: {},
      clientInfo: { name: "ox-browser-lock-holder", version: "0" },
      protocolVersion: acp.PROTOCOL_VERSION,
    });
    const session = await connection.agent.request(acp.methods.agent.session.new, {
      cwd: fixture.workspace,
      mcpServers: [],
    });
    const result = await connection.agent.request(acp.methods.agent.session.prompt, {
      prompt: [{ text: prompt, type: "text" }],
      sessionId: session.sessionId,
    });
    expect(result.stopReason).toBe("end_turn");
    return {
      async close() {
        if (child.exitCode !== null || child.signalCode !== null) return;
        const exited = once(child, "exit");
        connection.close();
        child.stdin.end();
        await exited;
      },
    };
  } catch (error) {
    connection.close();
    child.stdin.end();
    if (child.exitCode === null && child.signalCode === null) await once(child, "exit");
    throw error;
  }
}

// Modes are named by Ox, so the helper chooses and asserts by the label the
// agent advertised rather than by the value behind it.
async function chooseMode(page: Page, mode: string): Promise<void> {
  const control = page.getByLabel("Mode", { exact: true });
  await control.click();
  await page.getByRole("option", { name: mode, exact: true }).click();
  await expect(control).toHaveText(mode);
}

async function sendPrompt(page: Page): Promise<void> {
  await page.getByLabel("Message", { exact: true }).press("Enter");
}

async function registerWorkspace(page: Page, path: string, error?: string): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "Add workspace" });
  if (!await dialog.isVisible()) {
    const emptyAction = page.getByRole("main").getByRole("button", { name: "Add workspace" });
    if (await emptyAction.isVisible()) {
      await emptyAction.click();
    } else {
      await page
        .getByRole("navigation", { name: "Workspaces and conversations" })
        .getByRole("button", { name: "Add workspace" })
        .click();
    }
  }
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Workspace path").fill(path);
  await dialog.getByRole("button", { name: "Register workspace" }).click();
  if (error !== undefined) {
    await expect(dialog.getByRole("alert")).toHaveText(error);
    return;
  }
  await expect(dialog).toBeHidden();
}

async function bottomDistance(locator: Locator): Promise<number> {
  return locator.evaluate((element) => element.scrollHeight - element.scrollTop - element.clientHeight);
}

async function expectNoHorizontalOverflow(locator: Locator): Promise<void> {
  await expect
    .poll(() => locator.evaluate((element) => element.scrollWidth - element.clientWidth))
    .toBeLessThanOrEqual(1);
}

function conversationList(page: Page, workspace?: string) {
  return page.getByRole("list", {
    exact: workspace !== undefined,
    name: workspace === undefined ? /conversations$/ : `${workspace} conversations`,
  });
}

function newConversation(page: Page, workspace?: string) {
  return page.getByRole("button", {
    exact: workspace !== undefined,
    name: workspace === undefined ? /^New conversation in / : `New conversation in ${workspace}`,
  });
}

function workspaceActions(page: Page, workspace?: string) {
  return page.getByRole("button", {
    exact: workspace !== undefined,
    name: workspace === undefined ? /^Workspace actions for / : `Workspace actions for ${workspace}`,
  });
}

async function removeWorkspace(page: Page, workspace: string): Promise<void> {
  await workspaceActions(page, workspace).click();
  await page.getByRole("menuitem", { name: `Remove ${workspace}` }).click();
}

// Opening one of a workspace's conversations is how the browser switches to it.
async function showWorkspace(page: Page, workspace: string): Promise<void> {
  await conversationList(page, workspace).getByRole("link").first().click();
  await assertShowing(page, workspace);
}

// The conversation header names only the conversation, so the workspace the
// browser is showing is identified where the sidebar marks it as current.
async function assertShowing(page: Page, workspace: string): Promise<void> {
  await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
  await expect(
    page.getByRole("list", { name: "Workspaces" }).locator('li[aria-current="true"]'),
  ).toContainText(workspace);
}

async function assertReady(page: Page, url: string): Promise<void> {
  await page.goto(url);
  await expect(page.getByRole("heading", { level: 1, name: "Ox" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Workspaces and conversations" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
}

// Workspace settings replace the conversation, so a scenario that reads them
// navigates there and back.
async function openWorkspaceSettings(page: Page, workspace?: string): Promise<void> {
  await workspaceActions(page, workspace).click();
  await page.getByRole("menuitem", {
    exact: workspace !== undefined,
    name: workspace === undefined ? /^Settings for / : `Settings for ${workspace}`,
  }).click();
  await expect(page.getByRole("region", { name: "Workspace settings" })).toBeVisible();
}

async function saveMCPServers(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Save MCP servers" }).click();
  await expect(page.getByText("MCP servers saved", { exact: true })).toBeVisible();
}

async function startNewConversation(page: Page): Promise<void> {
  const conversations = conversationList(page).getByRole("listitem");
  const count = await conversations.count();
  await newConversation(page).click();
  await expect(conversations).toHaveCount(count + 1);
}

async function oxPID(pidFile: string): Promise<number> {
  await expect.poll(() => readFile(pidFile, "utf8").then((value) => value.trim(), () => "")).not.toBe("");
  return Number.parseInt((await readFile(pidFile, "utf8")).trim(), 10);
}

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return (error as NodeJS.ErrnoException).code !== "ESRCH";
  }
}

async function run(command: string, arguments_: string[], cwd: string): Promise<void> {
  const child = spawn(command, arguments_, { cwd, stdio: "inherit" });
  const [code] = (await once(child, "exit")) as [number | null];
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}`);
  }
}
