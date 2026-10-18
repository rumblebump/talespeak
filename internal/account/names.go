package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/internal/store"
)

// Account names are what other players see: 3 to 20 ASCII letters,
// digits, - and _, starting with a letter. Letter case is kept but does
// not make a name new.
const (
	minName = 3
	maxName = 20
)

// ValidName says what is wrong with name, or returns nil.
func ValidName(name string) error {
	if len(name) < minName || len(name) > maxName {
		return errors.New("Names are 3 to 20 letters long.")
	}
	for i, r := range name {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if i == 0 && !letter {
			return errors.New("Names start with a letter.")
		}
		if !letter && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return errors.New("Names use only letters, digits, - and _.")
		}
	}
	return nil
}

// ValidPassword says what is wrong with password, or returns nil.
func ValidPassword(password string) error {
	if len(password) < MinPassword {
		return errors.New("Passwords are at least 8 characters long.")
	}
	if len(password) > MaxPassword {
		return errors.New("Passwords are at most 128 characters long.")
	}
	return nil
}

// Invite codes are 10 symbols of Crockford's base32 (50 bits), shown as
// two groups of five: easy to read aloud and to type, no I, L, O or U.
const (
	codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	codeLen      = 10
)

// CreateInvite makes an invite to world and returns its code, shown once:
// the store keeps only its hash. now is the time expired invites are
// dropped at.
func CreateInvite(ctx context.Context, st store.Worlds, world, by int64, role string, uses int, now, expires time.Time) (string, time.Time, error) {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	code := make([]byte, codeLen)
	for i, b := range raw {
		code[i] = codeAlphabet[b%32] // 256 is a multiple of 32: no bias
	}
	expires = expires.Truncate(time.Second)
	inv := store.Invite{CodeHash: codeHash(string(code)), WorldID: world, CreatedBy: by, Role: role, Uses: uses, Expires: expires}
	if err := st.CreateInvite(ctx, inv, now); err != nil {
		return "", time.Time{}, err
	}
	return string(code[:5]) + "-" + string(code[5:]), expires, nil
}

// normalizeCode reads a code as typed: any case, with or without spaces
// and dashes, with O for 0 and I or L for 1.
func normalizeCode(s string) (string, bool) {
	if len(s) > 40 {
		return "", false
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case ' ', '-':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(codeAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != codeLen {
		return "", false
	}
	return b.String(), true
}

func codeHash(code string) []byte {
	h := sha256.Sum256([]byte("talespeak invite " + code))
	return h[:]
}
