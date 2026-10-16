// Package importer imports MonitoRSS v6 backups. Templates and include
// filters are preserved; every feed gets a baseline fetch (existing articles
// marked seen, nothing sent).
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jonthekiller/rss-tracker/internal/backup"
	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// MonitoRssFeed is a backup entry (subset of consumed fields).
type MonitoRssFeed struct {
	Title      *string             `json:"title"`
	URL        *string             `json:"url"`
	Guild      *string             `json:"guild"`
	Channel    *string             `json:"channel"`
	Text       *string             `json:"text"`
	Embeds     []map[string]any    `json:"embeds"`
	Filters    map[string][]string `json:"filters"`
	RFilters   map[string][]string `json:"rfilters"`
	CheckDates *bool               `json:"checkDates"`
}

// MonitoRssBackup is the parsed document.
type MonitoRssBackup struct {
	Feeds   []*MonitoRssFeed `json:"feeds"`
	Profile *struct {
		Name  *string  `json:"name"`
		Alert []string `json:"alert"`
	} `json:"profile"`
}

// ParseMonitoRssBackup validates the document shape.
func ParseMonitoRssBackup(data []byte) (*MonitoRssBackup, error) {
	var parsed json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("Invalid backup: not a JSON object")
	}
	var backupFile MonitoRssBackup
	if err := json.Unmarshal(data, &backupFile); err != nil {
		return nil, fmt.Errorf("Invalid backup: not a JSON object")
	}
	if backupFile.Feeds == nil {
		return nil, fmt.Errorf("Invalid MonitoRSS backup: missing \"feeds\" array")
	}
	return &backupFile, nil
}

// ImportEntryResult is one per-feed import outcome.
type ImportEntryResult struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"` // imported | skipped | error
	Detail string `json:"detail,omitempty"`
}

// ImportOptions carries the import callbacks.
type ImportOptions struct {
	GuildID string
	// FallbackChannelID is the destination when the backup channel is unavailable.
	FallbackChannelID string
	// MapOldChannels maps old channel IDs to new ones.
	MapOldChannels map[string]string
	// ChannelExists reports whether a channel exists in the target guild.
	ChannelExists func(guildID, channelID string) (bool, error)
	// DefaultTemplate is used when the feed has none (MonitoRSS uses its own).
	DefaultTemplate string
	// ActorCanManageChannel reports whether the actor may write in a channel.
	ActorCanManageChannel func(channelID string) (bool, error)
	// Now is an injectable clock (tests).
	Now func() int64
	// HTTPClient is injectable for tests (defaults to http.DefaultClient).
	HTTPClient *http.Client
}

