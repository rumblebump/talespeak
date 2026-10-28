// A game master pauses the world: the client says so and its countdowns
// stand still until the game goes on (T-0012).
import { test, expect, play, signUp } from "./fixtures.js";

const story = (page) => page.locator(".story");

async function type(page, line) {
  const input = page.getByRole("textbox");
  await input.fill(line);
  await input.press("Enter");
}

test("/pause stops the countdowns, /resume lets them run", async ({ page }) => {
  await signUp(page);
  await page.goto("/");
  const title = "World " + Math.random().toString(36).slice(2, 8);
  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: "Test pack (1.0.0)" });
  await form.getByLabel("name (optional)").fill(title);
  await form.getByLabel(/private: only/).check();
  await form.getByRole("button", { name: "create" }).click();
  await play(page, title);

  // Countdowns run on the browser's clock: a fake one, so nothing waits.
  await page.clock.install();
  await type(page, "candle");
  const timers = page.getByRole("list", { name: "Timers" });
  await expect(timers).toHaveText(/Candle (10:00|9:5\d)/);

  await type(page, "/pause");
  const banner = page.locator(".paused");
  await expect(banner).toHaveText("The game is paused: countdowns stop until the game master goes on.");
  await expect(story(page).locator("p.gm").last()).toContainText("paused the game");
  const frozen = await timers.textContent();
  await page.clock.runFor(5000);
  await expect(timers).toHaveText(frozen);

  await type(page, "/timers");
  await expect(story(page).locator(".server").last()).toContainText('"Candle" (candle)');

  await type(page, "/resume");
  await expect(banner).toBeHidden();
  await expect(story(page).locator("p.gm").last()).toContainText("resumed the game.");
  await expect(timers).toHaveText(/Candle (10:00|9:5\d)/);
  await page.clock.runFor(5000);
  await expect(timers).toHaveText(/Candle 9:5[0-5]/);
});
