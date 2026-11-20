import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createInterface } from "node:readline";

function fileHash(path) {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function exited(proc) {
  return new Promise((resolve) => proc.once("exit", resolve));
}

// runStack runs the services as one stack and returns a promise of its exit
// code, with a stop(code) method. An exit the stack did not cause stops every
// service. Each line of input rebuilds with the build command line and
// restarts the services whose binary changed. A service's current process and
// hash belong to its record here, and only a restart replaces them.
export function runStack({ services, build, input, log = console.log }) {
  let stopping = false;
  let busy = false;
  let resolveCode;
  const result = new Promise((resolve) => {
    resolveCode = resolve;
  });
  const lines = createInterface({ input });

  const records = services.map((spec) => ({ spec, proc: null, hash: null }));

  function stop(code) {
    if (stopping) return;
    stopping = true;
    lines.close();
    for (const record of records) record.proc?.kill();
    resolveCode(code);
  }

  function start(record) {
    const { command, args = [], cwd } = record.spec;
    const proc = spawn(command, args, { cwd, stdio: ["ignore", "inherit", "inherit"] });
    record.proc = proc;
    if (record.spec.restart) record.hash = fileHash(command);
    proc.on("error", (error) => {
      console.error(error.message);
      stop(1);
    });
    // An exit is expected exactly when the service has moved off this process.
    proc.on("exit", (code) => {
      if (record.proc === proc) stop(code ?? 1);
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
    const gone = exited(old);
    old.kill();
    await gone;
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

  for (const record of records) start(record);
  return Object.assign(result, { stop });
}
