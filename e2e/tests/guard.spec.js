// Tests of the offline and CSP guard itself (fixtures.js): each one makes
// the page do what the guard must catch, checks the guard saw it, then
// clears the guard's list so the test itself passes. Nothing leaves the
// machine: other origins are answered by page.route or blocked by the CSP.
import { test, expect, open } from "./fixtures.js";
import { DEMO_URL } from "../playwright.config.js";

const elsewhere = "http://127.0.0.1:9";

// caught waits until the guard has a problem matching re, then clears the list.
async function caught(guard, re) {
  await expect.poll(() => guard.join("\n"), { message: "guard problems" }).toMatch(re);
  guard.length = 0;
}

test("guard: a CSP violation fails the test", async ({ page, guard }) => {
  await open(page);
  await page.evaluate((url) => {
    const img = document.createElement("img");
    img.src = url + "/blocked.png";
    document.body.append(img);
  }, elsewhere);
  await caught(guard, /CSP violation: img-src http:\/\/127\.0\.0\.1:9\/blocked\.png/);
});

test("guard: a request to another origin fails the test", async ({ page, guard }) => {
  // Navigation is not covered by the CSP, so only the request check sees it.
  await page.route(elsewhere + "/**", (route) => route.fulfill({ status: 200, contentType: "text/plain", body: "x" }));
  await open(page);
  await page.evaluate((url) => {
    location.href = url + "/away";
  }, elsewhere);
  await caught(guard, /request to another origin: http:\/\/127\.0\.0\.1:9\/away/);
});

test("guard: an uncaught exception fails the test", async ({ page, guard }) => {
  await open(page);
  await page.evaluate(() => {
    setTimeout(() => {
      throw new Error("guard test boom");
    });
  });
  await caught(guard, /uncaught: guard test boom/);
});

test("guard: a console error fails the test unless allowed", async ({ page, guard }) => {
  await open(page);
  page.allowedErrors = [/expected by this test/];
  await page.evaluate(() => console.error("expected by this test"));
  await page.evaluate(() => console.error("not expected"));
  await caught(guard, /console error: not expected/);
  expect(guard.join("\n")).not.toContain("expected by this test");
});

test("guard: data: pictures and the second local server are allowed", async ({ page, guard }) => {
  await open(page, DEMO_URL + "/");
  const loaded = await page.evaluate(
    () =>
      new Promise((resolve) => {
        const img = document.createElement("img");
        img.onload = () => resolve(true);
        img.onerror = () => resolve(false);
        img.src = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=";
        document.body.append(img);
      }),
  );
  expect(loaded).toBe(true);
  expect(guard).toEqual([]);
});
