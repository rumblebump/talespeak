package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/mail"
	"github.com/rumblebump/talespeak/internal/store"
)

// Passwords that change: one-time reset codes that admins (for any
// account) and edu mode's game masters (for the plain players of their
// worlds) make and hand over, which work without mail; reset links by
// mail; and a signed-in account changing its own password. Each ends
// every sign-in of the account and its open games.

// How long codes work. A reset code made by a person is handed over in
// class or by message, so it lasts a day; a mailed link lasts an hour.
const (
	ResetCodeTTL  = 24 * time.Hour
	ResetLinkTTL  = time.Hour
	VerifyLinkTTL = 24 * time.Hour
	// linkCodeLen is the length of a code in a mailed link: 100 bits,
	// where a code read aloud has 50 (codeLen) and a person nearby.
	linkCodeLen = 20
)

var (
	errBadReset   = fail(http.StatusBadRequest, "That reset code is not valid, used or expired.")
	errNotYours   = fail(http.StatusForbidden, "You make reset codes only for the players of your worlds, never for game masters or admins.")
	errNoMail     = fail(http.StatusForbidden, "This server sends no mail. Ask an admin or your teacher for a reset code.")
	errMailLater  = fail(http.StatusServiceUnavailable, "The server cannot send mail right now. Try again later.")
	errManyMails  = fail(http.StatusTooManyRequests, "Too many mails: wait a while, then try again.")
	errWrongOwnPw = fail(http.StatusUnauthorized, "Your current password is not right.")
)

// Notice is the answer of requests that only report what they did.
type Notice struct {
	Message string `json:"message"`
}

