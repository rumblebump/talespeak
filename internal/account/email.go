package account

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rumblebump/talespeak/internal/mail"
	"github.com/rumblebump/talespeak/internal/store"
)

// Email addresses: an account adds one (POST /api/email) and confirms it
// with the link mailed to it (POST /api/verify). Only a confirmed address
// is kept on the account, and only it gets reset links. In edu mode the
// players never add one: only admins and game masters (the teachers) do.
// Addresses are never logged.

type emailRequest struct {
	Email string `json:"email"` // "" removes the address
}

// mayHaveEmail refuses an address to a (nil: an account being
// registered) when the server sends no mail, and in edu mode to anyone
// but admins and game masters.
func (s *Service) mayHaveEmail(ctx context.Context, a *store.Account) error {
	if !s.cfg.Email {
		return fail(http.StatusBadRequest, "This server sends no mail: leave the email address out.")
	}
	if !s.cfg.Edu {
		return nil
	}
	if a == nil {
		return fail(http.StatusForbidden, "Accounts on this server register without an email address.")
	}
	if a.Role == store.RoleAdmin {
		return nil
	}
	gm, err := s.cfg.Store.IsGM(ctx, a.ID)
	if err != nil {
		return err
	}
	if !gm {
		return fail(http.StatusForbidden, "On this server only teachers and admins add an email address.")
	}
	return nil
}

func (s *Service) setEmail(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q emailRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if err := s.mayHaveEmail(r.Context(), &a); err != nil {
		return nil, err
	}
	notice := "Your email address is removed."
	if q.Email == "" {
		if err := s.cfg.Store.SetEmail(r.Context(), a.ID, ""); err != nil {
			return nil, err
		}
		s.cfg.Log.Info("email address removed", "account", a.ID)
	} else {
		if err := mail.ValidAddress(q.Email); err != nil {
			return nil, fail(http.StatusBadRequest, err.Error())
		}
		if notice, err = s.sendVerify(r.Context(), a, q.Email); err != nil {
			return nil, err
		}
	}
	me, err := s.describe(r.Context(), &a)
	me.Notice = notice
	return me, err
}

// sendVerify queues the mail that confirms email for a, and returns what
// to tell the account.
func (s *Service) sendVerify(ctx context.Context, a store.Account, email string) (string, error) {
	if !s.mailAcc.allow(nameKey(a.Name)) || !s.mailTo.allow(strings.ToLower(email)) {
		return "", errManyMails
	}
	code := randomCode(linkCodeLen)
	t := store.Token{Hash: tokenCodeHash(code), Account: a.ID, Kind: store.TokenVerify, Email: email, Expires: s.now().Add(VerifyLinkTTL)}
	err := s.cfg.Store.CreateToken(ctx, t, &store.Mail{To: email, Subject: verifySubject, Body: s.verifyBody(a.Name, code)}, s.now())
	if errors.Is(err, store.ErrOutboxFull) {
		s.cfg.Log.Warn("confirmation mail not queued: the outbox is full", "account", a.ID)
		return "", errMailLater
	}
	if err != nil {
		return "", err
	}
	s.cfg.Kick()
	s.cfg.Log.Info("confirmation mail queued", "account", a.ID)
	return "We are sending a mail to " + email + ". Open its link within a day to confirm the address.", nil
}

type verifyRequest struct {
	Code string `json:"code"`
}

var errBadLink = fail(http.StatusBadRequest, "That link is not valid, used or expired.")

// verifyEmail confirms an address with the code of its mailed link. It
// needs no sign-in: the link may be opened on another device.
func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) (any, error) {
	if !s.cfg.Email {
		return nil, errNoMail
	}
	var q verifyRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if !s.guess.allow(s.client(r)) {
		return nil, errSlowDown
	}
	code, ok := normalizeSymbols(q.Code, linkCodeLen)
	if !ok {
		return nil, errBadLink
	}
	t, owner, err := s.cfg.Store.UseToken(r.Context(), tokenCodeHash(code), []string{store.TokenVerify}, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadLink
	}
	if err != nil {
		return nil, err
	}
	if err := s.mayHaveEmail(r.Context(), &owner); err != nil {
		return nil, err // no longer a teacher since the mail went
	}
	err = s.cfg.Store.SetEmail(r.Context(), owner.ID, t.Email)
	if errors.Is(err, store.ErrEmailTaken) {
		return nil, fail(http.StatusConflict, "That address belongs to another account already.")
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("email address confirmed", "account", owner.ID)
	var me Me
	if a, err := s.account(r.Context(), r); err == nil {
		me, err = s.describe(r.Context(), &a)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, errSignIn) {
		return nil, err
	} else if me, err = s.describe(r.Context(), nil); err != nil {
		return nil, err
	}
	me.Notice = "The email address of " + owner.Name + " is confirmed."
	return me, nil
}

// The mails. They name the account they are for and nothing else: no
// chat, nothing about other players.
const (
	resetSubject  = "Reset your Talespeak password"
	verifySubject = "Confirm your email address for Talespeak"
)

func (s *Service) link(kind, code string) string {
	return strings.TrimRight(s.cfg.PublicURL, "/") + "/#" + kind + "=" + groupCode(code)
}

func (s *Service) resetBody(name, code string) string {
	return "Someone asked to reset the password of the Talespeak account " + name + ".\n\n" +
		"To pick a new password, open this link within an hour:\n" + s.link("reset", code) + "\n\n" +
		"Or choose \"I have a reset code\" on the sign-in page and type this code:\n" + groupCode(code) + "\n\n" +
		"If it was not you, ignore this mail: your password stays as it is.\n"
}

func (s *Service) verifyBody(name, code string) string {
	return "The Talespeak account " + name + " wants to use this address for password resets.\n\n" +
		"To confirm it, open this link within a day:\n" + s.link("verify", code) + "\n\n" +
		"If you do not know this account, ignore this mail: the address will not be used.\n"
}
