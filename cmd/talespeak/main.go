// Command talespeak runs the Talespeak engine.
//
//	talespeak serve        start the server (TALESPEAK_LISTEN, TALESPEAK_DATA, TALESPEAK_PACK, TALESPEAK_PACKS;
//	                       sign-in: docs/accounts.md; limits: docs/admin-limits.md)
//	talespeak account add [-admin] NAME   add an account; the password is read from standard input
//	talespeak account password NAME       change an account's password (standard input)
//	talespeak account role NAME ROLE      make an account an admin or a player
//	talespeak account resetcode NAME      print a one-time reset code for an account
//	talespeak invite [-uses N] [-days D] [-gm] [-world ID]  print a new invite code to a world
//	talespeak mail test ADDRESS           send a test mail with the SMTP settings (docs/admin-email.md)
//	talespeak healthcheck  exit 0 if the local server reports healthy (used by Docker)
//	talespeak version      print the version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rumblebump/talespeak/internal/account"
	"github.com/rumblebump/talespeak/internal/clientip"
	"github.com/rumblebump/talespeak/internal/game"
	"github.com/rumblebump/talespeak/internal/hub"
	"github.com/rumblebump/talespeak/internal/mail"
	"github.com/rumblebump/talespeak/internal/pack"
	"github.com/rumblebump/talespeak/internal/server"
	"github.com/rumblebump/talespeak/internal/store"
	"github.com/rumblebump/talespeak/internal/ws"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // usage was already printed
		}
		fmt.Fprintln(os.Stderr, "talespeak:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args)
	case "account":
		return accountCmd(args, os.Stdin, os.Stdout)
	case "invite":
		return inviteCmd(args, os.Stdout)
	case "mail":
		return mailCmd(args, os.Getenv, os.Stdout)
	case "healthcheck":
		return healthcheck(healthURL(envOr("TALESPEAK_LISTEN", ":8080")))
	case "version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want serve, account, invite, mail, healthcheck or version)", cmd)
	}
}

func serve(args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fl.String("listen", envOr("TALESPEAK_LISTEN", ":8080"), "address to listen on")
	data := fl.String("data", envOr("TALESPEAK_DATA", "./data"), "data directory")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("serve: unexpected arguments %q", fl.Args())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openData(ctx, *data)
	if err != nil {
		return err
	}
	defer db.Close()

	proxies, err := clientip.New(envOr("TALESPEAK_TRUSTED_PROXIES", clientip.DefaultTrusted))
	if err != nil {
		return err
	}
	hosts, err := server.ParseHosts(os.Getenv("TALESPEAK_HOSTS"))
	if err != nil {
		return fmt.Errorf("TALESPEAK_HOSTS: %w", err)
	}
	lim, err := loadLimits(os.Getenv)
	if err != nil {
		return fmt.Errorf("limits: %w", err)
	}
	acc, err := loadAccess(os.Getenv)
	if err != nil {
		return err
	}
	classCode, err := loadClassCode(os.Getenv, acc)
	if err != nil {
		return err
	}
	mailSet, err := loadMail(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.Default()
	ps, err := loadPacks(os.Getenv, lim.pack)
	if err != nil {
		return err
	}
	changed, err := db.RecordPacks(ctx, ps.installed())
	if err != nil {
		return fmt.Errorf("installed packs: %w", err)
	}
	for _, p := range changed {
		log.Warn("pack files changed without a new version: worlds run the new files, give each change its own version",
			"pack", p.ID, "version", p.Version)
	}
	home, err := openHome(ctx, db, ps)
	if err == nil {
		err = db.SetVisibility(ctx, home.ID, acc.visibility())
	}
	if err != nil {
		return fmt.Errorf("main world of %s: %w", ps.mainID, err)
	}
	worlds := hub.New(hub.Config{Store: db, Packs: ps.cat, Home: home.ID, MaxLoaded: lim.loadedWorlds, Idle: lim.worldIdle,
		PerClient: lim.worldsPerClient, Log: log,
		Load: func(w store.World, p *pack.Pack) (*game.World, error) {
			return game.NewWorld(p, lim.script, log.With("world", w.ID), game.WithClientBudget(lim.luaClientRate, lim.luaClientBurst),
				game.WithClientMemory(lim.luaClientMemory), game.WithStore(db, w.ID), game.WithAudit(db))
		}})
	defer worlds.Close()
	go worlds.Run(ctx)
	var outbox *mail.Outbox // nil with mail off: Kick does nothing
	if mailSet.email {
		outbox = mail.NewOutbox(db, func(ctx context.Context, m mail.Message) error { return mail.Send(ctx, mailSet.smtp, m) }, nil, log)
		go outbox.Run(ctx)
	}
	var accounts *account.Service // the sockets ask it who connects; it tells them who signed out
	sockets := ws.New(version, log, ws.WithClientIP(proxies.IP), ws.WithGame(worlds),
		ws.WithAuth(func(r *http.Request) (ws.Who, int) { return accounts.Auth(r) }), ws.WithRecheck(lim.signInRecheck),
		ws.WithMaxConns(lim.maxConns), ws.WithMaxConnsPerClient(lim.maxConnsPerClient),
		ws.WithConnectRate(lim.connectRate, lim.connectBurst),
		ws.WithRateLimit(lim.messageRate, lim.messageBurst), ws.WithIdleTimeout(lim.idleTimeout))
	accounts = account.New(account.Config{Store: db, Registration: acc.registration, ClassCode: classCode, Edu: acc.mode == modeEdu,
		Create: acc.create, Home: home.ID, Packs: ps.lobby(), Live: worlds, Runs: func(w store.World) (account.Pack, error) {
			p, err := worlds.Pack(w)
			if err != nil {
				return account.Pack{}, err
			}
			return lobbyPack(p), nil
		}, WorldsPerAccount: lim.worldsPerAccount, MaxWorlds: lim.maxWorlds,
		Email: mailSet.email, PublicURL: mailSet.publicURL, Kick: outbox.Kick,
		SignOut:  func(id int64, login string) { sockets.SignOut(id, login) },
		ClientIP: proxies.IP, HTTPS: proxies.HTTPS, AddressBurst: lim.signInBurst, Log: log})
	srv := &http.Server{
		Addr: *listen,
		Handler: server.New(server.Options{Version: version, Store: db, WebSocket: sockets, Assets: pack.AssetHandler(ps.all...),
			API: accounts.Handler(), Hosts: hosts}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second, // the WebSocket handler clears both for its connections
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("talespeak started", "version", version, "listen", *listen, "data", *data, "pack", ps.main().Name(), "packs", len(ps.all),
		"world", home.ID, "mode", acc.mode, "registration", acc.registration, "access", acc.world, "create", acc.create,
		"class code", classCode != "", "email", mailSet.email)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// WebSockets are hijacked connections that http.Server.Shutdown does not close.
	if err := sockets.Shutdown(shutdownCtx); err != nil {
		log.Warn("websockets did not close in time", "err", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthURL turns a listen address like ":8080" or "0.0.0.0:8080" into a local URL.
func healthURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = strings.TrimPrefix(listen, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

func healthcheck(url string) error {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var h server.Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return fmt.Errorf("health: %w", err)
	}
	if resp.StatusCode != http.StatusOK || h.Status != "ok" {
		return fmt.Errorf("health: status %d, %q", resp.StatusCode, h.Status)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
