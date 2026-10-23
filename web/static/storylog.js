// The story log in the browser (T-0011). Plain ES module, no build step.

const MAX_KEPT = 50; // story lines kept per world

// Story lines the browser keeps, by kind: what the player read and typed.
// A line of any other kind is not kept, and a kept line of another kind
// is not shown again.
const KEPT_KINDS = new Set(["", "room-title", "you", "asked", "error", "picture-note", "spoken", "notice", "gm", "server", "guide", "asked guide", "divider"]);

/**
 * The story log in the browser: the last MAX_KEPT lines of each world, so a
 * reload shows what happened. In edu mode it lives only as long as the tab
 * (sessionStorage), so a shared school computer keeps nothing once it is
 * closed; elsewhere until the player signs out. The server keeps no story
 * log at all.
 */
const LOG_PREFIX = "talespeak.log.";

function logStorage(edu) {
  try {
    return edu ? sessionStorage : localStorage;
  } catch {
    return null; // storage refused: nothing is kept
  }
}

function logKey(account, world) {
  return LOG_PREFIX + account.toLowerCase() + "." + world;
}

/** loadLog reads the kept lines of a world: { text, kind, from }, the
 * oldest first. from is still to be cleaned as the server's. */
export function loadLog(edu, account, world) {
  const st = logStorage(edu);
  if (!st || !account || !world) return [];
  try {
    const list = JSON.parse(st.getItem(logKey(account, world)) || "[]");
    if (!Array.isArray(list)) return [];
    return list
      .filter((l) => l && typeof l === "object" && typeof l.t === "string" && KEPT_KINDS.has(l.k))
      .slice(-MAX_KEPT)
      .map((l) => ({ text: l.t.slice(0, 4000), kind: l.k, from: typeof l.f === "string" ? l.f : "" }));
  } catch {
    return [];
  }
}

export function saveLog(edu, account, world, lines) {
  const st = logStorage(edu);
  if (!st || !account || !world) return;
  const kept = lines
    .filter((l) => KEPT_KINDS.has(l.kind))
    .slice(-MAX_KEPT)
    .map((l) => ({ t: l.text, k: l.kind, f: l.from || undefined }));
  try {
    st.setItem(logKey(account, world), JSON.stringify(kept));
  } catch {
    // full or refused: the story still shows until the page closes
  }
}

/** forgetLogs removes every kept story from this browser (signing out). */
export function forgetLogs() {
  for (const st of [logStorage(false), logStorage(true)]) {
    if (!st) continue;
    try {
      for (let i = st.length - 1; i >= 0; i--) {
        const k = st.key(i);
        if (k && k.startsWith(LOG_PREFIX)) st.removeItem(k);
      }
    } catch {
      // refused: nothing was kept either
    }
  }
}
