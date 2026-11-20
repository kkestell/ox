import { runStack } from "./stack.mjs";

// The daemon, relay, and Vite serve one dev session. If one exits on its own,
// the others must stop too, especially when another daemon owns the socket.
const stack = runStack({
  services: [
    { name: "oxd", command: "bin/oxd", restart: true },
    { name: "ox", command: "bin/ox", args: ["-addr", "localhost:8081"], restart: true },
    { name: "vite", command: "node_modules/.bin/vite", cwd: import.meta.dirname, restart: false },
  ],
  build: "make build",
  input: process.stdin,
});

process.on("SIGINT", () => stack.stop(130));
process.on("SIGTERM", () => stack.stop(143));
console.log("Press Enter to rebuild and restart what changed.");
process.exitCode = await stack;
