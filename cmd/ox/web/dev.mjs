import { lstatSync } from "node:fs";
import { createServer } from "node:net";
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
// state, and socket under the checkout's .state and serves on free ports, so
// it runs beside the main checkout's stack and the user's Ox. The paths go by
// flag rather than XDG variables, so the Claude sessions oxd starts, and their
// tools, keep the user's environment.
const checkout = join(import.meta.dirname, "../../..");
const oxdArgs = [];
const oxArgs = [];
if (lstatSync(join(checkout, ".git")).isFile()) {
  const state = join(checkout, ".state");
  const config = `-config=${join(state, "config.json")}`;
  const socket = `-socket=${join(state, "oxd.sock")}`;
  oxdArgs.push(config, socket, `-db=${join(state, "ox.db")}`, `-ur=${join(state, "ur")}`);
  oxArgs.push(config, socket);
  process.env.OX_VITE_PORT ??= await freePort();
  process.env.OX_RELAY_PORT ??= await freePort();
}

// OX_RELAY_PORT moves ox off 8081, and vite.config.ts reads it too.
const relayPort = process.env.OX_RELAY_PORT ?? "8081";
const vitePort = process.env.OX_VITE_PORT ?? "8080";

// The daemon, relay, and Vite serve one dev session. If one exits on its own,
// the others must stop too, especially when another daemon owns the socket.
const stack = runStack({
  services: [
    { name: "oxd", command: "bin/oxd", args: oxdArgs, restart: true },
    { name: "ox", command: "bin/ox", args: [...oxArgs, "-addr", `localhost:${relayPort}`], restart: true },
    { name: "vite", command: "node_modules/.bin/vite", cwd: import.meta.dirname, restart: false },
  ],
  build: "make build",
  input: process.stdin,
  signals: process,
});

console.log(`Ox is at http://localhost:${vitePort}/`);
console.log("Press Enter to rebuild and restart what changed.");
process.exitCode = await stack;
