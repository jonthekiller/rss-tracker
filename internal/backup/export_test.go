package backup

import (
	"encoding/json"
	"strings"
	"testing"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func TestNormalizeBackupV2(t *testing.T) {
	data := []byte(`{"version":2,"exportedAt":"2024-01-01T00:00:00.000Z","channels":[{"channelId":"c1","settings":{"alert_user_id":"42"},"feeds":[{"id":"f1","name":"N","url":"u","guildId":"g","channelId":"c1","enabled":true,"intervalMinutes":10,"templateText":"t","embedJson":null,"batchMode":"auto","batchThreshold":5,"batchMaxItems":15,"maxAgeDays":30,"pauseReason":null,"filters":[],"seenKeys":["k1"]}]}]}`)
	file, err := NormalizeBackup(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != 2 || len(file.Channels) != 1 || len(file.Channels[0].Feeds) != 1 {
		t.Errorf("file = %+v", file)
	}
	if file.Channels[0].Settings["alert_user_id"] == nil || *file.Channels[0].Settings["alert_user_id"] != "42" {
		t.Errorf("settings = %v", file.Channels[0].Settings)
	}
	if len(file.Channels[0].Feeds[0].SeenKeys) != 1 || file.Channels[0].Feeds[0].SeenKeys[0] != "k1" {
		t.Errorf("seenKeys = %v", file.Channels[0].Feeds[0].SeenKeys)
	}
}

func TestNormalizeBackupV1(t *testing.T) {
	data := []byte(`{"version":1,"exportedAt":"x","feeds":[{"id":"f1","name":"N","url":"u","channelId":"c1"},{"id":"f2","name":"M","url":"v","channel":"c2"},{"id":"f3","name":"X","url":"w"}]}`)
	file, err := NormalizeBackup(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != 2 || len(file.Channels) != 2 {
		t.Fatalf("normalized = %+v", file)
	}
	if file.Channels[0].ChannelID != "c1" || file.Channels[1].ChannelID != "c2" {
		t.Errorf("channels = %v", file.Channels)
	}
}

func TestNormalizeBackupInvalid(t *testing.T) {
	for _, data := range []string{
		`not json`,
		`[]`,
		`{"version":3}`,
		`{"version":2}`,
		`{"version":1}`,
	} {
		if _, err := NormalizeBackup([]byte(data)); err == nil {
			t.Errorf("must reject %s", data)
		}
	}
}

func TestExportRestoreRoundTrip(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	addSeedFeeds(t, st)

	exported, err := ExportBackup(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonMarshal(exported)
	if err != nil {
		t.Fatal(err)
	}

	// Restore into a fresh store: identical state.
	st2 := newTestStore(t)
	defer st2.Close()
	results, err := RestoreBackup(st2, encoded, RestoreOptions{
		GuildID:               "g",
		ChannelExists:         func(string, string) (bool, error) { return true, nil },
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },
		Baseline:              func(string, string) (bool, string, string) { return true, "", "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	feed2, _ := st2.GetFeed("f1")
	if feed2 == nil || feed2.Name != "Feed A" || feed2.ChannelID != "c1" {
		t.Errorf("feed2 = %+v", feed2)
	}
	filters, _ := st2.GetFilters("f1")
	if len(filters) != 1 || filters[0].Pattern != "keep" {
		t.Errorf("filters = %+v", filters)
	}
	seen, _ := st2.GetSeenKeys("f1")
	if !seen["key-1"] || !seen["key-2"] {
		t.Errorf("seen keys not restored: %v", seen)
	}
	setting, _ := st2.GetChannelSetting("c1", "alert_user_id")
	if setting == nil || *setting != "42" {
		t.Errorf("settings not restored: %v", setting)
	}
}

func TestRestoreForbiddenWithoutManage(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	data := []byte(`{"version":2,"exportedAt":"x","channels":[{"channelId":"c1","settings":{},"feeds":[{"id":"f1","name":"N","url":"u","guildId":"g","channelId":"c1","enabled":true,"intervalMinutes":10,"templateText":"t","batchMode":"auto","batchThreshold":5,"batchMaxItems":15,"maxAgeDays":30,"filters":[],"seenKeys":[]}]}]}`)
	results, err := RestoreBackup(st, data, RestoreOptions{
		GuildID:               "g",
		ChannelExists:         func(string, string) (bool, error) { return true, nil },
		ActorCanManageChannel: func(string) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "forbidden" {
		t.Errorf("results = %+v", results)
	}
}

func TestRestoreUnknownChannelNoFallback(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	data := []byte(`{"version":2,"exportedAt":"x","channels":[{"channelId":"missing","settings":{},"feeds":[{"id":"f1","name":"N","url":"u","guildId":"g","channelId":"missing","enabled":true,"intervalMinutes":10,"templateText":"t","batchMode":"auto","batchThreshold":5,"batchMaxItems":15,"maxAgeDays":30,"filters":[],"seenKeys":[]}]}]}`)
	results, err := RestoreBackup(st, data, RestoreOptions{
		GuildID:               "g",
		ChannelExists:         func(string, string) (bool, error) { return false, nil },
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "channel_missing" {
		t.Errorf("results = %+v", results)
	}
	if !strings.Contains(results[0].Detail, "missing") {
		t.Errorf("detail = %q", results[0].Detail)
	}
}

func TestRestoreWithChannelMapping(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	data := []byte(`{"version":2,"exportedAt":"x","channels":[{"channelId":"old","settings":{},"feeds":[{"id":"f1","name":"N","url":"u","guildId":"g","channelId":"old","enabled":true,"intervalMinutes":10,"templateText":"t","batchMode":"auto","batchThreshold":5,"batchMaxItems":15,"maxAgeDays":30,"filters":[],"seenKeys":[]}]}]}`)
	results, err := RestoreBackup(st, data, RestoreOptions{
		GuildID:               "g",
		MapChannels:           map[string]string{"old": "new"},
		ChannelExists:         func(string, string) (bool, error) { return false, nil },
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },
		Baseline:              func(string, string) (bool, string, string) { return true, "", "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "restored" {
		t.Errorf("results = %+v", results)
	}
	feed, _ := st.GetFeed("f1")
	if feed == nil || feed.ChannelID != "new" {
		t.Errorf("feed = %+v", feed)
	}
}
