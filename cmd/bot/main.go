// Command bot is the single binary of RSS-Tracker. Subcommands:
//
//	bot                       run the Discord bot (default)
//	bot healthcheck           Docker HEALTHCHECK (scheduler heartbeat freshness)
//	bot backup [file]         export a backup JSON (default data/backups/…)
//	bot restore file.json     restore a backup (RESTORE_GUILD_ID required)
//	bot import file.json …    import a MonitoRSS backup
//	bot addon-run             Home Assistant add-on entrypoint (options.json)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Embedded timezone database: guarantees TIMEZONE support even in
	// minimal runtime images without tzdata.
	_ "time/tzdata"

	"github.com/jonthekiller/rss-tracker/internal/backup"
	"github.com/jonthekiller/rss-tracker/internal/bot"
	"github.com/jonthekiller/rss-tracker/internal/config"
	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/importer"
	"github.com/jonthekiller/rss-tracker/internal/render"
	"github.com/jonthekiller/rss-tracker/internal/scheduler"
	"github.com/jonthekiller/rss-tracker/internal/sender"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// version is overridden at build time via -ldflags "-X main.version=…".
var version = "dev"

const healthcheckMaxAgeMs = 10 * 60_000

func main() {
	env := config.EnvironMap()
	args := os.Args[1:]
	command := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}

	var err error
	switch command {
	case "run":
		err = runBot(env)
	case "healthcheck":
		err = runHealthcheck(env)
	case "backup":
		err = runBackupCLI(env, args)
	case "restore":
		err = runRestoreCLI(env, args)
	case "import":
		err = runImportCLI(env, args)
	case "addon-run":
		err = runAddon(env)
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "help", "--help", "-h":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fatal error:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`RSS-Tracker — lightweight self-hosted Discord RSS bot

Usage:
  bot [run]                     Run the Discord bot
  bot healthcheck               Docker healthcheck (heartbeat freshness)
  bot backup [file.json]        Export a backup
  bot restore file.json [--map-channel old=new …]
  bot import file.json --guild <id> --channel <id> [--map-old-channel old=new …]
  bot addon-run                 Home Assistant add-on entrypoint
  bot version                   Print version`)
}

func runBot(env map[string]string) error {
	cfg := config.LoadConfig(env)
	if err := cfg.Validate(); err != nil {
		return err
	}
	return bot.StartBot(cfg, version)
}

// runHealthcheck verifies the database opens and the scheduler heartbeat is
// recent. Deliberately independent from the full config: it must not require
// DISCORD_TOKEN (the healthcheck process does not inherit env vars exported at
// runtime, e.g. Home Assistant add-on options).
func runHealthcheck(env map[string]string) error {
	dbPath := config.ResolveDBPath(fallbackStr(env["DB_PATH"], "./data/rss-tracker.db"))
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// First boot, database not created yet: not an error.
		return nil
	}
	db, err := store.Open(store.OpenOptions{DBPath: dbPath})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	lastTick, err := store.KVGet(db, "last_scheduler_tick")
	if err != nil {
		return err
	}
	if lastTick == nil {
		// Bot may still be starting.
		return nil
	}
	tickTime, err := time.Parse(time.RFC3339Nano, *lastTick)
	if err != nil {
		return fmt.Errorf("unreadable heartbeat: %w", err)
	}
	age := time.Since(tickTime)
	if age > healthcheckMaxAgeMs*time.Millisecond {
		return fmt.Errorf("Scheduler heartbeat too old: %d ms", age.Milliseconds())
	}
	return nil
}

