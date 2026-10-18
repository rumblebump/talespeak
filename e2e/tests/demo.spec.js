// The shipped demo pack, as a new player sees it.
import { test, expect, open } from "./fixtures.js";
import { DEMO_URL } from "../playwright.config.js";

test("demo: guard, straw and the wall map", async ({ page }) => {
  await open(page, DEMO_URL + "/");
  const story = page.locator(".story");
  await expect(story.locator(".room-title")).toHaveText("A damp cell");
  await page.getByRole("button", { name: "sleepy guard" }).click();
  await page.getByRole("button", { name: "Ask for water" }).click();
  await expect(story).toContainText("slides a cup of water");

  const input = page.getByRole("textbox");
  await input.fill("search the straw");
  await input.press("Enter");
  await page.getByLabel("A bent nail").check();
  await page.getByRole("button", { name: "ok" }).click();
  await expect(story).toContainText("The nail might open the lock.");
  await expect(page.getByRole("button", { name: "flattened straw" })).toBeVisible();

  await page.getByRole("button", { name: "scratches on the wall" }).click();
  await expect(page.locator("dialog.picture img")).toHaveAttribute("src", /^\/packs\/demo\/assets\/wall-map-(clear|faint)\.png$/);
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "lock with letter wheels" }).click();
  await expect(page.getByRole("navigation")).toBeHidden();
  await input.fill("A piano.");
  await input.press("Enter");
  // The question stays in the story after the box closes, then the answer.
  await expect(story.locator(".asked").last()).toHaveText("I have bars but no drinks, and you are standing in me. What am I?");
  await expect(story.locator(".you").last()).toHaveText("A piano.");
  await expect(story).toContainText("the answer is one word of four letters.");
  await page.getByRole("button", { name: "lock with letter wheels" }).click();
  await input.fill("door");
  await input.press("Enter");
  await expect(story).toContainText("You turn the wheels to DOOR. Nothing happens.");
  await expect(story).toContainText("Hint: look around you.");
  await input.fill("open the lock");
  await input.press("Enter");
  await input.fill("CELL");
  await input.press("Enter");
  await expect(story).toContainText("The lock opens!");
});
