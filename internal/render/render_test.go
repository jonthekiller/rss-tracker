package render

import (
	"strings"
	"testing"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/feed"
)

func testContext() TemplateContext {
	pub := time.Date(2024, 3, 5, 18, 30, 0, 0, time.UTC)
	return TemplateContext{
		Article: feed.Article{
			Key:         "k",
			Link:        strPtr("https://example.com/a"),
			Title:       "Hello",
			Description: "<p>Some <b>html</b> &amp; text</p>",
			Summary:     "Some html & text",
			PublishedAt: &pub,
			Image:       strPtr("https://example.com/img.png"),
		},
		FeedName: "My Feed",
		Timezone: "UTC",
	}
}

func strPtr(s string) *string { return &s }

func TestRenderTemplatePlaceholders(t *testing.T) {
	got := RenderTemplate("**{title}**\n{link}\n{summary} — {author} / {feed.name}", testContext())
	if !strings.Contains(got, "**Hello**") || !strings.Contains(got, "https://example.com/a") {
		t.Errorf("render = %q", got)
	}
	if !strings.Contains(got, "My Feed") {
		t.Errorf("render = %q", got)
	}
}

func TestRenderTemplateStripsHTML(t *testing.T) {
	got := RenderTemplate("{description}", testContext())
	if got != "Some html & text" {
		t.Errorf("description render = %q", got)
	}
}

func TestRenderTemplateUnknownPlaceholderUntouched(t *testing.T) {
	got := RenderTemplate("{unknown} {TITLE}", testContext())
	if !strings.Contains(got, "{unknown}") {
		t.Errorf("unknown placeholder must stay: %q", got)
	}
	if !strings.Contains(got, "Hello") {
		t.Errorf("case-insensitive placeholder must work: %q", got)
	}
}

func TestRenderTemplateDate(t *testing.T) {
	got := RenderTemplate("{date}", testContext())
	if got != "5 mars 2024 à 18:30" {
		t.Errorf("date render = %q", got)
	}
}

