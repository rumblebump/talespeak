// Questions and typed input: dialogs, multiple choice, typed answers, the
// input line with its history and keys. A part of the game component;
// app.js merges it in.

import { MAX_HISTORY, MAX_INPUT, cleanLine, msLeft, speakerName, text } from "/clean.js";

/** dialog returns the methods and getters of this part. */
export function dialog(speaker) {
  let historyAt = -1;
  return {
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
  };
}
