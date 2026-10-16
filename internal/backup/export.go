// Package backup implements export/restore of the JSON v2 backup format
// (grouped per channel, compatible with the TypeScript version) plus the
// MonitoRSS v1-style normalization and the daily auto-backup.
package backup

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// BackupFeed is one feed entry of a v2 backup.
type BackupFeed struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	URL             string         `json:"url"`
	GuildID         string         `json:"guildId"`
	ChannelID       string         `json:"channelId"`
	Enabled         bool           `json:"enabled"`
	IntervalMinutes int            `json:"intervalMinutes"`
	TemplateText    string         `json:"templateText"`
	EmbedJSON       *string        `json:"embedJson"`
	BatchMode       string         `json:"batchMode"`
	BatchThreshold  int            `json:"batchThreshold"`
	BatchMaxItems   int            `json:"batchMaxItems"`
	MaxAgeDays      int            `json:"maxAgeDays"`
	SourceType      string         `json:"sourceType,omitempty"`
	ExtractConfig   *string        `json:"extractConfig,omitempty"`
	PauseReason     *string        `json:"pauseReason"`
	Filters         []BackupFilter `json:"filters"`
	SeenKeys        []string       `json:"seenKeys"`
}

// BackupFilter is one filter entry of a v2 backup.
type BackupFilter struct {
	Kind    string `json:"kind"`
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
	IsRegex bool   `json:"isRegex"`
}

// BackupChannel groups feeds and settings per channel.
type BackupChannel struct {
	ChannelID string             `json:"channelId"`
	Settings  map[string]*string `json:"settings"`
	Feeds     []*BackupFeed      `json:"feeds"`
}

// BackupFile is the v2 backup document.
type BackupFile struct {
	Version    int              `json:"version"`
	ExportedAt string           `json:"exportedAt"`
	Channels   []*BackupChannel `json:"channels"`
}

// FeedToBackup converts a feed row to its backup representation.
func FeedToBackup(feedRow *store.Feed, st *store.FeedStore) (*BackupFeed, error) {
	filters, err := st.GetFilters(feedRow.ID)
	if err != nil {
		return nil, err
	}
	backupFilters := make([]BackupFilter, 0, len(filters))
	for _, f := range filters {
		backupFilters = append(backupFilters, BackupFilter{
			Kind: string(f.Kind), Field: string(f.Field), Pattern: f.Pattern, IsRegex: f.IsRegex,
		})
	}
	seen, err := st.GetSeenKeys(feedRow.ID)
	if err != nil {
		return nil, err
	}
	seenKeys := make([]string, 0, len(seen))
	for key := range seen {
		seenKeys = append(seenKeys, key)
	}
	// Deterministic order for stable diffs/backups.
	sortStrings(seenKeys)

	return &BackupFeed{
		ID: feedRow.ID, Name: feedRow.Name, URL: feedRow.URL,
		GuildID: feedRow.GuildID, ChannelID: feedRow.ChannelID,
		Enabled: feedRow.Enabled, IntervalMinutes: feedRow.IntervalMinutes,
		TemplateText: feedRow.TemplateText, EmbedJSON: feedRow.EmbedJSON,
		BatchMode: string(feedRow.BatchMode), BatchThreshold: feedRow.BatchThreshold,
		BatchMaxItems: feedRow.BatchMaxItems, MaxAgeDays: feedRow.MaxAgeDays,
		SourceType: normalizeSourceType(feedRow.SourceType), ExtractConfig: feedRow.ExtractConfig,
		PauseReason: feedRow.PauseReason, Filters: backupFilters, SeenKeys: seenKeys,
	}, nil
}

