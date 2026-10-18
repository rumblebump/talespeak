// Read aloud with the browser's own voices (Web Speech API).
//
// Only voices that run on this device are used (localService): some
// browsers also list cloud voices, which would send the story text to a
// third party. With no local voice, read aloud is not offered.

/**
 * @returns {{ available: () => boolean, onChange: (f: () => void) => void, speak: (text: string) => void, stop: () => void }}
 */
export function createSpeaker() {
  const synth = typeof window.speechSynthesis === "object" ? window.speechSynthesis : null;
  const supported = synth !== null && typeof window.SpeechSynthesisUtterance === "function";
  let voice = null;
  let changed = () => {};

  function pickVoice() {
    if (!supported) return;
    const local = synth.getVoices().filter((v) => v.localService);
    const lang = (document.documentElement.lang || "en").toLowerCase();
    voice = local.find((v) => v.lang.toLowerCase().startsWith(lang)) || local[0] || null;
    changed();
  }

  if (supported) {
    pickVoice();
    synth.addEventListener("voiceschanged", pickVoice); // voices often load late
  }

  return {
    available: () => voice !== null,
    onChange(f) {
      changed = f;
    },
    speak(text) {
      if (!voice || !text) return;
      const u = new SpeechSynthesisUtterance(String(text));
      u.voice = voice;
      u.lang = voice.lang;
      synth.speak(u); // queued: lines are read in order
    },
    stop() {
      if (supported) synth.cancel();
    },
  };
}