// ResetCodeAnswer is what POST /api/resetcode answers.
type ResetCodeAnswer struct {
	Name    string    `json:"name"`
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

type resetCodeRequest struct {
	Name string `json:"name"`
}

// makesResetCodes reports whether a makes reset codes for others: admins,
// and in edu mode the game masters of a world.
func (s *Service) makesResetCodes(ctx context.Context, a *store.Account) (bool, error) {
	if a.Role == store.RoleAdmin {
		return true, nil
	}
	if !s.cfg.Edu {
		return false, nil
	}
	return s.cfg.Store.IsGM(ctx, a.ID)
}

func (s *Service) makeResetCode(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q resetCodeRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	ok, err := s.makesResetCodes(r.Context(), &a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fail(http.StatusForbidden, "Only admins, and teachers on a school server, make reset codes.")
	}
	admin := a.Role == store.RoleAdmin
	nobody := errNotYours // a game master learns nothing of accounts outside their worlds
	if admin {
		nobody = errNoAccount
	}
	if q.Name == "" || len(q.Name) > maxName {
		return nil, nobody
	}
	who, _, err := s.cfg.Store.AccountByName(r.Context(), q.Name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nobody
	}
	if err != nil {
		return nil, err
	}
	if !admin {
		if err := s.mayResetFor(r.Context(), &a, &who); err != nil {
			return nil, err
		}
	}
	if !s.codesAcc.allow(a.Name) {
		return nil, errSlowDown
	}
	code, expires, err := CreateResetCode(r.Context(), s.cfg.Store, who.ID, a.ID, s.now())
	if err != nil {
		return nil, err
	}
	s.cfg.Log.Info("reset code made", "account", a.ID, "for", who.ID)
	return ResetCodeAnswer{Name: who.Name, Code: code, Expires: expires}, nil
}

// CreateResetCode makes a one-time reset code for account, made by the
// account by (0: the command line), in place of its last one. It returns
// the code, shown once: the store keeps only its hash.
func CreateResetCode(ctx context.Context, st store.Tokens, account, by int64, now time.Time) (string, time.Time, error) {
	code := randomCode(codeLen)
	expires := now.Add(ResetCodeTTL).Truncate(time.Second)
	t := store.Token{Hash: tokenCodeHash(code), Account: account, Kind: store.TokenCode, CreatedBy: by, Expires: expires}
	if err := st.CreateToken(ctx, t, nil, now); err != nil {
		return "", time.Time{}, err
	}
	return groupCode(code), expires, nil
}

// mayResetFor refuses a game master a reset code for anyone but a plain
// player of one of their worlds: never an admin, never a game master
// (themselves included).
func (s *Service) mayResetFor(ctx context.Context, gm, who *store.Account) error {
	if who.Role != store.RolePlayer {
		return errNotYours
	}
	isGM, err := s.cfg.Store.IsGM(ctx, who.ID)
	if err != nil {
		return err
	}
	if isGM {
		return errNotYours
	}
	theirs, err := s.cfg.Store.GMOf(ctx, gm.ID, who.ID)
	if err != nil {
		return err
	}
	if !theirs {
		return errNotYours
	}
	return nil
}

type resetRequest struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// reset sets a new password with a reset code or a mailed link's code,
// and signs the browser in.
func (s *Service) reset(w http.ResponseWriter, r *http.Request) (any, error) {
	var q resetRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if err := ValidPassword(q.Password); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if !s.guess.allow(s.client(r)) {
		return nil, errSlowDown
	}
	kinds := []string{store.TokenCode}
	code, ok := normalizeSymbols(q.Code, codeLen)
	if !ok {
		kinds = []string{store.TokenReset}
		if code, ok = normalizeSymbols(q.Code, linkCodeLen); !ok {
			return nil, errBadReset
		}
	}
	hash, err := Hash(r.Context(), q.Password) // before the code is used up
	if err != nil {
		return nil, err
	}
	t, a, err := s.cfg.Store.UseToken(r.Context(), tokenCodeHash(code), kinds, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, errBadReset
	}
	if err != nil {
		return nil, err
	}
	if err := s.newPassword(r.Context(), a.ID, hash); err != nil {
		return nil, err
	}
	s.cfg.Log.Info("password reset", "account", a.ID, "code by", t.CreatedBy)
	if err := s.startSession(w, r, a.ID); err != nil {
		return nil, err
	}
	me, err := s.describe(r.Context(), &a)
	me.Notice = "Your password is changed, and you are signed in."
	return me, err
}

// newPassword sets the account's password hash, which ends all its
// sign-ins, and ends its open games.
func (s *Service) newPassword(ctx context.Context, account int64, hash string) error {
	if err := s.cfg.Store.SetPassword(ctx, account, hash); err != nil {
		return err
	}
	s.cfg.SignOut(account, "")
	return nil
}

type passwordRequest struct {
	Password string `json:"password"` // the current one
	New      string `json:"new"`
}

// changePassword lets a signed-in account change its own password, and
// signs it out everywhere else.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) (any, error) {
	a, err := s.account(r.Context(), r)
	if err != nil {
		return nil, err
	}
	var q passwordRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	if err := ValidPassword(q.New); err != nil {
		return nil, fail(http.StatusBadRequest, err.Error())
	}
	if q.Password == "" || len(q.Password) > MaxPassword {
		return nil, errWrongOwnPw
	}
	// The sign-in limits apply: a stolen session cannot guess its way to
	// the password faster than the sign-in form.
	client, name := s.client(r), strings.ToLower(a.Name)
	nameHere := name + " " + client
	if !s.signIn.allow(client) || !s.name.ok(nameHere) || !s.nameAll.ok(name) {
		return nil, errSlowDown
	}
	_, old, err := s.cfg.Store.AccountByName(r.Context(), a.Name)
	if err != nil {
		return nil, err
	}
	ok, err := Verify(r.Context(), old, q.Password)
	if err != nil && !errors.Is(err, errHash) {
		return nil, err
	}
	if !ok {
		s.name.take(nameHere)
		s.nameAll.take(name)
		return nil, errWrongOwnPw
	}
	hash, err := Hash(r.Context(), q.New)
	if err != nil {
		return nil, err
	}
	if err := s.newPassword(r.Context(), a.ID, hash); err != nil {
		return nil, err
	}
	s.cfg.Log.Info("password changed", "account", a.ID)
	if err := s.startSession(w, r, a.ID); err != nil {
		return nil, err
	}
	me, err := s.describe(r.Context(), &a)
	me.Notice = "Your password is changed. Your other devices are signed out."
	return me, err
}

