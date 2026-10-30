package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrationV4FromV3Database applies migrations 1-3 (a pre-burst-default
// database), then reopens it: v4 must lower the old default threshold (5) of
// non-RSS feeds to 2 while preserving explicit thresholds and RSS feeds.
func TestMigrationV4FromV3Database(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at INTEGER NOT NULL DEFAULT (unixepoch())
	)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range MIGRATIONS[:3] {
		if _, err := db.Exec(m.up); err != nil {
			t.Fatalf("legacy migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name,
		); err != nil {
			t.Fatal(err)
		}
	}
	feeds := []struct {
		id         string
		sourceType string
		threshold  int
	}{
		{"json-default", SourceTypeJSON, 5},
		{"html-explicit", SourceTypeHTML, 7},
		{"rss-default", SourceTypeRSS, 5},
		{"auto-default", SourceTypeAuto, 5},
	}
	for _, f := range feeds {
		if _, err := db.Exec(
			`INSERT INTO feeds (id, name, url, guild_id, channel_id, source_type, batch_threshold)
			 VALUES (?, ?, ?, 'g', 'c', ?, ?)`,
			f.id, f.id, "https://example.com/"+f.id, f.sourceType, f.threshold,
		); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	reopened, err := Open(OpenOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	st := NewFeedStore(reopened)
	want := map[string]int{
		"json-default":  DefaultWatchBatchThreshold,
		"html-explicit": 7,
		"rss-default":   DefaultBatchThreshold,
		"auto-default":  DefaultWatchBatchThreshold,
	}
	for id, threshold := range want {
		feed, err := st.GetFeed(id)
		if err != nil || feed == nil {
			t.Fatalf("%s: %v, %v", id, feed, err)
		}
		if feed.BatchThreshold != threshold {
			t.Errorf("%s: threshold = %d, want %d", id, feed.BatchThreshold, threshold)
		}
	}
}
