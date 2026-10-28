// A paused world (T-0012): while a game master has paused it, its
// countdowns stand still and what the player does waits. The engine
// sends {type: "pause", paused} and, on resume, every countdown again
// with the time left. A part of the game component; app.js merges it in.

import { msLeft, text } from "/clean.js";

/** pause returns the methods and getters of this part. */
export function pause() {
  return {
    // setPaused stops the countdowns where they are, or lets them run.
    setPaused(on) {
      this.paused = on;
      this.frozenAt = on ? Date.now() : 0;
      this.now = Date.now();
    },

    // clockNow is the time the countdowns count to: frozen while paused.
    get clockNow() {
      return this.paused ? this.frozenAt : this.now;
    },

    // endIn is when a countdown with ms left ends, on this browser's clock
    // as the countdowns see it; 0 for no countdown.
    endIn(ms) {
      const left = msLeft(ms);
      return left ? (this.paused ? this.frozenAt : Date.now()) + left : 0;
    },

    // setQuestionTimer updates the open question's countdown from a timer
    // message whose id is "question:" and the question's id: the game
    // master gave more time, took the limit off, or resumed the game.
    setQuestionTimer(msg) {
      const id = text(msg.id).slice("question:".length);
      if (this.dialog && this.dialog.id === id) this.dialog.end = this.endIn(msg.ms);
      if (this.sent && this.sent.id === id) this.sent.end = this.endIn(msg.ms);
    },
  };
}