type forgotRequest struct {
	Name string `json:"name"` // a name or a confirmed email address
}

// forgotAnswer is the one answer to a forgotten password, whether or not
// the account exists or has an address, so nobody learns either.
const forgotAnswer = "If that account has a confirmed email address, a mail with a reset link is on its way. The link works for one hour."

// forgotPassword mails a reset link to the account's confirmed address.
func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) (any, error) {
	if !s.cfg.Email {
		return nil, errNoMail
	}
	var q forgotRequest
	if err := decode(r, &q); err != nil {
		return nil, err
	}
	typed := strings.TrimSpace(q.Name)
	if typed == "" || len(typed) > mail.MaxAddress {
		return nil, errBadRequest
	}
	if !s.forgot.allow(clientip.Wide(s.client(r))) {
		return nil, errSlowDown
	}
	answer := Notice{Message: forgotAnswer}
	var a store.Account
	var err error
	if strings.Contains(typed, "@") {
		a, _, err = s.cfg.Store.AccountByEmail(r.Context(), typed)
	} else if len(typed) <= maxName {
		a, _, err = s.cfg.Store.AccountByName(r.Context(), typed)
	} else {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		return answer, nil
	}
	if err != nil {
		return nil, err
	}
	to, err := s.cfg.Store.AccountEmail(r.Context(), a.ID)
	if err != nil {
		return nil, err
	}
	if to == "" {
		return answer, nil
	}
	if err := s.mayHaveEmail(r.Context(), &a); err != nil {
		if isAPI(err) {
			return answer, nil // an edu player's old address gets nothing
		}
		return nil, err
	}
	if !s.mailAcc.allow(nameKey(a.Name)) || !s.mailTo.allow(strings.ToLower(to)) {
		return answer, nil // a stranger cannot fill someone's mailbox
	}
	code := randomCode(linkCodeLen)
	t := store.Token{Hash: tokenCodeHash(code), Account: a.ID, Kind: store.TokenReset, Expires: s.now().Add(ResetLinkTTL)}
	err = s.cfg.Store.CreateToken(r.Context(), t, &store.Mail{To: to, Subject: resetSubject, Body: s.resetBody(a.Name, code)}, s.now())
	if errors.Is(err, store.ErrOutboxFull) {
		s.cfg.Log.Warn("reset link not queued: the outbox is full", "account", a.ID)
		return answer, nil
	}
	if err != nil {
		return nil, err
	}
	s.cfg.Kick()
	s.cfg.Log.Info("reset link queued", "account", a.ID)
	return answer, nil
}

// randomCode is n random symbols of codeAlphabet.
func randomCode(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(err) // only crypto/rand can fail here, and then nothing works
	}
	for i, b := range raw {
		raw[i] = codeAlphabet[b%32] // 256 is a multiple of 32: no bias
	}
	return string(raw)
}

// groupCode writes a code in groups of five: 7KQ2M-XR4WD.
func groupCode(code string) string {
	var b strings.Builder
	for i := 0; i < len(code); i += 5 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(code[i:min(i+5, len(code))])
	}
	return b.String()
}

// tokenCodeHash is what the store keeps of a reset or confirmation code.
func tokenCodeHash(code string) []byte {
	h := sha256.Sum256([]byte("talespeak token " + code))
	return h[:]
}

// nameKey is what per-account limits count by.
func nameKey(name string) string { return strings.ToLower(name) }
