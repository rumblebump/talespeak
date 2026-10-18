// Accounts: signing up and in with the form, a character that lasts across
// reloads, a second window taking over, and an invite-only world.
import { test, expect, open, signUp } from "./fixtures.js";
import { INVITE_URL } from "../playwright.config.js";

const story = (page) => page.locator(".story");

test("sign up, sign out and sign in again with the form", async ({ page }) => {
  // A refused password (400, 401) is logged by the browser.
  page.allowedErrors = [/status of 40[01]/];
  const name = "mira_" + Math.random().toString(36).slice(2, 8);
  await page.goto("/");
  const form = page.getByRole("form", { name: "Sign in" });
  await expect(form).toBeVisible();
  await expect(page.getByRole("status")).toHaveText("not signed in");
  await expect(page.getByRole("textbox", { name: "What you do" })).toBeHidden();

  await form.getByLabel("name").fill(name);
  await form.getByLabel("password").fill("short");
  await form.getByRole("button", { name: "new account" }).click();
  await expect(form.getByRole("alert")).toHaveText("Passwords are at least 8 characters long.");
  await form.getByLabel("password").fill("correct horse");
  await form.getByRole("button", { name: "new account" }).click();
  await expect(page.getByRole("status")).toContainText("connected · engine");
  await expect(story(page)).toContainText("Welcome, " + name + ".");
  await expect(page.locator(".who")).toHaveText(name);
  await expect(page.getByRole("button", { name: "invite" })).toBeHidden(); // players do not invite

  await page.getByRole("button", { name: "sign out" }).click();
  await expect(form).toBeVisible();
  await expect(story(page)).toBeHidden();
  await form.getByLabel("name").fill(name.toUpperCase());
  await form.getByLabel("password").fill("wrong password");
  await form.getByRole("button", { name: "sign in" }).click();
  await expect(form.getByRole("alert")).toHaveText("Wrong name or password.");
  await form.getByLabel("password").fill("correct horse");
  await form.getByRole("button", { name: "sign in" }).click();
  await expect(page.getByRole("status")).toContainText("connected · engine");
  await expect(story(page)).toContainText("Welcome, " + name + ".");
});

test("a reload continues in the room the player left", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "cellar door" }).click();
  await expect(story(page)).toContainText("Down to the cellar.");
  await page.reload();
  await expect(page.getByRole("status")).toContainText("connected · engine");
  await expect(story(page)).toContainText("Cellar");
  await expect(story(page).locator(".room-title")).toHaveCount(0); // not back in the hall
  await expect(page.getByRole("navigation").getByRole("button")).toHaveText(["stairs up"]);
});

test("a second window takes over, and the first can take it back", async ({ page, context }) => {
  await open(page);
  const second = await context.newPage();
  await second.goto("/");
  await expect(second.getByRole("status")).toContainText("connected · engine");
  await expect(page.locator(".account").getByText("You are playing in another window or on another device now.")).toBeVisible();
  await expect(page.getByRole("status")).toHaveText("playing elsewhere");

  await page.getByRole("button", { name: "play here" }).click();
  await expect(page.getByRole("status")).toContainText("connected · engine");
  await expect(second.getByRole("button", { name: "play here" })).toBeVisible();
  await second.close();
});

test.describe("an invite-only world", () => {
  test.beforeEach(async ({}, testInfo) => {
    test.skip(testInfo.project.name !== "chromium", "one invite server: desktop only");
  });

  test("a new account waits for an invite, then plays", async ({ page, browser }) => {
    const kid = await signUp(page, INVITE_URL + "/");
    await page.goto(INVITE_URL + "/");
    const join = page.getByRole("form", { name: "Join the world" });
    await expect(join).toContainText("This world is invite only.");
    await expect(page.getByRole("status")).toHaveText("waiting for an invite");

    // The teacher, an admin made with the command line, makes an invite.
    const teacher = await browser.newPage();
    try {
      const resp = await teacher.request.post(INVITE_URL + "/api/login", { data: { name: "teacher", password: "admin password" } });
      expect(resp.status()).toBe(200);
      await teacher.goto(INVITE_URL + "/");
      await expect(teacher.getByRole("status")).toContainText("connected · engine");
      await teacher.getByRole("button", { name: "invite" }).click();
      const line = story(teacher).locator(".notice", { hasText: "Invite code:" });
      await expect(line).toBeVisible();
      const code = (await line.textContent()).match(/[0-9A-Z]{5}-[0-9A-Z]{5}/)[0];

      page.allowedErrors = [/status of 400/];
      await join.getByLabel("invite code").fill("AAAAA-AAAAA");
      await join.getByRole("button", { name: "join" }).click();
      await expect(join.getByRole("alert")).toContainText("not valid");
      await join.getByLabel("invite code").fill(code.toLowerCase());
      await join.getByRole("button", { name: "join" }).click();
      await expect(page.getByRole("status")).toContainText("connected · engine");
      await expect(story(page)).toContainText("Welcome, " + kid + ".");
    } finally {
      await teacher.close();
    }
  });
});
