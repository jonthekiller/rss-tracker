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
