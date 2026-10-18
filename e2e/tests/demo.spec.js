// The shipped demo pack, as a new player sees it.
import { test, expect, open } from "./fixtures.js";
import { DEMO_URL } from "../playwright.config.js";

test("demo: guard, straw, wall map, riddle, corridor and escape", async ({ page }) => {
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

  // While the guard watches, the door is a bad idea.
  await page.getByRole("button", { name: "lock with letter wheels" }).click();
  await page.getByRole("button", { name: "Better not" }).click();
  await expect(story).toContainText("You step back from the door.");

  // Two more cups empty his bottle, and he goes to refill it.
  await page.getByRole("button", { name: "sleepy guard" }).click();
  await page.getByRole("button", { name: "Ask for water" }).click();
  await expect(story).toContainText("\"Again?\"");
  await page.getByRole("button", { name: "sleepy guard" }).click();
  await page.getByRole("button", { name: "Ask for water" }).click();
  await expect(story).toContainText("stomps off down the corridor");
  await expect(page.getByRole("button", { name: "empty stool" })).toBeVisible();

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
  // Wait for the question, or CELL is typed as a command.
  await expect(page.getByRole("group", { name: /I have bars/ })).toBeVisible();
  await input.fill("CELL");
  await input.press("Enter");
  await expect(story).toContainText("The lock opens!");
  await expect(page.getByRole("group", { name: /I have bars/ })).toBeHidden();
  await page.getByRole("button", { name: "open cell door" }).click();

  // The corridor: the X from the wall map hides the code.
  await expect(story.locator(".room-title").last()).toHaveText("A dark corridor");
  await page.getByRole("button", { name: "stone with an X" }).click();
  await expect(story).toContainText("BOX 4 1 7");
  // The cell is solved: Whisp asks before the player goes back in.
  await page.getByRole("button", { name: "your cell" }).click();
  await expect(page.getByRole("group", { name: /locked in again/ })).toBeVisible();
  await page.getByRole("button", { name: "No, stay here" }).click();
  await expect(story.locator("p.guide", { hasText: "Good. Onwards!" }).locator(".from")).toHaveText("Whisp");
  await page.getByRole("button", { name: "door marked GUARDS" }).click();

  // The guard room: the code opens the box, its key the back door.
  await expect(story.locator(".room-title").last()).toHaveText("The guard room");
  await page.getByRole("button", { name: "iron box with number wheels" }).click();
  await expect(page.getByRole("group", { name: /Three number wheels/ })).toBeVisible();
  await input.fill("4 1 7");
  await input.press("Enter");
  await expect(story).toContainText("big iron key");
  // With the key, the corridor is solved and its door is gone.
  await expect(page.getByRole("button", { name: "door to the corridor" })).toBeHidden();
  await page.getByRole("button", { name: "narrow back door" }).click();
  await expect(story).toContainText("You escaped. Well done!");
  await expect(story).toContainText("THE END.");
  await expect(page.getByRole("navigation")).toBeHidden();
});

test("demo: trying the door while the guard watches", async ({ page }) => {
  await open(page, DEMO_URL + "/");
  const story = page.locator(".story");
  const input = page.getByRole("textbox");
  await input.fill("open the door");
  await input.press("Enter");
  await page.getByRole("button", { name: "Yes, quickly" }).click();
  await expect(story).toContainText("SLAP!");
  await expect(story).toContainText("After a few days, you die.");
  await input.fill("search the straw");
  await input.press("Enter");
  await expect(story).toContainText("The story has ended.");
});

test("demo: caught on the stairs after leaving the cell", async ({ page }) => {
  await open(page, DEMO_URL + "/");
  const story = page.locator(".story");
  const input = page.getByRole("textbox");
  for (let i = 0; i < 3; i++) {
    await page.getByRole("button", { name: "sleepy guard" }).click();
    await page.getByRole("button", { name: "Ask for water" }).click();
  }
  await expect(story).toContainText("stomps off down the corridor");
  await page.getByRole("button", { name: "lock with letter wheels" }).click();
  await expect(page.getByRole("group", { name: /I have bars/ })).toBeVisible();
  await input.fill("cell");
  await input.press("Enter");
  await page.getByRole("button", { name: "open cell door" }).click();
  await expect(story.locator(".room-title").last()).toHaveText("A dark corridor");
  await page.getByRole("button", { name: "stairs down" }).click();
  await page.getByRole("button", { name: "Yes, go down" }).click();
  await expect(story).toContainText("straight into the guard");
  await expect(story).toContainText("THE END.");
  await expect(page.getByRole("navigation")).toBeHidden();
});