// ExportBackup exports a backup. When channelIDs is provided, only those
// channels are exported; otherwise every channel.
func ExportBackup(st *store.FeedStore, channelIDs []string) (*BackupFile, error) {
	if channelIDs == nil {
		var err error
		channelIDs, err = st.GetAllChannelIDs()
		if err != nil {
			return nil, err
		}
	}
	groups := []*BackupChannel{}
	for _, channelID := range channelIDs {
		feeds, err := st.GetFeedsForChannel(channelID)
		if err != nil {
			return nil, err
		}
		settings, err := st.GetAllChannelSettings(channelID)
		if err != nil {
			return nil, err
		}
		if len(feeds) == 0 && len(settings) == 0 {
			continue
		}
		backupFeeds := make([]*BackupFeed, 0, len(feeds))
		for _, feedRow := range feeds {
			backupFeed, err := FeedToBackup(feedRow, st)
			if err != nil {
				return nil, err
			}
			backupFeeds = append(backupFeeds, backupFeed)
		}
		groups = append(groups, &BackupChannel{ChannelID: channelID, Settings: settings, Feeds: backupFeeds})
	}
	return &BackupFile{Version: 2, ExportedAt: nowUTC().Format("2006-01-02T15:04:05.000Z07:00"), Channels: groups}, nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// normalizeSourceType defaults empty source types to rss (v1/v2 backups and
// TS exports carry no sourceType).
func normalizeSourceType(sourceType string) string {
	if sourceType == "" {
		return store.SourceTypeRSS
	}
	return sourceType
}

var nowUTC = func() time.Time { return time.Now().UTC() }

// RestoreEntryResult is one per-feed restore outcome.
type RestoreEntryResult struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"` // restored | updated | channel_missing | forbidden | error
	Detail string `json:"detail,omitempty"`
}

// RestoreOptions carries the restore callbacks (Discord command context).
type RestoreOptions struct {
	// GuildID is the guild the feeds belong to.
	GuildID string
	// FallbackChannelID is used when a backup channel does not exist.
	FallbackChannelID string
	// MapChannels maps old channel IDs to new ones.
	MapChannels map[string]string
	// ChannelExists reports whether a channel ID exists in the target guild.
	ChannelExists func(guildID, channelID string) (bool, error)
	// ActorCanManageChannel reports whether the actor may write in a channel.
	ActorCanManageChannel func(channelID string) (bool, error)
	// Baseline marks current articles as seen without sending.
	Baseline func(feedID, url string) (ok bool, feedName string, errMsg string)
}

