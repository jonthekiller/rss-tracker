package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/backup"
	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/importer"
	"github.com/jonthekiller/rss-tracker/internal/render"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// ---------------------------------------------------------------------------
// /import
// ---------------------------------------------------------------------------

func handleImport(ctx *BotContext, c *icc) {
	if _, sub := c.subcommand(); sub != "monitorss" {
		_ = c.replyEphemeral("Unknown subcommand.")
		return
	}
	if err := c.deferReply(); err != nil {
		return
	}

	guildID := c.guildID()
	fallbackChannelID := c.channelID()
	if opt := c.optChannelID("channel"); opt != nil {
		fallbackChannelID = *opt
	}

	// Import is channel-scoped: the importer must be able to write in the
	// destination channel (explicit option or the current channel).
	if !ctx.canManageChannel(c.Interaction, fallbackChannelID) {
		_ = c.editReply(fmt.Sprintf(
			"🔒 You need the **Send Messages** permission in <#%s> to import feeds there.", fallbackChannelID))
		return
	}

	data, err := c.getAttachmentJSON("file")
	if err != nil {
		_ = c.editReply("❌ " + err.Error())
		return
	}

	ctx.Scheduler.Suspend()
	defer ctx.Scheduler.Resume()

	results, err := importer.ImportMonitoRssBackup(ctx.Store, data, importer.ImportOptions{
		GuildID:               guildID,
		FallbackChannelID:     fallbackChannelID,
		ChannelExists:         func(g, ch string) (bool, error) { return ctx.channelExists(g, ch), nil },
		ActorCanManageChannel: func(ch string) (bool, error) { return ctx.canManageChannel(c.Interaction, ch), nil },
		DefaultTemplate:       render.DefaultTemplate,
	})
	if err != nil {
		_ = c.editReply("❌ " + err.Error())
		return
	}
	_ = c.editReply(formatResultsReport(
		fmt.Sprintf("MonitoRSS import — %d feed(s)", len(results)), toRestoreResults(results)))
}

func toRestoreResults(results []importer.ImportEntryResult) []RestoreResult {
	out := make([]RestoreResult, 0, len(results))
	for _, r := range results {
		out = append(out, RestoreResult{Name: r.Name, URL: r.URL, Status: r.Status, Detail: r.Detail})
	}
	return out
}

func backupResultsToRestoreResults(results []backup.RestoreEntryResult) []RestoreResult {
	out := make([]RestoreResult, 0, len(results))
	for _, r := range results {
		out = append(out, RestoreResult{Name: r.Name, URL: r.URL, Status: r.Status, Detail: r.Detail})
	}
	return out
}

// ---------------------------------------------------------------------------
// /backup
// ---------------------------------------------------------------------------

func handleBackup(ctx *BotContext, c *icc) {
	_, sub := c.subcommand()
	if sub == "export" {
		handleBackupExport(ctx, c)
		return
	}
	handleBackupRestore(ctx, c)
}

func handleBackupExport(ctx *BotContext, c *icc) {
	all := derefB(c.optBool("all_channels"))
	if all && !isAdmin(c.Interaction) {
		_ = c.replyEphemeral("Exporting **all channels** requires the **Manage Server** permission. Export a single channel with `/backup export channel:<channel>`.")
		return
	}
	var channelIDs []string
	scopeLabel := "all channels"
	if !all {
		channelID := c.channelID()
		if opt := c.optChannelID("channel"); opt != nil {
			channelID = *opt
		}
		if !ctx.canManageChannel(c.Interaction, channelID) {
			denyChannelAccess(c, channelID)
			return
		}
		channelIDs = []string{channelID}
		scopeLabel = "<#" + channelID + ">"
	}
	backupFile, err := backup.ExportBackup(ctx.Store, channelIDs)
	if err != nil {
		_ = c.replyEphemeral("❌ Export failed: " + err.Error())
		return
	}
	feedCount := 0
	for _, channel := range backupFile.Channels {
		feedCount += len(channel.Feeds)
	}
	encoded, err := json.MarshalIndent(backupFile, "", "  ")
	if err != nil {
		_ = c.replyEphemeral("❌ Export failed: " + err.Error())
		return
	}
	filename := "rss-backup-" + time.Now().UTC().Format("2006-01-02") + ".json"
	_ = c.respondWithFile(
		fmt.Sprintf("💾 Backup (%s): %d feed(s) in %d channel(s), exported %s",
			scopeLabel, feedCount, len(backupFile.Channels), backupFile.ExportedAt),
		filename, encoded)
}

