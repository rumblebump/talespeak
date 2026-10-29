// World settings (T-0018): the New world form offers the pack's settings,
// the world keeps the pick, and its game master changes it with /set.
import { test, expect, card, play, signUp, status } from "./fixtures.js";

test("a world's death setting: picked in the lobby, changed with /set", async ({ page }) => {
  await signUp(page);
  await page.goto("/");
  await expect(status(page)).toHaveText("pick a world");

  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: "Test pack (1.0.0)" });
  await expect(form.getByLabel("When the guard catches a player")).toHaveCount(0); // the test pack has no settings
  await form.getByLabel("pack").selectOption({ label: "The damp cell (1.3.0)" });
  const death = form.getByLabel("When the guard catches a player");
  await expect(death).toHaveValue("respawn"); // the pack's default
  await death.selectOption("stay");
  const title = "Deadly " + Math.random().toString(36).slice(2, 8);
  await form.getByLabel("name (optional)").fill(title);
  await form.getByRole("button", { name: "create" }).click();
  await expect(card(page, title).locator(".card-code")).toContainText("Join code:");

  await play(page, title);
  const story = page.locator(".story");
  const input = page.getByRole("textbox");
  await input.fill("open the door");
  await input.press("Enter");
  await page.getByRole("button", { name: "Yes, quickly" }).click();
  await expect(story).toContainText("In this world, the dead stay dead.");

  // The creator is the world's game master: /set shows and changes it.
  await input.fill("/set");
  await input.press("Enter");
  await expect(story).toContainText("death (When the guard catches a player): stay; choices: respawn, revive, stay");
  await input.fill("/set death respawn");
  await input.press("Enter");
  await expect(story).toContainText('set "When the guard catches a player" to respawn.');
  await input.fill("search the straw");
  await input.press("Enter");
  await expect(story).toContainText("You wake up on the straw of your cell.");
});