// ImportMonitoRssBackup imports a MonitoRSS backup.
func ImportMonitoRssBackup(st *store.FeedStore, data []byte, options ImportOptions) ([]ImportEntryResult, error) {
	backupFile, err := ParseMonitoRssBackup(data)
	if err != nil {
		return nil, err
	}
	results := []ImportEntryResult{}

	// profile.alert becomes the target channel's failure-alert setting.
	if backupFile.Profile != nil && len(backupFile.Profile.Alert) > 0 {
		profileAlert := backupFile.Profile.Alert[0]
		existing, err := st.GetChannelSetting(options.FallbackChannelID, "alert_user_id")
		if err == nil && existing == nil {
			if err := st.SetChannelSetting(options.FallbackChannelID, "alert_user_id", &profileAlert); err != nil {
				return nil, err
			}
		}
	}

	for _, rawFeed := range backupFile.Feeds {
		name := "Unknown"
		if rawFeed.Title != nil && trim(*rawFeed.Title) != "" {
			name = trim(*rawFeed.Title)
		}
		url := ""
		if rawFeed.URL != nil {
			url = trim(*rawFeed.URL)
		}
		if url == "" {
			results = append(results, ImportEntryResult{Name: name, URL: "(missing)", Status: "skipped", Detail: "No URL in backup entry"})
			continue
		}
		exists, err := st.Exists(url, options.FallbackChannelID)
		if err != nil {
			return nil, err
		}
		mapped := false
		if rawFeed.Channel != nil {
			_, mapped = options.MapOldChannels[*rawFeed.Channel]
		}
		if exists && !mapped {
			results = append(results, ImportEntryResult{
				Name: name, URL: url, Status: "skipped",
				Detail: "This URL already exists for the target channel",
			})
			continue
		}

		resolved, err := resolveChannel(options, rawFeed)
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			channel := "(none)"
			if rawFeed.Channel != nil {
				channel = *rawFeed.Channel
			}
			results = append(results, ImportEntryResult{
				Name: name, URL: url, Status: "skipped",
				Detail: "Channel " + channel + " not found and no fallback",
			})
			continue
		}
		canManage, err := options.ActorCanManageChannel(*resolved)
		if err != nil {
			return nil, err
		}
		if !canManage {
			results = append(results, ImportEntryResult{
				Name: name, URL: url, Status: "skipped",
				Detail: "You need the Send Messages permission in <#" + *resolved + "> to import feeds there",
			})
			continue
		}

		templateText := options.DefaultTemplate
		if rawFeed.Text != nil && trim(*rawFeed.Text) != "" {
			templateText = *rawFeed.Text
		}
		var embedJSON *string
		if len(rawFeed.Embeds) > 0 {
			encoded, err := json.Marshal(rawFeed.Embeds[0])
			if err == nil {
				embedJSON = new(string)
				*embedJSON = string(encoded)
			}
		}

		feedRow, err := st.AddFeed(store.NewFeed{
			ID: backup.NewFeedID(), Name: name, URL: url,
			GuildID: options.GuildID, ChannelID: *resolved,
			TemplateText: templateText, EmbedJSON: embedJSON,
		})
		if err != nil {
			results = append(results, ImportEntryResult{Name: name, URL: url, Status: "error", Detail: err.Error()})
			continue
		}

		// MonitoRSS include filters (plain + regex).
		addFilters(st, feedRow.ID, rawFeed.Filters, false)
		addFilters(st, feedRow.ID, rawFeed.RFilters, true)

		// Baseline: fetch now, mark all current articles seen, never send.
		result, err := feed.FetchFeed(context.Background(), url, feed.FetchOptions{HTTPClient: options.HTTPClient})
		if err != nil {
			now := options.Now
			if now == nil {
				now = store.NowMillis
			}
			if err := st.MarkFailure(feedRow.ID, now(), err.Error()); err != nil {
				return nil, err
			}
			results = append(results, ImportEntryResult{
				Name: name, URL: url, Status: "imported",
				Detail: "Imported, but first fetch failed: " + err.Error(),
			})
			continue
		}
		if result.Status == feed.StatusOK {
			keys := make([]string, len(result.Articles))
			timestamps := make([]*int64, len(result.Articles))
			for i, article := range result.Articles {
				keys[i] = article.Key
				if article.PublishedAt != nil {
					ms := article.PublishedAt.UnixMilli()
					timestamps[i] = &ms
				}
			}
			if err := st.MarkSeen(feedRow.ID, keys, timestamps); err != nil {
				return nil, err
			}
			if name == "Unknown" && result.Meta.Title != nil {
				if err := st.UpdateFeed(feedRow.ID, store.FeedPatch{Name: result.Meta.Title}); err != nil {
					return nil, err
				}
			}
		}

		results = append(results, ImportEntryResult{Name: name, URL: url, Status: "imported"})
	}

	return results, nil
}

func addFilters(st *store.FeedStore, feedID string, filters map[string][]string, isRegex bool) {
	for field, patterns := range filters {
		for _, pattern := range patterns {
			_ = st.AddFilter(feedID, store.FilterInclude, toField(field), pattern, isRegex)
		}
	}
}

func resolveChannel(options ImportOptions, feedRow *MonitoRssFeed) (*string, error) {
	if feedRow.Channel != nil {
		if mapped, ok := options.MapOldChannels[*feedRow.Channel]; ok {
			return &mapped, nil
		}
		exists, err := options.ChannelExists(options.GuildID, *feedRow.Channel)
		if err != nil {
			return nil, err
		}
		if exists {
			channel := *feedRow.Channel
			return &channel, nil
		}
	}
	if options.FallbackChannelID != "" {
		return &options.FallbackChannelID, nil
	}
	return nil, nil
}

func toField(field string) store.FilterField {
	switch field {
	case "title":
		return store.FieldTitle
	case "description":
		return store.FieldDescription
	default:
		return store.FieldAny
	}
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