func handleBackupRestore(ctx *BotContext, c *icc) {
	if err := c.deferReply(); err != nil {
		return
	}
	guildID := c.guildID()
	data, err := c.getAttachmentJSON("file")
	if err != nil {
		_ = c.editReply("❌ " + err.Error())
		return
	}

	ctx.Scheduler.Suspend()
	defer ctx.Scheduler.Resume()

	results, err := backup.RestoreBackup(ctx.Store, data, backup.RestoreOptions{
		GuildID:               guildID,
		ChannelExists:         func(g, ch string) (bool, error) { return ctx.channelExists(g, ch), nil },
		ActorCanManageChannel: func(ch string) (bool, error) { return ctx.canManageChannel(c.Interaction, ch), nil },
		Baseline: func(feedID, feedURL string) (bool, string, string) {
			result, fetchErr := feed.FetchFeed(context.Background(), feedURL, feed.FetchOptions{})
			if fetchErr != nil {
				return false, "", fetchErr.Error()
			}
			if result.Status == feed.StatusOK {
				name := ""
				if result.Meta.Title != nil {
					name = *result.Meta.Title
				}
				now := store.NowMillis()
				if err := ctx.Pipeline.Baseline(&store.Feed{ID: feedID}, result.Articles, now); err != nil {
					return false, name, err.Error()
				}
			}
			return true, "", ""
		},
	})
	if err != nil {
		_ = c.editReply("❌ " + err.Error())
		return
	}
	_ = c.editReply(formatResultsReport(
		fmt.Sprintf("Restore — %d feed(s)", len(results)), backupResultsToRestoreResults(results)))
}

// ---------------------------------------------------------------------------
// /settings
// ---------------------------------------------------------------------------

func handleSettings(ctx *BotContext, c *icc) {
	if _, sub := c.subcommand(); sub != "alert" {
		_ = c.replyEphemeral("Unknown subcommand.")
		return
	}
	// Settings are PER CHANNEL: run the command in the channel to configure.
	channelID := c.channelID()
	if c.guildID() == "" || !ctx.canManageChannel(c.Interaction, channelID) {
		_ = c.replyEphemeral("🔒 You need the **Send Messages** permission in this channel to configure its alerts.")
		return
	}
	if opt := c.optChannelID("channel"); opt != nil && !ctx.canManageChannel(c.Interaction, *opt) {
		denyChannelAccess(c, *opt)
		return
	}

	if user := c.optUserID("user"); user != nil {
		if err := ctx.Store.SetChannelSetting(channelID, "alert_user_id", user); err != nil {
			_ = c.replyEphemeral("Database error: " + err.Error())
			return
		}
	} else if c.optUserID("user") == nil && hasOption(c, "user") {
		// Option explicitly emptied (not possible via Discord UI, kept for parity).
		_ = ctx.Store.SetChannelSetting(channelID, "alert_user_id", nil)
	}
	if channel := c.optChannelID("channel"); channel != nil {
		if err := ctx.Store.SetChannelSetting(channelID, "alert_channel_id", channel); err != nil {
			_ = c.replyEphemeral("Database error: " + err.Error())
			return
		}
	} else if hasOption(c, "channel") {
		_ = ctx.Store.SetChannelSetting(channelID, "alert_channel_id", nil)
	}
	// Null = enabled (default); '0' = explicitly muted.
	if enabled := c.optBool("enabled"); enabled != nil {
		var value *string
		if !*enabled {
			muted := "0"
			value = &muted
		}
		if err := ctx.Store.SetChannelSetting(channelID, "alert_enabled", value); err != nil {
			_ = c.replyEphemeral("Database error: " + err.Error())
			return
		}
	}
	// Null = follow the global threshold (default).
	if threshold := c.optInt("threshold"); threshold != nil {
		value := fmt.Sprintf("%d", *threshold)
		if err := ctx.Store.SetChannelSetting(channelID, "alert_threshold", &value); err != nil {
			_ = c.replyEphemeral("Database error: " + err.Error())
			return
		}
	}

	current := []string{}
	muted, _ := ctx.Store.GetChannelSetting(channelID, "alert_enabled")
	if muted != nil && *muted == "0" {
		current = append(current, "🔕 OFF")
	} else {
		if alertUser, _ := ctx.Store.GetChannelSetting(channelID, "alert_user_id"); alertUser != nil {
			current = append(current, "User: <@"+*alertUser+">")
		}
		if alertChannel, _ := ctx.Store.GetChannelSetting(channelID, "alert_channel_id"); alertChannel != nil {
			current = append(current, "Channel: <#"+*alertChannel+">")
		}
		if alertThreshold, _ := ctx.Store.GetChannelSetting(channelID, "alert_threshold"); alertThreshold != nil {
			current = append(current, "Threshold: "+*alertThreshold+" failures")
		}
	}
	status := "disabled (no target)"
	if len(current) == 1 {
		status = current[0]
	} else if len(current) > 1 {
		status = join(current, " · ")
	}
	_ = c.replyEphemeral(fmt.Sprintf("⚙️ Failure alerts for <#%s>: %s", channelID, status))
}

