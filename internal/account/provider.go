package account

import (
	"context"
	"errors"
	"strings"

	"github.com/rumblebump/talespeak/internal/store"
)

// Sign-in providers: who checks that a person is who they say. Every
// provider ends in the same sign-in session (startSession), behind the
// same rate limits, and an account it signs in gets no more access than
// any other: edu mode and world membership decide what it may play.

// Provider checks a sign-in with a name and a password typed into
// Talespeak's own form. The local provider is the only one so far.
//
// OIDC and SAML (later tasks) send the browser to an identity provider
// and come back with an identity instead of a password. They will add
// their own start and callback routes, match the identity to an account
// (or make one when registration is "sso"), and end in startSession like
// this one; this interface stays the one the sign-in form uses.
type Provider interface {
	// ID names the provider in settings and logs, such as "local".
	ID() string
	// SignIn returns the account that name and password sign in, or
	// ErrWrongLogin. It takes about as long for an account that does not
	// exist as for a wrong password.
	SignIn(ctx context.Context, name, password string) (store.Account, error)
}

// ErrWrongLogin is a name or password a provider does not know.
var ErrWrongLogin = errors.New("account: wrong name or password")

// LocalStore is what the local provider reads.
type LocalStore interface {
	AccountByName(ctx context.Context, name string) (store.Account, string, error)
	AccountByEmail(ctx context.Context, email string) (store.Account, string, error)
}

// Local is the provider of the server's own accounts: a name, or an
// account's confirmed email address, and a password with an argon2id hash
// in the store.
func Local(st LocalStore) Provider { return local{st} }

type local struct{ st LocalStore }

func (local) ID() string { return "local" }

func (l local) SignIn(ctx context.Context, name, password string) (store.Account, error) {
	var a store.Account
	var hash string
	var err error
	if strings.Contains(name, "@") { // names have no @: it is an email address
		a, hash, err = l.st.AccountByEmail(ctx, name)
	} else {
		a, hash, err = l.st.AccountByName(ctx, name)
	}
	missing := errors.Is(err, store.ErrNotFound)
	if err != nil && !missing {
		return store.Account{}, err
	}
	if missing {
		hash = dummyHash()
	}
	ok, err := Verify(ctx, hash, password)
	if err != nil && !errors.Is(err, errHash) {
		return store.Account{}, err
	}
	if missing || !ok {
		return store.Account{}, ErrWrongLogin
	}
	return a, nil
}
