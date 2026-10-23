// Connection loss, keyboard-only play, themes, and the client staying in
// step with the engine when it refuses an answer.
import { test, expect, open, status } from "./fixtures.js";

const story = (page) => page.locator(".story");

test("reconnects after the connection drops and starts a new session", async ({ page }) => {
  const sockets = [];
  await page.routeWebSocket(/\/ws\?world=\d+$/, (ws) => {
    ws.connectToServer();
    sockets.push(ws);
  });
  await open(page);
  // Change the session: ui.items replaces the items, and a dialog is open.
  await page.getByRole("button", { name: "painting" }).click();
  await expect(page.locator("dialog.picture")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("dialog.picture")).toBeHidden();
  await expect(page.getByRole("navigation").getByRole("button")).toHaveText(["lamp", "seen painting"]);
  await page.getByRole("button", { name: "lamp" }).click();
  await expect(page.getByRole("group", { name: "Light the lamp?" })).toBeVisible();

  // The retry timer runs on the fake clock, so the test does not wait.
  await page.clock.install();
  await sockets[0].close();
  await expect(status(page)).toHaveText("offline · retrying in 1 s");
  await expect(page.getByRole("group", { name: "Light the lamp?" })).toBeHidden();
  const input = page.getByRole("textbox");
  await expect(input).toHaveAttribute("placeholder", "waiting for the server…");
  // Offline, a typed line stays in the box instead of getting lost.
  await input.fill("hello?");
  await input.press("Enter");
  await expect(input).toHaveValue("hello?");
  await expect(story(page).locator(".you")).not.toContainText(["hello?"]);

  await page.clock.runFor(1000);
  await expect(status(page)).toContainText("connected · engine");
  expect(sockets).toHaveLength(2);
  // A new session: below a divider the story starts again, and the room's
  // own items are back; what came before stays above it.
  await expect(story(page).locator(".divider")).toHaveCount(1);
  await expect(story(page).locator(".divider ~ .room-title")).toHaveText(["Test hall"]);
  await expect(story(page)).not.toContainText("Lamp answer");
  await expect(page.getByRole("navigation").getByRole("button")).toHaveCount(9);
  await input.press("Enter");
  await expect(story(page)).toContainText("You typed: hello?");
});

test("keyboard only: items, a dialog, multiple choice and the picture", async ({ page }) => {
  await open(page);
  // tabTo presses Tab until the button or box named name has focus.
  const tabTo = async (name) => {
    for (let i = 0; i < 30; i++) {
      const label = await page.evaluate(() => {
        const el = document.activeElement;
        return el ? (el.labels && el.labels[0] ? el.labels[0].textContent : el.textContent).trim() : "";
      });
      if (label === name) return;
      await page.keyboard.press("Tab");
    }
    throw new Error("Tab never reached " + name);
  };

  await tabTo("lamp");
  await page.keyboard.press("Enter");
  const lamp = page.getByRole("group", { name: "Light the lamp?" });
  await expect(lamp.getByRole("button", { name: "Yes" })).toBeFocused();
  await page.keyboard.press("Tab");
  await page.keyboard.press("Enter"); // "No"
  await expect(story(page)).toContainText("Lamp answer: 2 No");

  await tabTo("chest");
  await page.keyboard.press("Enter");
  const chest = page.getByRole("group", { name: "Take what?" });
  await expect(chest.getByLabel("rope")).toBeFocused();
  await page.keyboard.press("Space");
  await page.keyboard.press("Tab");
  await expect(chest.getByLabel("coin")).toBeFocused();
  await page.keyboard.press("Space");
  await expect(chest).toContainText("Pick 1 to 2 · 2 picked");
  await page.keyboard.press("Enter");
  await expect(story(page)).toContainText("Took: rope+coin (1,2)");

  await tabTo("painting");
  await page.keyboard.press("Enter");
  const picture = page.locator("dialog.picture");
  await expect(picture).toBeVisible();
  await expect(picture.getByRole("button", { name: "close" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(picture).toBeHidden();
  await expect(page.getByRole("textbox")).toBeFocused();
});

test("clicking an item while a question is open asks for the answer first", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "lamp" }).click();
  const lamp = page.getByRole("group", { name: "Light the lamp?" });
  await expect(lamp).toBeVisible(); // or the chest is clicked before the question is open
  await page.getByRole("button", { name: "chest" }).click({ force: true });
  await expect(page.locator(".hint")).toHaveText("Answer the question first.");
  await expect(lamp).toBeVisible();
  await expect(story(page).locator(".you")).toHaveText(["lamp"]); // not "chest"
});

test("light and dark theme follow the system", async ({ page }) => {
  const background = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  await page.emulateMedia({ colorScheme: "dark" });
  await open(page);
  const dark = await background();
  await page.emulateMedia({ colorScheme: "light" });
  const light = await background();
  expect(dark).not.toBe(light);
  expect(dark).toBe("rgb(13, 17, 23)");
});

// The client closes a question when it sends the answer. If the engine
// refuses the answer, the question must still be answerable: here the
// answer is a C1 control character (U+0085), which JavaScript's trim()
// keeps but the engine's cleanInput turns into a space and trims away.
test("an answer the engine refuses leaves the question answerable", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "riddle" }).click();
  await expect(page.getByRole("group", { name: "What has keys but opens no locks?" })).toBeVisible();
  const input = page.getByRole("textbox");
  await input.fill("\u0085");
  await input.press("Enter");
  // Whatever the client shows, the player can still answer the riddle.
  await input.fill("piano");
  await input.press("Enter");
  await expect(story(page)).toContainText("Riddle: true piano");
  await expect(story(page).locator(".error")).not.toContainText(["Answer the open question first."]);
});

// A script that fails right after its question was answered: the error has
// no question id, so the client must not show the question again (T-0004
// security re-review: it re-opened the question in a loop and the player
// was stuck until a reload).
test("a script failing after an answer does not reopen the question", async ({ page }) => {
  await open(page);
  await page.getByRole("button", { name: "trapdoor" }).click();
  await page.getByRole("button", { name: "Yes" }).click();
  await expect(story(page).locator(".error")).toHaveText("This room's script failed. Tell the game master.");
  await expect(page.getByRole("group", { name: "Open the trapdoor?" })).toBeHidden();
  await page.getByRole("button", { name: "lamp" }).click();
  await expect(page.getByRole("group", { name: "Light the lamp?" })).toBeVisible();
});
