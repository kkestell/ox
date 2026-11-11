import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { binary, closeSessions } from "./helpers.ts";

// The launcher makes the run's directory and points the run at an oxd socket
// in it. The app it launches, the oxd the app starts, and the workers it
// starts inherit both.
process.env.OX_E2E_DIR ??= mkdtempSync(join(tmpdir(), "ox-e2e-"));
process.env.OX_SOCKET = join(process.env.OX_E2E_DIR, "oxd.sock");

export const config: WebdriverIO.Config = {
  runner: "local",
  specs: ["./specs/**/*.e2e.ts"],
  maxInstances: 1,
  framework: "mocha",
  reporters: ["spec"],
  logLevel: "warn",
  waitforTimeout: 10_000,
  mochaOpts: { ui: "bdd", timeout: 60_000 },
  services: [
    [
      "@wdio/tauri-service",
      { appBinaryPath: binary("ox"), driverProvider: "embedded", captureBackendLogs: true },
    ],
  ],
  capabilities: [{ browserName: "tauri" }],

  // The webview loads the UI after the WebDriver session starts; each spec
  // begins once the session list has rendered. Choosing the app's one window
  // explicitly turns off the service's window focus check before each
  // command, which asks tauri-plugin-wdio, a plugin the app does not have.
  async before() {
    await browser.switchToWindow(await browser.getWindowHandle());
    await $("button=New Session").waitForDisplayed({ timeout: 30_000 });
  },

  // Each test starts with no sessions. Once the app exits, oxd has no
  // clients and no sessions, and exits after its idle period.
  async afterTest() {
    await closeSessions();
  },

  onComplete() {
    console.log(`End-to-end run files: ${process.env.OX_E2E_DIR}`);
  },
};
