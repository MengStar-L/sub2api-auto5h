package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/config"
	"github.com/MengStar-L/sub2api-auto5h/internal/httpapi"
	"github.com/MengStar-L/sub2api-auto5h/internal/scheduler"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/webui"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "keygen":
			key, err := secure.GenerateKey()
			if err != nil {
				fatal(err)
			}
			fmt.Printf("%s=%s\n", config.EnvMasterKey, base64.StdEncoding.EncodeToString(key))
			return
		case "version", "--version", "-version":
			fmt.Printf("sub2api-auto5h %s (%s)\n", version, commit)
			return
		case "serve":
		default:
			fatal(fmt.Errorf("unknown command %q; use serve, keygen, or version", os.Args[1]))
		}
	}
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	var box *secure.Box
	if !cfg.SecretsLocked {
		box, err = secure.NewBox(cfg.MasterKey)
		if err != nil {
			return err
		}
	}
	ctx := context.Background()
	data, err := store.Open(ctx, cfg.DBPath, box)
	if err != nil {
		return err
	}
	defer data.Close()
	automation := scheduler.New(data, scheduler.DefaultFactory, logger)
	api, err := httpapi.New(data, automation, logger, cfg.CookieSecure, cfg.SecretsLocked)
	if err != nil {
		return err
	}
	api.MountFrontend(webui.Handler())
	if token := api.SetupToken(); token != "" {
		logger.Warn("first-run setup token", "token", token, "expires_in", "30m")
	}
	if cfg.SecretsLocked {
		logger.Error("master key missing or invalid; readiness and automation are disabled", "environment", config.EnvMasterKey)
	}
	automation.Start()
	defer automation.Stop()

	httpServer := &http.Server{
		Addr: cfg.Listen, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	errorsCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.Listen, "version", version)
		errorsCh <- httpServer.ListenAndServe()
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case received := <-signals:
		logger.Info("shutdown requested", "signal", received.String())
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sub2api-auto5h:", err)
	os.Exit(1)
}
