import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI ? "github" : "line",
  outputDir: "../.tmp/playwright",
  use: {
    baseURL: "http://127.0.0.1:3766",
    channel: process.env.CI ? undefined : "chrome",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: {
    command: "go run ../cmd/sfs --config ../config.demo.json serve --listen 127.0.0.1:3766",
    cwd: ".",
    url: "http://127.0.0.1:3766/v1/health",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
