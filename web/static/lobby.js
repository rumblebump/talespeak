// Signing in and the lobby of worlds: the account, world cards, join
// codes, invites and creating and ending worlds. A part of the game
// component; app.js merges it in.

import { cleanCard, cleanLine, setHash, text, worldFromHash } from "/clean.js";
import { api } from "/api.js";
import { forgetLogs } from "/storylog.js";

/** lobby returns the methods and getters of this part. */
export function lobby() {
  return {
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
      this.send("/api/register", this.registerBody()); // password.js: with the class code and email when asked
    },
    join() {
      this.send("/api/join", { code: this.form.code.trim() });
      this.form.code = "";
    },
    signOut() {
      this.send("/api/logout", {});
    },

    // enter plays the world of card; a game master of it plays as a
    // player with asPlayer (GM mode off until /gm).
    enter(card, asPlayer) {
      this.stopGame();
      this.asPlayer = asPlayer === true;
      this.closeMembers();
      this.world = card.id;
      setHash(card.id);
      this.view = "play";
      this.startGame();
    },

    enterAsPlayer(card) {
      this.enter(card, true);
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
  };
}
