// The Talespeak client: the lobby of worlds, then the engine's messages (story text, spoken
// lines, rooms, clickable items, dialogs, multiple choice, typed questions,
// pictures) and sends back what the player clicks and types. Plain ES
// modules, no build step.
//
// Alpine's CSP build runs the page without 'unsafe-eval', so the HTML only
// holds simple expressions and all logic lives in these modules: the
// component's state here, its parts in lobby.js, members.js, password.js, play.js,
// dialog.js and view.js, helpers in clean.js and api.js. Server text is
// untrusted: it is only ever shown with x-text or textContent.

import Alpine from "/vendor/alpine-csp.js";
import { dialog } from "/dialog.js";
import { lobby } from "/lobby.js";
import { members } from "/members.js";
import { loadSetting } from "/clean.js";
import { password } from "/password.js";
import { play } from "/play.js";
import { createSpeaker } from "/speech.js";
import { view } from "/view.js";

function game() {
  const speaker = createSpeaker();

  const component = {
    view: "loading", // signin, lobby, play or replaced
    me: null, // the answer of /api/me
    worlds: [], // the lobby's world cards
    page: 1, // the last lobby page loaded
    more: false, // the lobby has more worlds than it shows
    packs: [], // what the account may create worlds from
    world: 0, // the world played, or about to be
    asPlayer: false, // a game master plays the world as a player (GM mode off)
    membersOf: 0, // the world whose members the lobby shows, 0 for none
    members: [], // its members: { name, role, change }
    membersMakeGM: false, // the account may make its members game masters
    membersAdd: false, // the account may add accounts by name (admins)
    addMember: { name: "" },
    codes: {}, // world id -> the last join code made here: { text, code, copied }
    newWorld: { pack: "", title: "", visibility: "public" },
    form: { name: "", password: "", code: "", classCode: "", email: "" },
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
      this.takeLinkCode(); // before loadMe, which clears the address of a signed-out page
      this.loadMe();
    },
  };
  // The parts in their own files add their methods and getters; getters
  // must be copied as getters, so not with Object.assign.
  for (const part of [lobby(), members(), password(), play(speaker), dialog(speaker), view(speaker)]) {
    Object.defineProperties(component, Object.getOwnPropertyDescriptors(part));
  }
  return component;
}

Alpine.data("game", game);
Alpine.start();
