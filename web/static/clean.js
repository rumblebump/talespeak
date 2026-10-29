// Helpers of the client that touch no component state: the limits that
// match the engine, cleaning what the server sends, settings kept in this
// browser, the page's address and clock text.

// Limits that match the engine (docs/websocket-protocol.md).
export const MAX_LINES = 500; // older story lines are dropped
export const MAX_TEXT = 4000; // characters of one story line, title or notice
export const MAX_INPUT = 500; // bytes the engine accepts in one typed line
export const MAX_HISTORY = 50; // typed lines kept for arrow up and down
export const MAX_TIMERS = 20; // shown timers: 10 of the player and 10 of the room
export const MAX_MS = 24 * 60 * 60 * 1000; // the longest timer or question timeout
export const NEAR_BOTTOM = 48; // pixels from the end of the story that still count as at the end

/** Reads a setting saved in this browser; private windows may refuse. */
export function loadSetting(key) {
  try {
    return localStorage.getItem("talespeak." + key) === "on";
  } catch {
    return false;
  }
}

export function saveSetting(key, on) {
  try {
    localStorage.setItem("talespeak." + key, on ? "on" : "off");
  } catch {
    // not saved; the setting still applies until the page is closed
  }
}

/** worldFromHash reads the world a page shows from its address
 * (#world=12), so a reload stays in it; 0 for none. */
export function worldFromHash() {
  const m = /^#world=([1-9][0-9]{0,14})$/.exec(location.hash);
  return m ? Number(m[1]) : 0;
}

export function setHash(world) {
  const url = location.pathname + location.search + (world ? "#world=" + world : "");
  try {
    history.replaceState(null, "", url);
  } catch {
    // a sandboxed page may refuse; a reload then opens the lobby
  }
}

/** cleanCard keeps what the client shows of a world card. */
export function cleanCard(c) {
  return {
    id: Number.isSafeInteger(c.id) && c.id > 0 ? c.id : 0,
    title: text(c.title).slice(0, 80),
    packTitle: text(c.packTitle).slice(0, 120),
    version: text(c.version).slice(0, 64),
    unavailable: text(c.unavailable).slice(0, 200),
    description: text(c.description).slice(0, 300),
    visibility: c.visibility === "public" ? "public" : "private",
    gm: text(c.gm).slice(0, 20),
    players: Number.isSafeInteger(c.players) ? c.players : 0,
    online: Number.isSafeInteger(c.online) ? c.online : 0,
    role: c.role === "gm" || c.role === "player" ? c.role : "",
    main: c.main === true,
    created: text(c.created),
    play: c.play === true,
    invite: c.invite === true,
    finish: c.finish === true,
  };
}

/** text turns any server value into a string for display. */
export const text = (v) => (v === undefined || v === null ? "" : String(v));

/** cleanLine cleans a typed line as the engine does: control characters
 * (newlines included) become spaces, then it is trimmed. */
export const cleanLine = (s) => s.replace(/[\p{Cc}\u2028\u2029]/gu, " ").trim();

/** speakerName is who says a line, such as the guide: one short line of
 * text, or "" for the story itself. */
export const speakerName = (v) =>
  cleanLine(text(v))
    .replace(/[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g, "")
    .slice(0, 40);

/** eventID is a room event's id as the engine makes them ("lq3k9x.1f"),
 * or "". */
export const eventID = (v) => {
  const s = text(v);
  return /^[0-9a-z]{1,16}\.[0-9a-z]{1,16}$/.test(s) ? s : "";
};

/** A picture URL must be a pack image on this server. */
export function imageURL(v) {
  const s = text(v);
  return /^\/packs\/[a-z0-9][a-z0-9_-]{0,63}\/(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]{1,50})?\/assets\/[a-z0-9][a-z0-9_-]{0,63}\.(png|jpe?g|gif|webp)$/.test(s) ? s : "";
}

export function cleanItems(v) {
  if (!Array.isArray(v)) return [];
  return v
    .filter((it) => it && typeof it === "object" && typeof it.id === "string")
    .slice(0, 20)
    .map((it) => ({ id: it.id.slice(0, 64), label: text(it.label).slice(0, 100) }));
}

/** msLeft reads a server duration in milliseconds: 0 when it is missing,
 * not a number or out of range. */
export function msLeft(v) {
  return Number.isInteger(v) && v > 0 && v <= MAX_MS ? v : 0;
}

/** clockText shows a time left as m:ss, or h:mm:ss from an hour on. */
export function clockText(ms) {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const pad = (n) => String(n).padStart(2, "0");
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return (h > 0 ? h + ":" + pad(m) : String(m)) + ":" + pad(s % 60);
}
