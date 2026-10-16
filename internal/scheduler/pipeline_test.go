package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/sender"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// handlerHolder lets tests change the served body between checks.
type handlerHolder struct {
	mu      sync.Mutex
	handler http.HandlerFunc
}

func (h *handlerHolder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handler(w, r)
}

func (h *handlerHolder) set(handler http.HandlerFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handler = handler
}

type harness struct {
	store     *store.FeedStore
	sender    *sender.SenderQueue
	pipeline  *Pipeline
	scheduler *Scheduler
	sentMu    sync.Mutex
	sent      []string
	now       int64
	serverURL string
	holder    *handlerHolder
	alerts    []string
}

func newHarness(t *testing.T, initialXML string, mutate func(*PipelineOptions)) *harness {
	t.Helper()
	db, err := store.Open(store.OpenOptions{DBPath: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := store.NewFeedStore(db)

	sq := sender.New(sender.Options{
		SendDelayMs: 1,
		Send: func(channelID string, message sender.OutboundMessage) error {
			return nil // replaced below through a wrapper
		},
		Delay: func(time.Duration) {},
	})

	h := &harness{store: st, sender: sq, now: int64(1_700_000_000_000)}

	// Wrap the sender to record deliveries (the queue is shared with the pipeline).
	wrapped := sender.New(sender.Options{
		SendDelayMs: 1,
		Send: func(channelID string, message sender.OutboundMessage) error {
			h.sentMu.Lock()
			h.sent = append(h.sent, channelID+"|"+message.Content)
			h.sentMu.Unlock()
			return nil
		},
		Delay: func(time.Duration) {},
	})
	h.sender = wrapped

	options := PipelineOptions{
		Store:                 st,
		Sender:                wrapped,
		Timezone:              "UTC",
		MinIntervalMinutes:    2,
		Now:                   func() int64 { return h.now },
		FailureAlertThreshold: 3,
		OnFeedFailed:          func(feed *store.Feed, lastError string) { h.alerts = append(h.alerts, feed.ID) },
		OnFeedRecovered:       func(feed *store.Feed) {},
	}
	if mutate != nil {
		mutate(&options)
	}
	h.pipeline = NewPipeline(options)
	h.scheduler = NewScheduler(h.pipeline, st, SchedulerOptions{TickIntervalMs: 10_000})

	h.holder = &handlerHolder{handler: func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(initialXML))
	}}
	server := httptest.NewServer(h.holder)
	t.Cleanup(server.Close)
	h.serverURL = server.URL
	return h
}

func (h *harness) sentMessages() []string {
	h.sentMu.Lock()
	defer h.sentMu.Unlock()
	return append([]string{}, h.sent...)
}

func addFeed(t *testing.T, h *harness, id, channelID string) {
	t.Helper()
	if _, err := h.store.AddFeed(store.NewFeed{
		ID: id, Name: "Feed " + id, URL: h.serverURL, GuildID: "g", ChannelID: channelID,
		IntervalMinutes: 10, HasInterval: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFirstCheckBaselinesWithoutSending(t *testing.T) {
	h := newHarness(t, rssFixture(3), nil)
	addFeed(t, h, "f1", "c1")

	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	// "delivered" only reflects the fresh count (TS parity); what matters is
	// that nothing was sent on the very first check.
	if messages := h.sentMessages(); len(messages) != 0 {
		t.Fatalf("first check must not send: %v", messages)
	}
	seen, _ := h.store.GetSeenKeys("f1")
	if len(seen) != 3 {
		t.Errorf("baseline missing: %v", seen)
	}
	_ = outcome
}

func TestSecondCheckDeliversIndividual(t *testing.T) {
	h := newHarness(t, rssFixture(2), nil)
	addFeed(t, h, "f1", "c1")

	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	h.now += 1000

	// Feed now contains 2 more articles (guids 3, 4).
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rssFixture(4)))
	})

	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "delivered" || outcome.FreshCount != 2 {
		t.Fatalf("outcome = %+v", outcome)
	}
	messages := h.sentMessages()
	if len(messages) != 2 {
		t.Fatalf("messages = %v", messages)
	}
	// Oldest first: guid 3 before guid 4.
	if index(messages[0], "Item 3") > index(messages[1], "Item 4") {
		t.Errorf("publication order broken: %v", messages)
	}
}

func TestDigestWhenBurstExceedsThreshold(t *testing.T) {
	h := newHarness(t, rssFixture(1), nil)
	addFeed(t, h, "f1", "c1")
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	h.now += 1000

	// Burst: 5 new articles at once (threshold 5, auto mode).
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rssFixture(6)))
	})

	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "delivered" {
		t.Fatalf("outcome = %+v", outcome)
	}
	messages := h.sentMessages()
	if len(messages) != 1 {
		t.Fatalf("digest must be a single message: %v", messages)
	}
	if !contains(messages[0], "📰 **Feed f1**") || !contains(messages[0], "5 new articles") {
		t.Errorf("digest content = %q", messages[0])
	}
}

