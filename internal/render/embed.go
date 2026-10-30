package render

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonthekiller/rss-tracker/internal/feed"
)

// timeNow is injectable for tests.
var timeNow = time.Now

// EmbedTemplate is the user-facing embed template (JSON stored per feed).
// Placeholders are the same as text templates. Timestamp may be "article"
// (article date), "now" (send time) or null/absent.
type EmbedTemplate struct {
	Title         *string              `json:"title"`
	Description   *string              `json:"description"`
	URL           *string              `json:"url"`
	Color         json.RawMessage      `json:"color"`
	FooterText    *string              `json:"footerText"`
	FooterIconURL *string              `json:"footerIconURL"`
	AuthorName    *string              `json:"authorName"`
	AuthorURL     *string              `json:"authorURL"`
	AuthorIconURL *string              `json:"authorIconURL"`
	ThumbnailURL  *string              `json:"thumbnailURL"`
	ImageURL      *string              `json:"imageURL"`
	Timestamp     *string              `json:"timestamp"`
	Fields        []EmbedTemplateField `json:"fields"`
}

// EmbedTemplateField is one field of the embed template.
type EmbedTemplateField struct {
	Name   *string `json:"name"`
	Value  *string `json:"value"`
	Inline *bool   `json:"inline"`
}

// ParseEmbedTemplate parses the stored JSON; nil when absent or invalid.
func ParseEmbedTemplate(jsonStr *string) (*EmbedTemplate, error) {
	if jsonStr == nil || strings.TrimSpace(*jsonStr) == "" {
		return nil, nil
	}
	var template EmbedTemplate
	if err := json.Unmarshal([]byte(*jsonStr), &template); err != nil {
		return nil, err
	}
	return &template, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RenderEmbed turns an embed template into a Discord embed, enforcing limits.
func RenderEmbed(template *EmbedTemplate, context TemplateContext) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}

	if title := deref(template.Title); title != "" {
		embed.Title = feed.Truncate(RenderTemplate(title, context), LimitEmbedTitle)
	}
	if description := deref(template.Description); description != "" {
		embed.Description = feed.Truncate(RenderTemplate(description, context), LimitEmbedDescription)
	}
	if url := deref(template.URL); url != "" {
		if rendered := RenderTemplate(url, context); IsSafeURL(rendered) {
			embed.URL = rendered
		}
	}

	if len(template.Color) > 0 && string(template.Color) != "null" {
		if color := normalizeColor(template.Color); color != nil {
			embed.Color = *color
		}
	}

	if footerText := deref(template.FooterText); footerText != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: feed.Truncate(RenderTemplate(footerText, context), LimitEmbedFooter),
		}
		if icon := deref(template.FooterIconURL); icon != "" {
			if rendered := RenderTemplate(icon, context); IsSafeURL(rendered) {
				embed.Footer.IconURL = rendered
			}
		}
	}

	if authorName := deref(template.AuthorName); authorName != "" {
		embed.Author = &discordgo.MessageEmbedAuthor{
			Name: feed.Truncate(RenderTemplate(authorName, context), LimitEmbedAuthor),
		}
		if u := deref(template.AuthorURL); u != "" {
			if rendered := RenderTemplate(u, context); IsSafeURL(rendered) {
				embed.Author.URL = rendered
			}
		}
		if icon := deref(template.AuthorIconURL); icon != "" {
			if rendered := RenderTemplate(icon, context); IsSafeURL(rendered) {
				embed.Author.IconURL = rendered
			}
		}
	}

	if thumbnail := deref(template.ThumbnailURL); thumbnail != "" {
		if rendered := RenderTemplate(thumbnail, context); IsSafeURL(rendered) {
			embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: rendered}
		}
	}
	if image := deref(template.ImageURL); image != "" {
		if rendered := RenderTemplate(image, context); IsSafeURL(rendered) {
			embed.Image = &discordgo.MessageEmbedImage{URL: rendered}
		}
	}

	if template.Timestamp != nil {
		switch *template.Timestamp {
		case "article":
			if context.Article.PublishedAt != nil {
				embed.Timestamp = context.Article.PublishedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")
			}
		case "now":
			embed.Timestamp = timeNow().UTC().Format("2006-01-02T15:04:05.000Z07:00")
		}
	}

	if len(template.Fields) > 0 {
		count := min(len(template.Fields), LimitEmbedFields)
		fields := make([]*discordgo.MessageEmbedField, 0, count)
		for _, field := range template.Fields[:count] {
			name := feed.Truncate(RenderTemplate(deref(field.Name), context), LimitEmbedFieldName)
			if name == "" {
				name = "\u200b"
			}
			value := feed.Truncate(RenderTemplate(deref(field.Value), context), LimitEmbedFieldValue)
			if value == "" {
				value = "\u200b"
			}
			inline := field.Inline != nil && *field.Inline
			fields = append(fields, &discordgo.MessageEmbedField{Name: name, Value: value, Inline: inline})
		}
		embed.Fields = fields
	}

	return embed
}

