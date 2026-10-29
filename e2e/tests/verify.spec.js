// A mailed link that confirms an email address waits for a click, so a
// mail scanner that opens it confirms nothing.
import { test, expect } from "./fixtures.js";

test("a verify link asks before it confirms the address", async ({ page }) => {
  page.allowedErrors = [/status of 403/]; // this server sends no mail
  let asked = 0;
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/api/verify") asked++;
  });
  await page.goto("/#verify=ABCDE-FGHJK-MNPQR-STVWX");
  const confirm = page.getByRole("form", { name: "Confirm email" });
  await expect(confirm).toBeVisible();
  expect(new URL(page.url()).hash).toBe("");
  expect(asked).toBe(0);
  await confirm.getByRole("button", { name: "confirm the address" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "sends no mail" })).toBeVisible();
  expect(asked).toBe(1);
});
