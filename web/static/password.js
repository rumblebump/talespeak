// Passwords and email addresses: a forgotten password (by mail, when the
// server sends it), resetting it with a code (from an admin or teacher,
// or from a mailed link), changing it, making reset codes for others,
// and adding an email address. A part of the game component; app.js
// merges it in. See docs/accounts.md.

import { api } from "/api.js";
import { text } from "/clean.js";

// A code as a person or a link writes it: two or four groups of five.
const CODE = /^[0-9A-Za-z]{5}(-[0-9A-Za-z]{5}){1,3}$/;

/** linkCode reads #reset=CODE or #verify=CODE from the page's address. */
function linkCode() {
  const m = /^#(reset|verify)=([0-9A-Za-z-]{11,23})$/.exec(location.hash);
  return m && CODE.test(m[2]) ? { kind: m[1], code: m[2].toUpperCase() } : null;
}

/** password returns the methods, getters and state of this part. */
export function password() {
  return {
    // pw: which form is open below the sign-in ("forgot", "reset" or ""),
    // what the forms hold, and what the last request said.
    pw: { view: "", name: "", code: "", password: "", current: "", fresh: "", email: "", who: "", made: "", notice: "", error: "", busy: false },

    // takeLinkCode opens the form a mailed link asks for, and takes the
    // code out of the address so it stays in no history or bookmark.
    takeLinkCode() {
      const link = linkCode();
      if (!link) return;
      try {
        history.replaceState(null, "", location.pathname + location.search);
      } catch {
        // a sandboxed page may refuse; the code is used once anyway
      }
      if (link.kind === "reset") {
        this.pw.view = "reset";
        this.pw.code = link.code;
      } else {
        this.pwSend("/api/verify", { code: link.code }, (me) => this.show(me));
      }
    },

    // pwSend posts body to path and passes the answer to then; a
    // notice in the answer is shown, an error below the forms.
    async pwSend(path, body, then) {
      if (this.pw.busy) return;
      this.pw.busy = true;
      this.pw.error = "";
      this.pw.notice = "";
      try {
        const answer = await api(path, body);
        const notice = text(answer.notice || answer.message).slice(0, 300);
        if (then) await then(answer);
        this.pw.notice = notice;
      } catch (err) {
        this.pw.error = err.message;
      } finally {
        this.pw.busy = false;
      }
    },

    openForgot() {
      this.pw.view = this.pw.view === "forgot" ? "" : "forgot";
      this.pw.name = this.form.name;
      this.pw.error = "";
    },
    openReset() {
      this.pw.view = this.pw.view === "reset" ? "" : "reset";
      this.pw.error = "";
    },

    forgot() {
      this.pwSend("/api/forgot", { name: this.pw.name.trim() });
    },

    resetPassword() {
      this.pwSend("/api/reset", { code: this.pw.code.trim(), password: this.pw.password }, (me) => {
        this.pw.view = "";
        this.pw.code = "";
        this.pw.password = "";
        return this.show(me);
      });
    },

    changePassword() {
      this.pwSend("/api/password", { password: this.pw.current, new: this.pw.fresh }, (me) => {
        this.pw.current = "";
        this.pw.fresh = "";
        this.me = me;
      });
    },

    setEmail(remove) {
      this.pwSend("/api/email", { email: remove ? "" : this.pw.email.trim() }, (me) => {
        this.pw.email = "";
        this.me = me;
      });
    },
    removeEmail() {
      this.setEmail(true);
    },
    addEmail() {
      this.setEmail(false);
    },

    makeResetCode() {
      this.pw.made = "";
      this.pwSend("/api/resetcode", { name: this.pw.who.trim() }, (rc) => {
        const until = new Date(text(rc.expires)).toLocaleString();
        this.pw.made = "Reset code for " + text(rc.name) + ": " + text(rc.code) + " (works once, until " + until + ")";
        this.pw.who = "";
      });
    },

    // registerBody is what "new account" sends: the class code and an
    // email address only when the server asks for them.
    registerBody() {
      const body = { name: this.form.name.trim(), password: this.form.password };
      if (this.needsClassCode) body.code = (this.form.classCode || "").trim();
      if (this.canGiveEmail && (this.form.email || "").trim()) body.email = this.form.email.trim();
      return body;
    },

    get needsClassCode() {
      return !!this.me && this.me.classCode === true && this.me.registration === "self";
    },
    // canGiveEmail: a new account may give an address (never in edu mode).
    get canGiveEmail() {
      return !!this.me && this.me.email === true && this.me.mode !== "edu" && this.me.registration === "self";
    },
    get mailOn() {
      return !!this.me && this.me.email === true;
    },
    get showForgot() {
      return this.pw.view === "forgot" && this.view === "signin";
    },
    get showReset() {
      return this.pw.view === "reset" && (this.view === "signin" || this.view === "lobby");
    },
    get showPwNotice() {
      return !!this.pw.notice && (this.view === "signin" || this.view === "lobby");
    },
    get showPwError() {
      return !!this.pw.error && (this.view === "signin" || this.view === "lobby");
    },
    get mayAddEmail() {
      return this.signedIn && this.me.account.addEmail === true;
    },
    get makesResetCodes() {
      return this.signedIn && this.me.account.resetCodes === true;
    },
    get emailText() {
      const e = this.signedIn ? text(this.me.account.email).slice(0, 254) : "";
      return e ? "Your confirmed address: " + e : "No email address yet. Add one to get a reset link when you forget your password.";
    },
    get hasEmail() {
      return this.signedIn && !!this.me.account.email;
    },
  };
}
