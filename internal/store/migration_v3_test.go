package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrationV3FromV2Database applies only migrations 1-2 (a legacy
// database), then reopens it: v3 must add the source columns and legacy
// feeds must read back as rss.
func TestMigrationV3FromV2Database(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
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
	for _, m := range MIGRATIONS[:2] {
		if _, err := db.Exec(m.up); err != nil {
			t.Fatalf("legacy migration %d: %v", m.version, err)
		}
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO feeds (id, name, url, guild_id, channel_id) VALUES ('f1', 'Legacy', 'https://example.com/rss', 'g', 'c')`,
	); err != nil {
		t.Fatal(err)
	}
	db.Close()

	reopened, err := Open(OpenOptions{DBPath: dbPath})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	st := NewFeedStore(reopened)
	feed, err := st.GetFeed("f1")
	if err != nil || feed == nil {
		t.Fatalf("GetFeed: %v, %v", feed, err)
	}
	if feed.SourceType != SourceTypeRSS {
		t.Errorf("legacy feed source type = %q, want rss", feed.SourceType)
	}
	if feed.ExtractConfig != nil {
		t.Errorf("legacy feed extract config = %v, want nil", feed.ExtractConfig)
	}
}

func TestSourceFieldsRoundTrip(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()

	config := `{"itemPath":"data.items","titleField":"{id} — {name}"}`
	added, err := st.AddFeed(NewFeed{
		ID: "w1", Name: "Watch", URL: "https://example.com/api",
		GuildID: "g", ChannelID: "c",
		SourceType: SourceTypeJSON, ExtractConfig: &config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.SourceType != SourceTypeJSON || added.ExtractConfig == nil || *added.ExtractConfig != config {
		t.Fatalf("added = %+v (config %v)", added, added.ExtractConfig)
	}

	fetched, err := st.GetFeed("w1")
	if err != nil || fetched == nil {
		t.Fatal(err)
	}
	if fetched.SourceType != SourceTypeJSON {
		t.Errorf("fetched source type = %q", fetched.SourceType)
	}

	// Patch the config (and clear it with a nil inner pointer).
	newConfig := `{"itemSelector":"li.vuln"}`
	patched := &newConfig
	if err := st.UpdateFeed("w1", FeedPatch{ExtractConfig: &patched}); err != nil {
		t.Fatal(err)
	}
	fetched, _ = st.GetFeed("w1")
	if fetched.ExtractConfig == nil || *fetched.ExtractConfig != newConfig {
		t.Fatalf("patched config = %v", fetched.ExtractConfig)
	}

	htmlType := SourceTypeHTML
	cleared := (*string)(nil)
	if err := st.UpdateFeed("w1", FeedPatch{SourceType: &htmlType, ExtractConfig: &cleared}); err != nil {
		t.Fatal(err)
	}
	fetched, _ = st.GetFeed("w1")
	if fetched.SourceType != SourceTypeHTML || fetched.ExtractConfig != nil {
		t.Errorf("cleared = %+v", fetched)
	}
}

// TestAddFeedDefaultsSourceTypeToRSS guards the legacy insert path.
func TestAddFeedDefaultsSourceTypeToRSS(t *testing.T) {
	st, closeDB := openTestStore(t)
	defer closeDB()

	feed, err := st.AddFeed(NewFeed{ID: "r1", URL: "https://example.com/rss", GuildID: "g", ChannelID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if feed.SourceType != SourceTypeRSS {
		t.Errorf("source type = %q, want rss", feed.SourceType)
	}
}
