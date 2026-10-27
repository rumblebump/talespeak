// Passwords: changing one's own, an admin's reset code used on the
// sign-in page, and a reset link that opens the form with its code.
import { test, expect, signUp, status } from "./fixtures.js";
import { INVITE_URL } from "../playwright.config.js";

test("change the password in the lobby, then sign in with it", async ({ page }) => {
  page.allowedErrors = [/status of 401/];
  const name = await signUp(page);
  await page.goto("/");
  await expect(status(page)).toHaveText("pick a world");
  await page.getByText("your account").click();
  const form = page.getByRole("form", { name: "Change password" });
  await form.getByLabel("current password").fill("wrong password");
  await form.getByLabel("new password").fill("a new password");
  await form.getByRole("button", { name: "change password" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "current password is not right" })).toBeVisible();
  await form.getByLabel("current password").fill("correct horse");
  await form.getByRole("button", { name: "change password" }).click();
  await expect(page.locator(".pw-notice")).toHaveText("Your password is changed. Your other devices are signed out.");

  await page.getByRole("button", { name: "sign out" }).click();
  const signIn = page.getByRole("form", { name: "Sign in" });
  await signIn.getByLabel("name").fill(name);
  await signIn.getByLabel("password").fill("correct horse");
  await signIn.getByRole("button", { name: "sign in" }).click();
  await expect(signIn.getByRole("alert")).toHaveText("Wrong name or password.");
  await signIn.getByLabel("password").fill("a new password");
  await signIn.getByRole("button", { name: "sign in" }).click();
  await expect(status(page)).toHaveText("pick a world");
});

test("an admin's reset code sets a new password on the sign-in page", async ({ page, browser }) => {
  // The player: an account on the invite-only server, signed out in this page.
  const player = await browser.newContext();
  const name = await signUp(await player.newPage(), INVITE_URL);
  await player.close();

  // The admin makes a reset code in the lobby.
  const admin = await browser.newContext();
  const adminPage = await admin.newPage();
  const login = await adminPage.request.post(INVITE_URL + "/api/login", { data: { name: "teacher", password: "admin password" } });
  expect(login.status()).toBe(200);
  await adminPage.goto(INVITE_URL);
  await adminPage.getByText("your account").click();
  const make = adminPage.getByRole("form", { name: "Reset code" });
  await make.getByLabel("account name").fill(name);
  await make.getByRole("button", { name: "make a reset code" }).click();
  const made = make.locator(".pw-made");
  await expect(made).toContainText("Reset code for " + name + ": ");
  const code = /: ([0-9A-Z]{5}-[0-9A-Z]{5}) /.exec(await made.textContent())[1];
  await admin.close();

  // The player types it on the sign-in page.
  await page.goto(INVITE_URL);
  await page.getByRole("button", { name: "I have a reset code" }).click();
  const reset = page.getByRole("form", { name: "Reset password" });
  await reset.getByLabel("reset code").fill(code.toLowerCase());
  await reset.getByLabel("new password").fill("a new password");
  await reset.getByRole("button", { name: "set the new password" }).click();
  await expect(status(page)).toHaveText("pick a world");
  await expect(page.locator(".who")).toHaveText(name);
  await expect(page.locator(".pw-notice")).toHaveText("Your password is changed, and you are signed in.");
  await expect(reset).toBeHidden();
});

test("a reset link opens the form with its code and leaves the address", async ({ page }) => {
  page.allowedErrors = [/status of 400/];
  await page.goto("/#reset=ABCDE-FGHJK-MNPQR-STVWX");
  const reset = page.getByRole("form", { name: "Reset password" });
  await expect(reset).toBeVisible();
  await expect(reset.getByLabel("reset code")).toHaveValue("ABCDE-FGHJK-MNPQR-STVWX");
  expect(new URL(page.url()).hash).toBe("");
  await reset.getByLabel("new password").fill("a new password");
  await reset.getByRole("button", { name: "set the new password" }).click();
  await expect(page.getByRole("alert").filter({ hasText: "not valid, used or expired" })).toBeVisible();
  // Without mail, "forgot password" says whom to ask.
  await page.getByRole("button", { name: "forgot password?" }).click();
  await expect(page.getByRole("form", { name: "Forgot password" })).toContainText("This server sends no mail.");
});
