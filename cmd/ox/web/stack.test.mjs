import assert from "node:assert/strict";
import { appendFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { afterEach, beforeEach, test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";
import { runStack } from "./stack.mjs";

let dir;
let stack;
let input;
let lines;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "stack-"));
  input = new PassThrough();
  lines = [];
});

afterEach(async () => {
  stack?.stop(0);
  await stack;
  stack = undefined;
  rmSync(dir, { recursive: true, force: true });
});

function script(name, body = "exec sleep 1000") {
  const path = join(dir, name);
  writeFileSync(path, `#!/bin/sh\necho $$ > ${path}.pid\n${body}\n`, { mode: 0o755 });
  return path;
}

function pid(path) {
  try {
    return readFileSync(`${path}.pid`, "utf8").trim();
  } catch {
    return "";
  }
}

async function until(condition) {
  for (let i = 0; i < 200; i++) {
    const value = condition();
    if (value) return value;
    await sleep(25);
  }
  assert.fail("timed out");
}

// start runs the stack with a build that appends a comment to each script
// named in changed, or exits nonzero when fail is set. The build waits
// buildDelay seconds first, and ox runs oxBody.
async function start({ changed = [], fail = false, restart = {}, buildDelay = 0, oxBody } = {}) {
  const paths = { oxd: script("oxd"), ox: script("ox", oxBody), vite: script("vite") };
  const edits = changed.map((name) => `echo '#' >> ${paths[name]}`).join("; ");
  stack = runStack({
    services: Object.entries(paths).map(([name, command]) => ({
      name,
      command,
      restart: restart[name] ?? name !== "vite",
    })),
    build: fail ? "exit 1" : `sleep ${buildDelay}; ${edits || ":"}`,
    input,
    log: (line) => lines.push(line),
  });
  await until(() => Object.values(paths).every((path) => pid(path)));
  return paths;
}

function snapshot(paths) {
  return Object.fromEntries(Object.entries(paths).map(([name, path]) => [name, pid(path)]));
}

test("a changed binary restarts alone", async () => {
  const paths = await start({ changed: ["ox"] });
  const before = snapshot(paths);
  input.write("\n");
  await until(() => lines.includes("restarted ox") && pid(paths.ox) !== before.ox);
  const after = snapshot(paths);
  assert.notEqual(after.ox, before.ox);
  assert.equal(after.oxd, before.oxd);
  assert.equal(after.vite, before.vite);
  assert.equal(process.kill(Number(after.oxd), 0), true);
});

test("an unchanged binary is not restarted", async () => {
  const paths = await start();
  const before = snapshot(paths);
  input.write("\n");
  await until(() => lines.includes("nothing changed"));
  assert.deepEqual(snapshot(paths), before);
});

test("a failed build restarts nothing", async () => {
  const paths = await start({ fail: true });
  const before = snapshot(paths);
  input.write("\n");
  await until(() => lines.some((line) => line.startsWith("build failed")));
  assert.deepEqual(snapshot(paths), before);
  appendFileSync(paths.ox, "#\n");
  input.write("\n");
  await until(() => lines.filter((line) => line.startsWith("build failed")).length === 2);
  assert.deepEqual(snapshot(paths), before);
});

test("a service with restart false is not restarted", async () => {
  const paths = await start({ changed: ["vite"] });
  const before = snapshot(paths);
  input.write("\n");
  await until(() => lines.includes("nothing changed"));
  assert.deepEqual(snapshot(paths), before);
});

test("an exit the stack did not cause stops the stack", async () => {
  const paths = await start();
  const oxd = Number(pid(paths.oxd));
  const ox = Number(pid(paths.ox));
  process.kill(Number(pid(paths.vite)), "SIGKILL");
  const code = await stack;
  assert.notEqual(code, 0);
  await until(() => !alive(oxd) && !alive(ox));
});

test("an exit after a restart stops the stack", async () => {
  const paths = await start({ changed: ["ox"] });
  const before = pid(paths.ox);
  input.write("\n");
  await until(() => lines.includes("restarted ox") && pid(paths.ox) !== before);
  process.kill(Number(pid(paths.ox)), "SIGKILL");
  assert.notEqual(await stack, 0);
});

test("a second Enter during a rebuild does not start another", async () => {
  const paths = await start({ changed: ["ox"], buildDelay: 0.3 });
  const before = pid(paths.ox);
  input.write("\n\n");
  await until(() => lines.includes("restarted ox") && pid(paths.ox) !== before);
  await sleep(500);
  assert.ok(lines.includes("a rebuild is in progress"));
  assert.equal(lines.filter((line) => line === "restarted ox").length, 1);
});

test("stopping during a restart starts no new process", async () => {
  const paths = await start({
    changed: ["ox"],
    oxBody: "trap 'sleep 0.5; exit 0' TERM\nwhile :; do sleep 0.05; done",
  });
  const before = pid(paths.ox);
  input.write("\n");
  await sleep(300);
  stack.stop(0);
  await sleep(1000);
  assert.equal(pid(paths.ox), before);
  assert.equal(lines.includes("restarted ox"), false);
});

function alive(id) {
  try {
    process.kill(id, 0);
    return true;
  } catch {
    return false;
  }
}
