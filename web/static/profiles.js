// Characters on rulesets (docs/rulesets.md): a world whose story runs on a
// ruleset is played by one of the account's profiles. Its card says which
// one, or lets the player pick one or make a new one before playing. A part
// of the game component; app.js merges it in.

import { cleanLine, text } from "/clean.js";
import { api } from "/api.js";

const MAX_OPTIONS = 20; // store.MaxProfiles
const MAX_NAME = 20; // profile.MaxName

/** cleanOption keeps what the client shows of one profile. */
function cleanOption(o) {
  if (!o || typeof o !== "object" || !Number.isSafeInteger(o.id) || o.id <= 0) return null;
  return {
    id: o.id,
    name: text(o.name).slice(0, MAX_NAME),
    version: text(o.version).slice(0, 64),
    ok: o.ok === true,
    upgrade: text(o.upgrade).slice(0, 64),
    reason: text(o.reason).slice(0, 300),
  };
}

/** cleanProfiles keeps what the client shows of a card's profiles; null
 * for a world that needs none. */
export function cleanProfiles(p) {
  if (!p || typeof p !== "object") return null;
  return {
    ruleset: text(p.ruleset).slice(0, 200),
    bound: cleanOption(p.bound),
    options: Array.isArray(p.options) ? p.options.slice(0, MAX_OPTIONS).map(cleanOption).filter(Boolean) : [],
    new: p.new === true,
    locked: text(p.locked).slice(0, 300),
  };
}

/** profiles returns the methods, getters and state of this part. */
export function profiles() {
  return {
    picking: 0, // the world whose card shows the profile picker
    pick: { profile: 0, name: "" }, // the picked profile, 0 for a new one named name

    hasProfiles(card) {
      return !!card.profiles;
    },
    // needsPick reports whether playing card's world first needs a
    // profile picked: none is bound, or the bound one must be checked
    // again or moved to a new ruleset version.
    needsPick(card) {
      const p = card.profiles;
      return !!p && (!p.bound || !p.bound.ok || !!p.bound.upgrade);
    },
    isPicking(card) {
      return this.picking === card.id;
    },
    // pickOptions are the profiles the picker lists: the bound one, or
    // the account's profiles on the ruleset.
    pickOptions(card) {
      const p = card.profiles;
      if (!p) return [];
      return p.bound ? [p.bound] : p.options;
    },
    canMakeNew(card) {
      return !!card.profiles && !card.profiles.bound && card.profiles.new;
    },
    makingNew(card) {
      return this.canMakeNew(card) && this.pick.profile === 0;
    },
    canPick(card) {
      if (this.busy || !card.profiles) return false;
      if (this.makingNew(card)) return cleanLine(this.pick.name) !== "";
      return this.pickOptions(card).some((o) => o.id === this.pick.profile && o.ok);
    },
    pickGroup(card) {
      return "profile-" + card.id;
    },
    // openPick shows the picker on card, with the first profile that fits
    // picked, else a new one.
    openPick(card) {
      const ok = this.pickOptions(card).find((o) => o.ok);
      this.pick = { profile: ok ? ok.id : 0, name: "" };
      this.picking = card.id;
      this.formError = "";
    },
    cancelPick() {
      this.picking = 0;
    },
    // pickProfile binds the world's character to the picked profile, then
    // plays the world.
    async pickProfile(card) {
      if (!this.canPick(card)) return;
      this.busy = true;
      this.formError = "";
      try {
        await api("/api/profiles", { world: card.id, profile: this.pick.profile, name: cleanLine(this.pick.name) });
        this.picking = 0;
        await this.loadLobby();
        const now = this.worlds.find((c) => c.id === card.id);
        if (now && now.play && !this.needsPick(now)) this.enter(now);
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
    },
    // profileLine says on the card which ruleset the world runs, and who
    // plays it or why the account cannot.
    profileLine(card) {
      const p = card.profiles;
      if (!p) return "";
      const parts = ["Ruleset: " + p.ruleset];
      if (p.bound) parts.push("your character: " + p.bound.name);
      if (p.bound && !p.bound.ok) parts.push(p.bound.reason);
      else if (!p.bound && p.locked) parts.push(p.locked);
      return parts.join(" · ");
    },
    // optionText names a profile in the picker, with what playing this
    // world does to it.
    optionText(o) {
      let s = o.name + " (" + o.version + ")";
      if (o.upgrade) s += " · moves up to " + o.upgrade + " and cannot go back to older worlds";
      if (!o.ok) s += " · " + o.reason;
      return s;
    },
  };
}
