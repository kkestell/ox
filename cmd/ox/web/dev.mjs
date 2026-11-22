import { runStack } from "./stack.mjs";

// OX_RELAY_PORT moves ox off 8081, and vite.config.ts reads it too.
const relayPort = process.env.OX_RELAY_PORT ?? "8081";

// The daemon, relay, and Vite serve one dev session. If one exits on its own,
// the others must stop too, especially when another daemon owns the socket.
const stack = runStack({
  services: [
    { name: "oxd", command: "bin/oxd", restart: true },
    { name: "ox", command: "bin/ox", args: ["-addr", `localhost:${relayPort}`], restart: true },
    { name: "vite", command: "node_modules/.bin/vite", cwd: import.meta.dirname, restart: false },
  ],
  build: "make build",
  input: process.stdin,
  signals: process,
});

console.log("Press Enter to rebuild and restart what changed.");
process.exitCode = await stack;