func runBackupCLI(env map[string]string, args []string) error {
	cfg := config.LoadConfig(env)
	output := "./data/backups/rss-backup-" + time.Now().UTC().Format("2006-01-02") + ".json"
	if len(args) > 0 {
		output = args[0]
	}
	db, err := store.Open(store.OpenOptions{DBPath: config.ResolveDBPath(cfg.DBPath), EnsureDir: true})
	if err != nil {
		return err
	}
	defer db.Close()
	backupFile, err := backup.ExportBackup(store.NewFeedStore(db), nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(backupFile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(output, encoded, 0o644); err != nil {
		return err
	}
	feedCount := 0
	for _, channel := range backupFile.Channels {
		feedCount += len(channel.Feeds)
	}
	fmt.Printf("Backup written to %s (%d feeds in %d channels)\n", output, feedCount, len(backupFile.Channels))
	return nil
}

func runRestoreCLI(env map[string]string, args []string) error {
	var file string
	mapChannels := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case i == 0:
			file = arg
		case strings.HasPrefix(arg, "--map-channel="):
			parts := strings.SplitN(strings.TrimPrefix(arg, "--map-channel="), "=", 2)
			if len(parts) == 2 {
				mapChannels[parts[0]] = parts[1]
			}
		}
	}
	if file == "" {
		return fmt.Errorf("Usage: bot restore file.json [--map-channel old=new ...]")
	}
	guildID := env["RESTORE_GUILD_ID"]
	if guildID == "" {
		return fmt.Errorf("RESTORE_GUILD_ID env var is required for CLI restore")
	}
	cfg := config.LoadConfig(env)
	db, err := store.Open(store.OpenOptions{DBPath: config.ResolveDBPath(cfg.DBPath), EnsureDir: true})
	if err != nil {
		return err
	}
	defer db.Close()
	st := store.NewFeedStore(db)
	pipeline := scheduler.NewPipeline(scheduler.PipelineOptions{
		Store:              st,
		Sender:             sender.New(sender.Options{SendDelayMs: cfg.SendDelayMs, Send: func(string, sender.OutboundMessage) error { return nil }}),
		Timezone:           cfg.Timezone,
		MinIntervalMinutes: cfg.MinIntervalMinutes,
	})
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	results, err := backup.RestoreBackup(st, data, backup.RestoreOptions{
		GuildID:               guildID,
		MapChannels:           mapChannels,
		ChannelExists:         func(string, string) (bool, error) { return false, nil }, // CLI cannot verify; remap explicitly
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },          // CLI has no Discord actor
		Baseline: func(feedID, feedURL string) (bool, string, string) {
			result, fetchErr := feed.FetchFeed(nil2ctx(), feedURL, feed.FetchOptions{})
			if fetchErr != nil {
				return false, "", fetchErr.Error()
			}
			if result.Status == feed.StatusOK {
				name := ""
				if result.Meta.Title != nil {
					name = *result.Meta.Title
				}
				if err := pipeline.Baseline(&store.Feed{ID: feedID}, result.Articles, store.NowMillis()); err != nil {
					return false, name, err.Error()
				}
			}
			return true, "", ""
		},
	})
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("%-16s %s — %s\n", strings.ToUpper(r.Status), r.Name, r.Detail)
	}
	return nil
}

func runImportCLI(env map[string]string, args []string) error {
	var file, guildID, fallbackChannelID string
	mapOldChannels := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case i == 0:
			file = arg
		case arg == "--guild" && i+1 < len(args):
			i++
			guildID = args[i]
		case arg == "--channel" && i+1 < len(args):
			i++
			fallbackChannelID = args[i]
		case strings.HasPrefix(arg, "--map-old-channel="):
			parts := strings.SplitN(strings.TrimPrefix(arg, "--map-old-channel="), "=", 2)
			if len(parts) == 2 {
				mapOldChannels[parts[0]] = parts[1]
			}
		}
	}
	if file == "" || guildID == "" || fallbackChannelID == "" {
		return fmt.Errorf("Usage: bot import backup.json --guild <id> --channel <id> [--map-old-channel old=new ...]")
	}
	cfg := config.LoadConfig(env)
	db, err := store.Open(store.OpenOptions{DBPath: config.ResolveDBPath(cfg.DBPath), EnsureDir: true})
	if err != nil {
		return err
	}
	defer db.Close()
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	results, err := importer.ImportMonitoRssBackup(store.NewFeedStore(db), data, importer.ImportOptions{
		GuildID:               guildID,
		FallbackChannelID:     fallbackChannelID,
		MapOldChannels:        mapOldChannels,
		ChannelExists:         func(string, string) (bool, error) { return false, nil }, // CLI cannot verify; map explicitly
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },          // CLI has no Discord actor
		DefaultTemplate:       render.DefaultTemplate,
	})
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("%-10s %s — %s\n", strings.ToUpper(r.Status), r.Name, r.Detail)
	}
	return nil
}

// runAddon is the Home Assistant add-on entrypoint: it reads the Supervisor
// options file, maps it to env vars and starts the bot (replaces run.sh +
// export-options.js — no shell in the add-on image).
func runAddon(env map[string]string) error {
	optionsPath := env["HA_OPTIONS_PATH"]
	if optionsPath == "" {
		optionsPath = "/data/options.json"
	}
	merged := map[string]string{}
	for key, value := range env {
		merged[key] = value
	}
	data, err := os.ReadFile(optionsPath)
	if err == nil {
		var options map[string]any
		if err := json.Unmarshal(data, &options); err != nil {
			return fmt.Errorf("read %s: %w", optionsPath, err)
		}
		setIfString := func(key string, value any) {
			if s, ok := value.(string); ok {
				merged[key] = s
			} else if n, ok := value.(float64); ok {
				merged[key] = fmt.Sprintf("%d", int64(n))
			}
		}
		merged["DB_PATH"] = "/data/rss-tracker.db"
		for key, value := range options {
			switch key {
			case "discord_token":
				setIfString("DISCORD_TOKEN", value)
			case "default_interval_minutes":
				setIfString("DEFAULT_INTERVAL_MINUTES", value)
			case "send_delay_ms":
				setIfString("SEND_DELAY_MS", value)
			case "log_level":
				setIfString("LOG_LEVEL", value)
			case "backup_hour":
				setIfString("BACKUP_HOUR", value)
			case "backup_retention_days":
				setIfString("BACKUP_RETENTION_DAYS", value)
			case "failure_alert_threshold":
				setIfString("FAILURE_ALERT_THRESHOLD", value)
			case "timezone":
				setIfString("TIMEZONE", value)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", optionsPath, err)
	}
	return runBot(merged)
}

func fallbackStr(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

func nil2ctx() context.Context { return context.Background() }
