// The Talespeak client: the lobby of worlds, then the engine's messages (story text, spoken
// lines, rooms, clickable items, dialogs, multiple choice, typed questions,
// pictures) and sends back what the player clicks and types. Plain ES
// modules, no build step.
//
// Alpine's CSP build runs the page without 'unsafe-eval', so the HTML only
// holds simple expressions and all logic lives here. Server text is
// untrusted: it is only ever shown with x-text or textContent.

import Alpine from "/vendor/alpine-csp.js";
import { connect } from "/net.js";
import { createSpeaker } from "/speech.js";
import { forgetLogs, loadLog, saveLog } from "/storylog.js";

// Limits that match the engine (docs/websocket-protocol.md).
const MAX_LINES = 500; // older story lines are dropped
const MAX_INPUT = 500; // bytes the engine accepts in one typed line
const MAX_HISTORY = 50; // typed lines kept for arrow up and down
const MAX_TIMERS = 20; // shown timers: 10 of the player and 10 of the room
const MAX_MS = 24 * 60 * 60 * 1000; // the longest timer or question timeout
const NEAR_BOTTOM = 48; // pixels from the end of the story that still count as at the end

/**
 * api calls the account API: GET without a body, else a JSON POST. It
 * returns the answer, or throws an Error whose message the player can
 * read.
 * @param {string} path
 * @param {object} [body]
 */
async function api(path, body) {
  // Only this server's account API: a fixed path, never one from the server.
  const apiPath = /^\/api\/[a-z]+(\?page=[1-9][0-9]{0,3})?$/.test(path) ? path : "/api/invalid";
  const init = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  let resp;
  try {
    resp = await fetch(apiPath, { ...init, credentials: "same-origin", cache: "no-store" });
  } catch {
    throw new Error("The server cannot be reached. Try again in a moment.");
  }
  let data = null;
  try {
    data = await resp.json();
  } catch {
    // not JSON: a proxy's error page
  }
  if (!resp.ok || !data || typeof data !== "object") {
    throw new Error(data && typeof data.error === "string" ? data.error : "Something went wrong (" + resp.status + ").");
  }
  return data;
}

/** Reads a setting saved in this browser; private windows may refuse. */
function loadSetting(key) {
  try {
    return localStorage.getItem("talespeak." + key) === "on";
  } catch {
    return false;
  }
}

function saveSetting(key, on) {
  try {
    localStorage.setItem("talespeak." + key, on ? "on" : "off");
  } catch {
    // not saved; the setting still applies until the page is closed
  }
}

/** worldFromHash reads the world a page shows from its address
 * (#world=12), so a reload stays in it; 0 for none. */
function worldFromHash() {
  const m = /^#world=([1-9][0-9]{0,14})$/.exec(location.hash);
  return m ? Number(m[1]) : 0;
}

function setHash(world) {
  const url = location.pathname + location.search + (world ? "#world=" + world : "");
  try {
    history.replaceState(null, "", url);
  } catch {
    // a sandboxed page may refuse; a reload then opens the lobby
  }
}