func normalizeColor(raw json.RawMessage) *int {
	var text strings.Builder
	if len(raw) > 0 {
		if raw[0] == '"' {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				text.WriteString(s)
			}
		} else {
			text.Write(raw)
		}
	}
	value := strings.TrimSpace(text.String())
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "\"") {
		return nil
	}
	if number, err := strconv.Atoi(value); err == nil {
		if number >= 0 && number <= 0xffffff {
			return &number
		}
		return nil
	}
	if strings.HasPrefix(value, "#") {
		value = value[1:]
	}
	if len(value) == 6 && isHex(value) {
		if number, err := strconv.ParseInt(value, 16, 32); err == nil {
			n := int(number)
			return &n
		}
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// IsSafeURL rejects non-http(s) URLs (javascript: etc.) to avoid embed errors.
func IsSafeURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// Payload is an outgoing message: text content plus optional embeds.
type Payload struct {
	Content string
	Embeds  []*discordgo.MessageEmbed
}

// BuildArticlePayload renders the outgoing payload for an article.
func BuildArticlePayload(templateText string, embedJSON *string, context TemplateContext) Payload {
	if templateText == "{empty}" {
		templateText = DefaultTemplate
	}
	content := EnforceContentLimit(RenderTemplate(templateText, context))

	embeds := []*discordgo.MessageEmbed{}
	if template, err := ParseEmbedTemplate(embedJSON); err == nil && template != nil {
		// Invalid embed template: fall back to text-only (never lose the article).
		embeds = append(embeds, RenderEmbed(template, context))
	}

	if content == "" && len(embeds) == 0 {
		link := ""
		if context.Article.Link != nil {
			link = *context.Article.Link
		}
		content = "Unable to render article: " + link
		if link == "" {
			content = "Unable to render article: " + context.Article.Key
		}
	}

	return Payload{Content: content, Embeds: embeds}
}

// embedCharCount returns the number of characters Discord counts for one
// embed (title, description, field names/values, footer and author names).
func embedCharCount(embed *discordgo.MessageEmbed) int {
	if embed == nil {
		return 0
	}
	count := len([]rune(embed.Title)) + len([]rune(embed.Description))
	if embed.Footer != nil {
		count += len([]rune(embed.Footer.Text))
	}
	if embed.Author != nil {
		count += len([]rune(embed.Author.Name))
	}
	for _, field := range embed.Fields {
		if field == nil {
			continue
		}
		count += len([]rune(field.Name)) + len([]rune(field.Value))
	}
	return count
}

// truncateTo is feed.Truncate with a clean empty result for non-positive
// limits (avoids leaving a lone ellipsis when an embed must shrink to zero).
func truncateTo(text string, max int) string {
	if max <= 0 {
		return ""
	}
	return feed.Truncate(text, max)
}

// clampEmbedTotal shrinks an embed until it fits Discord's 6000-character
// total. It drops the least important content first: description, then
// fields from the end, then footer/author, then title.
func clampEmbedTotal(embed *discordgo.MessageEmbed) {
	for embedCharCount(embed) > LimitEmbedTotal {
		excess := embedCharCount(embed) - LimitEmbedTotal
		switch {
		case embed.Description != "":
			embed.Description = truncateTo(embed.Description, len([]rune(embed.Description))-excess)
		case len(embed.Fields) > 0:
			embed.Fields = embed.Fields[:len(embed.Fields)-1]
		case embed.Footer != nil:
			embed.Footer = nil
		case embed.Author != nil:
			embed.Author = nil
		case embed.Title != "":
			embed.Title = truncateTo(embed.Title, len([]rune(embed.Title))-excess)
		default:
			return
		}
	}
}

// embedIsEmpty reports whether an embed carries no visible content. Discord
// rejects messages whose only embed is empty.
func embedIsEmpty(embed *discordgo.MessageEmbed) bool {
	if embed == nil {
		return true
	}
	return embed.Title == "" && embed.Description == "" && len(embed.Fields) == 0 &&
		embed.Footer == nil && embed.Author == nil && embed.Image == nil &&
		embed.Thumbnail == nil && embed.URL == "" && embed.Timestamp == ""
}

// BuildBurstEmbedMessages renders a burst as one or more messages containing
// one embed per article (instead of the text-only digest). It returns false
// when no usable embed template exists, so callers can fall back to the text
// digest. Discord limits are enforced: at most 10 embeds and 6000 embed
// characters per message; the header only appears on the first message.
func BuildBurstEmbedMessages(articles []feed.Article, embedJSON *string, feedName, timezone string) ([]Payload, bool) {
	if len(articles) == 0 {
		return nil, false
	}
	template, err := ParseEmbedTemplate(embedJSON)
	if err != nil || template == nil {
		return nil, false
	}

	embeds := make([]*discordgo.MessageEmbed, 0, len(articles))
	for _, article := range articles {
		embed := RenderEmbed(template, TemplateContext{
			Article: article, FeedName: feedName, Timezone: timezone,
		})
		clampEmbedTotal(embed)
		if embedIsEmpty(embed) {
			return nil, false
		}
		embeds = append(embeds, embed)
	}

	header := feed.Truncate(digestHeader(feedName, len(articles)), LimitContent)
	messages := make([]Payload, 0, (len(embeds)+LimitEmbedsPerMessage-1)/LimitEmbedsPerMessage)
	current := make([]*discordgo.MessageEmbed, 0, LimitEmbedsPerMessage)
	currentChars := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		content := ""
		if len(messages) == 0 {
			content = header
		}
		messages = append(messages, Payload{Content: content, Embeds: current})
		current = make([]*discordgo.MessageEmbed, 0, LimitEmbedsPerMessage)
		currentChars = 0
	}

	for _, embed := range embeds {
		chars := embedCharCount(embed)
		// Defensive: clampEmbedTotal should always make a single embed fit.
		if chars > LimitEmbedTotal {
			return nil, false
		}
		if len(current) == LimitEmbedsPerMessage ||
			(len(current) > 0 && currentChars+chars > LimitEmbedTotal) {
			flush()
		}
		current = append(current, embed)
		currentChars += chars
	}
	flush()
	return messages, true
}
