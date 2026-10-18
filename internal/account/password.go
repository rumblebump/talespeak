// Package account is sign-in for local accounts: password hashes, sign-in
// sessions in a cookie, invites to a world, and the small JSON API the web
// client uses (docs/accounts.md). Other sign-in providers (OIDC, SAML)
// come later and end in the same sessions.
package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters, as OWASP recommends for argon2id at the time of
// writing: 19 MiB, two passes, one lane.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// MinPassword and MaxPassword bound a password in bytes. The upper bound
// keeps a hash's input small; argon2's cost does not grow with it, but a
// megabyte password is no password.
const (
	MinPassword = 8
	MaxPassword = 128
)

// maxHashing is how many hashes run at once. Each takes argonMemory, so a
// burst of sign-ins cannot take the server's memory; the others wait.
const maxHashing = 2

// maxWaiting is how many more may wait for a slot; past it a sign-in is
// refused at once (503), so a flood cannot keep every slot and queue busy
// for the length of a request.
const maxWaiting = 8

var (
	hashing = make(chan struct{}, maxHashing)
	waiting atomic.Int32
)

// errBusy is a hash that could not start before its request ended.
var errBusy = errors.New("account: too many sign-ins at once")

// withHashSlot runs f once a hashing slot is free, or returns errBusy when
// ctx ends first.
func withHashSlot(ctx context.Context, f func()) error {
	if waiting.Add(1) > maxHashing+maxWaiting {
		waiting.Add(-1)
		return errBusy
	}
	defer waiting.Add(-1)
	select {
	case hashing <- struct{}{}:
	case <-ctx.Done():
		return errBusy
	}
	defer func() { <-hashing }()
	f()
	return nil
}

var b64 = base64.RawStdEncoding

// Hash returns the PHC string of an argon2id hash of password with a new
// random salt: $argon2id$v=19$m=19456,t=2,p=1$salt$key.
func Hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	var key []byte
	err := withHashSlot(ctx, func() {
		key = argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether password matches hash, a string from Hash. Hashes
// with other parameters are verified with theirs, within bounds, so the
// parameters can be raised later without locking anyone out.
func Verify(ctx context.Context, hash, password string) (bool, error) {
	p, err := parseHash(hash)
	if err != nil {
		return false, err
	}
	var key []byte
	err = withHashSlot(ctx, func() {
		key = argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	})
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

type params struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

var errHash = errors.New("account: not an argon2id hash")

// parseHash reads a PHC argon2id string. Parameters far above the
// defaults are refused, so a hash put into the database by hand cannot
// make a sign-in take a gigabyte.
func parseHash(hash string) (params, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return params{}, errHash
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return params{}, errHash
	}
	if p.memory < 8 || p.memory > 256*1024 || p.time < 1 || p.time > 10 || p.threads < 1 || p.threads > 4 {
		return params{}, errHash
	}
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil || len(p.salt) < 8 || len(p.salt) > 64 {
		return params{}, errHash
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > 64 {
		return params{}, errHash
	}
	return p, nil
}

// dummyHash is verified when an account does not exist, so a sign-in takes
// as long for a name that is not taken as for one that is.
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash(context.Background(), "not a password anyone has")
	if err != nil {
		panic(err) // only crypto/rand can fail here, and then nothing works
	}
	return h
})
