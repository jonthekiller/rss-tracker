package bot

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/backup"
	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/render"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

var log = slog.With("scope", "bot")

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// chunkLines splits lines into ≤1900-char message chunks (rune-safe).
func chunkLines(lines []string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = 1900
	}
	chunks := []string{}
	var current []string
	length := 0
	for _, line := range lines {
		if length+runeLen(line)+1 > maxChars && len(current) > 0 {
			chunks = append(chunks, strings.Join(current, "\n"))
			current = []string{}
			length = 0
		}
		current = append(current, line)
		length += runeLen(line) + 1
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, "\n"))
	}
	return chunks
}

func runeLen(s string) int { return len([]rune(s)) }

// formatFeedLine is the one-line feed summary used by /feed list.
func formatFeedLine(feedRow *store.Feed) string {
	status := "✅"
	if !feedRow.Enabled {
		status = "⏸️"
	} else if feedRow.ConsecutiveFailures > 0 {
		status = "⚠️"
	}
	id := feedRow.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s **%s** — `%s` → <#%s> (every %d min)", status, feedRow.Name, id, feedRow.ChannelID, feedRow.IntervalMinutes)
}

// formatResultsReport renders a per-feed report (import/restore), first chunk only.
func formatResultsReport(title string, results []RestoreResult) string {
	lines := make([]string, 0, len(results)+1)
	lines = append(lines, "**"+title+"**")
	for _, r := range results {
		icon := "✅"
		if r.Status == "error" || r.Status == "channel_missing" {
			icon = "❌"
		} else if r.Detail != "" {
			icon = "⚠️"
		}
		line := fmt.Sprintf("%s **%s** — %s", icon, r.Name, r.Status)
		if r.Detail != "" {
			line += " (" + r.Detail + ")"
		}
		lines = append(lines, line)
	}
	chunks := chunkLines(lines, 1900)
	if len(chunks) == 0 {
		return title
	}
	return chunks[0]
}

// RestoreResult is the shared shape of import/restore entry results.
type RestoreResult struct {
	Name   string
	URL    string
	Status string
	Detail string
}

// getFeedOrReply resolves the id option and enforces channel permissions.
func getFeedOrReply(ctx *BotContext, c *icc) *store.Feed {
	id := deref(c.optString("id"))
	feedRow, err := ctx.Store.GetFeed(id)
	if err != nil || feedRow == nil {
		_ = c.replyEphemeral(fmt.Sprintf("Feed `%s` not found. Use `/feed list` to see ids.", id))
		return nil
	}
	// The feed must belong to this server, and the user must be allowed to
	// manage the channel it posts into (write access or server admin).
	if feedRow.GuildID != c.guildID() {
		_ = c.replyEphemeral(fmt.Sprintf("Feed `%s` not found. Use `/feed list` to see ids.", id))
		return nil
	}
	if !ctx.canManageChannel(c.Interaction, feedRow.ChannelID) {
		denyChannelAccess(c, feedRow.ChannelID)
		return nil
	}
	return feedRow
}

