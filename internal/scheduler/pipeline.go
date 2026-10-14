// Package scheduler implements the feed check pipeline (fetch → detect →
// render → enqueue) and the periodic tick loop.
package scheduler

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/feed"
	"github.com/jonthekiller/rss-tracker/internal/render"
	"github.com/jonthekiller/rss-tracker/internal/sender"
	"github.com/jonthekiller/rss-tracker/internal/store"
)

// FailureAlertThreshold is the default consecutive-failure threshold.
const FailureAlertThreshold = 3

// MaxBackoffMs is the ceiling of the exponential failure backoff (6 h).
const MaxBackoffMs = 6 * 3600 * 1000

// PipelineOptions configures a Pipeline.
type PipelineOptions struct {
	Store  *store.FeedStore
	Sender *sender.SenderQueue
	// Timezone is the IANA zone for {date} rendering.
	Timezone           string
	MinIntervalMinutes int
	// Concurrency bounds parallel feed fetches (default 4).
	Concurrency int
	// MaxBackoffMs overrides the default 6 h backoff ceiling.
	MaxBackoffMs int64
	// FetchInject allows tests to stub HTTP behavior indirectly.
	FetchTimeout time.Duration
	// Now is an injectable clock (tests).
	Now func() int64
	// OnFeedFailed is called when a feed crosses the alert threshold.
	OnFeedFailed func(feed *store.Feed, lastError string)
	// OnFeedRecovered is called when a previously failing feed succeeds.
	OnFeedRecovered func(feed *store.Feed)
	// FailureAlertThreshold overrides the default threshold.
	FailureAlertThreshold int
}

// Pipeline renders and enqueues feed deliveries.
type Pipeline struct {
	options PipelineOptions
	log     *slog.Logger
}

// NewPipeline creates a Pipeline.
func NewPipeline(options PipelineOptions) *Pipeline {
	if options.Concurrency <= 0 {
		options.Concurrency = 4
	}
	return &Pipeline{options: options, log: slog.With("scope", "scheduler")}
}

func (p *Pipeline) now() int64 {
	if p.options.Now != nil {
		return p.options.Now()
	}
	return store.NowMillis()
}

// IntervalMs is the effective check interval for a feed.
func (p *Pipeline) IntervalMs(feed *store.Feed) int64 {
	minutes := int64(feed.IntervalMinutes)
	if int64(p.options.MinIntervalMinutes) > minutes {
		minutes = int64(p.options.MinIntervalMinutes)
	}
	return minutes * 60_000
}

// Baseline marks every current article as seen without sending anything.
func (p *Pipeline) Baseline(feed *store.Feed, articles []feed.Article, now int64) error {
	keys := make([]string, len(articles))
	timestamps := make([]*int64, len(articles))
	for i, a := range articles {
		keys[i] = a.Key
		if a.PublishedAt != nil {
			ms := a.PublishedAt.UnixMilli()
			timestamps[i] = &ms
		}
	}
	return p.options.Store.MarkSeen(feed.ID, keys, timestamps)
}

// CheckOutcome is the result of a single feed check.
type CheckOutcome struct {
	FeedID     string
	Status     string // "not_modified" | "delivered" | "empty" | "error"
	FreshCount int
	Error      string
}

