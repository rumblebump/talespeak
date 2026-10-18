// Read aloud with the browser's own voices (Web Speech API).
//
// Only voices that run on this device are used (localService): some
// browsers also list cloud voices, which would send the story text to a
// third party. With no local voice, read aloud is not offered.

/**
 * speak(text, from) reads a line; a line with a speaker (from, such as the
 * guide) gets a second local voice of the same language when there is one,
 * else the same voice a little higher.
 *
 * @returns {{ available: () => boolean, onChange: (f: () => void) => void, speak: (text: string, from?: string) => void, stop: () => void }}
 */
export function createSpeaker() {
  const synth = typeof window.speechSynthesis === "object" ? window.speechSynthesis : null;
  const supported = synth !== null && typeof window.SpeechSynthesisUtterance === "function";
  let voice = null;
  let guide = null; // the voice for lines with a speaker; null: voice, pitched up
  let changed = () => {};

  function pickVoice() {
    if (!supported) return;
    const local = synth.getVoices().filter((v) => v.localService);
    const lang = (document.documentElement.lang || "en").toLowerCase();
    const same = local.filter((v) => v.lang.toLowerCase().startsWith(lang));
    voice = same[0] || local[0] || null;
    guide = same.find((v) => v !== voice) || null;
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
    speak(text, from) {
      if (!voice || !text) return;
      const u = new SpeechSynthesisUtterance(String(text));
      u.voice = from && guide ? guide : voice;
      u.lang = u.voice.lang;
      if (from && !guide) u.pitch = 1.3;
      synth.speak(u); // queued: lines are read in order
    },
    stop() {
      if (supported) synth.cancel();
    },
  };
}
