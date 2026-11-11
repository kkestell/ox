import { execFile } from "node:child_process";
import { mkdtemp, realpath } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";

const target = resolve(import.meta.dirname, "../../target/debug");

/** The path of a binary the workspace builds. */
export function binary(name: string): string {
  return join(target, name);
}

/**
 * Adds a new folder as a workspace with `oxctl`, starts a session of the
 * scripted fake agent in it, and returns the folder's path, which the
 * session's entry shows on hover. Each line of a prompt to the fake agent is a
 * directive, such as `say 3 0`; its module documentation lists them.
 */
export async function startSession(): Promise<string> {
  const cwd = await realpath(await mkdtemp(join(process.env.OX_E2E_DIR!, "session-")));
  const workspace = (await oxctl("workspace", "add", cwd)).trim();
  await oxctl("new", "--workspace", workspace, "--", binary("oxd-fake-agent"));
  return cwd;
}

/** Closes every session oxd has and removes every workspace. */
export async function resetDaemon() {
  for (const line of lines(await oxctl("ls"))) {
    await oxctl("close", line.split("\t")[0]!);
  }
  for (const line of lines(await oxctl("workspace", "ls"))) {
    await oxctl("workspace", "rm", line.split("\t")[0]!);
  }
}

function lines(output: string): string[] {
  return output.split("\n").filter(Boolean);
}

async function oxctl(...args: string[]): Promise<string> {
  const { stdout } = await promisify(execFile)(binary("oxctl"), args);
  return stdout;
}

/** The session list's group for the workspace in `cwd`. */
export function workspaceGroup(cwd: string) {
  return $(`//section[@class="workspace"][.//h2[@title="${cwd}"]]`);
}

/** The session list's entry for the session in `cwd`. */
export function sessionEntry(cwd: string) {
  return $(`//li[@class="session"][button[@class="select"][@title="${cwd}"]]`);
}

/** Views the session in `cwd`. */
export async function openSession(cwd: string) {
  const select = sessionEntry(cwd).$("button.select");
  await select.click();
  await expect(select).toHaveAttribute("aria-current", "true");
}

export function composer() {
  return $("form.composer textarea");
}

export function sendButton() {
  return $('form.composer button[aria-label="Send"]');
}

/** Sends `text`, or what the composer holds, once the session can take a prompt. */
export async function send(text?: string) {
  if (text !== undefined) await composer().setValue(text);
  await expect(sendButton()).toBeEnabled();
  await sendButton().click();
}

/** The agent's text items in the viewed conversation. */
export function agentText() {
  return $$(".conversation > p.text");
}

/**
 * Chooses the option with `value` in the select `selector` matches. The
 * embedded driver clicks with `element.click()`, which selects no option, so
 * this sets the value and dispatches `change`, as the webview does when a user
 * chooses an option.
 */
export async function choose(selector: string, value: string) {
  await $(selector).waitForExist();
  await browser.execute(
    (selector, value) => {
      const select = document.querySelector<HTMLSelectElement>(selector)!;
      select.value = value;
      select.dispatchEvent(new Event("change", { bubbles: true }));
    },
    selector,
    value,
  );
}
