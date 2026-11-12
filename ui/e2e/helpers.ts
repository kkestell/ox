import { execFile } from "node:child_process";
import { mkdtemp, realpath, writeFile } from "node:fs/promises";
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

/**
 * Adds a new Git repository, with `main` at one commit, as a workspace, and
 * returns its path and workspace ID.
 */
export async function gitWorkspace(): Promise<{ cwd: string; workspace: string }> {
  const cwd = await tempDir("repo-");
  await git(cwd, "init", "-b", "main");
  await commit(cwd);
  return { cwd, workspace: await addWorkspace(cwd) };
}

/**
 * Adds a clone of a new bare repository with branches `main` and `feature` as
 * a workspace, and returns its path and workspace ID, and the path of
 * another clone, `pusher`, which updates the remote without the workspace
 * learning of it.
 */
export async function gitWorkspaceWithRemote(): Promise<{
  cwd: string;
  workspace: string;
  pusher: string;
}> {
  const remote = await tempDir("remote-");
  await git(remote, "init", "--bare", "-b", "main");
  const pusher = await tempDir("pusher-");
  await git(pusher, "init", "-b", "main");
  await git(pusher, "remote", "add", "origin", remote);
  await commit(pusher);
  await git(pusher, "push", "origin", "main", "main:feature");
  const cwd = await tempDir("repo-");
  await git(cwd, "clone", remote, ".");
  return { cwd, workspace: await addWorkspace(cwd), pusher };
}

/** Starts a session of the fake agent in a new worktree of `workspace`. */
export async function startWorktreeSession(workspace: string) {
  await oxctl("new", "--workspace", workspace, "--worktree", "--", binary("oxd-fake-agent"));
}

/** Runs git in `cwd` and returns its output. */
export async function git(cwd: string, ...args: string[]): Promise<string> {
  const { stdout } = await promisify(execFile)("git", args, { cwd });
  return stdout;
}

/** Commits a change to a new file in `cwd`, with an identity of its own. */
export async function commit(cwd: string) {
  await writeFile(join(cwd, `${Date.now()}-${Math.random()}.txt`), "change\n");
  await git(cwd, "add", ".");
  await git(
    cwd,
    ...["-c", "user.name=Ox", "-c", "user.email=ox@example.com", "-c", "commit.gpgsign=false"],
    ...["commit", "-m", "change"],
  );
}

async function tempDir(prefix: string): Promise<string> {
  return realpath(await mkdtemp(join(process.env.OX_E2E_DIR!, prefix)));
}

async function addWorkspace(cwd: string): Promise<string> {
  return (await oxctl("workspace", "add", cwd)).trim();
}

/** Deletes every session oxd has and removes every workspace. */
export async function resetDaemon() {
  for (const line of lines(await oxctl("ls"))) {
    await oxctl("delete", "--discard", line.split("\t")[0]!);
  }
  for (const line of lines(await oxctl("workspace", "ls"))) {
    await oxctl("workspace", "rm", line.split("\t")[0]!);
  }
}

/**
 * Stops the run's oxd. It runs as `oxd --socket <OX_SOCKET>`, as the client
 * starts it.
 */
export async function stopDaemon() {
  const socket = process.env.OX_SOCKET!.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  await promisify(execFile)("pkill", ["-TERM", "-f", `oxd --socket ${socket}$`]);
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

/** The session list's entry for the one session in a worktree. */
export function worktreeEntry() {
  const worktrees = join(process.env.OX_E2E_DIR!, "worktrees/");
  return $(`//li[@class="session"][button[@class="select"][starts-with(@title, "${worktrees}")]]`);
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

/**
 * Moves the slider `selector` matches to `value`. The embedded driver neither
 * drags a slider's thumb nor sends it keys, so this sets the value through the
 * input's own setter, which React's tracking does not see, and dispatches
 * `input`, as the webview does when a user moves the thumb.
 */
export async function slide(selector: string, value: number) {
  await $(selector).waitForExist();
  await browser.execute(
    (selector, value) => {
      const input = document.querySelector<HTMLInputElement>(selector)!;
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(
        input,
        String(value),
      );
      input.dispatchEvent(new Event("input", { bubbles: true }));
    },
    selector,
    value,
  );
}
