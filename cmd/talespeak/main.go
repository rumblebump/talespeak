// Command talespeak runs the Talespeak engine.
//
//	talespeak serve        start the server (TALESPEAK_LISTEN, TALESPEAK_DATA)
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rumblebump/talespeak/internal/server"
	"github.com/rumblebump/talespeak/internal/store/sqlite"
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
	case "healthcheck":
		return healthcheck(healthURL(envOr("TALESPEAK_LISTEN", ":8080")))
	case "version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want serve, healthcheck or version)", cmd)
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

	if err := os.MkdirAll(*data, 0o750); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	db, err := sqlite.Open(ctx, filepath.Join(*data, "talespeak.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	log := slog.Default()
	sockets := ws.New(version, log)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(server.Options{Version: version, Store: db, WebSocket: sockets}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second, // the WebSocket handler clears both for its connections
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("talespeak started", "version", version, "listen", *listen, "data", *data)

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
