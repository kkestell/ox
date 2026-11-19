import { defineConfig } from "@playwright/test";

// Each test file runs its own oxd and relay; see e2e/services.ts.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  use: { browserName: "chromium", channel: "chrome", headless: true },
});
