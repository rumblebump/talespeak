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
		if v != account.RegistrationSelf && v != account.RegistrationOff && v != account.RegistrationSSO {
			errs = append(errs, fmt.Errorf("TALESPEAK_REGISTRATION=%q: want self, off or sso", v))
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

// Class code bounds, in letters and digits: a code must be hard enough to
// guess within the registration limit, and short enough to write on a
// board.
const (
	minClassCode = 6
	maxClassCode = 64
)

// loadClassCode reads TALESPEAK_CLASS_CODE, which registering needs when
// set; only self-registration has a use for it.
func loadClassCode(getenv func(string) string, a access) (string, error) {
	code := getenv("TALESPEAK_CLASS_CODE")
	if code == "" {
		return "", nil
	}
	if a.registration != account.RegistrationSelf {
		return "", fmt.Errorf("TALESPEAK_CLASS_CODE is set, but TALESPEAK_REGISTRATION is %s: the code is only for self-registration", a.registration)
	}
	n := 0
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			n++
		case r == ' ', r == '-':
		default:
			return "", errors.New("TALESPEAK_CLASS_CODE: use only letters, digits, spaces and -")
		}
	}
	if n < minClassCode || n > maxClassCode {
		return "", fmt.Errorf("TALESPEAK_CLASS_CODE: want %d to %d letters and digits", minClassCode, maxClassCode)
	}
	return code, nil
}

// openData opens the database in the data directory, creating both.
func openData(ctx context.Context, dir string) (*sqlite.Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	return sqlite.Open(ctx, filepath.Join(dir, "talespeak.db"))
}

// packs are the packs a server has: any number of versions of each pack
// id. The main world plays the main pack id (the one in TALESPEAK_PACK,
// or the demo).
type packs struct {
	mainID string
	cat    *pack.Catalog
	all    []*pack.Pack // the main pack id's versions first, then the others by id; newest first
}

// main is the version of the main pack new main worlds start on.
func (ps packs) main() *pack.Pack { return ps.cat.Newest(ps.mainID) }

// lobby returns the packs as the lobby shows them: the version new worlds
// start on of each pack id, the main one first.
func (ps packs) lobby() []account.Pack {
	var l []account.Pack
	for _, id := range ps.ids() {
		if p := ps.cat.Newest(id); !p.IsRuleset() { // nobody plays a ruleset as a world
			l = append(l, lobbyPack(p))
		}
	}
	return l
}

// ids are the pack ids, the main one first, then the others sorted.
func (ps packs) ids() []string {
	ids := []string{ps.mainID}
	for _, id := range ps.cat.IDs() {
		if id != ps.mainID {
			ids = append(ids, id)
		}
	}
	return ids
}

func lobbyPack(p *pack.Pack) account.Pack {
	ap := account.Pack{ID: p.ID, Title: p.Title, Description: p.Description, Version: p.Version}
	for _, s := range p.Settings {
		ap.Settings = append(ap.Settings, account.Setting{Name: s.Name, Label: s.Label, Choices: s.Choices, Default: s.Default})
	}
	return ap
}

// installed lists the packs as the store records them.
func (ps packs) installed() []store.InstalledPack {
	var l []store.InstalledPack
	for _, p := range ps.all {
		l = append(l, store.InstalledPack{ID: p.ID, Version: p.Version, Hash: p.Hash})
	}
	return l
}

// keepHome reports whether a main world that recorded version should stay
// the main world: when it runs the version new main worlds start on. One
// on an older major version keeps running as a world of its own while
// that version is installed, and a new main world starts.
func (ps packs) keepHome(recorded string) bool {
	p, err := ps.cat.Pick(ps.mainID, recorded)
	return err == nil && p == ps.main()
}

// openHome returns the main world, starting a new one when the one before
// runs an older major version of the main pack.
func openHome(ctx context.Context, db store.Store, ps packs) (store.World, error) {
	return db.OpenWorld(ctx, ps.mainID, ps.main().Version, ps.keepHome)
}

// maxPacks bounds the pack folders in TALESPEAK_PACKS.
const maxPacks = 100

// loadPacks loads the pack in TALESPEAK_PACK, which names the main
// world's pack (the demo without it), every folder with a pack.yaml in
// TALESPEAK_PACKS, and the built-in demo unless one of those is the same
// version of it. Two packs with one id and version are an error, except
// that the main pack may be in TALESPEAK_PACKS too: the main one is kept.
// Other versions of one pack are all installed (docs/pack-versions.md).
func loadPacks(getenv func(string) string, lim pack.Limits) (packs, error) {
	load := func(name string, fsys fs.FS) (*pack.Pack, error) {
		p, err := pack.LoadLimits(fsys, lim)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return p, nil
	}
	var main *pack.Pack
	var err error
	if dir := getenv("TALESPEAK_PACK"); dir != "" {
		if main, err = load("pack "+dir, os.DirFS(dir)); err != nil {
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
	same := func(a, b *pack.Pack) bool { return a.ID == b.ID && a.Version == b.Version }
	ps := packs{mainID: demo.ID}
	var all []*pack.Pack
	if main != nil {
		ps.mainID = main.ID
		all = append(all, main)
		others = slices.DeleteFunc(others, func(p *pack.Pack) bool { return same(p, main) })
	}
	all = append(all, others...)
	if !slices.ContainsFunc(all, func(p *pack.Pack) bool { return same(p, demo) }) {
		all = append(all, demo)
	}
	if ps.cat, err = pack.NewCatalog(all...); err != nil {
		return packs{}, fmt.Errorf("TALESPEAK_PACKS: %w", err)
	}
	if err := ps.cat.Check(); err != nil {
		return packs{}, fmt.Errorf("TALESPEAK_PACKS: %w", err)
	}
	if ps.main().IsRuleset() {
		return packs{}, fmt.Errorf("TALESPEAK_PACK: %s is a ruleset pack; the main world plays a story pack", ps.main().Name())
	}
	for _, id := range ps.ids() {
		ps.all = append(ps.all, ps.cat.Versions(id)...)
	}
	return ps, nil
}

// accountCmd runs "talespeak account add|password|role|resetcode".
func accountCmd(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("account: want add, password, role or resetcode")
	}
	sub, args := args[0], args[1:]
	fl := flag.NewFlagSet("account "+sub, flag.ContinueOnError)
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	admin := fl.Bool("admin", false, "add: make the account an admin")
	if err := fl.Parse(args); err != nil {
		return err
	}
	want := map[string]int{"add": 1, "password": 1, "role": 2, "resetcode": 1}[sub]
	if want == 0 {
		return fmt.Errorf("account: unknown command %q (want add, password, role or resetcode)", sub)
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
	case "resetcode":
		a, _, err := db.AccountByName(ctx, name)
		if err != nil {
			return fmt.Errorf("account resetcode %q: %w", name, err)
		}
		code, expires, err := account.CreateResetCode(ctx, db, a.ID, 0, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s  (reset code of %s, works once, until %s)\n", code, a.Name, expires.UTC().Format(time.RFC3339))
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
		if w, err = openHome(ctx, db, ps); err != nil {
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
