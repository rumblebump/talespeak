// Two players in the demo's forest clearing, on a server of their own: the
// dice game in turns (lib/round.lua) and the horn only one player can have
// (lib/items.lua).
import { test, expect, open } from "./fixtures.js";
import { PARTY_URL } from "../playwright.config.js";

// One world for all: the tests run one at a time, on one project.
test.describe.configure({ mode: "serial" });
test.beforeEach(async ({}, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "one shared world: desktop only");
});

// escape plays the shortest way out of the castle into the clearing.
async function escape(page) {
  await open(page, PARTY_URL + "/");
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
  await page.getByRole("button", { name: "stone with an X" }).click();
  await page.getByRole("button", { name: "door marked GUARDS" }).click();
  await page.getByRole("button", { name: "iron box with number wheels" }).click();
  await expect(page.getByRole("group", { name: /Three number wheels/ })).toBeVisible();
  await input.fill("417");
  await input.press("Enter");
  await page.getByRole("button", { name: "narrow back door" }).click();
  await expect(story.locator(".room-title").last()).toHaveText("A forest clearing");
  return story;
}

test("party: two players take turns at the dice", async ({ page, browser }) => {
  const b = await browser.newPage();
  try {
    const storyA = await escape(page);
    const storyB = await escape(b);
    await expect(storyA).toContainText(/traveler( \d+)? comes out of the forest/);

    await page.getByRole("button", { name: "dice by the fire" }).click();
    await expect(storyA).toContainText("Your turn!");
    await expect(page.getByRole("list", { name: "Timers" })).toContainText(/'s turn \d/);
    await expect(storyB).toContainText("picks up the dice");
    await expect(b.getByRole("list", { name: "Timers" })).toContainText(/'s turn \d/);
    await expect(b.getByRole("button", { name: "end your turn" })).toBeHidden();

    // Not B's turn: the dice wait.
    await b.getByRole("button", { name: "dice by the fire" }).click();
    await expect(storyB).toContainText(/Wait for your turn: traveler( \d+)? is acting\./);

    await page.getByRole("button", { name: "dice by the fire" }).click();
    await expect(storyA).toContainText("You roll ");
    await page.getByRole("button", { name: "end your turn" }).click();
    await expect(storyA).toContainText("You end your turn.");
    await expect(page.getByRole("button", { name: "end your turn" })).toBeHidden();

    await expect(storyB).toContainText(/traveler( \d+)? rolls /);
    await expect(storyB).toContainText("Your turn!");
    await b.getByRole("button", { name: "dice by the fire" }).click();
    await expect(storyB).toContainText("You roll ");
    await b.getByRole("button", { name: "end your turn" }).click();
    await expect(storyB).toContainText(/wins the dice with|tie with/);
    await expect(storyA).toContainText(/wins the dice with|tie with/);
  } finally {
    await b.close();
  }
});

test("party: only one player gets the horn, and it comes back", async ({ page, browser }) => {
  const b = await browser.newPage();
  try {
    await escape(b);
    const storyA = await escape(page);
    const storyB = b.locator(".story");

    await page.getByRole("button", { name: "old hunting horn" }).click();
    await expect(storyA).toContainText("You take the old hunting horn.");
    await expect(b.getByRole("button", { name: "old hunting horn" })).toBeHidden();
    const input = b.getByRole("textbox");
    await input.fill("take the horn");
    await input.press("Enter");
    await expect(storyB).toContainText(/took the old horn already\./);

    // A leaves the world: the horn lies by the fire again.
    await page.close();
    await expect(storyB).toContainText("The old horn lies by the fire again.");
    await expect(b.getByRole("button", { name: "old hunting horn" })).toBeVisible();
  } finally {
    await b.close();
  }
});
