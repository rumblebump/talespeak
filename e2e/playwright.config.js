// Browser tests against the real server binary (docs/testing.md). Build it
// first: CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
import { defineConfig, devices } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const TEST_PACK_URL = "http://127.0.0.1:18765";
export const DEMO_URL = "http://127.0.0.1:18766";

// Each server starts from a fresh data directory.
function server(url, pack) {
  const env = {
    TALESPEAK_LISTEN: url.replace("http://", ""),
    TALESPEAK_DATA: mkdtempSync(join(tmpdir(), "talespeak-e2e-")),
    // Every test opens connections from 127.0.0.1; the default burst of 60 is for real clients.
    TALESPEAK_CONNECT_BURST: "100000",
  };
  if (pack) env.TALESPEAK_PACK = pack;
  return { command: "../bin/talespeak serve", url: url + "/healthz", env, reuseExistingServer: false, timeout: 20000 };
}

export default defineConfig({
  testDir: "tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { baseURL: TEST_PACK_URL, trace: "retain-on-failure", screenshot: "only-on-failure" },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "phone", use: { ...devices["Pixel 7"] } },
  ],
  webServer: [server(TEST_PACK_URL, "../content/test-pack"), server(DEMO_URL)],
});
