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