func denyChannelAccess(c *icc, channelID string) {
	_ = c.replyEphemeral(fmt.Sprintf(
		"🔒 You need the **Send Messages** permission in <#%s> to configure its feeds. (Server administrators can always configure.)",
		channelID))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------------------------------------------------------------------------
// /feed subcommands
// ---------------------------------------------------------------------------

func handleFeed(ctx *BotContext, c *icc) {
	group, sub := c.subcommand()
	if group == "filter" {
		switch sub {
		case "add":
			feedFilterAdd(ctx, c)
		default:
			feedFilterRemove(ctx, c)
		}
		return
	}
	switch sub {
	case "add":
		feedAdd(ctx, c)
	case "list":
		feedList(ctx, c)
	case "remove":
		feedRemove(ctx, c)
	case "show":
		feedShow(ctx, c)
	case "pause":
		feedPauseResume(ctx, c, false)
	case "resume":
		feedPauseResume(ctx, c, true)
	case "move":
		feedMove(ctx, c)
	case "template":
		feedTemplateModal(ctx, c)
	case "embed":
		feedEmbedModal(ctx, c)
	case "frequency":
		feedFrequency(ctx, c)
	case "batch":
		feedBatch(ctx, c)
	case "test":
		feedTest(ctx, c)
	case "refresh":
		feedRefresh(ctx, c)
	default:
		_ = c.replyEphemeral("Unknown subcommand.")
	}
}

func feedAdd(ctx *BotContext, c *icc) {
	if err := c.deferReply(); err != nil {
		return
	}
	rawURL := strings.TrimSpace(deref(c.optString("url")))
	channelID := c.channelID()
	if opt := c.optChannelID("channel"); opt != nil {
		channelID = *opt
	}
	name := strings.TrimSpace(deref(c.optString("name")))
	if name == "" {
		name = "Unknown"
	}

	// You can only add feeds to a channel you can write in.
	if !ctx.canManageChannel(c.Interaction, channelID) {
		denyChannelAccess(c, channelID)
		return
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		_ = c.editReply(fmt.Sprintf("Invalid URL: `%s`", rawURL))
		return
	}

	exists, err := ctx.Store.Exists(rawURL, channelID)
	if err != nil {
		_ = c.editReply("Database error: " + err.Error())
		return
	}
	if exists {
		_ = c.editReply("This feed already exists for this channel.")
		return
	}

	feedName := name
	warning := ""
	result, fetchErr := feed.FetchFeed(context.Background(), rawURL, feed.FetchOptions{})
	if fetchErr != nil {
		warning = "⚠️ Could not fetch the feed right now (" + fetchErr.Error() + "); it will retry automatically."
	} else if result.Status == feed.StatusOK {
		if name == "Unknown" && result.Meta.Title != nil {
			feedName = *result.Meta.Title
		}
	}

	feedRow, err := ctx.Store.AddFeed(store.NewFeed{
		ID: backup.NewFeedID(), Name: feedName, URL: rawURL,
		GuildID: c.guildID(), ChannelID: channelID,
		TemplateText:    render.DefaultTemplate,
		IntervalMinutes: ctx.Config.DefaultIntervalMinutes, HasInterval: true,
	})
	if err != nil {
		_ = c.editReply("Database error: " + err.Error())
		return
	}

	// Baseline: mark current articles as seen without sending.
	articleCount := 0
	if fetchErr == nil && result.Status == feed.StatusOK {
		now := store.NowMillis()
		if err := ctx.Pipeline.Baseline(feedRow, result.Articles, now); err == nil {
			articleCount = len(result.Articles)
		}
	}

	response := fmt.Sprintf("✅ Feed **%s** added to <#%s> (`%s`).\n%s\n%s",
		feedName, channelID, shortID(feedRow.ID),
		articleCountSuffix(articleCount), warning)
	_ = c.editReply(response)
}

func articleCountSuffix(count int) string {
	if count > 0 {
		return fmt.Sprintf("%d existing article(s) marked as seen (nothing will be sent for those).", count)
	}
	return ""
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func feedList(ctx *BotContext, c *icc) {
	var feedRows []*store.Feed
	if channel := c.optChannelID("channel"); channel != nil {
		// Listing another channel's feeds requires write access there.
		if !ctx.canManageChannel(c.Interaction, *channel) {
			denyChannelAccess(c, *channel)
			return
		}
		feedRows, _ = ctx.Store.GetFeedsForChannel(*channel)
	} else if isAdmin(c.Interaction) {
		feedRows, _ = ctx.Store.GetFeedsForGuild(c.guildID())
	} else {
		// Non-admins only see the feeds of the channel they are typing in.
		feedRows, _ = ctx.Store.GetFeedsForChannel(c.channelID())
	}
	if len(feedRows) == 0 {
		_ = c.replyEphemeral("No feeds found. Add one with `/feed add`.")
		return
	}
	lines := []string{fmt.Sprintf("**Feeds (%d)**", len(feedRows))}
	for _, feedRow := range feedRows {
		lines = append(lines, formatFeedLine(feedRow))
	}
	chunks := chunkLines(lines, 1900)
	_ = c.replyEphemeral(chunks[0])
	for _, chunk := range chunks[1:min(5, len(chunks))] {
		_ = c.followUpEphemeral(chunk)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func feedRemove(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	if err := ctx.Store.DeleteFeed(feedRow.ID); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("🗑️ Feed **%s** removed.", feedRow.Name))
}

func feedShow(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	filters, _ := ctx.Store.GetFilters(feedRow.ID)
	lines := []string{
		fmt.Sprintf("**%s** (`%s`)", feedRow.Name, feedRow.ID),
		fmt.Sprintf("URL: <%s>", feedRow.URL),
		fmt.Sprintf("Channel: <#%s>", feedRow.ChannelID),
		fmt.Sprintf("Enabled: %t%s", feedRow.Enabled, pauseSuffix(feedRow.PauseReason)),
		fmt.Sprintf("Frequency: every %d min", feedRow.IntervalMinutes),
		fmt.Sprintf("Batch: %s (threshold %d; digests list every article, split across messages if needed)", feedRow.BatchMode, feedRow.BatchThreshold),
		fmt.Sprintf("Max article age: %s", maxAgeLabel(feedRow.MaxAgeDays)),
		"Template:",
		"```",
		feedRow.TemplateText,
		"```",
	}
	if feedRow.EmbedJSON != nil {
		lines = append(lines, "Embed:", "```json", truncateRunes(*feedRow.EmbedJSON, 500), "```")
	}
	if feedRow.LastError != nil {
		lines = append(lines, fmt.Sprintf("Last error (%d consecutive): %s", feedRow.ConsecutiveFailures, *feedRow.LastError))
	}
	seenCount, _ := ctx.Store.CountSeen(feedRow.ID)
	lines = append(lines, fmt.Sprintf("Seen articles: %d", seenCount))
	if len(filters) > 0 {
		lines = append(lines, "Filters:")
		for _, f := range filters {
			regexLabel := ""
			if f.IsRegex {
				regexLabel = " (regex)"
			}
			lines = append(lines, fmt.Sprintf("  - `#%d` %s %s%s: `%s`", f.ID, f.Kind, f.Field, regexLabel, f.Pattern))
		}
	}
	chunks := chunkLines(lines, 1900)
	_ = c.replyEphemeral(chunks[0])
	for _, chunk := range chunks[1:min(5, len(chunks))] {
		_ = c.followUpEphemeral(chunk)
	}
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func pauseSuffix(reason *string) string {
	if reason != nil {
		return " (" + *reason + ")"
	}
	return ""
}

func maxAgeLabel(days int) string {
	if days == 0 {
		return "disabled"
	}
	return fmt.Sprintf("%d days", days)
}

func feedPauseResume(ctx *BotContext, c *icc, resume bool) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	if resume {
		nextCheck := int64(0)
		enabled := true
		if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{
			Enabled: &enabled, ClearPauseReason: true, NextCheckAt: &nextCheck,
		}); err != nil {
			_ = c.replyEphemeral("Database error: " + err.Error())
			return
		}
		_ = c.replyEphemeral(fmt.Sprintf("▶️ Feed **%s** resumed.", feedRow.Name))
		return
	}
	enabled := false
	reason := "manual"
	if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{
		Enabled: &enabled, PauseReason: &reason,
	}); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("⏸️ Feed **%s** paused.", feedRow.Name))
}

