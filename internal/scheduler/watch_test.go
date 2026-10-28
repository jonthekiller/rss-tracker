package scheduler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

const enisaJSONOne = `[
  {"id":"EUVD-1","description":"First vulnerability","datePublished":"Oct 10, 2026, 10:23:20 AM"}
]`

const enisaJSONTwo = `[
  {"id":"EUVD-2","description":"Second vulnerability","datePublished":"Oct 11, 2026, 9:00:00 AM"},
  {"id":"EUVD-1","description":"First vulnerability","datePublished":"Oct 10, 2026, 10:23:20 AM"}
]`

func addWatchFeed(t *testing.T, h *harness, id, channelID, sourceType, extractConfig string) {
	t.Helper()
	if _, err := h.store.AddFeed(store.NewFeed{
		ID: id, Name: "Watch " + id, URL: h.serverURL, GuildID: "g", ChannelID: channelID,
		IntervalMinutes: 10, HasInterval: true,
		SourceType: sourceType, ExtractConfig: strPtr(extractConfig),
	}); err != nil {
		t.Fatal(err)
	}
}

func strPtr(s string) *string { return &s }

func TestWatchJSONFeedDeliversNewItems(t *testing.T) {
	h := newHarness(t, enisaJSONOne, nil)
	addWatchFeed(t, h, "f1", "c1", store.SourceTypeJSON, "")

	// First check baselines without sending ("delivered" only reflects the
	// fresh count — TS parity).
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if msgs := h.sentMessages(); len(msgs) != 0 {
		t.Fatalf("baseline sent %v", msgs)
	}
	if seen, _ := h.store.GetSeenKeys("f1"); len(seen) != 1 {
		t.Fatalf("baseline seen keys = %v", seen)
	}

	h.now += 1000
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(enisaJSONTwo))
	})
	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "delivered" || outcome.FreshCount != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	messages := h.sentMessages()
	if len(messages) != 1 || !strings.Contains(messages[0], "EUVD-2") {
		t.Fatalf("messages = %v", messages)
	}
	// The already-seen EUVD-1 must never be re-sent.
	if strings.Contains(messages[0], "EUVD-1") {
		t.Errorf("old item replayed: %v", messages)
	}
}

func TestWatchAutoSniffsJSONAndHTML(t *testing.T) {
	h := newHarness(t, enisaJSONOne, nil)
	addWatchFeed(t, h, "f1", "c1", store.SourceTypeAuto, "")
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if seen, _ := h.store.GetSeenKeys("f1"); len(seen) != 1 {
		t.Fatalf("json auto baseline seen keys = %v", seen)
	}

	// A second feed watching an HTML page in diff mode.
	addWatchFeed(t, h, "f2", "c2", store.SourceTypeAuto, "")
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><p>Version 1.0</p></body></html>"))
	})
	h.pipeline.CheckFeed(context.Background(), "f2", h.now)
	if seen, _ := h.store.GetSeenKeys("f2"); len(seen) != 1 {
		t.Fatalf("html auto baseline seen keys = %v", seen)
	}

	h.now += 1000
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><p>Version 2.0</p></body></html>"))
	})
	outcome := h.pipeline.CheckFeed(context.Background(), "f2", h.now)
	if outcome.Status != "delivered" || outcome.FreshCount != 1 {
		t.Fatalf("html diff outcome = %+v", outcome)
	}
	if msgs := h.sentMessages(); len(msgs) != 1 || !strings.Contains(msgs[0], "Page updated") {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestWatchInvalidExtractConfigFails(t *testing.T) {
	h := newHarness(t, enisaJSONOne, nil)
	addWatchFeed(t, h, "f1", "c1", store.SourceTypeJSON, `{"nope":true}`)

	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "error" {
		t.Fatalf("outcome = %+v, want error", outcome)
	}
	if !strings.Contains(outcome.Error, "extraction config") {
		t.Errorf("error = %q", outcome.Error)
	}
	feedRow, _ := h.store.GetFeed("f1")
	if feedRow.ConsecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d", feedRow.ConsecutiveFailures)
	}
}

func TestWatchZeroItemsIsEmptyNotError(t *testing.T) {
	h := newHarness(t, `[{"id":"EUVD-1","description":"x"}]`, nil)
	addWatchFeed(t, h, "f1", "c1", store.SourceTypeJSON, "")

	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "empty" {
		t.Fatalf("outcome = %+v, want empty", outcome)
	}
	feedRow, _ := h.store.GetFeed("f1")
	if feedRow.LastError != nil {
		t.Errorf("empty extraction must not be an error: %v", *feedRow.LastError)
	}
}
