// Browser tests against the real server binary (docs/testing.md). Build it
// first: CGO_ENABLED=0 go build -o bin/talespeak ./cmd/talespeak
import { defineConfig, devices } from "@playwright/test";

export const TEST_PACK_URL = "http://127.0.0.1:18765";
export const DEMO_URL = "http://127.0.0.1:18766";
// The demo again, as a world of its own for the tests where players meet.
export const PARTY_URL = "http://127.0.0.1:18767";
// The test pack as an invite-only world, with an admin account "teacher".
export const INVITE_URL = "http://127.0.0.1:18768";

// Each server starts from a fresh data directory under e2e/.data/, emptied
// by the server's own start command (this file is also loaded by every
// test worker, so it must not touch the disk itself).
function server(url, name, pack, extra = {}, before = "") {
  const data = ".data/" + name;
  const env = {
    TALESPEAK_LISTEN: url.replace("http://", ""),
    TALESPEAK_DATA: data,
    // Every test opens connections from 127.0.0.1; the default burst of 60 is for real clients.
    TALESPEAK_CONNECT_BURST: "100000",
    // Every test registers its own account, from the same address.
    TALESPEAK_SIGNIN_BURST: "100000",
  };
  if (pack) env.TALESPEAK_PACK = pack;
  Object.assign(env, extra);
  return {
    command: `rm -rf ${data} && ${before}../bin/talespeak serve`,
    url: url + "/healthz",
    env,
    reuseExistingServer: false,
    timeout: 20000,
  };
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
  webServer: [
    server(TEST_PACK_URL, "test-pack", "../content/test-pack"),
    server(DEMO_URL, "demo"),
    server(PARTY_URL, "party"),
    server(
      INVITE_URL,
      "invite",
      "../content/test-pack",
      { TALESPEAK_WORLD_ACCESS: "invite" },
      "echo 'admin password' | ../bin/talespeak account add -admin teacher && ",
    ),
  ],
});
