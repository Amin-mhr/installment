package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"interview/internal/app"
	"interview/internal/logging"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet (bad config), so failures always reach stderr too.
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	gin.SetMode(cfg.GinMode)

	a, cleanup, err := app.Initialize(cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	slog.SetDefault(a.Log)

	errCh := make(chan error, 1)
	go func() { errCh <- a.Server.ListenAndServe() }()
	a.Log.Info("listening", "addr", cfg.Addr, "log_file", filepath.Join(cfg.Log.Dir, logging.FileName))

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		a.Log.Error("server stopped unexpectedly", "err", err)
		return err
	case <-ctx.Done():
		a.Log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Server.Shutdown(shutdownCtx); err != nil {
			a.Log.Error("shutdown failed", "err", err)
			return err
		}
		a.Log.Info("stopped")
		return nil
	}
}
