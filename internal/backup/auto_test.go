package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

func newTestStore(t *testing.T) *store.FeedStore {
	t.Helper()
	db, err := store.Open(store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store.NewFeedStore(db)
}

func addSeedFeeds(t *testing.T, st *store.FeedStore) {
	t.Helper()
	enabled := true
	if _, err := st.AddFeed(store.NewFeed{ID: "f1", Name: "Feed A", URL: "https://a", GuildID: "g", ChannelID: "c1", Enabled: &enabled, IntervalMinutes: 10, HasInterval: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFeed(store.NewFeed{ID: "f2", Name: "Feed B", URL: "https://b", GuildID: "g", ChannelID: "c2", Enabled: &enabled, IntervalMinutes: 10, HasInterval: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFilter("f1", store.FilterInclude, store.FieldTitle, "keep", false); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSeen("f1", []string{"key-1", "key-2"}, []*int64{}); err != nil {
		t.Fatal(err)
	}
	value := "42"
	if err := st.SetChannelSetting("c1", "alert_user_id", &value); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAutoBackupRetention(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	dir := t.TempDir()
	hour := 3
	for i := 1; i <= 9; i++ {
		// Seed old backup files: rss-backup-YYYY-MM-DD.json.
		path := filepath.Join(dir, "rss-backup-2024-01-"+pad(i)+".json")
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nowUTC = func() time.Time { return time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC) }
	path, err := WriteAutoBackup(AutoBackupOptions{Store: st, Directory: dir, Hour: &hour, Keep: 7})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "rss-backup-2024-01-10.json") {
		t.Errorf("path = %s", path)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 7 {
		t.Errorf("files = %d, want 7 (oldest deleted first)", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "rss-backup-2024-01-03.json")); !os.IsNotExist(err) {
		t.Error("oldest file must be deleted")
	}
}

func pad(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestWriteAutoBackupDisabled(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	path, err := WriteAutoBackup(AutoBackupOptions{Store: st, Directory: t.TempDir(), Hour: nil})
	if err != nil || path != "" {
		t.Errorf("disabled backup must return empty: %v %v", path, err)
	}
}
