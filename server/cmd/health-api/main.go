// Command health-api is the Health Assistant backend.
//
//	health-api serve    run the HTTP server
//	health-api migrate  apply pending database migrations
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wanggang316/health-assistant/server/internal/auth"
	"github.com/wanggang316/health-assistant/server/internal/config"
	"github.com/wanggang316/health-assistant/server/internal/httpapi"
	"github.com/wanggang316/health-assistant/server/internal/store"
)

// version is set at build time with -ldflags "-X main.version=<git sha>".
var version = "dev"

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "serve" && os.Args[1] != "migrate") {
		fmt.Fprintln(os.Stderr, "usage: health-api serve|migrate")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := newLogger(cfg.LogLevel)
	if os.Args[1] == "migrate" {
		err = migrate(cfg, log)
	} else {
		err = serve(cfg, log)
	}
	if err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func migrate(cfg config.Config, log *slog.Logger) error {
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()
	applied, err := st.Migrate(context.Background())
	log.Info("migrate", "applied", applied)
	return err
}

func serve(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()
	unknown, err := st.CheckSchema(ctx)
	if err != nil {
		return err
	}
	if len(unknown) > 0 {
		log.Warn("database has migrations from a newer release", "versions", unknown)
	}

	apple, err := auth.NewAppleClient(cfg.AppleTeamID, cfg.AppleBundleID, cfg.AppleSIWAKeyID, cfg.AppleSIWAPrivateKey)
	if err != nil {
		return err
	}
	svc := &auth.Service{
		Store:    st,
		Apple:    auth.NewAppleVerifier(ctx, auth.AppleJWKSURL, cfg.AppleBundleID),
		Google:   auth.NewGoogleVerifier(ctx, auth.GoogleJWKSURL, cfg.GoogleIOSClientID),
		AppleAPI: apple,
		EncKey:   cfg.TokenEncryptionKey,
		Log:      log,
		Now:      time.Now,
	}
	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: httpapi.New(httpapi.Deps{
			Store: st, Auth: svc, Log: log, Version: version, RecognitionQuota: cfg.RecognitionDailyQuota,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.ListenAddr, "version", version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// Wait for running requests, for example recognitions (§10.2).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}
