package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rumblebump/talespeak/content"
	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
)

// Server modes (docs/accounts.md).
const (
	modePublic = "public"
	modeEdu    = "edu"
)

// access is who may register and play, from TALESPEAK_MODE,
// TALESPEAK_REGISTRATION and TALESPEAK_WORLD_ACCESS.
type access struct {
	mode, registration, world string
}

// loadAccess reads the sign-in settings. Edu mode defaults to admin-made
// accounts and always keeps worlds invite only: registering never lets a
// child into a world.
func loadAccess(getenv func(string) string) (access, error) {
	a := access{mode: getenv("TALESPEAK_MODE")}
	if a.mode == "" {
		a.mode = modePublic
	}
	if a.mode != modePublic && a.mode != modeEdu {
		return access{}, fmt.Errorf("TALESPEAK_MODE=%q: want public or edu", a.mode)
	}
	a.registration, a.world = account.RegistrationSelf, account.AccessOpen
	if a.mode == modeEdu {
		a.registration, a.world = account.RegistrationOff, account.AccessInvite
	}
	var errs []error
	if v := getenv("TALESPEAK_REGISTRATION"); v != "" {
		if v != account.RegistrationSelf && v != account.RegistrationOff {
			errs = append(errs, fmt.Errorf("TALESPEAK_REGISTRATION=%q: want self or off", v))
		}
		a.registration = v
	}
	if v := getenv("TALESPEAK_WORLD_ACCESS"); v != "" {
		switch {
		case v != account.AccessOpen && v != account.AccessInvite:
			errs = append(errs, fmt.Errorf("TALESPEAK_WORLD_ACCESS=%q: want open or invite", v))
		case a.mode == modeEdu && v == account.AccessOpen:
			errs = append(errs, errors.New("TALESPEAK_WORLD_ACCESS=open: edu worlds are always invite only"))
		}
		a.world = v
	}
	return a, errors.Join(errs...)
}

// openData opens the database in the data directory, creating both.
func openData(ctx context.Context, dir string) (*sqlite.Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	return sqlite.Open(ctx, filepath.Join(dir, "talespeak.db"))
}

// loadPack loads the pack the server runs: the demo, or the folder in
// TALESPEAK_PACK. Until packs can be installed (T-0010), a server runs
// one.
func loadPack(lim pack.Limits) (*pack.Pack, string, error) {
	name, fsys := "demo pack", content.Demo()
	if dir := os.Getenv("TALESPEAK_PACK"); dir != "" {
		name, fsys = "pack "+dir, os.DirFS(dir)
	}
	p, err := pack.LoadLimits(fsys, lim)
	if err != nil {
		return nil, name, fmt.Errorf("%s: %w", name, err)
	}
	return p, name, nil
}

// packVersion is recorded with each world; packs get versions with
// T-0010.
const packVersion = ""

// accountCmd runs "talespeak account add|password|role".
func accountCmd(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("account: want add, password or role")
	}
	sub, args := args[0], args[1:]
	fl := flag.NewFlagSet("account "+sub, flag.ContinueOnError)
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	admin := fl.Bool("admin", false, "add: make the account an admin")
	if err := fl.Parse(args); err != nil {
		return err
	}
	want := map[string]int{"add": 1, "password": 1, "role": 2}[sub]
	if want == 0 {
		return fmt.Errorf("account: unknown command %q (want add, password or role)", sub)
	}
	if fl.NArg() != want {
		return fmt.Errorf("account %s: want %d argument(s), got %q", sub, want, fl.Args())
	}
	name := fl.Arg(0)
	ctx := context.Background()
	db, err := openData(ctx, *data)
	if err != nil {
		return err
	}
	defer db.Close()

	switch sub {
	case "add":
		if err := account.ValidName(name); err != nil {
			return fmt.Errorf("account add: %w", err)
		}
		hash, err := readPassword(ctx, stdin)
		if err != nil {
			return err
		}
		role := store.RolePlayer
		if *admin {
			role = store.RoleAdmin
		}
		a, err := db.CreateAccount(ctx, name, hash, role)
		if errors.Is(err, store.ErrNameTaken) {
			return fmt.Errorf("account add: %q is taken", name)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "added %s (%s)\n", a.Name, a.Role)
	case "password":
		a, _, err := db.AccountByName(ctx, name)
		if err != nil {
			return fmt.Errorf("account password %q: %w", name, err)
		}
		hash, err := readPassword(ctx, stdin)
		if err != nil {
			return err
		}
		if err := db.SetPassword(ctx, a.ID, hash); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "changed the password of %s and signed it out everywhere\n", a.Name)
	case "role":
		role := fl.Arg(1)
		if role != store.RoleAdmin && role != store.RolePlayer {
			return fmt.Errorf("account role: want admin or player, got %q", role)
		}
		a, _, err := db.AccountByName(ctx, name)
		if err != nil {
			return fmt.Errorf("account role %q: %w", name, err)
		}
		if err := db.SetRole(ctx, a.ID, role); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s is now %s\n", a.Name, role)
	}
	return nil
}

// readPassword reads one line from stdin and hashes it. A password is
// never a command-line argument, which other users of the machine can see.
func readPassword(ctx context.Context, stdin io.Reader) (string, error) {
	fmt.Fprintln(os.Stderr, "password (one line on standard input):")
	line, err := bufio.NewReader(io.LimitReader(stdin, account.MaxPassword+3)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	password := strings.TrimRight(line, "\r\n")
	if err := account.ValidPassword(password); err != nil {
		return "", err
	}
	return account.Hash(ctx, password)
}

// inviteCmd runs "talespeak invite": it prints a new invite code to the
// world the server runs.
func inviteCmd(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("invite", flag.ContinueOnError)
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	uses := fl.Int("uses", 1, "how many accounts may use the code")
	days := fl.Int("days", 7, "days until the code expires")
	gm := fl.Bool("gm", false, "make who uses it a game master of the world")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("invite: unexpected arguments %q", fl.Args())
	}
	if *uses < 1 || *uses > account.MaxInviteUses || *days < 1 || *days > account.MaxInviteDays {
		return fmt.Errorf("invite: want -uses from 1 to %d and -days from 1 to %d", account.MaxInviteUses, account.MaxInviteDays)
	}
	lim, err := loadLimits(os.Getenv)
	if err != nil {
		return fmt.Errorf("limits: %w", err)
	}
	p, _, err := loadPack(lim.pack)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := openData(ctx, *data)
	if err != nil {
		return err
	}
	defer db.Close()
	w, err := db.OpenWorld(ctx, p.ID, packVersion)
	if err != nil {
		return err
	}
	role := store.MemberPlayer
	if *gm {
		role = store.MemberGM
	}
	code, expires, err := account.CreateInvite(ctx, db, w.ID, 0, role, *uses, time.Now(), time.Now().Add(time.Duration(*days)*24*time.Hour))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s  (%s of %s, %d use(s), expires %s)\n", code, role, p.ID, *uses, expires.UTC().Format(time.RFC3339))
	return nil
}
