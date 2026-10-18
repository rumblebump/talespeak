// Read aloud, with a fake speechSynthesis: headless browsers have no voices,
// and the test checks which voice is used.
import { test, expect, open } from "./fixtures.js";

function fakeSpeech(voices) {
  window.__spoken = [];
  const synth = {
    getVoices: () => voices,
    speak: (u) => window.__spoken.push({ text: u.text, voice: u.voice && u.voice.name }),
    cancel: () => window.__spoken.push({ text: "(cancel)" }),
    addEventListener: () => {},
  };
  Object.defineProperty(window, "speechSynthesis", { value: synth, configurable: true });
  window.SpeechSynthesisUtterance = class {
    constructor(text) {
      this.text = text;
    }
  };
}

test("reads new text aloud with a local voice", async ({ page }) => {
  await page.addInitScript(fakeSpeech, [
    { name: "Cloud", lang: "en-US", localService: false },
    { name: "Local", lang: "en-US", localService: true },
  ]);
  await open(page);
  const toggle = page.getByRole("button", { name: /read aloud/ });
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await toggle.click();
  await page.getByRole("button", { name: "lamp" }).click();
  await expect.poll(() => page.evaluate(() => window.__spoken)).toEqual([
    { text: "Light the lamp?. 1: Yes. 2: No. 3: Maybe later", voice: "Local" },
  ]);
  await page.keyboard.press("1");
  await expect.poll(() => page.evaluate(() => window.__spoken.length)).toBe(2);
  expect(await page.evaluate(() => window.__spoken[1])).toEqual({ text: "Lamp answer: 1 Yes", voice: "Local" });
  await toggle.click(); // off stops speaking
  expect(await page.evaluate(() => window.__spoken[2])).toEqual({ text: "(cancel)" });
});

test("no read aloud without a local voice", async ({ page }) => {
  await page.addInitScript(fakeSpeech, [{ name: "Cloud", lang: "en-US", localService: false }]);
  await open(page);
  await expect(page.getByRole("button", { name: /large text/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /read aloud/ })).toBeHidden();
});

test("spoken-only lines and a strict spelling question", async ({ page }) => {
  await page.addInitScript(fakeSpeech, [{ name: "Local", lang: "en-GB", localService: true }]);
  await open(page);
  // Read aloud stays off: the pack asks for these lines to be spoken.
  await page.getByRole("button", { name: "voice" }).click();
  const story = page.locator(".story");
  await expect(story.locator(".spoken")).toHaveText(["(listen)listen again"]);
  await expect(story).toContainText("Heard and read.");
  await expect(story).not.toContainText("Only heard, never read.");
  await expect.poll(() => page.evaluate(() => window.__spoken.map((s) => s.text))).toEqual(["Only heard, never read.", "Heard and read."]);
  await story.locator(".spoken").getByRole("button", { name: "listen again" }).click();
  await expect.poll(() => page.evaluate(() => window.__spoken.length)).toBe(3);

  await page.getByRole("button", { name: "spelling" }).click();
  const question = page.getByRole("group", { name: "Listen, then answer." });
  await expect(question).toBeVisible();
  await expect(question).not.toContainText("Write the word you hear.");
  await expect(page.getByRole("navigation")).toBeHidden(); // strict: no labels to copy from
  const input = page.getByRole("textbox");
  await expect(input).toHaveAttribute("autocorrect", "off");
  await question.getByRole("button", { name: "listen again" }).click();
  await expect.poll(() => page.evaluate(() => window.__spoken.at(-1).text)).toBe("Write the word you hear.");
  await input.fill("Castle");
  await input.press("Enter");
  await expect(story).toContainText("Spelling: true");
  await expect(page.getByRole("navigation")).toBeVisible();
});

test("spoken-only lines are shown when the device cannot speak", async ({ page }) => {
  await page.addInitScript(fakeSpeech, []);
  await open(page);
  await page.getByRole("button", { name: "voice" }).click();
  await expect(page.locator(".story")).toContainText("Only heard, never read.");
  await page.getByRole("button", { name: "spelling" }).click();
  await expect(page.getByRole("group", { name: "Write the word you hear." })).toBeVisible();
});
