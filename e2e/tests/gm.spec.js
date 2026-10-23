// Slash commands, the game master's tools, the chat-like layout and the
// story kept in the browser (T-0011).
import { test, expect, card, open, play, signUp, status } from "./fixtures.js";

const story = (page) => page.locator(".story");
const uniqueTitle = () => "World " + Math.random().toString(36).slice(2, 8);

// type sends a line from the input box.
async function type(page, line) {
  const input = page.getByRole("textbox");
  await input.fill(line);
  await input.press("Enter");
}

test("/help for a player lists what everyone may type, and no game master tools", async ({ page }) => {
  await open(page);
  // A first visit says where to start, once.
  await expect(story(page).locator(".server")).toHaveText(["Type /help to see what you can type."]);
  await expect(page.getByRole("textbox")).toHaveAttribute("placeholder", "");
  await type(page, "/help");
  const help = story(page).locator(".server").last();
  await expect(help).toContainText("/help: what you can type here");
  await expect(help).not.toContainText("/say");
  await expect(help).not.toContainText("/reset");
  // A game master's command is unknown to a player, as one that does not exist.
  await type(page, "/reset room");
  await expect(story(page).locator(".server").last()).toHaveText("There is no command /reset here. Type /help to see what you can type.");
  // A command never reaches the room's on_input.
  await expect(story(page)).not.toContainText("You typed: /");
});

test("/help works while a question is open", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "lamp" }).click();
  const question = page.getByRole("group", { name: "Light the lamp?" });
  await expect(question).toBeVisible();
  await type(page, "/help");
  await expect(story(page).locator(".server").last()).toContainText("/help: what you can type here");
  await expect(question).toBeVisible();
  await question.getByRole("button", { name: "Yes" }).click();
  await expect(story(page)).toContainText("Lamp answer: 1 Yes");
});

test("a game master's words have a look of their own, and /reset room starts the room over", async ({ page, browser }) => {
  await signUp(page);
  await page.goto("/");
  const title = uniqueTitle();
  const form = page.getByRole("form", { name: "New world" });
  await form.getByLabel("pack").selectOption({ label: "Test pack (1.0.0)" });
  await form.getByLabel("name (optional)").fill(title);
  await form.getByLabel(/private: only/).check();
  await form.getByRole("button", { name: "create" }).click();
  const codeLine = card(page, title).locator(".card-code");
  await expect(codeLine).toContainText("Join code:");
  const code = (await codeLine.textContent()).match(/[0-9A-Z]{5}-[0-9A-Z]{5}/)[0];
  await play(page, title);

  const friendContext = await browser.newContext();
  const friend = await friendContext.newPage();
  try {
    await friend.addInitScript(() => {
      // A fake local voice, to hear how a game master's line is read.
      window.__spoken = [];
      const synth = {
        getVoices: () => [{ name: "Local", lang: "en-US", localService: true }],
        speak: (u) => window.__spoken.push(u.text),
        cancel: () => {},
        addEventListener: () => {},
      };
      Object.defineProperty(window, "speechSynthesis", { value: synth, configurable: true });
      window.SpeechSynthesisUtterance = class {
        constructor(text) {
          this.text = text;
        }
      };
    });
    await signUp(friend);
    await friend.goto("/");
    const join = friend.getByRole("form", { name: "Join with a code" });
    await join.getByLabel("join code").fill(code);
    await join.getByRole("button", { name: "join" }).click();
    await expect(status(friend)).toContainText("connected · engine");

    await type(page, "/help");
    const help = story(page).locator(".server").last();
    await expect(help).toContainText("/say TEXT");
    await expect(help).toContainText("/reset room [ROOM]");

    // /say reaches everyone, marked as the game master's, and read aloud
    // as theirs.
    await friend.getByRole("button", { name: /read aloud/ }).click();
    await type(page, "/say Hello, everyone!");
    const said = story(friend).locator("p.gm");
    await expect(said).toHaveText(/Hello, everyone!/);
    await expect(said.locator(".from")).toHaveCount(1);
    await expect.poll(() => friend.evaluate(() => window.__spoken)).toContainEqual(expect.stringMatching(/^Game master .+ says: Hello, everyone!$/));
    await friend.getByRole("button", { name: /read aloud/ }).click();
    await expect(story(page).locator("p.gm").last()).toContainText("Hello, everyone!");

    // A pack's guide line never gets that look.
    await type(friend, "guide");
    await expect(story(friend).locator(".guide").first()).toContainText("I am your guide.");
    await expect(story(friend).locator("p.gm")).toHaveCount(1);
    await friend.getByRole("group").getByRole("button", { name: "Yes" }).click();

    // The friend answers a question; the reset closes the next one and
    // starts the room over for both, with a mark from the game master.
    await friend.getByRole("button", { name: "lamp" }).click();
    await expect(friend.getByRole("group", { name: "Light the lamp?" })).toBeVisible();
    await type(page, "/reset room");
    await expect(friend.getByRole("group", { name: "Light the lamp?" })).toBeHidden();
    await expect(story(friend).locator("p.gm").last()).toContainText("started this room over.");
    await expect(story(friend).locator(".room-title").last()).toHaveText("Test hall");
    await expect(story(page).locator("p.gm").last()).toContainText("started this room over.");

    // The audit log has both, the latest first.
    await type(page, "/audit");
    const audit = story(page).locator(".server").last();
    await expect(audit).toContainText("reset room \"hall\"");
    await expect(audit).toContainText("say \"Hello, everyone!\"");
  } finally {
    await friendContext.close();
  }
});

