import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { constants } from "node:os";
import { createInterface } from "node:readline";

function fileHash(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

// exited waits for proc to exit without signalling it, and resolves when it
// exits with status 0, on signal, or with status 128 plus the signal's number,
// as a Node process that handles the signal exits. Any other exit rejects,
// such as the status 66 of a race report.
export function exited(proc, signal = "SIGTERM") {
  return new Promise((resolve, reject) => {
    const judge = (code, received) => {
      if (code === 0 || received === signal || code === 128 + constants.signals[signal]) resolve();
      else if (code !== null) reject(new Error(`${proc.spawnfile} exited with status ${code}`));
      else reject(new Error(`${proc.spawnfile} exited on ${received}`));
    };
    if (proc.exitCode !== null || proc.signalCode !== null) return judge(proc.exitCode, proc.signalCode);
    proc.once("exit", judge);
  });
}

// stopProcess sends signal to proc unless it has already exited, and resolves
// or rejects as exited does.
export function stopProcess(proc, signal = "SIGTERM") {
  if (proc.exitCode === null && proc.signalCode === null) proc.kill(signal);
  return exited(proc, signal);
}

// runStack runs the services as one stack and returns a promise of its exit
// code, with a stop(code) method. An exit the stack did not cause stops every
// service. The code resolves once every process the stack stopped, on a
// restart or a shutdown, has exited, and is 1 when any of them failed. Each
// line of input rebuilds with the build command line and restarts the services
// whose binary changed. A service's current process and hash belong to its
// record here, and only a restart replaces them.
//
// signals, such as process, delivers the stack's own SIGINT and SIGTERM. The
// services share the terminal's process group, so a terminal Ctrl-C has
// already sent them SIGINT, and the stack judges their exits without
// signalling them again.
export function runStack({ services, build, input, signals, log = console.log }) {
  let stopping = false;
  let busy = false;
  let resolveCode;
  const result = new Promise((resolve) => {
    resolveCode = resolve;
  });
  const lines = createInterface({ input });

  const records = services.map((spec) => ({ spec, proc: null, hash: null }));

  // stops holds the judged exit of every process the stack has stopped.
  const stops = [];

  function retire(proc, signal, send) {
    const stopped = send ? stopProcess(proc, signal) : exited(proc, signal);
    stops.push(stopped);
    return stopped;
  }

  // stop ends the stack: it signals each current process unless send is false,
  // waits for every stopped process to exit, and resolves code, or 1 when any
  // of them failed.
  async function stop(code, signal = "SIGTERM", send = true) {
    if (stopping) return;
    stopping = true;
    lines.close();
    for (const record of records) {
      if (record.proc) retire(record.proc, signal, send);
    }
    const settled = await Promise.allSettled(stops);
    for (const { reason } of settled.filter(({ status }) => status === "rejected")) log(reason.message);
    resolveCode(settled.some(({ status }) => status === "rejected") ? 1 : code);
  }

  function start(record) {
    const { command, args = [], cwd } = record.spec;
    const proc = spawn(command, args, { cwd, stdio: ["ignore", "inherit", "inherit"] });
    record.proc = proc;
    if (record.spec.restart) record.hash = fileHash(command);
    proc.on("error", (error) => {
      console.error(error.message);
      record.proc = null;
      stop(1);
    });
    // An exit is expected exactly when the service has moved off this process.
    proc.on("exit", (code) => {
      if (record.proc !== proc) return;
      record.proc = null;
      stop(code ?? 1);
    });
  }

  function runBuild() {
    return new Promise((resolve) => {
      const proc = spawn(build, { shell: true, stdio: "inherit" });
      proc.on("error", () => resolve(false));
      proc.on("exit", (code) => resolve(code === 0));
    });
  }

  async function restart(record) {
    const old = record.proc;
    record.proc = null;
    try {
      await retire(old, "SIGTERM", true);
    } catch {
      stop(1);
      return;
    }
    if (stopping) return;
    start(record);
    log(`restarted ${record.spec.name}`);
  }

  async function rebuild() {
    log("rebuilding");
    if (!(await runBuild())) {
      log("build failed; nothing restarted");
      return;
    }
    let changed = false;
    for (const record of records) {
      if (stopping) return;
      if (!record.spec.restart || fileHash(record.spec.command) === record.hash) continue;
      changed = true;
      await restart(record);
    }
    if (!changed) log("nothing changed");
  }

  lines.on("line", async () => {
    if (stopping) return;
    if (busy) {
      log("a rebuild is in progress");
      return;
    }
    busy = true;
    try {
      await rebuild();
    } finally {
      busy = false;
    }
  });

  signals?.on("SIGINT", () => stop(130, "SIGINT", false));
  signals?.on("SIGTERM", () => stop(143));
  for (const record of records) start(record);
  return Object.assign(result, { stop });
}