/** cleanCard keeps what the client shows of a world card. */
function cleanCard(c) {
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
const text = (v) => (v === undefined || v === null ? "" : String(v));

/** cleanLine cleans a typed line as the engine does: control characters
 * (newlines included) become spaces, then it is trimmed. */
const cleanLine = (s) => s.replace(/[\p{Cc}\u2028\u2029]/gu, " ").trim();

/** speakerName is who says a line, such as the guide: one short line of
 * text, or "" for the story itself. */
const speakerName = (v) =>
  cleanLine(text(v))
    .replace(/[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g, "")
    .slice(0, 40);

/** A picture URL must be a pack image on this server. */
function imageURL(v) {
  const s = text(v);
  return /^\/packs\/[a-z0-9][a-z0-9_-]{0,63}\/(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]{1,50})?\/assets\/[a-z0-9][a-z0-9_-]{0,63}\.(png|jpe?g|gif|webp)$/.test(s) ? s : "";
}

function cleanItems(v) {
  if (!Array.isArray(v)) return [];
  return v
    .filter((it) => it && typeof it === "object" && typeof it.id === "string")
    .slice(0, 20)
    .map((it) => ({ id: it.id, label: text(it.label) }));
}

/** msLeft reads a server duration in milliseconds: 0 when it is missing,
 * not a number or out of range. */
function msLeft(v) {
  return Number.isInteger(v) && v > 0 && v <= MAX_MS ? v : 0;
}

/** clockText shows a time left as m:ss, or h:mm:ss from an hour on. */
function clockText(ms) {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const pad = (n) => String(n).padStart(2, "0");
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return (h > 0 ? h + ":" + pad(m) : String(m)) + ":" + pad(s % 60);
}

function game() {
  const speaker = createSpeaker();
  let conn = null;
  let lineNo = 0;
  let historyAt = -1;
  let scrollSeen = 0; // the story's scrollTop at its last scroll event
  let resizing = null; // the ResizeObserver of the story

  return {
    view: "loading", // signin, lobby, play or replaced
    me: null, // the answer of /api/me
    worlds: [], // the lobby's world cards
    page: 1, // the last lobby page loaded
    more: false, // the lobby has more worlds than it shows
    packs: [], // what the account may create worlds from
    world: 0, // the world played, or about to be
    codes: {}, // world id -> the last join code made here: { text, code, copied }
    newWorld: { pack: "", title: "", visibility: "public" },
    form: { name: "", password: "", code: "" },
    formError: "",
    busy: false,
    state: "connecting",
    statusText: "connecting…",
    lines: [],
    items: [],
    dialog: null, // { id, title, options, multiple, min, max, ask, strict, speak, hide, end }
    sent: null, // the question just answered, until the engine replies
    picked: [], // option numbers ticked in a multiple choice
    picture: { url: "", caption: "" },
    pixelArt: false,
    draft: "",
    history: [],
    hint: "",
    readAloud: false,
    largeText: false,
    canSpeak: false,
    timers: [], // shown countdowns: { id, title, end }, end in this browser's clock
    now: Date.now(),
    atEnd: true, // the story is scrolled to its end: new lines keep it there
    newBelow: false, // lines came in while the player read further up

    init() {
      // Countdowns count from the time left the server sent, on this
      // browser's clock, so a wrong clock on the device does not matter.
      setInterval(() => {
        this.now = Date.now();
      }, 1000);
      this.largeText = loadSetting("largeText");
      this.readAloud = loadSetting("readAloud");
      this.applyLargeText();
      this.canSpeak = speaker.available();
      speaker.onChange(() => {
        this.canSpeak = speaker.available();
      });
      this.loadMe();
    },

    // Accounts

    // loadMe asks the server who is signed in, and shows what fits.
    async loadMe() {
      try {
        this.show(await api("/api/me"));
      } catch (err) {
        this.setStatus("offline", "offline · reload the page to try again");
        this.formError = err.message;
        this.view = "signin";
      }
    },

    // show moves to the view for me, an answer of the account API.
    async show(me) {
      this.me = me;
      this.formError = "";
      this.form.password = "";
      if (!me.account) {
        this.stopGame();
        forgetLogs(); // the next one at this browser sees nothing of it
        this.world = 0;
        this.codes = {};
        setHash(0); // the next account to sign in here starts in the lobby
        this.view = "signin";
        this.setStatus("offline", "not signed in");
        this.$nextTick(() => this.$refs.name && this.$refs.name.focus());
        return;
      }
      if (this.view === "play") return;
      await this.loadLobby();
      const want = Number.isSafeInteger(me.joined) && me.joined > 0 ? me.joined : worldFromHash();
      const card = this.worlds.find((c) => c.id === want);
      if (card && card.play) this.enter(card);
      else this.openLobby();
    },

    // loadLobby asks the server for the world cards: the first page, or
    // with page the next one, added to those shown (admins of servers
    // with many worlds).
    async loadLobby(page) {
      try {
        const l = await api(page > 1 ? "/api/worlds?page=" + page : "/api/worlds");
        const got = Array.isArray(l.worlds) ? l.worlds.filter((c) => c && typeof c === "object").map(cleanCard).filter((c) => c.id) : [];
        if (page > 1) {
          const seen = new Set(this.worlds.map((c) => c.id));
          this.worlds = [...this.worlds, ...got.filter((c) => !seen.has(c.id))];
        } else {
          this.worlds = got;
        }
        this.page = page > 1 ? page : 1;
        this.more = l.more === true && this.page < 1000;
        this.packs = Array.isArray(l.packs)
          ? l.packs
              .filter((p) => p && typeof p.id === "string")
              .map((p) => ({ id: p.id, title: text(p.title).slice(0, 120), description: text(p.description).slice(0, 300), version: text(p.version).slice(0, 64) }))
          : [];
        if (!this.packs.some((p) => p.id === this.newWorld.pack)) this.newWorld.pack = this.packs.length ? this.packs[0].id : "";
      } catch (err) {
        this.formError = err.message;
      }
    },

    async moreWorlds() {
      if (this.busy || !this.more) return;
      this.busy = true;
      try {
        await this.loadLobby(this.page + 1);
      } finally {
        this.busy = false;
      }
    },

    openLobby() {
      this.view = "lobby";
      this.world = 0;
      setHash(0);
      this.setStatus("offline", "pick a world");
    },

    // send runs one account request and shows its answer.
    async send(path, body) {
      if (this.busy) return;
      this.busy = true;
      this.formError = "";
      try {
        await this.show(await api(path, body));
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
    },

    signIn() {
      this.send("/api/login", { name: this.form.name.trim(), password: this.form.password });
    },
    register() {
      this.send("/api/register", { name: this.form.name.trim(), password: this.form.password });
    },
    join() {
      this.send("/api/join", { code: this.form.code.trim() });
      this.form.code = "";
    },
    signOut() {
      this.send("/api/logout", {});
    },

    // enter plays the world of card.
    enter(card) {
      this.stopGame();
      this.world = card.id;
      setHash(card.id);
      this.view = "play";
      this.startGame();
    },

    // toLobby leaves the world for the lobby.
    async toLobby() {
      this.stopGame();
      this.openLobby();
      await this.loadLobby();
    },

    // createWorld makes a world from the picked pack and shows its join
    // code on its card.
    async createWorld() {
      if (this.busy) return;
      this.busy = true;
      this.formError = "";
      try {
        const made = await api("/api/worlds", {
          pack: this.newWorld.pack,
          title: cleanLine(this.newWorld.title),
          visibility: this.newWorld.visibility === "private" ? "private" : "public",
        });
        const id = made.world && Number.isSafeInteger(made.world.id) ? made.world.id : 0;
        if (id && made.invite) this.keepCode(id, made.invite);
        this.newWorld.title = "";
        await this.loadLobby();
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
    },

    // makeCode makes a join code for card's world, for a group.
    async makeCode(card) {
      if (this.busy) return;
      this.busy = true;
      this.formError = "";
      try {
        this.keepCode(card.id, await api("/api/invites", { world: card.id, uses: 30 }));
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
    },

    keepCode(world, inv) {
      const code = text(inv.code);
      const n = Number.isSafeInteger(inv.uses) ? inv.uses : 1;
      const until = new Date(text(inv.expires)).toLocaleDateString();
      const who = n === 1 ? "one player" : "up to " + n + " players";
      this.codes = { ...this.codes, [world]: { code, copied: false, text: "Join code: " + code + " (for " + who + ", until " + until + ")" } };
    },

    codeFor(card) {
      const c = this.codes[card.id];
      return c ? c.text : "";
    },

    async copyCode(card) {
      const c = this.codes[card.id];
      if (!c) return;
      try {
        await navigator.clipboard.writeText(c.code);
        this.codes = { ...this.codes, [card.id]: { ...c, copied: true } };
      } catch {
        this.formError = "This browser did not let the page copy. Select the code and copy it yourself.";
      }
    },

    copyLabel(card) {
      const c = this.codes[card.id];
      return c && c.copied ? "copied" : "copy";
    },

    async endWorld(card) {
      if (this.busy) return;
      if (!window.confirm("End " + card.title + " for every player? Its game state is deleted. This cannot be undone.")) return;
      this.busy = true;
      this.formError = "";
      try {
        await api("/api/finish", { world: card.id });
        await this.loadLobby();
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
    },

    // makeInvite asks for a one-use invite code to the world played and
    // shows it in the story.
    async makeInvite() {
      try {
        const inv = await api("/api/invites", { world: this.world });
        const until = new Date(text(inv.expires)).toLocaleDateString();
        this.addLine("Invite code: " + text(inv.code) + " (for one player, until " + until + ")", "notice", true);
      } catch (err) {
        this.addLine(err.message, "error", true);
      }
    },

    playHere() {
      this.view = "loading";
      this.loadMe();
    },

    get signedIn() {
      return !!(this.me && this.me.account);
    },
    get accountName() {
      return this.signedIn ? text(this.me.account.name) : "";
    },
    get canRegister() {
      return !!this.me && this.me.registration === "self";
    },
    get isEdu() {
      return !!this.me && this.me.mode === "edu";
    },
    get canCreate() {
      return this.signedIn && this.packs.length > 0;
    },
    get playing() {
      return this.worlds.find((c) => c.id === this.world) || null;
    },
    get canInvite() {
      const c = this.playing;
      return this.signedIn && this.view === "play" && !!c && c.invite;
    },
    get showSignIn() {
      return this.view === "signin";
    },
    get showLobby() {
      return this.view === "lobby";
    },
    get showPlay() {
      return this.view === "play";
    },
    get showReplaced() {
      return this.view === "replaced";
    },

    // The lobby's cards: the account's own worlds first, then the main
    // world, then by players online and the newest.
    get cards() {
      const rank = (c) => (c.role === "gm" ? 0 : c.role ? 1 : c.main ? 2 : 3);
      return [...this.worlds].sort((a, b) => rank(a) - rank(b) || b.online - a.online || b.id - a.id);
    },
    get noWorlds() {
      return this.worlds.length === 0;
    },
    get noWorldsText() {
      return this.canCreate
        ? "No worlds to play yet. Join one with a code, or start a new one below."
        : "No worlds for you yet. When a game master gives you a code, type it below.";
    },
    get newPack() {
      return this.packs.find((p) => p.id === this.newWorld.pack) || null;
    },
    get newPackDescription() {
      return this.newPack ? this.newPack.description : "";
    },
    get newPackTitle() {
      return this.newPack ? this.newPack.title : "";
    },
    // packLabel names a pack and the version new worlds start on.
    packLabel(p) {
      return p.version ? p.title + " (" + p.version + ")" : p.title;
    },
    showPackTitle(card) {
      return card.packTitle !== card.title;
    },
    roleText(card) {
      return card.role === "gm" ? "you are its game master" : "you are a member";
    },
    // cardMeta is one line about the world: who runs it, how many play,
    // since when.
    cardMeta(card) {
      const parts = [];
      if (card.version) parts.push("version " + card.version);
      if (card.gm) parts.push("GM " + card.gm);
      parts.push(card.players === 1 ? "1 player" : card.players + " players");
      parts.push(card.online + " online now");
      const started = new Date(card.created);
      if (!Number.isNaN(started.getTime())) parts.push("since " + started.toLocaleDateString());
      return parts.join(" · ");
    },
    cannotPlay(card) {
      if (!card.role && card.visibility !== "public") return "";
      return card.unavailable || "This world's pack is not installed on this server.";
    },

    // The game

    startGame() {
      this.setStatus("connecting", "connecting…");
      // What the player saw here before the page was reloaded.
      lineNo = 0;
      this.lines = loadLog(this.isEdu, this.accountName, this.world).map((l) => ({
        text: l.text,
        kind: l.kind,
        cls: l.kind + " earlier",
        from: speakerName(l.from),
        gm: l.kind === "gm",
        n: ++lineNo,
        speech: "",
        earlier: true,
      }));
      this.atEnd = true;
      this.newBelow = false;
      this.$nextTick(() => this.toEnd());
      // The story shrinks when the items, a question or a phone's keyboard
      // take room below it: keep its end in view.
      if (!resizing && this.$refs.story && typeof ResizeObserver === "function") {
        resizing = new ResizeObserver(() => {
          if (this.atEnd) this.toEnd();
        });
        resizing.observe(this.$refs.story);
      }
      conn = connect({
        world: this.world,
        onOpen: () => this.setStatus("online", "connected"),
        onMessage: (msg) => this.receive(msg),
        onClose: (retryMs) => {
          this.endSession();
          this.setStatus("offline", "offline · retrying in " + Math.round(retryMs / 1000) + " s");
        },
        onReplaced: () => {
          this.endSession();
          conn = null;
          this.view = "replaced";
          this.setStatus("offline", "playing elsewhere");
        },
        // A connection refused because the sign-in ended, the world ended
        // or the invite was taken back looks like any other: ask the
        // server first.
        canRetry: async () => {
          const me = await api("/api/me");
          if (!me.account) {
            this.show(me);
            return false;
          }
          await this.loadLobby();
          const card = this.playing;
          if (card && card.play) return true;
          this.stopGame();
          this.openLobby();
          this.formError = "That world has ended, or you may no longer play it.";
          return false;
        },
      });
    },

    stopGame() {
      if (conn) conn.stop();
      conn = null;
      this.endSession();
      this.lines = [];
      this.items = [];
    },

    // endSession forgets what belonged to the connection that ended.
    endSession() {
      this.closeDialog();
      this.sent = null;
      this.timers = []; // the session ended, and its timers with it
    },

    // wire sends msg to the engine; false when there is no connection.
    wire(msg) {
      return conn !== null && conn.send(msg);
    },

    setStatus(state, s) {
      this.state = state;
      this.statusText = s;
    },

    // receive renders one message from the engine.
    receive(msg) {
      // An error with the id of the question just answered means the
      // engine refused the answer and the question is still open: show it
      // again. Any other error means the question is gone.
      if (this.sent && msg.type === "error" && msg.id === this.sent.id && !this.dialog) {
        this.dialog = this.sent;
        this.picked = [];
      }
      if (msg.type !== "pong") this.sent = null;
      switch (msg.type) {
        case "welcome":
          this.setStatus("online", "connected · engine " + text(msg.version));
          // A new connection starts a new session; the story so far stays
          // above a divider.
          if (this.lines.length > 0) this.addLine("· · ·", "divider", true);
          else if (this.history.length === 0) this.addLine("Type /help to see what you can type.", "server", true);
          this.items = [];
          this.timers = [];
          this.closeDialog();
          break;
        case "room":
          this.addLine(msg.title, "room-title");
          this.addLine(msg.text, "");
          this.items = cleanItems(msg.items);
          break;
        case "text":
          this.addLine(msg.text, "", false, { speak: msg.speak === true, hide: msg.hide === true, from: speakerName(msg.from) });
          break;
        case "items":
          this.items = cleanItems(msg.items);
          break;
        case "dialog":
        case "select":
        case "ask":
          this.openDialog(msg);
          break;
        case "image":
          this.showPicture(msg);
          break;
        case "timer":
          this.setTimer(msg);
          break;
        case "close":
          // The engine ended the question: its timeout passed, or the
          // player was moved.
          if (this.dialog && this.dialog.id === text(msg.id)) this.closeDialog();
          break;
        case "notice":
          // From the engine or a game master: a look a pack's lines never
          // get, so a story cannot pretend to be either.
          if (msg.role === "gm") this.addLine(msg.text, "gm", false, { from: speakerName(msg.from) });
          else this.addLine(msg.text, "server", true);
          break;
        case "error":
          this.addLine(msg.message, "error");
          break;
      }
    },

    // addLine adds a story line and reads it aloud when read aloud is on.
    // voice.speak reads it aloud anyway; voice.hide shows it only when this
    // device cannot speak it (a listening exercise), and offers a replay.
    // voice.from names who says it, such as the guide: the line shows the
    // name and is read in the guide's voice.
    addLine(value, kind, silent, voice) {
      const s = text(value);
      if (s === "") return;
      const from = voice && voice.from ? voice.from : "";
      const spoken = !!voice && voice.speak && this.canSpeak;
      const hidden = spoken && voice.hide;
      const gm = kind === "gm";
      const k = hidden ? "spoken" : from && !gm ? (kind + " guide").trim() : kind;
      this.lines.push({
        n: ++lineNo,
        text: hidden ? "(listen)" : s,
        kind: k,
        cls: k,
        from,
        gm,
        speech: spoken ? s : "",
        earlier: false,
      });
      if (this.lines.length > MAX_LINES) this.lines.splice(0, this.lines.length - MAX_LINES);
      saveLog(this.isEdu, this.accountName, this.world, this.lines);
      if (spoken) speaker.speak(s, from);
      else if (gm && !silent) this.say("Game master " + (from ? from + " " : "") + "says: " + s, ""); // a pack's voice cannot sound like one
      else if (!silent) this.say(s, from);
      this.follow();
    },

    // Chat-like scrolling: the story stays at its newest line while the
    // player is there; reading further up, new lines wait below a button.

    // follow keeps the end in view after the page drew a change.
    follow() {
      this.$nextTick(() => {
        if (this.atEnd) this.toEnd();
        else this.newBelow = true;
      });
    },

    toEnd() {
      const story = this.$refs.story;
      if (story) story.scrollTop = story.scrollHeight;
      this.atEnd = true;
      this.newBelow = false;
    },

    // onStoryScroll notes whether the player is at the end of the story.
    // Only scrolling up leaves it: the story growing, or the page
    // scrolling to the end, fires scroll events too, sometimes after
    // more lines came in.
    onStoryScroll() {
      const story = this.$refs.story;
      if (!story) return;
      const top = story.scrollTop;
      if (story.scrollHeight - top - story.clientHeight <= NEAR_BOTTOM) {
        this.atEnd = true;
        this.newBelow = false;
      } else if (top < scrollSeen - 1) {
        this.atEnd = false;
      }
      scrollSeen = top;
    },

    // jumpToEnd is the "new lines" button.
    jumpToEnd() {
      this.toEnd();
      if (this.$refs.command) this.$refs.command.focus();
    },

    // acted: the player did something, so they want to see what follows.
    acted() {
      this.atEnd = true;
      this.follow();
    },

    say(s, from) {
      if (this.readAloud && this.canSpeak) speaker.speak(s, from);
    },

    // replay speaks a spoken line again.
    replay(line) {
      if (line && line.speech) speaker.speak(line.speech, line.from);
    },

    // Timers

    // setTimer shows, updates or removes one countdown; ms 0 removes it.
    setTimer(msg) {
      const id = text(msg.id);
      const rest = this.timers.filter((t) => t.id !== id);
      const ms = msLeft(msg.ms);
      if (ms) {
        rest.push({ id, title: cleanLine(text(msg.title)).slice(0, 100), end: Date.now() + ms });
        this.now = Date.now();
      }
      this.timers = rest.slice(-MAX_TIMERS);
    },

    get timerList() {
      return this.timers.map((t) => ({ id: t.id, text: t.title + " " + clockText(t.end - this.now) }));
    },

    get dialogCountdown() {
      if (!this.dialog || !this.dialog.end) return "";
      return "time left: " + clockText(this.dialog.end - this.now);
    },

    // Items

    get showItems() {
      // A strict question hides them: an item's label could give the answer away.
      return this.items.length > 0 && !(this.dialog && this.dialog.strict);
    },

    click(item) {
      if (this.dialog) {
        this.hint = "Answer the question first.";
        return;
      }
      if (this.wire({ type: "click", item: item.id })) {
        this.acted();
        this.addLine(item.label, "you", true);
      }
    },

    // Dialogs and multiple choice

    openDialog(msg) {
      const ask = msg.type === "ask";
      const options = ask || !Array.isArray(msg.options) ? [] : msg.options.slice(0, 9).map(text);
      if (options.length === 0 && !ask) return;
      const multiple = msg.type === "select";
      const max = Number.isInteger(msg.max) ? msg.max : options.length;
      this.dialog = {
        id: text(msg.id),
        title: text(msg.title),
        options,
        multiple,
        min: Number.isInteger(msg.min) ? msg.min : 0,
        max: Math.min(max, options.length),
        ask,
        strict: ask && msg.strict === true,
        from: speakerName(msg.from),
        speech: "",
        hide: false,
        end: msLeft(msg.ms) ? Date.now() + msLeft(msg.ms) : 0,
      };
      this.now = Date.now();
      // A spoken question: always read aloud, and with hide only heard.
      const title = this.dialog.title;
      if (msg.speak === true && this.canSpeak) {
        this.dialog.speech = title;
        this.dialog.hide = msg.hide === true;
        speaker.speak(title, this.dialog.from);
      } else {
        this.say(ask ? title : title + ". " + options.map((o, i) => i + 1 + ": " + o).join(". "), this.dialog.from);
      }
      this.picked = [];
      this.hint = "";
      this.draft = "";
      this.follow(); // the question takes room below the story
      if (ask) {
        this.$nextTick(() => this.$refs.command && this.$refs.command.focus());
        return;
      }
      // Focus the first answer once Alpine has drawn the options.
      this.$nextTick(() =>
        requestAnimationFrame(() => {
          const list = multiple ? this.$refs.multiList : this.$refs.singleList;
          const first = list && list.querySelector("button, input");
          if (first) first.focus();
        }),
      );
    },

    closeDialog() {
      this.dialog = null;
      this.picked = [];
      this.hint = "";
    },

    // answered writes the question and its answer into the story, so the
    // story reads on after the question box closes, and closes the
    // question; receive reopens it if the engine refuses the answer.
    answered(answer) {
      this.acted();
      this.addLine(this.dialogTitle, "asked", true, { from: this.dialog.from });
      this.addLine(answer, "you", true);
      this.sent = this.dialog;
      this.closeDialog();
    },

    get dialogTitle() {
      if (!this.dialog) return "";
      return this.dialog.hide ? "Listen, then answer." : this.dialog.title;
    },
    get canReplayQuestion() {
      return this.dialog !== null && this.dialog.speech !== "";
    },
    replayQuestion() {
      if (this.canReplayQuestion) speaker.speak(this.dialog.speech, this.dialog.from);
    },
    get dialogLabel() {
      return this.dialogFrom ? this.dialogFrom + ": " + this.dialogTitle : this.dialogTitle;
    },
    get dialogFrom() {
      return this.dialog ? this.dialog.from : "";
    },
    get isAsk() {
      return this.dialog !== null && this.dialog.ask;
    },
    get isStrict() {
      return this.dialog !== null && this.dialog.strict;
    },
    get dialogOptions() {
      return this.dialog ? this.dialog.options : [];
    },
    get isSingle() {
      return this.dialog !== null && !this.dialog.multiple && !this.dialog.ask;
    },
    get isMultiple() {
      return this.dialog !== null && this.dialog.multiple;
    },
    get selectHint() {
      if (!this.isMultiple) return "";
      const { min, max } = this.dialog;
      const n = this.picked.length;
      const want = min === max ? "Pick " + max : min === 0 ? "Pick up to " + max : "Pick " + min + " to " + max;
      return want + " · " + n + " picked";
    },
    get canConfirm() {
      return this.isMultiple && this.picked.length >= this.dialog.min && this.picked.length <= this.dialog.max;
    },

    isPicked(n) {
      return this.picked.includes(n);
    },

    // choose answers a single choice with option n (1-based).
    choose(n) {
      if (!this.isSingle || n < 1 || n > this.dialog.options.length) return;
      const d = this.dialog;
      if (this.wire({ type: "choose", id: d.id, option: n })) {
        this.answered(d.options[n - 1]);
      }
    },

    // toggle ticks or unticks option n of a multiple choice. From a
    // checkbox, event is its change event: a refused tick is undone there,
    // since the box ticks itself before this runs.
    toggle(n, event) {
      if (!this.isMultiple || n < 1 || n > this.dialog.options.length) return;
      if (this.isPicked(n)) {
        this.picked = this.picked.filter((p) => p !== n);
        this.hint = "";
      } else if (this.picked.length < this.dialog.max) {
        this.picked = [...this.picked, n].sort((a, b) => a - b);
        this.hint = "";
      } else {
        this.hint = "You can pick at most " + this.dialog.max + ".";
      }
      if (event && event.target) event.target.checked = this.isPicked(n);
    },

    confirm() {
      if (!this.canConfirm) return;
      const d = this.dialog;
      if (this.wire({ type: "choose", id: d.id, choices: this.picked })) {
        const labels = this.picked.map((n) => d.options[n - 1]);
        this.answered(labels.length ? labels.join(", ") : "(nothing)");
      }
    },

    // Typed input

    submit() {
      const line = cleanLine(this.draft);
      if (line === "") {
        this.draft = "";
        return;
      }
      if (new TextEncoder().encode(line).length > MAX_INPUT) {
        this.hint = "That is too long.";
        return;
      }
      // A slash command goes to the engine as it is, also while a
      // question is open: /help should always work.
      if (line.startsWith("/")) {
        if (!this.wire({ type: "input", text: line })) return;
        this.acted();
        this.addLine(line, "you", true);
        this.remember(line);
        this.draft = "";
        this.hint = "";
        return;
      }
      if (this.isAsk) {
        const d = this.dialog;
        if (this.wire({ type: "answer", id: d.id, text: line })) {
          this.answered(line);
          this.draft = "";
        }
        return;
      }
      if (this.dialog && this.answerByText(line)) {
        this.draft = "";
        return;
      }
      if (this.dialog) {
        this.hint = "Answer the question first: type its number, or click an answer.";
        return;
      }
      if (!this.wire({ type: "input", text: line })) return;
      this.acted();
      this.addLine(line, "you", true);
      this.remember(line);
      this.draft = "";
    },

    // answerByText answers the open dialog with a typed number or label.
    answerByText(line) {
      const d = this.dialog;
      const lower = line.toLowerCase();
      if (!d.multiple) {
        let n = Number(line);
        if (!Number.isInteger(n)) n = d.options.findIndex((o) => o.toLowerCase() === lower) + 1;
        if (n < 1 || n > d.options.length) return false;
        this.choose(n);
        return true;
      }
      const nums = line.split(/[\s,]+/).map(Number);
      if (nums.some((n) => !Number.isInteger(n) || n < 1 || n > d.options.length)) return false;
      this.picked = [...new Set(nums)].sort((a, b) => a - b);
      if (!this.canConfirm) {
        this.hint = this.selectHint;
        return true;
      }
      this.confirm();
      return true;
    },

    remember(line) {
      if (this.history[this.history.length - 1] !== line) this.history.push(line);
      if (this.history.length > MAX_HISTORY) this.history.shift();
      historyAt = -1;
    },

    // onInputKey walks through earlier lines with arrow up and down.
    onInputKey(event) {
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      if (this.history.length === 0) return;
      event.preventDefault();
      if (historyAt === -1) historyAt = this.history.length;
      historyAt += event.key === "ArrowUp" ? -1 : 1;
      if (historyAt < 0) historyAt = 0;
      if (historyAt >= this.history.length) {
        historyAt = -1;
        this.draft = "";
        return;
      }
      this.draft = this.history[historyAt];
    },

    // onKey: number keys answer the open dialog, unless the player is
    // typing a line; Enter confirms a multiple choice.
    onKey(event) {
      if (!this.dialog || this.dialog.ask || event.ctrlKey || event.metaKey || event.altKey) return;
      if (this.$refs.picture && this.$refs.picture.open) return;
      const typing = event.target === this.$refs.command && this.draft !== "";
      if (event.key === "Enter" && this.isMultiple && event.target !== this.$refs.command) {
        event.preventDefault();
        this.confirm();
        return;
      }
      const n = Number(event.key);
      if (typing || !Number.isInteger(n) || n < 1 || n > this.dialog.options.length) return;
      event.preventDefault();
      if (this.isSingle) this.choose(n);
      else this.toggle(n);
    },

    // Pictures

    showPicture(msg) {
      const url = imageURL(msg.url);
      if (url === "") return;
      const caption = text(msg.text);
      if (url !== this.picture.url) this.pixelArt = false;
      this.picture = { url, caption };
      if (caption) this.addLine("[picture] " + caption, "picture-note");
      const dlg = this.$refs.picture;
      if (dlg && !dlg.open) dlg.showModal();
    },

    // pictureLoaded keeps small pixel art sharp when it is scaled up.
    pictureLoaded(event) {
      this.pixelArt = event.target.naturalWidth > 0 && event.target.naturalWidth <= 256;
    },

    get pictureClass() {
      return this.pixelArt ? "pixel" : "";
    },

    closePicture() {
      const dlg = this.$refs.picture;
      if (dlg && dlg.open) dlg.close();
    },

    pictureClosed() {
      if (this.$refs.command) this.$refs.command.focus();
    },

    // Settings

    toggleReadAloud() {
      this.readAloud = !this.readAloud;
      saveSetting("readAloud", this.readAloud);
      if (!this.readAloud) speaker.stop();
    },

    toggleLargeText() {
      this.largeText = !this.largeText;
      saveSetting("largeText", this.largeText);
      this.applyLargeText();
    },

    applyLargeText() {
      document.documentElement.classList.toggle("large", this.largeText);
    },

    get readAloudLabel() {
      return this.readAloud ? "read aloud: on" : "read aloud: off";
    },
    get largeTextLabel() {
      return this.largeText ? "large text: on" : "large text: off";
    },
    // No typing hints while playing: finding out what works is part of
    // the game, and /help is there.
    get placeholder() {
      return this.state === "online" ? "" : "waiting for the server…";
    },
    // Typing help (autocorrect, autocomplete) is off for strict questions.
    get autocorrect() {
      return this.isStrict ? "off" : "on";
    },
  };
}

Alpine.data("game", game);
Alpine.start();
