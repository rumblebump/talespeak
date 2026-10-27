package account

import (
	"context"
	"testing"

	"github.com/rumblebump/talespeak/internal/store"
)

// fakeProvider signs in one name and password.
type fakeProvider struct{ name, password string }

func (fakeProvider) ID() string { return "fake" }

func (p fakeProvider) SignIn(_ context.Context, name, password string) (store.Account, error) {
	if name == p.name && password == p.password {
		return store.Account{ID: 99, Name: name}, nil
	}
	return store.Account{}, ErrWrongLogin
}

// Providers are asked in turn: the first that knows the sign-in wins.
func TestProviders(t *testing.T) {
	f := newFixture(t, Config{})
	f.player("mira")
	f.svc.cfg.Providers = []Provider{Local(f.st), fakeProvider{"sso-user", "from elsewhere"}}
	ctx := context.Background()
	if a, err := f.svc.signInWith(ctx, "mira", "correct horse"); err != nil || a.Name != "mira" {
		t.Errorf("local = %+v, %v", a, err)
	}
	if a, err := f.svc.signInWith(ctx, "sso-user", "from elsewhere"); err != nil || a.ID != 99 {
		t.Errorf("second provider = %+v, %v", a, err)
	}
	if _, err := f.svc.signInWith(ctx, "mira", "from elsewhere"); err != ErrWrongLogin {
		t.Errorf("nobody's = %v", err)
	}
	if id := Local(f.st).ID(); id != "local" {
		t.Errorf("Local().ID() = %q", id)
	}
}
