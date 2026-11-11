import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { promisify } from "node:util";

const target = resolve(import.meta.dirname, "../../target/debug");

/** The path of a binary the workspace builds. */
export function binary(name: string): string {
  return join(target, name);
}

/**
 * Starts a session of the scripted fake agent in a new folder with `oxctl`,
 * and returns the folder's name, which the session list shows. Each line of a
 * prompt to the fake agent is a directive, such as `say 3 0`; its module
 * documentation lists them.
 */
export async function startSession(): Promise<string> {
  const cwd = await mkdtemp(join(process.env.OX_E2E_DIR!, "session-"));
  await oxctl("new", "--cwd", cwd, "--", binary("oxd-fake-agent"));
  return basename(cwd);
}

/** Closes every session oxd has. */
export async function closeSessions() {
  const listing = await oxctl("ls");
  for (const line of listing.split("\n").filter(Boolean)) {
    await oxctl("close", line.split("\t")[0]!);
  }
}

async function oxctl(...args: string[]): Promise<string> {
  const { stdout } = await promisify(execFile)(binary("oxctl"), args);
  return stdout;
}

/** The session list's entry for the session in `folder`. */
export function sessionEntry(folder: string) {
  return $(`//li[@class="session"][.//span[@class="folder"][.="${folder}"]]`);
}

/** Views the session in `folder`. */
export async function openSession(folder: string) {
  const select = sessionEntry(folder).$("button.select");
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
