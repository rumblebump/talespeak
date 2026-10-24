// The members of a world in the lobby (T-0011): its game masters and
// admins see who belongs to it, take players out, and make game masters
// where the server lets them. The server decides who may change whom
// (docs/accounts.md); the buttons only follow its answer. A part of the
// game component; app.js merges it in.

import { text } from "/clean.js";
import { api } from "/api.js";

/** cleanMember keeps what the lobby shows of one member. */
function cleanMember(m) {
  return {
    name: text(m.name).slice(0, 20),
    role: m.role === "gm" ? "gm" : "player",
    change: m.change === true,
  };
}

/** members returns the methods and getters of this part. */
export function members() {
  return {
    // toggleMembers shows the members of card's world, or hides them.
    async toggleMembers(card) {
      if (this.membersOf === card.id) {
        this.closeMembers();
        return;
      }
      this.closeMembers();
      this.membersOf = card.id;
      await this.loadMembers();
    },

    closeMembers() {
      this.membersOf = 0;
      this.members = [];
      this.membersMakeGM = false;
      this.membersAdd = false;
      this.addMember.name = "";
    },

    async loadMembers() {
      const world = this.membersOf;
      if (!world) return;
      try {
        const l = await api("/api/members?world=" + world);
        if (this.membersOf !== world) return; // another card opened meanwhile
        this.members = Array.isArray(l.members) ? l.members.filter((m) => m && typeof m === "object").map(cleanMember).filter((m) => m.name) : [];
        this.membersMakeGM = l.makeGM === true;
        this.membersAdd = l.add === true;
      } catch (err) {
        this.formError = err.message;
        this.closeMembers();
      }
    },

    // setRole asks the server to give name the role ("gm", "player" or
    // "none"), then shows the list again.
    async setRole(name, role) {
      if (this.busy || !this.membersOf) return;
      this.busy = true;
      this.formError = "";
      try {
        await api("/api/members", { world: this.membersOf, name, role });
      } catch (err) {
        this.formError = err.message;
      } finally {
        this.busy = false;
      }
      await this.loadMembers();
    },

    promote(m) {
      this.setRole(m.name, "gm");
    },
    demote(m) {
      this.setRole(m.name, "player");
    },
    removeMember(m) {
      if (!window.confirm("Take " + m.name + " out of this world?")) return;
      this.setRole(m.name, "none");
    },
    addByName() {
      const name = this.addMember.name.trim();
      if (!name) return;
      this.addMember.name = "";
      this.setRole(name, "player");
    },

    membersShown(card) {
      return this.membersOf === card.id;
    },
    get noMembers() {
      return this.members.length === 0;
    },
    memberRole(m) {
      return m.role === "gm" ? "game master" : "player";
    },
    mayPromote(m) {
      return m.change && m.role === "player" && this.membersMakeGM;
    },
    mayDemote(m) {
      return m.change && m.role === "gm";
    },
    // playsAs and playsAsGM pick the play buttons of a card: game masters
    // choose GM mode or not, everyone else just plays.
    playsAs(card) {
      return card.play && card.role !== "gm";
    },
    playsAsGM(card) {
      return card.play && card.role === "gm";
    },
  };
}
