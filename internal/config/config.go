// Package config loads the bot configuration from environment variables,
// mirroring the names and defaults of the original TypeScript implementation.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds every tunable of the bot. See .env.example / README.
type Config struct {
	DiscordToken           string
	DBPath                 string
	DefaultIntervalMinutes int
	MinIntervalMinutes     int
	SendDelayMs            int
	LogLevel               string
	// BackupHour is nil when the daily auto-backup is disabled.
	BackupHour            *int
	BackupKeep            int
	FailureAlertThreshold int
	Timezone              string
}

func intEnv(env map[string]string, name string, fallback int, min int, max int) int {
	raw := strings.TrimSpace(env[name])
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if value < min {
		value = min
	}
	if value > max {
		value = max
	}
	return value
}

// LoadConfig reads configuration from the provided environment map (nil means
// process env). It does not require DISCORD_TOKEN so that CLI helpers and the
// Docker healthcheck can run without it; the bot entrypoint validates it.
func LoadConfig(env map[string]string) Config {
	if env == nil {
		env = EnvironMap()
	}

	backupHourRaw := strings.TrimSpace(env["BACKUP_HOUR"])
	var backupHour *int
	if backupHourRaw != "" && (env["BACKUP_ENABLED"] == "" || strings.EqualFold(env["BACKUP_ENABLED"], "true")) {
		if hour, err := strconv.Atoi(backupHourRaw); err == nil {
			hour = min(max(hour, 0), 23)
			backupHour = &hour
		}
	}

	return Config{
		DiscordToken:           env["DISCORD_TOKEN"],
		DBPath:                 fallback(env["DB_PATH"], "./data/rss-tracker.db"),
		DefaultIntervalMinutes: intEnv(env, "DEFAULT_INTERVAL_MINUTES", 10, 2, 1<<31-1),
		MinIntervalMinutes:     intEnv(env, "MIN_INTERVAL_MINUTES", 2, 1, 1<<31-1),
		SendDelayMs:            intEnv(env, "SEND_DELAY_MS", 1000, 250, 1<<31-1),
		LogLevel:               fallback(env["LOG_LEVEL"], "info"),
		BackupHour:             backupHour,
		BackupKeep:             intEnv(env, "BACKUP_RETENTION_DAYS", 7, 1, 365),
		FailureAlertThreshold:  intEnv(env, "FAILURE_ALERT_THRESHOLD", 3, 1, 100),
		Timezone:               fallback(env["TIMEZONE"], "UTC"),
	}
}

// Validate returns an error when the config cannot run the bot.
func (c Config) Validate() error {
	if c.DiscordToken == "" {
		return fmt.Errorf("DISCORD_TOKEN is required (see .env.example)")
	}
	return nil
}

// ResolveDBPath returns dbPath as-is when absolute, otherwise relative to the
// current working directory (same contract as the TS resolveDbPath).
func ResolveDBPath(dbPath string) string {
	if filepath.IsAbs(dbPath) {
		return dbPath
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return dbPath
	}
	return abs
}

func fallback(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// EnvironMap returns the process environment as a KEY=VALUE map.
func EnvironMap() map[string]string {
	m := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			m[key] = value
		}
	}
	return m
}
