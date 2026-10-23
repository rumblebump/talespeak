// What the player sees and sets: pictures, read aloud, large text, and
// the labels that go with them. A part of the game component; app.js
// merges it in.

import { imageURL, saveSetting, text } from "/clean.js";

/** view returns the methods and getters of this part. */
export function view(speaker) {
  return {
    // Pictures

    showPicture(msg) {
      const url = imageURL(msg.url);
      if (url === "") return;
      const caption = text(msg.text).slice(0, 200);
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
