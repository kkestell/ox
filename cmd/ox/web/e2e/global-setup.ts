// globalSetup builds the web client and the binaries, with the race
// detector, into a temporary directory that services.ts runs them from.
// OX_SCREENSHOTS builds oxd with the scene player.
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export default function globalSetup() {
  const root = resolve(import.meta.dirname, "../../../..");
  const bin = mkdtempSync(join(tmpdir(), "ox-e2e-bin-"));
  execFileSync("npm", ["run", "build"], { cwd: join(root, "cmd/ox/web"), stdio: "inherit" });
  const tags = process.env.OX_SCREENSHOTS ? ["-tags=screenshots"] : [];
  execFileSync("go", ["build", "-race", ...tags, "-o", `${bin}/`, "./cmd/ox", "./cmd/ox-mcp", "./cmd/oxd"], {
    cwd: root,
    stdio: "inherit",
  });
  process.env.OX_E2E_BIN = bin;
  return () => rmSync(bin, { recursive: true, force: true });
}
