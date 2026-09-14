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

test("independently drives Ox through the deterministic provider", async () => {
  const fixture = await createFixture();
  try {
    await driveOx(fixture);
    expect(fixture.requests).toBe(1);
  } finally {
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
    await expect(page.getByText("Register a server-local workspace to begin.")).toBeVisible();
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toHaveCount(0);

    await page.getByLabel("Workspace path").fill("/definitely/not/an/ox-workspace");
    await page.getByRole("button", { name: "Register workspace" }).click();
    await expect(page.getByRole("alert")).toHaveText("workspace must be a listable directory");

    await page.getByLabel("Workspace path").fill(fixture.workspace);
    await page.getByRole("button", { name: "Register workspace" }).click();
    await expect(page.getByRole("navigation", { name: "Workspaces and conversations" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();

    const sidebar = await boundingBox(page.getByRole("navigation", { name: "Workspaces and conversations" }));
    const primary = await boundingBox(page.locator("main"));
    expect(sidebar.x + sidebar.width).toBeLessThanOrEqual(primary.x);

    await page.getByLabel("Workspace path").fill(second);
    await page.getByRole("button", { name: "Register workspace" }).click();
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

    await page.getByRole("button", { exact: true, name: `Remove ${basename(second)}` }).click();
    await assertShowing(page, basename(fixture.workspace));
    expect(await readFile(join(second, "keep.txt"), "utf8")).toBe("keep");
  } finally {
    await host?.stop();
    await fixture.close();
    await rm(second, { force: true, recursive: true });
  }
});

test("supervises a real Ox process through clean shutdown and unexpected exit", async ({ page }) => {
  const fixture = await createFixture();
  let host: BrowserHost | undefined;
  try {
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    await host.stop();
    await fixture.waitForOxExit();

    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);

    await fixture.stopOx();
    await expect(page.getByText("Ox is unavailable. Open workspace settings for diagnostics.")).toBeVisible();
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
    await page.getByLabel("OpenRouter login credential").fill(credential);
    await page.getByRole("button", { name: "OpenRouter login" }).click();
    await expect(page.getByRole("alert")).toHaveText("OpenRouter login failed");
    await expect(page.getByLabel("OpenRouter login credential")).toHaveValue("");
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
    await expect(page.getByText("Ox is not connected.")).toBeVisible();
    await expect(page.getByRole("region", { name: "Conversation" })).toHaveCount(0);

    await openWorkspaceSettings(page);
    await expect(page.getByRole("region", { name: "Authentication" })).toContainText("Not connected");

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
    await expect(page.getByRole("region", { name: "Session information" })).toContainText("Context:");
    await expect(page.getByLabel("Mode", { exact: true })).toBeVisible();
    await expect(page.getByText("Host revision", { exact: true })).toBeHidden();
    await expect(page.getByText("Session ID", { exact: true })).toBeHidden();
    await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);

    await page.reload();
    await expect(page.getByRole("region", { name: "Conversation" }).getByRole("heading", { name: "smoke" })).toBeVisible();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    secondContext = await browser.newContext();
    const second = await secondContext.newPage();
    await assertReady(second, url);
    await second.getByText("Conversation actions", { exact: true }).click();
    await second.getByRole("button", { name: "Close conversation" }).click();
    await expect(page.getByText("Choose a conversation or start a new one.")).toBeVisible();

    await sessions.getByRole("link", { name: "smoke" }).click();
    await page.getByText("Conversation actions", { exact: true }).click();
    await page.getByRole("button", { name: "Delete conversation" }).click();
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
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("code");
    await page.getByLabel("Mode", { exact: true }).selectOption("plan");
    await expect(page.getByLabel("Mode", { exact: true })).toHaveValue("plan");

    await page.getByLabel("Message").fill("inspect these attachments");
    await page.getByText("Add context", { exact: true }).click();
    await page.getByLabel("Attachments", { exact: true }).setInputFiles([
      { name: "picture.png", mimeType: "image/png", buffer: Buffer.from("image") },
      { name: "sound.wav", mimeType: "audio/wav", buffer: Buffer.from("audio") },
      { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("notes") },
    ]);
    await page.getByLabel("Name").fill("Guide");
    await page.getByLabel("URI").fill("https://example.test/guide");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect.poll(() => fixture.requests).toBe(1);
    const prompt = JSON.stringify(fixture.prompts[0]);
    expect(prompt).toContain("image_url");
    expect(prompt).toContain("input_audio");
    expect(prompt).toContain("notes.txt");
    expect(prompt).toContain("https://example.test/guide");
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");
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
    await page.getByLabel("Mode", { exact: true }).selectOption("auto");
    await page.getByLabel("Message").fill("exercise filesystem callbacks");
    await page.getByRole("button", { name: "Send prompt" }).click();

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
    await page.getByLabel("Mode", { exact: true }).selectOption("auto");
    await page.getByLabel("Message").fill("exercise terminal callbacks");
    await page.getByRole("button", { name: "Send prompt" }).click();

    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript).toContainText("terminal complete");
    await expect(transcript).toContainText("terminal callback output");
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
    await page.getByLabel("Message").fill("hold");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();

    await newConversation(page).click();
    await page.getByLabel("Message").fill("second session");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect.poll(() => fixture.requests).toBe(2);
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    const sessions = conversationList(page).getByRole("listitem");
    await sessions.filter({ hasText: "hold" }).getByRole("link").click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();
    await page.getByRole("button", { name: "Cancel prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeHidden();
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
    await page.getByLabel("Message").fill("hold across reload");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();

    await page.reload();
    await expect(page.getByRole("region", { name: "Conversation" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();

    fixture.releaseHeldPrompt();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("held prompt complete");
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeHidden();
  } finally {
    await host?.stop();
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
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("button", { name: "Cancel prompt" })).toBeVisible();

    await showWorkspace(page, second);
    await page.getByLabel("Message").fill("second workspace turn");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("browser smoke");

    // Following another workspace's settings link selects that workspace.
    await openWorkspaceSettings(page, first);
    await expect(page.getByRole("region", { name: "Workspace settings" }).getByRole("heading", { level: 2 })).toHaveText(first);
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
    await page.getByRole("button", { name: "Send prompt" }).click();
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
    await page.getByRole("button", { name: "Send prompt" }).click();
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
    await page.getByRole("button", { name: "Send prompt" }).click();
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
    host = startBrowserHost(fixture);
    await assertReady(page, await host.url);
    await showWorkspace(page, second);
    await page.getByLabel("Message", { exact: true }).fill("request permission");
    await page.getByRole("button", { name: "Send prompt" }).click();
    const permission = page.getByRole("region", { name: "Transcript" }).getByRole("article", { name: /Permission for/ });
    await expect(permission).toContainText("pwd");
    await expect(conversationList(page, second)).toContainText("Waiting for you");

    await showWorkspace(page, first);
    await expect(permission).toBeHidden();

    await conversationList(page, second)
      .getByRole("listitem")
      .filter({ hasText: "Waiting for you" })
      .getByRole("link")
      .click();

    await expect(permission).toContainText("pwd");
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
    await page.getByRole("button", { name: "Send prompt" }).click();
    const transcript = page.getByRole("region", { name: "Transcript" });
    await expect(transcript.getByRole("article", { name: "Choose a color", exact: true })).toContainText("Choose a color");
    await expect(page.getByRole("heading", { name: "Pending interactions" })).toHaveCount(0);
    await expect(page.getByRole("option", { name: "Red" })).toHaveText("Red");
    await expect(page.getByRole("combobox", { name: "Answer" })).toHaveValue("");
    await page.reload();
    await expect(transcript.getByRole("article", { name: "Choose a color", exact: true })).toContainText("Choose a color");
    await page.getByRole("combobox", { name: "Answer" }).selectOption("Red");
    await page.getByRole("button", { name: "Submit answer" }).click();
    await expect(transcript.getByRole("article", { name: "Choose a color", exact: true })).toBeHidden();
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
    await page.getByRole("button", { name: "Send prompt" }).click();
    const permission = page.getByRole("region", { name: "Transcript" }).getByRole("article", { name: /Permission for/ });
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
    await page.getByLabel("Mode", { exact: true }).selectOption("auto");
    await page.getByLabel("Message").fill("use HTTP MCP tool");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP complete");
    await page.getByRole("region", { name: "Transcript" }).getByText("Details", { exact: true }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP fixture result");
    expect(mcp.httpAuthorizations).toContain(mcp.httpSecret);

    await page.getByRole("textbox", { name: "Message" }).fill("use HTTP MCP failure");
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP failure complete");
    await page.getByRole("region", { name: "Transcript" }).getByText("Details", { exact: true }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("HTTP MCP fixture failure");
    await page.getByText("Conversation actions", { exact: true }).click();
    await page.getByRole("button", { name: "Close conversation" }).click();
    await expect(page.getByText("Choose a conversation or start a new one.")).toBeVisible();

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
    await page.getByRole("button", { name: "Send prompt" }).click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("stdio MCP complete");
    await page.getByRole("region", { name: "Transcript" }).getByText("Details", { exact: true }).last().click();
    await expect(page.getByRole("region", { name: "Transcript" })).toContainText("stdio MCP fixture result");
    await expect(page.locator("main")).not.toContainText(mcp.stdioSecret);
    await page.getByText("Conversation actions", { exact: true }).click();
    await page.getByRole("button", { name: "Close conversation" }).click();

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

function startBrowserHost(fixture: Fixture): BrowserHost {
  const arguments_ = [
    "run",
    "./src/host.ts",
    "--port",
    "0",
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
  options: { registerSecondWorkspace?: boolean; registerWorkspace?: boolean } = {},
): Promise<Fixture> {
  const root = await mkdtemp(join(tmpdir(), "ox-client-smoke-"));
  const workspace = join(root, "workspace");
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
          'data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}',
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

async function boundingBox(locator: Locator): Promise<{ height: number; width: number; x: number; y: number }> {
  const box = await locator.boundingBox();
  if (box === null) {
    throw new Error("element has no layout box");
  }
  return box;
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

// Opening one of a workspace's conversations is how the browser switches to it.
async function showWorkspace(page: Page, workspace: string): Promise<void> {
  await conversationList(page, workspace).getByRole("link").first().click();
  await assertShowing(page, workspace);
}

async function assertShowing(page: Page, workspace: string): Promise<void> {
  await expect(page.getByRole("region", { name: "Conversation" }).locator("header")).toContainText(workspace);
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
  await page
    .getByRole("link", {
      exact: workspace !== undefined,
      name: workspace === undefined ? /^Settings for / : `Settings for ${workspace}`,
    })
    .click();
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