// CheckFeed fetches a feed and delivers new articles.
func (p *Pipeline) CheckFeed(ctx context.Context, feedID string, now int64) CheckOutcome {
	st := p.options.Store
	feedRow, err := st.GetFeed(feedID)
	if err != nil || feedRow == nil {
		return CheckOutcome{FeedID: feedID, Status: "error", Error: "feed not found"}
	}

	wasFailing := feedRow.ConsecutiveFailures > 0

	result, fetchErr := feed.FetchFeed(ctx, feedRow.URL, feed.FetchOptions{
		ETag:         feedRow.ETag,
		LastModified: feedRow.LastModified,
		Timeout:      p.options.FetchTimeout,
	})
	if fetchErr != nil {
		message := fetchErr.Error()
		p.log.Warn("Feed check failed", "feed", feedRow.Name, "url", feedRow.URL, "err", message)
		if err := st.MarkFailure(feedID, now, message); err != nil {
			p.log.Error("MarkFailure failed", "err", err)
		}
		updated, _ := st.GetFeed(feedID)
		failures := 1
		if updated != nil {
			failures = updated.ConsecutiveFailures
		}
		backoff := int64(0)
		if p.options.MaxBackoffMs > 0 {
			backoff = p.options.MaxBackoffMs
		} else {
			backoff = int64(math.Min(float64(MaxBackoffMs), float64(p.IntervalMs(feedRow))*math.Pow(2, float64(failures))))
		}
		if err := st.SetNextCheck(feedID, now+backoff); err != nil {
			p.log.Error("SetNextCheck failed", "err", err)
		}
		// Per-channel threshold (/settings alert threshold) wins over the global one.
		threshold := p.options.FailureAlertThreshold
		if threshold == 0 {
			threshold = FailureAlertThreshold
		}
		if perChannelRaw, err := st.GetChannelSetting(feedRow.ChannelID, "alert_threshold"); err == nil && perChannelRaw != nil {
			if perChannel, parseErr := parseInt(*perChannelRaw); parseErr == nil {
				threshold = perChannel
			}
		}
		if failures >= threshold && (updated == nil || !updated.FailedNotified) {
			if p.options.OnFeedFailed != nil {
				p.options.OnFeedFailed(feedRow, message)
			}
		}
		return CheckOutcome{FeedID: feedID, Status: "error", Error: message}
	}

	if result.Status == feed.StatusNotModified {
		p.log.Debug("Not modified (304)", "feed", feedRow.Name)
		if err := st.MarkChecked(feedID, now, nil, nil); err != nil {
			p.log.Error("MarkChecked failed", "err", err)
		}
		if err := st.SetNextCheck(feedID, now+p.IntervalMs(feedRow)); err != nil {
			p.log.Error("SetNextCheck failed", "err", err)
		}
		if wasFailing && p.options.OnFeedRecovered != nil {
			p.options.OnFeedRecovered(feedRow)
		}
		return CheckOutcome{FeedID: feedID, Status: "not_modified"}
	}

	knownKeys, err := st.GetSeenKeys(feedID)
	if err != nil {
		return CheckOutcome{FeedID: feedID, Status: "error", Error: err.Error()}
	}
	filters, err := st.GetFilters(feedID)
	if err != nil {
		return CheckOutcome{FeedID: feedID, Status: "error", Error: err.Error()}
	}
	detection := feed.DetectNewArticles(feed.DetectionInput{
		Articles:   result.Articles,
		KnownKeys:  knownKeys,
		MaxAgeDays: feedRow.MaxAgeDays,
		Now:        now,
		Filters:    filters,
	})

	// A feed without prior seen-state (first check ever) is baselined
	// without delivering: never flood a channel on the very first fetch.
	seenCount, err := st.CountSeen(feedID)
	if err != nil {
		return CheckOutcome{FeedID: feedID, Status: "error", Error: err.Error()}
	}
	hasPriorState := seenCount > 0

	// Mark every article currently in the feed as seen (including blocked
	// and too old ones) so they are never reprocessed.
	if err := p.Baseline(feedRow, result.Articles, now); err != nil {
		return CheckOutcome{FeedID: feedID, Status: "error", Error: err.Error()}
	}

	if len(detection.Fresh) > 0 && hasPriorState {
		p.Deliver(feedRow, detection.Fresh)
	}

	if err := st.MarkChecked(feedID, now, result.ETag, result.LastModified); err != nil {
		p.log.Error("MarkChecked failed", "err", err)
	}
	if wasFailing && p.options.OnFeedRecovered != nil {
		p.options.OnFeedRecovered(feedRow)
	}
	if err := st.SetNextCheck(feedID, now+p.IntervalMs(feedRow)); err != nil {
		p.log.Error("SetNextCheck failed", "err", err)
	}
	if len(detection.Fresh) > 0 {
		p.log.Info("Feed checked with new articles",
			"feed", feedRow.Name,
			"fresh", len(detection.Fresh),
			"blocked", len(detection.Blocked),
			"skipped", len(detection.TooOld),
		)
	}
	status := "empty"
	if len(detection.Fresh) > 0 {
		status = "delivered"
	}
	return CheckOutcome{FeedID: feedID, Status: status, FreshCount: len(detection.Fresh)}
}

// DryRunReport is the /debug feed report (no state change).
type DryRunReport struct {
	Feed      DryRunFeed       `json:"feed"`
	State     DryRunState      `json:"state"`
	Fetch     *DryRunFetch     `json:"fetch,omitempty"`
	Detection *DryRunDetection `json:"detection,omitempty"`
	Delivery  *DryRunDelivery  `json:"delivery,omitempty"`
	Render    *DryRunRender    `json:"render,omitempty"`
}

