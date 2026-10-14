package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) (*FeedStore, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(OpenOptions{DBPath: dbPath, EnsureDir: false})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return NewFeedStore(db), func() { db.Close() }
}

func TestMigrationsApplied(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()
	// The schema_migrations table must record both versions.
	rows, err := st.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	versions := []int{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Errorf("versions = %v", versions)
	}
}

func TestFeedCRUD(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()

	feed, err := st.AddFeed(NewFeed{
		ID: "f1", Name: "Test", URL: "https://example.com/rss",
		GuildID: "g", ChannelID: "c",
		IntervalMinutes: 10, HasInterval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !feed.Enabled || feed.BatchMode != BatchAuto || feed.TemplateText != "**{title}**\n{link}" {
		t.Errorf("defaults not applied: %+v", feed)
	}

	exists, err := st.Exists("https://example.com/rss", "c")
	if err != nil || !exists {
		t.Errorf("exists = %v, %v", exists, err)
	}

	// Duplicate (url, channel) must fail.
	if _, err := st.AddFeed(NewFeed{ID: "f2", URL: "https://example.com/rss", GuildID: "g", ChannelID: "c"}); err == nil {
		t.Error("duplicate (url, channel) must be rejected")
	}

	enabled := false
	reason := "manual"
	if err := st.UpdateFeed("f1", FeedPatch{Enabled: &enabled, PauseReason: &reason}); err != nil {
		t.Fatal(err)
	}
	feed, _ = st.GetFeed("f1")
	if feed.Enabled || *feed.PauseReason != "manual" {
		t.Errorf("update failed: %+v", feed)
	}

	// Clearing the pause reason must set NULL.
	if err := st.UpdateFeed("f1", FeedPatch{ClearPauseReason: true}); err != nil {
		t.Fatal(err)
	}
	feed, _ = st.GetFeed("f1")
	if feed.PauseReason != nil {
		t.Errorf("pause reason not cleared: %+v", feed.PauseReason)
	}

	due, err := st.GetFeedsDueNow(9999999999999, 10)
	if err != nil || len(due) != 0 {
		t.Errorf("disabled feed must not be due: %v %v", len(due), err)
	}

	if err := st.DeleteFeed("f1"); err != nil {
		t.Fatal(err)
	}
	feed, _ = st.GetFeed("f1")
	if feed != nil {
		t.Error("feed not deleted")
	}
}

func TestFiltersAndSeen(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()

	if _, err := st.AddFeed(NewFeed{ID: "f1", URL: "u", GuildID: "g", ChannelID: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFilter("f1", FilterInclude, FieldTitle, "pattern", true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFilter("f1", FilterExclude, FieldAny, "bad", false); err != nil {
		t.Fatal(err)
	}
	filters, _ := st.GetFilters("f1")
	if len(filters) != 2 || !filters[0].IsRegex {
		t.Fatalf("filters = %+v", filters)
	}
	removed, err := st.RemoveFilter("f1", filters[0].ID)
	if err != nil || !removed {
		t.Errorf("remove = %v %v", removed, err)
	}

	seen := []string{"a", "b"}
	published := []*int64{}
	if err := st.MarkSeen("f1", seen, published); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := st.MarkSeen("f1", seen, published); err != nil {
		t.Fatal(err)
	}
	keys, _ := st.GetSeenKeys("f1")
	if len(keys) != 2 {
		t.Errorf("seen keys = %v", keys)
	}
	n, _ := st.CountSeen("f1")
	if n != 2 {
		t.Errorf("countSeen = %d", n)
	}

	// CASCADE delete must clear seen rows.
	if err := st.DeleteFeed("f1"); err != nil {
		t.Fatal(err)
	}
	keys, _ = st.GetSeenKeys("f1")
	if len(keys) != 0 {
		t.Errorf("seen keys must cascade: %v", keys)
	}
}

func TestChannelSettings(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()

	value := "12345"
	if err := st.SetChannelSetting("c1", "alert_user_id", &value); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetChannelSetting("c1", "alert_user_id")
	if got == nil || *got != value {
		t.Errorf("channel setting = %v", got)
	}
	if err := st.SetChannelSetting("c1", "alert_user_id", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetChannelSetting("c1", "alert_user_id")
	if got != nil {
		t.Errorf("channel setting not cleared: %v", got)
	}
}

func TestKV(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kv.db")
	db, err := Open(OpenOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := KVSet(db, "last_scheduler_tick", "2024-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	value, err := KVGet(db, "last_scheduler_tick")
	if err != nil || value == nil || *value != "2024-01-01T00:00:00Z" {
		t.Errorf("kv get = %v %v", value, err)
	}
	missing, _ := KVGet(db, "nope")
	if missing != nil {
		t.Error("missing key must be nil")
	}
}
