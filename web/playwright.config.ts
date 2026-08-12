import { defineConfig, devices } from "@playwright/test"

// E2E drives the REAL harbrr binary (embedded UI + SQLite) against a fresh
// data dir per run, so every run starts at the first-run setup wizard. The
// binary embeds web/dist at compile time — run via `make web-e2e` (rebuilds
// frontend + binary) so the tests never exercise a stale bundle.
export default defineConfig({
  testDir: "./e2e",
  workers: 1, // one shared server + one admin account: tests are stateful
  retries: process.env.CI ? 2 : 0,
  // list keeps failures visible in the live CI log; html writes the directory
  // the workflow uploads on failure (never auto-serving it).
  reporter: [["list"], ["html", { outputFolder: "playwright-report", open: "never" }]],
  use: {
    baseURL: "http://127.0.0.1:7479",
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "rm -rf .e2e-data && ../bin/harbrr serve --host 127.0.0.1 --port 7479 --data-dir .e2e-data",
    url: "http://127.0.0.1:7479/healthz",
    reuseExistingServer: false,
  },
})