func feedMove(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	channel := c.optChannel("channel")
	if channel == nil {
		_ = c.replyEphemeral("The target must be a text channel I can write to.")
		return
	}
	if !isSendableChannel(channel) {
		_ = c.replyEphemeral("The target must be a text channel I can write to.")
		return
	}
	// Moving requires write access on the destination channel too.
	if !ctx.canManageChannel(c.Interaction, channel.ID) {
		denyChannelAccess(c, channel.ID)
		return
	}
	if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{
		ChannelID: &channel.ID, ClearPauseReason: true,
	}); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("📦 Feed **%s** moved to <#%s>.", feedRow.Name, channel.ID))
}

// isSendableChannel reports whether a channel is text-based and writable
// (guild text, announcement, and threads — not categories/voice/forums).
func isSendableChannel(ch *discordgo.Channel) bool {
	switch ch.Type {
	case discordgo.ChannelTypeGuildText,
		discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread:
		return true
	default:
		return false
	}
}

func feedTemplateModal(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	title := fmt.Sprintf("Template — %s", truncateRunes(feedRow.Name, 40))
	_ = c.showModal("feed-template:"+feedRow.ID, title, discordgo.TextInput{
		CustomID: "template",
		Label:    "Placeholders: {title} {link} {description}…",
		Style:    discordgo.TextInputParagraph,
		Value:    feedRow.TemplateText,
		Required: true,
	})
}

func feedEmbedModal(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	value := ""
	if feedRow.EmbedJSON != nil {
		value = *feedRow.EmbedJSON
	}
	title := fmt.Sprintf("Embed JSON — %s", truncateRunes(feedRow.Name, 40))
	_ = c.showModal("feed-embed:"+feedRow.ID, title, discordgo.TextInput{
		CustomID: "embed",
		Label:    "Embed template JSON (empty = none)",
		Style:    discordgo.TextInputParagraph,
		Value:    value,
	})
}

func feedFrequency(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	minutes := int(derefI64(c.optInt("minutes")))
	if err := ctx.Store.UpdateFeed(feedRow.ID, store.FeedPatch{IntervalMinutes: &minutes}); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("⏱️ Feed **%s** will now be checked every %d min.", feedRow.Name, minutes))
}

func derefI64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func feedBatch(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	mode := store.BatchMode(deref(c.optString("mode")))
	patch := store.FeedPatch{BatchMode: &mode}
	if threshold := c.optInt("threshold"); threshold != nil {
		t := int(*threshold)
		patch.BatchThreshold = &t
	}
	if err := ctx.Store.UpdateFeed(feedRow.ID, patch); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("📚 Batch mode of **%s** set to `%s`.", feedRow.Name, mode))
}

