package importer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

const backupFixture = `{
  "profile": {"name": "monito", "alert": ["999"]},
  "feeds": [
    {
      "title": "Tech News",
      "url": "%URL%",
      "guild": "g",
      "channel": "old-channel",
      "text": ":newspaper: | **{title}**\n{link}",
      "filters": {"title": ["keep"]},
      "rfilters": {"description": ["release \\d"]}
    },
    {"title": "No URL", "channel": "x"},
    {"title": "Forbidden", "url": "https://example.com/forbidden", "channel": "chan"}
  ]
}`

func TestImportMonitoRss(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss version="2.0"><channel><title>Tech News Feed</title><item><guid>g1</guid><title>One</title><link>https://a</link></item></channel></rss>`))
	}))
	defer server.Close()

	data := []byte(replaceAll(backupFixture, "%URL%", server.URL+"/rss"))
	results, err := ImportMonitoRssBackup(st, data, ImportOptions{
		GuildID:               "g",
		FallbackChannelID:     "fallback",
		ChannelExists:         func(_, channel string) (bool, error) { return channel == "chan", nil },
		ActorCanManageChannel: func(channel string) (bool, error) { return channel != "chan", nil },
		DefaultTemplate:       "**{title}**\n{link}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Status != "imported" || results[1].Status != "skipped" || results[2].Status != "skipped" {
		t.Errorf("statuses = %v", results)
	}

	feeds, _ := st.GetFeedsForChannel("fallback")
	if len(feeds) != 1 {
		t.Fatalf("feeds = %d", len(feeds))
	}
	feedRow := feeds[0]
	if feedRow.Name != "Tech News" { // backup title kept (rename only for Unknown)
		t.Errorf("name = %q", feedRow.Name)
	}
	if feedRow.TemplateText != ":newspaper: | **{title}**\n{link}" {
		t.Errorf("template = %q", feedRow.TemplateText)
	}
	filters, _ := st.GetFilters(feedRow.ID)
	if len(filters) != 2 {
		t.Fatalf("filters = %+v", filters)
	}
	if filters[0].Pattern != "keep" || filters[0].IsRegex {
		t.Errorf("filter 1 = %+v", filters[0])
	}
	if filters[1].Pattern != `release \d` || !filters[1].IsRegex {
		t.Errorf("filter 2 = %+v", filters[1])
	}
	seen, _ := st.GetSeenKeys(feedRow.ID)
	if len(seen) != 1 {
		t.Errorf("seen = %v", seen)
	}
	// profile.alert mapped to the fallback channel.
	alert, _ := st.GetChannelSetting("fallback", "alert_user_id")
	if alert == nil || *alert != "999" {
		t.Errorf("alert = %v", alert)
	}
}

func TestImportRejectsInvalid(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	if _, err := ImportMonitoRssBackup(st, []byte(`{"nope":true}`), ImportOptions{}); err == nil {
		t.Error("must reject non-MonitoRSS JSON")
	}
	if _, err := ImportMonitoRssBackup(st, []byte(`[]`), ImportOptions{}); err == nil {
		t.Error("must reject non-objects")
	}
}

func TestImportSkipsExistingURL(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()
	if _, err := st.AddFeed(store.NewFeed{ID: "f1", Name: "X", URL: "https://example.com/exists", GuildID: "g", ChannelID: "fallback"}); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"feeds":[{"title":"X","url":"https://example.com/exists"}]}`)
	results, err := ImportMonitoRssBackup(st, data, ImportOptions{
		GuildID:               "g",
		FallbackChannelID:     "fallback",
		ActorCanManageChannel: func(string) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "skipped" {
		t.Errorf("results = %+v", results)
	}
}

func replaceAll(s, old, new string) string {
	out := ""
	for {
		idx := indexOf(s, old)
		if idx < 0 {
			return out + s
		}
		out += s[:idx] + new
		s = s[idx+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestFixtureFileIsParseable(t *testing.T) {
	// The repository fixture (testdata/monitorss-backup.json) is the reference
	// format captured from a real MonitoRSS v6 backup; it must stay parseable.
	// It is excluded from the public repository (real Discord IDs), so skip
	// when absent.
	data, err := os.ReadFile("testdata/monitorss-backup.json")
	if os.IsNotExist(err) {
		t.Skip("fixture excluded from the public repository")
	}
	if err != nil {
		t.Fatalf("fixture unreadable: %v", err)
	}
	if _, err := ParseMonitoRssBackup(data); err != nil {
		t.Fatalf("fixture no longer parseable: %v", err)
	}
}