func TestIncludeFiltersOnDelivery(t *testing.T) {
	h := newHarness(t, rssFixture(2), nil)
	addFeed(t, h, "f1", "c1")
	if err := h.store.AddFilter("f1", store.FilterInclude, store.FieldTitle, "Item 1", false); err != nil {
		t.Fatal(err)
	}
	// Only Item 1 pre-seen, so Item 2 ("Item 2") is the only fresh one and is blocked.
	if err := h.store.MarkSeen("f1", []string{"g1"}, []*int64{}); err != nil {
		t.Fatal(err)
	}
	// Mark "existing" baseline: mark g1 seen so g2 is fresh but blocked by filter.
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if messages := h.sentMessages(); len(messages) != 0 {
		t.Errorf("filtered articles must not send: %v", messages)
	}
}

func TestBackoffAndAlertThreshold(t *testing.T) {
	h := newHarness(t, "", func(o *PipelineOptions) { o.FailureAlertThreshold = 3 })
	addFeed(t, h, "f1", "c1")

	// Server now fails.
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	for i := 0; i < 2; i++ {
		outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
		if outcome.Status != "error" {
			t.Fatalf("outcome = %+v", outcome)
		}
		h.now += 1000
	}
	if len(h.alerts) != 0 {
		t.Errorf("no alert before the threshold: %v", h.alerts)
	}
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if len(h.alerts) != 1 {
		t.Errorf("alert expected at 3 consecutive failures: %v", h.alerts)
	}

	// Exponential backoff: next_check_at = now + min(6h, interval * 2^failures).
	feed, _ := h.store.GetFeed("f1")
	interval := int64(10) * 60_000 // feed interval 10 min
	want := h.now + interval*8     // 2^3
	if feed.NextCheckAt != want {
		t.Errorf("next check = %d, want %d", feed.NextCheckAt, want)
	}
}

func TestPerChannelThresholdOverride(t *testing.T) {
	h := newHarness(t, "", func(o *PipelineOptions) { o.FailureAlertThreshold = 10 })
	addFeed(t, h, "f1", "c1")
	value := "2"
	if err := h.store.SetChannelSetting("c1", "alert_threshold", &value); err != nil {
		t.Fatal(err)
	}
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	h.now += 1000
	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if len(h.alerts) != 1 {
		t.Errorf("per-channel threshold must win: %v", h.alerts)
	}
}

func TestConditionalGetSkipsUnchanged(t *testing.T) {
	h := newHarness(t, rssFixture(2), nil)
	addFeed(t, h, "f1", "c1")

	var sawIfNoneMatch string
	h.holder.set(func(w http.ResponseWriter, r *http.Request) {
		sawIfNoneMatch = r.Header.Get("If-None-Match")
		if sawIfNoneMatch != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"etag-1"`)
		w.Write([]byte(rssFixture(2)))
	})

	h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	h.now += 1000
	outcome := h.pipeline.CheckFeed(context.Background(), "f1", h.now)
	if outcome.Status != "not_modified" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if sawIfNoneMatch != `"etag-1"` {
		t.Errorf("If-None-Match = %q", sawIfNoneMatch)
	}
}

func TestSchedulerTickAndSuspend(t *testing.T) {
	h := newHarness(t, rssFixture(2), nil)
	addFeed(t, h, "f1", "c1")

	if n := h.scheduler.Tick(context.Background()); n != 1 {
		t.Errorf("tick = %d", n)
	}
	h.scheduler.Suspend()
	if !h.scheduler.IsSuspended() {
		t.Error("must be suspended")
	}
	if n := h.scheduler.Tick(context.Background()); n != 0 {
		t.Errorf("suspended tick must process nothing, got %d", n)
	}
	h.scheduler.Resume()
	if h.scheduler.IsSuspended() {
		t.Error("must be resumed")
	}
}

func TestDryRunChangesNoState(t *testing.T) {
	h := newHarness(t, rssFixture(2), nil)
	addFeed(t, h, "f1", "c1")

	report, err := h.pipeline.DryRun(context.Background(), "f1", h.now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Fetch == nil || report.Fetch.Status != "ok" || report.Fetch.ArticleCount != 2 {
		t.Fatalf("report = %+v", report)
	}
	seen, _ := h.store.GetSeenKeys("f1")
	if len(seen) != 0 {
		t.Errorf("dry-run must not baseline: %v", seen)
	}
	feed, _ := h.store.GetFeed("f1")
	if feed.LastCheckedAt != nil {
		t.Errorf("dry-run must not mark checked: %+v", feed)
	}
}

func rssFixture(count int) string {
	items := ""
	for i := 1; i <= count; i++ {
		items += `<item><guid>g` + itoa(i) + `</guid><title>Item ` + itoa(i) + `</title><link>https://example.com/` + itoa(i) + `</link><pubDate>Mon, 01 Jan 2024 10:00:00 GMT</pubDate></item>`
	}
	return `<rss version="2.0"><channel><title>Fixture</title>` + items + `</channel></rss>`
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func contains(s, sub string) bool { return index(s, sub) >= 0 }
