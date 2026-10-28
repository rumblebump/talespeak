// A world's members in the lobby, playing as GM or as a player, and
// switching GM mode in-game (T-0011).
import { test, expect, card, signUp, status } from "./fixtures.js";

const story = (page) => page.locator(".story");

async function type(page, line) {
  const input = page.getByRole("textbox");
  await input.fill(line);
  await input.press("Enter");
}

test("a game master makes a member GM in the lobby, and plays as a player until /gm", async ({ page, browser }) => {
  const owner = await signUp(page);
  await page.goto("/");
  const title = "Members " + Math.random().toString(36).slice(2, 8);
  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: "Test pack (1.0.0)" });
  await form.getByLabel("name (optional)").fill(title);
  await form.getByLabel(/private: only/).check();
  await form.getByRole("button", { name: "create" }).click();
  const world = card(page, title);
  const codeLine = world.locator(".card-code");
  await expect(codeLine).toContainText("Join code:");
  const code = (await codeLine.textContent()).match(/[0-9A-Z]{5}-[0-9A-Z]{5}/)[0];

  const friendContext = await browser.newContext();
  const friend = await friendContext.newPage();
  try {
    const name = await signUp(friend);
    await friend.goto("/");
    const join = friend.getByRole("form", { name: "Join with a code" });
    await join.getByLabel("join code").fill(code);
    await join.getByRole("button", { name: "join" }).click();
    await expect(status(friend)).toContainText("connected · engine");
    // A player plays; there is no GM choice and no members list.
    await friend.getByRole("button", { name: "worlds" }).click();
    const theirs = card(friend, title);
    await expect(theirs.getByRole("button", { name: "play", exact: true })).toBeVisible();
    await expect(theirs.getByRole("button", { name: "play as GM" })).toHaveCount(0);
    await expect(theirs.getByRole("button", { name: "members" })).toHaveCount(0);

    // The game master lists the members and makes the friend a GM.
    await page.reload();
    await world.getByRole("button", { name: "members" }).click();
    const list = world.locator(".members");
    await expect(list.getByRole("listitem")).toHaveCount(2);
    const row = list.getByRole("listitem").filter({ hasText: name });
    await expect(row).toContainText("player");
    await expect(list.getByRole("listitem").filter({ hasText: owner }).getByRole("button")).toHaveCount(0);
    await row.getByRole("button", { name: "make GM" }).click();
    await expect(row).toContainText("game master");
    // GMs change no other GM; only admins add accounts by name.
    await expect(row.getByRole("button")).toHaveCount(0);
    await expect(list.getByRole("form", { name: "Add a member" })).toBeHidden();

    // As a GM now, the friend plays as a player first: no GM tools until
    // /gm on.
    await friend.reload();
    await theirs.getByRole("button", { name: "play as player" }).click();
    await expect(status(friend)).toContainText("connected · engine");
    await type(friend, "/say hi");
    await expect(story(friend).locator(".server").last()).toContainText("There is no command /say here.");
    await type(friend, "/gm on");
    await expect(story(friend).locator(".server").last()).toContainText("GM mode is on");
    await type(friend, "/say hi");
    await expect(story(friend).locator("p.gm").last()).toContainText("hi");
  } finally {
    await friendContext.close();
  }
});
