import { defineConfig } from "@playwright/test";

// Each test runs its own oxd and relay; see the services fixture in e2e/ui.ts.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: true,
  workers: "100%",
  timeout: 30_000,
  use: { browserName: "chromium", channel: "chrome", headless: true, trace: "retain-on-failure" },
});
