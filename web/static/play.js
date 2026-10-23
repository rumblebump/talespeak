// Playing a world: the connection, every message the engine sends, the
// story lines and their scrolling, timers and clickable items. A part of
// the game component; app.js merges it in.

import { MAX_LINES, MAX_TIMERS, NEAR_BOTTOM, cleanItems, cleanLine, clockText, msLeft, speakerName, text } from "/clean.js";
import { api } from "/api.js";
import { connect } from "/net.js";
import { loadLog, saveLog } from "/storylog.js";

/** play returns the methods and getters of this part. */
export function play(speaker) {
  let conn = null;
  let lineNo = 0;
  let scrollSeen = 0; // the story's scrollTop at its last scroll event
  let resizing = null; // the ResizeObserver of the story
  return {
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
  };
}