func TestEnforceContentLimit(t *testing.T) {
	long := strings.Repeat("a", 2500)
	got := EnforceContentLimit(long)
	if runeLen(got) != LimitContent {
		t.Errorf("len = %d", runeLen(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("must end with ellipsis")
	}
}

func TestBuildArticlePayloadEmbed(t *testing.T) {
	embedJSON := `{"title":"{title}","description":"{summary}","color":"#5865F2","timestamp":"article","footerText":"{feed.name}","fields":[{"name":"Author","value":"{author}"}]}`
	payload := BuildArticlePayload(DefaultTemplate, &embedJSON, testContext())
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds = %d", len(payload.Embeds))
	}
	if payload.Embeds[0].Title != "Hello" || payload.Embeds[0].Footer.Text != "My Feed" {
		t.Errorf("embed = %+v", payload.Embeds[0])
	}
	if payload.Embeds[0].Color != 0x5865F2 {
		t.Errorf("color = %d", payload.Embeds[0].Color)
	}
	if payload.Embeds[0].Timestamp != "2024-03-05T18:30:00.000Z" {
		t.Errorf("timestamp = %q", payload.Embeds[0].Timestamp)
	}
}

func TestBuildArticlePayloadInvalidEmbedFallsBack(t *testing.T) {
	embedJSON := `{"title":`
	payload := BuildArticlePayload(DefaultTemplate, &embedJSON, testContext())
	if len(payload.Embeds) != 0 || payload.Content == "" {
		t.Errorf("invalid embed must fall back to text: %+v", payload)
	}
}

func TestBuildArticlePayloadEmptyTemplate(t *testing.T) {
	payload := BuildArticlePayload("{empty}", nil, testContext())
	if !strings.Contains(payload.Content, "**Hello**") {
		t.Errorf("{empty} must use the default template: %q", payload.Content)
	}
}

func TestBuildDigestSingleMessage(t *testing.T) {
	messages := BuildDigestMessages([]DigestItem{
		{Title: "One", Link: strPtr("https://a")}, {Title: "Two"},
	}, DigestOptions{FeedName: "F"})
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "1. [One](<https://a>)") ||
		!strings.Contains(messages[0].Content, "2. Two") {
		t.Errorf("content = %q", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "2 new articles") {
		t.Errorf("header = %q", messages[0].Content)
	}
}

func TestBuildDigestSplitAcrossMessages(t *testing.T) {
	items := []DigestItem{}
	for i := 0; i < 200; i++ {
		items = append(items, DigestItem{Title: strings.Repeat("x", 60), Link: strPtr("https://example.com/" + strings.Repeat("y", 40))})
	}
	messages := BuildDigestMessages(items, DigestOptions{FeedName: "F"})
	if len(messages) < 2 {
		t.Fatalf("expected split, got %d messages", len(messages))
	}
	for i, m := range messages {
		if runeLen(m.Content) > LimitContent {
			t.Errorf("message %d too long: %d", i, runeLen(m.Content))
		}
	}
	if !strings.Contains(messages[0].Content, "📰 **F**") {
		t.Errorf("header only on first message: %q", messages[0].Content[:40])
	}
	if strings.Contains(messages[1].Content, "📰 **F**") {
		t.Errorf("header must not repeat on later messages")
	}
}

func TestParseEmbedTemplateRejectsInvalid(t *testing.T) {
	invalid := "not json"
	if _, err := ParseEmbedTemplate(&invalid); err == nil {
		t.Error("invalid JSON must error")
	}
	if _, err := ParseEmbedTemplate(nil); err != nil {
		t.Errorf("nil embed is fine: %v", err)
	}
}

func enisaArticle(id, score string) feed.Article {
	return feed.Article{
		Key:         id,
		Title:       id,
		Link:        strPtr("https://euvd.enisa.europa.eu/vulnerability/" + id),
		Description: "A critical vulnerability in " + id,
		Summary:     "A critical vulnerability in " + id,
		Extra: map[string]string{
			"basescore": score,
			"epss":      "0.42",
			"assigner":  "Vendor " + id,
		},
	}
}

func TestBuildBurstEmbedMessagesSingleMessage(t *testing.T) {
	embedJSON := `{
		"title":"🚨 {title} — CVSS {basescore}",
		"description":"{summary}",
		"url":"{link}",
		"color":"#cc3333",
		"timestamp":"article",
		"fields":[
			{"name":"CVSS","value":"{basescore}","inline":true},
			{"name":"EPSS","value":"{epss}","inline":true},
			{"name":"Vendor","value":"{assigner}","inline":true}
		]
	}`
	articles := []feed.Article{
		enisaArticle("EUVD-1", "9.4"),
		enisaArticle("EUVD-2", "9.3"),
		enisaArticle("EUVD-3", "9.1"),
	}
	messages, ok := BuildBurstEmbedMessages(articles, &embedJSON, "ENISA", "UTC")
	if !ok {
		t.Fatal("expected embed burst")
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	if len(messages[0].Embeds) != 3 {
		t.Fatalf("embeds = %d", len(messages[0].Embeds))
	}
	if !strings.Contains(messages[0].Content, "3 new articles") {
		t.Errorf("header = %q", messages[0].Content)
	}
	if got := messages[0].Embeds[0].Title; got != "🚨 EUVD-1 — CVSS 9.4" {
		t.Errorf("embed title = %q", got)
	}
	fields := messages[0].Embeds[0].Fields
	if len(fields) != 3 || fields[0].Value != "9.4" || fields[1].Value != "0.42" || fields[2].Value != "Vendor EUVD-1" {
		t.Errorf("fields = %+v", fields)
	}
}

func TestBuildBurstEmbedMessagesSplitsAtTenEmbeds(t *testing.T) {
	embedJSON := `{"title":"{title}"}`
	articles := make([]feed.Article, 0, 12)
	for i := 1; i <= 12; i++ {
		articles = append(articles, enisaArticle("EUVD-"+itoa(i), "9.0"))
	}
	messages, ok := BuildBurstEmbedMessages(articles, &embedJSON, "ENISA", "UTC")
	if !ok {
		t.Fatal("expected embed burst")
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
	if len(messages[0].Embeds) != LimitEmbedsPerMessage {
		t.Errorf("first message embeds = %d", len(messages[0].Embeds))
	}
	if len(messages[1].Embeds) != 2 {
		t.Errorf("second message embeds = %d", len(messages[1].Embeds))
	}
	if !strings.Contains(messages[0].Content, "12 new articles") {
		t.Errorf("first header = %q", messages[0].Content)
	}
	if messages[1].Content != "" {
		t.Errorf("header must not repeat: %q", messages[1].Content)
	}
}

func TestBuildBurstEmbedMessagesSplitsAtTotalChars(t *testing.T) {
	embedJSON := `{"title":"{title}","description":"{description}"}`
	long := strings.Repeat("x", 3500)
	articles := []feed.Article{
		{Key: "a", Title: "A", Description: long},
		{Key: "b", Title: "B", Description: long},
		{Key: "c", Title: "C", Description: long},
	}
	messages, ok := BuildBurstEmbedMessages(articles, &embedJSON, "F", "UTC")
	if !ok {
		t.Fatal("expected embed burst")
	}
	if len(messages) < 2 {
		t.Fatalf("messages = %d, want split", len(messages))
	}
	for i, message := range messages {
		if len(message.Embeds) > LimitEmbedsPerMessage {
			t.Errorf("message %d has %d embeds", i, len(message.Embeds))
		}
		total := 0
		for _, embed := range message.Embeds {
			total += embedCharCount(embed)
		}
		if total > LimitEmbedTotal {
			t.Errorf("message %d chars = %d", i, total)
		}
	}
}

func TestBuildBurstEmbedMessagesClampsOversizedEmbed(t *testing.T) {
	fields := `[`
	for i := 0; i < 10; i++ {
		if i > 0 {
			fields += ","
		}
		fields += `{"name":"Field","value":"` + strings.Repeat("v", 300) + `"}`
	}
	fields += `]`
	embedJSON := `{"title":"{title}","description":"{description}","fields":` + fields + `}`
	article := feed.Article{
		Key: "a", Title: "Title", Description: strings.Repeat("d", 5000),
	}
	messages, ok := BuildBurstEmbedMessages([]feed.Article{article}, &embedJSON, "F", "UTC")
	if !ok {
		t.Fatal("expected embed burst")
	}
	if got := embedCharCount(messages[0].Embeds[0]); got > LimitEmbedTotal {
		t.Errorf("embed chars = %d, want <= %d", got, LimitEmbedTotal)
	}
}

func TestBuildBurstEmbedMessagesFallback(t *testing.T) {
	articles := []feed.Article{enisaArticle("EUVD-1", "9.4")}
	if _, ok := BuildBurstEmbedMessages(articles, nil, "F", "UTC"); ok {
		t.Error("nil embed must fall back")
	}
	invalid := `{"title":`
	if _, ok := BuildBurstEmbedMessages(articles, &invalid, "F", "UTC"); ok {
		t.Error("invalid embed must fall back")
	}
	empty := `{}`
	if _, ok := BuildBurstEmbedMessages(articles, &empty, "F", "UTC"); ok {
		t.Error("empty embed must fall back")
	}
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
