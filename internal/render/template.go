// Package render builds outgoing Discord payloads: text templates, JSON embed
// templates and digests. Behavior mirrors the TypeScript implementation,
// including Discord length limits.
package render

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/feed"
)

// TemplateContext carries everything needed to render a template.
type TemplateContext struct {
	Article  feed.Article
	FeedName string
	Timezone string
}

// DefaultTemplate is the default message template.
const DefaultTemplate = "**{title}**\n{link}"

// Limits are the Discord hard limits.
const (
	LimitContent          = 2000
	LimitEmbedDescription = 4096
	LimitEmbedTitle       = 256
	LimitEmbedFieldName   = 256
	LimitEmbedFieldValue  = 1024
	LimitEmbedFooter      = 2048
	LimitEmbedAuthor      = 256
	LimitEmbedFields      = 10
	LimitEmbedTotal       = 6000
)

var placeholderRe = regexp.MustCompile(`(?i)\{([a-z.]+)\}`)

// frenchMonths are used to replicate the fr-FR long date format
// ("14 octobre 2025 à 18:30") produced by Intl.DateTimeFormat in the TS build.
var frenchMonths = []string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

// FormatFrenchDate renders t in the given IANA timezone like
// Intl.DateTimeFormat("fr-FR", { dateStyle: "long", timeStyle: "short" }).
func FormatFrenchDate(t time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		slog.Warn("invalid timezone, falling back to UTC", "timezone", timezone, "err", err)
		loc = time.UTC
	}
	local := t.In(loc)
	return fmt.Sprintf(
		"%d %s %d à %02d:%02d",
		local.Day(), frenchMonths[int(local.Month())-1], local.Year(),
		local.Hour(), local.Minute(),
	)
}

// RenderTemplate replaces known placeholders in a user template. Unknown
// placeholders are left untouched.
func RenderTemplate(template string, context TemplateContext) string {
	article := context.Article
	description := feed.StripHTML(article.Description)
	link := ""
	if article.Link != nil {
		link = *article.Link
	}
	date := ""
	if article.PublishedAt != nil {
		date = FormatFrenchDate(*article.PublishedAt, context.Timezone)
	}
	author := ""
	if article.Author != nil {
		author = *article.Author
	}
	image := ""
	if article.Image != nil {
		image = *article.Image
	}

	replacements := map[string]string{
		"title":       article.Title,
		"link":        link,
		"description": description,
		"summary":     feed.Truncate(description, 300),
		"author":      author,
		"date":        date,
		"image":       image,
		"feed.name":   context.FeedName,
	}

	return placeholderRe.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(match, "{"), "}"))
		if value, ok := replacements[name]; ok {
			return value
		}
		return match
	})
}

// EnforceContentLimit truncates content to the Discord 2000-char limit.
func EnforceContentLimit(content string) string {
	return feed.Truncate(content, LimitContent)
}
