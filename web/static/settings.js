// The settings of a new world (T-0018): the choices a pack offers each
// world, such as what happens when a player dies, picked in the lobby's
// New world form. A part of the game component; app.js merges it in.

import { text } from "/clean.js";

// Limits that match the engine (internal/pack/settings.go).
const MAX_SETTINGS = 20;
const MAX_CHOICES = 20;
const WORD = /^[a-z0-9][a-z0-9_-]{0,31}$/;

/** cleanSettings keeps the well-formed settings of a pack from the server. */
export function cleanSettings(v) {
  if (!Array.isArray(v)) return [];
  const out = [];
  for (const s of v.slice(0, MAX_SETTINGS)) {
    if (!s || typeof s !== "object" || typeof s.name !== "string" || !/^[a-z][a-z0-9_]{0,31}$/.test(s.name)) continue;
    const choices = Array.isArray(s.choices) ? s.choices.filter((c) => typeof c === "string" && WORD.test(c)).slice(0, MAX_CHOICES) : [];
    if (choices.length < 2) continue;
    const fallback = choices.includes(s.default) ? s.default : choices[0];
    out.push({ name: s.name, label: text(s.label).slice(0, 100) || s.name, choices, default: fallback });
  }
  return out;
}

/** settings returns the methods and getters of this part. */
export function settings() {
  return {
    // newSettings are the settings of the pack picked for a new world.
    get newSettings() {
      return this.newPack ? this.newPack.settings : [];
    },
    get hasNewSettings() {
      return this.newSettings.length > 0;
    },
    // settingPicked reports whether choice is what the form has for s.
    settingPicked(s, choice) {
      const v = this.newWorld.settings[s.name];
      return (s.choices.includes(v) ? v : s.default) === choice;
    },
    // pickSetting keeps the choice of a setting's select.
    pickSetting(s, event) {
      const v = event && event.target ? event.target.value : "";
      if (s.choices.includes(v)) this.newWorld.settings = { ...this.newWorld.settings, [s.name]: v };
    },
    // pickedSettings are the form's picks for the picked pack, for POST
    // /api/worlds: only its own settings, each one of its choices.
    pickedSettings() {
      const out = {};
      for (const s of this.newSettings) {
        const v = this.newWorld.settings[s.name];
        out[s.name] = s.choices.includes(v) ? v : s.default;
      }
      return out;
    },
  };
}