test("the story stays at its end, and a reload shows what happened", async ({ page }) => {
  await open(page);
  for (let i = 1; i <= 15; i++) await type(page, "line " + i); // within the message rate limit
  await expect(story(page)).toContainText("You typed: line 15");
  const atEnd = () => story(page).evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight < 50);
  await expect.poll(atEnd).toBe(true);
  // The input bar stays on screen at the bottom.
  const box = await page.locator("form.prompt").boundingBox();
  const vp = page.viewportSize();
  expect(box.y + box.height).toBeLessThanOrEqual(vp.height);
  expect(box.y + box.height).toBeGreaterThan(vp.height - 80);

  // Reading further up, new lines wait below a button.
  await story(page).evaluate((el) => {
    el.scrollTop = 0;
  });
  await expect.poll(atEnd).toBe(false);
  await type(page, "one more");
  await expect.poll(atEnd).toBe(true); // the player typed: they want to see the answer

  await page.reload();
  await expect(status(page)).toContainText("connected · engine");
  await expect(story(page).locator(".earlier").filter({ hasText: "You typed: line 15" })).toHaveCount(1);
  await expect(story(page).locator(".divider")).toHaveCount(1);
  await expect(story(page).locator(".server:not(.earlier)")).toHaveCount(0); // the first-visit line is not repeated
  await expect.poll(atEnd).toBe(true);

  // Signing out forgets it.
  await page.getByRole("button", { name: "sign out" }).click();
  await expect(page.getByRole("form", { name: "Sign in" })).toBeVisible();
  const kept = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith("talespeak.log.")));
  expect(kept).toEqual([]);
});

test("new lines below: a button jumps to them", async ({ page }) => {
  await open(page);
  for (let i = 1; i <= 15; i++) await type(page, "line " + i); // within the message rate limit
  await expect(story(page)).toContainText("You typed: line 15");
  // A question with a timeout: what follows it comes without the player
  // typing, while they read further up.
  await type(page, "hourglass");
  await expect(page.getByRole("group", { name: "Quick: left or right?" })).toBeVisible();
  await story(page).evaluate((el) => {
    el.scrollTop = 0;
    el.dispatchEvent(new Event("scroll"));
  });
  await expect(story(page)).toContainText("Too slow.");
  const button = page.getByRole("button", { name: "new lines below" });
  await expect(button).toBeVisible();
  expect(await story(page).evaluate((el) => el.scrollTop)).toBe(0); // the page did not jump away from what the player reads
  await button.click();
  await expect(button).toBeHidden();
  await expect.poll(() => story(page).evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight < 50)).toBe(true);
});
