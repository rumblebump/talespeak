// Characters on rulesets (docs/rulesets.md): a world of a story on a
// ruleset is played by one of the account's profiles, picked or made on its
// card; a story that continues another is locked until that one is
// finished, then brings its flag along.
import { test, expect, card, signUp, status } from "./fixtures.js";
import { RULESET_URL } from "../playwright.config.js";

const story = (page) => page.locator(".story");
const uniqueTitle = () => "World " + Math.random().toString(36).slice(2, 8);

// allowFavicon lets the browser's request for /favicon.ico, which the
// server does not have, fail without failing the test; it comes at no
// fixed time, after the first visit to this server's origin.
function allowFavicon(page) {
  page.allowedErrors = [/status of 404/];
}

async function createWorld(page, title, pack) {
  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: pack + " (1.0.0)" });
  await form.getByLabel("name (optional)").fill(title);
  await form.getByLabel(/private: only/).check();
  await form.getByRole("button", { name: "create" }).click();
  await expect(card(page, title)).toBeVisible();
  return card(page, title);
}

test("a new character finishes Forest Start, then plays Dragon Isle", async ({ page }) => {
  allowFavicon(page);
  await signUp(page, RULESET_URL);
  await page.goto(RULESET_URL + "/");
  await expect(status(page)).toHaveText("pick a world");
  // Rulesets are not offered as worlds.
  const packs = page.getByRole("form", { name: "New world" }).getByLabel("pack");
  await expect(packs.locator("option")).toHaveText(["The damp cell (1.1.0)", "Dragon Isle (1.0.0)", "Forest Start (1.0.0)", "Sky Tower (1.0.0)"]);

  const dragonTitle = uniqueTitle();
  const dragon = await createWorld(page, dragonTitle, "Dragon Isle");
  await expect(dragon).toContainText("Ruleset: Tiny Rules 1.0.0 · Finish Forest Start first.");
  await dragon.getByRole("button", { name: "play" }).click();
  const dragonPick = dragon.getByRole("group", { name: "who plays" });
  await expect(dragonPick).toBeVisible();
  await expect(dragonPick.getByLabel("a new character")).toBeHidden();
  await expect(dragonPick.getByRole("button", { name: "play" })).toBeDisabled();
  await dragonPick.getByRole("button", { name: "cancel" }).click();

  const forestTitle = uniqueTitle();
  const forest = await createWorld(page, forestTitle, "Forest Start");
  await expect(forest).toContainText("Ruleset: Tiny Rules 1.0.0");
  await forest.getByRole("button", { name: "play" }).click();
  const pick = forest.getByRole("group", { name: "who plays" });
  await expect(pick.getByLabel("a new character")).toBeChecked();
  await expect(pick.getByRole("button", { name: "play" })).toBeDisabled();
  await pick.getByLabel("name").fill("Fox Friend");
  await pick.getByRole("button", { name: "play" }).click();
  await expect(status(page)).toContainText("connected · engine");
  await expect(story(page)).toContainText("Forest Start 1.0.0 on tiny-rules 1.0.0. Level 1.");
  await page.getByRole("navigation", { name: "Things you can click" }).getByRole("button", { name: "help the fox" }).click();
  await expect(story(page)).toContainText("Level 2. Forest Start is finished.");

  await page.getByRole("button", { name: "worlds" }).click();
  await expect(status(page)).toHaveText("pick a world");
  await expect(card(page, forestTitle)).toContainText("your character: Fox Friend");
  const after = card(page, dragonTitle);
  await expect(after).not.toContainText("Finish Forest Start first.");
  await after.getByRole("button", { name: "play" }).click();
  const again = after.getByRole("group", { name: "who plays" });
  await expect(again.getByLabel("Fox Friend (1.0.0)")).toBeChecked();
  await again.getByRole("button", { name: "play" }).click();
  await expect(status(page)).toContainText("connected · engine");
  await expect(story(page)).toContainText("The fox you rescued runs ahead of you.");
  await expect(story(page)).toContainText("Level 2.");

  // Back again: the bound character plays without a picker.
  await page.getByRole("button", { name: "worlds" }).click();
  await card(page, dragonTitle).getByRole("button", { name: "play" }).click();
  await expect(status(page)).toContainText("connected · engine");
});

test("a Tiny Rules 1 character moves up to play Sky Tower", async ({ page }) => {
  allowFavicon(page);
  await signUp(page, RULESET_URL);
  await page.goto(RULESET_URL + "/");
  const forest = await createWorld(page, uniqueTitle(), "Forest Start");
  await forest.getByRole("button", { name: "play" }).click();
  const pick = forest.getByRole("group", { name: "who plays" });
  await pick.getByLabel("name").fill("Climber");
  await pick.getByRole("button", { name: "play" }).click();
  await expect(story(page)).toContainText("Level 1.");
  await page.getByRole("button", { name: "worlds" }).click();

  const sky = await createWorld(page, uniqueTitle(), "Sky Tower");
  await expect(sky).toContainText("Ruleset: Tiny Rules 2.0.0");
  await sky.getByRole("button", { name: "play" }).click();
  const skyPick = sky.getByRole("group", { name: "who plays" });
  await expect(skyPick).toContainText("Climber (1.0.0) · moves up to 2.0.0 and cannot go back to older worlds");
  await skyPick.getByLabel(/Climber \(1\.0\.0\)/).check();
  await skyPick.getByRole("button", { name: "play" }).click();
  await expect(status(page)).toContainText("connected · engine");
  await expect(story(page)).toContainText("Level 1, courage 1.");
});
