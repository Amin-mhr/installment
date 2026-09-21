// Package logging builds the application logger: human-readable text on the
// console and JSON lines in a rotating file, so logs outlive the terminal session.
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// FileName is the active log file inside Config.Dir. Rotated files are kept next
// to it, named app-<timestamp>.log (optionally .gz).
const FileName = "app.log"

type Config struct {
	Dir        string // directory for the log files; created if missing
	Level      string // debug, info, warn or error
	MaxSizeMB  int    // rotate once the file reaches this size
	MaxBackups int    // how many rotated files to keep
	MaxAgeDays int    // delete rotated files older than this
	Compress   bool   // gzip rotated files
}

// ParseLevel accepts debug, info, warn or error (any case).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", s)
}

// New returns a logger that writes text to console and JSON lines to
// <cfg.Dir>/app.log, plus a func that closes the file. It fails fast if the log
// directory cannot be created or written, rather than losing logs silently later.
func New(cfg Config, console io.Writer) (*slog.Logger, func(), error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	path := filepath.Join(cfg.Dir, FileName)
	probe, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	probe.Close()

	file := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		Compress:   cfg.Compress,
	}
	opts := &slog.HandlerOptions{Level: level}
	handler := fanout{slog.NewTextHandler(console, opts), slog.NewJSONHandler(file, opts)}
	return slog.New(handler), func() { file.Close() }, nil
}

// fanout sends every record to all of its handlers. (slog has no built-in multi-handler in Go 1.22.)
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