type DryRunFeed struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	URL             string  `json:"url"`
	ChannelID       string  `json:"channelId"`
	Enabled         bool    `json:"enabled"`
	PauseReason     *string `json:"pauseReason"`
	IntervalMinutes int     `json:"intervalMinutes"`
	BatchMode       string  `json:"batchMode"`
	BatchThreshold  int     `json:"batchThreshold"`
	TemplateText    string  `json:"templateText"`
	EmbedJSON       *string `json:"embedJson"`
}

type DryRunState struct {
	SeenCount           int     `json:"seenCount"`
	NextCheckAt         int64   `json:"nextCheckAt"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	LastError           *string `json:"lastError"`
	QueueDepth          int     `json:"queueDepth"`
}

type DryRunFetch struct {
	Status               string  `json:"status"` // not_modified | ok | error
	ArticleCount         int     `json:"articleCount,omitempty"`
	FeedTitle            *string `json:"feedTitle,omitempty"`
	ETagReceived         *string `json:"etagReceived,omitempty"`
	LastModifiedReceived *string `json:"lastModifiedReceived,omitempty"`
	Detail               string  `json:"detail,omitempty"`
	Error                string  `json:"error,omitempty"`
}

type DryRunDetection struct {
	Fresh         int      `json:"fresh"`
	Blocked       int      `json:"blocked"`
	TooOld        int      `json:"tooOld"`
	FreshTitles   []string `json:"freshTitles"`
	BlockedTitles []string `json:"blockedTitles"`
}

type DryRunDelivery struct {
	Mode         string `json:"mode"`
	Messages     int    `json:"messages"`
	WouldSendNow bool   `json:"wouldSendNow"`
}

type DryRunRender struct {
	ArticleTitle string `json:"articleTitle"`
	Content      string `json:"content"`
	EmbedCount   int    `json:"embedCount"`
}

// DryRun fetches and analyses a feed WITHOUT changing any state.
func (p *Pipeline) DryRun(ctx context.Context, feedID string, now int64) (*DryRunReport, error) {
	st := p.options.Store
	feedRow, err := st.GetFeed(feedID)
	if err != nil {
		return nil, err
	}
	if feedRow == nil {
		return nil, errNotFound
	}

	seenCount, _ := st.CountSeen(feedID)
	queueDepth := p.options.Sender.Depth(feedRow.ChannelID)
	report := &DryRunReport{
		Feed: DryRunFeed{
			ID: feedRow.ID, Name: feedRow.Name, URL: feedRow.URL, ChannelID: feedRow.ChannelID,
			Enabled: feedRow.Enabled, PauseReason: feedRow.PauseReason,
			IntervalMinutes: feedRow.IntervalMinutes, BatchMode: string(feedRow.BatchMode),
			BatchThreshold: feedRow.BatchThreshold, TemplateText: feedRow.TemplateText,
			EmbedJSON: feedRow.EmbedJSON,
		},
		State: DryRunState{
			SeenCount: seenCount, NextCheckAt: feedRow.NextCheckAt,
			ConsecutiveFailures: feedRow.ConsecutiveFailures, LastError: feedRow.LastError,
			QueueDepth: queueDepth,
		},
	}

	result, fetchErr := feed.FetchFeed(ctx, feedRow.URL, feed.FetchOptions{
		ETag:         feedRow.ETag,
		LastModified: feedRow.LastModified,
		Timeout:      p.options.FetchTimeout,
	})
	if fetchErr != nil {
		report.Fetch = &DryRunFetch{Status: "error", Error: fetchErr.Error()}
		return report, nil
	}

	if result.Status == feed.StatusNotModified {
		report.Fetch = &DryRunFetch{Status: "not_modified", Detail: "HTTP 304 — the feed has not changed since the last check"}
		return report, nil
	}

	knownKeys, err := st.GetSeenKeys(feedID)
	if err != nil {
		report.Fetch = &DryRunFetch{Status: "error", Error: err.Error()}
		return report, nil
	}
	filters, err := st.GetFilters(feedID)
	if err != nil {
		report.Fetch = &DryRunFetch{Status: "error", Error: err.Error()}
		return report, nil
	}
	detection := feed.DetectNewArticles(feed.DetectionInput{
		Articles: result.Articles, KnownKeys: knownKeys, MaxAgeDays: feedRow.MaxAgeDays,
		Now: now, Filters: filters,
	})
	willDigest := feedRow.BatchMode == store.BatchDigest ||
		(feedRow.BatchMode == store.BatchAuto && len(detection.Fresh) >= feedRow.BatchThreshold)

	report.Fetch = &DryRunFetch{
		Status: "ok", ArticleCount: len(result.Articles), FeedTitle: result.Meta.Title,
		ETagReceived: result.ETag, LastModifiedReceived: result.LastModified,
	}
	freshTitles := make([]string, 0)
	for _, a := range lastN(detection.Fresh, 5) {
		freshTitles = append(freshTitles, a.Title)
	}
	blockedTitles := make([]string, 0)
	for _, a := range firstN(detection.Blocked, 5) {
		blockedTitles = append(blockedTitles, a.Title)
	}
	report.Detection = &DryRunDetection{
		Fresh: len(detection.Fresh), Blocked: len(detection.Blocked), TooOld: len(detection.TooOld),
		FreshTitles: freshTitles, BlockedTitles: blockedTitles,
	}
	report.Delivery = &DryRunDelivery{
		Mode:         map[bool]string{true: "digest", false: "individual"}[willDigest],
		Messages:     map[bool]int{true: 1, false: len(detection.Fresh)}[willDigest],
		WouldSendNow: feedRow.Enabled && len(detection.Fresh) > 0 && seenCount > 0,
	}

	// Render preview of the latest fresh article (or the latest article).
	var preview *feed.Article
	if len(detection.Fresh) > 0 {
		preview = &detection.Fresh[len(detection.Fresh)-1]
	} else if len(result.Articles) > 0 {
		preview = &result.Articles[len(result.Articles)-1]
	}
	if preview != nil {
		payload := render.BuildArticlePayload(feedRow.TemplateText, feedRow.EmbedJSON, render.TemplateContext{
			Article: *preview, FeedName: feedRow.Name, Timezone: p.options.Timezone,
		})
		report.Render = &DryRunRender{
			ArticleTitle: preview.Title, Content: payload.Content, EmbedCount: len(payload.Embeds),
		}
	}
	return report, nil
}

// Deliver renders and enqueues delivery of fresh articles (batch-aware).
func (p *Pipeline) Deliver(feedRow *store.Feed, fresh []feed.Article) {
	useDigest := feedRow.BatchMode == store.BatchDigest ||
		(feedRow.BatchMode == store.BatchAuto && len(fresh) >= feedRow.BatchThreshold)

	if useDigest {
		items := make([]render.DigestItem, 0, len(fresh))
		for _, a := range fresh {
			items = append(items, render.DigestItem{Title: a.Title, Link: a.Link})
		}
		messages := render.BuildDigestMessages(items, render.DigestOptions{
			FeedName: feedRow.Name, Timezone: p.options.Timezone,
		})
		for _, message := range messages {
			p.options.Sender.Send(feedRow.ChannelID, sender.OutboundMessage{
				Content: message.Content, Embeds: message.Embeds,
			})
		}
		p.log.Info("Digest enqueued for burst",
			"feed", feedRow.Name, "channel", feedRow.ChannelID,
			"fresh", len(fresh), "messages", len(messages))
		return
	}

	for _, article := range fresh {
		payload := render.BuildArticlePayload(feedRow.TemplateText, feedRow.EmbedJSON, render.TemplateContext{
			Article: article, FeedName: feedRow.Name, Timezone: p.options.Timezone,
		})
		p.options.Sender.Send(feedRow.ChannelID, sender.OutboundMessage{
			Content: payload.Content, Embeds: payload.Embeds,
		})
	}
	p.log.Info("Individual articles enqueued",
		"feed", feedRow.Name, "channel", feedRow.ChannelID, "fresh", len(fresh))
}

var errNotFound = errorString("feed not found")

type errorString string

func (e errorString) Error() string { return string(e) }

func parseInt(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errorString("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func lastN(articles []feed.Article, n int) []feed.Article {
	if len(articles) <= n {
		return articles
	}
	return articles[len(articles)-n:]
}

func firstN(articles []feed.Article, n int) []feed.Article {
	if len(articles) <= n {
		return articles
	}
	return articles[:n]
}