// hasOption reports whether the option was passed (present with a null-ish
// value in the TS version meant "clear"; Discord omits unset options).
func hasOption(c *icc, name string) bool {
	return findOption(c.options(), name) != nil
}

func join(values []string, sep string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}

// ---------------------------------------------------------------------------
// /status
// ---------------------------------------------------------------------------

func handleStatus(ctx *BotContext, c *icc) {
	feeds, _ := ctx.Store.GetAllFeeds()
	enabled := 0
	for _, feedRow := range feeds {
		if feedRow.Enabled {
			enabled++
		}
	}
	var failing []*store.Feed
	for _, feedRow := range feeds {
		if feedRow.ConsecutiveFailures > 0 {
			failing = append(failing, feedRow)
		}
	}
	lines := []string{
		"🤖 **RSS-Tracker**",
		fmt.Sprintf("Feeds: %d/%d enabled, %d paused", enabled, len(feeds), len(feeds)-enabled),
		fmt.Sprintf("Failing: %d", len(failing)),
		fmt.Sprintf("Send queue: %d message(s) pending", ctx.Sender.TotalDepth()),
		fmt.Sprintf("Scheduler: %s", schedulerStatusLabel(ctx)),
	}
	if len(failing) > 0 {
		lines = append(lines, "", "**Failing feeds:**")
		for _, feedRow := range failing[:min(10, len(failing))] {
			lastError := "unknown"
			if feedRow.LastError != nil {
				lastError = *feedRow.LastError
			}
			lines = append(lines, fmt.Sprintf("- **%s**: %s (×%d)", feedRow.Name, lastError, feedRow.ConsecutiveFailures))
		}
	}
	chunks := chunkLines(lines, 1900)
	_ = c.replyEphemeral(chunks[0])
}

func schedulerStatusLabel(ctx *BotContext) string {
	if ctx.Scheduler.IsSuspended() {
		return "suspended (import/restore)"
	}
	return "running"
}

// ---------------------------------------------------------------------------
// /debug
// ---------------------------------------------------------------------------

func handleDebug(ctx *BotContext, c *icc) {
	_, sub := c.subcommand()
	if sub == "feed" {
		handleDebugFeed(ctx, c)
		return
	}
	handleDebugSystem(ctx, c)
}

