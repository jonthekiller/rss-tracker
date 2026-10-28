package store

import (
	"os"
	"testing"
)

// TestInteropWithProductionDB opens a copy of a real 1.x database and verifies
// full read compatibility (skipped unless INTEROP_DB is set).
func TestInteropWithProductionDB(t *testing.T) {
	path := os.Getenv("INTEROP_DB")
	if path == "" {
		t.Skip("INTEROP_DB not set")
	}
	db, err := Open(OpenOptions{DBPath: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	st := NewFeedStore(db)
	feeds, err := st.GetAllFeeds()
	if err != nil {
		t.Fatalf("feeds: %v", err)
	}
	totalSeen := 0
	for _, f := range feeds {
		n, _ := st.CountSeen(f.ID)
		totalSeen += n
		if _, err := st.GetFilters(f.ID); err != nil {
			t.Fatalf("filters: %v", err)
		}
	}
	t.Logf("feeds=%d totalSeenKeys=%d", len(feeds), totalSeen)
	if _, err := KVGet(db, "last_scheduler_tick"); err != nil {
		t.Fatalf("kv: %v", err)
	}
	ids, err := st.GetAllChannelIDs()
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	t.Logf("channelsWithState=%d", len(ids))
}
