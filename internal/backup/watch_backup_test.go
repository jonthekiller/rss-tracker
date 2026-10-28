package backup

import (
	"encoding/json"
	"testing"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// TestWatchSourceBackupRoundTrip exports a non-RSS feed and restores it:
// source type and extraction config must survive.
func TestWatchSourceBackupRoundTrip(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	config := `{"itemPath":"data.items","titleField":"{id} — {name}"}`
	if _, err := st.AddFeed(store.NewFeed{
		ID: "w1", Name: "ENISA", URL: "https://example.com/api",
		GuildID: "g", ChannelID: "c1",
		SourceType: store.SourceTypeJSON, ExtractConfig: &config,
	}); err != nil {
		t.Fatal(err)
	}

	exported, err := ExportBackup(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Channels[0].Feeds[0].SourceType != store.SourceTypeJSON {
		t.Errorf("exported sourceType = %q", exported.Channels[0].Feeds[0].SourceType)
	}
	if exported.Channels[0].Feeds[0].ExtractConfig == nil || *exported.Channels[0].Feeds[0].ExtractConfig != config {
		t.Fatalf("exported extractConfig = %v", exported.Channels[0].Feeds[0].ExtractConfig)
	}

	encoded, err := jsonMarshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	st2 := newTestStore(t)
	defer st2.Close()
	results, err := RestoreBackup(st2, encoded, RestoreOptions{
		GuildID:               "g",
		FallbackChannelID:     "c1",
		MapChannels:           map[string]string{},
		ChannelExists:         func(guildID, channelID string) (bool, error) { return channelID == "c1", nil },
		ActorCanManageChannel: func(channelID string) (bool, error) { return true, nil },
		Baseline:              func(feedID, url string) (bool, string, string) { return true, "", "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "restored" {
		t.Fatalf("results = %+v", results)
	}
	restored, _ := st2.GetFeed("w1")
	if restored.SourceType != store.SourceTypeJSON {
		t.Errorf("restored sourceType = %q", restored.SourceType)
	}
	if restored.ExtractConfig == nil || *restored.ExtractConfig != config {
		t.Errorf("restored extractConfig = %v", restored.ExtractConfig)
	}
}

// TestBackupWithoutSourceTypeIsRSS ensures older backups (TS exports and
// earlier Go versions) restore as RSS feeds.
func TestBackupWithoutSourceTypeIsRSS(t *testing.T) {
	data := []byte(`{"version":2,"exportedAt":"2024-01-01T00:00:00.000Z","channels":[{"channelId":"c1","settings":{},"feeds":[{"id":"f1","name":"N","url":"u","guildId":"g","channelId":"c1","enabled":true,"intervalMinutes":10,"templateText":"t","embedJson":null,"batchMode":"auto","batchThreshold":5,"batchMaxItems":15,"maxAgeDays":30,"pauseReason":null,"filters":[],"seenKeys":["k1"]}]}]}`)
	st := newTestStore(t)
	defer st.Close()

	results, err := RestoreBackup(st, data, RestoreOptions{
		GuildID:               "g",
		FallbackChannelID:     "c1",
		MapChannels:           map[string]string{},
		ChannelExists:         func(guildID, channelID string) (bool, error) { return channelID == "c1", nil },
		ActorCanManageChannel: func(channelID string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "restored" {
		t.Fatalf("results = %+v", results)
	}
	feed, _ := st.GetFeed("f1")
	if feed.SourceType != store.SourceTypeRSS {
		t.Errorf("sourceType = %q, want rss", feed.SourceType)
	}
	if feed.ExtractConfig != nil {
		t.Errorf("extractConfig = %v, want nil", feed.ExtractConfig)
	}
}

// TestExportBackupJSONShape checks the sourceType/extractConfig JSON keys.
func TestExportBackupJSONShape(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	config := `{"itemSelector":"li.vuln"}`
	if _, err := st.AddFeed(store.NewFeed{
		ID: "w1", Name: "Page", URL: "https://example.com/page",
		GuildID: "g", ChannelID: "c1",
		SourceType: store.SourceTypeHTML, ExtractConfig: &config,
	}); err != nil {
		t.Fatal(err)
	}
	exported, err := ExportBackup(st, []string{"c1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Channels []struct {
			Feeds []struct {
				SourceType    string  `json:"sourceType"`
				ExtractConfig *string `json:"extractConfig"`
			} `json:"feeds"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	feedJSON := doc.Channels[0].Feeds[0]
	if feedJSON.SourceType != "html" || feedJSON.ExtractConfig == nil || *feedJSON.ExtractConfig != config {
		t.Errorf("feed JSON = %+v", feedJSON)
	}
}
