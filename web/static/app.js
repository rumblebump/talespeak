// The Talespeak client: renders the engine's messages (story text, spoken
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

// Limits that match the engine (docs/websocket-protocol.md).
const MAX_LINES = 500; // older story lines are dropped
const MAX_INPUT = 500; // bytes the engine accepts in one typed line
const MAX_HISTORY = 50; // typed lines kept for arrow up and down
const MAX_TIMERS = 10; // shown timers per player
const MAX_MS = 24 * 60 * 60 * 1000; // the longest timer or question timeout

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

/** text turns any server value into a string for display. */
const text = (v) => (v === undefined || v === null ? "" : String(v));

/** cleanLine cleans a typed line as the engine does: control characters
 * (newlines included) become spaces, then it is trimmed. */
const cleanLine = (s) => s.replace(/[\p{Cc}\u2028\u2029]/gu, " ").trim();

/** speakerName is who says a line, such as the guide: one short line of
 * text, or "" for the story itself. */
const speakerName = (v) => cleanLine(text(v)).slice(0, 40);

/** A picture URL must be a pack image on this server. */
function imageURL(v) {
  const s = text(v);
  return /^\/packs\/[a-z0-9][a-z0-9_-]{0,63}\/assets\/[a-z0-9][a-z0-9_-]{0,63}\.(png|jpe?g|gif|webp)$/.test(s) ? s : "";
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

  return {
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
      conn = connect({
        onOpen: () => this.setStatus("online", "connected"),
        onMessage: (msg) => this.receive(msg),
        onClose: (retryMs) => {
          this.closeDialog();
          this.sent = null;
          this.timers = []; // the session ended, and its timers with it
          this.setStatus("offline", "offline · retrying in " + Math.round(retryMs / 1000) + " s");
        },
      });
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
          this.lines = []; // a new connection starts a new session
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
      this.lines.push({
        n: ++lineNo,
        text: hidden ? "(listen)" : s,
        kind: hidden ? "spoken" : from ? (kind + " guide").trim() : kind,
        from,
        speech: spoken ? s : "",
      });
      if (this.lines.length > MAX_LINES) this.lines.splice(0, this.lines.length - MAX_LINES);
      if (spoken) speaker.speak(s, from);
      else if (!silent) this.say(s, from);
      this.$nextTick(() => {
        const story = this.$refs.story;
        if (story && story.lastElementChild) story.lastElementChild.scrollIntoView({ block: "nearest" });
      });
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
      if (conn.send({ type: "click", item: item.id })) this.addLine(item.label, "you", true);
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
      if (conn.send({ type: "choose", id: d.id, option: n })) {
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
      if (conn.send({ type: "choose", id: d.id, choices: this.picked })) {
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
      if (this.isAsk) {
        const d = this.dialog;
        if (conn.send({ type: "answer", id: d.id, text: line })) {
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
      if (!conn.send({ type: "input", text: line })) return;
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
    get placeholder() {
      if (this.state !== "online") return "waiting for the server…";
      if (this.isAsk) return "type your answer, then Enter";
      return this.dialog ? "type a number to answer" : "type what you do, then Enter";
    },
    // Typing help (autocorrect, autocomplete) is off for strict questions.
    get autocorrect() {
      return this.isStrict ? "off" : "on";
    },
  };
}

Alpine.data("game", game);
Alpine.start();