// NormalizeBackup accepts both v2 backups and legacy v1 (flat feeds, global
// settings dropped).
func NormalizeBackup(data []byte) (*BackupFile, error) {
	var raw struct {
		Version  *int             `json:"version"`
		Feeds    *json.RawMessage `json:"feeds"`
		Channels *json.RawMessage `json:"channels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("Invalid backup: not a JSON object")
	}
	if raw.Version != nil && *raw.Version == 2 {
		if raw.Channels == nil {
			return nil, fmt.Errorf("Invalid backup: missing channels array")
		}
		var file BackupFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, err
		}
		return &file, nil
	}
	if raw.Version != nil && *raw.Version == 1 {
		if raw.Feeds == nil {
			return nil, fmt.Errorf("Invalid backup: missing feeds array")
		}
		// v1: flat feed list with per-feed channelId; global settings dropped.
		var legacyFeeds []struct {
			ChannelID *string         `json:"channelId"`
			Channel   *string         `json:"channel"`
			Rest      json.RawMessage `json:"-"`
		}
		if err := json.Unmarshal(*raw.Feeds, &legacyFeeds); err != nil {
			return nil, fmt.Errorf("Invalid backup: unreadable feeds array")
		}
		var anyFeeds []map[string]any
		if err := json.Unmarshal(*raw.Feeds, &anyFeeds); err != nil {
			return nil, fmt.Errorf("Invalid backup: unreadable feeds array")
		}

		groups := map[string]*BackupChannel{}
		var order []string
		for _, feed := range anyFeeds {
			channelID := ""
			if v, ok := feed["channelId"].(string); ok {
				channelID = v
			} else if v, ok := feed["channel"].(string); ok {
				channelID = v
			}
			if channelID == "" {
				continue
			}
			group, ok := groups[channelID]
			if !ok {
				group = &BackupChannel{ChannelID: channelID, Settings: map[string]*string{}, Feeds: []*BackupFeed{}}
				groups[channelID] = group
				order = append(order, channelID)
			}
			encoded, err := json.Marshal(feed)
			if err != nil {
				continue
			}
			var backupFeed BackupFeed
			if err := json.Unmarshal(encoded, &backupFeed); err != nil {
				continue
			}
			group.Feeds = append(group.Feeds, &backupFeed)
		}
		channels := make([]*BackupChannel, 0, len(order))
		for _, id := range order {
			channels = append(channels, groups[id])
		}
		return &BackupFile{
			Version: 2, ExportedAt: nowUTC().Format("2006-01-02T15:04:05.000Z07:00"), Channels: channels,
		}, nil
	}
	version := "unknown"
	if raw.Version != nil {
		version = fmt.Sprintf("%d", *raw.Version)
	}
	return nil, fmt.Errorf("Unsupported backup version: %s", version)
}

// RestoreBackup restores a backup: upsert feeds by stable UUID, restore
// filters, per-channel settings and seen keys (no duplicates, no replayed
// bursts). Each channel group requires the actor to manage that channel.
func RestoreBackup(st *store.FeedStore, data []byte, options RestoreOptions) ([]RestoreEntryResult, error) {
	backupFile, err := NormalizeBackup(data)
	if err != nil {
		return nil, err
	}
	results := []RestoreEntryResult{}

	for _, group := range backupFile.Channels {
		targetChannel, err := resolveChannel(options, group.ChannelID)
		if err != nil {
			return nil, err
		}
		if targetChannel == nil {
			for _, feedBackup := range group.Feeds {
				results = append(results, RestoreEntryResult{
					Name: feedBackup.Name, URL: feedBackup.URL, Status: "channel_missing",
					Detail: "Channel " + group.ChannelID + " not found in this guild and no fallback provided",
				})
			}
			continue
		}
		canManage, err := options.ActorCanManageChannel(*targetChannel)
		if err != nil {
			return nil, err
		}
		if !canManage {
			for _, feedBackup := range group.Feeds {
				results = append(results, RestoreEntryResult{
					Name: feedBackup.Name, URL: feedBackup.URL, Status: "forbidden",
					Detail: "You need the Send Messages permission in <#" + *targetChannel + "> to restore its feeds",
				})
			}
			continue
		}

		// Per-channel settings.
		for key, value := range group.Settings {
			if err := st.SetChannelSetting(*targetChannel, key, value); err != nil {
				return nil, err
			}
		}

		for _, feedBackup := range group.Feeds {
			result, err := restoreFeed(st, options, feedBackup, *targetChannel)
			if err != nil {
				results = append(results, RestoreEntryResult{
					Name: feedBackup.Name, URL: feedBackup.URL, Status: "error", Detail: err.Error(),
				})
				continue
			}
			results = append(results, result)
		}
	}

	return results, nil
}

func restoreFeed(st *store.FeedStore, options RestoreOptions, feedBackup *BackupFeed, targetChannel string) (RestoreEntryResult, error) {
	existing, err := st.GetFeed(feedBackup.ID)
	if err != nil {
		return RestoreEntryResult{}, err
	}
	batchMode := store.BatchMode(feedBackup.BatchMode)
	sourceType := normalizeSourceType(feedBackup.SourceType)
	if existing != nil {
		// Existing feeds keep their live channel (never moved by a restore).
		pauseReason := feedBackup.PauseReason
		if err := st.UpdateFeed(feedBackup.ID, store.FeedPatch{
			Name:             &feedBackup.Name,
			Enabled:          &feedBackup.Enabled,
			IntervalMinutes:  &feedBackup.IntervalMinutes,
			TemplateText:     &feedBackup.TemplateText,
			EmbedJSON:        &feedBackup.EmbedJSON,
			BatchMode:        &batchMode,
			BatchThreshold:   &feedBackup.BatchThreshold,
			BatchMaxItems:    &feedBackup.BatchMaxItems,
			MaxAgeDays:       &feedBackup.MaxAgeDays,
			SourceType:       &sourceType,
			ExtractConfig:    &feedBackup.ExtractConfig,
			PauseReason:      pauseReason,
			ClearPauseReason: pauseReason == nil,
		}); err != nil {
			return RestoreEntryResult{}, err
		}
	} else {
		enabled := feedBackup.Enabled
		if _, err := st.AddFeed(store.NewFeed{
			ID: feedBackup.ID, Name: feedBackup.Name, URL: feedBackup.URL,
			GuildID: options.GuildID, ChannelID: targetChannel,
			Enabled: &enabled, IntervalMinutes: feedBackup.IntervalMinutes, HasInterval: true,
			TemplateText: feedBackup.TemplateText, EmbedJSON: feedBackup.EmbedJSON,
			BatchMode: batchMode, HasBatchMode: true,
			BatchThreshold: feedBackup.BatchThreshold, HasThreshold: true,
			BatchMaxItems: feedBackup.BatchMaxItems, HasMaxItems: true,
			MaxAgeDays: feedBackup.MaxAgeDays, HasMaxAgeDays: true,
			SourceType: sourceType, ExtractConfig: feedBackup.ExtractConfig,
			PauseReason: feedBackup.PauseReason,
		}); err != nil {
			return RestoreEntryResult{}, err
		}
	}

	feedChannel := targetChannel
	if existing != nil {
		feedChannel = existing.ChannelID
	}

	// Filters: replace wholesale.
	if err := st.RemoveAllFilters(feedBackup.ID); err != nil {
		return RestoreEntryResult{}, err
	}
	for _, filter := range feedBackup.Filters {
		if err := st.AddFilter(
			feedBackup.ID, store.FilterKind(filter.Kind), store.FilterField(filter.Field),
			filter.Pattern, filter.IsRegex,
		); err != nil {
			return RestoreEntryResult{}, err
		}
	}

	// Seen keys: restore to avoid re-sending old articles.
	status := "restored"
	if existing != nil {
		status = "updated"
	}
	if len(feedBackup.SeenKeys) > 0 {
		timestamps := make([]*int64, len(feedBackup.SeenKeys))
		if err := st.MarkSeen(feedBackup.ID, feedBackup.SeenKeys, timestamps); err != nil {
			return RestoreEntryResult{}, err
		}
	} else {
		// Baseline fetch for feeds restored without seen keys.
		ok, feedName, errMsg := options.Baseline(feedBackup.ID, feedBackup.URL)
		if !ok {
			return RestoreEntryResult{
				Name: feedBackup.Name, URL: feedBackup.URL, Status: status,
				Detail: "Restored, but baseline fetch failed: " + orDefault(errMsg, "unknown") + ". It will retry automatically.",
			}, nil
		}
		if feedBackup.Name == "Unknown" && feedName != "" {
			name := feedName
			if err := st.UpdateFeed(feedBackup.ID, store.FeedPatch{Name: &name}); err != nil {
				return RestoreEntryResult{}, err
			}
		}
	}

	detail := ""
	if feedChannel != feedBackup.ChannelID {
		detail = "Channel: <#" + feedChannel + ">"
	}
	return RestoreEntryResult{Name: feedBackup.Name, URL: feedBackup.URL, Status: status, Detail: detail}, nil
}

func resolveChannel(options RestoreOptions, backupChannelID string) (*string, error) {
	if mapped, ok := options.MapChannels[backupChannelID]; ok {
		return &mapped, nil
	}
	exists, err := options.ChannelExists(options.GuildID, backupChannelID)
	if err != nil {
		return nil, err
	}
	if exists {
		return &backupChannelID, nil
	}
	if options.FallbackChannelID != "" {
		return &options.FallbackChannelID, nil
	}
	return nil, nil
}

// NewFeedID generates a stable UUID for new feeds.
func NewFeedID() string {
	return newUUID()
}

func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}
