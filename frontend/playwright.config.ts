import { defineConfig, devices } from "@playwright/test";

// Browser end-to-end tests: a real Chromium drives the panel exactly as a person
// would. They need a running panel (the embedded interface on :8080 by default)
// with the admin still in the first-start state; see e2e/README.md.
export default defineConfig({
  testDir: "./e2e",
  // The tests build on each other (one admin, one squid), so they run in order.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: [["list"]],
  outputDir: "./e2e/.results",
  use: {
    baseURL: process.env.BASE_URL ?? "http://localhost:8080",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    locale: "en-GB",
    viewport: { width: 1400, height: 1000 },
    // SLOWMO=600 slows every action down so a person can watch the browser.
    launchOptions: { slowMo: Number(process.env.SLOWMO ?? 0) },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1400, height: 1000 } } }],
});