func feedFilterAdd(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	kind := store.FilterKind(deref(c.optString("kind")))
	field := store.FilterField(deref(c.optString("field")))
	text := deref(c.optString("text"))
	isRegex := derefB(c.optBool("regex"))
	if isRegex {
		if _, err := regexp.Compile(text); err != nil {
			_ = c.replyEphemeral("Invalid regex: " + err.Error())
			return
		}
	}
	if err := ctx.Store.AddFilter(feedRow.ID, kind, field, text, isRegex); err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	regexLabel := ""
	if isRegex {
		regexLabel = " (regex)"
	}
	_ = c.replyEphemeral(fmt.Sprintf("🔎 Filter added on **%s**: %s %s%s: `%s`", feedRow.Name, kind, field, regexLabel, text))
}

func derefB(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func feedFilterRemove(ctx *BotContext, c *icc) {
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	filterID := derefI64(c.optInt("filter_id"))
	removed, err := ctx.Store.RemoveFilter(feedRow.ID, filterID)
	if err != nil {
		_ = c.replyEphemeral("Database error: " + err.Error())
		return
	}
	if removed {
		_ = c.replyEphemeral(fmt.Sprintf("🗑️ Filter #%d removed.", filterID))
		return
	}
	_ = c.replyEphemeral(fmt.Sprintf("Filter #%d not found on **%s**.", filterID, feedRow.Name))
}

func feedTest(ctx *BotContext, c *icc) {
	if err := c.deferReply(); err != nil {
		return
	}
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	n := 1
	if v := c.optInt("n"); v != nil {
		n = int(*v)
	}
	mode := deref(c.optString("mode"))
	if mode == "" {
		mode = "preview"
	}
	// Real sends are capped lower to avoid flooding the channel.
	if mode == "send" && n > 3 {
		n = 3
	}

	result, fetchErr := feed.FetchFeed(context.Background(), feedRow.URL, feed.FetchOptions{})
	if fetchErr != nil {
		_ = c.editReply("Failed to fetch feed: " + fetchErr.Error())
		return
	}
	if result.Status == feed.StatusNotModified {
		_ = c.editReply("Feed unchanged since last check (HTTP 304) — nothing to preview. Use `/debug feed` for diagnostics.")
		return
	}
	articles := result.Articles
	if len(articles) > n {
		articles = articles[len(articles)-n:]
	}
	// Reverse: latest first.
	for i, j := 0, len(articles)-1; i < j; i, j = i+1, j-1 {
		articles[i], articles[j] = articles[j], articles[i]
	}

	sent := 0
	for _, article := range articles {
		payload := render.BuildArticlePayload(feedRow.TemplateText, feedRow.EmbedJSON, render.TemplateContext{
			Article: article, FeedName: feedRow.Name, Timezone: ctx.Config.Timezone,
		})
		if mode == "send" {
			// TS checked interaction.channel; sending to the interaction
			// channel is equivalent.
			if _, err := c.Session.ChannelMessageSendComplex(c.channelID(), &discordgo.MessageSend{
				Content: payload.Content, Embeds: payload.Embeds,
			}); err != nil {
				_ = c.editReply("Cannot send in this channel.")
				return
			}
			sent++
		} else {
			if _, err := c.Session.FollowupMessageCreate(c.Interaction, true, &discordgo.WebhookParams{
				Content: payload.Content, Embeds: payload.Embeds, Flags: ephemeralFlags,
			}); err != nil {
				log.Warn("feed test followup failed", "err", err)
			}
		}
	}

	if mode == "send" {
		_ = c.editReply(fmt.Sprintf("✅ Simulated %d send(s) to this channel with the current template. **State unchanged** (no article marked seen).", sent))
		return
	}
	_ = c.editReply(fmt.Sprintf("👀 Preview of %d article(s) (ephemeral). Use mode `send` to post a real message in this channel.", len(articles)))
}

func feedRefresh(ctx *BotContext, c *icc) {
	if err := c.deferReply(); err != nil {
		return
	}
	feedRow := getFeedOrReply(ctx, c)
	if feedRow == nil {
		return
	}
	outcome := ctx.Pipeline.CheckFeed(context.Background(), feedRow.ID, store.NowMillis())
	switch outcome.Status {
	case "error":
		_ = c.editReply("❌ Check failed: " + outcome.Error)
	case "delivered":
		_ = c.editReply(fmt.Sprintf("✅ Checked — %d new article(s) sent.", outcome.FreshCount))
	default:
		_ = c.editReply("✅ Checked — nothing new.")
	}
}

var _ = time.Now
