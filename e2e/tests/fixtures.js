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
        // Tests may open the local servers (test pack, demo, party), nothing else.
        if (o !== origin && !/^http:\/\/127\.0\.0\.1:1876[567]$/.test(o)) problems.push("request to another origin: " + url);
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

// open loads the client and waits until the engine has welcomed it.
export async function open(page, url = "/") {
  await page.goto(url);
  await expect(page.getByRole("status")).toContainText("connected · engine");
}
