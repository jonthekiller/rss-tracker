package feed

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

const (
	userAgent = "RSS-Tracker (+https://github.com/jonthekiller/rss-tracker)"
	// DefaultTimeout bounds a single feed fetch.
	DefaultTimeout = 15 * time.Second
	// MaxBytes caps the response body size (10 MB).
	MaxBytes = 10 * 1024 * 1024
)

// FetchOptions carries conditional-GET state and injectables (tests).
type FetchOptions struct {
	ETag         *string
	LastModified *string
	Timeout      time.Duration
	HTTPClient   *http.Client
	Now          func() time.Time
}

// HashKey is the SHA-1 hex of a string.
func HashKey(input string) string {
	sum := sha1.Sum([]byte(input))
	return hex.EncodeToString(sum[:])
}

// ArticleKey computes the stable article key: guid || link || sha1(title+pubdate).
func ArticleKey(guid, link, title string, publishedAt *time.Time) string {
	if guid != "" {
		return guid
	}
	if link != "" {
		return link
	}
	pubISO := ""
	if publishedAt != nil {
		pubISO = publishedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
	return HashKey(title + "|" + pubISO)
}

var (
	styleRe   = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`)
	scriptRe  = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`)
	brRe      = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockRe   = regexp.MustCompile(`(?i)</(p|div|li|h[1-6])>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	wsRe      = regexp.MustCompile(`[ \t]+`)
	multiNlRe = regexp.MustCompile(`\n{3,}`)
)

// StripHTML removes tags and collapses whitespace, like the TS stripHtml.
func StripHTML(html string) string {
	text := styleRe.ReplaceAllString(html, " ")
	text = scriptRe.ReplaceAllString(text, " ")
	text = brRe.ReplaceAllString(text, "\n")
	text = blockRe.ReplaceAllString(text, "\n")
	text = tagRe.ReplaceAllString(text, " ")
	text = DecodeEntities(text)
	text = wsRe.ReplaceAllString(text, " ")
	text = multiNlRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

var entityRe = regexp.MustCompile(`&#(0?39|x[0-9a-fA-F]+|\d+);|&([a-z]+);`)

var namedEntities = map[string]string{
	"nbsp": " ", "amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"hellip": "…", "mdash": "—", "ndash": "–",
}

// DecodeEntities resolves the entity subset the TS version handled. XML
// parsers already resolve standard entities, so this mostly matters for
// entities inside CDATA/HTML content.
func DecodeEntities(text string) string {
	return entityRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(match, "&"), ";")
		if strings.HasPrefix(inner, "#") {
			digits := strings.TrimPrefix(inner, "#")
			if strings.HasPrefix(digits, "x") || strings.HasPrefix(digits, "X") {
				if code, err := strconv.ParseInt(digits[1:], 16, 32); err == nil {
					return string(rune(code))
				}
				return match
			}
			if code, err := strconv.Atoi(strings.TrimPrefix(digits, "0")); err == nil {
				return string(rune(code))
			}
			return match
		}
		if replacement, ok := namedEntities[inner]; ok {
			return replacement
		}
		return match
	})
}

// Truncate shortens text to maxLength, ending with an ellipsis.
func Truncate(text string, maxLength int) string {
	runes := []rune(text)
	if len(runes) <= maxLength {
		return text
	}
	cut := runes[:max(0, maxLength-1)]
	return strings.TrimRight(string(cut), " \t\n") + "…"
}

var imageSrcRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)

// ExtractFirstImage returns the src of the first <img> in the HTML.
func ExtractFirstImage(html string) *string {
	match := imageSrcRe.FindStringSubmatch(html)
	if match == nil {
		return nil
	}
	return &match[1]
}

func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func firstAuthor(authors []*gofeed.Person) *string {
	for _, a := range authors {
		if a != nil && strings.TrimSpace(a.Name) != "" {
			return &a.Name
		}
	}
	return nil
}

// normalize converts a gofeed item into an Article, mirroring the TS
// normalizeEntries per-format field selection.
func normalize(item *gofeed.Item) Article {
	// Per-format description: RSS content:encoded ?? description;
	// Atom content ?? summary; JSON content_html ?? content_text.
	// gofeed exposes all of these through Content/Description.
	description := item.Content
	if description == "" {
		description = item.Description
	}

	var author *string
	if a := firstAuthor(item.Authors); a != nil {
		author = a
	} else if item.Author != nil && item.Author.Name != "" {
		author = textPtr(item.Author.Name)
	} else if item.DublinCoreExt != nil && len(item.DublinCoreExt.Creator) > 0 {
		author = textPtr(item.DublinCoreExt.Creator[0])
	}

	var published *time.Time
	if item.PublishedParsed != nil {
		t := *item.PublishedParsed
		published = &t
	} else if item.UpdatedParsed != nil {
		t := *item.UpdatedParsed
		published = &t
	}

	var image *string
	if item.Image != nil && item.Image.URL != "" {
		image = textPtr(item.Image.URL)
	} else if enc := enclosureImage(item); enc != nil {
		image = enc
	} else {
		image = ExtractFirstImage(description)
	}

	title := strings.TrimSpace(item.Title)
	guid := ""
	if item.GUID != "" {
		guid = item.GUID
	}
	link := item.Link

	summary := Truncate(StripHTML(description), 300)

	return Article{
		Key:         ArticleKey(guid, link, title, published),
		GUID:        textPtr(guid),
		Link:        textPtr(link),
		Title:       title,
		Description: description,
		Summary:     summary,
		Author:      author,
		PublishedAt: published,
		Image:       image,
	}
}

func enclosureImage(item *gofeed.Item) *string {
	for _, enc := range item.Enclosures {
		if enc != nil && enc.Type != "" && strings.HasPrefix(enc.Type, "image/") && enc.URL != "" {
			return textPtr(enc.URL)
		}
	}
	return nil
}

// FetchFeed fetches a feed with conditional GET support. It returns a
// not-modified result on HTTP 304 and a *FetchError on failure.
func FetchFeed(ctx context.Context, url string, options FetchOptions) (*FetchFeedResult, error) {
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &FetchError{Message: "Failed to reach feed: " + err.Error()}
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept",
		"application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	if options.ETag != nil {
		req.Header.Set("If-None-Match", *options.ETag)
	}
	if options.LastModified != nil {
		req.Header.Set("If-Modified-Since", *options.LastModified)
	}

	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &FetchError{Message: "Failed to reach feed: " + err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &FetchFeedResult{Status: StatusNotModified}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &FetchError{
			Message:    fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
			StatusCode: resp.StatusCode,
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, &FetchError{Message: "Failed to read feed: " + err.Error()}
	}
	if len(body) > MaxBytes {
		return nil, &FetchError{Message: fmt.Sprintf("Feed too large (%d bytes)", len(body))}
	}

	parser := newParser()
	parsed, err := parser.ParseString(string(body))
	if err != nil {
		return nil, &FetchError{Message: "Malformed feed: " + err.Error()}
	}

	articles := make([]Article, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		articles = append(articles, normalize(item))
	}

	meta := FeedMeta{}
	if parsed.Title != "" {
		meta.Title = &parsed.Title
	}

	return &FetchFeedResult{
		Status:       StatusOK,
		Articles:     articles,
		Meta:         meta,
		ETag:         headerPtr(resp.Header.Get("ETag")),
		LastModified: headerPtr(resp.Header.Get("Last-Modified")),
	}, nil
}

func headerPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// newParser is the shared gofeed parser used by FetchFeed and tests.
func newParser() *gofeed.Parser { return gofeed.NewParser() }