func handleDebugFeed(ctx *BotContext, c *icc) {
	if err := c.deferReply(); err != nil {
		return
	}
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	report, err := ctx.Pipeline.DryRun(context.Background(), feedRow.ID, store.NowMillis())
	if err != nil {
		_ = c.editReply("❌ " + err.Error())
		return
	}

	id := report.Feed.ID
	if len(id) > 8 {
		id = id[:8]
	}
	lines := []string{
		fmt.Sprintf("🔧 **Dry-run — %s** (`%s`)", report.Feed.Name, id),
		fmt.Sprintf("URL: <%s> → <#%s>", report.Feed.URL, report.Feed.ChannelID),
		fmt.Sprintf("Enabled: %t%s · interval %d min · batch `%s` (≥ %d)",
			report.Feed.Enabled, pauseSuffix(report.Feed.PauseReason),
			report.Feed.IntervalMinutes, report.Feed.BatchMode, report.Feed.BatchThreshold),
		fmt.Sprintf("State: %d seen · failures ×%d%s · queue: %d",
			report.State.SeenCount, report.State.ConsecutiveFailures,
			lastErrorSuffix(report.State.LastError), report.State.QueueDepth),
	}

	switch {
	case report.Fetch == nil:
		lines = append(lines, "Fetch: (no result)")
	case report.Fetch.Status == "error":
		lines = append(lines, "Fetch: ❌ "+report.Fetch.Error)
		lines = append(lines, "→ Check the URL, the container logs and your network from the host.")
	case report.Fetch.Status == "not_modified":
		lines = append(lines, "Fetch: ✅ "+report.Fetch.Detail)
	default:
		extra := ""
		if report.Fetch.FeedTitle != nil {
			extra = fmt.Sprintf(" (feed title: \"%s\")", *report.Fetch.FeedTitle)
		}
		lines = append(lines, fmt.Sprintf("Fetch: ✅ %d article(s) parsed%s", report.Fetch.ArticleCount, extra))
	}

	if report.Detection != nil {
		d := report.Detection
		lines = append(lines, fmt.Sprintf("Detection: %d new · %d blocked by filters · %d too old", d.Fresh, d.Blocked, d.TooOld))
		if len(d.FreshTitles) > 0 {
			lines = append(lines, "New (would be sent):")
			for _, title := range d.FreshTitles {
				lines = append(lines, "  - "+title)
			}
		}
		if len(d.BlockedTitles) > 0 {
			lines = append(lines, "Blocked:")
			for _, title := range d.BlockedTitles {
				lines = append(lines, "  - "+title)
			}
		}
	}
	if report.Delivery != nil {
		suffix := ""
		if !report.Delivery.WouldSendNow {
			suffix = " — **nothing would be sent right now** (paused, nothing new, or first check not yet done)"
		}
		lines = append(lines, fmt.Sprintf("Delivery: `%s` → %d message(s)%s", report.Delivery.Mode, report.Delivery.Messages, suffix))
	}
	if report.Render != nil {
		content := report.Render.Content
		if len(content) > 500 {
			content = truncateRunes(content, 500)
		}
		lines = append(lines,
			fmt.Sprintf("Render preview (latest article \"%s\", %d embed):", report.Render.ArticleTitle, report.Render.EmbedCount),
			"```",
			content,
			"```")
	}
	lines = append(lines, "*No state was modified by this dry-run.*")

	chunks := chunkLines(lines, 1900)
	_ = c.editReply(chunks[0])
	for _, chunk := range chunks[1:min(5, len(chunks))] {
		_ = c.followUpEphemeral(chunk)
	}
}

func lastErrorSuffix(errText *string) string {
	if errText == nil {
		return ""
	}
	return " — last: " + *errText
}

func handleDebugSystem(ctx *BotContext, c *icc) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	uptimeS := time.Since(startTime).Milliseconds() / 1000
	feeds, _ := ctx.Store.GetAllFeeds()
	enabled := 0
	for _, feedRow := range feeds {
		if feedRow.Enabled {
			enabled++
		}
	}
	botUser := "not ready"
	if ctx.Session != nil && ctx.Session.State != nil && ctx.Session.State.User != nil {
		botUser = ctx.Session.State.User.Username
	}
	lines := []string{
		"🔧 **System diagnostics**",
		fmt.Sprintf("Version: %s · Go %s", ctx.Version, runtime.Version()),
		fmt.Sprintf("Uptime: %dh %dm", uptimeS/3600, (uptimeS%3600)/60),
		fmt.Sprintf("Memory: heap %d/%d MB · sys %d MB", mem.HeapAlloc/1024/1024, mem.HeapSys/1024/1024, mem.Sys/1024/1024),
		fmt.Sprintf("Feeds: %d/%d enabled", enabled, len(feeds)),
		fmt.Sprintf("Send queue: %d message(s) pending", ctx.Sender.TotalDepth()),
		fmt.Sprintf("Scheduler: %s", schedulerStatusLabel(ctx)),
		fmt.Sprintf("Bot user: %s", botUser),
		"",
		"💡 Container logs: `docker logs -f rss-tracker` (or the add-on log page).",
	}
	_ = c.replyEphemeral(chunkLines(lines, 1900)[0])
}

var startTime = time.Now()
