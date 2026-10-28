// Package logging provides the structured logger used across the bot.
// Output is human-readable, single-line, "[scope] message key=value …" — the
// same shape the pino-pretty pipeline produced in the TypeScript version, so
// `docker logs` and the Home Assistant add-on log page stay easy to scan.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Level resolves the slog level from a LOG_LEVEL string (default info).
func Level(name string) slog.Level {
	switch strings.ToLower(name) {
	case "trace", "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a logger tagged with its scope, honoring LOG_LEVEL_OVERRIDE
// (tests) over level.
func New(scope string, level string) *slog.Logger {
	name := os.Getenv("LOG_LEVEL_OVERRIDE")
	if name == "" {
		name = level
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: Level(name),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Compact defaults: drop the source-free time duplication and
			// render the level in caps like pino-pretty did.
			if a.Key == slog.LevelKey {
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					return slog.String(slog.LevelKey, strings.ToUpper(lvl.String()))
				}
			}
			return a
		},
	})
	return slog.New(handler).With("scope", scope)
}
