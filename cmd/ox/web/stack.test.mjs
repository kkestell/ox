import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { appendFileSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { afterEach, beforeEach, test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";
import { runStack, stopProcess } from "./stack.mjs";

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

// script writes a service that sleeps until killed, or, given trap, sets the
// trap before writing its PID and then waits in a loop that lets it run. The
// loop waits on each sleep with the wait builtin, which a trapped signal
// interrupts at once, while sh can drop a SIGINT that arrives during a
// foreground command.
function script(name, trap) {
  const path = join(dir, name);
  const body = trap
    ? `${trap}\necho $$ > ${path}.pid\nwhile :; do sleep 0.05 & wait $!; done`
    : `echo $$ > ${path}.pid\nexec sleep 1000`;
  writeFileSync(path, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
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
// buildDelay seconds first. Each service runs with the trap traps names for
// it, and the stack takes signals.
async function start({ changed = [], fail = false, restart = {}, buildDelay = 0, traps = {}, signals } = {}) {
  const paths = Object.fromEntries(["oxd", "ox", "vite"].map((name) => [name, script(name, traps[name])]));
  const edits = changed.map((name) => `echo '#' >> ${paths[name]}`).join("; ");
  stack = runStack({
    services: Object.entries(paths).map(([name, command]) => ({
      name,
      command,
      restart: restart[name] ?? name !== "vite",
    })),
    build: fail ? "exit 1" : `sleep ${buildDelay}; ${edits || ":"}`,
    input,
    signals,
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
  const paths = await start({ traps: { vite: "trap 'exit 3' USR1" } });
  const oxd = Number(pid(paths.oxd));
  const ox = Number(pid(paths.ox));
  process.kill(Number(pid(paths.vite)), "SIGUSR1");
  assert.equal(await stack, 3);
  assert.deepEqual(lines, []);
  assert.ok(!alive(oxd) && !alive(ox));
});

test("an exit after a restart stops the stack", async () => {
  const paths = await start({ changed: ["ox"] });
  const before = pid(paths.ox);
  input.write("\n");
  await until(() => lines.includes("restarted ox") && pid(paths.ox) !== before);
  process.kill(Number(pid(paths.ox)), "SIGKILL");
  assert.notEqual(await stack, 0);
});

test("a retiring service that fails stops the stack", async () => {
  const paths = await start({ changed: ["oxd"], traps: { oxd: "trap 'exit 66' TERM" } });
  const before = pid(paths.oxd);
  input.write("\n");
  assert.notEqual(await stack, 0);
  assert.equal(lines.filter((line) => line === `${paths.oxd} exited with status 66`).length, 1);
  assert.equal(lines.includes("restarted oxd"), false);
  assert.equal(pid(paths.oxd), before);
});

test("a service that fails on Ctrl-C fails the stack", async () => {
  const signals = new EventEmitter();
  const paths = await start({ traps: { oxd: "trap 'exit 66' INT" }, signals });
  for (const path of Object.values(paths)) process.kill(Number(pid(path)), "SIGINT");
  signals.emit("SIGINT");
  assert.equal(await stack, 1);
  assert.deepEqual(lines, [`${paths.oxd} exited with status 66`]);
});

// signalStack sends signal to the stack, after sending it to every service
// too when the terminal delivers it, and expects each service to have received
// it alone. A service takes a first SIGINT as a graceful stop that ends 0.3
// seconds later and a second as a forced stop that exits 1, as oxd does. In
// the terminal case the stack's signal waits until every service has handled
// the first.
async function signalStack(signal, terminal) {
  const names = ["oxd", "ox", "vite"];
  const signals = new EventEmitter();
  const record =
    "trap 'echo INT >> $0.log; [ -e $0.int ] && exit 1; touch $0.int; (sleep 0.3; kill -USR1 $$) &' INT; " +
    "trap 'exit 0' USR1; trap 'echo TERM >> $0.log; exit 0' TERM";
  const paths = await start({ traps: Object.fromEntries(names.map((name) => [name, record])), signals });
  const pids = names.map((name) => Number(pid(paths[name])));
  if (terminal) {
    for (const id of pids) process.kill(id, signal);
    await until(() => names.every((name) => existsSync(`${paths[name]}.int`)));
  }
  signals.emit(signal);
  const code = await stack;
  assert.ok(pids.every((id) => !alive(id)));
  for (const name of names) assert.equal(readFileSync(`${paths[name]}.log`, "utf8"), `${signal.slice(3)}\n`);
  return code;
}

test("one terminal Ctrl-C signals each service once", async () => {
  // A terminal Ctrl-C reaches every process in the foreground group.
  assert.equal(await signalStack("SIGINT", true), 130);
});

test("SIGTERM to the stack signals every service", async () => {
  assert.equal(await signalStack("SIGTERM", false), 143);
});

test("stopProcess accepts only status 0, the signal it sent, or 128 plus its number", async () => {
  const spawned = (path) => {
    const proc = spawn(path, { stdio: "ignore" });
    return until(() => pid(path)).then(() => proc);
  };
  await assert.rejects(stopProcess(await spawned(script("failing", "trap 'exit 66' TERM"))), /status 66$/);
  await stopProcess(await spawned(script("killed", "trap 'exit 66' TERM")), "SIGKILL");
  await stopProcess(await spawned(script("terminated")));
  await stopProcess(await spawned(script("clean", "trap 'exit 0' TERM")));
  await stopProcess(await spawned(script("handled", "trap 'exit 143' TERM")));
  const crashing = await spawned(script("crashing", "trap 'kill -KILL $$' TERM"));
  await assert.rejects(stopProcess(crashing), /exited on SIGKILL$/);
  const exited = spawn("sh", ["-c", "exit 3"]);
  await new Promise((resolve) => exited.once("exit", resolve));
  await assert.rejects(stopProcess(exited), /^Error: sh exited with status 3$/);
});

test("a second Enter during a rebuild does not start another", async () => {
  const paths = await start({ changed: ["ox"], buildDelay: 0.3 });
  const before = pid(paths.ox);
  input.write("\n\n");
  await until(() => lines.includes("restarted ox") && pid(paths.ox) !== before);
  // Both lines are read before the first build ends, so a second rebuild
  // would already have logged.
  assert.equal(lines.filter((line) => line === "rebuilding").length, 1);
  assert.ok(lines.includes("a rebuild is in progress"));
});

test("stopping during a restart starts no new process", async () => {
  const paths = await start({
    changed: ["ox"],
    traps: { ox: "trap 'touch $0.stopping; sleep 0.5; exit 0' TERM" },
  });
  const before = pid(paths.ox);
  input.write("\n");
  await until(() => existsSync(`${paths.ox}.stopping`));
  stack.stop(0);
  await stack;
  assert.equal(alive(Number(before)), false);
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
