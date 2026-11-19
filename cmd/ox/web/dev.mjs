import { spawn } from "node:child_process";

// The daemon, relay, and Vite serve one dev session. If one exits, the
// others must stop too, especially when another daemon owns the socket.
const children = [
  spawn("bin/oxd", [], { stdio: "inherit" }),
  spawn("bin/ox", ["-addr", "localhost:8081"], { stdio: "inherit" }),
  spawn("node_modules/.bin/vite", [], { cwd: import.meta.dirname, stdio: "inherit" }),
];

let stopping = false;
function stop(code) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  for (const child of children) child.kill();
}

for (const child of children) {
  child.on("error", (error) => {
    console.error(error.message);
    stop(1);
  });
  child.on("exit", (code) => stop(code ?? 1));
}
process.on("SIGINT", () => stop(130));
process.on("SIGTERM", () => stop(143));
