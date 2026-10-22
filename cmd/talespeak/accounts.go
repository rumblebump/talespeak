package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

// access is who may register, play the main world and create worlds,
// from TALESPEAK_MODE, TALESPEAK_REGISTRATION, TALESPEAK_WORLD_ACCESS and
// TALESPEAK_CREATE_WORLDS.
type access struct {
	mode, registration, world, create string
}

// visibility is the main world's visibility for a.world.
func (a access) visibility() string {
	if a.world == account.AccessOpen {
		return store.VisibilityPublic
	}
	return store.VisibilityPrivate
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
	a.registration, a.world, a.create = account.RegistrationSelf, account.AccessOpen, account.CreateEveryone
	if a.mode == modeEdu {
		a.registration, a.world, a.create = account.RegistrationOff, account.AccessInvite, account.CreateGM
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
	if a.world == account.AccessInvite {
		a.create = account.CreateGM // a server kept closed stays closed
	}
	if v := getenv("TALESPEAK_CREATE_WORLDS"); v != "" {
		switch {
		case v != account.CreateEveryone && v != account.CreateGM:
			errs = append(errs, fmt.Errorf("TALESPEAK_CREATE_WORLDS=%q: want everyone or gm", v))
		case a.mode == modeEdu && v == account.CreateEveryone:
			errs = append(errs, errors.New("TALESPEAK_CREATE_WORLDS=everyone: in edu mode only admins and game masters create worlds"))
		}
		a.create = v
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

// packs are the packs a server has: the main world's (the demo, or the
// folder in TALESPEAK_PACK) and every pack worlds can be created from.
type packs struct {
	main *pack.Pack
	all  []*pack.Pack // main first, then the others by id
}

// byID returns the packs by id.
func (ps packs) byID() map[string]*pack.Pack {
	m := map[string]*pack.Pack{}
	for _, p := range ps.all {
		m[p.ID] = p
	}
	return m
}

// lobby returns the packs as the lobby shows them.
func (ps packs) lobby() []account.Pack {
	var l []account.Pack
	for _, p := range ps.all {
		l = append(l, account.Pack{ID: p.ID, Title: p.Title, Description: p.Description, Version: packVersion})
	}
	return l
}

// maxPacks bounds the pack folders in TALESPEAK_PACKS.
const maxPacks = 100

// loadPacks loads the pack in TALESPEAK_PACK, which runs the main world
// (the demo without it), every folder with a pack.yaml in TALESPEAK_PACKS,
// and the built-in demo unless one of those has its id. Two packs with one
// id are an error, except that the main pack may be in TALESPEAK_PACKS
// too: the main one is kept.
func loadPacks(getenv func(string) string, lim pack.Limits) (packs, error) {
	load := func(name string, fsys fs.FS) (*pack.Pack, error) {
		p, err := pack.LoadLimits(fsys, lim)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return p, nil
	}
	var ps packs
	var err error
	if dir := getenv("TALESPEAK_PACK"); dir != "" {
		if ps.main, err = load("pack "+dir, os.DirFS(dir)); err != nil {
			return packs{}, err
		}
	}
	var others []*pack.Pack
	if dir := getenv("TALESPEAK_PACKS"); dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return packs{}, fmt.Errorf("TALESPEAK_PACKS: %w", err)
		}
		var errs []error
		for _, e := range entries {
			sub := filepath.Join(dir, e.Name())
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if _, err := os.Stat(filepath.Join(sub, "pack.yaml")); err != nil {
				continue // not a pack
			}
			if len(others) >= maxPacks {
				return packs{}, fmt.Errorf("TALESPEAK_PACKS: more than %d packs", maxPacks)
			}
			p, err := load("pack "+sub, os.DirFS(sub))
			if err != nil {
				errs = append(errs, err)
				continue
			}
			others = append(others, p)
		}
		if err := errors.Join(errs...); err != nil {
			return packs{}, err
		}
	}
	demo, err := load("demo pack", content.Demo())
	if err != nil {
		return packs{}, err
	}
	if ps.main == nil {
		// The demo runs the main world, from TALESPEAK_PACKS when it is
		// there too.
		ps.main = demo
		if i := slices.IndexFunc(others, func(p *pack.Pack) bool { return p.ID == demo.ID }); i >= 0 {
			ps.main = others[i]
		}
	}
	others = slices.DeleteFunc(others, func(p *pack.Pack) bool { return p.ID == ps.main.ID })
	if ps.main.ID != demo.ID && !slices.ContainsFunc(others, func(p *pack.Pack) bool { return p.ID == demo.ID }) {
		others = append(others, demo)
	}
	slices.SortFunc(others, func(a, b *pack.Pack) int { return strings.Compare(a.ID, b.ID) })
	ps.all = append([]*pack.Pack{ps.main}, others...)
	for i := 1; i < len(ps.all); i++ {
		if ps.all[i].ID == ps.all[i-1].ID {
			return packs{}, fmt.Errorf("TALESPEAK_PACKS: two packs have the id %q", ps.all[i].ID)
		}
	}
	return ps, nil
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

// inviteCmd runs "talespeak invite": it prints a new invite code to a
// world, the main one when -world is not given.
func inviteCmd(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("invite", flag.ContinueOnError)
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	uses := fl.Int("uses", 1, "how many accounts may use the code")
	days := fl.Int("days", 7, "days until the code expires")
	gm := fl.Bool("gm", false, "make who uses it a game master of the world")
	worldID := fl.Int64("world", 0, "the world's id (from the lobby); 0 is the server's main world")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("invite: unexpected arguments %q", fl.Args())
	}
	if *uses < 1 || *uses > account.MaxInviteUses || *days < 1 || *days > account.MaxInviteDays || *worldID < 0 {
		return fmt.Errorf("invite: want -uses from 1 to %d and -days from 1 to %d", account.MaxInviteUses, account.MaxInviteDays)
	}
	ctx := context.Background()
	db, err := openData(ctx, *data)
	if err != nil {
		return err
	}
	defer db.Close()
	var w store.World
	if *worldID == 0 {
		lim, err := loadLimits(os.Getenv)
		if err != nil {
			return fmt.Errorf("limits: %w", err)
		}
		ps, err := loadPacks(os.Getenv, lim.pack)
		if err != nil {
			return err
		}
		if w, err = db.OpenWorld(ctx, ps.main.ID, packVersion); err != nil {
			return err
		}
	} else {
		w, err = db.World(ctx, *worldID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && w.Finished) {
			return fmt.Errorf("invite: world %d does not exist or has ended", *worldID)
		}
		if err != nil {
			return err
		}
	}
	role := store.MemberPlayer
	if *gm {
		role = store.MemberGM
	}
	code, expires, err := account.CreateInvite(ctx, db, w.ID, 0, role, *uses, time.Now(), time.Now().Add(time.Duration(*days)*24*time.Hour))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s  (%s of world %d, %s, %d use(s), expires %s)\n", code, role, w.ID, w.PackID, *uses, expires.UTC().Format(time.RFC3339))
	return nil
}
