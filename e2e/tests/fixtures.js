// The offline and CSP guard every browser test uses: a test fails if the
// page requests anything from another origin, if the browser reports a
// Content-Security-Policy violation, or on any console error or uncaught
// exception. Import { test, expect } from here, not from @playwright/test.
import { test as base, expect } from "@playwright/test";

export const test = base.extend({
  guard: [
    async ({ page }, use, testInfo) => {
      const problems = [];
      const origin = new URL(testInfo.project.use.baseURL).origin;
      page.on("request", (req) => {
        const url = req.url();
        if (url.startsWith("data:") || url.startsWith("blob:")) return;
        const o = new URL(url).origin;
        // Tests may open the local servers (test pack, demo, party, invite), nothing else.
        if (o !== origin && !/^http:\/\/127\.0\.0\.1:1876[5-8]$/.test(o)) problems.push("request to another origin: " + url);
      });
      page.on("console", (msg) => {
        if (msg.type() === "error" && !page.allowedErrors?.some((re) => re.test(msg.text()))) {
          problems.push("console error: " + msg.text());
        }
      });
      page.on("pageerror", (err) => problems.push("uncaught: " + err.message));
      await page.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (e) => {
          console.error("CSP violation: " + e.violatedDirective + " " + e.blockedURI);
        });
      });
      await use(problems);
      expect(problems, "offline and CSP guard").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

// signUp registers a new account on the server of url and signs the
// page's browser context in with it. Every test plays its own character,
// named traveler_ and a random suffix, since tests run side by side.
export async function signUp(page, url = "/") {
  const name = "traveler_" + Math.random().toString(36).slice(2, 10);
  const api = url.startsWith("http") ? new URL("/api/register", url).href : "/api/register";
  const resp = await page.request.post(api, { data: { name, password: "correct horse" } });
  expect(resp.status(), "register " + name).toBe(200);
  return name;
}

// card is a world's card in the lobby, by its title; main: the server's
// main world.
export function card(page, title) {
  return title === undefined ? page.locator('.card[data-main="true"]') : page.getByRole("article", { name: title, exact: true });
}

// status is the connection status in the header. The lobby's join codes
// are status regions too, and stay on the page for a moment after a play
// click: always ask for this one.
export function status(page) {
  return page.locator("header").getByRole("status");
}

// play picks a world in the lobby (the main world by default) and waits
// until the engine has welcomed the player.
export async function play(page, title) {
  await card(page, title).getByRole("button", { name: "play" }).click();
  await expect(status(page)).toContainText("connected · engine");
}

// open signs up, loads the client, picks the server's main world in the
// lobby and waits until the engine has welcomed it.
export async function open(page, url = "/") {
  await signUp(page, url);
  await page.goto(url);
  await play(page);
}
