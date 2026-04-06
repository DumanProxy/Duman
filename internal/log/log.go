package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config holds logging configuration.
type Config struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json", "text"
	Output string // "stderr", "stdout", or file path
}

// New creates a configured slog.Logger.
// WARNING: debug level logs query content — never use in production.
func New(cfg Config) *slog.Logger {
	level := parseLevel(cfg.Level)
	writer := parseOutput(cfg.Output)

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	return slog.New(handler)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func parseOutput(s string) io.Writer {
	switch strings.ToLower(s) {
	case "stdout":
		return os.Stdout
	case "", "stderr":
		return os.Stderr
	default:
		f, err := os.OpenFile(s, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return os.Stderr
		}
		return f
	}
}
