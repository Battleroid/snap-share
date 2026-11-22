import { defineConfig } from "@playwright/test";

// BASE_URL is the instance's SNAP_PUBLIC_URL (the CSP only allows scripts from that origin).
export default defineConfig({
  testDir: "./specs",
  outputDir: "./out/results",
  timeout: 30_000,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.BASE_URL,
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
  },
});
