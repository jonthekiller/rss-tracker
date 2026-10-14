package bot

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/backup"
	"github.com/jonthekiller/rss-tracker/internal/config"
	"github.com/jonthekiller/rss-tracker/internal/logging"
	"github.com/jonthekiller/rss-tracker/internal/scheduler"
	"github.com/jonthekiller/rss-tracker/internal/sender"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// StartBot wires the Discord client, opens the database and runs until
// SIGINT/SIGTERM. It is the port of src/bot/bot.ts.
func StartBot(cfg config.Config, version string) error {
	log := logging.New("bot", cfg.LogLevel)

	ctx := &BotContext{Config: cfg, Version: version}

	db, err := store.Open(store.OpenOptions{DBPath: config.ResolveDBPath(cfg.DBPath), EnsureDir: true})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	ctx.Store = store.NewFeedStore(db)
	defer db.Close()

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return fmt.Errorf("create discord session: %w", err)
	}
	// Same minimal intent surface as the TS build: no message content needed.
	session.Identify.Intents = discordgo.IntentsGuilds
	session.StateEnabled = true
	ctx.Session = session

	ctx.Sender = sender.New(sender.Options{
		SendDelayMs: cfg.SendDelayMs,
		Send: func(channelID string, message sender.OutboundMessage) error {
			channel, err := session.Channel(channelID)
			if err != nil {
				return err
			}
			if channel == nil || !isSendableChannel(channel) {
				// Unknown channel / not sendable: error code 10003 semantics.
				return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: 10003}}
			}
			_, err = session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
				Content: message.Content, Embeds: message.Embeds,
			})
			return err
		},
		OnSendError: func(channelID string, _ sender.OutboundMessage, err error) {
			code := errorCode(err)
			log.Warn("Failed to send message to channel", "channelId", channelID, "code", code, "err", err)
			if code == 10003 || code == 50001 || code == 50013 {
				// Unknown channel / missing access: pause every feed targeting it.
				feeds, _ := ctx.Store.GetFeedsForChannel(channelID)
				for _, feedRow := range feeds {
					if feedRow.Enabled {
						enabled := false
						reason := "channel inaccessible"
						if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{
							Enabled: &enabled, PauseReason: &reason,
						}); err != nil {
							log.Error("pause feed failed", "err", err)
						}
					}
				}
			}
		},
	})

	ctx.Pipeline = scheduler.NewPipeline(schedulerPipelineOptions(cfg, ctx))
	ctx.Scheduler = scheduler.NewScheduler(ctx.Pipeline, ctx.Store, scheduler.SchedulerOptions{})

	session.AddHandler(func(s *discordgo.Session, ready *discordgo.Ready) {
		log.Info("Bot ready", "user", ready.User.Username, "guilds", len(ready.Guilds))
		if err := registerCommands(s, ready.Guilds); err != nil {
			log.Error("Failed to register slash commands", "err", err)
		} else {
			log.Info("Slash commands registered")
		}
	})
	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Interaction handler panic", "panic", r)
			}
		}()
		switch i.Type {
		case discordgo.InteractionApplicationCommandAutocomplete:
			handleAutocomplete(ctx, s, i.Interaction)
		case discordgo.InteractionModalSubmit:
			handleModalSubmit(ctx, s, i.Interaction)
		case discordgo.InteractionApplicationCommand:
			routeCommand(ctx, s, i)
		}
	})

	// Heartbeat for the Docker healthcheck. Written immediately so a restart
	// with an existing database doesn't fail the first checks.
	kvSet := func() {
		if err := store.KVSet(db, "last_scheduler_tick", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			log.Error("heartbeat write failed", "err", err)
		}
	}
	kvSet()
	heartbeatStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				kvSet()
			case <-heartbeatStop:
				return
			}
		}
	}()

	stopAutoBackup := startAutoBackup(ctx, cfg)

	if err := session.Open(); err != nil {
		return fmt.Errorf("discord login: %w", err)
	}
	StartBotContext(ctx)

	ctx.Scheduler.Start()
	allFeeds, _ := ctx.Store.GetAllFeeds()
	log.Info("Scheduler started", "feeds", len(allFeeds))

	// Wait for shutdown signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Info("Shutting down")
	ctx.Scheduler.Stop()
	close(heartbeatStop)
	stopAutoBackup()
	session.Close()
	db.Close()
	return nil
}

