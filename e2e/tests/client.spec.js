// One test per renderer of the web client, against content/test-pack.
import { test, expect, open } from "./fixtures.js";

const story = (page) => page.locator(".story");

test("room and text renderer", async ({ page }) => {
  await open(page);
  await expect(story(page).locator(".room-title")).toHaveText("Test hall");
  await expect(story(page)).toContainText("A plain room for tests.");
  await expect(story(page)).toContainText("Welcome, traveler.");
});

test("clickable items, dialog answered with the mouse", async ({ page }) => {
  await open(page);
  const items = page.getByRole("navigation", { name: "Things you can click" });
  await expect(items.getByRole("button")).toHaveText(["lamp", "chest", "painting", "broken lever", "riddle", "spelling", "voice"]);
  await items.getByRole("button", { name: "lamp" }).click();
  const dialog = page.getByRole("group", { name: "Light the lamp?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Yes" })).toBeFocused();
  await dialog.getByRole("button", { name: "Maybe later" }).click();
  await expect(story(page)).toContainText("Lamp answer: 3 Maybe later");
  await expect(dialog).toBeHidden();
});

test("dialog answered with the keyboard", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "lamp" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("group", { name: "Light the lamp?" })).toBeVisible();
  await page.keyboard.press("9"); // out of range: nothing happens
  await page.keyboard.press("2");
  await expect(story(page)).toContainText("Lamp answer: 2 No");
});

test("multiple choice with the mouse", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "chest" }).click();
  const dialog = page.getByRole("group", { name: "Take what?" });
  const ok = dialog.getByRole("button", { name: "ok" });
  await expect(dialog).toContainText("Pick 1 to 2 · 0 picked");
  await expect(ok).toBeDisabled();
  await dialog.getByLabel("map").check();
  await dialog.getByLabel("rope").check();
  await dialog.getByLabel("coin").click(); // a third is refused
  await expect(dialog.getByLabel("coin")).not.toBeChecked();
  await expect(page.locator(".hint")).toHaveText("You can pick at most 2.");
  await ok.click();
  await expect(story(page)).toContainText("Took: rope+map (1,3)");
});

test("multiple choice with the keyboard and by typing", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "chest" }).click();
  await expect(page.getByRole("group", { name: "Take what?" })).toBeVisible();
  await page.keyboard.press("4");
  await page.keyboard.press("2");
  await page.keyboard.press("2"); // untick
  await page.keyboard.press("Enter");
  await expect(story(page)).toContainText("Took: key (4)");

  await page.getByRole("button", { name: "chest" }).click();
  const input = page.getByRole("textbox");
  await input.fill("3, 1");
  await input.press("Enter");
  await expect(story(page)).toContainText("Took: rope+map (1,3)");
});

test("typed input, history and answering a dialog by typing", async ({ page }) => {
  await open(page);
  const input = page.getByRole("textbox");
  await input.fill("open the door");
  await input.press("Enter");
  await expect(story(page).locator(".you")).toHaveText(["open the door"]);
  await expect(story(page)).toContainText("You typed: open the door");
  await expect(input).toHaveValue("");

  await input.press("ArrowUp");
  await expect(input).toHaveValue("open the door");
  await input.press("ArrowDown");
  await expect(input).toHaveValue("");

  await page.getByRole("button", { name: "lamp" }).click();
  await input.fill("look around"); // not an answer
  await input.press("Enter");
  await expect(page.locator(".hint")).toContainText("Answer the question first");
  await input.fill("yes"); // a label works as well as a number
  await input.press("Enter");
  await expect(story(page)).toContainText("Lamp answer: 1 Yes");
});

test("untrusted text is shown as text", async ({ page }) => {
  await open(page);
  const input = page.getByRole("textbox");
  const html = '<img src="/x.png" onerror="window.pwned = 1"><b>bold</b>';
  await input.fill(html);
  await input.press("Enter");
  await expect(story(page)).toContainText("You typed: " + html);
  await expect(story(page).locator("img, b")).toHaveCount(0);
  expect(await page.evaluate(() => window.pwned)).toBeUndefined();
});

test("picture popup", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "painting" }).click();
  const picture = page.locator("dialog.picture");
  await expect(picture).toBeVisible();
  await expect(picture.locator("figcaption")).toHaveText("A test square");
  const img = picture.getByRole("img", { name: "A test square" });
  await expect(img).toHaveAttribute("src", "/packs/test-pack/assets/square.png");
  await expect.poll(() => img.evaluate((el) => el.naturalWidth)).toBe(16);
  await expect(img).toHaveClass("pixel");
  // ui.items after the picture replaced the items.
  await expect(page.getByRole("navigation").getByRole("button")).toHaveText(["lamp", "seen painting"]);
  await page.keyboard.press("Escape");
  await expect(picture).toBeHidden();
  await expect(page.getByRole("textbox")).toBeFocused();
  await expect(story(page)).toContainText("[picture] A test square");
});

test("script errors reach the player without details", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "broken lever" }).click();
  await expect(story(page).locator(".error")).toHaveText("This room's script failed. Tell the game master.");
  await expect(story(page)).not.toContainText("lever broke");
});

test("large text is remembered", async ({ page }) => {
  await open(page);
  const toggle = page.getByRole("button", { name: /large text/ });
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  const small = await page.evaluate(() => getComputedStyle(document.documentElement).fontSize);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-pressed", "true");
  const large = await page.evaluate(() => getComputedStyle(document.documentElement).fontSize);
  expect(parseFloat(large)).toBeGreaterThan(parseFloat(small));
  await open(page);
  await expect(page.getByRole("button", { name: /large text/ })).toHaveAttribute("aria-pressed", "true");
});

test("fits the screen without scrolling sideways", async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 });
  await open(page);
  await page.getByRole("button", { name: "chest" }).click();
  await expect(page.getByRole("group", { name: "Take what?" })).toBeVisible();
  const [scroll, width] = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
  expect(scroll).toBeLessThanOrEqual(width);
});

test("the page loads nothing from another origin", async ({ page }) => {
  const urls = [];
  page.on("request", (r) => urls.push(r.url()));
  await open(page);
  await page.getByRole("button", { name: "painting" }).click();
  await expect(page.locator("dialog.picture")).toBeVisible();
  const origin = new URL(page.url()).origin;
  expect(urls.length).toBeGreaterThan(0);
  for (const u of urls) expect(new URL(u).origin).toBe(origin);
  // The only external URL is the AGPL source link, which is followed, never loaded.
  const html = (await page.content()).replace('href="https://github.com/rumblebump/talespeak"', "");
  expect(html).not.toMatch(/(https?:)?\/\/[a-z0-9.-]+\.[a-z]{2,}/i);
});

test("typed answer to a riddle", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "riddle" }).click();
  const question = page.getByRole("group", { name: "What has keys but opens no locks?" });
  await expect(question).toContainText("Type your answer below.");
  const input = page.getByRole("textbox");
  await expect(input).toBeFocused();
  await expect(input).toHaveAttribute("placeholder", "type your answer, then Enter");
  await expect(input).toHaveAttribute("autocorrect", "on");
  await expect(page.getByRole("navigation")).toBeVisible(); // not strict: items stay
  await input.pressSequentially("2 Piano"); // digits are typed, not taken as a choice
  await expect(input).toHaveValue("2 Piano");
  await input.fill("  A   Piano ");
  await input.press("Enter");
  await expect(story(page)).toContainText("Riddle: true A Piano");
  await expect(question).toBeHidden();
});

