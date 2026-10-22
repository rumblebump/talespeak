// The lobby: a card per world, creating a world from a pack, its join code,
// and ending it.
import { test, expect, card, play, signUp } from "./fixtures.js";

const story = (page) => page.locator(".story");
const uniqueTitle = () => "World " + Math.random().toString(36).slice(2, 8);

// createWorld fills the new-world form and returns the join code shown on
// the new world's card.
async function createWorld(page, title, pack, visibility) {
  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: pack });
  await form.getByLabel("name (optional)").fill(title);
  await form.getByLabel(visibility).check();
  await form.getByRole("button", { name: "create" }).click();
  const code = card(page, title).locator(".card-code");
  await expect(code).toContainText("Join code:");
  return (await code.textContent()).match(/[0-9A-Z]{5}-[0-9A-Z]{5}/)[0];
}

test("a private world: its creator gets a code, a friend joins with it", async ({ page, browser }) => {
  await signUp(page);
  await page.goto("/");
  await expect(page.getByRole("status")).toHaveText("pick a world");
  await expect(card(page)).toContainText("main world");
  await expect(card(page)).toContainText("public");

  const title = uniqueTitle();
  const code = await createWorld(page, title, "Test pack", /private: only/);
  const mine = card(page, title);
  await expect(mine).toContainText("Test pack");
  await expect(mine).toContainText("Every renderer once");
  await expect(mine).toContainText("you are its game master");
  await expect(mine).toContainText("for up to 30 players");
  await expect(mine.getByRole("button", { name: "end world" })).toBeVisible();

  const friendContext = await browser.newContext();
  const friend = await friendContext.newPage();
  try {
    await signUp(friend);
    await friend.goto("/");
    await expect(card(friend)).toBeVisible();
    await expect(card(friend, title)).toHaveCount(0); // private: not listed
    const join = friend.getByRole("form", { name: "Join with a code" });
    await join.getByLabel("join code").fill(code.replaceAll("-", " ").toLowerCase());
    await join.getByRole("button", { name: "join" }).click();
    await expect(friend.getByRole("status")).toContainText("connected · engine");
    await expect(story(friend).locator(".room-title")).toHaveText("Test hall"); // the test pack's start room
    expect(friend.url()).toMatch(/#world=\d+$/);

    await play(page, title);
    await expect(story(page).locator(".room-title")).toHaveText("Test hall");

    // Back in the lobby, the friend's card shows the world with both.
    await friend.getByRole("button", { name: "worlds" }).click();
    await expect(friend.getByRole("status")).toHaveText("pick a world");
    const theirs = card(friend, title);
    await expect(theirs).toContainText("you are a member");
    // The server may not have seen the friend's connection close yet.
    await expect(theirs).toContainText(/2 players · [12] online now/);
    await expect(theirs.getByRole("button", { name: "end world" })).toBeHidden();
    await expect(theirs.getByRole("button", { name: "join code" })).toBeHidden();
  } finally {
    await friendContext.close();
  }
});

test("a public world is listed for everyone, and its game master can end it", async ({ page, browser }) => {
  await signUp(page);
  await page.goto("/");
  const title = uniqueTitle();
  await createWorld(page, title, "The damp cell", /public: listed/);

  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  try {
    await signUp(other);
    await other.goto("/");
    await expect(card(other, title)).toContainText("public");
    await play(other, title);
    await expect(story(other).locator(".room-title")).toHaveText("A damp cell");

    page.once("dialog", (d) => d.accept());
    await card(page, title).getByRole("button", { name: "end world" }).click();
    await expect(card(page, title)).toHaveCount(0);

    // The player in it is sent back to the lobby.
    await expect(story(other)).toContainText("This world has ended.");
    await expect(other.getByRole("status")).toHaveText("pick a world");
    await expect(other.getByRole("region", { name: "Worlds" }).getByRole("alert")).toHaveText("That world has ended, or you may no longer play it.");
    await expect(card(other, title)).toHaveCount(0);
  } finally {
    await otherContext.close();
  }
});

test("a reload stays in the world, the lobby button leaves it", async ({ page }) => {
  await signUp(page);
  await page.goto("/");
  const title = uniqueTitle();
  await createWorld(page, title, "Test pack", /private: only/);
  await play(page, title);
  const url = page.url();
  await page.reload();
  await expect(page.getByRole("status")).toContainText("connected · engine");
  await expect(story(page).locator(".room-title")).toHaveText("Test hall");
  expect(page.url()).toBe(url);

  await page.getByRole("button", { name: "worlds" }).click();
  await expect(page.getByRole("status")).toHaveText("pick a world");
  expect(page.url()).not.toContain("#world=");
  await page.reload();
  await expect(page.getByRole("status")).toHaveText("pick a world"); // no world in the address: the lobby
});