// startAutoBackup starts the daily backup loop and returns a stop function.
func startAutoBackup(ctx *BotContext, cfg config.Config) func() {
	return backup.StartAutoBackupLoop(backup.AutoBackupOptions{
		Store:     ctx.Store,
		Directory: filepath.Join(filepath.Dir(config.ResolveDBPath(cfg.DBPath)), "backups"),
		Hour:      cfg.BackupHour,
		Keep:      cfg.BackupKeep,
	})
}

// errorCode extracts the Discord error code from a REST error.
func errorCode(err error) int {
	if restErr, ok := err.(*discordgo.RESTError); ok && restErr.Message != nil {
		return restErr.Message.Code
	}
	return 0
}

// registerCommands registers commands per guild (instant availability) and
// clears the global set to remove duplicates from older versions.
func registerCommands(s *discordgo.Session, guilds []*discordgo.Guild) error {
	if s.State == nil || s.State.User == nil {
		return fmt.Errorf("client not ready")
	}
	appID := s.State.User.ID
	if _, err := s.ApplicationCommandBulkOverwrite(appID, "", []*discordgo.ApplicationCommand{}); err != nil {
		return fmt.Errorf("clear global commands: %w", err)
	}
	for _, guild := range guilds {
		if _, err := s.ApplicationCommandBulkOverwrite(appID, guild.ID, buildCommandPayloads()); err != nil {
			return fmt.Errorf("register commands for guild %s: %w", guild.ID, err)
		}
	}
	return nil
}

// sendFailureAlert DMs the configured user or posts in the configured channel
// when a feed crosses the failure threshold.
func (ctx *BotContext) sendFailureAlert(feedRow *store.Feed, lastError string) {
	log := logging.New("bot", ctx.Config.LogLevel)
	message := fmt.Sprintf(
		"⚠️ **Feed failed** — %s (<%s>)\n```\n%s\n```", feedRow.Name, feedRow.URL, lastError)

	// Per-channel kill switch (/settings alert enabled:false).
	if enabled, _ := ctx.Store.GetChannelSetting(feedRow.ChannelID, "alert_enabled"); enabled != nil && *enabled == "0" {
		return
	}
	// Alert targets are configured per channel (/settings alert in that channel).
	if userID, _ := ctx.Store.GetChannelSetting(feedRow.ChannelID, "alert_user_id"); userID != nil {
		channel, err := ctx.Session.UserChannelCreate(*userID)
		if err == nil && channel != nil {
			if _, err := ctx.Session.ChannelMessageSend(channel.ID, message); err == nil {
				notified := true
				if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{FailedNotified: &notified}); err != nil {
					log.Error("failedNotified update failed", "err", err)
				}
				return
			}
		}
		return
	}
	if channelID, _ := ctx.Store.GetChannelSetting(feedRow.ChannelID, "alert_channel_id"); channelID != nil {
		channel, err := ctx.Session.Channel(*channelID)
		if err == nil && channel != nil && isSendableChannel(channel) {
			if _, err := ctx.Session.ChannelMessageSend(*channelID, message); err == nil {
				notified := true
				if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{FailedNotified: &notified}); err != nil {
					log.Error("failedNotified update failed", "err", err)
				}
			}
		}
	}
}

// schedulerPipelineOptions builds the pipeline with the failure-alert hook.
func schedulerPipelineOptions(cfg config.Config, ctx *BotContext) scheduler.PipelineOptions {
	return scheduler.PipelineOptions{
		Store:                 ctx.Store,
		Sender:                ctx.Sender,
		Timezone:              cfg.Timezone,
		MinIntervalMinutes:    cfg.MinIntervalMinutes,
		FailureAlertThreshold: cfg.FailureAlertThreshold,
		OnFeedFailed: func(feedRow *store.Feed, lastError string) {
			ctx.sendFailureAlert(feedRow, lastError)
		},
		OnFeedRecovered: func(feedRow *store.Feed) {},
	}
}
