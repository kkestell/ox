import { lstatSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runStack } from "./stack.mjs";

// freePort resolves to a port the OS reports free, found by listening on port
// 0 and closing.
function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "localhost", () => {
      const { port } = server.address();
      server.close(() => resolve(String(port)));
    });
  });
}

// A linked worktree's .git is a file. Its stack keeps its configuration,
// state, and socket under the checkout's .state and serves on a free port, so
// it runs beside the main checkout's stack and the user's Ox. The paths go by
// flag rather than XDG variables, so the Claude sessions oxd starts, and their
// tools, keep the user's environment. Its session worktrees go outside every
// checkout, since oxd refuses a worktree directory inside the repository.
const checkout = join(import.meta.dirname, "../../..");
const oxdArgs = [];
const oxArgs = [];
let port = "8080";
if (lstatSync(join(checkout, ".git")).isFile()) {
  const state = join(checkout, ".state");
  const config = `-config=${join(state, "config.json")}`;
  const socket = `-socket=${join(state, "oxd.sock")}`;
  oxdArgs.push(
    config,
    socket,
    `-db=${join(state, "ox.db")}`,
    `-ur=${join(state, "ur")}`,
    `-worktrees=${join(tmpdir(), "ox-dev-worktrees")}`,
  );
  oxArgs.push(config, socket);
  port = await freePort();
}
oxArgs.push("-addr", `localhost:${port}`);

// The daemon and relay serve one dev session. If one exits on its own, the
// other must stop too, especially when another daemon owns the socket.
const stack = runStack({
  services: [
    { name: "oxd", command: "bin/oxd", args: oxdArgs },
    { name: "ox", command: "bin/ox", args: oxArgs },
  ],
  build: "make build",
  input: process.stdin,
  signals: process,
});

console.log(`Ox is at http://localhost:${port}/`);
console.log("Press Enter to rebuild and restart what changed.");
process.exitCode = await stack;
