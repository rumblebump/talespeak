// The game component is app.js merged with its parts (lobby.js, play.js,
// dialog.js, view.js): every part's methods and getters reach the page,
// getters stay live, and the client cuts what the server sends to the
// lengths the engine allows. And the story log in edu mode stays out of
// localStorage.
import { test, expect, open, status } from "./fixtures.js";

const story = (page) => page.locator(".story");

// The Alpine data of the game component, inside page.evaluate (the
// page's CSP has no unsafe-eval, so no helper builds functions there).

test("every part's methods and getters are on the component", async ({ page }) => {
  await open(page);
  const kinds = await page.evaluate(() => {
    const data = document.body._x_dataStack[0];
    const out = {};
    for (const k of ["loadMe", "enter", "receive", "addLine", "send", "openDialog", "closeDialog", "submit", "onKey", "startGame", "click", "showPicture", "toggleLargeText", "applyLargeText", "init"]) {
      out[k] = typeof data[k];
    }
    for (const k of ["isEdu", "pictureClass", "readAloudLabel", "largeTextLabel", "placeholder", "autocorrect"]) {
      let o = data;
      let d;
      while (o && !(d = Object.getOwnPropertyDescriptor(o, k))) o = Object.getPrototypeOf(o);
      out[k] = d && typeof d.get === "function" ? "getter" : "value";
    }
    return out;
  });
  for (const [k, kind] of Object.entries(kinds)) {
    expect(kind, k).toBe(["isEdu", "pictureClass", "readAloudLabel", "largeTextLabel", "placeholder", "autocorrect"].includes(k) ? "getter" : "function");
  }
  // Getters follow the state they read, and the page shows it.
  const labels = await page.evaluate(() => {
    const d = document.body._x_dataStack[0];
    const before = d.largeTextLabel;
    d.largeText = !d.largeText;
    const after = d.largeTextLabel;
    d.largeText = !d.largeText;
    return [before, after, d.placeholder];
  });
  expect(labels).toEqual(["large text: off", "large text: on", ""]);
  await page.getByRole("button", { name: "large text: off" }).click();
  await expect(page.getByRole("button", { name: "large text: on" })).toBeVisible();
});

test("the client cuts long server text to the engine's limits", async ({ page }) => {
  await open(page);
  const got = await page.evaluate(async () => {
    const d = document.body._x_dataStack[0];
    const { cleanItems, MAX_TEXT } = await import("/clean.js");
    d.addLine("x".repeat(5000), "server");
    const line = d.lines[d.lines.length - 1].text.length;
    const items = cleanItems([{ id: "i".repeat(100), label: "l".repeat(300) }]);
    d.openDialog({ type: "dialog", id: "d-long", title: "t".repeat(300), options: ["o".repeat(300), "short"] });
    const dialog = { title: d.dialog.title.length, options: d.dialog.options.map((o) => o.length) };
    d.closeDialog();
    return { MAX_TEXT, line, id: items[0].id.length, label: items[0].label.length, dialog };
  });
  expect(got).toEqual({ MAX_TEXT: 4000, line: 4000, id: 64, label: 100, dialog: { title: 200, options: [200, 5] } });
  await expect(story(page).locator(".server").last()).toHaveText("x".repeat(4000));

  await page.getByRole("button", { name: "painting" }).click();
  const picture = page.locator("dialog.picture");
  await expect(picture).toBeVisible();
  await page.evaluate(() => {
    const d = document.body._x_dataStack[0];
    d.showPicture({ url: d.picture.url, text: "c".repeat(300) });
  });
  await expect(picture.locator("figcaption")).toHaveText("c".repeat(200));
});

// /api/me says edu: the story log is kept for the tab only (sessionStorage),
// never in localStorage, and still comes back after a reload.
test("edu mode keeps the story log out of localStorage", async ({ page }) => {
  await page.route("**/api/me", async (route) => {
    const resp = await route.fetch();
    const me = await resp.json();
    await route.fulfill({ response: resp, json: { ...me, mode: "edu" } });
  });
  await open(page);
  expect(await page.evaluate(() => document.body._x_dataStack[0].isEdu)).toBe(true);
  const input = page.getByRole("textbox");
  await input.fill("kept for the tab");
  await input.press("Enter");
  await expect(story(page)).toContainText("You typed: kept for the tab");
  const keys = () =>
    page.evaluate(() => ({
      local: Object.keys(localStorage).filter((k) => k.startsWith("talespeak.log.")),
      session: Object.keys(sessionStorage).filter((k) => k.startsWith("talespeak.log.")),
    }));
  await expect.poll(async () => (await keys()).session.length).toBe(1);
  expect((await keys()).local).toEqual([]);

  await page.reload();
  await expect(status(page)).toContainText("connected · engine");
  await expect(story(page).locator(".earlier").filter({ hasText: "You typed: kept for the tab" })).toHaveCount(1);
  expect((await keys()).local).toEqual([]);

  // Signing out forgets it here too.
  await page.getByRole("button", { name: "sign out" }).click();
  await expect(page.getByRole("form", { name: "Sign in" })).toBeVisible();
  expect(await keys()).toEqual({ local: [], session: [] });
});
